package maxkb

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Conversation IDs are installed by the authenticated host bridge, never read
// from an arbitrary model request. The host must bind them to user/account/job.
const ContextChatID = "maxkb_chat_id"
const ContextScopeID = "maxkb_scope_id"
const ContextReturnedChatID = "maxkb_returned_chat_id"

const maxBodyBytes = 1 << 20

var errUnsupported = errors.New("MaxKB only supports non-streaming single-user text turns; configure generation parameters in the MaxKB application")

type Adaptor struct{}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   *bool         `json:"stream,omitempty"`
	ChatID   string        `json:"chat_id,omitempty"`
	ScopeID  string        `json:"dpin_scope,omitempty"`
}

func (*Adaptor) Init(*relaycommon.RelayInfo) {}

func (*Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info.RelayFormat != types.RelayFormatOpenAI || info.IsStream {
		return "", errUnsupported
	}
	appID, err := uuid.Parse(info.UpstreamModelName)
	if err != nil || appID == uuid.Nil {
		return "", errors.New("MaxKB model mapping must specify an application UUID")
	}
	base, err := url.Parse(info.ChannelBaseUrl)
	if err != nil || base == nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return "", errors.New("invalid MaxKB deployment address")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/chat/api/" + appID.String() + "/chat/completions"
	base.RawPath = ""
	return base.String(), nil
}

func (*Adaptor) SetupRequestHeader(c *gin.Context, header *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, header)
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "application/json")
	header.Set("Authorization", "Bearer "+info.ApiKey)
	if c.GetString(ContextScopeID) != "" {
		for _, name := range []string{"X-DPIN-Scope", "X-DPIN-Timestamp", "X-DPIN-Nonce", "X-DPIN-Signature"} {
			value := c.GetString("maxkb_signed_" + name)
			if value == "" {
				return errors.New("MaxKB identity signature is unavailable")
			}
			header.Set(name, value)
		}
	}
	return nil
}

func (*Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil || info.IsStream || (request.Stream != nil && *request.Stream) || len(request.Messages) != 1 {
		return nil, errUnsupported
	}
	// MaxKB consumes only the last message and owns its history. Reject controls
	// it cannot honor rather than silently dropping history, tools or max_tokens.
	encoded, err := common.Marshal(request)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := common.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	for key := range fields {
		if key != "model" && key != "messages" && key != "stream" {
			return nil, errUnsupported
		}
	}
	message := request.Messages[0]
	content, ok := message.Content.(string)
	if !ok || message.Role != "user" || strings.TrimSpace(content) == "" || message.Name != nil || message.Prefix != nil || message.ReasoningContent != nil || message.Reasoning != nil || len(message.ToolCalls) > 0 || message.ToolCallId != "" || len(message.Tools) > 0 || len(message.Annotations) > 0 {
		return nil, errUnsupported
	}
	stream := false
	return chatRequest{Model: info.UpstreamModelName, Messages: []chatMessage{{Role: "user", Content: content}}, Stream: &stream, ChatID: c.GetString(ContextChatID), ScopeID: c.GetString(ContextScopeID)}, nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, body io.Reader) (any, error) {
	// Validate again here: global/channel passthrough must not bypass this
	// adaptor's field, generation-count, stream and trusted-conversation rules.
	data, err := io.ReadAll(io.LimitReader(body, maxBodyBytes+1))
	if err != nil || len(data) > maxBodyBytes {
		return nil, errors.New("invalid or oversized MaxKB request")
	}
	var fields map[string]json.RawMessage
	var request chatRequest
	if common.Unmarshal(data, &fields) != nil || common.Unmarshal(data, &request) != nil {
		return nil, errUnsupported
	}
	for key := range fields {
		if key != "model" && key != "messages" && key != "stream" && key != "chat_id" && key != "dpin_scope" {
			return nil, errUnsupported
		}
	}
	if info.IsStream || (request.Stream != nil && *request.Stream) || request.Model != info.UpstreamModelName || len(request.Messages) != 1 || request.Messages[0].Role != "user" || strings.TrimSpace(request.Messages[0].Content) == "" || request.ChatID != c.GetString(ContextChatID) || request.ScopeID != c.GetString(ContextScopeID) {
		return nil, errUnsupported
	}
	var messages []map[string]json.RawMessage
	if common.Unmarshal(fields["messages"], &messages) != nil || len(messages) != 1 || len(messages[0]) != 2 {
		return nil, errUnsupported
	}
	if request.ChatID != "" {
		chatID, err := uuid.Parse(request.ChatID)
		if err != nil || chatID == uuid.Nil {
			return nil, errors.New("invalid MaxKB conversation reference")
		}
	}
	if request.ScopeID != "" {
		secret := os.Getenv("DPIN_MAXKB_IDENTITY_SECRET")
		if len(secret) < 32 {
			return nil, errors.New("MaxKB identity is not configured")
		}
		nonce := make([]byte, 16)
		if _, err := rand.Read(nonce); err != nil {
			return nil, err
		}
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		nonceHex := hex.EncodeToString(nonce)
		digest := sha256.Sum256(data)
		appID, _ := uuid.Parse(info.UpstreamModelName)
		signed := strings.Join([]string{"dpin-maxkb-scope-v1", appID.String(), request.ScopeID, stamp, nonceHex, hex.EncodeToString(digest[:])}, "\n")
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(signed))
		c.Set("maxkb_signed_X-DPIN-Scope", request.ScopeID)
		c.Set("maxkb_signed_X-DPIN-Timestamp", stamp)
		c.Set("maxkb_signed_X-DPIN-Nonce", nonceHex)
		c.Set("maxkb_signed_X-DPIN-Signature", hex.EncodeToString(mac.Sum(nil)))
	}
	return channel.DoApiRequest(a, c, info, strings.NewReader(string(data)))
}

