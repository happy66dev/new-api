package virtualmodel

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecuteUserUpstreamModelUsageStreaming 验证流式响应末尾 usage 事件被解析喵。
func TestExecuteUserUpstreamModelUsageStreaming(t *testing.T) {
	// 开发模式允许本地 http mock 上游，测试结束后自动恢复环境变量喵。
	t.Setenv("VIRTUAL_MODEL_INSECURE_UPSTREAM", "1")
	gin.SetMode(gin.TestMode)
	// mock 上游按 OpenAI 流式格式回包：内容 chunk → 末尾带 usage 的 chunk → [DONE] 喵。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":50,\"total_tokens\":150}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "https://gateway.test/v1/chat/completions", strings.NewReader(`{"model":"user/demo","stream":true,"messages":[]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	result := ExecuteUserUpstreamModel(ctx, CustomCandidateExecutionInput{
		BaseURL:        upstream.URL,
		APIKey:         "sk-test",
		RealModelName:  "gpt-4o",
		AuthStyle:      "bearer",
		TimeoutSeconds: 10,
	})
	require.NoError(t, result.Err)
	// 流式 usage 必须被解析到返回结构，供日志 token 与计费使用喵。
	require.NotNil(t, result.Usage)
	assert.Equal(t, 100, result.Usage.PromptTokens)
	assert.Equal(t, 50, result.Usage.CompletionTokens)
	// 透传响应已写入客户端 recorder 喵。
	assert.Equal(t, http.StatusOK, recorder.Code)
}

// TestExecuteUserUpstreamModelUsageNonStreaming 验证非流式响应顶层 usage 被解析喵。
func TestExecuteUserUpstreamModelUsageNonStreaming(t *testing.T) {
	t.Setenv("VIRTUAL_MODEL_INSECURE_UPSTREAM", "1")
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","object":"chat.completion","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "https://gateway.test/v1/chat/completions", strings.NewReader(`{"model":"user/demo","stream":false,"messages":[]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	result := ExecuteUserUpstreamModel(ctx, CustomCandidateExecutionInput{
		BaseURL:        upstream.URL,
		APIKey:         "sk-test",
		RealModelName:  "gpt-4o",
		AuthStyle:      "bearer",
		TimeoutSeconds: 10,
	})
	require.NoError(t, result.Err)
	// 非流式 usage 解析到标准 token 字段喵。
	require.NotNil(t, result.Usage)
	assert.Equal(t, 10, result.Usage.PromptTokens)
	assert.Equal(t, 5, result.Usage.CompletionTokens)
	// 上游真实提供 usage 时不得误标为估计喵。
	assert.Nil(t, result.Usage.BillingUsage)
}

// TestExecuteUserUpstreamModelNoUsage 验证上游不返回 usage 时结果不带 nil 混淆喵。
func TestExecuteUserUpstreamModelNoUsage(t *testing.T) {
	t.Setenv("VIRTUAL_MODEL_INSECURE_UPSTREAM", "1")
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","object":"chat.completion","choices":[{"message":{"content":"hi"}}]}`))
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "https://gateway.test/v1/chat/completions", strings.NewReader(`{"model":"user/demo","stream":false,"messages":[]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	result := ExecuteUserUpstreamModel(ctx, CustomCandidateExecutionInput{
		BaseURL:        upstream.URL,
		APIKey:         "sk-test",
		RealModelName:  "gpt-4o",
		AuthStyle:      "bearer",
		TimeoutSeconds: 10,
	})
	require.NoError(t, result.Err)
	// 上游不返回 usage 时按请求/响应文本估计 token，打上 Estimated 标记供日志与前端「?」展示喵。
	require.NotNil(t, result.Usage)
	assert.True(t, result.Usage.BillingUsage != nil && result.Usage.BillingUsage.Estimated)
	assert.Positive(t, result.Usage.TotalTokens)
	// 透传响应仍写入客户端，不因缺 usage 而失败喵。
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "hi")
}

// TestExecuteUserUpstreamModelParseDTO 验证解析出的 usage 能映射到 new-api 标准字段（含 input/output 风格）喵。
func TestExecuteUserUpstreamModelParseDTO(t *testing.T) {
	t.Setenv("VIRTUAL_MODEL_INSECURE_UPSTREAM", "1")
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Anthropic/Responses 风格：input_tokens / output_tokens 兜底回填标准字段喵。
		_, _ = w.Write([]byte(`{"id":"1","usage":{"input_tokens":200,"output_tokens":80}}`))
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "https://gateway.test/v1/chat/completions", strings.NewReader(`{"model":"user/demo","stream":false,"messages":[]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	result := ExecuteUserUpstreamModel(ctx, CustomCandidateExecutionInput{
		BaseURL:        upstream.URL,
		APIKey:         "sk-test",
		RealModelName:  "gpt-4o",
		AuthStyle:      "bearer",
		TimeoutSeconds: 10,
	})
	require.NoError(t, result.Err)
	require.NotNil(t, result.Usage)
	// input/output 风格经归一化回填到 prompt/completion 标准字段喵。
	assert.Equal(t, 200, result.Usage.PromptTokens)
	assert.Equal(t, 80, result.Usage.CompletionTokens)
}
