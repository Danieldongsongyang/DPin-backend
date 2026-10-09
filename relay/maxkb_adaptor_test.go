package relay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	filterdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/maxkb"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaxKBApplicationRelay(t *testing.T) {
	const appID = "01a0dbc2-4722-7681-9fbc-4ddf277cbbd6"
	const chatID = "11a0dbc2-4722-7681-9fbc-4ddf277cbbd6"
	const responsePrefix = `{"id":"record-id","choices":[{"index":0,"chat_id":"` + chatID + `","finish_reason":"stop","message":{"role":"assistant","content":"测试回复"}}],"usage":`
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "/chat/api/"+appID+"/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer application-secret", r.Header.Get("Authorization"))
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.JSONEq(t, `{"model":"`+appID+`","messages":[{"role":"user","content":" 测试输入 "}],"stream":false,"chat_id":"`+chatID+`"}`, string(body))
		_, _ = io.WriteString(w, responsePrefix+`{"prompt_tokens":809,"completion_tokens":22,"total_tokens":831,"usage_source":"upstream","cache_usage_source":"upstream","prompt_tokens_details":{"cached_tokens":100}}}`)
	}))
	defer upstream.Close()
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, OriginModelName: "support-reply", ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeMaxKB, ChannelBaseUrl: upstream.URL, UpstreamModelName: appID, ApiKey: "application-secret"}}
	adaptor := GetAdaptor(constant.APITypeMaxKB)
	require.NotNil(t, adaptor)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(maxkb.ContextChatID, chatID)
	request := &dto.GeneralOpenAIRequest{Model: appID, Messages: []dto.Message{{Role: "user", Content: " 测试输入 "}}}
	converted, err := adaptor.ConvertOpenAIRequest(c, info, request)
	require.NoError(t, err)
	body, err := common.Marshal(converted)
	require.NoError(t, err)
	response, err := adaptor.DoRequest(c, info, strings.NewReader(string(body)))
	require.NoError(t, err)
	usageValue, apiErr := adaptor.DoResponse(c, response.(*http.Response), info)
	require.Nil(t, apiErr)
	usage, ok := usageValue.(*dto.Usage)
	require.True(t, ok)
	assert.Equal(t, 809, usage.PromptTokens)
	assert.Equal(t, 22, usage.CompletionTokens)
	assert.Equal(t, 831, usage.TotalTokens)
	assert.Equal(t, "upstream", usage.UsageSource)
	assert.Equal(t, 100, usage.PromptTokensDetails.CachedTokens)
	assert.Equal(t, chatID, c.GetString(maxkb.ContextReturnedChatID))
	assert.Contains(t, recorder.Body.String(), `"model":"support-reply"`)
	assert.Contains(t, recorder.Body.String(), "测试回复")
	assert.NotContains(t, recorder.Body.String(), chatID)
	assert.NotContains(t, recorder.Body.String(), "application-secret")

	t.Run("passthrough cannot change conversation or generation parameters", func(t *testing.T) {
		for _, payload := range []string{
			`{"model":"` + appID + `","messages":[{"role":"user","content":"x"}],"stream":true}`,
			`{"model":"` + appID + `","messages":[{"role":"user","content":"x"}],"n":2147483647}`,
			`{"model":"` + appID + `","messages":[{"role":"user","content":"x"}],"max_tokens":18446744073686646784}`,
			`{"model":"` + appID + `","messages":[{"role":"user","content":"x","tool_calls":[]}],"chat_id":"` + chatID + `"}`,
			`{"model":"` + appID + `","messages":[{"role":"user","content":"x"}],"chat_id":"21a0dbc2-4722-7681-9fbc-4ddf277cbbd6"}`,
			`{"model":"` + appID + `","messages":[{"role":"system","content":"history"},{"role":"user","content":"x"}],"chat_id":"` + chatID + `"}`,
		} {
			_, err := adaptor.DoRequest(c, info, strings.NewReader(payload))
			require.Error(t, err)
		}
		assert.Equal(t, int32(1), calls.Load())
	})

	t.Run("conversion rejects ignored controls and multi-message context", func(t *testing.T) {
		zero := uint(0)
		for _, request := range []*dto.GeneralOpenAIRequest{
			nil,
			{Model: appID, Messages: []dto.Message{{Role: "user", Content: "x"}}, MaxTokens: &zero},
			{Model: appID, Messages: []dto.Message{{Role: "assistant", Content: "x"}, {Role: "user", Content: "y"}}},
			{Model: appID, Messages: []dto.Message{{Role: "user", Content: []any{map[string]any{"type": "image_url"}}}}},
		} {
			_, err := adaptor.ConvertOpenAIRequest(c, info, request)
			require.Error(t, err)
		}
	})

	t.Run("usage provenance and arithmetic are required before publishing a reply", func(t *testing.T) {
		for _, tc := range []struct {
			name, usage string
			valid       bool
		}{
			{"actual zero", `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0,"usage_source":"upstream","cache_usage_source":"upstream","prompt_tokens_details":{"cached_tokens":0}}`, true},
			{"missing cache fact", `{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2,"usage_source":"upstream"}`, false},
			{"cache exceeds input", `{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2,"usage_source":"upstream","cache_usage_source":"upstream","prompt_tokens_details":{"cached_tokens":2}}`, false},
			{"cache absent provenance", `{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2,"usage_source":"upstream","prompt_tokens_details":{"cached_tokens":0}}`, false},
			{"cache negative", `{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2,"usage_source":"upstream","cache_usage_source":"upstream","prompt_tokens_details":{"cached_tokens":-1}}`, false},
			{"cache fractional", `{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2,"usage_source":"upstream","cache_usage_source":"upstream","prompt_tokens_details":{"cached_tokens":0.5}}`, false},
			{"no provenance", `{"prompt_tokens":809,"completion_tokens":22,"total_tokens":831}`, false},
			{"estimated", `{"prompt_tokens":809,"completion_tokens":22,"total_tokens":831,"usage_source":"estimated"}`, false},
			{"missing", `null`, false},
			{"missing completion", `{"prompt_tokens":1,"total_tokens":1,"usage_source":"upstream"}`, false},
			{"negative", `{"prompt_tokens":-1,"completion_tokens":1,"total_tokens":0,"usage_source":"upstream"}`, false},
			{"fractional", `{"prompt_tokens":1.5,"completion_tokens":1,"total_tokens":2.5,"usage_source":"upstream"}`, false},
			{"sum mismatch", `{"prompt_tokens":1,"completion_tokens":1,"total_tokens":3,"usage_source":"upstream"}`, false},
			{"sum overflow", `{"prompt_tokens":2147483647,"completion_tokens":1,"total_tokens":2147483648,"usage_source":"upstream"}`, false},
			{"integer overflow", `{"prompt_tokens":18446744073709551615,"completion_tokens":0,"total_tokens":18446744073709551615,"usage_source":"upstream"}`, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				context, _ := gin.CreateTestContext(recorder)
				response := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(responsePrefix + tc.usage + "}"))}
				usage, apiErr := adaptor.DoResponse(context, response, info)
				if tc.valid {
					require.Nil(t, apiErr)
					assert.NotNil(t, usage)
					return
				}
				require.NotNil(t, apiErr)
				assert.Nil(t, usage)
				assert.Empty(t, recorder.Body.String())
				assert.Empty(t, context.GetString(maxkb.ContextReturnedChatID))
			})
		}
	})

	t.Run("invalid conversation response does not become a new session", func(t *testing.T) {
		for _, replacement := range []string{"", "not-a-uuid", "21a0dbc2-4722-7681-9fbc-4ddf277cbbd6"} {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Set(maxkb.ContextChatID, chatID)
			body := strings.ReplaceAll(responsePrefix+`{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2,"usage_source":"upstream"}}`, chatID, replacement)
			_, apiErr := adaptor.DoResponse(context, &http.Response{Body: io.NopCloser(strings.NewReader(body))}, info)
			require.NotNil(t, apiErr)
			assert.Empty(t, context.GetString(maxkb.ContextReturnedChatID))
		}
	})

	t.Run("application mapping and protocol routing", func(t *testing.T) {
		for _, path := range []string{"/v1/embeddings", "/v1/responses", "/v1/images/generations", "/v1/systemone"} {
			allowed, _ := model.ChannelSatisfiesFilters(&model.Channel{Type: constant.ChannelTypeMaxKB}, "support-reply", []filterdto.ChannelFilter{{Kind: filterdto.FilterRequestPath, RequestPath: path}})
			assert.False(t, allowed, path)
		}
		allowed, _ := model.ChannelSatisfiesFilters(&model.Channel{Type: constant.ChannelTypeMaxKB}, "support-reply", []filterdto.ChannelFilter{{Kind: filterdto.FilterRequestPath, RequestPath: "/v1/chat/completions"}})
		assert.True(t, allowed)
		assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, common.GetEndpointTypesByChannelType(constant.ChannelTypeMaxKB, "gpt-image-1"))
		apiType, supported := common.ChannelType2APIType(constant.ChannelTypeMaxKB)
		assert.True(t, supported)
		assert.Equal(t, constant.APITypeMaxKB, apiType)
		for _, app := range []string{"support-reply", "00000000-0000-0000-0000-000000000000", "../../admin"} {
			copy := *info
			channel := *info.ChannelMeta
			copy.ChannelMeta = &channel
			copy.UpstreamModelName = app
			_, err := adaptor.GetRequestURL(&copy)
			require.Error(t, err, fmt.Sprintf("application %q", app))
		}
	})
}

