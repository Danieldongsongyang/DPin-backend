package router

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TEST_*_DSN 必须指向可销毁的独立测试库；测试结束删除本测试创建的表。
func TestBelloBuddyConfiguration(t *testing.T) {
	for _, dialect := range []common.DatabaseType{common.DatabaseTypeSQLite, common.DatabaseTypeMySQL, common.DatabaseTypePostgreSQL} {
		t.Run(string(dialect), func(t *testing.T) {
			var dialector gorm.Dialector
			switch dialect {
			case common.DatabaseTypeSQLite:
				sqlitePath := filepath.Join(t.TempDir(), "bellobuddy.db")
				dialector = sqlite.Open(sqlitePath)
				oldSQLitePath := common.SQLitePath
				common.SQLitePath = sqlitePath
				t.Cleanup(func() { common.SQLitePath = oldSQLitePath })
				t.Setenv("SQL_DSN", "local")
			case common.DatabaseTypeMySQL:
				if os.Getenv("TEST_MYSQL_DSN") == "" {
					t.Skip("未配置独立 MySQL 测试库")
				}
				dialector = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
				t.Setenv("SQL_DSN", os.Getenv("TEST_MYSQL_DSN"))
			case common.DatabaseTypePostgreSQL:
				if os.Getenv("TEST_POSTGRES_DSN") == "" {
					t.Skip("未配置独立 PostgreSQL 测试库")
				}
				dialector = postgres.Open(os.Getenv("TEST_POSTGRES_DSN"))
				t.Setenv("SQL_DSN", os.Getenv("TEST_POSTGRES_DSN"))
			}
			db, err := gorm.Open(dialector, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			versionSQL := "SELECT sqlite_version()"
			if dialect == common.DatabaseTypeMySQL {
				versionSQL = "SELECT VERSION()"
			}
			if dialect == common.DatabaseTypePostgreSQL {
				versionSQL = "SHOW server_version"
			}
			var version string
			require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
			t.Logf("真实数据库版本：%s %s", dialect, version)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			oldDB, oldLogDB, oldRedis, oldSecret := model.DB, model.LOG_DB, common.RedisEnabled, common.SessionSecret
			oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
			model.DB, model.LOG_DB, common.RedisEnabled, common.SessionSecret = db, db, false, "bellobuddy-session-regression-secret"
			common.SetDatabaseTypes(dialect, dialect)
			tables := []any{&model.User{}, &model.UserSession{}, &model.UserAccessToken{}, &model.Option{}, &model.Log{}, &model.AuditLog{}, &model.CasbinRule{}, &model.AuthzRole{}, &model.Channel{}, &model.Ability{}, &model.Token{}, &model.BelloBuddyRelayIdentity{}, &model.SubscriptionPlan{}, &model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}, &model.BelloBuddyPlan{}, &model.BelloBuddyPlanSource{}, &model.BelloBuddyAccount{}, &model.BelloBuddyJob{}, &model.BelloBuddyJobBinding{}}
			t.Cleanup(func() {
				for i := len(tables) - 1; i >= 0; i-- {
					_ = db.Migrator().DropTable(tables[i])
				}
				_ = sqlDB.Close()
				model.DB, model.LOG_DB, common.RedisEnabled, common.SessionSecret = oldDB, oldLogDB, oldRedis, oldSecret
				common.SetDatabaseTypes(oldMain, oldLog)
			})
			// 接入前 Token 数据升级；其余配置表从全新数据库建立。
			require.NoError(t, db.AutoMigrate(&belloBuddyTokenBeforeBridge{}))
			legacyCredential := belloBuddyTokenBeforeBridge{UserId: 1, Key: "legacybellobuddyfixturecredential", Name: "原有用户凭据", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 1000, UsedQuota: 7}
			require.NoError(t, db.Create(&legacyCredential).Error)
			// 连续两次迁移；随后再次迁移并验证数据和唯一索引。
			require.NoError(t, db.AutoMigrate(tables...))
			require.NoError(t, db.AutoMigrate(tables...))
			var upgradedCredential model.Token
			require.NoError(t, db.First(&upgradedCredential, legacyCredential.Id).Error)
			assert.Empty(t, upgradedCredential.ManagedBy)
			assert.Equal(t, legacyCredential.Key, upgradedCredential.Key)
			assert.Equal(t, legacyCredential.RemainQuota, upgradedCredential.RemainQuota)
			assert.Equal(t, legacyCredential.UsedQuota, upgradedCredential.UsedQuota)
			// 经实际数据库初始化设置原生中继依赖的方言列名。
			oldMaster := common.IsMasterNode
			common.IsMasterNode = false
			t.Setenv("LOG_SQL_DSN", "")
			require.NoError(t, model.InitDB())
			common.IsMasterNode = oldMaster
			initializedDB, err := model.DB.DB()
			require.NoError(t, err)
			require.NoError(t, initializedDB.Close())
			model.DB = db
			users := []model.User{
				{Username: "bello-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1},
				{Username: "bello-other", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1},
				{Username: "bello-admin", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1},
			}
			tokens := make([]string, len(users))
			for i := range users {
				users[i].AffCode = fmt.Sprintf("bello%d", i)
				require.NoError(t, db.Create(&users[i]).Error)
				bundle, err := service.CreateLoginSession(users[i].Id, "password", "127.0.0.1", "BelloBuddy test")
				require.NoError(t, err)
				tokens[i] = bundle.AccessToken
			}
			if dialect == common.DatabaseTypeSQLite && os.Getenv("BELLOBUDDY_ELECTRON_BINARY") != "" {
				t.Run("Electron认证网络", func(t *testing.T) {
					runBelloBuddyElectronAuthSpike(t, db, users[0].Id)
				})
			}
			gin.SetMode(gin.TestMode)
			engine := gin.New()
			registerBelloBuddyRoutes(engine.Group("/api"))
			request := func(method, path, token string, body any, status int) common.RawMessage {
				t.Helper()
				var raw []byte
				if body != nil {
					raw, err = common.Marshal(body)
					require.NoError(t, err)
				}
				req := httptest.NewRequest(method, "/api/bellobuddy"+path, strings.NewReader(string(raw)))
				req.Header.Set("Content-Type", "application/json")
				if token != "" {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				recorder := httptest.NewRecorder()
				engine.ServeHTTP(recorder, req)
				require.Equal(t, status, recorder.Code, "%s %s: %s", method, path, recorder.Body.String())
				assert.Contains(t, recorder.Header().Get("Cache-Control"), "no-store")
				var response struct {
					Data common.RawMessage `json:"data"`
					Code string            `json:"code"`
				}
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
				return response.Data
			}
			request(http.MethodGet, "/plans", "", nil, http.StatusUnauthorized)
			request(http.MethodGet, "/plans", "local-only", nil, http.StatusUnauthorized)
			request(http.MethodGet, "/capabilities", "", nil, http.StatusUnauthorized)
			request(http.MethodGet, "/capabilities", "local-only", nil, http.StatusUnauthorized)
			patRaw := "nap_bellobuddy-personal-token-test"
			pat := model.UserAccessToken{UserId: users[0].Id, Name: "测试 PAT", TokenHash: model.AccessTokenFingerprint(patRaw), Scopes: "[]"}
			require.NoError(t, db.Create(&pat).Error)
			request(http.MethodGet, "/plans", patRaw, nil, http.StatusForbidden)
			request(http.MethodGet, "/capabilities", patRaw, nil, http.StatusForbidden)
			capabilities := request(http.MethodGet, "/capabilities", tokens[0], nil, http.StatusOK)
			assert.JSONEq(t, `{
				"schema_version":1,"plan_kinds":["filter","communication"],
				"selection_modes":{"filter":["plan","pass_all","skip_all"],"communication":["plan","jump"]},
				"limits":{"max_body_bytes":2097152,"max_plan_bytes":61440,"max_jobs":200},
				"features":{"plan_management":true,"job_configuration":true,"runtime_config":true},
				"ai_screen":{"available":false,"protocol":"typesafe_systemone","models":[],"code":"AI_RESOURCE_UNAVAILABLE"},
				"ai_reply":{"available":false,"protocol":"maxkb_application","models":[],"code":"AI_RESOURCE_UNAVAILABLE"}
			}`, string(capabilities))
			identityOther, _, err := service.ParseDashboardAccessToken(tokens[1])
			require.NoError(t, err)
			require.NoError(t, db.Model(&model.UserSession{}).Where("sid = ?", identityOther.SessionID).Update("expires_at", common.GetTimestamp()-1).Error)
			request(http.MethodGet, "/plans", tokens[1], nil, http.StatusUnauthorized)
			request(http.MethodGet, "/capabilities", tokens[1], nil, http.StatusUnauthorized)
			require.NoError(t, db.Model(&model.UserSession{}).Where("sid = ?", identityOther.SessionID).Update("expires_at", common.GetTimestamp()+3600).Error)
			require.NoError(t, db.Model(&users[1]).Update("status", common.UserStatusDisabled).Error)
			request(http.MethodGet, "/plans", tokens[1], nil, http.StatusUnauthorized)
			require.NoError(t, db.Model(&users[1]).Update("status", common.UserStatusEnabled).Error)
			request(http.MethodPost, "/admin/plans", tokens[0], nil, http.StatusForbidden)
			enabled, limit := true, 50
			// 使用实际已登记的三份完整方案，证明已知字段、嵌套值和旧元数据无损往返。
			actualFilterID := 0
			for _, file := range []string{"filter-after-sales.json", "communication-after-sales-1.json", "communication-after-sales-2.json"} {
				raw, err := os.ReadFile(filepath.Join("testdata", "bellobuddy", file))
				require.NoError(t, err)
				var metadata struct {
					Name       string `json:"name"`
					SchemeName string `json:"scheme_name"`
					FilterDef  *struct {
						ID string `json:"id"`
					} `json:"filter_def"`
				}
				require.NoError(t, common.Unmarshal(raw, &metadata))
				input := service.BelloBuddyPlanInput{Kind: "filter", Name: metadata.Name, Enabled: &enabled, SchemaVersion: 1, Content: raw}
				if metadata.SchemeName != "" {
					input.Kind, input.Name = "communication", metadata.SchemeName
					if metadata.FilterDef != nil {
						input.FilterPlanID = actualFilterID
					}
				}
				var actual service.BelloBuddyPlanDetail
				require.NoError(t, common.Unmarshal(request(http.MethodPost, "/plans", tokens[0], input, http.StatusOK), &actual))
				assert.JSONEq(t, string(raw), string(actual.Content))
				var roundtrip service.BelloBuddyPlanDetail
				require.NoError(t, common.Unmarshal(request(http.MethodGet, fmt.Sprintf("/plans/%d", actual.ID), tokens[0], nil, http.StatusOK), &roundtrip))
				assert.JSONEq(t, string(raw), string(roundtrip.Content))
				preservedRaw, err := common.Marshal(common.RawMessage(raw))
				require.NoError(t, err)
				assert.Equal(t, string(preservedRaw), string(roundtrip.Content), "完整 JSON 原文类型、数组顺序及缺失字段必须保留")
				if input.Kind == "filter" {
					actualFilterID = actual.ID
				}
			}
			filter := service.BelloBuddyPlanInput{Kind: "filter", Name: "筛选", Enabled: &enabled, SchemaVersion: 1,
				Content: common.RawMessage(`{"id":"legacy-filter","schema_defs":["FilterDef"],"name":"筛选","disabled":false,"enable_quick_screening":true,"enable_ai_screening":false,"any_fails":[{"conditions":[{"ages":[{"max":23,"exclusive_max":true}]}],"desc":"年龄"}],"any_passes":[],"compat":{"zero":0,"false":false,"large":9007199254740993,"null":null,"order":[3,1,2]}}`)}
			for _, content := range []string{
				`{"schema_defs":["FilterDef"],"name":"筛选","disabled":true,"any_fails":[],"any_passes":[]}`,
				`{"schema_defs":["FilterDef"],"name":"筛选","any_fails":[{"conditions":[]}],"any_passes":[]}`,
				`{"schema_defs":["FilterDef"],"name":"筛选","any_fails":[{"conditions":[{"ages":[{"min":30,"max":23}]}]}],"any_passes":[]}`,
				`{"schema_defs":["FilterDef"],"name":"筛选","any_fails":[],"any_passes":[],"enable_ai_screening":"true"}`,
			} {
				bad := filter
				bad.Content = common.RawMessage(content)
				request(http.MethodPost, "/plans", tokens[0], bad, http.StatusBadRequest)
			}
			var private service.BelloBuddyPlanDetail
			require.NoError(t, common.Unmarshal(request(http.MethodPost, "/plans", tokens[0], filter, http.StatusOK), &private))
			assert.Equal(t, users[0].Id, private.OwnerID)
			assert.JSONEq(t, string(filter.Content), string(private.Content))
			assert.Contains(t, string(private.Content), `"large":9007199254740993`, "不能通过浮点解析损失兼容整数精度")
			t.Run("完整正文TEXT边界", func(t *testing.T) {
				input := filter
				prefix := strings.TrimSuffix(string(input.Content), "}") + `,"padding":"`
				input.Content = common.RawMessage(prefix + strings.Repeat("x", service.BelloBuddyMaxPlanBytes-len(prefix)-2) + `"}`)
				created, err := service.SaveBelloBuddyPlan(users[0].Id, 0, false, input)
				require.NoError(t, err)
				assert.Equal(t, string(input.Content), string(created.Content))
				input.Content = common.RawMessage(prefix + strings.Repeat("x", service.BelloBuddyMaxPlanBytes-len(prefix)-1) + `"}`)
				_, err = service.SaveBelloBuddyPlan(users[0].Id, 0, false, input)
				assert.Equal(t, http.StatusBadRequest, service.BelloBuddyErrorResponse(err).Status)
			})
			privatePath := fmt.Sprintf("/plans/%d", private.ID)
			request(http.MethodGet, privatePath, tokens[1], nil, http.StatusNotFound)
			filter.ExpectedVersion = private.Version
			request(http.MethodPut, privatePath, tokens[1], filter, http.StatusNotFound)
			var updated service.BelloBuddyPlanDetail
			require.NoError(t, common.Unmarshal(request(http.MethodPut, privatePath, tokens[0], filter, http.StatusOK), &updated))
			assert.EqualValues(t, 2, updated.Version)
			assert.JSONEq(t, string(filter.Content), string(updated.Content))
			request(http.MethodPut, privatePath, tokens[0], filter, http.StatusConflict)
			// 同版本并发保存只允许一个写入者，另一请求不能覆盖赢家。
			t.Run("并发方案CAS", func(t *testing.T) {
				input := filter
				input.ExpectedVersion = 0
				created, err := service.SaveBelloBuddyPlan(users[0].Id, 0, false, input)
				require.NoError(t, err)
				input.ExpectedVersion = created.Version
				start, results := make(chan struct{}), make(chan error, 2)
				for range 2 {
					go func() {
						<-start
						_, err := service.SaveBelloBuddyPlan(users[0].Id, created.ID, false, input)
						results <- err
					}()
				}
				close(start)
				successes := 0
				for range 2 {
					if err := <-results; err == nil {
						successes++
					} else {
						assert.ErrorIs(t, err, service.ErrBelloBuddyConflict)
					}
				}
				assert.Equal(t, 1, successes)
				var winner model.BelloBuddyPlan
				require.NoError(t, db.First(&winner, created.ID).Error)
				assert.EqualValues(t, 2, winner.Version)
			})
			filter.ExpectedVersion = 0
			var public service.BelloBuddyPlanDetail
			require.NoError(t, common.Unmarshal(request(http.MethodPost, "/admin/plans", tokens[2], filter, http.StatusOK), &public))
			request(http.MethodGet, fmt.Sprintf("/plans/%d", public.ID), tokens[1], nil, http.StatusOK)
			request(http.MethodPut, fmt.Sprintf("/plans/%d", public.ID), tokens[0], filter, http.StatusNotFound)
			var listing struct {
				Items []service.BelloBuddyPlanDetail `json:"items"`
			}
			require.NoError(t, common.Unmarshal(request(http.MethodGet, "/plans", tokens[1], nil, http.StatusOK), &listing))
			require.Len(t, listing.Items, 1)
			assert.Nil(t, listing.Items[0].Content)
			communication := service.BelloBuddyPlanInput{Kind: "communication", Name: "沟通", Enabled: &enabled, SchemaVersion: 1, FilterPlanID: private.ID,
				Content: common.RawMessage(`{"id":"legacy-chat","schema_defs":["BeiliaoConfig"],"scheme_name":"沟通","disabled":false,"conditions_as_match":true,"filter_def":{"id":"legacy-filter"},"unknown":{"keep":false}}`)}
			for _, badBody := range []string{
				strings.Replace(string(communication.Content), `"legacy-filter"`, `"different-filter"`, 1),
				strings.Replace(string(communication.Content), `"filter_def":{"id":"legacy-filter"},`, "", 1),
				strings.Replace(string(communication.Content), `"filter_def":{"id":"legacy-filter"}`, `"filter_def":{}`, 1),
				strings.Replace(string(communication.Content), `"filter_def":{"id":"legacy-filter"}`, `"filter_def":{"id":123}`, 1),
			} {
				bad := communication
				bad.Content = common.RawMessage(badBody)
				request(http.MethodPost, "/plans", tokens[0], bad, http.StatusBadRequest)
			}
			missingAssociation := communication
			missingAssociation.FilterPlanID = 0
			missingAssociation.Content = common.RawMessage(strings.Replace(string(communication.Content), `"conditions_as_match":true`, `"conditions_as_match":false`, 1))
			request(http.MethodPost, "/plans", tokens[0], missingAssociation, http.StatusBadRequest)
			var chat service.BelloBuddyPlanDetail
			require.NoError(t, common.Unmarshal(request(http.MethodPost, "/plans", tokens[0], communication, http.StatusOK), &chat))
			request(http.MethodPost, "/admin/plans", tokens[2], communication, http.StatusNotFound)
			syncInput := service.BelloBuddySyncInput{DisplayName: "招聘账号", Jobs: []service.BelloBuddyJobInput{{JobID: "Job", Name: "售后主管", Online: &enabled}, {JobID: "job", Name: "另一个职位", Online: &enabled}}}
			var synced service.BelloBuddyJobsConfig
			require.NoError(t, common.Unmarshal(request(http.MethodPost, "/accounts/Account/jobs/sync", tokens[0], syncInput, http.StatusOK), &synced))
			require.Len(t, synced.Jobs, 2)
			assert.EqualValues(t, 2, synced.Account.Version)
			request(http.MethodGet, "/accounts/Account/jobs/config", tokens[1], nil, http.StatusNotFound)
			runtime := service.BelloBuddyRuntimeInput{JobIDs: []string{"Job"}, RunnerVersion: "test-1", SchemaVersion: 1}
			request(http.MethodPost, "/accounts/Account/runtime-config", tokens[0], runtime, http.StatusConflict)
			selection := service.BelloBuddyJobConfigInput{JobID: "Job", ExpectedVersion: 1, FilterMode: "skip_all", CommunicationMode: "plan", CommunicationID: chat.ID, GreetingLimit: &limit}
			request(http.MethodPut, "/accounts/Account/jobs/Job/config", tokens[0], selection, http.StatusOK)
			var snapshot service.BelloBuddyRuntimeConfig
			require.NoError(t, common.Unmarshal(request(http.MethodPost, "/accounts/Account/runtime-config", tokens[0], runtime, http.StatusOK), &snapshot))
			require.Len(t, snapshot.Plans, 2, "特殊打招呼模式仍需解析沟通关联筛选")
			assert.Equal(t, 0, snapshot.Jobs[0].GreetingLimit)
			assert.Equal(t, "BOT_NOT_CONFIGURED", snapshot.Jobs[0].BotStatus)
			assert.Equal(t, "legacy-filter", snapshot.PlanAliases[private.ID])
			assert.Equal(t, "legacy-chat", snapshot.PlanAliases[chat.ID])
			// 数据库存量关联被破坏时也要拒绝启动，不只依赖保存时校验。
			require.NoError(t, db.Model(&model.BelloBuddyPlan{}).Where("id = ?", chat.ID).Update("filter_plan_id", actualFilterID).Error)
			request(http.MethodPost, "/accounts/Account/runtime-config", tokens[0], runtime, http.StatusConflict)
			require.NoError(t, db.Model(&model.BelloBuddyPlan{}).Where("id = ?", chat.ID).Update("filter_plan_id", private.ID).Error)
			selection.ExpectedVersion, selection.FilterMode, selection.CommunicationMode, selection.CommunicationID = 2, "pass_all", "jump", 0
			invalid := selection
			invalid.JobID, invalid.ExpectedVersion, invalid.FilterMode, invalid.FilterPlanID = "job", 1, "plan", private.ID
			invalid.CommunicationMode = "unknown"
			request(http.MethodPut, "/accounts/Account/jobs/config/batch", tokens[0], map[string]any{"jobs": []service.BelloBuddyJobConfigInput{selection, invalid}}, http.StatusBadRequest)
			var after service.BelloBuddyJobsConfig
			require.NoError(t, common.Unmarshal(request(http.MethodGet, "/accounts/Account/jobs/config", tokens[0], nil, http.StatusOK), &after))
			assert.EqualValues(t, 2, after.Jobs[0].Version)
			assert.EqualValues(t, 3, after.Account.Version, "失败事务不得推进账号版本")
			request(http.MethodPut, "/accounts/Account/jobs/Job/config", tokens[0], selection, http.StatusOK)
			require.NoError(t, common.Unmarshal(request(http.MethodPost, "/accounts/Account/runtime-config", tokens[0], runtime, http.StatusOK), &snapshot))
			assert.Empty(t, snapshot.Plans, "特殊模式不生成空壳方案")
			assert.Equal(t, "pass_all", snapshot.Jobs[0].FilterMode)
			assert.Equal(t, "jump", snapshot.Jobs[0].CommunicationMode)
			request(http.MethodPost, "/accounts/Account/runtime-config", tokens[1], runtime, http.StatusNotFound)
			invalid = selection
			invalid.JobID, invalid.ExpectedVersion, invalid.FilterMode, invalid.FilterPlanID = "job", 1, "plan", private.ID
			request(http.MethodPost, "/accounts/Account/jobs/sync", tokens[1], syncInput, http.StatusOK)
			request(http.MethodPut, "/accounts/Account/jobs/job/config", tokens[1], invalid, http.StatusNotFound)
			// 私人方案禁用后保留绑定，下一次启动拒绝；不变成默认通过。
			selection.ExpectedVersion, selection.FilterMode, selection.FilterPlanID = 3, "plan", private.ID
			request(http.MethodPut, "/accounts/Account/jobs/Job/config", tokens[0], selection, http.StatusOK)
			publicSelection := selection
			publicSelection.JobID, publicSelection.ExpectedVersion, publicSelection.FilterPlanID = "job", 1, public.ID
			request(http.MethodPut, "/accounts/Account/jobs/job/config", tokens[0], publicSelection, http.StatusOK)
			bothJobs := runtime
			bothJobs.JobIDs = []string{"Job", "job"}
			require.NoError(t, common.Unmarshal(request(http.MethodPost, "/accounts/Account/runtime-config", tokens[0], bothJobs, http.StatusOK), &snapshot))
			require.Len(t, snapshot.Plans, 2, "旧正文 ID 相同的副本仍按服务端 ID 独立解析")
			assert.Equal(t, "legacy-filter", snapshot.PlanAliases[private.ID])
			assert.Equal(t, "legacy-filter", snapshot.PlanAliases[public.ID])
			disabled := false
			filter.Enabled, filter.ExpectedVersion = &disabled, 2
			filter.Content = common.RawMessage(strings.Replace(string(filter.Content), `"disabled":false`, `"disabled":true`, 1))
			request(http.MethodPut, privatePath, tokens[0], filter, http.StatusOK)
			request(http.MethodPost, "/accounts/Account/runtime-config", tokens[0], runtime, http.StatusConflict)
			require.NoError(t, db.AutoMigrate(tables...))
			var saved model.BelloBuddyPlan
			require.NoError(t, db.First(&saved, private.ID).Error)
			assert.Equal(t, string(filter.Content), saved.Content)
			var account model.BelloBuddyAccount
			require.NoError(t, db.Where("user_id = ?", users[0].Id).First(&account).Error)
			duplicate := account
			duplicate.ID = 0
			assert.Error(t, db.Create(&duplicate).Error, "重复迁移必须保留账号唯一性")
			var job model.BelloBuddyJob
			require.NoError(t, db.Where("account_id = ?", account.ID).First(&job).Error)
			job.ID = 0
			assert.Error(t, db.Create(&job).Error, "重复迁移必须保留职位唯一性")
			// 同用户大小写不同账号也保持隔离。
			request(http.MethodPost, "/accounts/account/jobs/sync", tokens[0], syncInput, http.StatusOK)
			// 首批有效方案按来源导入，沟通旧引用映射到服务端筛选 ID。
			importInput := service.BelloBuddyPlanImportInput{Namespace: "bellobuddy-local-v1"}
			for _, file := range []string{"communication-after-sales-1.json", "communication-after-sales-2.json", "filter-after-sales.json"} {
				raw, err := os.ReadFile(filepath.Join("testdata", "bellobuddy", file))
				require.NoError(t, err)
				var metadata struct {
					ID         string `json:"id"`
					Name       string `json:"name"`
					SchemeName string `json:"scheme_name"`
					FilterDef  *struct {
						ID string `json:"id"`
					} `json:"filter_def"`
				}
				require.NoError(t, common.Unmarshal(raw, &metadata))
				item := service.BelloBuddyPlanImportItem{BelloBuddyPlanInput: service.BelloBuddyPlanInput{Kind: "filter", Name: metadata.Name, Enabled: &enabled, SchemaVersion: 1, Content: raw}, SourceID: metadata.ID}
				if metadata.SchemeName != "" {
					item.Kind, item.Name = "communication", metadata.SchemeName
				}
				if metadata.FilterDef != nil {
					item.FilterSourceID = metadata.FilterDef.ID
				}
				importInput.Plans = append(importInput.Plans, item)
			}
			request(http.MethodPost, "/admin/plans/import", tokens[0], importInput, http.StatusForbidden)
			request(http.MethodPost, "/admin/plans/import", patRaw, importInput, http.StatusForbidden)
			var firstImport []service.BelloBuddyPlanImportResult
			require.NoError(t, common.Unmarshal(request(http.MethodPost, "/admin/plans/import", tokens[2], importInput, http.StatusOK), &firstImport))
			require.Len(t, firstImport, 3)
			assert.Equal(t, "filter", firstImport[0].Kind)
			for _, imported := range firstImport {
				assert.True(t, imported.Created)
				var detail service.BelloBuddyPlanDetail
				require.NoError(t, common.Unmarshal(request(http.MethodGet, fmt.Sprintf("/plans/%d", imported.PlanID), tokens[1], nil, http.StatusOK), &detail))
				for _, source := range importInput.Plans {
					if source.SourceID == imported.SourceID {
						expected, err := common.Marshal(source.Content)
						require.NoError(t, err)
						assert.Equal(t, string(expected), string(detail.Content), "导入须保留完整正文 %s", source.SourceID)
					}
				}
				t.Logf("真实方案映射：%s %s → id=%d/version=%d", imported.Kind, imported.SourceID, imported.PlanID, imported.Version)
			}
			var importedChat service.BelloBuddyPlanDetail
			require.NoError(t, common.Unmarshal(request(http.MethodGet, fmt.Sprintf("/plans/%d", firstImport[1].PlanID), tokens[1], nil, http.StatusOK), &importedChat))
			assert.Equal(t, firstImport[0].PlanID, importedChat.FilterPlanID)
			// 管理员禁用及编辑后再次导入，仅返回原映射和当前版本。
			originalFilter := importInput.Plans[2].BelloBuddyPlanInput
			originalFilter.Enabled, originalFilter.ExpectedVersion = &disabled, firstImport[0].Version
			originalFilter.Content = common.RawMessage(strings.Replace(string(originalFilter.Content), `"disabled": false`, `"disabled": true`, 1))
			request(http.MethodPut, fmt.Sprintf("/admin/plans/%d", firstImport[0].PlanID), tokens[2], originalFilter, http.StatusOK)
			var repeated []service.BelloBuddyPlanImportResult
			require.NoError(t, common.Unmarshal(request(http.MethodPost, "/admin/plans/import", tokens[2], importInput, http.StatusOK), &repeated))
			require.Len(t, repeated, 3)
			for i, imported := range repeated {
				assert.False(t, imported.Created)
				assert.Equal(t, firstImport[i].PlanID, imported.PlanID)
			}
			assert.EqualValues(t, 2, repeated[0].Version)
			var edited model.BelloBuddyPlan
			require.NoError(t, db.First(&edited, repeated[0].PlanID).Error)
			assert.False(t, edited.Enabled)
			assert.JSONEq(t, string(originalFilter.Content), edited.Content)
			changedSource := importInput
			changedSource.Plans = append([]service.BelloBuddyPlanImportItem(nil), importInput.Plans...)
			changedSource.Plans[2].Name = "不同来源内容"
			request(http.MethodPost, "/admin/plans/import", tokens[2], changedSource, http.StatusConflict)
			var beforeImportPlans, beforeImportSources int64
			require.NoError(t, db.Model(&model.BelloBuddyPlan{}).Count(&beforeImportPlans).Error)
			require.NoError(t, db.Model(&model.BelloBuddyPlanSource{}).Count(&beforeImportSources).Error)
			brokenImport := importInput
			brokenImport.Namespace, brokenImport.Plans = "broken-source", importInput.Plans[1:]
			// 售后方案 2 无关联；改变为不存在的关联，整批导入应回滚。
			brokenImport.Plans = append([]service.BelloBuddyPlanImportItem(nil), brokenImport.Plans...)
			brokenImport.Plans[0].FilterSourceID = "missing-filter"
			var brokenBody map[string]common.RawMessage
			require.NoError(t, common.Unmarshal(brokenImport.Plans[0].Content, &brokenBody))
			brokenBody["filter_def"] = common.RawMessage(`{"id":"missing-filter"}`)
			brokenRaw, err := common.Marshal(brokenBody)
			require.NoError(t, err)
			brokenImport.Plans[0].Content = brokenRaw
			request(http.MethodPost, "/admin/plans/import", tokens[2], brokenImport, http.StatusNotFound)
			var afterImportPlans, afterImportSources int64
			require.NoError(t, db.Model(&model.BelloBuddyPlan{}).Count(&afterImportPlans).Error)
			require.NoError(t, db.Model(&model.BelloBuddyPlanSource{}).Count(&afterImportSources).Error)
			assert.Equal(t, beforeImportPlans, afterImportPlans)
			assert.Equal(t, beforeImportSources, afterImportSources)
			// 同一来源的筛选方案已被管理员改为其他旧 ID，后续新增沟通来源不能误连。
			inconsistentImport := importInput
			inconsistentImport.Namespace = "inconsistent-filter-reference"
			inconsistentImport.Plans = []service.BelloBuddyPlanImportItem{importInput.Plans[2]}
			var inconsistent []service.BelloBuddyPlanImportResult
			require.NoError(t, common.Unmarshal(request(http.MethodPost, "/admin/plans/import", tokens[2], inconsistentImport, http.StatusOK), &inconsistent))
			require.NoError(t, db.Model(&model.BelloBuddyPlan{}).Where("id = ?", inconsistent[0].PlanID).Update("content", strings.Replace(string(importInput.Plans[2].Content), inconsistent[0].SourceID, "changed-legacy-id", 1)).Error)
			inconsistentImport.Plans = []service.BelloBuddyPlanImportItem{importInput.Plans[0]}
			request(http.MethodPost, "/admin/plans/import", tokens[2], inconsistentImport, http.StatusBadRequest)
			var rejectedSourceCount int64
			require.NoError(t, db.Model(&model.BelloBuddyPlanSource{}).Where("namespace = ?", inconsistentImport.Namespace).Count(&rejectedSourceCount).Error)
			assert.EqualValues(t, 1, rejectedSourceCount, "关联不一致不应遗留导入来源占位")
			if sourceRoot := os.Getenv("BELLOBUDDY_P2_SOURCE_ROOT"); sourceRoot != "" {
				t.Run("真实来源CLI导入", func(t *testing.T) {
					server := httptest.NewServer(engine)
					defer server.Close()
					mappingPath := filepath.Join(t.TempDir(), "mapping.json")
					for run := range 2 {
						command := exec.Command("node", "../tools/bellobuddy/import-plans.mjs", "--source-root", sourceRoot, "--base-url", server.URL, "--namespace", "p2-registered-live-source", "--mapping-output", mappingPath, "--apply")
						command.Env = append(os.Environ(), "DPIN_ACCESS_TOKEN="+tokens[2])
						output, err := command.CombinedOutput()
						require.NoError(t, err, "%s", output)
						var mapping struct {
							Plans []service.BelloBuddyPlanImportResult `json:"plans"`
						}
						require.NoError(t, common.Unmarshal(output, &mapping))
						require.Len(t, mapping.Plans, 3)
						stored, err := os.ReadFile(mappingPath)
						require.NoError(t, err)
						assert.Equal(t, string(output), string(stored))
						assert.NotContains(t, string(stored), tokens[2])
						for _, entry := range mapping.Plans {
							assert.Equal(t, run == 0, entry.Created)
							var detail model.BelloBuddyPlan
							require.NoError(t, db.First(&detail, entry.PlanID).Error)
							for _, fixture := range importInput.Plans {
								if fixture.SourceID == entry.SourceID {
									expected, err := common.Marshal(fixture.Content)
									require.NoError(t, err)
									assert.Equal(t, string(expected), detail.Content, "实际来源与完整基准正文一致")
								}
							}
							t.Logf("实际来源CLI，第%d次：%s %s → id=%d/version=%d，新增=%t", run+1, entry.Kind, entry.SourceID, entry.PlanID, entry.Version, entry.Created)
						}
					}
				})
			}
			require.NoError(t, db.AutoMigrate(tables...))
			var preserved model.BelloBuddyPlanSource
			require.NoError(t, db.Where("plan_id = ?", firstImport[0].PlanID).First(&preserved).Error)
			assert.Equal(t, importInput.Namespace, preserved.Namespace)
			preserved.ID = 0
			assert.Error(t, db.Create(&preserved).Error, "来源唯一约束在重复迁移后仍有效")
			// 同名职位必须由管理员逐项确认映射；跨用户共用机器人，不自动绑定其余同名职位。
			channel := model.Channel{Type: constant.ChannelTypeMaxKB, Key: "test-upstream-secret", Status: common.ChannelStatusEnabled, Name: "招聘机器人", Models: "recruitment-bot", Group: "default"}
			require.NoError(t, db.Create(&channel).Error)
			bindingInput := service.BelloBuddyBindingInput{SharedKey: "company-sales-support", Name: "共享售后岗位", ChannelID: channel.Id, Model: "recruitment-bot", Enabled: &enabled}
			request(http.MethodPost, "/admin/job-bindings", tokens[0], bindingInput, http.StatusForbidden)
			request(http.MethodPost, "/admin/job-bindings", patRaw, bindingInput, http.StatusForbidden)
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("type", constant.ChannelTypeOpenAI).Error)
			request(http.MethodPost, "/admin/job-bindings", tokens[2], bindingInput, http.StatusBadRequest)
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("type", constant.ChannelTypeMaxKB).Error)
			var binding model.BelloBuddyJobBinding
			require.NoError(t, common.Unmarshal(request(http.MethodPost, "/admin/job-bindings", tokens[2], bindingInput, http.StatusOK), &binding))
			request(http.MethodPost, "/admin/job-bindings", tokens[2], bindingInput, http.StatusConflict)
			mappedSync := service.BelloBuddySyncInput{DisplayName: "显式映射测试账号", Jobs: []service.BelloBuddyJobInput{{JobID: "target", Name: "映射测试岗位", City: "北京", Online: &enabled}, {JobID: "unconfirmed", Name: "映射测试岗位", City: "北京", Online: &enabled}}}
			mappedConfig := service.BelloBuddyJobConfigInput{ExpectedVersion: 1, FilterMode: "pass_all", CommunicationMode: "jump", GreetingLimit: &limit}
			for _, token := range tokens[:2] {
				request(http.MethodPost, "/accounts/mapped-account/jobs/sync", token, mappedSync, http.StatusOK)
				for _, jobID := range []string{"target", "unconfirmed"} {
					mappedConfig.JobID = jobID
					request(http.MethodPut, "/accounts/mapped-account/jobs/"+jobID+"/config", token, mappedConfig, http.StatusOK)
				}
			}
			candidatePath := "/admin/job-bindings/candidates?name=映射测试岗位&city=北京&unbound=true"
			request(http.MethodGet, candidatePath, tokens[0], nil, http.StatusForbidden)
			var candidates struct {
				Items []service.BelloBuddyJobCandidate `json:"items"`
				Total int                              `json:"total"`
			}
			require.NoError(t, common.Unmarshal(request(http.MethodGet, candidatePath, tokens[2], nil, http.StatusOK), &candidates))
			require.Len(t, candidates.Items, 4)
			mapping := service.BelloBuddyBindingJobsInput{ExpectedVersion: binding.Version}
			for _, candidate := range candidates.Items {
				assert.Equal(t, 0, candidate.BindingID)
				if candidate.JobID == "target" {
					mapping.Jobs = append(mapping.Jobs, service.BelloBuddyJobMappingInput{JobConfigID: candidate.JobConfigID, ExpectedVersion: candidate.Version})
				}
			}
			require.Len(t, mapping.Jobs, 2)
			mappingPath := fmt.Sprintf("/admin/job-bindings/%d/jobs", binding.ID)
			request(http.MethodPut, mappingPath, tokens[0], mapping, http.StatusForbidden)
			badMapping := mapping
			badMapping.Jobs = append([]service.BelloBuddyJobMappingInput(nil), mapping.Jobs...)
			badMapping.Jobs[1].ExpectedVersion++
			var beforeMapping service.BelloBuddyJobsConfig
			require.NoError(t, common.Unmarshal(request(http.MethodGet, "/accounts/mapped-account/jobs/config", tokens[0], nil, http.StatusOK), &beforeMapping))
			request(http.MethodPut, mappingPath, tokens[2], badMapping, http.StatusConflict)
			var afterMapping service.BelloBuddyJobsConfig
			require.NoError(t, common.Unmarshal(request(http.MethodGet, "/accounts/mapped-account/jobs/config", tokens[0], nil, http.StatusOK), &afterMapping))
			assert.Equal(t, beforeMapping, afterMapping, "任一职位版本冲突时跨用户映射全部回滚")
			request(http.MethodPut, mappingPath, tokens[2], mapping, http.StatusOK)
			request(http.MethodPut, mappingPath, tokens[2], mapping, http.StatusConflict)
			mappedRuntime := service.BelloBuddyRuntimeInput{JobIDs: []string{"target", "unconfirmed"}, RunnerVersion: "test-1", SchemaVersion: 1}
			for _, token := range tokens[:2] {
				body := request(http.MethodPost, "/accounts/mapped-account/runtime-config", token, mappedRuntime, http.StatusOK)
				assert.NotContains(t, string(body), "channel_id")
				assert.NotContains(t, string(body), "test-upstream-secret")
				require.NoError(t, common.Unmarshal(body, &snapshot))
				require.Len(t, snapshot.Jobs, 2)
				for _, configured := range snapshot.Jobs {
					if configured.JobID == "target" {
						assert.Equal(t, "BOT_CONFIGURED", configured.BotStatus)
						assert.Equal(t, binding.Version, configured.BotBindingVersion)
					} else {
						assert.Equal(t, "BOT_NOT_CONFIGURED", configured.BotStatus)
					}
				}
			}
			bindingInput.Enabled, bindingInput.ExpectedVersion = &disabled, binding.Version
			bindingPath := fmt.Sprintf("/admin/job-bindings/%d", binding.ID)
			require.NoError(t, common.Unmarshal(request(http.MethodPut, bindingPath, tokens[2], bindingInput, http.StatusOK), &binding))
			request(http.MethodPut, mappingPath, tokens[2], mapping, http.StatusConflict)
			require.NoError(t, common.Unmarshal(request(http.MethodPost, "/accounts/mapped-account/runtime-config", tokens[0], mappedRuntime, http.StatusOK), &snapshot))
			assert.Equal(t, "BOT_NOT_CONFIGURED", snapshot.Jobs[0].BotStatus, "机器人未配置不阻止本地流程启动")
			assert.EqualValues(t, 2, snapshot.Jobs[0].BotBindingVersion)
			bindingInput.Enabled, bindingInput.ExpectedVersion = &enabled, binding.Version
			request(http.MethodPut, bindingPath, tokens[2], bindingInput, http.StatusOK)
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("type", constant.ChannelTypeOpenAI).Error)
			require.NoError(t, common.Unmarshal(request(http.MethodPost, "/accounts/mapped-account/runtime-config", tokens[0], mappedRuntime, http.StatusOK), &snapshot))
			assert.Equal(t, "BOT_UNAVAILABLE", snapshot.Jobs[0].BotStatus, "渠道类型改变不能把普通模型误当作岗位机器人")
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("type", constant.ChannelTypeMaxKB).Error)
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("status", common.ChannelStatusManuallyDisabled).Error)
			require.NoError(t, common.Unmarshal(request(http.MethodPost, "/accounts/mapped-account/runtime-config", tokens[0], mappedRuntime, http.StatusOK), &snapshot))
			assert.Equal(t, "BOT_UNAVAILABLE", snapshot.Jobs[0].BotStatus)
			require.NoError(t, db.AutoMigrate(tables...))
			var preservedBinding model.BelloBuddyJobBinding
			require.NoError(t, db.First(&preservedBinding, binding.ID).Error)
			assert.EqualValues(t, 3, preservedBinding.Version)
			preservedBinding.ID = 0
			assert.Error(t, db.Create(&preservedBinding).Error, "重复迁移保留共享岗位唯一性")
			t.Run("用户态AI桥接", func(t *testing.T) {
				runBelloBuddyAIBridge(t, db, engine, users, tokens)
			})
			// 撤销会话后，持有原访问 Token 也无法获取方案。
			identity, _, err := service.ParseDashboardAccessToken(tokens[0])
			require.NoError(t, err)
			_, err = model.RevokeUserSession(users[0].Id, identity.SessionID, "test")
			require.NoError(t, err)
			request(http.MethodGet, privatePath, tokens[0], nil, http.StatusUnauthorized)
			request(http.MethodGet, "/capabilities", tokens[0], nil, http.StatusUnauthorized)
			request(http.MethodPost, "/ai/screen", tokens[0], nil, http.StatusUnauthorized)
			request(http.MethodPost, "/ai/reply", tokens[0], nil, http.StatusUnauthorized)
		})
	}
}

