package typesafe

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// Adaptor only accepts the native System One protocol.
type Adaptor struct{}

func (*Adaptor) Init(*relaycommon.RelayInfo) {}

func (*Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info.RelayFormat != types.RelayFormatTypeSafe {
		return "", errors.New("TypeSafe only supports System One")
	}
	base := strings.TrimRight(info.ChannelBaseUrl, "/")
	if base == "" {
		base = constant.GetChannelBaseURL(constant.ChannelTypeTypeSafe)
	}
	return base + "/v1/systemone", nil
}

func (*Adaptor) SetupRequestHeader(_ *gin.Context, header *http.Header, info *relaycommon.RelayInfo) error {
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "application/json")
	header.Set("Authorization", "Bearer "+info.ApiKey)
	return nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, body io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, body)
}

func (*Adaptor) ConvertOpenAIRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeneralOpenAIRequest) (any, error) {
	return nil, errors.New("TypeSafe does not support OpenAI chat requests")
}

func (*Adaptor) ConvertRerankRequest(*gin.Context, int, dto.RerankRequest) (any, error) {
	return nil, errors.New("TypeSafe does not support rerank requests")
}

func (*Adaptor) ConvertEmbeddingRequest(*gin.Context, *relaycommon.RelayInfo, dto.EmbeddingRequest) (any, error) {
	return nil, errors.New("TypeSafe does not support embedding requests")
}

func (*Adaptor) ConvertAudioRequest(*gin.Context, *relaycommon.RelayInfo, dto.AudioRequest) (io.Reader, error) {
	return nil, errors.New("TypeSafe does not support audio requests")
}

func (*Adaptor) ConvertImageRequest(*gin.Context, *relaycommon.RelayInfo, dto.ImageRequest) (any, error) {
	return nil, errors.New("TypeSafe does not support image requests")
}

func (*Adaptor) ConvertOpenAIResponsesRequest(*gin.Context, *relaycommon.RelayInfo, dto.OpenAIResponsesRequest) (any, error) {
	return nil, errors.New("TypeSafe does not support OpenAI Responses requests")
}

func (*Adaptor) ConvertClaudeRequest(*gin.Context, *relaycommon.RelayInfo, *dto.ClaudeRequest) (any, error) {
	return nil, errors.New("TypeSafe does not support Anthropic messages")
}

func (*Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	return nil, errors.New("TypeSafe does not support Gemini requests")
}

func (*Adaptor) DoResponse(c *gin.Context, resp *http.Response, _ *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	defer resp.Body.Close()
	const maxResponseBytes = 16 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, types.NewError(fmt.Errorf("read TypeSafe response: %w", err), types.ErrorCodeBadResponseBody)
	}
	if len(body) > maxResponseBytes {
		return nil, types.NewError(errors.New("TypeSafe response is too large"), types.ErrorCodeBadResponseBody)
	}
	var result struct {
		Answers json.RawMessage `json:"answers"`
		Usage   *struct {
			InputTokens  *int `json:"input_tokens"`
			OutputTokens *int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := common.Unmarshal(body, &result); err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if common.GetJsonType(result.Answers) != "object" || result.Usage == nil || result.Usage.InputTokens == nil || result.Usage.OutputTokens == nil {
		return nil, types.NewError(errors.New("TypeSafe response is missing answers or token usage"), types.ErrorCodeBadResponseBody)
	}
	input, output := *result.Usage.InputTokens, *result.Usage.OutputTokens
	if input < 0 || output < 0 || input > math.MaxInt32 || output > math.MaxInt32 || input > math.MaxInt32-output {
		return nil, types.NewError(errors.New("TypeSafe response has invalid token usage"), types.ErrorCodeBadResponseBody)
	}
	usage := &dto.Usage{PromptTokens: input, CompletionTokens: output, TotalTokens: input + output}
	c.Data(http.StatusOK, "application/json", body)
	return usage, nil
}

func (*Adaptor) GetModelList() []string { return []string{"jev-latest", "jev-preview", "jev-1.13.0"} }

func (*Adaptor) GetChannelName() string { return "TypeSafe AI" }
