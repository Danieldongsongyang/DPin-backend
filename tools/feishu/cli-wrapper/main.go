// 独立可执行文件：固定官方命令实现，仅增加非交互应用身份凭据提供器。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/larksuite/cli/cmd"
	"github.com/larksuite/cli/extension/credential"
)

type dpinProvider struct {
	once     sync.Once
	token    *credential.Token
	err      error
	endpoint string // 仅测试设置；生产固定飞书官方地址。
	client   *http.Client
}

func (p *dpinProvider) Name() string  { return "dpin-application" }
func (p *dpinProvider) Priority() int { return -100 }
func (p *dpinProvider) ResolveAccount(context.Context) (*credential.Account, error) {
	id, secret := os.Getenv("LARKSUITE_CLI_APP_ID"), os.Getenv("LARKSUITE_CLI_APP_SECRET")
	if id == "" || secret == "" {
		return nil, &credential.BlockError{Provider: p.Name(), Reason: "application credentials are required"}
	}
	return &credential.Account{AppID: id, AppSecret: secret, Brand: credential.BrandFeishu, DefaultAs: credential.IdentityBot, SupportedIdentities: credential.SupportsBot}, nil
}
func (p *dpinProvider) ResolveToken(ctx context.Context, spec credential.TokenSpec) (*credential.Token, error) {
	if spec.Type != credential.TokenTypeTAT || spec.AppID != os.Getenv("LARKSUITE_CLI_APP_ID") {
		return nil, &credential.BlockError{Provider: p.Name(), Reason: "only configured bot identity is supported"}
	}
	p.once.Do(func() { p.token, p.err = p.fetch(ctx) })
	return p.token, p.err
}
func (p *dpinProvider) fetch(ctx context.Context) (*credential.Token, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	account, err := p.ResolveAccount(ctx)
	if err != nil {
		return nil, err
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {account.AppID}, "client_secret": {account.AppSecret}}
	endpoint := p.endpoint
	if endpoint == "" {
		endpoint = "https://accounts.feishu.cn/oauth/v3/token"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("TAT_REQUEST_INVALID")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := p.client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("TAT_NETWORK_ERROR")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("TAT_RESPONSE_INVALID")
	}
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("TAT_HTTP_%d", response.StatusCode)
	}
	var result struct {
		Code   int    `json:"code"`
		Token  string `json:"access_token"`
		Expire int    `json:"expires_in"`
	}
	if json.Unmarshal(data, &result) != nil {
		return nil, fmt.Errorf("TAT_RESPONSE_INVALID")
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("TAT_CREDENTIAL_REJECTED_%d", result.Code)
	}
	if result.Token == "" || (result.Expire > 0 && result.Expire <= 60) {
		return nil, fmt.Errorf("TAT_EXPIRY_INVALID")
	}
	// 单个调用进程最多45秒；下次调用／服务重启必重新取得令牌，不持久化个人登录态。
	return &credential.Token{Value: result.Token, Source: p.Name()}, nil
}

func main() {
	credential.Register(&dpinProvider{})
	os.Exit(cmd.Execute())
}
