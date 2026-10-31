package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func resumeUnknown(code string) error { return &FeishuArchiveFailure{Code: code} }

func (engine *BelloBuddyResumeEngine) advance(ctx context.Context, task *model.BelloBuddyResumeArchive, stage string, values map[string]any) error {
	if err := engine.guardLease(ctx, task.ID); err != nil {
		return err
	}
	if values == nil {
		values = map[string]any{}
	}
	values["stage"] = stage
	values["attempt_id"] = ""
	values["updated_at"] = time.Now().Unix()
	if err := engine.DB.Model(task).Updates(values).Error; err != nil {
		return err
	}
	return engine.DB.First(task, task.ID).Error
}

// 写前持久登记尝试；成功输出先 fsync 私有回执，随后才提交数据库阶段结果。
func (engine *BelloBuddyResumeEngine) write(ctx context.Context, task *model.BelloBuddyResumeArchive, operation string, jobMap *model.BelloBuddyResumeJobMap, call func() (map[string]any, error)) (map[string]any, error) {
	if err := engine.guardLease(ctx, task.ID); err != nil {
		return nil, err
	}
	if task.AttemptID != "" {
		return engine.readReceipt(task.AttemptID)
	}
	attempt := model.BelloBuddyResumeAttempt{ID: uuid.NewString(), ArchiveID: task.ID, Operation: operation, State: "started", StartedAt: time.Now().Unix()}
	err := engine.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&attempt).Error; err != nil {
			return err
		}
		if err := tx.Model(task).Updates(map[string]any{"attempt_id": attempt.ID, "stage": operation}).Error; err != nil {
			return err
		}
		if jobMap != nil {
			return tx.Model(jobMap).Update("attempt_id", attempt.ID).Error
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	task.AttemptID = attempt.ID
	task.Stage = operation
	data, err := call()
	if err != nil {
		engine.DB.Model(&attempt).Updates(map[string]any{"state": "unknown", "error_code": resumeFailureCode(err), "finished_at": time.Now().Unix()})
		return nil, err
	}
	// 只保留恢复所需 ID 及操作成功事实，原始供应商响应不进入普通日志或库。
	receipt := map[string]any{"operation": operation, "confirmed": true}
	if id := resumeCreatedRecordID(data); id != "" {
		receipt["record_id"] = id
	}
	encoded, err := common.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(engine.Config.DataDir, "receipts", attempt.ID+".json")
	file, err := createFeishuPrivateFile(path)
	if err != nil {
		return nil, err
	}
	_, err = file.Write(encoded)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if err := syncFeishuCreatedFile(path); err != nil {
		return nil, err
	}
	if err := engine.guardLease(ctx, task.ID); err != nil {
		return nil, err
	}
	if err := engine.DB.Model(&attempt).Updates(map[string]any{"state": "confirmed", "receipt": string(encoded), "finished_at": time.Now().Unix()}).Error; err != nil {
		return nil, err
	}
	return receipt, nil
}

func (engine *BelloBuddyResumeEngine) readReceipt(id string) (map[string]any, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, resumeUnknown("ARCHIVE_RECEIPT_INVALID")
	}
	var attempt model.BelloBuddyResumeAttempt
	if err := engine.DB.First(&attempt, "id = ?", id).Error; err != nil {
		return nil, err
	}
	data := []byte(attempt.Receipt)
	if len(data) == 0 {
		var err error
		data, err = os.ReadFile(filepath.Join(engine.Config.DataDir, "receipts", id+".json"))
		if err != nil {
			return nil, resumeUnknown("EXTERNAL_WRITE_RESULT_UNKNOWN")
		}
	}
	var receipt map[string]any
	if common.Unmarshal(data, &receipt) != nil || receipt["operation"] != attempt.Operation || receipt["confirmed"] != true {
		return nil, resumeUnknown("ARCHIVE_RECEIPT_INVALID")
	}
	return receipt, nil
}

func resumeCreatedRecordID(data map[string]any) string {
	for _, name := range []string{"record_id", "id"} {
		if id, ok := data[name].(string); ok && strings.HasPrefix(id, "rec") {
			return id
		}
	}
	if record, ok := data["record"].(map[string]any); ok {
		return resumeCreatedRecordID(record)
	}
	if ids, ok := data["record_id_list"].([]any); ok && len(ids) == 1 {
		id, _ := ids[0].(string)
		if strings.HasPrefix(id, "rec") {
			return id
		}
	}
	return ""
}

