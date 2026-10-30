package service

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type resumeFakeFeishu struct {
	config                                    ResumeArchiveConfig
	records                                   map[string]*FeishuArchiveRecord
	hashes                                    map[string]string
	jobWrites, talentWrites, uploads, removes int
	jobUpdates                                int
	fault                                     string
	hrType                                    string
	multiple                                  bool
}

func (f *resumeFakeFeishu) Field(_ context.Context, _ string, id string) (*FeishuArchiveField, error) {
	key := ""
	for name, value := range f.config.Target.Fields {
		if value == id {
			key = name
		}
	}
	typeName := "text"
	switch key {
	case "hr":
		typeName = f.hrType
	case "resume":
		typeName = "attachment"
	case "job_link":
		typeName = "link"
	case "downloaded_at", "archived_at":
		typeName = "datetime"
	}
	field := &FeishuArchiveField{ID: id, Type: typeName, Multiple: f.multiple}
	field.Options = append(field.Options, struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	}{Name: "F1_TEST"}, struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	}{Name: "F1_TEST_B"})
	return field, nil
}
func (f *resumeFakeFeishu) Get(_ context.Context, _ string, id string) (*FeishuArchiveRecord, error) {
	record := f.records[id]
	if record == nil {
		return nil, resumeUnknown("FEISHU_RECORD_MISSING")
	}
	return record, nil
}
func (f *resumeFakeFeishu) Search(_ context.Context, _ string, field, value string) ([]FeishuArchiveRecord, error) {
	records := []FeishuArchiveRecord{}
	for _, record := range f.records {
		if record.Fields[field] == value {
			records = append(records, *record)
		}
	}
	return records, nil
}
func (f *resumeFakeFeishu) Write(_ context.Context, table, id string, fields map[string]any) (map[string]any, error) {
	creating := id == ""
	if creating {
		if table == f.config.Target.JobTable {
			f.jobWrites++
			id = fmt.Sprintf("rec_job_%d", f.jobWrites)
		} else {
			f.talentWrites++
			id = fmt.Sprintf("rec_talent_%d", f.talentWrites)
		}
		f.records[id] = &FeishuArchiveRecord{ID: id, Fields: map[string]any{}}
	}
	if !creating && table == f.config.Target.JobTable {
		f.jobUpdates++
	}
	data, _ := common.Marshal(fields)
	normalized := map[string]any{}
	_ = common.Unmarshal(data, &normalized)
	for key, value := range normalized {
		f.records[id].Fields[key] = value
	}
	if (creating && table == f.config.Target.JobTable && f.fault == "job_response_lost") || (creating && table == f.config.Target.TalentTable && f.fault == "talent_response_lost") || (!creating && f.fault == "time_response_lost" && fields[f.config.Target.Fields["archived_at"]] != nil) {
		return nil, resumeUnknown("CLI_TIMEOUT")
	}
	if !creating && table == f.config.Target.JobTable && f.fault == "catalog_update_lost" {
		return nil, resumeUnknown("CLI_TIMEOUT")
	}
	return map[string]any{"record": map[string]any{"record_id": id}}, nil
}
func (f *resumeFakeFeishu) Upload(_ context.Context, id, name string) (map[string]any, error) {
	f.uploads++
	token := fmt.Sprintf("file_%d", f.uploads)
	data, err := os.ReadFile(filepath.Join(f.config.DataDir, "files", name))
	if err != nil {
		return nil, err
	}
	f.hashes[token] = model.BelloBuddyIdentityHash(string(data))
	if f.fault != "media_only" {
		attachments, _ := f.records[id].Fields[f.config.Target.Fields["resume"]].([]any)
		f.records[id].Fields[f.config.Target.Fields["resume"]] = append(attachments, map[string]any{"file_token": token, "name": name, "size": float64(len(data))})
	}
	if f.fault == "media_only" || f.fault == "attachment_response_lost" {
		return nil, resumeUnknown("CLI_TIMEOUT")
	}
	return map[string]any{"ok": true}, nil
}
func (f *resumeFakeFeishu) Remove(_ context.Context, id string, tokens []string) (map[string]any, error) {
	f.removes++
	if f.fault == "remove_not_applied" {
		return nil, resumeUnknown("CLI_TIMEOUT")
	}
	attachments, _ := f.records[id].Fields[f.config.Target.Fields["resume"]].([]any)
	keep := []any{}
	for _, item := range attachments {
		value := item.(map[string]any)
		found := false
		for _, token := range tokens {
			if value["file_token"] == token {
				found = true
			}
		}
		if !found {
			keep = append(keep, item)
		}
	}
	f.records[id].Fields[f.config.Target.Fields["resume"]] = keep
	if f.fault == "remove_response_lost" {
		return nil, resumeUnknown("CLI_TIMEOUT")
	}
	return map[string]any{"ok": true}, nil
}
func (f *resumeFakeFeishu) DownloadHash(_ context.Context, _ string, token string) (string, error) {
	if f.fault == "hash_mismatch" {
		return "incorrect", nil
	}
	return f.hashes[token], nil
}

