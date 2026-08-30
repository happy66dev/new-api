package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	virtualmodelservice "github.com/QuantumNous/new-api/service/virtualmodel"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// init 确保流式超时全局常量非零，避免 StreamScannerHandler 里 time.NewTicker(0) 直接 panic 喵。
func init() {
	gin.SetMode(gin.TestMode)
	if constant.StreamingTimeout == 0 {
		constant.StreamingTimeout = 30
	}
}

// TestApplyUsagePostProcessingNormalizesCacheReadInputTokensOnce 验证中转站风格 cache_read_input_tokens
// 并入 CachedTokens 后必须清零，防止 CachedTokensTotal() 将同一批命中 token 双重计费喵。
func TestApplyUsagePostProcessingNormalizesCacheReadInputTokensOnce(t *testing.T) {
	// 仅回传 cache_read_input_tokens 的典型中转站 usage 喵。
	const cacheReadInputTokens = 7
	usage := &dto.Usage{
		PromptTokensDetails: dto.InputTokenDetails{CacheReadInputTokens: cacheReadInputTokens},
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	applyUsagePostProcessing(info, usage, nil)

	// 归一化后值只存在于一个字段，总和必须等于原始单值，绝不双计喵。
	require.Equal(t, cacheReadInputTokens, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 0, usage.PromptTokensDetails.CacheReadInputTokens)
	require.Equal(t, cacheReadInputTokens, usage.PromptTokensDetails.CachedTokensTotal())
}

// TestApplyUsagePostProcessingKeepsExplicitCachedTokens 验证上游同时回传两个字段时保留显式 cached_tokens，
// 不因 CacheReadInputTokens 非零而覆盖（该场景本就按两者之和计费，属于上游自身语义）喵。
func TestApplyUsagePostProcessingKeepsExplicitCachedTokens(t *testing.T) {
	usage := &dto.Usage{
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         5,
			CacheReadInputTokens: 6,
		},
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	applyUsagePostProcessing(info, usage, nil)

	require.Equal(t, 5, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 6, usage.PromptTokensDetails.CacheReadInputTokens)
	require.Equal(t, 11, usage.PromptTokensDetails.CachedTokensTotal())
}

// TestApplyUsagePostProcessingNilInputs 验证空指针输入被安全忽略喵。
func TestApplyUsagePostProcessingNilInputs(t *testing.T) {
	applyUsagePostProcessing(nil, nil, nil)
	applyUsagePostProcessing(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, nil, nil)
	// 走到这里不 panic 即通过喵。
}

// TestOpenaiTTSHandler_StreamProbeFailureDoesNotCommitStatus 验证 TTS 流式探测失败时
// 响应状态未被提前提交（WriteHeader 推迟到探测放流之后），调用方才能回 502/切换候选喵。
func TestOpenaiTTSHandler_StreamProbeFailureDoesNotCommitStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 探测参数：内容门槛 10，空流在放流前结束会触发探测失败喵。
	params := virtualmodelservice.ProbeParameters{StallTimeoutSeconds: 60, MinContentChars: 10, ProbeTotalTimeoutSeconds: 60}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", nil)
	common.SetContextKey(c, constant.ContextKeyVirtualModelProbeParameters, params)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("data: [DONE]\n")),
	}
	info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{}}

	usage, apiErr := OpenaiTTSHandler(c, resp, info)

	require.Nil(t, usage)
	require.NotNil(t, apiErr)
	assert.False(t, c.Writer.Written(), "探测失败时不得提前提交响应状态，否则无法回 502/切换候选")
	assert.Empty(t, recorder.Body.String())
}

// TestOpenaiTTSHandler_StreamProbeSuccessWritesContent 验证 TTS 流式探测放流成功后正常转发内容喵。
func TestOpenaiTTSHandler_StreamProbeSuccessWritesContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 内容门槛 5，首行内容 "Hello" 达到门槛即放流喵。
	params := virtualmodelservice.ProbeParameters{StallTimeoutSeconds: 60, MinContentChars: 5, ProbeTotalTimeoutSeconds: 60}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", nil)
	common.SetContextKey(c, constant.ContextKeyVirtualModelProbeParameters, params)

	body := "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n" +
		"data: [DONE]\n"
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{}}

	usage, apiErr := OpenaiTTSHandler(c, resp, info)

	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.True(t, c.Writer.Written(), "放流后 dataHandler 写入应隐式提交响应状态")
	assert.Contains(t, recorder.Body.String(), "Hello")
}
