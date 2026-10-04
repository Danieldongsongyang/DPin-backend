package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	filterdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTypeSafeSystemOneForwardsNativePayloadAndUsage(t *testing.T) {
	const payload = `{"model":"jev-latest","state":{"ticket":"urgent"},"questions":{"urgent":{"type":"noul","instructions":"Is this urgent?"}}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/systemone", r.URL.Path)
		assert.Equal(t, "Bearer typesafe-secret", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.JSONEq(t, payload, string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"jev-1.13.0","answers":{"urgent":{"type":"noul","noul":0.95}},"usage":{"input_tokens":296,"output_tokens":20}}`)
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")
	request, err := helper.GetAndValidateTypeSafeRequest(c)
	require.NoError(t, err)
	assert.Equal(t, "jev-latest", request.Model)
	assert.Equal(t, payload, string(request.RawBody))

	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatTypeSafe,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeTypeSafe,
			ChannelBaseUrl: upstream.URL,
			ApiKey:         "typesafe-secret",
		},
	}
	adaptor := GetAdaptor(constant.APITypeTypeSafe)
	require.NotNil(t, adaptor)
	response, err := adaptor.DoRequest(c, info, strings.NewReader(payload))
	require.NoError(t, err)
	httpResponse, ok := response.(*http.Response)
	require.True(t, ok)
	require.Equal(t, http.StatusOK, httpResponse.StatusCode)
	usageValue, apiErr := adaptor.DoResponse(c, httpResponse, info)
	require.Nil(t, apiErr)
	usage, ok := usageValue.(*kitdto.Usage)
	require.True(t, ok)
	assert.Equal(t, 296, usage.PromptTokens)
	assert.Equal(t, 20, usage.CompletionTokens)
	assert.Equal(t, 316, usage.TotalTokens)
	assert.Contains(t, recorder.Body.String(), `"answers"`)
}

func TestTypeSafeSystemOneRejectsInvalidRequestsAndUsage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		wantErr string
	}{
		{"missing questions", `{"model":"jev-latest","state":"text"}`, "questions"},
		{"unsupported stream", `{"model":"jev-latest","state":"text","questions":{"q":{"type":"noul","instructions":"yes?"}},"stream":true}`, "stream"},
		{"invalid choice", `{"model":"jev-latest","state":"text","questions":{"q":{"type":"choice","instructions":"Which?","criteria":{}}}}`, "criteria"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			_, err := helper.GetAndValidateTypeSafeRequest(c)
			require.ErrorContains(t, err, tc.wantErr)
		})
	}

	channel := &model.Channel{Type: constant.ChannelTypeTypeSafe}
	valid, _ := model.ChannelSatisfiesFilters(channel, "jev-latest", []filterdto.ChannelFilter{{Kind: filterdto.FilterRequestPath, RequestPath: "/v1/systemone"}})
	assert.True(t, valid)
	valid, _ = model.ChannelSatisfiesFilters(channel, "jev-latest", []filterdto.ChannelFilter{{Kind: filterdto.FilterRequestPath, RequestPath: "/v1/images/generations"}})
	assert.False(t, valid)

	for _, body := range []string{
		`{"answers":{},"usage":{"input_tokens":-1,"output_tokens":0}}`,
		`{"answers":{},"usage":{"output_tokens":5}}`,
		`{"answers":{},"usage":{"input_tokens":2147483647,"output_tokens":1}}`,
	} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		response := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
		adaptor := GetAdaptor(constant.APITypeTypeSafe)
		usage, apiErr := adaptor.DoResponse(c, response, &relaycommon.RelayInfo{})
		require.NotNil(t, apiErr)
		assert.Nil(t, usage)
		assert.Empty(t, recorder.Body.String())
	}

	assert.Equal(t, "https://api.typesafe.ai", constant.GetChannelBaseURL(constant.ChannelTypeTypeSafe))
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeTypeSafe}, common.GetEndpointTypesByChannelType(constant.ChannelTypeTypeSafe, "jev-latest"))
}

func TestTypeSafeBuiltinPriceChargesOnlyInputTokens(t *testing.T) {
	expression, ok := billing_setting.GetBuiltinBillingExpr("jev-latest")
	require.True(t, ok)
	result, err := billingexpr.ComputeTieredQuota(&billingexpr.BillingSnapshot{
		ExprString:   expression,
		GroupRatio:   1,
		QuotaPerUnit: 500000,
	}, billingexpr.TokenParams{P: 1_000_000, C: 100_000})
	require.NoError(t, err)
	assert.Equal(t, 21_000, result.ActualQuotaAfterGroup)
}