func resumeFixturePDF(label string) []byte {
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>", "", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"}
	stream := "BT /F1 12 Tf 20 150 Td (F1_TEST " + label + ") Tj ET"
	objects[3] = fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream)
	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, object := range objects {
		offsets = append(offsets, pdf.Len())
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return pdf.Bytes()
}

func TestBelloBuddyResumeArchiveDatabaseMatrix(t *testing.T) {
	for _, dialect := range []common.DatabaseType{common.DatabaseTypeSQLite, common.DatabaseTypeMySQL, common.DatabaseTypePostgreSQL} {
		t.Run(string(dialect), func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case common.DatabaseTypeSQLite:
				driver = sqlite.Open(filepath.Join(t.TempDir(), "f1.db"))
			case common.DatabaseTypeMySQL:
				if os.Getenv("TEST_MYSQL_DSN") == "" {
					t.Skip("必须另行完成真实 MySQL 验证")
				}
				driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
			case common.DatabaseTypePostgreSQL:
				if os.Getenv("TEST_POSTGRES_DSN") == "" {
					t.Skip("必须另行完成真实 PostgreSQL 验证")
				}
				driver = postgres.Open(os.Getenv("TEST_POSTGRES_DSN"))
			}
			db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
			common.SetDatabaseTypes(dialect, dialect)
			t.Cleanup(func() { common.SetDatabaseTypes(oldMain, oldLog) })
			base := []any{&model.User{}, &model.BelloBuddyAccount{}, &model.BelloBuddyJob{}}
			tables := append(base, model.BelloBuddyResumeModels()...)
			t.Cleanup(func() {
				for i := len(tables) - 1; i >= 0; i-- {
					_ = db.Migrator().DropTable(tables[i])
				}
				sqlDB, _ := db.DB()
				_ = sqlDB.Close()
			})
			versionQuery := "SELECT sqlite_version()"
			if dialect == common.DatabaseTypeMySQL {
				versionQuery = "SELECT VERSION()"
			}
			if dialect == common.DatabaseTypePostgreSQL {
				versionQuery = "SHOW server_version"
			}
			var version string
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("真实数据库 %s %s", dialect, version)
			// 最新主模型先建库并保留代表性账号／职位，再升级新增归档模型。
			require.NoError(t, db.AutoMigrate(base...))
			users := []model.User{{Id: 1, Username: "F1_TEST", DisplayName: "不作为HR匹配来源", Status: common.UserStatusEnabled, AffCode: "f1_a"}, {Id: 2, Username: "F1_TEST_B", Status: common.UserStatusEnabled, AffCode: "f1_b"}}
			require.NoError(t, db.Create(&users).Error)
			accounts := []model.BelloBuddyAccount{{UserID: 1, AccountKey: "account-A", AccountHash: model.BelloBuddyIdentityHash("account-A"), DisplayName: "F1_TEST A", Platform: "boss", Version: 1}, {UserID: 1, AccountKey: "account-B", AccountHash: model.BelloBuddyIdentityHash("account-B"), Platform: "boss", Version: 1}, {UserID: 2, AccountKey: "account-A", AccountHash: model.BelloBuddyIdentityHash("account-A"), Platform: "boss", Version: 1}}
			require.NoError(t, db.Create(&accounts).Error)
			jobs := []model.BelloBuddyJob{{AccountID: 1, JobID: "job-A", JobHash: model.BelloBuddyIdentityHash("job-A"), Name: "F1_TEST岗位", Version: 1}, {AccountID: 2, JobID: "job-A", JobHash: model.BelloBuddyIdentityHash("job-A"), Name: "F1_TEST岗位", Version: 1}, {AccountID: 3, JobID: "job-A", JobHash: model.BelloBuddyIdentityHash("job-A"), Name: "F1_TEST岗位", Version: 1}, {AccountID: 1, JobID: "job-B", JobHash: model.BelloBuddyIdentityHash("job-B"), Name: "F1_TEST岗位", Version: 1}}
			require.NoError(t, db.Create(&jobs).Error)
			require.NoError(t, db.AutoMigrate(&legacyResumeJobMap{}))
			legacyMap := legacyResumeJobMap{MapKey: "legacy_map", RecordID: "rec_legacy", AttemptID: "legacy_attempt"}
			require.NoError(t, db.Create(&legacyMap).Error)
			require.NoError(t, db.AutoMigrate(&legacyResumeLease{}, &legacyResumeAttempt{}))
			legacyLease := legacyResumeLease{ID: "legacy_lease", Owner: "old_owner", BusyArchiveID: 42}
			legacyAttempt := legacyResumeAttempt{ID: "legacy_attempt", ArchiveID: 42, Operation: "job_create", State: "confirmed", Receipt: "legacy_receipt"}
			require.NoError(t, db.Create(&legacyLease).Error)
			require.NoError(t, db.Create(&legacyAttempt).Error)
			for range 2 {
				require.NoError(t, db.AutoMigrate(tables...))
			}
			var upgradedMap model.BelloBuddyResumeJobMap
			require.NoError(t, db.First(&upgradedMap, legacyMap.ID).Error)
			assert.Equal(t, "rec_legacy", upgradedMap.RecordID)
			assert.Equal(t, "legacy_attempt", upgradedMap.AttemptID)
			var upgradedLease model.BelloBuddyResumeLease
			var upgradedAttempt model.BelloBuddyResumeAttempt
			require.NoError(t, db.First(&upgradedLease, "id = ?", legacyLease.ID).Error)
			require.NoError(t, db.First(&upgradedAttempt, "id = ?", legacyAttempt.ID).Error)
			assert.Equal(t, 42, upgradedLease.BusyArchiveID)
			assert.Zero(t, upgradedLease.BusyJobSyncID)
			assert.Equal(t, "legacy_receipt", upgradedAttempt.Receipt)
			assert.Zero(t, upgradedAttempt.JobSyncID)
			var preserved model.BelloBuddyJob
			require.NoError(t, db.First(&preserved, 1).Error)
			assert.Equal(t, "F1_TEST岗位", preserved.Name)
			t.Run("lease_expiry_preserves_inflight_work", func(t *testing.T) {
				key := model.BelloBuddyIdentityHash("F1_TEST_lease")
				now := time.Now().Unix()
				old, claimed, err := model.ClaimBelloBuddyResumeLease(db, key, "old-owner", now)
				require.NoError(t, err)
				require.True(t, claimed)
				require.NoError(t, db.Model(&model.BelloBuddyResumeLease{}).Where("id = ?", key).Update("busy_archive_id", 123).Error)
				engine := &BelloBuddyResumeEngine{DB: db}
				oldCtx := context.WithValue(context.Background(), resumeLeaseContextKey{}, resumeLeaseIdentity{Key: key, Owner: old.Owner})
				require.NoError(t, engine.guardLease(oldCtx, 123))
				require.NoError(t, db.Model(&model.BelloBuddyResumeLease{}).Where("id = ?", key).Update("expires_at", now-1).Error)
				current, claimed, err := model.ClaimBelloBuddyResumeLease(db, key, "new-owner", now)
				require.NoError(t, err)
				require.True(t, claimed)
				assert.Equal(t, 123, current.BusyArchiveID)
				assert.Error(t, engine.guardLease(oldCtx, 123))
				assert.Error(t, model.ReleaseBelloBuddyResumeLease(db, old, false))
				require.NoError(t, model.ReleaseBelloBuddyResumeLease(db, current, true))
			})

			for _, scenario := range []string{"normal", "job_response_lost", "talent_response_lost", "attachment_response_lost", "media_only", "time_response_lost", "hash_mismatch", "hr_missing", "hr_select"} {
				t.Run(scenario, func(t *testing.T) {
					for _, table := range model.BelloBuddyResumeModels() {
						require.NoError(t, db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(table).Error)
					}
					config := ResumeArchiveConfig{DataDir: t.TempDir(), MaxStorageBytes: 2 << 30, Target: FeishuArchiveTarget{Version: "f1-test-v1", Base: "base", JobTable: "tblJobs", TalentTable: "tblTalent", Fields: map[string]string{}}}
					for _, key := range []string{"resume", "job_link", "name", "owner", "account", "account_id", "platform", "candidate_id", "archive_key", "downloaded_at", "archived_at", "job_name", "hr"} {
						config.Target.Fields[key] = "fld_" + key
					}
					for _, directory := range []string{"files", "inputs", "receipts", "downloads", "cli-home"} {
						require.NoError(t, os.Mkdir(filepath.Join(config.DataDir, directory), 0700))
					}
					backend := &resumeFakeFeishu{config: config, records: map[string]*FeishuArchiveRecord{}, hashes: map[string]string{}, fault: scenario, hrType: "text"}
					if scenario == "hr_missing" {
						backend.hrType = "user"
					}
					if scenario == "hr_select" {
						backend.hrType = "select"
					}
					engine := &BelloBuddyResumeEngine{DB: db, Config: config, Backend: backend}
					input := BelloBuddyResumeInput{ClientUploadID: "upload-A", CandidateID: "candidate-A", CandidateName: "F1_TEST候选人", DownloadedAt: time.Now().UnixMilli()}
					pdf := resumeFixturePDF("v1")
					task, err := engine.Accept(context.Background(), 1, "account-A", "job-A", "same-name.pdf", input, bytes.NewReader(pdf))
					require.NoError(t, err)
					assert.Equal(t, "queued", task.State)
					if scenario == "normal" {
						duplicate := *task
						duplicate.ID = 0
						duplicate.ArchiveID = "F1_TEST_CONSTRAINT"
						require.Error(t, db.Create(&duplicate).Error)
						files := filepath.Join(config.DataDir, "files")
						if runtime.GOOS == "windows" {
							require.NoError(t, exec.Command("icacls.exe", files, "/deny", "*S-1-1-0:(WD,AD)").Run())
						} else {
							require.NoError(t, os.Chmod(files, 0500))
						}
						denied := input
						denied.ClientUploadID = "F1_TEST_DISK_PERMISSION"
						_, permissionErr := engine.Accept(context.Background(), 1, "account-A", "job-A", "same.pdf", denied, bytes.NewReader(pdf))
						if runtime.GOOS == "windows" {
							require.NoError(t, exec.Command("icacls.exe", files, "/remove:d", "*S-1-1-0").Run())
						} else {
							require.NoError(t, os.Chmod(files, 0700))
						}
						require.Error(t, permissionErr)
						assert.Equal(t, "ARCHIVE_DISK_ERROR", resumeFailureCode(permissionErr))
						filler := filepath.Join(files, "F1_TEST_CAPACITY.bin")
						f, err := os.OpenFile(filler, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
						require.NoError(t, err)
						require.NoError(t, f.Truncate(BelloBuddyResumeMaxBytes))
						require.NoError(t, f.Close())
						engine.Config.MaxStorageBytes = BelloBuddyResumeMaxBytes
						denied.ClientUploadID = "F1_TEST_CAPACITY"
						_, capacityErr := engine.Accept(context.Background(), 1, "account-A", "job-A", "same.pdf", denied, bytes.NewReader(pdf))
						require.Error(t, capacityErr)
						assert.Equal(t, "ARCHIVE_CAPACITY_FULL", resumeFailureCode(capacityErr))
						assert.FileExists(t, filepath.Join(files, task.StorageName))
						require.NoError(t, os.Remove(filler))
						engine.Config.MaxStorageBytes = config.MaxStorageBytes
					}

					received, err := engine.GetByUpload(1, "account-A", "job-A", input.ClientUploadID)
					require.NoError(t, err)
					assert.Equal(t, task.ArchiveID, received.ArchiveID)
					_, err = engine.Get(2, task.ArchiveID)
					assert.Error(t, err)
					_, err = engine.GetByUpload(1, "account-B", "job-A", input.ClientUploadID)
					assert.Error(t, err)
					_, err = engine.Accept(context.Background(), 1, "account-A", "job-A", "same-name.pdf", input, bytes.NewReader(resumeFixturePDF("changed")))
					assert.Error(t, err)
					var wg sync.WaitGroup
					results := make(chan error, 4)
					for i := range 4 {
						wg.Add(1)
						go func() {
							defer wg.Done()
							duplicate := input
							duplicate.ClientUploadID = fmt.Sprintf("concurrent-%d", i)
							returned, err := engine.Accept(context.Background(), 1, "account-A", "job-A", "renamed.pdf", duplicate, bytes.NewReader(pdf))
							if err == nil && returned.ArchiveID != task.ArchiveID {
								err = fmt.Errorf("dedup failed")
							}
							results <- err
						}()
					}
					wg.Wait()
					close(results)
					for err := range results {
						require.NoError(t, err)
					}
					runErr := engine.RunOnce(context.Background())
					task, err = engine.Get(1, task.ArchiveID)
					require.NoError(t, err)
					hrBlocked := strings.HasPrefix(scenario, "hr_")
					blocked := scenario == "job_response_lost" || scenario == "media_only" || scenario == "hash_mismatch" || hrBlocked
					if blocked {
						require.Error(t, runErr)
						assert.Equal(t, "blocked", task.State)
						assert.FileExists(t, filepath.Join(config.DataDir, "files", task.StorageName))
						if hrBlocked {
							assert.Equal(t, "HR_FIELD_NOT_TEXT", task.ErrorCode)
							assert.Zero(t, backend.jobWrites+backend.talentWrites+backend.uploads)
						}
						before := backend.jobWrites + backend.talentWrites + backend.uploads
						_, err = engine.Reconcile(1, task.ArchiveID)
						require.NoError(t, err)
						secondErr := engine.RunOnce(context.Background())
						require.Error(t, secondErr)
						assert.Equal(t, before, backend.jobWrites+backend.talentWrites+backend.uploads)
						if hrBlocked {
							backend.hrType = "text"
							backend.fault = "normal"
							_, err = engine.Reconcile(1, task.ArchiveID)
							require.NoError(t, err)
							require.NoError(t, engine.RunOnce(context.Background()))
						}
					} else {
						require.NoError(t, runErr)
						assert.Equal(t, "archived", task.State)
						assert.Equal(t, 1, backend.jobWrites)
						assert.Equal(t, 1, backend.talentWrites)
						assert.Equal(t, 1, backend.uploads)
						// 重新实例化及重复迁移保持接收、任务、映射、附件回执。
						require.NoError(t, db.AutoMigrate(model.BelloBuddyResumeModels()...))
						reopened := &BelloBuddyResumeEngine{DB: db, Config: config, Backend: backend}
						require.NoError(t, reopened.RunOnce(context.Background()))
						assert.Equal(t, 1, backend.uploads)
						assert.NoError(t, reopened.CleanupVerifiedFiles(task.VerifiedAt+7*86400-1))
						assert.FileExists(t, filepath.Join(config.DataDir, "files", task.StorageName))
						assert.NoError(t, reopened.CleanupVerifiedFiles(task.VerifiedAt+7*86400+1))
						assert.NoFileExists(t, filepath.Join(config.DataDir, "files", task.StorageName))
						_, err = reopened.GetByUpload(1, "account-A", "job-A", input.ClientUploadID)
						assert.NoError(t, err)
					}
					if scenario == "normal" {
						// 不同用户／账号／职位同候选人同文件仍为独立记录。
						for i, scope := range []struct {
							User         int
							Account, Job string
						}{{1, "account-B", "job-A"}, {1, "account-A", "job-B"}, {2, "account-A", "job-A"}} {
							isolated := input
							isolated.ClientUploadID = fmt.Sprintf("isolated-%d", i)
							_, err = engine.Accept(context.Background(), scope.User, scope.Account, scope.Job, "same-name.pdf", isolated, bytes.NewReader(pdf))
							require.NoError(t, err)
							require.NoError(t, engine.RunOnce(context.Background()))
						}
						assert.Equal(t, 4, backend.talentWrites)
						assert.Equal(t, 4, backend.jobWrites)
						var scope model.BelloBuddyResumeScope
						require.NoError(t, db.First(&scope, task.ScopeID).Error)
						record := backend.records[scope.RecordID]
						record.Fields["manual_review"] = "保留人工评审"
						record.Fields[config.Target.Fields["resume"]] = append(record.Fields[config.Target.Fields["resume"]].([]any), map[string]any{"file_token": "manual-token", "name": "manual.pdf"})
						newInput := input
						newInput.ClientUploadID = "new-version"
						newest, err := engine.Accept(context.Background(), 1, "account-A", "job-A", "same-name.pdf", newInput, bytes.NewReader(resumeFixturePDF("v2")))
						require.NoError(t, err)
						backend.fault = "remove_response_lost"
						require.NoError(t, engine.RunOnce(context.Background()))
						newest, err = engine.Get(1, newest.ArchiveID)
						require.NoError(t, err)
						assert.Equal(t, "archived", newest.State)
						assert.Equal(t, "保留人工评审", record.Fields["manual_review"])
						attachments, err := resumeAttachmentList(record, config.Target.Fields["resume"])
						require.NoError(t, err)
						assert.Len(t, attachments, 2)
						assert.Equal(t, 1, backend.removes)
						var old model.BelloBuddyResumeArchive
						require.NoError(t, db.First(&old, task.ID).Error)
						assert.Equal(t, "superseded", old.State)
						duplicate := input
						duplicate.ClientUploadID = "old-content-again"
						returned, err := engine.Accept(context.Background(), 1, "account-A", "job-A", "different-name.pdf", duplicate, bytes.NewReader(pdf))
						require.NoError(t, err)
						assert.Equal(t, task.ArchiveID, returned.ArchiveID)
						assert.Equal(t, "superseded", returned.State)
					}
					invalid := input
					invalid.ClientUploadID = "bad-pdf"
					_, err = engine.Accept(context.Background(), 1, "account-A", "job-A", "fake.pdf", invalid, strings.NewReader("not PDF"))
					assert.Error(t, err)
					_, err = engine.Accept(context.Background(), 2, "account-B", "job-A", "valid.pdf", invalid, bytes.NewReader(pdf))
					assert.Error(t, err)
				})
			}
			t.Run("job_catalog_contracts", func(t *testing.T) { verifyJobCatalogContracts(t, db) })
		})
	}
}