// 使用真实中继外的既有会话服务及控制器验证 Chromium Cookie 行为。
// /test/* 只存在于临时 httptest 服务器，不注册到应用路由。
func runBelloBuddyElectronAuthSpike(t *testing.T, db *gorm.DB, userID int) {
	t.Helper()
	oldSecure, oldTrusted := common.SessionCookieSecure, common.SessionCookieTrustedURLs
	t.Cleanup(func() {
		common.SessionCookieSecure, common.SessionCookieTrustedURLs = oldSecure, oldTrusted
	})
	script, err := filepath.Abs("../tools/bellobuddy/electron-auth-spike.cjs")
	require.NoError(t, err)
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("Secure=%t", secure), func(t *testing.T) {
			common.SessionCookieSecure, common.SessionCookieTrustedURLs = secure, nil
			gin.SetMode(gin.TestMode)
			engine := gin.New()
			engine.POST("/test/login", func(c *gin.Context) {
				bundle, err := service.CreateLoginSession(userID, "password", c.ClientIP(), c.Request.UserAgent())
				if err != nil {
					c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"success": false})
					return
				}
				service.WriteRefreshCookie(c, bundle.RefreshToken)
				c.JSON(http.StatusOK, gin.H{"success": true, "data": bundle})
			})
			engine.POST("/test/expire-grace", func(c *gin.Context) {
				result := db.Model(&model.UserSession{}).Where("sid = ? AND user_id = ?", c.GetHeader("X-Auth-Session"), userID).Update("previous_valid_until", time.Now().Unix()-1)
				if result.Error != nil || result.RowsAffected != 1 {
					c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"success": false})
					return
				}
				c.JSON(http.StatusOK, gin.H{"success": true})
			})
			var refreshCalls atomic.Int64
			var holdRefresh atomic.Bool
			refreshEntered, releaseRefresh := make(chan struct{}), make(chan struct{})
			engine.POST("/api/user/auth/refresh", middleware.SessionCookieOriginGuard(), func(c *gin.Context) {
				refreshCalls.Add(1)
				if holdRefresh.CompareAndSwap(true, false) {
					close(refreshEntered)
					select {
					case <-releaseRefresh:
					case <-c.Request.Context().Done():
						return
					}
				}
				controller.RefreshAuth(c)
			})
			engine.POST("/api/user/auth/logout", middleware.SessionCookieOriginGuard(), controller.AuthLogout)
			engine.GET("/api/user/self", middleware.UserAuth(), controller.GetSelf)
			engine.GET("/api/bellobuddy/spike", middleware.UserAuth(), middleware.BelloBuddySessionRequired(), func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"user_id": c.GetInt("id")}})
			})
			engine.GET("/api/bellobuddy/spike/upstream", middleware.UserAuth(), middleware.BelloBuddySessionRequired(), func(c *gin.Context) {
				c.JSON(http.StatusUnauthorized, gin.H{"success": false, "code": "AI_UPSTREAM_REJECTED", "source": "upstream", "request_id": "spike-upstream"})
			})
			engine.GET("/api/bellobuddy/spike/quota", middleware.UserAuth(), middleware.BelloBuddySessionRequired(), func(c *gin.Context) {
				c.JSON(http.StatusForbidden, gin.H{"success": false, "code": "AI_QUOTA_EXHAUSTED", "source": "dpin", "request_id": "spike-quota"})
			})
			registerBelloBuddyRoutes(engine.Group("/api"))
			t.Cleanup(func() {
				require.NoError(t, db.Where("owner_id = ? AND name LIKE ?", userID, "__electron_plan_spike_%").Delete(&model.BelloBuddyPlan{}).Error)
			})
			engine.POST("/test/refresh-count", func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"count": refreshCalls.Load()}})
			})
			engine.POST("/test/hold-refresh", func(c *gin.Context) {
				holdRefresh.Store(true)
				c.JSON(http.StatusOK, gin.H{"success": true})
			})
			engine.POST("/test/wait-refresh", func(c *gin.Context) {
				select {
				case <-refreshEntered:
					c.JSON(http.StatusOK, gin.H{"success": true})
				case <-c.Request.Context().Done():
				}
			})
			engine.POST("/test/release-refresh", func(c *gin.Context) {
				close(releaseRefresh)
				c.JSON(http.StatusOK, gin.H{"success": true})
			})
			var server *httptest.Server
			pin := ""
			if secure {
				server = httptest.NewTLSServer(engine)
				pin = fmt.Sprintf("%x", sha256.Sum256(server.Certificate().Raw))
			} else {
				server = httptest.NewServer(engine)
			}
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Getenv("BELLOBUDDY_ELECTRON_BINARY"), script, server.URL, pin, t.TempDir())
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "ELECTRON_RUN_AS_NODE=") {
					command.Env = append(command.Env, entry)
				}
			}
			output, err := command.CombinedOutput()
			require.NoError(t, err, "Electron 网络验证结果：%s", output)
			var result struct {
				Electron string   `json:"electron"`
				Protocol string   `json:"protocol"`
				Passed   []string `json:"passed"`
			}
			var found bool
			for line := range strings.SplitSeq(string(output), "\n") {
				if strings.HasPrefix(line, `{"electron":`) {
					require.NoError(t, common.UnmarshalJsonStr(line, &result))
					found = true
				}
			}
			require.True(t, found, "没有收到 Electron 的验证结果")
			assert.NotEmpty(t, result.Electron)
			assert.GreaterOrEqual(t, len(result.Passed), 16)
			t.Logf("Electron %s %s：%v", result.Electron, result.Protocol, result.Passed)
		})
	}
}

