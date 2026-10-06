package controller

import (
	"bytes"
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel/maxkb"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// 非流式桥接在原生中继结算完成后才向客户端发布结果。
// 隔离头部并限制缓冲区，避免透出上游响应及裸 chat_id。
type belloBuddyResponseWriter struct {
	gin.ResponseWriter
	header  http.Header
	body    bytes.Buffer
	status  int
	written bool
}

func (w *belloBuddyResponseWriter) Header() http.Header { return w.header }
func (w *belloBuddyResponseWriter) WriteHeader(status int) {
	if !w.written {
		w.status = status
	}
}
func (w *belloBuddyResponseWriter) WriteHeaderNow() { w.written = true }
func (w *belloBuddyResponseWriter) Write(data []byte) (int, error) {
	w.WriteHeaderNow()
	if w.body.Len()+len(data) > (16<<20)+65536 {
		return 0, errors.New("BelloBuddy response exceeds size limit")
	}
	return w.body.Write(data)
}
func (w *belloBuddyResponseWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}
func (w *belloBuddyResponseWriter) Status() int { return w.status }
func (w *belloBuddyResponseWriter) Size() int {
	if !w.written {
		return -1
	}
	return w.body.Len()
}
func (w *belloBuddyResponseWriter) Written() bool { return w.written }
func (w *belloBuddyResponseWriter) Flush()        { w.WriteHeaderNow() }

func BelloBuddyAIResponse() gin.HandlerFunc {
	return func(c *gin.Context) {
		original := c.Writer
		buffer := &belloBuddyResponseWriter{ResponseWriter: original, header: original.Header().Clone(), status: http.StatusOK}
		c.Writer = buffer
		defer func() { c.Writer = original }()
		c.Next()
		c.Writer = original
		requestID := c.GetString(common.RequestIdKey)
		if buffer.status >= http.StatusBadRequest {
			var failure struct {
				Success *bool              `json:"success"`
				Code    string             `json:"code"`
				Message string             `json:"message"`
				Source  string             `json:"source"`
				Error   *types.OpenAIError `json:"error"`
			}
			_ = common.Unmarshal(buffer.body.Bytes(), &failure)
			if failure.Success != nil && !*failure.Success && failure.Source == "dpin" {
				c.JSON(buffer.status, gin.H{"success": false, "code": failure.Code, "message": failure.Message, "source": "dpin", "request_id": requestID, "retryable": false})
				return
			}
			code, source := "AI_REQUEST_FAILED", "dpin"
			if failure.Error != nil {
				if value, ok := failure.Error.Code.(string); ok && value != "" {
					code = value
				}
			}
			for _, event := range service.RequestPolicy(c).Events() {
				if event.Decision.Action == "failure" {
					switch event.ErrorCode {
					case string(types.ErrorCodeDoRequestFailed), string(types.ErrorCodeBadResponseBody), string(types.ErrorCodeReadResponseBodyFailed), string(types.ErrorCodeBadResponse), string(types.ErrorCodeBadResponseStatusCode):
						source = "upstream"
					default:
						if event.ErrorSource == "upstream" || event.ErrorSource == "transport" {
							source = "upstream"
						}
					}
				}
			}
			switch code {
			case string(types.ErrorCodeInsufficientUserQuota), string(types.ErrorCodePreConsumeTokenQuotaFailed):
				code = "AI_QUOTA_EXHAUSTED"
			case string(types.ErrorCodeAccessDenied):
				code = "AI_RESOURCE_FORBIDDEN"
			case string(types.ErrorCodeGetChannelFailed):
				code = "AI_RESOURCE_UNAVAILABLE"
			case string(types.ErrorCodeModelPriceError):
				code = "AI_PRICE_NOT_CONFIGURED"
			case string(types.ErrorCodeInvalidRequest), string(types.ErrorCodeReadRequestBodyFailed):
				code = "INVALID_REQUEST"
			case string(types.ErrorCodeDoRequestFailed):
				code = "AI_UPSTREAM_UNAVAILABLE"
			case string(types.ErrorCodeBadResponseBody), string(types.ErrorCodeReadResponseBodyFailed), string(types.ErrorCodeBadResponse):
				code = "AI_UPSTREAM_RESPONSE_INVALID"
			default:
				code = "AI_REQUEST_FAILED"
				if source == "upstream" {
					code = "AI_UPSTREAM_REJECTED"
				}
			}
			// 上游 401/403 不等于产品会话失效，保留来源并以网关失败状态返回。
			status := buffer.status
			if source == "upstream" {
				status = http.StatusBadGateway
			}
			c.JSON(status, gin.H{"success": false, "code": code, "message": "AI 调用未成功，请根据错误来源检查服务或权限", "source": source, "request_id": requestID, "retryable": false})
			return
		}
		var result common.RawMessage = buffer.body.Bytes()
		if !c.GetBool("bello_ai_reply") {
			if common.GetJsonType(result) != "object" {
				belloReply(c, nil, service.ErrBelloBuddyConfig)
				return
			}
			c.JSON(http.StatusOK, gin.H{"success": true, "data": result, "request_id": requestID})
			return
		}
		route, ok := common.GetContextKeyType[*service.BelloBuddyAIRoute](c, "bello_ai_route")
		var response dto.TextResponse
		if !ok || route == nil || common.Unmarshal(result, &response) != nil || len(response.Choices) != 1 {
			belloReply(c, nil, service.ErrBelloBuddyConfig)
			return
		}
		reference, err := service.SealBelloBuddyConversation(route.Scope, c.GetString(maxkb.ContextReturnedChatID))
		if err != nil {
			belloReply(c, nil, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
			"text": response.Choices[0].Message.Content, "model": route.Model, "usage": response.Usage,
			"conversation_ref": reference, "binding_version": route.Scope.BindingVersion,
		}, "request_id": requestID})
	}
}

func RelayBelloBuddyAI(c *gin.Context) {
	format := types.RelayFormat(types.RelayFormatTypeSafe)
	if c.GetBool("bello_ai_reply") {
		format = types.RelayFormatOpenAI
	}
	Relay(c, format)
}
