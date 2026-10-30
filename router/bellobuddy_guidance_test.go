package router

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func runBelloBuddyGuidanceOptimization(t *testing.T, db *gorm.DB, engine *gin.Engine, session string) {
	t.Helper()
	require.NoError(t, i18n.Init())
	t.Setenv("BELLOBUDDY_GUIDANCE_MODEL", "guidance-test-model")
	oldRatios := ratio_setting.ModelRatio2JSONString()
	var ratios map[string]float64
	require.NoError(t, common.UnmarshalJsonStr(oldRatios, &ratios))
	ratios["guidance-test-model"] = 1
	encoded, err := common.Marshal(ratios)
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(encoded)))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldRatios)) })
	var calls atomic.Int32
	var malformed atomic.Bool
	const original = "有京东运营经验，推广经验优先"
	const optimized = "必须条件：有京东运营经验。优先条件：有推广经验。"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer guidance-upstream-secret", r.Header.Get("Authorization"))
		body, readErr := io.ReadAll(r.Body)
		require.NoError(t, readErr)
		assert.Contains(t, string(body), "不得将优先条件升级为必须条件")
		assert.Contains(t, string(body), "不重复这些通用规则、等级定义")
		assert.Equal(t, 1, strings.Count(string(body), original))
		content, marshalErr := common.Marshal(service.BelloBuddyGuidanceResult{Guidance: optimized, Changes: []string{"区分必须与优先"}, Questions: []string{"半年经验是总经验还是客服经验？"}})
		require.NoError(t, marshalErr)
		if malformed.Load() {
			content = []byte(`{"guidance":"","changes":[],"questions":[]}`)
		}
		response, marshalErr := common.Marshal(map[string]any{"id": "guidance-response", "model": "guidance-test-model", "choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": string(content)}}}, "usage": map[string]int{"prompt_tokens": 20, "completion_tokens": 10, "total_tokens": 30}})
		require.NoError(t, marshalErr)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(response)
	}))
	t.Cleanup(upstream.Close)
	base := upstream.URL
	channel := model.Channel{Type: constant.ChannelTypeDeepSeek, Key: "guidance-upstream-secret", Status: common.ChannelStatusEnabled, Name: "Guidance test", Models: "guidance-test-model", Group: "default", BaseURL: &base}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: channel.Models, ChannelId: channel.Id, Enabled: true}).Error)
	var before []model.BelloBuddyPlan
	require.NoError(t, db.Order("id").Find(&before).Error)
	invoke := func(token string, payload any, status int) string {
		t.Helper()
		body, marshalErr := common.Marshal(payload)
		require.NoError(t, marshalErr)
		req := httptest.NewRequest(http.MethodPost, "/api/bellobuddy/ai/guidance/optimize", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, req)
		require.Equal(t, status, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), channel.Key)
		return response.Body.String()
	}
	input := service.BelloBuddyGuidanceInput{SchemaVersion: 1, Guidance: original, Mode: "level"}
	invoke("", input, 401)
	invoke("nap_bellobuddy-personal-token-test", input, 403)
	invoke(session, map[string]any{"schema_version": 1, "guidance": original, "mode": "level", "model": "attacker"}, 400)
	invoke(session, service.BelloBuddyGuidanceInput{SchemaVersion: 1, Guidance: strings.Repeat("x", service.BelloBuddyGuidanceMaxBytes+1), Mode: "level"}, 400)
	assert.Zero(t, calls.Load())
	result := invoke(session, input, 200)
	assert.Contains(t, result, "推广经验")
	assert.Contains(t, result, "半年经验")
	assert.EqualValues(t, 1, calls.Load())
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", channel.Id).Update("enabled", false).Error)
	invoke(session, input, 503)
	assert.EqualValues(t, 1, calls.Load())
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", channel.Id).Update("enabled", true).Error)
	malformed.Store(true)
	result = invoke(session, input, 502)
	assert.Contains(t, result, "AI_GUIDANCE_RESPONSE_INVALID")
	assert.NotContains(t, result, optimized)
	assert.EqualValues(t, 2, calls.Load())
	var after []model.BelloBuddyPlan
	require.NoError(t, db.Order("id").Find(&after).Error)
	assert.Equal(t, before, after, "生成建议不会保存或改写任何方案")
	for _, invalid := range []string{`null`, `{"guidance":"x","changes":[],"questions":null}`, `{"guidance":"x","changes":[1],"questions":[]}`} {
		_, parseErr := service.ParseBelloBuddyGuidanceResult(invalid)
		assert.Error(t, parseErr)
	}
	// A configured channel without any model price must report the real cause
	// before dispatching upstream; the editor must not receive a generic error.
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldRatios))
	result = invoke(session, input, 400)
	assert.Contains(t, result, "AI_PRICE_NOT_CONFIGURED")
	assert.Contains(t, result, "尚未配置计费价格")
	assert.EqualValues(t, 2, calls.Load())
	// Exercise the real default optimization model with no test-injected price.
	t.Setenv("BELLOBUDDY_GUIDANCE_MODEL", "deepseek-v4-flash")
	require.NoError(t, db.Model(&channel).Update("models", channel.Models+",deepseek-v4-flash").Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "deepseek-v4-flash", ChannelId: channel.Id, Enabled: true}).Error)
	malformed.Store(false)
	result = invoke(session, input, 200)
	assert.Contains(t, result, "推广经验")
	assert.EqualValues(t, 3, calls.Load())
}