// 通过实际路由、原生中继和真实数据库保护授权、会话作用域及扣费/退款契约。
func runBelloBuddyAIBridge(t *testing.T, db *gorm.DB, engine *gin.Engine, users []model.User, sessions []string) {
	t.Helper()
	oldCount, oldTrust := constant.CountToken, operation_setting.GetQuotaSetting().TrustQuotaUSD
	constant.CountToken, operation_setting.GetQuotaSetting().TrustQuotaUSD = true, 0
	oldMemory, oldBatch, oldRetry := common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.RetryTimes
	oldRatio, oldCompletion := ratio_setting.ModelRatio2JSONString(), ratio_setting.CompletionRatio2JSONString()
	common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.RetryTimes = false, false, 2
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"jev-latest":1,"bello-test-bot":1}`))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"jev-latest":1,"bello-test-bot":1}`))
	t.Cleanup(func() {
		constant.CountToken, operation_setting.GetQuotaSetting().TrustQuotaUSD = oldCount, oldTrust
		common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.RetryTimes = oldMemory, oldBatch, oldRetry
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldRatio))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(oldCompletion))
	})
	service.InitHttpClient()
	oldLogDB, oldLogType := model.LOG_DB, common.LogDatabaseType()
	logDB, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "separate-consume-log.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, logDB.AutoMigrate(&model.Log{}, &model.AuditLog{}))
	model.LOG_DB = logDB
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.LOG_DB = oldLogDB
		common.SetLogDatabaseType(oldLogType)
		sqlLogDB, err := logDB.DB()
		require.NoError(t, err)
		require.NoError(t, sqlLogDB.Close())
	})
	for _, user := range users[:2] {
		require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Updates(map[string]any{"quota": 1000000, "used_quota": 0, "setting": `{"billing_preference":"wallet_only"}`}).Error)
	}
	const appID = "00000000-0000-4000-8000-000000000001"
	const chatID = "00000000-0000-4000-8000-000000000002"
	var calls atomic.Int32
	var behavior atomic.Int32
	var reservationBefore atomic.Int64
	received := make(chan common.RawMessage, 16)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		if !assert.NoError(t, err) {
			w.WriteHeader(500)
			return
		}
		received <- body
		assert.Equal(t, "Bearer test-upstream-secret", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if behavior.Load() != 0 {
			var reservedUser model.User
			if assert.NoError(t, db.First(&reservedUser, users[0].Id).Error) {
				assert.Less(t, reservedUser.Quota, int(reservationBefore.Load()), "请求已实际预扣，然后才能验证退款")
			}
		}
		if behavior.Load() == 2 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"message":"test-upstream-secret","code":"upstream_denied"}}`)
			return
		}
		if r.URL.Path == "/v1/systemone" {
			_, _ = io.WriteString(w, `{"answers":{"screen":"pass"},"usage":{"input_tokens":12,"output_tokens":3}}`)
			return
		}
		assert.Equal(t, "/chat/api/"+appID+"/chat/completions", r.URL.Path)
		usage := `,"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25,"usage_source":"upstream"}`
		if behavior.Load() == 1 {
			usage = ""
		}
		if behavior.Load() == 3 {
			usage = `,"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0,"usage_source":"upstream"}`
		}
		_, _ = io.WriteString(w, `{"id":"test-response","choices":[{"index":0,"chat_id":"`+chatID+`","message":{"role":"assistant","content":"测试建议文本"},"finish_reason":"stop"}]`+usage+`}`)
	}))
	t.Cleanup(upstream.Close)
	base, mapping := upstream.URL, `{"bello-test-bot":"`+appID+`"}`
	screen := model.Channel{Type: constant.ChannelTypeTypeSafe, Key: "test-upstream-secret", Status: common.ChannelStatusEnabled, Name: "AI测试筛选", Models: service.BelloBuddyScreenModel, Group: "default", BaseURL: &base}
	reply := model.Channel{Type: constant.ChannelTypeMaxKB, Key: "test-upstream-secret", Status: common.ChannelStatusEnabled, Name: "AI测试岗位机器人", Models: "bello-test-bot", Group: "default", BaseURL: &base, ModelMapping: &mapping}
	for _, channel := range []*model.Channel{&screen, &reply} {
		require.NoError(t, db.Create(channel).Error)
		require.NoError(t, db.Create(&model.Ability{Group: "default", Model: channel.Models, ChannelId: channel.Id, Enabled: true}).Error)
	}
	wrong := model.Channel{Type: constant.ChannelTypeOpenAI, Key: "wrong-key", Status: common.ChannelStatusEnabled, Name: "错误协议测试", Models: service.BelloBuddyScreenModel, Group: "default", BaseURL: &base}
	require.NoError(t, db.Create(&wrong).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: wrong.Models, ChannelId: wrong.Id, Enabled: true, Priority: common.GetPointer(int64(1000))}).Error)
	filter := model.BelloBuddyPlan{Kind: "filter", Name: "AI桥接测试", Visibility: "public", Enabled: true, Content: `{"enable_ai_screening":true}`, SchemaVersion: 1, Version: 1}
	require.NoError(t, db.Create(&filter).Error)
	communication := model.BelloBuddyPlan{Kind: "communication", Name: "AI沟通测试", Visibility: "public", Enabled: true, Content: `{}`, FilterPlanID: filter.ID, SchemaVersion: 1, Version: 1}
	require.NoError(t, db.Create(&communication).Error)
	binding := model.BelloBuddyJobBinding{SharedKey: "ai-test-binding", KeyHash: model.BelloBuddyIdentityHash("ai-test-binding"), Name: "AI岗位", ChannelID: reply.Id, Model: reply.Models, Enabled: true, Version: 1}
	require.NoError(t, db.Create(&binding).Error)
	for _, user := range users[:2] {
		account := model.BelloBuddyAccount{UserID: user.Id, AccountKey: "ai-account", AccountHash: model.BelloBuddyIdentityHash("ai-account"), Platform: "boss", Version: 1}
		require.NoError(t, db.Create(&account).Error)
		for _, id := range []string{"ai-job", "ai-other-job"} {
			job := model.BelloBuddyJob{AccountID: account.ID, JobID: id, JobHash: model.BelloBuddyIdentityHash(id), Online: true, FilterMode: "plan", FilterPlanID: filter.ID, CommunicationMode: "plan", CommunicationID: communication.ID, BindingID: binding.ID, Version: 1}
			require.NoError(t, db.Create(&job).Error)
		}
	}
	invoke := func(path, session string, input service.BelloBuddyAIInput, status int, headers map[string]string) common.RawMessage {
		t.Helper()
		body, err := common.Marshal(input)
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, "/api/bellobuddy/ai/"+path, strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+session)
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		require.Equal(t, status, w.Code, w.Body.String())
		assert.NotContains(t, w.Body.String(), "test-upstream-secret")
		assert.NotContains(t, w.Body.String(), chatID)
		return common.RawMessage(w.Body.Bytes())
	}
	// 能力列表按真实用户组与渠道协议计算，不能把 OpenAI 渠道当作 JEV。
	capRequest := httptest.NewRequest(http.MethodGet, "/api/bellobuddy/capabilities", nil)
	capRequest.Header.Set("Authorization", "Bearer "+sessions[0])
	capResponse := httptest.NewRecorder()
	engine.ServeHTTP(capResponse, capRequest)
	require.Equal(t, 200, capResponse.Code)
	var caps struct {
		Data struct {
			Screen struct {
				Available bool     `json:"available"`
				Models    []string `json:"models"`
			} `json:"ai_screen"`
			Reply struct {
				Available bool     `json:"available"`
				Models    []string `json:"models"`
			} `json:"ai_reply"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(capResponse.Body.Bytes(), &caps))
	assert.True(t, caps.Data.Screen.Available)
	assert.Equal(t, []string{service.BelloBuddyScreenModel}, caps.Data.Screen.Models)
	assert.True(t, caps.Data.Reply.Available)
	assert.Equal(t, []string{reply.Models}, caps.Data.Reply.Models)
	input := service.BelloBuddyAIInput{AccountKey: "ai-account", JobID: "ai-job", JobVersion: 1, SchemaVersion: 1, CandidateID: "candidate-1", FilterPlanID: filter.ID, FilterVersion: 1, BindingVersion: 1}
	input.Request = common.RawMessage(`{"state":{"future_integer":9007199254740993,"history":[false,0,null]},"questions":{"screen":{"type":"choice","instructions":"依据当前方案筛选","criteria":{"pass":"符合","reject":"不符"},"future":{"zero":0}}},"answers":{}}`)
	invoke("screen", "local-only", input, 401, nil)
	invoke("screen", "nap_bellobuddy-personal-token-test", input, 403, nil)
	stale := input
	stale.JobVersion++
	invoke("screen", sessions[0], stale, 409, nil)
	result := invoke("screen", sessions[0], input, 200, map[string]string{"Sec-WebSocket-Protocol": "openai-insecure-api-key.attacker-key"})
	assert.Contains(t, string(result), `"screen":"pass"`)
	require.Len(t, received, 1)
	forwarded := <-received
	assert.Contains(t, string(forwarded), "9007199254740993")
	assert.Contains(t, string(forwarded), `"future":{"zero":0}`)
	assert.Contains(t, string(forwarded), `"model":"jev-latest"`)
	var owner model.User
	require.NoError(t, db.First(&owner, users[0].Id).Error)
	assert.Equal(t, 999985, owner.Quota)
	assert.Equal(t, 15, owner.UsedQuota)
	internal, err := model.GetOrCreateBelloBuddyRelayToken(users[0].Id)
	require.NoError(t, err)
	again, err := model.GetOrCreateBelloBuddyRelayToken(users[0].Id)
	require.NoError(t, err)
	assert.Equal(t, internal.Id, again.Id)
	assert.Equal(t, model.TokenManagedByBelloBuddy, internal.ManagedBy)
	assert.NotContains(t, string(result), internal.Key)
	assert.Equal(t, 15, internal.UsedQuota)
	// 普通令牌路由既不能消费应用内部 Key，也不能通过请求头伪造桥接标记。
	engine.POST("/v1/bello-test", middleware.TokenAuth(), func(c *gin.Context) { c.Status(200) })
	req := httptest.NewRequest(http.MethodPost, "/v1/bello-test", nil)
	req.Header.Set("Authorization", "Bearer "+internal.Key)
	req.Header.Set("bello_relay_internal", "true")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	assert.Equal(t, 403, w.Code)
	// 真实导出控制器：单个和批量查询均不得返回完整内部凭据。
	for _, batch := range []bool{false, true} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", users[0].Id)
		c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(internal.Id)}}
		c.Request = httptest.NewRequest(http.MethodPost, "/test-token-key", strings.NewReader(fmt.Sprintf(`{"ids":[1,%d]}`, internal.Id)))
		c.Request.Header.Set("Content-Type", "application/json")
		if batch {
			controller.GetTokenKeysBatch(c)
		} else {
			controller.GetTokenKey(c)
		}
		assert.Equal(t, 403, w.Code)
		assert.NotContains(t, w.Body.String(), internal.Key)
		assert.NotContains(t, w.Body.String(), "legacybellobuddyfixturecredential")
	}
	w = httptest.NewRecorder()
	managedContext, _ := gin.CreateTestContext(w)
	managedContext.Set("id", users[0].Id)
	managedContext.Request = httptest.NewRequest(http.MethodPut, "/test-token-update", strings.NewReader(fmt.Sprintf(`{"id":%d,"status":1,"unlimited_quota":true}`, internal.Id)))
	managedContext.Request.Header.Set("Content-Type", "application/json")
	controller.UpdateToken(managedContext)
	assert.Equal(t, 403, w.Code)
	// 原有普通用户凭据不受内部凭据限制影响。
	w = httptest.NewRecorder()
	normalContext, _ := gin.CreateTestContext(w)
	normalContext.Set("id", users[0].Id)
	normalContext.Params = gin.Params{{Key: "id", Value: "1"}}
	normalContext.Request = httptest.NewRequest(http.MethodPost, "/test-token-key", nil)
	controller.GetTokenKey(normalContext)
	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "legacybellobuddyfixturecredential")
	// 请求不能覆盖服务端模型、计费乘数或渠道，禁用 AI 的方案不得调用。
	for _, raw := range []string{`{"model":"other","state":"x","questions":{}}`, `{"max_tokens":2147483648}`, `{"n":2}`, `{"channel_id":1}`} {
		bad := input
		bad.Request = common.RawMessage(raw)
		invoke("screen", sessions[0], bad, 400, nil)
	}
	require.NoError(t, db.Model(&filter).Update("content", `{"enable_ai_screening":false}`).Error)
	invoke("screen", sessions[0], input, 400, nil)
	require.NoError(t, db.Model(&filter).Update("content", `{"enable_ai_screening":true}`).Error)
	assert.EqualValues(t, 1, calls.Load())
	input.Request = nil
	input.Input = "首轮候选人上下文与问题"
	result = invoke("reply", sessions[0], input, 200, nil)
	<-received
	var response struct {
		Data struct {
			Reference string `json:"conversation_ref"`
			Text      string `json:"text"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(result, &response))
	require.NotEmpty(t, response.Data.Reference)
	assert.Equal(t, "测试建议文本", response.Data.Text)
	identity, _, err := service.ParseDashboardAccessToken(sessions[0])
	require.NoError(t, err)
	route, err := service.ResolveBelloBuddyAIRoute(identity, input, true)
	require.NoError(t, err)
	for _, dimension := range []string{"auth_version", "account", "binding_id", "binding_version", "channel", "channel_mapping"} {
		scope := route.Scope
		switch dimension {
		case "auth_version":
			scope.AuthVersion++
		case "account":
			scope.AccountID++
		case "binding_id":
			scope.BindingID++
		case "binding_version":
			scope.BindingVersion++
		case "channel":
			scope.ChannelID++
		case "channel_mapping":
			scope.ChannelSignature = "changed"
		}
		_, err := service.OpenBelloBuddyConversation(response.Data.Reference, scope)
		require.Error(t, err, dimension)
	}
	expired := input
	expired.ConversationRef = expireBelloBuddyReference(t, response.Data.Reference)
	invoke("reply", sessions[0], expired, 409, nil)
	originalSecret := common.SessionSecret
	common.SessionSecret = "rotated-bellobuddy-conversation-secret"
	_, err = service.OpenBelloBuddyConversation(response.Data.Reference, route.Scope)
	require.Error(t, err)
	common.SessionSecret = originalSecret
	input.ConversationRef = response.Data.Reference
	invoke("reply", sessions[0], input, 200, nil)
	var next map[string]common.RawMessage
	require.NoError(t, common.Unmarshal(<-received, &next))
	assert.JSONEq(t, `"`+chatID+`"`, string(next["chat_id"]))
	for _, change := range []string{"candidate", "job", "user", "tamper", "binding"} {
		bad := input
		session := sessions[0]
		switch change {
		case "candidate":
			bad.CandidateID = "candidate-2"
		case "job":
			bad.JobID = "ai-other-job"
		case "user":
			session = sessions[1]
		case "tamper":
			bad.ConversationRef = "!" + bad.ConversationRef
		case "binding":
			bad.BindingVersion++
		}
		invoke("reply", session, bad, 409, nil)
	}
	assert.EqualValues(t, 3, calls.Load())
	// 固定渠道仍受能力记录和内部令牌模型限制约束，拒绝时不提交上游。
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", reply.Id).Update("enabled", false).Error)
	invoke("reply", sessions[0], input, 403, nil)
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", reply.Id).Update("enabled", true).Error)
	require.NoError(t, db.Model(internal).Updates(map[string]any{"model_limits_enabled": true, "model_limits": "jev-latest"}).Error)
	invoke("reply", sessions[0], input, 403, nil)
	require.NoError(t, db.Model(internal).Update("model_limits_enabled", false).Error)
	// 上游可能已处理的失败不重试；缺少真实用量不透出建议文本，原生预扣全部退款。
	require.NoError(t, db.First(&owner, users[0].Id).Error)
	quotaBefore, usedBefore := owner.Quota, owner.UsedQuota
	assert.Equal(t, 999935, quotaBefore)
	assert.Equal(t, 65, usedBefore)
	reservationBefore.Store(int64(quotaBefore))
	behavior.Store(1)
	result = invoke("reply", sessions[0], input, 502, nil)
	<-received
	assert.Contains(t, string(result), `"source":"upstream"`)
	assert.NotContains(t, string(result), "测试建议文本")
	assert.EqualValues(t, 4, calls.Load())
	require.Eventually(t, func() bool {
		var user model.User
		var credential model.Token
		return db.First(&user, users[0].Id).Error == nil && db.First(&credential, internal.Id).Error == nil && user.Quota == quotaBefore && user.UsedQuota == usedBefore && credential.UsedQuota == usedBefore
	}, 2*time.Second, 10*time.Millisecond, "原生异步退款恢复钱包与令牌账本")
	behavior.Store(2)
	result = invoke("reply", sessions[0], input, 502, nil)
	<-received
	assert.Contains(t, string(result), `"source":"upstream"`)
	assert.EqualValues(t, 5, calls.Load())
	require.Eventually(t, func() bool {
		var user model.User
		var credential model.Token
		return db.First(&user, users[0].Id).Error == nil && db.First(&credential, internal.Id).Error == nil && user.Quota == quotaBefore && credential.UsedQuota == usedBefore
	}, 2*time.Second, 10*time.Millisecond)
	behavior.Store(0)
	require.NoError(t, db.Model(&owner).Update("quota", 0).Error)
	result = invoke("reply", sessions[0], input, 403, nil)
	assert.Contains(t, string(result), `"code":"AI_QUOTA_EXHAUSTED"`)
	assert.EqualValues(t, 5, calls.Load())
	require.NoError(t, db.Model(&owner).Update("quota", quotaBefore).Error)
	// 渠道映射变化使旧引用失效，绝不默默新建对话。
	changedMapping := `{"bello-test-bot":"00000000-0000-4000-8000-000000000003"}`
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", reply.Id).Update("model_mapping", changedMapping).Error)
	invoke("reply", sessions[0], input, 409, nil)
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", reply.Id).Update("model_mapping", mapping).Error)
	// 真实零用量可正常完成，并归还全部预扣；独立日志库保留准确用量。
	behavior.Store(3)
	reservationBefore.Store(int64(quotaBefore))
	result = invoke("reply", sessions[0], input, 200, nil)
	<-received
	assert.Contains(t, string(result), `"total_tokens":0`)
	require.NoError(t, db.First(&owner, users[0].Id).Error)
	assert.Equal(t, quotaBefore, owner.Quota)
	assert.Equal(t, usedBefore, owner.UsedQuota)
	var logs []model.Log
	require.NoError(t, logDB.Where("user_id = ? AND type = ?", users[0].Id, model.LogTypeConsume).Order("id").Find(&logs).Error)
	require.Len(t, logs, 4)
	assert.Equal(t, 12, logs[0].PromptTokens)
	assert.Equal(t, 3, logs[0].CompletionTokens)
	assert.Equal(t, 15, logs[0].Quota)
	for _, log := range logs[1:3] {
		assert.Equal(t, 20, log.PromptTokens)
		assert.Equal(t, 5, log.CompletionTokens)
		assert.Equal(t, 25, log.Quota)
	}
	assert.Zero(t, logs[3].PromptTokens)
	assert.Zero(t, logs[3].CompletionTokens)
	assert.Zero(t, logs[3].Quota)
	assert.Contains(t, logs[3].Content, "上游实际用量为零")
	assert.NotContains(t, logs[3].Content, "上游没有返回计费信息")
	// 删除/禁用后关联保留，重新登录不能自动生成替代凭据。
	require.NoError(t, db.Model(internal).Update("status", common.TokenStatusDisabled).Error)
	invoke("reply", sessions[0], input, 403, nil)
	require.NoError(t, internal.Delete())
	_, err = model.GetOrCreateBelloBuddyRelayToken(users[0].Id)
	require.ErrorIs(t, err, model.ErrTokenInvalid)
	var identities int64
	require.NoError(t, db.Model(&model.BelloBuddyRelayIdentity{}).Where("user_id = ?", users[0].Id).Count(&identities).Error)
	assert.EqualValues(t, 1, identities)
}

