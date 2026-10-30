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
			oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
			common.SetDatabaseTypes(dialect, dialect)
			t.Cleanup(func() { common.SetDatabaseTypes(oldMain, oldLog) })
			tables := append([]any{&model.User{}, &model.BelloBuddyAccount{}, &model.BelloBuddyJob{}, &model.BelloBuddyJobBinding{}}, model.BelloBuddyResumeModels()...)
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
			input := BelloBuddyAIInput{CandidateID: "candidate-A", Input: `{"protocol":"bellobuddy-c2-v1","materials":{"candidate":{"name":"已有候选人姓名","degree":"大专"}},"responsibility":{"typing_invitation_allowed":false}}`}
			scope := BelloBuddyConversationScope{UserID: 1, AccountID: 1, JobID: 1, CandidateHash: model.BelloBuddyIdentityHash(input.CandidateID)}
			reply := `{"protocol":"bellobuddy-c2-v1","reply_text":"欢迎了解客服岗位。","answers":[]}`
			unchanged, err := AddBelloBuddyTypingInvitation(scope, input, reply)
			require.NoError(t, err)
			assert.Equal(t, reply, unchanged)
			input.Input = strings.Replace(input.Input, "false", "true", 1)
			invitation, err := AddBelloBuddyTypingInvitation(scope, input, reply)
			require.NoError(t, err)
			require.Contains(t, invitation, typingConfig.PublicURL+"#")
			token := engine.states[key].Token
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
			assert.Error(t, RequireBelloBuddyTyping(1, typingScopeKey(config.Target, 1, 1, 1, "candidate-B")))
			backend.fault = ""
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
		})
	}
}
