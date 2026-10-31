package service

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
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

func TestBelloBuddyTypingDatabaseMatrix(t *testing.T) {
	for _, dialect := range []common.DatabaseType{common.DatabaseTypeSQLite, common.DatabaseTypeMySQL, common.DatabaseTypePostgreSQL} {
		t.Run(string(dialect), func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case common.DatabaseTypeSQLite:
				driver = sqlite.Open(filepath.Join(t.TempDir(), "typing.db"))
			case common.DatabaseTypeMySQL:
				if os.Getenv("TEST_MYSQL_DSN") == "" {
					t.Skip("Requires the real MySQL test database")
				}
				driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
			case common.DatabaseTypePostgreSQL:
				if os.Getenv("TEST_POSTGRES_DSN") == "" {
					t.Skip("Requires the real PostgreSQL test database")
				}
				driver = postgres.Open(os.Getenv("TEST_POSTGRES_DSN"))
			}
			db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			versionQuery := "SELECT sqlite_version()"
			if dialect == common.DatabaseTypeMySQL {
				versionQuery = "SELECT VERSION()"
			} else if dialect == common.DatabaseTypePostgreSQL {
				versionQuery = "SHOW server_version"
			}
			var version string
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("真实数据库版本：%s %s", dialect, version)
			oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
			common.SetDatabaseTypes(dialect, dialect)
			t.Cleanup(func() { common.SetDatabaseTypes(oldMain, oldLog) })
			tables := append([]any{&model.User{}, &model.BelloBuddyAccount{}, &model.BelloBuddyJob{}, &model.BelloBuddyJobBinding{}, &model.BelloBuddyPlan{}}, model.BelloBuddyResumeModels()...)
			require.NoError(t, db.AutoMigrate(tables...))
			t.Cleanup(func() {
				for i := len(tables) - 1; i >= 0; i-- {
					_ = db.Migrator().DropTable(tables[i])
				}
				sqlDB, _ := db.DB()
				_ = sqlDB.Close()
			})
			require.NoError(t, db.Create(&model.User{Id: 1, Username: "F1_TEST", Status: common.UserStatusEnabled, AffCode: "typing-test"}).Error)
			require.NoError(t, db.Create(&model.BelloBuddyAccount{ID: 1, UserID: 1, AccountKey: "account-A", AccountHash: model.BelloBuddyIdentityHash("account-A"), DisplayName: "招聘测试账号", Platform: "boss", Version: 1}).Error)
			require.NoError(t, db.Create(&model.BelloBuddyJobBinding{ID: 1, SharedKey: "typing-test", KeyHash: model.BelloBuddyIdentityHash("typing-test"), Enabled: true, Model: "typing-model", Version: 1}).Error)
			require.NoError(t, db.Create(&[]model.BelloBuddyJob{{ID: 1, AccountID: 1, JobID: "job-A", JobHash: model.BelloBuddyIdentityHash("job-A"), Name: "客服", BindingID: 1, Version: 1}, {ID: 2, AccountID: 1, JobID: "job-B", JobHash: model.BelloBuddyIdentityHash("job-B"), Name: "其他岗位", Version: 1}}).Error)
			config := ResumeArchiveConfig{DataDir: t.TempDir(), Target: FeishuArchiveTarget{Version: "typing-test-v1", Base: "base", JobTable: "tblJobs", TalentTable: "tblTalent", Fields: map[string]string{}}}
			for _, key := range []string{"resume", "job_link", "name", "owner", "account", "account_id", "platform", "candidate_id", "archive_key", "downloaded_at", "archived_at", "job_name", "hr"} {
				config.Target.Fields[key] = "fld_" + key
			}
			backend := &resumeFakeFeishu{config: config, records: map[string]*FeishuArchiveRecord{}, hashes: map[string]string{}, hrType: "text"}
			resume := &BelloBuddyResumeEngine{DB: db, Config: config, Backend: backend}
			fields := map[string]string{}
			for _, key := range []string{"status", "speed", "accuracy", "duration", "finished_at", "count", "attempt_id", "article", "verification", "errors", "typed", "correct"} {
				fields[key] = "fld_typing_" + key
			}
			privateRoot := t.TempDir()
			if runtime.GOOS == "windows" {
				current, err := user.Current()
				require.NoError(t, err)
				require.NoError(t, exec.Command("icacls.exe", privateRoot, "/inheritance:r", "/grant:r", "*"+current.Uid+":(OI)(CI)F", "*S-1-5-32-544:(OI)(CI)F", "*S-1-5-18:(OI)(CI)F").Run())
			}
			typingConfig := BelloBuddyTypingConfig{DataDir: filepath.Join(privateRoot, "assessments"), PublicURL: "https://typing.example/assessment.html", EnabledModels: []string{"typing-model"}, AllowedOrigins: []string{"https://typing.example"}, Fields: fields, Article: strings.Repeat("测", 800), ArticleName: "客服测评"}
			engine, err := NewBelloBuddyTypingEngine(resume, typingConfig)
			require.NoError(t, err)
			clock := time.Now()
			engine.now = func() time.Time { return clock }
			typingEngineMu.Lock()
			previous := typingEngine
			typingEngine = engine
			typingEngineMu.Unlock()
			t.Cleanup(func() { typingEngineMu.Lock(); typingEngine = previous; typingEngineMu.Unlock() })
			key := typingScopeKey(config.Target, 1, 1, 1, "candidate-A")
			require.NoError(t, db.Create(&model.BelloBuddyResumeJobMap{MapKey: resumeJobMapKey(config.Target, 1, 1, 1), RecordID: "rec_job"}).Error)
			// A pre-upgrade ticket has no invitation_mode and retains its archival policy.
			token, err := newTypingToken()
			require.NoError(t, err)
			legacy := &BelloBuddyTypingState{ScopeKey: key, Token: token, UserID: 1, AccountID: 1, JobID: 1, CandidateID: "candidate-A", Name: "已有候选人姓名", CreatedAt: clock.Unix(), ExpiresAt: clock.Add(7 * 24 * time.Hour).Unix(), Attempts: []BelloBuddyTypingAttempt{}}
			require.NoError(t, engine.save(legacy))
			engine.states[key] = legacy
			info, err := engine.PublicCall(token, "info", nil)
			require.NoError(t, err)
			assert.Equal(t, "已有候选人姓名", info["name"])
			assert.Zero(t, info["attempt_count"])
			assert.Error(t, RequireBelloBuddyTyping(1, key))
			assert.NoError(t, RequireBelloBuddyTyping(2, key))
			require.NoError(t, engine.SyncOnce(context.Background()))
			assert.Zero(t, backend.talentWrites, "Opening a link must not create a talent record")
			_, err = engine.PublicCall(strings.Repeat("x", 43), "info", nil)
			assert.Error(t, err)

			finish := func(typed string) string {
				data, err := engine.PublicCall(token, "attempt", []byte(`{}`))
				require.NoError(t, err)
				id := data["attempt_id"].(string)
				request, err := common.Marshal(map[string]any{"attempt_id": id})
				require.NoError(t, err)
				_, err = engine.PublicCall(token, "start", request)
				require.NoError(t, err)
				early, _ := common.Marshal(map[string]any{"attempt_id": id, "typed": typed})
				_, err = engine.PublicCall(token, "finish", early)
				assert.Error(t, err, "An early result must not be accepted")
				for range 20 {
					clock = clock.Add(3 * time.Second)
					beat, _ := common.Marshal(map[string]any{"attempt_id": id, "length": len([]rune(typed))})
					_, err = engine.PublicCall(token, "heartbeat", beat)
					require.NoError(t, err)
				}
				_, err = engine.PublicCall(token, "finish", early)
				require.NoError(t, err)
				_, err = engine.PublicCall(token, "finish", early)
				require.NoError(t, err, "A retry after response loss must be idempotent")
				return id
			}
			first := finish("错")
			assert.Error(t, RequireBelloBuddyTyping(1, key), "The archive gate waits for a verified Feishu write")
			backend.fault = "talent_response_lost"
			require.NoError(t, engine.SyncOnce(context.Background()), "A committed write with a lost response is reconciled by readback")
			assert.Equal(t, 1, backend.talentWrites)
			recordID := engine.states[key].RecordID
			require.NotEmpty(t, recordID)
			assert.Equal(t, first, backend.records[recordID].Fields[fields["attempt_id"]])
			assert.Equal(t, float64(0), backend.records[recordID].Fields[fields["speed"]], "Low scores are valid results, not automatic rejection")
			assert.NoError(t, RequireBelloBuddyTyping(1, key))
			assert.NoError(t, RequireBelloBuddyTyping(1, typingScopeKey(config.Target, 1, 1, 1, "candidate-B")), "An old model binding without a historical ticket cannot add a new archive gate")
			backend.fault = ""
			require.NoError(t, db.Model(&model.BelloBuddyJob{}).Where("id = ?", 1).Update("binding_id", 0).Error)
			second := finish("测测测错")
			require.NoError(t, engine.SyncOnce(context.Background()))
			require.NoError(t, engine.SyncOnce(context.Background()))
			assert.Equal(t, 1, backend.talentWrites, "Retakes update the candidate's existing job record")
			assert.Equal(t, second, backend.records[recordID].Fields[fields["attempt_id"]])
			assert.Equal(t, float64(2), backend.records[recordID].Fields[fields["count"]])
			assert.Equal(t, float64(3), backend.records[recordID].Fields[fields["speed"]])
			assert.Equal(t, float64(75), backend.records[recordID].Fields[fields["accuracy"]])
			assert.Equal(t, "已有候选人姓名", backend.records[recordID].Fields[config.Target.Fields["name"]])
			reloaded, err := NewBelloBuddyTypingEngine(resume, typingConfig)
			require.NoError(t, err)
			info, err = reloaded.PublicCall(token, "info", nil)
			require.NoError(t, err)
			assert.Equal(t, 2, info["attempt_count"], "Results and ownership survive restart")
			for range 8 {
				_, err = engine.PublicCall(token, "attempt", []byte(`{}`))
				require.NoError(t, err)
			}
			_, err = engine.PublicCall(token, "attempt", []byte(`{}`))
			assert.Error(t, err, "Unstarted attempts must also count toward the hourly limit")
			clock = clock.Add(time.Hour)
			finish("测测")
			backend.records[recordID].Fields[config.Target.Fields["candidate_id"]] = "candidate-B"
			assert.Error(t, engine.SyncOnce(context.Background()), "An identity conflict must never overwrite another candidate")
			assert.Equal(t, 1, backend.talentWrites)

			t.Run("plan_invitations_without_model", func(t *testing.T) {
				planConfig := typingConfig
				planConfig.DataDir = filepath.Join(privateRoot, "plan-assessments")
				planConfig.EnabledModels = nil
				planBackend := &resumeFakeFeishu{config: config, records: map[string]*FeishuArchiveRecord{}, hashes: map[string]string{}, hrType: "text"}
				planResume := &BelloBuddyResumeEngine{DB: db, Config: config, Backend: planBackend}
				planEngine, err := NewBelloBuddyTypingEngine(planResume, planConfig)
				require.NoError(t, err, "A model whitelist is not required for plan invitations")
				typingEngineMu.Lock()
				typingEngine = planEngine
				typingEngineMu.Unlock()
				planBody := `{"name":"客服测评","schema_defs":["FilterDef"],"disabled":false,"any_fails":[],"any_passes":[],"enable_typing_assessment":true}`
				plan := model.BelloBuddyPlan{ID: 1, Kind: "filter", Name: "客服测评", Enabled: true, Visibility: "private", OwnerID: 1, Version: 1, SchemaVersion: BelloBuddySchemaVersion, Content: planBody}
				require.NoError(t, db.Create(&plan).Error)
				planInput := BelloBuddyPlanInput{Kind: "filter", Name: plan.Name, Enabled: &plan.Enabled, SchemaVersion: BelloBuddySchemaVersion, Content: common.RawMessage(planBody)}
				require.NoError(t, ValidateBelloBuddyPlan(planInput))
				planInput.Content = common.RawMessage(strings.Replace(planBody, `"enable_typing_assessment":true`, `"enable_typing_assessment":"true"`, 1))
				assert.Error(t, ValidateBelloBuddyPlan(planInput), "The switch must be a boolean")
				require.NoError(t, db.Model(&model.BelloBuddyJob{}).Where("id = ?", 2).Updates(map[string]any{"filter_mode": "plan", "filter_plan_id": plan.ID}).Error)
				var account model.BelloBuddyAccount
				var job model.BelloBuddyJob
				require.NoError(t, db.First(&account, 1).Error)
				require.NoError(t, db.First(&job, 2).Error)
				assert.Zero(t, job.BindingID)
				secret := common.SessionSecret
				common.SessionSecret = strings.Repeat("test-typing-session-", 3)
				t.Cleanup(func() { common.SessionSecret = secret })
				identity := AuthIdentity{UserID: 1, UserAuthVersion: 1, SessionID: "typing-session"}
				runtimeConfig := BelloBuddyRuntimeConfig{Account: account, FetchedAt: common.GetTimestamp(), Jobs: []BelloBuddyRuntimeJob{{BelloBuddyJob: job}}, Plans: []BelloBuddyPlanDetail{{BelloBuddyPlan: plan, Content: common.RawMessage(planBody)}}}
				require.NoError(t, IssueBelloBuddyRuntimeSnapshot(identity, &runtimeConfig))
				input := BelloBuddyTypingInvitationInput{JobVersion: job.Version, FilterPlanID: plan.ID, FilterVersion: plan.Version, SnapshotRef: runtimeConfig.SnapshotRef, CandidateID: "candidate-plan", CandidateName: "模板测评候选人"}
				invitation, err := PrepareBelloBuddyTypingInvitation(identity, account.AccountKey, job.JobID, input)
				require.NoError(t, err)
				assert.Equal(t, "ready", invitation.State)
				assert.Contains(t, invitation.Text, invitation.Link)
				repeated, err := PrepareBelloBuddyTypingInvitation(identity, account.AccountKey, job.JobID, input)
				require.NoError(t, err)
				assert.Equal(t, invitation, repeated)
				assert.False(t, planEngine.states[invitation.InvitationID].InvitationDelivered, "Preparing a link is not a delivery receipt")
				assert.NoError(t, RequireBelloBuddyTyping(job.ID, invitation.InvitationID), "The new switch does not block resume archival")
				for _, change := range []struct {
					name string
					edit func(*BelloBuddyTypingInvitationInput)
				}{{"job_version", func(v *BelloBuddyTypingInvitationInput) { v.JobVersion++ }}, {"filter_version", func(v *BelloBuddyTypingInvitationInput) { v.FilterVersion++ }}, {"foreign_plan", func(v *BelloBuddyTypingInvitationInput) { v.FilterPlanID++ }}, {"missing_snapshot", func(v *BelloBuddyTypingInvitationInput) { v.SnapshotRef = "" }}} {
					t.Run(change.name, func(t *testing.T) {
						invalid := input
						change.edit(&invalid)
						_, err := PrepareBelloBuddyTypingInvitation(identity, account.AccountKey, job.JobID, invalid)
						assert.Error(t, err)
					})
				}
				otherUser := identity
				otherUser.UserID = 2
				_, err = PrepareBelloBuddyTypingInvitation(otherUser, account.AccountKey, job.JobID, input)
				assert.Error(t, err)
				_, err = PrepareBelloBuddyTypingInvitation(identity, account.AccountKey, "job-A", input)
				assert.Error(t, err)
				for _, update := range []struct {
					name     string
					model    any
					field    string
					changed  any
					original any
				}{
					{"current_job_version", &model.BelloBuddyJob{}, "version", int64(2), job.Version},
					{"current_job_plan", &model.BelloBuddyJob{}, "filter_plan_id", 99, plan.ID},
					{"current_job_mode", &model.BelloBuddyJob{}, "filter_mode", "pass_all", "plan"},
					{"current_filter_version", &model.BelloBuddyPlan{}, "version", int64(2), plan.Version},
					{"current_filter_switch", &model.BelloBuddyPlan{}, "content", strings.Replace(planBody, `"enable_typing_assessment":true`, `"enable_typing_assessment":false`, 1), planBody},
				} {
					t.Run(update.name, func(t *testing.T) {
						id := job.ID
						if _, isPlan := update.model.(*model.BelloBuddyPlan); isPlan {
							id = plan.ID
						}
						require.NoError(t, db.Model(update.model).Where("id = ?", id).Update(update.field, update.changed).Error)
						_, err := PrepareBelloBuddyTypingInvitation(identity, account.AccountKey, job.JobID, input)
						assert.Error(t, err, "A signed snapshot cannot bypass changed current configuration")
						require.NoError(t, db.Model(update.model).Where("id = ?", id).Update(update.field, update.original).Error)
					})
				}
				for _, body := range []string{strings.Replace(planBody, `,"enable_typing_assessment":true`, "", 1), strings.Replace(planBody, `"enable_typing_assessment":true`, `"enable_typing_assessment":false`, 1)} {
					runtimeConfig.Plans[0].Content = common.RawMessage(body)
					require.NoError(t, IssueBelloBuddyRuntimeSnapshot(identity, &runtimeConfig))
					disabled := input
					disabled.SnapshotRef = runtimeConfig.SnapshotRef
					_, err = PrepareBelloBuddyTypingInvitation(identity, account.AccountKey, job.JobID, disabled)
					assert.Error(t, err, "Only an explicitly enabled signed plan may invite")
				}
				expired, err := openBelloBuddySnapshot(input.SnapshotRef, identity, account.AccountKey)
				require.NoError(t, err)
				expired.ExpiresAt = common.GetTimestamp() - 1
				expired.IssuedAt = expired.ExpiresAt - BelloBuddyRuntimeSnapshotTTL
				expiredRef, err := sealBelloBuddySnapshot(*expired)
				require.NoError(t, err)
				invalid := input
				invalid.SnapshotRef = expiredRef.SnapshotRef
				_, err = PrepareBelloBuddyTypingInvitation(identity, account.AccountKey, job.JobID, invalid)
				assert.ErrorIs(t, err, ErrBelloBuddySnapshotExpired)
				input.RecruiterMessages = []string{typingConfig.PublicURL + "#" + strings.Repeat("x", 43)}
				ready, err := PrepareBelloBuddyTypingInvitation(identity, account.AccountKey, job.JobID, input)
				require.NoError(t, err)
				assert.Equal(t, "ready", ready.State, "Another candidate's link cannot acknowledge this invitation")
				input.RecruiterMessages = []string{invitation.Text}
				sent, err := PrepareBelloBuddyTypingInvitation(identity, account.AccountKey, job.JobID, input)
				require.NoError(t, err)
				assert.Equal(t, "sent", sent.State)
				assert.Empty(t, sent.Text)
				ticket := planEngine.states[invitation.InvitationID].Token
				require.NoError(t, db.Model(&model.BelloBuddyPlan{}).Where("id = ?", plan.ID).Update("enabled", false).Error)
				_, err = PrepareBelloBuddyTypingInvitation(identity, account.AccountKey, job.JobID, input)
				assert.Error(t, err, "Current plan access must still be checked")
				_, err = planEngine.PublicCall(ticket, "info", nil)
				require.NoError(t, err, "Disabling new invitations must not invalidate issued tickets")
				require.NoError(t, db.Model(&model.BelloBuddyAccount{}).Where("id = ?", account.ID).Update("user_id", 2).Error)
				_, err = planEngine.PublicCall(ticket, "info", nil)
				assert.Error(t, err, "Tickets may not cross changed account ownership")
				require.NoError(t, db.Model(&model.BelloBuddyAccount{}).Where("id = ?", account.ID).Update("user_id", 1).Error)
				planClock := time.Now()
				planEngine.now = func() time.Time { return planClock }
				attempt, err := planEngine.PublicCall(ticket, "attempt", []byte(`{}`))
				require.NoError(t, err)
				attemptID := attempt["attempt_id"].(string)
				request, err := common.Marshal(map[string]any{"attempt_id": attemptID})
				require.NoError(t, err)
				_, err = planEngine.PublicCall(ticket, "start", request)
				require.NoError(t, err)
				for range 20 {
					planClock = planClock.Add(3 * time.Second)
					beat, err := common.Marshal(map[string]any{"attempt_id": attemptID, "length": 1})
					require.NoError(t, err)
					_, err = planEngine.PublicCall(ticket, "heartbeat", beat)
					require.NoError(t, err)
				}
				finished, err := common.Marshal(map[string]any{"attempt_id": attemptID, "typed": "错"})
				require.NoError(t, err)
				_, err = planEngine.PublicCall(ticket, "finish", finished)
				require.NoError(t, err)
				require.NoError(t, db.Create(&model.BelloBuddyResumeJobMap{MapKey: resumeJobMapKey(config.Target, 1, 1, job.ID), RecordID: "rec_job_plan"}).Error)
				require.NoError(t, planEngine.SyncOnce(context.Background()))
				planRecordID := planEngine.states[invitation.InvitationID].RecordID
				require.NotEmpty(t, planRecordID)
				assert.Equal(t, float64(0), planBackend.records[planRecordID].Fields[fields["speed"]], "Plan assessments upload low scores without an automatic hiring decision")
				require.NoError(t, db.Model(&model.BelloBuddyPlan{}).Where("id = ?", plan.ID).Update("enabled", true).Error)
				completed, err := PrepareBelloBuddyTypingInvitation(identity, account.AccountKey, job.JobID, input)
				require.NoError(t, err)
				assert.Equal(t, "completed", completed.State)
				assert.Empty(t, completed.Text)
				reloaded, err := NewBelloBuddyTypingEngine(planResume, planConfig)
				require.NoError(t, err)
				assert.Equal(t, "plan", reloaded.states[invitation.InvitationID].InvitationMode)
				assert.True(t, reloaded.states[invitation.InvitationID].InvitationDelivered)
			})
		})
	}
}
