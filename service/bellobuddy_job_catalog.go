package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BelloBuddyCatalogJob struct {
	JobID         string  `json:"job_id"`
	Name          string  `json:"name"`
	City          *string `json:"city,omitempty"`
	Online        *bool   `json:"online"`
	Description   *string `json:"boss_description,omitempty"`
	SourceWarning string  `json:"source_warning,omitempty"`
}
type BelloBuddyCatalogInput struct {
	ClientSyncID      string                 `json:"client_sync_id"`
	PlatformAccountID string                 `json:"platform_account_id,omitempty"`
	ObservedAt        int64                  `json:"observed_at"`
	Trigger           string                 `json:"trigger"`
	DisplayName       string                 `json:"display_name"`
	Jobs              []BelloBuddyCatalogJob `json:"jobs"`
}
type BelloBuddyCatalogReceipt struct {
	ClientSyncID string                       `json:"client_sync_id"`
	ReceivedAt   int64                        `json:"received_at"`
	Jobs         []model.BelloBuddyJobCatalog `json:"jobs"`
}

func resumeJobMapKey(target FeishuArchiveTarget, userID, accountID, jobID int) string {
	data, _ := common.Marshal(target)
	return model.BelloBuddyIdentityHash(fmt.Sprintf("%s:%d:%d:%d", data, userID, accountID, jobID))
}

func jobPlatformAccountIdentity(userID int, account *model.BelloBuddyAccount) string {
	if account.PlatformAccountID == "" {
		return account.AccountKey
	}
	return fmt.Sprintf("dpin:%d:boss:%s", userID, account.PlatformAccountID)
}

var jobPlatformAccountIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

func validJobPlatformAccountID(value string) bool {
	return jobPlatformAccountIDPattern.MatchString(value) && !strings.HasPrefix(value, "acc_")
}

// 本地账号槽位和回执保持各自归属；只在持有 base 租约的 worker 中复用已核验的飞书行。
// 旧映射已有行时不改指向，避免迁移既有人才关联；没有稳定身份的历史行不能靠名称认领。
func (engine *BelloBuddyResumeEngine) jobIdentityMappings(account *model.BelloBuddyAccount, job *model.BelloBuddyJob, userID int) ([]model.BelloBuddyResumeJobMap, map[string]bool, error) {
	accounts := []model.BelloBuddyAccount{*account}
	if account.PlatformAccountID != "" {
		if err := engine.DB.Where("user_id = ? AND platform = ? AND platform_account_id = ?", userID, "boss", account.PlatformAccountID).Find(&accounts).Error; err != nil {
			return nil, nil, err
		}
	}
	accountIDs := make([]int, 0, len(accounts))
	accountKeys := make(map[string]bool, len(accounts))
	for _, alias := range accounts {
		accountIDs = append(accountIDs, alias.ID)
		accountKeys[alias.AccountKey] = true
	}
	var jobs []model.BelloBuddyJob
	if err := engine.DB.Where("account_id IN ? AND job_hash = ?", accountIDs, model.BelloBuddyIdentityHash(job.JobID)).Find(&jobs).Error; err != nil {
		return nil, nil, err
	}
	keys := make([]string, 0, len(jobs))
	for _, alias := range jobs {
		keys = append(keys, resumeJobMapKey(engine.Config.Target, userID, alias.AccountID, alias.ID))
	}
	var mappings []model.BelloBuddyResumeJobMap
	if err := engine.DB.Where("map_key IN ?", keys).Find(&mappings).Error; err != nil {
		return nil, nil, err
	}
	return mappings, accountKeys, nil
}

func (engine *BelloBuddyResumeEngine) shareVerifiedJobFields(account *model.BelloBuddyAccount, job *model.BelloBuddyJob, userID int, mapping *model.BelloBuddyResumeJobMap) error {
	mappings, _, err := engine.jobIdentityMappings(account, job, userID)
	if err != nil {
		return err
	}
	ids := []int{}
	for _, alias := range mappings {
		if alias.RecordID == mapping.RecordID {
			ids = append(ids, alias.ID)
		}
	}
	return engine.DB.Model(&model.BelloBuddyResumeJobMap{}).Where("id IN ? AND record_id = ?", ids, mapping.RecordID).Update("managed_fields", mapping.ManagedFields).Error
}

