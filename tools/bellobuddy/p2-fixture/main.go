// P1/P2/P3 隔离联调入口：复用真实迁移、认证控制器和路由，不签发测试登录会话。
// 仅允许临时状态目录及名称明确的本阶段测试库；不配置真实 AI 渠道或招聘数据。
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/router"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	mysqldriver "github.com/go-sql-driver/mysql"
)

func main() {
	phase := flag.String("phase", "p2", "隔离阶段：p2 或 p3")
	stateDir := flag.String("state-dir", "", "本阶段专用临时目录，权限 0700")
	dsn := flag.String("dsn", "local", "仅接受回环地址的 bellobuddy_阶段_startup_ 测试库")
	startupOnly := flag.Bool("check-startup", false, "完整迁移三次、验证数据后退出")
	port := flag.Int("listen-port", 34443, "回环 HTTPS 端口")
	flag.Parse()
	if err := run(*stateDir, *dsn, *startupOnly, *port, *phase); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(stateDir, dsn string, startupOnly bool, port int, phase string) error {
	stateDir, err := filepath.Abs(stateDir)
	if err != nil || (phase != "p2" && phase != "p3") || !strings.HasPrefix(stateDir, "/private/tmp/bellobuddy-"+phase+"-") || port < 1024 || port > 65535 {
		return fmt.Errorf("仅允许 /private/tmp/bellobuddy-阶段- 前缀状态目录及非特权端口")
	}
	if dsn != "local" {
		var host, database, driver, tableCountQuery string
		if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
			parsed, err := url.Parse(dsn)
			if err != nil {
				return err
			}
			host, database = parsed.Hostname(), strings.TrimPrefix(parsed.Path, "/")
			driver, tableCountQuery = "pgx", "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public'"
		} else {
			parsed, err := mysqldriver.ParseDSN(dsn)
			if err != nil || parsed.Net != "tcp" {
				return fmt.Errorf("MySQL 必须使用回环 TCP 测试库")
			}
			host, _, err = net.SplitHostPort(parsed.Addr)
			if err != nil {
				return err
			}
			database = parsed.DBName
			driver, tableCountQuery = "mysql", "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE()"
		}
		if host != "127.0.0.1" || !strings.HasPrefix(database, "bellobuddy_"+phase+"_startup_") {
			return fmt.Errorf("拒绝访问非本阶段回环测试库")
		}
		connection, err := sql.Open(driver, dsn)
		if err != nil {
			return err
		}
		var tableCount int
		err = connection.QueryRow(tableCountQuery).Scan(&tableCount)
		_ = connection.Close()
		if err != nil || tableCount != 0 {
			return fmt.Errorf("夹具仅允许全新空测试库")
		}
	}
	if info, err := os.Lstat(stateDir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("状态目录不能是文件或符号链接")
		}
		entries, err := os.ReadDir(stateDir)
		if err != nil || len(entries) != 0 {
			return fmt.Errorf("夹具仅允许全新空状态目录")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(stateDir, 0700); err != nil {
		return err
	}
	_ = os.Setenv("SQL_DSN", dsn)
	_ = os.Unsetenv("LOG_SQL_DSN")
	common.SQLitePath = filepath.Join(stateDir, "fixture.db")
	common.RedisEnabled, common.IsMasterNode = false, true
	common.SessionSecret = randomSecret()
	common.SessionCookieSecure = true
	common.SessionCookieTrustedURLs = []string{fmt.Sprintf("https://127.0.0.1:%d", port)}
	common.PasswordLoginEnabled = true
	common.CriticalRateLimitEnable = true
	if err := model.InitDB(); err != nil {
		return err
	}
	if err := model.InitLogDB(); err != nil {
		return err
	}
	if err := authz.Init(model.DB); err != nil {
		return err
	}
	model.InitOptionMap()
	if err := i18n.Init(); err != nil {
		return err
	}
	password := randomSecret()
	totpSecret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(randomSecret()))
	hash, err := common.Password2Hash(password)
	if err != nil {
		return err
	}
	users := []model.User{
		{Username: "p2-fixture-admin", Password: hash, Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "p2fixtureadmin", AuthVersion: 1},
		{Username: "p2-fixture-totp", Password: hash, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "p2fixturetotp", AuthVersion: 1},
	}
	if phase == "p3" {
		users = append(users, model.User{Username: "p3-fixture-user", Password: hash, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "p3fixtureuser", AuthVersion: 1}, model.User{Username: "p3-fixture-other", Password: hash, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "p3fixtureother", AuthVersion: 1})
	}
	for i := range users {
		if err := model.DB.Create(&users[i]).Error; err != nil {
			return fmt.Errorf("夹具要求全新测试库：%w", err)
		}
	}
	if err := model.DB.Create(&model.TwoFA{UserId: users[1].Id, Secret: totpSecret, IsEnabled: true}).Error; err != nil {
		return err
	}
	// 与真正启动相同的整库迁移入口；再次启动必须保留用户及认证因素。
	for range 2 {
		previousDB, err := model.DB.DB()
		if err != nil {
			return err
		}
		if err := model.InitDB(); err != nil {
			return err
		}
		_ = previousDB.Close()
		if err := model.InitLogDB(); err != nil {
			return err
		}
		var preserved model.User
		if err := model.DB.First(&preserved, users[0].Id).Error; err != nil || preserved.Username != users[0].Username || preserved.Password != hash || preserved.AuthVersion != 1 {
			return fmt.Errorf("重复整库启动未保留用户")
		}
		factor, err := model.GetTwoFAByUserId(users[1].Id)
		if err != nil || factor == nil || !factor.IsEnabled || factor.Secret != totpSecret {
			return fmt.Errorf("重复整库启动未保留 TOTP")
		}
	}
	versionSQL := "SELECT sqlite_version()"
	if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
		versionSQL = "SELECT VERSION()"
	} else if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		versionSQL = "SHOW server_version"
	}
	var version string
	if err := model.DB.Raw(versionSQL).Scan(&version).Error; err != nil {
		return err
	}
	tables, err := model.DB.Migrator().GetTables()
	if err != nil {
		return err
	}
	fmt.Printf("完整整库启动三次通过：%s %s，表数量=%d，用户及 TOTP 保留\n", common.MainDatabaseType(), version, len(tables))
	if startupOnly {
		return nil
	}
	certificate, key, fingerprint, err := fixtureCertificate()
	if err != nil {
		return err
	}
	certPath, keyPath := filepath.Join(stateDir, "certificate.pem"), filepath.Join(stateDir, "private-key.pem")
	if err := os.WriteFile(certPath, certificate, 0600); err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, key, 0600); err != nil {
		return err
	}
	credentials, err := common.Marshal(map[string]any{"origin": common.SessionCookieTrustedURLs[0], "admin_username": users[0].Username, "totp_username": users[1].Username, "password": password, "totp_secret": totpSecret, "certificate_sha256": fingerprint, "phase": phase, "user_username": "p3-fixture-user", "other_username": "p3-fixture-other"})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stateDir, "credentials.json"), credentials, 0600); err != nil {
		return err
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.DisableCache())
	if phase == "p3" {
		// 使用生产路由及其原生认证、权限、额度、限流；空测试库不配置真实AI渠道。
		router.SetApiRouter(engine)
	} else {
		api := engine.Group("/api")
		api.POST("/user/login", middleware.CriticalRateLimit(), middleware.TurnstileCheck(), controller.Login)
		api.POST("/user/login/verify", middleware.CriticalRateLimit(), controller.VerifyLogin)
		api.POST("/user/login/2fa", middleware.CriticalRateLimit(), controller.Verify2FALogin)
		api.GET("/user/login/encryption-key", controller.GetPasswordEncryptionKey)
		api.POST("/user/auth/refresh", middleware.SessionCookieOriginGuard(), middleware.CriticalRateLimit(), controller.RefreshAuth)
		api.POST("/user/auth/logout", middleware.SessionCookieOriginGuard(), controller.AuthLogout)
		api.GET("/user/self", middleware.UserAuth(), controller.GetSelf)
		api.GET("/bellobuddy/capabilities", middleware.UserAuth(), middleware.BelloBuddySessionRequired(), controller.GetBelloBuddyCapabilities)
	}
	engine.GET("/sign-in", func(c *gin.Context) { c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(loginPage)) })
	engine.GET("/login", func(c *gin.Context) { c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(loginPage)) })
	fmt.Printf("隔离认证服务：%s/login；凭据仅见私密状态文件\n", common.SessionCookieTrustedURLs[0])
	server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", port), Handler: engine, ReadHeaderTimeout: 10 * time.Second}
	return server.ListenAndServeTLS(certPath, keyPath)
}

