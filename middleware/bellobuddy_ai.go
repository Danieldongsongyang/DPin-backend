package middleware

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/maxkb"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func abortBelloBuddyAI(c *gin.Context, err error) {
	failure := service.BelloBuddyErrorResponse(err)
	c.AbortWithStatusJSON(failure.Status, gin.H{"success": false, "code": failure.Code, "message": failure.Message, "source": "dpin"})
}

func PrepareBelloBuddyAI(reply bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := GetSessionAuthIdentity(c)
		header := strings.Fields(c.GetHeader("Authorization"))
		if !ok || len(header) != 2 || !strings.EqualFold(header[0], "Bearer") {
			abortBelloBuddyAI(c, &service.BelloBuddyError{Status: http.StatusUnauthorized, Code: "AUTH_SESSION_REQUIRED", Message: "AI 调用需要显式的产品会话凭据"})
			return
		}
		if c.ContentType() != "application/json" {
			abortBelloBuddyAI(c, &service.BelloBuddyError{Status: http.StatusBadRequest, Code: "INVALID_REQUEST", Message: "AI 请求必须使用 JSON"})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.BelloBuddyMaxBodyBytes)
		storage, err := common.GetBodyStorage(c)
		if err != nil || storage.Size() > service.BelloBuddyMaxBodyBytes {
			abortBelloBuddyAI(c, &service.BelloBuddyError{Status: http.StatusRequestEntityTooLarge, Code: "INVALID_REQUEST", Message: "AI 请求超过大小限制"})
			return
		}
		var fields map[string]common.RawMessage
		if common.UnmarshalBodyReusable(c, &fields) != nil || fields == nil {
			abortBelloBuddyAI(c, &service.BelloBuddyError{Status: http.StatusBadRequest, Code: "INVALID_REQUEST", Message: "AI 请求必须是 JSON 对象"})
			return
		}
		for field := range fields {
			switch field {
			case "account_key", "job_id", "job_version", "schema_version", "candidate_id", "filter_plan_id", "filter_version", "binding_version", "snapshot_ref", "communication_plan_id", "communication_version", "request", "input", "conversation_ref":
			default:
				abortBelloBuddyAI(c, &service.BelloBuddyError{Status: http.StatusBadRequest, Code: "INVALID_REQUEST", Message: "AI 请求包含不支持的字段"})
				return
			}
		}
		var input service.BelloBuddyAIInput
		if err := common.UnmarshalBodyReusable(c, &input); err != nil {
			abortBelloBuddyAI(c, &service.BelloBuddyError{Status: http.StatusBadRequest, Code: "INVALID_REQUEST", Message: "AI 请求 JSON 无效"})
			return
		}
		route, err := service.ResolveBelloBuddyAIRoute(identity, input, reply)
		if err != nil {
			abortBelloBuddyAI(c, err)
			return
		}
		var payload []byte
		path, channelType := "/v1/systemone", constant.ChannelTypeTypeSafe
		if reply {
			if strings.TrimSpace(input.Input) == "" || len(input.Input) > 512<<10 || len(input.Request) != 0 {
				abortBelloBuddyAI(c, &service.BelloBuddyError{Status: http.StatusBadRequest, Code: "INVALID_REQUEST", Message: "回复请求必须包含一轮文本输入"})
				return
			}
			if input.ConversationRef != "" {
				chatID, err := service.OpenBelloBuddyConversation(input.ConversationRef, route.Scope)
				if err != nil {
					abortBelloBuddyAI(c, err)
					return
				}
				c.Set(maxkb.ContextChatID, chatID)
			}
			payload, err = common.Marshal(gin.H{"model": route.Model, "messages": []gin.H{{"role": "user", "content": input.Input}}, "stream": false})
			path, channelType = "/v1/chat/completions", constant.ChannelTypeMaxKB
		} else {
			if input.Input != "" || input.ConversationRef != "" {
				abortBelloBuddyAI(c, &service.BelloBuddyError{Status: http.StatusBadRequest, Code: "INVALID_REQUEST", Message: "筛选请求必须使用 JEV 结构化协议"})
				return
			}
			payload, err = service.BuildBelloBuddyScreenRequest(input.Request)
		}
		if err != nil {
			abortBelloBuddyAI(c, err)
			return
		}
		token, err := model.GetOrCreateBelloBuddyRelayToken(identity.UserID)
		if errors.Is(err, model.ErrTokenInvalid) || (err == nil && (token.Status != common.TokenStatusEnabled || (token.ExpiredTime != -1 && token.ExpiredTime <= common.GetTimestamp()) || (!token.UnlimitedQuota && token.RemainQuota <= 0))) {
			abortBelloBuddyAI(c, &service.BelloBuddyError{Status: http.StatusForbidden, Code: "AI_CREDENTIAL_DISABLED", Message: "BelloBuddy AI 凭据已停用，请联系管理员"})
			return
		}
		if err != nil {
			abortBelloBuddyAI(c, err)
			return
		}
		common.CleanupBodyStorage(c)
		storage, err = common.CreateBodyStorage(payload)
		if err != nil {
			abortBelloBuddyAI(c, err)
			return
		}
		c.Set(common.KeyBodyStorage, storage)
		c.Set(common.KeyRequestBody, nil)
		c.Request.Body = io.NopCloser(bytes.NewReader(payload))
		c.Request.ContentLength = int64(len(payload))
		originalPath, originalQuery, originalRawPath := c.Request.URL.Path, c.Request.URL.RawQuery, c.Request.URL.RawPath
		originalAuthorization := c.GetHeader("Authorization")
		originalProtocol := c.Request.Header.Values("Sec-WebSocket-Protocol")
		c.Request.Header.Del("Sec-WebSocket-Protocol")
		c.Request.URL.Path, c.Request.URL.RawQuery, c.Request.URL.RawPath = path, "", ""
		c.Request.Header.Set("Authorization", "Bearer "+token.Key)
		c.Set("bello_relay_internal", true)
		c.Set("bello_relay_token_id", token.Id)
		c.Set("bello_ai_route", route)
		c.Set("bello_ai_reply", reply)
		service.GetChannelConstraints(c).AddFilter(dto.ChannelFilter{Kind: dto.FilterChannelType, ChannelType: channelType})
		if route.ChannelID > 0 {
			service.GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: route.ChannelID, Source: dto.PinSourceBelloBuddy, Rank: -10, RetryMode: dto.PinRetrySingleAttempt})
		}
		defer func() {
			c.Request.URL.Path, c.Request.URL.RawQuery, c.Request.URL.RawPath = originalPath, originalQuery, originalRawPath
			c.Request.Header.Set("Authorization", originalAuthorization)
			for _, value := range originalProtocol {
				c.Request.Header.Add("Sec-WebSocket-Protocol", value)
			}
			c.Set("bello_relay_internal", false)
		}()
		c.Next()
	}
}