func (engine *BelloBuddyResumeEngine) reuseJobMapping(ctx context.Context, account *model.BelloBuddyAccount, job *model.BelloBuddyJob, user *model.User, mapping *model.BelloBuddyResumeJobMap, observation *model.BelloBuddyJobCatalog) (bool, error) {
	if !engine.Config.JobSync.Enabled {
		return false, nil
	}
	mappings, accountKeys, err := engine.jobIdentityMappings(account, job, user.Id)
	if err != nil {
		return false, err
	}
	mapIDs := make([]int, 0, len(mappings))
	for _, alias := range mappings {
		mapIDs = append(mapIDs, alias.ID)
	}
	var catalogs []model.BelloBuddyJobCatalog
	if err := engine.DB.Where("job_map_id IN ?", mapIDs).Find(&catalogs).Error; err != nil {
		return false, err
	}
	var newer *model.BelloBuddyJobCatalog
	for _, catalog := range catalogs {
		if observation != nil && (catalog.ObservedAt > observation.ObservedAt || catalog.ObservedAt == observation.ObservedAt && catalog.ID > observation.ID) {
			if newer == nil || catalog.ObservedAt > newer.ObservedAt || catalog.ObservedAt == newer.ObservedAt && catalog.ID > newer.ID {
				newer = &catalog
			}
		}
		if catalog.AttemptID != "" {
			return false, resumeUnknown("JOB_UPDATE_RESULT_UNKNOWN_OR_CHANGED")
		}
	}
	var newerMapping *model.BelloBuddyResumeJobMap
	if newer != nil {
		for _, alias := range mappings {
			if alias.ID == newer.JobMapID && alias.RecordID != "" && alias.ManagedFields != "" {
				newerMapping = &alias
			}
		}
		if newer.State != "synced" || newerMapping == nil {
			return false, resumeUnknown("JOB_NEWER_SOURCE_PENDING")
		}
		record, err := engine.Backend.Get(ctx, engine.Config.Target.JobTable, newerMapping.RecordID)
		if err != nil {
			return false, err
		}
		var verified map[string]any
		if common.UnmarshalJsonStr(newerMapping.ManagedFields, &verified) != nil || !resumeFieldsMatch(record, verified) {
			return false, resumeUnknown("JOB_NEWER_SOURCE_PENDING")
		}
	}
	if mapping.RecordID != "" {
		if newerMapping != nil && newerMapping.RecordID != mapping.RecordID {
			return false, resumeUnknown("JOB_IDENTITY_AMBIGUOUS")
		}
		return newer != nil, nil
	}
	known := map[string]model.BelloBuddyResumeJobMap{}
	for _, alias := range mappings {
		if alias.RecordID == "" && alias.AttemptID != "" {
			return false, resumeUnknown("JOB_CREATE_RESULT_UNKNOWN")
		}
		if alias.RecordID != "" {
			known[alias.RecordID] = alias
		}
	}
	if len(known) > 1 {
		return false, resumeUnknown("JOB_IDENTITY_AMBIGUOUS")
	}
	target := engine.Config.Target
	var selected *FeishuArchiveRecord
	for id := range known {
		record, err := engine.Backend.Get(ctx, target.JobTable, id)
		if err != nil {
			return false, err
		}
		// 数据库已证明旧槽位归属；允许旧简历路径创建的行尚未补齐职位来源字段。
		if record.Fields[target.Fields["hr"]] != user.Username {
			return false, resumeUnknown("JOB_IDENTITY_AMBIGUOUS")
		}
		if value := record.Fields[engine.Config.JobSync.Fields["job_id"]]; value != nil && value != "" && value != job.JobID {
			return false, resumeUnknown("JOB_IDENTITY_AMBIGUOUS")
		}
		if value, ok := record.Fields[engine.Config.JobSync.Fields["account_id"]].(string); ok && value != "" && !accountKeys[value] && value != jobPlatformAccountIdentity(user.Id, account) {
			return false, resumeUnknown("JOB_IDENTITY_AMBIGUOUS")
		}
		selected = record
	}
	if selected == nil {
		records, err := engine.Backend.SearchJobs(ctx, target.JobTable, user.Username, job.JobID)
		if err != nil {
			return false, err
		}
		for _, record := range records {
			if record.Fields[target.Fields["hr"]] != user.Username || record.Fields[engine.Config.JobSync.Fields["platform"]] != "BOSS直聘" || record.Fields[engine.Config.JobSync.Fields["job_id"]] != job.JobID {
				return false, resumeUnknown("JOB_IDENTITY_AMBIGUOUS")
			}
			remoteAccount, _ := record.Fields[engine.Config.JobSync.Fields["account_id"]].(string)
			// 仅凭旧槽位字符串不能恢复 DPin 用户归属；旧行必须先由数据库映射证明。
			matched := account.PlatformAccountID != "" && remoteAccount == jobPlatformAccountIdentity(user.Id, account)
			if !matched {
				parts := strings.SplitN(remoteAccount, ":", 4)
				ownerID := 0
				if len(parts) == 4 && parts[0] == "dpin" && parts[2] == "boss" && validJobPlatformAccountID(parts[3]) {
					ownerID, _ = strconv.Atoi(parts[1])
				}
				if ownerID > 0 && (ownerID != user.Id || account.PlatformAccountID != "") {
					continue // 明确属于另一真实招聘账号，不合并。
				}
				return false, resumeUnknown("JOB_IDENTITY_AMBIGUOUS")
			}
			if selected != nil && selected.ID != record.ID {
				return false, resumeUnknown("JOB_IDENTITY_AMBIGUOUS")
			}
			selected = &record
		}
	}
	if selected == nil {
		return false, nil
	}
	managed, err := common.Marshal(selected.Fields)
	if err != nil {
		return false, err
	}
	if err := engine.DB.Model(mapping).Updates(map[string]any{"record_id": selected.ID, "managed_fields": string(managed)}).Error; err != nil {
		return false, err
	}
	mapping.RecordID, mapping.ManagedFields = selected.ID, string(managed)
	return newer != nil, nil
}
func (engine *BelloBuddyResumeEngine) CatalogReceipt(userID int, accountKey, clientID string) (*BelloBuddyCatalogReceipt, error) {
	if ValidateBelloBuddyIdentifier(accountKey) != nil || ValidateBelloBuddyIdentifier(clientID) != nil {
		return nil, ErrBelloBuddyNotFound
	}
	var batch model.BelloBuddyJobSyncBatch
	identity, _ := common.Marshal([]any{userID, accountKey, clientID})
	key := model.BelloBuddyIdentityHash(string(identity))
	if err := engine.DB.Where("sync_key = ? AND user_id = ?", key, userID).First(&batch).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrBelloBuddyNotFound
	} else if err != nil {
		return nil, err
	}
	var ids []int
	if err := common.UnmarshalJsonStr(batch.CatalogIDs, &ids); err != nil {
		return nil, err
	}
	result := &BelloBuddyCatalogReceipt{ClientSyncID: batch.ClientSyncID, ReceivedAt: batch.ReceivedAt, Jobs: []model.BelloBuddyJobCatalog{}}
	if len(ids) > 0 {
		if err := engine.DB.Where("id IN ? AND user_id = ? AND account_id = ?", ids, userID, batch.AccountID).Order("id").Find(&result.Jobs).Error; err != nil {
			return nil, err
		}
	}
	return result, nil
}
func (engine *BelloBuddyResumeEngine) AcceptJobCatalog(userID int, accountKey string, input BelloBuddyCatalogInput) (*BelloBuddyCatalogReceipt, error) {
	if !engine.Config.JobSync.Enabled {
		return nil, &BelloBuddyError{503, "JOB_SYNC_DISABLED", "职位同步尚未配置"}
	}
	if ValidateBelloBuddyIdentifier(accountKey) != nil || ValidateBelloBuddyIdentifier(input.ClientSyncID) != nil || input.ObservedAt <= 0 || input.ObservedAt > time.Now().UnixMilli()+300000 || !slices.Contains([]string{"login", "list", "refresh"}, input.Trigger) || utf8.RuneCountInString(input.DisplayName) > 128 || len(input.Jobs) > BelloBuddyMaxJobs {
		return nil, belloInvalid("职位同步来源、编号或数量无效")
	}
	if input.PlatformAccountID != "" && !validJobPlatformAccountID(input.PlatformAccountID) {
		return nil, belloInvalid("招聘平台账号编号无效")
	}
	seen := map[string]bool{}
	for _, job := range input.Jobs {
		encodedJob, err := common.Marshal(job)
		if err != nil || len(encodedJob) > 60<<10 {
			return nil, belloInvalid("职位资料编码后超过三数据库共同存储上限")
		}
		if ValidateBelloBuddyIdentifier(job.JobID) != nil || seen[job.JobID] || strings.TrimSpace(job.Name) == "" || utf8.RuneCountInString(job.Name) > 128 || strings.ContainsRune(job.Name, 0) || job.Online == nil || job.City != nil && (utf8.RuneCountInString(*job.City) > 128 || strings.ContainsRune(*job.City, 0)) || job.Description != nil && (len(*job.Description) > 48<<10 || strings.ContainsRune(*job.Description, 0)) {
			return nil, belloInvalid("职位必须包含可信的稳定ID、名称和明确状态；JD不能超限")
		}
		seen[job.JobID] = true
		if job.SourceWarning != "" && job.SourceWarning != "BOSS_MANAGEMENT_UNVERIFIED" {
			return nil, belloInvalid("职位来源状态无效")
		}
	}
	payload, _ := common.Marshal(input)
	hash := model.BelloBuddyIdentityHash(string(payload))
	identity, _ := common.Marshal([]any{userID, accountKey, input.ClientSyncID})
	key := model.BelloBuddyIdentityHash(string(identity))
	target, _ := common.Marshal([]any{engine.Config.Target, engine.Config.JobSync.Fields})
	// 不以列表缺席推定下线；只更新实际观察到且具有明确状态的职位。
	err := model.WithBelloBuddyAccount(userID, accountKey, true, func(tx *gorm.DB, account *model.BelloBuddyAccount) error {
		if input.PlatformAccountID != "" {
			if account.PlatformAccountID != "" && account.PlatformAccountID != input.PlatformAccountID {
				return &BelloBuddyError{409, "JOB_IDENTITY_CONFLICT", "此账号槽位已绑定其他 BOSS 账号，请核对账号后重新同步"}
			}
			if account.PlatformAccountID == "" {
				if err := tx.Model(account).Update("platform_account_id", input.PlatformAccountID).Error; err != nil {
					return err
				}
				account.PlatformAccountID = input.PlatformAccountID
			}
		}
		var old model.BelloBuddyJobSyncBatch
		if err := tx.Where("sync_key = ?", key).First(&old).Error; err == nil {
			if old.PayloadHash != hash {
				return &BelloBuddyError{409, "JOB_SYNC_ID_CONFLICT", "同一同步编号的内容不一致"}
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var user model.User
		if err := tx.Select("id", "status").First(&user, userID).Error; err != nil || user.Status != common.UserStatusEnabled {
			return ErrBelloBuddyNotFound
		}
		ids := []int{}
		for _, source := range input.Jobs {
			// 先查最新观察，旧请求不能把职位重新改回旧版本。
			var job model.BelloBuddyJob
			err := tx.Where("account_id = ? AND job_hash = ?", account.ID, model.BelloBuddyIdentityHash(source.JobID)).First(&job).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			var catalog model.BelloBuddyJobCatalog
			if err == nil {
				if err := tx.Where("user_id = ? AND account_id = ? AND job_id = ?", userID, account.ID, job.ID).First(&catalog).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
			}
			if catalog.ID != 0 && input.ObservedAt <= catalog.ObservedAt {
				ids = append(ids, catalog.ID)
				continue
			}
			city := job.City
			if source.City != nil {
				city = *source.City
			}
			metadata := BelloBuddySyncInput{DisplayName: input.DisplayName, Jobs: []BelloBuddyJobInput{{JobID: source.JobID, Name: source.Name, City: city, Online: source.Online}}}
			if err := syncBelloBuddyJobMetadata(tx, account, metadata); err != nil {
				return err
			}
			if err := tx.Where("account_id = ? AND job_hash = ?", account.ID, model.BelloBuddyIdentityHash(source.JobID)).First(&job).Error; err != nil {
				return err
			}
			mapping := model.BelloBuddyResumeJobMap{MapKey: resumeJobMapKey(engine.Config.Target, userID, account.ID, job.ID)}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&mapping).Error; err != nil {
				return err
			}
			if err := tx.Where("map_key = ?", mapping.MapKey).First(&mapping).Error; err != nil {
				return err
			}
			desired, _ := common.Marshal(source)
			warning := ""
			if source.Description == nil {
				warning = "BOSS_JD_UNAVAILABLE"
			}
			if source.SourceWarning != "" {
				if warning != "" {
					warning += ","
				}
				warning += source.SourceWarning
			}
			if catalog.ID == 0 {
				catalog = model.BelloBuddyJobCatalog{JobMapID: mapping.ID, UserID: userID, AccountID: account.ID, JobID: job.ID, PlatformJobID: job.JobID, Target: string(target), Desired: string(desired), ObservedAt: input.ObservedAt, Revision: 1, State: "queued", Warning: warning}
				if err := tx.Create(&catalog).Error; err != nil {
					return err
				}
			} else {
				// 不抹掉不明的外部尝试。worker先核对该尝试，再应用新期望值。
				if err := tx.Model(&catalog).Updates(map[string]any{"desired": string(desired), "observed_at": input.ObservedAt, "revision": catalog.Revision + 1, "state": "queued", "error_code": "", "warning": warning}).Error; err != nil {
					return err
				}
			}
			ids = append(ids, catalog.ID)
		}
		encoded, _ := common.Marshal(ids)
		return tx.Create(&model.BelloBuddyJobSyncBatch{SyncKey: key, ClientSyncID: input.ClientSyncID, UserID: userID, AccountID: account.ID, PayloadHash: hash, CatalogIDs: string(encoded), ReceivedAt: time.Now().UnixMilli()}).Error
	})
	if err != nil {
		return nil, err
	}
	return engine.CatalogReceipt(userID, accountKey, input.ClientSyncID)
}

// 与简历worker使用同一个base租约；任何未明创建先核对，不另开一条写入链。
func (engine *BelloBuddyResumeEngine) guardJobLease(ctx context.Context, id int) error {
	identity, ok := ctx.Value(resumeLeaseContextKey{}).(resumeLeaseIdentity)
	if !ok {
		return resumeUnknown("ARCHIVE_LEASE_MISSING")
	}
	var count int64
	if err := engine.DB.Model(&model.BelloBuddyResumeLease{}).Where("id = ? AND owner = ? AND busy_job_sync_id = ? AND busy_archive_id = 0 AND expires_at > ?", identity.Key, identity.Owner, id, time.Now().Unix()).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return resumeUnknown("ARCHIVE_LEASE_LOST")
	}
	return nil
}
func (engine *BelloBuddyResumeEngine) runJobCatalog(ctx context.Context, lease *model.BelloBuddyResumeLease, keepBusy *bool) error {
	var task model.BelloBuddyJobCatalog
	query := engine.DB.Where("state IN ?", []string{"queued", "processing"})
	if lease.BusyJobSyncID != 0 {
		query = engine.DB.Where("id = ?", lease.BusyJobSyncID)
	}
	if err := query.Order("observed_at DESC").Order("id DESC").First(&task).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if task.State == "blocked" {
		return nil
	}
	if err := engine.DB.Model(lease).Where("id = ? AND owner = ?", lease.ID, lease.Owner).Update("busy_job_sync_id", task.ID).Error; err != nil {
		return err
	}
	*keepBusy = true
	ctx = context.WithValue(ctx, resumeLeaseContextKey{}, resumeLeaseIdentity{Key: lease.ID, Owner: lease.Owner})
	if err := engine.guardJobLease(ctx, task.ID); err != nil {
		return err
	}
	if err := engine.DB.Model(&task).Where("revision = ?", task.Revision).Update("state", "processing").Error; err != nil {
		return err
	}
	err := engine.processJobCatalog(ctx, &task)
	if err == nil {
		// 到达这里必有读回。较新的期望值已由接收事务置queued，不得覆盖。
		if err = engine.DB.Model(&task).Where("revision = ?", task.Revision).Updates(map[string]any{"state": "synced", "error_code": "", "synced_at": time.Now().UnixMilli()}).Error; err != nil {
			return err
		}
		*keepBusy = false
		return nil
	}
	if engine.guardJobLease(ctx, task.ID) != nil {
		return err
	}
	if update := engine.DB.Model(&task).Where("revision = ?", task.Revision).Updates(map[string]any{"state": "blocked", "error_code": resumeFailureCode(err)}).Error; update != nil {
		return update
	}
	// 没有任何外部写入不明，允许其他任务正常执行。
	var pending int64
	if engine.DB.Model(&model.BelloBuddyResumeAttempt{}).Where("job_sync_id = ? AND state IN ?", task.ID, []string{"started", "unknown"}).Count(&pending).Error == nil && pending == 0 {
		*keepBusy = false
	}
	return err
}
func (engine *BelloBuddyResumeEngine) writeJobCatalog(ctx context.Context, task *model.BelloBuddyJobCatalog, mapping *model.BelloBuddyResumeJobMap, fields map[string]any) error {
	if err := engine.guardJobLease(ctx, task.ID); err != nil {
		return err
	}
	operation := "catalog_update"
	if mapping.RecordID == "" {
		operation = "catalog_create"
	}
	attempt := model.BelloBuddyResumeAttempt{ID: uuid.NewString(), JobSyncID: task.ID, Operation: operation, State: "started", StartedAt: time.Now().Unix()}
	encoded, _ := common.Marshal(fields)
	if err := engine.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&attempt).Error; err != nil {
			return err
		}
		if err := tx.Model(task).Updates(map[string]any{"attempt_id": attempt.ID, "attempt_fields": string(encoded)}).Error; err != nil {
			return err
		}
		if mapping.RecordID == "" {
			return tx.Model(mapping).Update("attempt_id", attempt.ID).Error
		}
		return nil
	}); err != nil {
		return err
	}
	task.AttemptID = attempt.ID
	task.AttemptFields = string(encoded)
	data, err := engine.Backend.Write(ctx, engine.Config.Target.JobTable, mapping.RecordID, fields)
	if err != nil {
		engine.DB.Model(&attempt).Updates(map[string]any{"state": "unknown", "error_code": resumeFailureCode(err)})
		return err
	}
	receipt, _ := common.Marshal(map[string]any{"operation": operation, "confirmed": true, "record_id": resumeCreatedRecordID(data)})
	filename := filepath.Join(engine.Config.DataDir, "receipts", attempt.ID+".json")
	file, err := createFeishuPrivateFile(filename)
	if err != nil {
		return err
	}
	_, err = file.Write(receipt)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := syncFeishuCreatedFile(filename); err != nil {
		return err
	}
	if err := engine.guardJobLease(ctx, task.ID); err != nil {
		return err
	}
	return engine.DB.Model(&attempt).Updates(map[string]any{"state": "confirmed", "receipt": string(receipt), "finished_at": time.Now().Unix()}).Error
}
func (engine *BelloBuddyResumeEngine) processJobCatalog(ctx context.Context, task *model.BelloBuddyJobCatalog) error {
	target := engine.Config.Target
	encoded, _ := common.Marshal([]any{target, engine.Config.JobSync.Fields})
	if !engine.Config.JobSync.Enabled || task.Target != string(encoded) {
		return resumeUnknown("TARGET_CONFIG_CHANGED")
	}
	var account model.BelloBuddyAccount
	if err := engine.DB.First(&account, task.AccountID).Error; err != nil {
		return err
	}
	_, job, user, err := engine.ownership(task.UserID, account.AccountKey, task.PlatformJobID)
	if err != nil {
		return err
	}
	if job.ID != task.JobID {
		return ErrBelloBuddyNotFound
	}
	var source BelloBuddyCatalogJob
	if err := common.UnmarshalJsonStr(task.Desired, &source); err != nil {
		return err
	}
	if source.Online == nil || source.JobID != task.PlatformJobID {
		return resumeUnknown("JOB_SOURCE_INVALID")
	}
	fields := map[string]any{target.Fields["job_name"]: source.Name, target.Fields["hr"]: user.Username}
	remoteAccountID := jobPlatformAccountIdentity(user.Id, &account)
	for key, value := range map[string]string{"account": account.DisplayName, "account_id": remoteAccountID, "platform": "BOSS直聘", "job_id": source.JobID, "online": map[bool]string{true: "在招", false: "已下线"}[*source.Online]} {
		fields[engine.Config.JobSync.Fields[key]] = value
	}
	if source.City != nil {
		fields[engine.Config.JobSync.Fields["city"]] = *source.City
	}
	if source.Description != nil {
		fields[engine.Config.JobSync.Fields["boss_description"]] = *source.Description
	}
	for field := range fields {
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
	}
	var mapping model.BelloBuddyResumeJobMap
	if err := engine.DB.First(&mapping, task.JobMapID).Error; err != nil {
		return err
	}
	// 接收新观察不能替换旧的未知尝试。更新已知行时可以读回证明；未知创建必须有可信回执。
	if task.AttemptID != "" {
		var attempted map[string]any
		if err := common.UnmarshalJsonStr(task.AttemptFields, &attempted); err != nil {
			return err
		}
		if mapping.RecordID == "" {
			receipt, err := engine.readReceipt(task.AttemptID)
			if err != nil {
				return resumeUnknown("JOB_CREATE_RESULT_UNKNOWN")
			}
			mapping.RecordID = resumeCreatedRecordID(receipt)
			if mapping.RecordID == "" {
				return resumeUnknown("JOB_CREATE_RESULT_UNKNOWN")
			}
		}
		record, err := engine.Backend.Get(ctx, target.JobTable, mapping.RecordID)
		if err != nil {
			return err
		}
		if !resumeFieldsMatch(record, attempted) {
			return resumeUnknown("JOB_UPDATE_RESULT_UNKNOWN_OR_CHANGED")
		}
		data, _ := common.Marshal(attempted)
		if err := engine.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&mapping).Updates(map[string]any{"record_id": mapping.RecordID, "managed_fields": string(data)}).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.BelloBuddyResumeAttempt{}).Where("id = ?", task.AttemptID).Updates(map[string]any{"state": "verified", "finished_at": time.Now().Unix()}).Error; err != nil {
				return err
			}
			return tx.Model(task).Updates(map[string]any{"attempt_id": "", "attempt_fields": ""}).Error
		}); err != nil {
			return err
		}
		task.AttemptID = ""
		mapping.ManagedFields = string(data)
		if err := engine.shareVerifiedJobFields(&account, job, user.Id, &mapping); err != nil {
			return err
		}
	} else if mapping.RecordID == "" && mapping.AttemptID != "" {
		receipt, err := engine.readReceipt(mapping.AttemptID)
		if err != nil {
			return resumeUnknown("JOB_CREATE_RESULT_UNKNOWN")
		}
		mapping.RecordID = resumeCreatedRecordID(receipt)
		if mapping.RecordID == "" {
			return resumeUnknown("JOB_CREATE_RESULT_UNKNOWN")
		}
		if err := engine.DB.Model(&mapping).Update("record_id", mapping.RecordID).Error; err != nil {
			return err
		}
	}
	stale, err := engine.reuseJobMapping(ctx, &account, job, user, &mapping, task)
	if err != nil {
		return err
	}
	if stale {
		return nil
	}
	if mapping.RecordID != "" {
		record, err := engine.Backend.Get(ctx, target.JobTable, mapping.RecordID)
		if err != nil {
			return err
		}
		if resumeFieldsMatch(record, fields) {
			managed, err := common.Marshal(fields)
			if err != nil {
				return err
			}
			mapping.ManagedFields = string(managed)
			return engine.shareVerifiedJobFields(&account, job, user.Id, &mapping)
		}
	}
	if err := engine.writeJobCatalog(ctx, task, &mapping, fields); err != nil {
		return err
	}
	// 统一恢复路径执行读回和映射提交；新期望值仍是当前不可变内存快照。
	return engine.processJobCatalog(ctx, task)
}

func (engine *BelloBuddyResumeEngine) ReconcileJobCatalog(userID int, accountKey, clientID string) (*BelloBuddyCatalogReceipt, error) {
	receipt, err := engine.CatalogReceipt(userID, accountKey, clientID)
	if err != nil {
		return nil, err
	}
	ids := []int{}
	for _, task := range receipt.Jobs {
		ids = append(ids, task.ID)
	}
	if len(ids) > 0 {
		if err := engine.DB.Model(&model.BelloBuddyJobCatalog{}).Where("id IN ? AND user_id = ? AND state = ?", ids, userID, "blocked").Updates(map[string]any{"state": "queued", "error_code": ""}).Error; err != nil {
			return nil, err
		}
	}
	return engine.CatalogReceipt(userID, accountKey, clientID)
}