func TestFeishuDateReadbackPreservesTextAndInstant(t *testing.T) {
	data := map[string]any{"record_id_list": []any{"rec_test"}, "field_id_list": []any{"fld_date", "fld_text"}, "field_type_list": []any{"datetime", "text"}, "data": []any{[]any{"2026-10-14T07:32:10.000+08:00", "2026-10-14T07:32:10.000+08:00"}}}
	records, err := decodeFeishuRecords(data)
	require.NoError(t, err)
	assert.Equal(t, "2026-10-14 07:32:10", records[0].Fields["fld_date"])
	assert.Equal(t, "2026-10-14T07:32:10.000+08:00", records[0].Fields["fld_text"])
	data["data"] = []any{[]any{"2026-10-13T23:32:10Z", "unchanged"}}
	records, err = decodeFeishuRecords(data)
	require.NoError(t, err)
	assert.Equal(t, "2026-10-14 07:32:10", records[0].Fields["fld_date"])
	data["data"] = []any{[]any{"invalid", "unchanged"}}
	_, err = decodeFeishuRecords(data)
	require.Error(t, err)
	for _, check := range []struct {
		Data map[string]any
		Code string
	}{
		{map[string]any{"code": float64(403), "type": "network", "message": "private vendor data", "failed": []any{"private path"}}, "FEISHU_DOWNLOAD_FORBIDDEN_403"},
		{map[string]any{"type": "network"}, "FEISHU_NETWORK_ERROR"},
		{map[string]any{}, "CLI_RESULT_UNKNOWN"},
	} {
		failure := feishuDownloadFailure(check.Data)
		assert.Equal(t, check.Code, failure.Code)
		assert.NotContains(t, failure.Error(), "private")
	}
}