func TestMaxKBTrustedScope(t *testing.T) {
	const secret = "p7b-test-shared-identity-secret-0123456789"
	t.Setenv("DPIN_MAXKB_IDENTITY_SECRET", secret)
	scope := service.BelloBuddyConversationScope{UserID: 1, AccountID: 2, JobID: 3, CandidateHash: "candidate-a", BindingID: 4, ChannelID: 5, ChannelSignature: "original-app"}
	identity, err := service.BelloBuddyMaxKBScope(scope)
	require.NoError(t, err)
	rotated := scope
	rotated.AuthVersion, rotated.BindingVersion = 9, 10
	same, err := service.BelloBuddyMaxKBScope(rotated)
	require.NoError(t, err)
	assert.Equal(t, identity, same, "登录轮换与绑定显示配置版本不改变记忆身份")
	for _, dimension := range []string{"user", "account", "job", "candidate", "binding", "target"} {
		other := scope
		switch dimension {
		case "user":
			other.UserID++
		case "account":
			other.AccountID++
		case "job":
			other.JobID++
		case "candidate":
			other.CandidateHash = "candidate-b"
		case "binding":
			other.BindingID++
		case "target":
			other.ChannelSignature = "changed-app"
		}
		next, err := service.BelloBuddyMaxKBScope(other)
		require.NoError(t, err)
		assert.NotEqual(t, identity, next, dimension)
	}
	const appID = "01a0dbc2-4722-7681-9fbc-4ddf277cbbd6"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var data map[string]any
		require.NoError(t, common.Unmarshal(body, &data))
		assert.Equal(t, identity, data["dpin_scope"])
		assert.Equal(t, identity, r.Header.Get("X-DPIN-Scope"))
		stamp := r.Header.Get("X-DPIN-Timestamp")
		timestamp, err := strconv.ParseInt(stamp, 10, 64)
		require.NoError(t, err)
		assert.InDelta(t, time.Now().Unix(), timestamp, 60)
		digest := sha256.Sum256(body)
		signed := strings.Join([]string{"dpin-maxkb-scope-v1", appID, identity, stamp, r.Header.Get("X-DPIN-Nonce"), hex.EncodeToString(digest[:])}, "\n")
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(signed))
		assert.Equal(t, hex.EncodeToString(mac.Sum(nil)), r.Header.Get("X-DPIN-Signature"))
		_, _ = io.WriteString(w, `{"dpin_scope":"`+identity+`","id":"signed-record","choices":[{"index":0,"chat_id":"11a0dbc2-4722-7681-9fbc-4ddf277cbbd6","finish_reason":"stop","message":{"role":"assistant","content":"合成回复"}}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5,"usage_source":"upstream","cache_usage_source":"upstream","prompt_tokens_details":{"cached_tokens":0}}}`)
	}))
	defer upstream.Close()
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, OriginModelName: "test-model", ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeMaxKB, ChannelBaseUrl: upstream.URL, UpstreamModelName: appID, ApiKey: "application-test"}}
	a := GetAdaptor(constant.APITypeMaxKB)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	c.Set(maxkb.ContextScopeID, identity)
	converted, err := a.ConvertOpenAIRequest(c, info, &dto.GeneralOpenAIRequest{Model: appID, Messages: []dto.Message{{Role: "user", Content: "合成问题"}}})
	require.NoError(t, err)
	body, err := common.Marshal(converted)
	require.NoError(t, err)
	result, err := a.DoRequest(c, info, strings.NewReader(string(body)))
	require.NoError(t, err)
	_, apiErr := a.DoResponse(c, result.(*http.Response), info)
	require.Nil(t, apiErr)
	for _, returnedScope := range []string{"", "wrong-scope"} {
		payload := `{"dpin_scope":"` + returnedScope + `","id":"record","choices":[{"index":0,"chat_id":"11a0dbc2-4722-7681-9fbc-4ddf277cbbd6","finish_reason":"stop","message":{"role":"assistant","content":"不能接受错误作用域"}}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5,"usage_source":"upstream","cache_usage_source":"upstream","prompt_tokens_details":{"cached_tokens":0}}}`
		_, ackError := a.DoResponse(c, &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(payload))}, info)
		require.NotNil(t, ackError, "旧服务忽略作用域或错误回包都不能返回可发送建议")
	}
	c.Set(maxkb.ContextScopeID, "another-scope")
	_, err = a.DoRequest(c, info, strings.NewReader(string(body)))
	require.Error(t, err)
}