func (engine *BelloBuddyResumeEngine) confirmAttempt(task *model.BelloBuddyResumeArchive) error {
	if task.AttemptID == "" {
		return nil
	}
	return engine.DB.Model(&model.BelloBuddyResumeAttempt{}).Where("id = ?", task.AttemptID).Updates(map[string]any{"state": "verified", "finished_at": time.Now().Unix()}).Error
}

func feishuCellEquals(actual, expected any) bool {
	if reflect.DeepEqual(actual, expected) {
		return true
	}
	if items, ok := expected.([]map[string]any); ok {
		values, ok := actual.([]any)
		if !ok || len(values) != len(items) {
			return false
		}
		for i, item := range items {
			value, ok := values[i].(map[string]any)
			if !ok || value["id"] != item["id"] {
				return false
			}
		}
		return true
	}
	if want, ok := expected.([]string); ok {
		values, ok := actual.([]any)
		if !ok || len(values) != len(want) {
			return false
		}
		for i, value := range values {
			if value != want[i] {
				return false
			}
		}
		return true
	}
	return false
}

func resumeFieldsMatch(record *FeishuArchiveRecord, fields map[string]any) bool {
	for key, value := range fields {
		if !feishuCellEquals(record.Fields[key], value) {
			return false
		}
	}
	return true
}

func resumeAttachmentList(record *FeishuArchiveRecord, field string) ([]FeishuArchiveAttachment, error) {
	value := record.Fields[field]
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, resumeUnknown("FEISHU_ATTACHMENT_RESPONSE_INVALID")
	}
	attachments := make([]FeishuArchiveAttachment, 0, len(items))
	for _, item := range items {
		value, ok := item.(map[string]any)
		if !ok {
			return nil, resumeUnknown("FEISHU_ATTACHMENT_RESPONSE_INVALID")
		}
		token, _ := value["file_token"].(string)
		name, _ := value["name"].(string)
		size, _ := value["size"].(float64)
		if token == "" {
			return nil, resumeUnknown("FEISHU_ATTACHMENT_RESPONSE_INVALID")
		}
		attachments = append(attachments, FeishuArchiveAttachment{Token: token, Name: name, Size: int64(size)})
	}
	return attachments, nil
}