// 明确的旧版本结构，验证新增列在已存在映射／租约／尝试上升级，并重复迁移。
type legacyResumeJobMap struct {
	ID        int    `gorm:"primaryKey"`
	MapKey    string `gorm:"type:varchar(64);not null;uniqueIndex"`
	RecordID  string `gorm:"type:varchar(128)"`
	AttemptID string `gorm:"type:varchar(36)"`
}

func (legacyResumeJobMap) TableName() string { return "bello_buddy_resume_job_maps" }

func verifyJobCatalogContracts(t *testing.T, db *gorm.DB) {
	for _, table := range model.BelloBuddyResumeModels() {
		require.NoError(t, db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(table).Error)
	}
	oldDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })
	config := ResumeArchiveConfig{DataDir: t.TempDir(), MaxStorageBytes: 2 << 30, Target: FeishuArchiveTarget{Version: "f1-test-v1", Base: "base", JobTable: "tblJobs", TalentTable: "tblTalent", Fields: map[string]string{}}}
	for _, key := range []string{"resume", "job_link", "name", "owner", "account", "account_id", "platform", "candidate_id", "archive_key", "downloaded_at", "archived_at", "job_name", "hr"} {
		config.Target.Fields[key] = "fld_" + key
	}
	config.JobSync.Enabled = true
	config.JobSync.Fields = map[string]string{}
	for _, key := range []string{"account", "account_id", "platform", "job_id", "online", "city", "boss_description"} {
		config.JobSync.Fields[key] = "fld_catalog_" + key
	}
	for _, directory := range []string{"files", "inputs", "receipts", "downloads", "cli-home"} {
		require.NoError(t, os.Mkdir(filepath.Join(config.DataDir, directory), 0700))
	}
	backend := &resumeFakeFeishu{config: config, records: map[string]*FeishuArchiveRecord{}, hashes: map[string]string{}, hrType: "text"}
	engine := &BelloBuddyResumeEngine{DB: db, Config: config, Backend: backend}
	online := true
	city := "上海"
	description := "F1_TEST 完整JD\n第二段"
	input := BelloBuddyCatalogInput{ClientSyncID: "catalog-initial", ObservedAt: time.Now().UnixMilli() - 100000, Trigger: "login", DisplayName: "F1_TEST", Jobs: []BelloBuddyCatalogJob{{JobID: "job-A", Name: "F1_TEST 同名岗位", Online: &online, City: &city, Description: &description}}}
	oversized := input
	oversized.Jobs = append([]BelloBuddyCatalogJob{}, input.Jobs...)
	escaped := strings.Repeat("\"", 40<<10)
	oversized.Jobs[0].Description = &escaped
	_, validationErr := engine.AcceptJobCatalog(1, "account-A", oversized)
	require.Error(t, validationErr)
	assert.Equal(t, "INVALID_REQUEST", resumeFailureCode(validationErr))
	receipt, err := engine.AcceptJobCatalog(1, "account-A", input)
	require.NoError(t, err)
	require.Len(t, receipt.Jobs, 1)
	again, err := engine.AcceptJobCatalog(1, "account-A", input)
	require.NoError(t, err)
	assert.Equal(t, receipt.ClientSyncID, again.ClientSyncID)
	conflict := input
	conflict.DisplayName = "different"
	_, err = engine.AcceptJobCatalog(1, "account-A", conflict)
	require.Error(t, err)
	assert.Equal(t, "JOB_SYNC_ID_CONFLICT", resumeFailureCode(err))
	_, err = engine.CatalogReceipt(2, "account-A", input.ClientSyncID)
	assert.ErrorIs(t, err, ErrBelloBuddyNotFound)
	require.NoError(t, engine.RunOnce(context.Background()))
	receipt, err = engine.CatalogReceipt(1, "account-A", input.ClientSyncID)
	require.NoError(t, err)
	assert.Equal(t, "synced", receipt.Jobs[0].State)
	var mapping model.BelloBuddyResumeJobMap
	require.NoError(t, db.First(&mapping, receipt.Jobs[0].JobMapID).Error)
	recordID := mapping.RecordID
	require.NotEmpty(t, recordID)
	record := backend.records[recordID]
	record.Fields["manual_description"] = "HR自编职位描述"
	record.Fields["manual_completion_date"] = "保持"
	record.Fields[config.Target.Fields["job_name"]] = "HR手改系统字段"
	// 用户已确认系统字段以 BOSS 覆盖；下线、重开均保留同一行，不写完成日期。
	for _, state := range []bool{false, true} {
		input.ClientSyncID = fmt.Sprintf("catalog-online-%v", state)
		input.ObservedAt++
		input.Trigger = "refresh"
		input.Jobs[0].Name = "F1_TEST 改名"
		input.Jobs[0].Online = &state
		_, err = engine.AcceptJobCatalog(1, "account-A", input)
		require.NoError(t, err)
		require.NoError(t, engine.RunOnce(context.Background()))
		assert.Equal(t, map[bool]string{true: "在招", false: "已下线"}[state], record.Fields[config.JobSync.Fields["online"]])
		assert.Equal(t, "F1_TEST 改名", record.Fields[config.Target.Fields["job_name"]])
		assert.Equal(t, "HR自编职位描述", record.Fields["manual_description"])
		assert.Equal(t, "保持", record.Fields["manual_completion_date"])
	}
	assert.Equal(t, 1, backend.jobWrites)
	empty := input
	empty.ClientSyncID = "empty-source"
	empty.ObservedAt++
	empty.Jobs = []BelloBuddyCatalogJob{}
	_, err = engine.AcceptJobCatalog(1, "account-A", empty)
	require.NoError(t, err)
	assert.Equal(t, "在招", record.Fields[config.JobSync.Fields["online"]])
	stale := input
	stale.ClientSyncID = "stale-source"
	stale.ObservedAt--
	stale.Jobs = append([]BelloBuddyCatalogJob{}, input.Jobs...)
	stale.Jobs[0].Name = "过时名称"
	_, err = engine.AcceptJobCatalog(1, "account-A", stale)
	require.NoError(t, err)
	require.NoError(t, engine.RunOnce(context.Background()))
	assert.Equal(t, "F1_TEST 改名", record.Fields[config.Target.Fields["job_name"]])
	input.ClientSyncID = "jd-unavailable"
	input.ObservedAt++
	input.Jobs[0].Description = nil
	receipt, err = engine.AcceptJobCatalog(1, "account-A", input)
	require.NoError(t, err)
	require.NoError(t, engine.RunOnce(context.Background()))
	assert.Equal(t, "BOSS_JD_UNAVAILABLE", receipt.Jobs[0].Warning)
	assert.Equal(t, description, record.Fields[config.JobSync.Fields["boss_description"]])
	// 简历归档复用已同步职位行；不因改名另建，也不覆盖其他手工字段。
	task, err := engine.Accept(context.Background(), 1, "account-A", "job-A", "F1_TEST.pdf", BelloBuddyResumeInput{ClientUploadID: "catalog-resume", CandidateID: "F1_TEST", DownloadedAt: time.Now().UnixMilli()}, bytes.NewReader(resumeFixturePDF("catalog")))
	require.NoError(t, err)
	require.NoError(t, engine.RunOnce(context.Background()))
	task, err = engine.Get(1, task.ArchiveID)
	require.NoError(t, err)
	assert.Equal(t, "archived", task.State)
	assert.Equal(t, 1, backend.jobWrites)
	var scope model.BelloBuddyResumeScope
	require.NoError(t, db.First(&scope, task.ScopeID).Error)
	assert.Equal(t, mapping.ID, scope.JobMapID)
	// 相同平台职位在另一个招聘账号／产品用户下保持独立。
	for i, identity := range []struct {
		user    int
		account string
	}{{1, "account-B"}, {2, "account-A"}} {
		input.ClientSyncID = fmt.Sprintf("isolation-%d", i)
		input.ObservedAt++
		_, err = engine.AcceptJobCatalog(identity.user, identity.account, input)
		require.NoError(t, err)
		require.NoError(t, engine.RunOnce(context.Background()))
	}
	assert.Equal(t, 3, backend.jobWrites)
	// 更新响应丢失：重建engine先按旧尝试读回，成功后不重复调用更新。
	input.ClientSyncID = "update-lost"
	input.ObservedAt++
	input.Jobs[0].Name = "F1_TEST 最新名称"
	_, err = engine.AcceptJobCatalog(1, "account-A", input)
	require.NoError(t, err)
	backend.fault = "catalog_update_lost"
	require.Error(t, engine.RunOnce(context.Background()))
	count := backend.jobUpdates
	backend.fault = ""
	_, err = engine.ReconcileJobCatalog(1, "account-A", input.ClientSyncID)
	require.NoError(t, err)
	restarted := &BelloBuddyResumeEngine{DB: db, Config: config, Backend: backend}
	require.NoError(t, restarted.RunOnce(context.Background()))
	assert.Equal(t, count, backend.jobUpdates)
	receipt, err = engine.CatalogReceipt(1, "account-A", input.ClientSyncID)
	require.NoError(t, err)
	assert.Equal(t, "synced", receipt.Jobs[0].State)
	// 未知创建没有可信ID时绝不按同名猜测或重建，仍持有base的待核对归属。
	input.ClientSyncID = "create-lost"
	input.ObservedAt++
	input.Jobs[0].JobID = "job-new"
	_, err = engine.AcceptJobCatalog(1, "account-A", input)
	require.NoError(t, err)
	backend.fault = "job_response_lost"
	require.Error(t, engine.RunOnce(context.Background()))
	count = backend.jobWrites
	backend.fault = ""
	_, err = engine.ReconcileJobCatalog(1, "account-A", input.ClientSyncID)
	require.NoError(t, err)
	require.Error(t, restarted.RunOnce(context.Background()))
	assert.Equal(t, count, backend.jobWrites)
	receipt, err = engine.CatalogReceipt(1, "account-A", input.ClientSyncID)
	require.NoError(t, err)
	assert.Equal(t, "JOB_CREATE_RESULT_UNKNOWN", receipt.Jobs[0].ErrorCode)
}

type legacyResumeLease struct {
	ID            string `gorm:"type:varchar(64);primaryKey"`
	Owner         string `gorm:"type:varchar(36);not null"`
	ExpiresAt     int64  `gorm:"not null"`
	BusyArchiveID int    `gorm:"not null"`
}

func (legacyResumeLease) TableName() string { return "bello_buddy_resume_leases" }

type legacyResumeAttempt struct {
	ID         string `gorm:"type:varchar(36);primaryKey"`
	ArchiveID  int    `gorm:"not null;index"`
	Operation  string `gorm:"type:varchar(32);not null"`
	State      string `gorm:"type:varchar(24);not null"`
	Receipt    string `gorm:"type:text"`
	ErrorCode  string `gorm:"type:varchar(64)"`
	StartedAt  int64
	FinishedAt int64
}

func (legacyResumeAttempt) TableName() string { return "bello_buddy_resume_attempts" }