// 接入前的 Token 表结构用于验证新增管理标识不改变已有凭据与账本。
type belloBuddyTokenBeforeBridge struct {
	Id                 int            `json:"id"`
	UserId             int            `json:"user_id" gorm:"index"`
	Key                string         `json:"key" gorm:"type:varchar(128);uniqueIndex"`
	Status             int            `json:"status" gorm:"default:1"`
	Name               string         `json:"name" gorm:"index" `
	CreatedTime        int64          `json:"created_time" gorm:"bigint"`
	AccessedTime       int64          `json:"accessed_time" gorm:"bigint"`
	ExpiredTime        int64          `json:"expired_time" gorm:"bigint;default:-1"` // -1 means never expired
	RemainQuota        int            `json:"remain_quota" gorm:"default:0"`
	UnlimitedQuota     bool           `json:"unlimited_quota"`
	ModelLimitsEnabled bool           `json:"model_limits_enabled"`
	ModelLimits        string         `json:"model_limits" gorm:"type:text"`
	AllowIps           *string        `json:"allow_ips" gorm:"default:''"`
	UsedQuota          int            `json:"used_quota" gorm:"default:0"` // used quota
	Group              string         `json:"group" gorm:"default:''"`
	CrossGroupRetry    bool           `json:"cross_group_retry"` // 跨分组重试，仅auto分组有效
	AutoGroups         string         `json:"-" gorm:"type:text"`
	DeletedAt          gorm.DeletedAt `gorm:"index"`
}