func (engine *BelloBuddyResumeEngine) process(ctx context.Context, task *model.BelloBuddyResumeArchive) error {
	var scope model.BelloBuddyResumeScope
	var mapping model.BelloBuddyResumeJobMap
	if err := engine.DB.First(&scope, task.ScopeID).Error; err != nil {
		return err
	}
	if err := engine.DB.First(&mapping, scope.JobMapID).Error; err != nil {
		return err
	}
	target := engine.Config.Target
	encoded, _ := common.Marshal(target)
	if scope.Target != string(encoded) {
		return resumeUnknown("TARGET_CONFIG_CHANGED")
	}
	var account model.BelloBuddyAccount
	var job model.BelloBuddyJob
	if err := engine.DB.First(&account, scope.AccountID).Error; err != nil {
		return err
	}
	if err := engine.DB.First(&job, scope.JobID).Error; err != nil {
		return err
	}
	_, _, user, err := engine.ownership(scope.UserID, account.AccountKey, job.JobID)
	if err != nil {
		return err
	}
	if err := RequireBelloBuddyTyping(job.ID, scope.ScopeKey); err != nil {
		return err
	}
	var predecessors int64
	if err := engine.DB.Model(&model.BelloBuddyResumeArchive{}).Where("scope_id = ? AND id < ? AND state NOT IN ?", scope.ID, task.ID, []string{"archived", "superseded", "abandoned"}).Count(&predecessors).Error; err != nil {
		return err
	}
	if predecessors > 0 {
		return resumeUnknown("EARLIER_VERSION_UNRESOLVED")
	}
	// HR 普通文本直接写完整登录 username，不用昵称、飞书用户 ID 或选项映射。
	hr, err := engine.Backend.Field(ctx, target.JobTable, target.Fields["hr"])
	if err != nil {
		return err
	}
	if hr.Type != "text" {
		return resumeUnknown("HR_FIELD_NOT_TEXT")
	}
	for _, entry := range []struct{ Key, Type string }{{"job_name", "text"}, {"resume", "attachment"}, {"job_link", "link"}, {"name", "text"}, {"owner", "text"}, {"account", "text"}, {"account_id", "text"}, {"platform", "text"}, {"candidate_id", "text"}, {"archive_key", "text"}, {"downloaded_at", "datetime"}, {"archived_at", "datetime"}} {
		table := target.TalentTable
		if entry.Key == "job_name" {
			table = target.JobTable
		}
		field, err := engine.Backend.Field(ctx, table, target.Fields[entry.Key])
		if err != nil {
			return err
		}
		if field.Type != entry.Type {
			return resumeUnknown("FEISHU_SCHEMA_CHANGED")
		}
	}
	jobFields := map[string]any{target.Fields["job_name"]: job.Name, target.Fields["hr"]: user.Username}
	if mapping.RecordID == "" && mapping.AttemptID == "" {
		if _, err := engine.reuseJobMapping(ctx, &account, &job, user, &mapping, nil); err != nil {
			return err
		}
	}
	if mapping.RecordID == "" {
		if mapping.AttemptID != "" {
			receipt, err := engine.readReceipt(mapping.AttemptID)
			if err != nil {
				return resumeUnknown("JOB_CREATE_RESULT_UNKNOWN")
			}
			mapping.RecordID = resumeCreatedRecordID(receipt)
		} else {
			createFields := map[string]any{target.Fields["job_name"]: job.Name, target.Fields["hr"]: user.Username}
			if engine.Config.JobSync.Enabled {
				for key, value := range map[string]string{"account": account.DisplayName, "account_id": jobPlatformAccountIdentity(user.Id, &account), "platform": "BOSS直聘", "job_id": job.JobID} {
					field := engine.Config.JobSync.Fields[key]
					if !strings.HasPrefix(field, "fld") {
						return resumeUnknown("JOB_SYNC_CONFIG_INVALID")
					}
					schema, err := engine.Backend.Field(ctx, target.JobTable, field)
					if err != nil {
						return err
					}
					if schema.Type != "text" {
						return resumeUnknown("FEISHU_SCHEMA_CHANGED")
					}
					createFields[field] = value
				}
			}
			receipt, err := engine.write(ctx, task, "job_create", &mapping, func() (map[string]any, error) { return engine.Backend.Write(ctx, target.JobTable, "", createFields) })
			if err != nil {
				return err
			}
			mapping.RecordID = resumeCreatedRecordID(receipt)
		}
		if mapping.RecordID == "" {
			return resumeUnknown("JOB_CREATE_RESULT_UNKNOWN")
		}
		record, err := engine.Backend.Get(ctx, target.JobTable, mapping.RecordID)
		if err != nil {
			return err
		}
		if !resumeFieldsMatch(record, jobFields) {
			return resumeUnknown("JOB_RECORD_CHANGED")
		}
		if err := engine.DB.Model(&mapping).Update("record_id", mapping.RecordID).Error; err != nil {
			return err
		}
		if err := engine.confirmAttempt(task); err != nil {
			return err
		}
	} else {
		record, err := engine.Backend.Get(ctx, target.JobTable, mapping.RecordID)
		if err != nil {
			return err
		}
		if mapping.ManagedFields != "" {
			var managed map[string]any
			if common.UnmarshalJsonStr(mapping.ManagedFields, &managed) != nil {
				return resumeUnknown("JOB_RECORD_EVIDENCE_INVALID")
			}
			for _, id := range []string{target.Fields["job_name"], target.Fields["hr"]} {
				if value, ok := managed[id]; ok {
					jobFields[id] = value
				}
			}
		}
		if !resumeFieldsMatch(record, jobFields) {
			return resumeUnknown("JOB_RECORD_CHANGED")
		}
	}
	if task.Stage == "accepted" || task.Stage == "job_create" {
		if err := engine.advance(ctx, task, "job_ready", nil); err != nil {
			return err
		}
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	fields := map[string]any{target.Fields["archive_key"]: scope.ScopeKey, target.Fields["owner"]: user.Username, target.Fields["account"]: account.DisplayName, target.Fields["account_id"]: account.AccountKey, target.Fields["platform"]: account.Platform, target.Fields["candidate_id"]: scope.CandidateID, target.Fields["job_link"]: []map[string]any{{"id": mapping.RecordID}}, target.Fields["downloaded_at"]: time.UnixMilli(task.DownloadedAt).In(loc).Format("2006-01-02 15:04:05")}
	if scope.RecordID == "" {
		records, err := engine.Backend.Search(ctx, target.TalentTable, target.Fields["archive_key"], scope.ScopeKey)
		if err != nil {
			return err
		}
		if len(records) > 1 {
			return resumeUnknown("FEISHU_ARCHIVE_KEY_CONFLICT")
		}
		if len(records) == 0 {
			if task.AttemptID != "" {
				return resumeUnknown("TALENT_CREATE_RESULT_UNKNOWN")
			}
			createFields := make(map[string]any, len(fields)+1)
			for key, value := range fields {
				createFields[key] = value
			}
			if task.CandidateName != "" {
				createFields[target.Fields["name"]] = task.CandidateName
			}
			receipt, callErr := engine.write(ctx, task, "talent_create", nil, func() (map[string]any, error) { return engine.Backend.Write(ctx, target.TalentTable, "", createFields) })
			if callErr == nil {
				scope.RecordID = resumeCreatedRecordID(receipt)
			}
			if scope.RecordID == "" {
				records, err = engine.Backend.Search(ctx, target.TalentTable, target.Fields["archive_key"], scope.ScopeKey)
				if err != nil {
					return err
				}
				if len(records) != 1 {
					if callErr != nil {
						return callErr
					}
					return resumeUnknown("TALENT_CREATE_RESULT_UNKNOWN")
				}
				scope.RecordID = records[0].ID
			}
		} else {
			scope.RecordID = records[0].ID
		}
		record, err := engine.Backend.Get(ctx, target.TalentTable, scope.RecordID)
		if err != nil {
			return err
		}
		if !resumeFieldsMatch(record, fields) {
			return resumeUnknown("TALENT_RECORD_EVIDENCE_CONFLICT")
		}
		if err := engine.DB.Model(&scope).Update("record_id", scope.RecordID).Error; err != nil {
			return err
		}
		if err := engine.confirmAttempt(task); err != nil {
			return err
		}
	}
	if task.Stage == "job_ready" || task.Stage == "talent_create" {
		if err := engine.advance(ctx, task, "talent_ready", nil); err != nil {
			return err
		}
	}
	record, err := engine.Backend.Get(ctx, target.TalentTable, scope.RecordID)
	if err != nil {
		return err
	}
	identityFields := map[string]any{target.Fields["archive_key"]: scope.ScopeKey, target.Fields["candidate_id"]: scope.CandidateID, target.Fields["job_link"]: fields[target.Fields["job_link"]]}
	if !resumeFieldsMatch(record, identityFields) {
		return resumeUnknown("TALENT_RECORD_CHANGED")
	}
	if task.Stage == "talent_ready" || task.Stage == "metadata_update" {
		if task.CandidateName != "" && (record.Fields[target.Fields["name"]] == nil || record.Fields[target.Fields["name"]] == "") {
			fields[target.Fields["name"]] = task.CandidateName
		}
		if !resumeFieldsMatch(record, fields) {
			if task.AttemptID != "" {
				return resumeUnknown("METADATA_RESULT_UNKNOWN_OR_CHANGED")
			}
			_, callErr := engine.write(ctx, task, "metadata_update", nil, func() (map[string]any, error) {
				return engine.Backend.Write(ctx, target.TalentTable, scope.RecordID, fields)
			})
			record, err = engine.Backend.Get(ctx, target.TalentTable, scope.RecordID)
			if err != nil {
				return err
			}
			if !resumeFieldsMatch(record, fields) {
				if callErr != nil {
					return callErr
				}
				return resumeUnknown("METADATA_VERIFY_FAILED")
			}
		}
		if err := engine.confirmAttempt(task); err != nil {
			return err
		}
		if err := engine.advance(ctx, task, "metadata_ready", nil); err != nil {
			return err
		}
	}
	if task.Stage == "metadata_ready" || task.Stage == "attachment_upload" {
		record, err = engine.Backend.Get(ctx, target.TalentTable, scope.RecordID)
		if err != nil {
			return err
		}
		attachments, err := resumeAttachmentList(record, target.Fields["resume"])
		if err != nil {
			return err
		}
		matches := []FeishuArchiveAttachment{}
		for _, item := range attachments {
			if item.Name == task.StorageName && (item.Size == 0 || item.Size == task.Size) {
				matches = append(matches, item)
			}
		}
		if len(matches) == 0 {
			if task.AttemptID != "" {
				return resumeUnknown("ATTACHMENT_UPLOAD_RESULT_UNKNOWN")
			}
			path := filepath.Join(engine.Config.DataDir, "files", task.StorageName)
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() || info.Size() != task.Size || task.StorageName != task.ArchiveID+".pdf" {
				return resumeUnknown("ARCHIVE_FILE_MISSING_OR_CHANGED")
			}
			file, err := os.ReadFile(path)
			if err != nil || model.BelloBuddyIdentityHash(string(file)) != task.SHA256 {
				return resumeUnknown("ARCHIVE_FILE_MISSING_OR_CHANGED")
			}
			_, callErr := engine.write(ctx, task, "attachment_upload", nil, func() (map[string]any, error) { return engine.Backend.Upload(ctx, scope.RecordID, task.StorageName) })
			record, err = engine.Backend.Get(ctx, target.TalentTable, scope.RecordID)
			if err != nil {
				return err
			}
			attachments, err = resumeAttachmentList(record, target.Fields["resume"])
			if err != nil {
				return err
			}
			for _, item := range attachments {
				if item.Name == task.StorageName && (item.Size == 0 || item.Size == task.Size) {
					matches = append(matches, item)
				}
			}
			if len(matches) == 0 {
				if callErr != nil {
					return callErr
				}
				return resumeUnknown("ATTACHMENT_VERIFY_FAILED")
			}
		}
		if len(matches) != 1 {
			return resumeUnknown("ATTACHMENT_EVIDENCE_CONFLICT")
		}
		hash, err := engine.Backend.DownloadHash(ctx, scope.RecordID, matches[0].Token)
		if err != nil {
			return err
		}
		if hash != task.SHA256 {
			return resumeUnknown("ATTACHMENT_HASH_MISMATCH")
		}
		if err := engine.confirmAttempt(task); err != nil {
			return err
		}
		if err := engine.advance(ctx, task, "attachment_ready", map[string]any{"file_token": matches[0].Token, "verified_at": time.Now().Unix()}); err != nil {
			return err
		}
	}
	var older []model.BelloBuddyResumeArchive
	if err := engine.DB.Where("scope_id = ? AND id < ? AND file_token <> ?", scope.ID, task.ID, "").Find(&older).Error; err != nil {
		return err
	}
	if task.Stage == "attachment_ready" || task.Stage == "attachment_remove" {
		record, err = engine.Backend.Get(ctx, target.TalentTable, scope.RecordID)
		if err != nil {
			return err
		}
		attachments, err := resumeAttachmentList(record, target.Fields["resume"])
		if err != nil {
			return err
		}
		present := []string{}
		for _, item := range attachments {
			present = append(present, item.Token)
		}
		if !slices.Contains(present, task.FileToken) {
			return resumeUnknown("NEW_ATTACHMENT_MISSING")
		}
		remove := []string{}
		for _, old := range older {
			if old.FileToken != task.FileToken && slices.Contains(present, old.FileToken) {
				remove = append(remove, old.FileToken)
			}
		}
		if len(remove) > 0 {
			if task.AttemptID != "" {
				return resumeUnknown("OLD_ATTACHMENT_REMOVE_RESULT_UNKNOWN")
			}
			_, callErr := engine.write(ctx, task, "attachment_remove", nil, func() (map[string]any, error) { return engine.Backend.Remove(ctx, scope.RecordID, remove) })
			record, err = engine.Backend.Get(ctx, target.TalentTable, scope.RecordID)
			if err != nil {
				return err
			}
			attachments, err = resumeAttachmentList(record, target.Fields["resume"])
			if err != nil {
				return err
			}
			present = nil
			for _, item := range attachments {
				present = append(present, item.Token)
			}
			if !slices.Contains(present, task.FileToken) {
				return resumeUnknown("NEW_ATTACHMENT_MISSING")
			}
			for _, token := range remove {
				if slices.Contains(present, token) {
					if callErr != nil {
						return callErr
					}
					return resumeUnknown("OLD_ATTACHMENT_REMOVE_VERIFY_FAILED")
				}
			}
		}
		if err := engine.confirmAttempt(task); err != nil {
			return err
		}
		if err := engine.advance(ctx, task, "replacement_ready", nil); err != nil {
			return err
		}
	}
	if task.Stage == "replacement_ready" || task.Stage == "time_update" {
		// 固定归档时间以便响应丢失后比较；不重新生成时间覆盖不明结果。
		if task.ArchiveTime == 0 {
			task.ArchiveTime = time.Now().Unix()
			if err := engine.DB.Model(task).Update("archive_time", task.ArchiveTime).Error; err != nil {
				return err
			}
		}
		dateFields := map[string]any{target.Fields["archived_at"]: time.Unix(task.ArchiveTime, 0).In(loc).Format("2006-01-02 15:04:05")}
		record, err = engine.Backend.Get(ctx, target.TalentTable, scope.RecordID)
		if err != nil {
			return err
		}
		if !resumeFieldsMatch(record, dateFields) {
			if task.AttemptID != "" {
				return resumeUnknown("ARCHIVE_TIME_RESULT_UNKNOWN")
			}
			_, callErr := engine.write(ctx, task, "time_update", nil, func() (map[string]any, error) {
				return engine.Backend.Write(ctx, target.TalentTable, scope.RecordID, dateFields)
			})
			record, err = engine.Backend.Get(ctx, target.TalentTable, scope.RecordID)
			if err != nil {
				return err
			}
			if !resumeFieldsMatch(record, dateFields) {
				if callErr != nil {
					return callErr
				}
				return resumeUnknown("ARCHIVE_TIME_VERIFY_FAILED")
			}
		}
		if !resumeFieldsMatch(record, identityFields) {
			return resumeUnknown("TALENT_RECORD_CHANGED")
		}
		if err := engine.confirmAttempt(task); err != nil {
			return err
		}
		if err := engine.guardLease(ctx, task.ID); err != nil {
			return err
		}
		return engine.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(task).Updates(map[string]any{"state": "archived", "stage": "completed", "completed_at": time.Now().Unix(), "attempt_id": "", "error_code": "", "updated_at": time.Now().Unix()}).Error; err != nil {
				return err
			}
			return tx.Model(&model.BelloBuddyResumeArchive{}).Where("scope_id = ? AND id < ? AND state = ?", scope.ID, task.ID, "archived").Update("state", "superseded").Error
		})
	}
	return fmt.Errorf("unsupported resume stage %s", task.Stage)
}

// 显式管理员放弃只清理本任务私有副本；不清飞书对象，不推断不明写入失败。
func (engine *BelloBuddyResumeEngine) Abandon(actorID int, id string) error {
	var task model.BelloBuddyResumeArchive
	if err := engine.DB.Where("archive_id = ?", id).First(&task).Error; err != nil {
		return ErrBelloBuddyNotFound
	}
	if task.State != "blocked" && task.State != "abandoned" {
		return &BelloBuddyError{409, "ARCHIVE_NOT_BLOCKED", "仅可明确放弃受阻任务"}
	}
	if task.StorageName != task.ArchiveID+".pdf" {
		return resumeUnknown("ARCHIVE_FILE_INVALID")
	}
	if task.State == "blocked" {
		result := engine.DB.Model(&task).Where("id = ? AND state = ?", task.ID, "blocked").Updates(map[string]any{"state": "abandoned", "abandoned_by": actorID, "updated_at": time.Now().Unix()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrBelloBuddyConflict
		}
	}
	err := os.Remove(filepath.Join(engine.Config.DataDir, "files", task.StorageName))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return engine.DB.Model(&task).Update("purged_at", time.Now().Unix()).Error
}
