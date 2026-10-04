package relay

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/sjson"
)

func TypeSafeHelper(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	info.InitChannelMeta(c)
	if info.ChannelType != constant.ChannelTypeTypeSafe {
		return types.NewErrorWithStatusCode(errors.New("System One requires a TypeSafe channel"), types.ErrorCodeInvalidApiType, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	request, ok := info.Request.(*dto.TypeSafeRequest)
	if !ok {
		return types.NewErrorWithStatusCode(errors.New("invalid System One request"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if err := helper.ModelMappedHelper(c, info, request); err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}
	if len(request.RawBody) == 0 {
		return types.NewError(errors.New("empty System One body"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	body, err := sjson.SetBytes(request.RawBody, "model", request.Model)
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if request.Stream != nil {
		body, err = sjson.DeleteBytes(body, "stream")
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
	}
	if len(info.ParamOverride) > 0 {
		body, err = relaycommon.ApplyParamOverrideWithRelayInfo(body, info)
		if err != nil {
			return newAPIErrorFromParamOverride(err)
		}
	}
	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)
	response, err := adaptor.DoRequest(c, info, bytes.NewReader(body))
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	httpResp, ok := response.(*http.Response)
	if !ok || httpResp == nil {
		return types.NewError(errors.New("empty TypeSafe upstream response"), types.ErrorCodeBadResponse)
	}
	if httpResp.StatusCode != http.StatusOK {
		apiErr := service.RelayErrorHandler(c.Request.Context(), httpResp, false)
		service.ResetStatusCode(apiErr, c.GetString("status_code_mapping"))
		return apiErr
	}
	usageValue, apiErr := adaptor.DoResponse(c, httpResp, info)
	if apiErr != nil {
		service.ResetStatusCode(apiErr, c.GetString("status_code_mapping"))
		return apiErr
	}
	usage, ok := usageValue.(*dto.Usage)
	if !ok {
		return types.NewError(errors.New("invalid TypeSafe usage"), types.ErrorCodeBadResponseBody)
	}
	service.PostTextConsumeQuota(c, info, usage, nil)
	return nil
}