func (*Adaptor) DoResponse(c *gin.Context, response *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil || len(body) > maxBodyBytes {
		return nil, types.NewError(errors.New("invalid or oversized MaxKB response"), types.ErrorCodeBadResponseBody, types.ErrOptionWithSkipRetry())
	}
	var result struct {
		ID      string `json:"id"`
		ScopeID string `json:"dpin_scope"`
		Choices []struct {
			ChatID       string      `json:"chat_id"`
			Index        int         `json:"index"`
			Message      chatMessage `json:"message"`
			FinishReason string      `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			Prompt        *int   `json:"prompt_tokens"`
			Completion    *int   `json:"completion_tokens"`
			Total         *int   `json:"total_tokens"`
			Source        string `json:"usage_source"`
			CacheSource   string `json:"cache_usage_source"`
			PromptDetails *struct {
				Cached *int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if common.Unmarshal(body, &result) != nil || result.ID == "" || len(result.ID) > 128 || len(result.Choices) != 1 || result.Choices[0].Index != 0 || result.Choices[0].Message.Role != "assistant" || strings.TrimSpace(result.Choices[0].Message.Content) == "" || result.Choices[0].FinishReason != "stop" {
		return nil, types.NewError(errors.New("MaxKB response is missing a completed text answer"), types.ErrorCodeBadResponseBody, types.ErrOptionWithSkipRetry())
	}
	if expected := c.GetString(ContextScopeID); expected != "" && result.ScopeID != expected {
		return nil, types.NewError(errors.New("MaxKB response scope does not match"), types.ErrorCodeBadResponseBody, types.ErrOptionWithSkipRetry())
	}
	chatID, err := uuid.Parse(result.Choices[0].ChatID)
	if err != nil || chatID == uuid.Nil || (c.GetString(ContextChatID) != "" && c.GetString(ContextChatID) != result.Choices[0].ChatID) {
		return nil, types.NewError(errors.New("MaxKB response has an invalid conversation reference"), types.ErrorCodeBadResponseBody, types.ErrOptionWithSkipRetry())
	}
	// The MaxKB extension must mark actual provider metadata as upstream.
	// Its unmodified API can fall back to estimates; presence of numbers alone
	// cannot distinguish that path and is insufficient for actual-usage billing.
	u := result.Usage
	if u == nil || u.Source != "upstream" || u.Prompt == nil || u.Completion == nil || u.Total == nil || *u.Prompt < 0 || *u.Completion < 0 || *u.Prompt > math.MaxInt32 || *u.Completion > math.MaxInt32 || *u.Prompt > math.MaxInt32-*u.Completion || *u.Total != *u.Prompt+*u.Completion {
		return nil, types.NewError(errors.New("MaxKB response is missing valid actual token usage"), types.ErrorCodeBadResponseBody, types.ErrOptionWithSkipRetry())
	}
	// A missing cache fact cannot be billed as a cache miss under cache pricing.
	if u.CacheSource != "upstream" || u.PromptDetails == nil || u.PromptDetails.Cached == nil || *u.PromptDetails.Cached < 0 || *u.PromptDetails.Cached > *u.Prompt {
		return nil, types.NewError(errors.New("MaxKB response is missing valid actual cache usage"), types.ErrorCodeBadResponseBody, types.ErrOptionWithSkipRetry())
	}
	usage := &dto.Usage{PromptTokens: *u.Prompt, CompletionTokens: *u.Completion, TotalTokens: *u.Total, UsageSource: "upstream"}
	usage.PromptTokensDetails.CachedTokens = *u.PromptDetails.Cached
	out := dto.TextResponse{Id: result.ID, Object: "chat.completion", Created: common.GetTimestamp(), Model: info.OriginModelName, Usage: *usage, Choices: []dto.OpenAITextResponseChoice{{Index: 0, Message: dto.Message{Role: "assistant", Content: result.Choices[0].Message.Content}, FinishReason: "stop"}}}
	encoded, err := common.Marshal(out)
	if err != nil {
		return nil, types.NewError(errors.New("failed to encode MaxKB reply"), types.ErrorCodeBadResponseBody, types.ErrOptionWithSkipRetry())
	}
	c.Set(ContextReturnedChatID, chatID.String())
	c.Data(http.StatusOK, "application/json", encoded)
	return usage, nil
}

func (*Adaptor) ConvertRerankRequest(*gin.Context, int, dto.RerankRequest) (any, error) {
	return nil, errUnsupported
}
func (*Adaptor) ConvertEmbeddingRequest(*gin.Context, *relaycommon.RelayInfo, dto.EmbeddingRequest) (any, error) {
	return nil, errUnsupported
}
func (*Adaptor) ConvertAudioRequest(*gin.Context, *relaycommon.RelayInfo, dto.AudioRequest) (io.Reader, error) {
	return nil, errUnsupported
}
func (*Adaptor) ConvertImageRequest(*gin.Context, *relaycommon.RelayInfo, dto.ImageRequest) (any, error) {
	return nil, errUnsupported
}
func (*Adaptor) ConvertOpenAIResponsesRequest(*gin.Context, *relaycommon.RelayInfo, dto.OpenAIResponsesRequest) (any, error) {
	return nil, errUnsupported
}
func (*Adaptor) ConvertClaudeRequest(*gin.Context, *relaycommon.RelayInfo, *dto.ClaudeRequest) (any, error) {
	return nil, errUnsupported
}
func (*Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	return nil, errUnsupported
}
func (*Adaptor) GetModelList() []string { return nil }
func (*Adaptor) GetChannelName() string { return "MaxKB" }