func randomSecret() string {
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value)
}

func fixtureCertificate() ([]byte, []byte, string, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, "", err
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "BelloBuddy P2 隔离测试"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(24 * time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, "", err
	}
	digest := sha256.Sum256(der)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), hex.EncodeToString(digest[:]), nil
}

const loginPage = `<!doctype html><meta charset="utf-8"><title>DPin 隔离登录验收</title>
<h1>DPin 隔离登录验收</h1><p>本页使用原生登录和附加验证接口，验证结束后关闭窗口。</p>
<form id="login"><label>用户名 <input id="username" autocomplete="username"></label><label>密码 <input id="password" type="password" autocomplete="current-password"></label><button>登录</button></form>
<form id="verify" hidden><label>动态验证码 <input id="code" autocomplete="one-time-code"></label><button>完成附加验证</button></form><p id="result"></p>
<script>
let flow;
async function submit(path,body){const response=await fetch(path,{method:'POST',credentials:'include',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});const result=await response.json();if(!result.success){document.getElementById('result').textContent=result.message||result.code||'登录失败';return;}if(result.data&&result.data.require_verification){flow=result.data.flow_token;document.getElementById('verify').hidden=false;document.getElementById('result').textContent='请完成附加验证';return;}document.getElementById('password').value='';document.getElementById('code').value='';document.getElementById('result').textContent='已完成登录，请关闭此窗口返回客户端';}
document.getElementById('login').onsubmit=e=>{e.preventDefault();submit('/api/user/login',{username:document.getElementById('username').value,password:document.getElementById('password').value});};
document.getElementById('verify').onsubmit=e=>{e.preventDefault();submit('/api/user/login/verify',{flow_token:flow,method:'2fa',code:document.getElementById('code').value});};
</script>`
