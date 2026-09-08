package virtualmodel

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestExecuteUserUpstreamModelAnthropicCacheSplit 验证 anthropic 语义透传把缓存读/写单独拆分、
// 并与 new-api 内部口径对齐（input 字段=输入+缓存读+缓存写，prompt 列保持原始 input_tokens）喵。
func TestExecuteUserUpstreamModelAnthropicCacheSplit(t *testing.T) {
	// 开发模式允许本地 http mock 上游，测试结束后自动恢复环境变量喵。
	t.Setenv("VIRTUAL_MODEL_INSECURE_UPSTREAM", "1")
	gin.SetMode(gin.TestMode)
	// mock 上游按 Anthropic SSE 格式回包：message_start 带 input/cache、message_delta 带最终 output 喵。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"usage\":{\"input_tokens\":40,\"output_tokens\":1,\"cache_creation_input_tokens\":4,\"cache_read_input_tokens\":12}}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n"))
		_, _ = w.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":9}}\n\n"))
		_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "https://gateway.test/v1/messages", strings.NewReader(`{"model":"user/demo","stream":true,"max_tokens":100,"messages":[]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	result := ExecuteUserUpstreamModel(ctx, CustomCandidateExecutionInput{
		BaseURL:        upstream.URL,
		APIKey:         "sk-test",
		RealModelName:  "claude-sonnet-4",
		AuthStyle:      "anthropic",
		TimeoutSeconds: 10,
	})
	require.NoError(t, result.Err)
	require.NotNil(t, result.Usage)
	// prompt 列保持原始 input_tokens（不含缓存读取），与内部日志语义一致喵。
	require.Equal(t, 40, result.Usage.PromptTokens)
	// 缓存读取/写入进入规范分类，参与缓存价计费与状态页缓存命中统计喵。
	require.Equal(t, 12, result.Usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 4, result.Usage.PromptTokensDetails.CachedCreationTokens)
	// input 字段 = 输入 + 缓存读取 + 缓存写入，与内部 Claude 口径对齐喵。
	require.Equal(t, 56, result.Usage.InputTokens)
	// 输出取最终 message_delta 的累计 output_tokens 喵。
	require.Equal(t, 9, result.Usage.CompletionTokens)
	require.Equal(t, http.StatusOK, recorder.Code)
}