func (belloBuddyTokenBeforeBridge) TableName() string { return "tokens" }

// 签发一个拥有真实服务器认证标签但已过期的夹具，验证到期检查没有被跳过。
func expireBelloBuddyReference(t *testing.T, reference string) string {
	t.Helper()
	key := sha256.Sum256([]byte("dpin-bellobuddy-conversation-v1\x00" + common.SessionSecret))
	block, err := aes.NewCipher(key[:])
	require.NoError(t, err)
	aead, err := cipher.NewGCM(block)
	require.NoError(t, err)
	sealed, err := base64.RawURLEncoding.DecodeString(reference)
	require.NoError(t, err)
	nonce := sealed[:aead.NonceSize()]
	body, err := aead.Open(nil, nonce, sealed[aead.NonceSize():], []byte("bellobuddy-maxkb-v1"))
	require.NoError(t, err)
	var fields map[string]common.RawMessage
	require.NoError(t, common.Unmarshal(body, &fields))
	fields["expires_at"], err = common.Marshal(common.GetTimestamp() - 1)
	require.NoError(t, err)
	body, err = common.Marshal(fields)
	require.NoError(t, err)
	newNonce := make([]byte, aead.NonceSize())
	_, err = rand.Read(newNonce)
	require.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString(aead.Seal(newNonce, newNonce, body, []byte("bellobuddy-maxkb-v1")))
}