func BelloBuddyAIChannelAccess() gin.HandlerFunc {
	return func(c *gin.Context) {
		route, ok := common.GetContextKeyType[*service.BelloBuddyAIRoute](c, "bello_ai_route")
		if !ok || route == nil {
			abortBelloBuddyAI(c, service.ErrBelloBuddyConfig)
			return
		}
		if c.GetInt("token_id") != c.GetInt("bello_relay_token_id") {
			abortBelloBuddyAI(c, service.ErrBelloBuddyConfig)
			return
		}
		if c.GetBool("token_model_limit_enabled") {
			limits, ok := common.GetContextKeyType[map[string]bool](c, "token_model_limit")
			if !ok || !TokenModelLimitAllows(limits, route.Model) {
				abortBelloBuddyAI(c, &service.BelloBuddyError{Status: http.StatusForbidden, Code: "AI_RESOURCE_FORBIDDEN", Message: "内部凭据无权使用该模型"})
				return
			}
		}
		if route.ChannelID > 0 {
			if err := service.CheckBelloBuddyChannelAccess(common.GetContextKeyString(c, constant.ContextKeyUsingGroup), route.Model, route.ChannelID); err != nil {
				abortBelloBuddyAI(c, err)
				return
			}
		}
		c.Next()
	}
}

// 分发完成后锁定一次调用，并核对实际中继上下文与会话引用的目标一致。
func BelloBuddyAISelectedChannel() gin.HandlerFunc {
	return func(c *gin.Context) {
		route, ok := common.GetContextKeyType[*service.BelloBuddyAIRoute](c, "bello_ai_route")
		if !ok || route == nil {
			abortBelloBuddyAI(c, service.ErrBelloBuddyConfig)
			return
		}
		channelID := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
		if route.ChannelID > 0 {
			signature := model.BelloBuddyIdentityHash(common.GetContextKeyString(c, constant.ContextKeyChannelBaseUrl) + "\x00" + common.GetContextKeyString(c, constant.ContextKeyChannelModelMapping) + "\x00" + route.Model)
			if channelID != route.ChannelID || signature != route.Scope.ChannelSignature {
				abortBelloBuddyAI(c, &service.BelloBuddyError{Status: http.StatusConflict, Code: "BOT_BINDING_CHANGED", Message: "机器人渠道配置已变更，请重新读取职位配置"})
				return
			}
		}
		if channelID <= 0 {
			abortBelloBuddyAI(c, service.ErrBelloBuddyConfig)
			return
		}
		service.GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: channelID, Source: dto.PinSourceBelloBuddy, Rank: -10, RetryMode: dto.PinRetrySingleAttempt})
		c.Next()
	}
}
