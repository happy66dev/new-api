package virtualmodel

import (
	"io"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExtractUsageFromOpenAIBody 验证非流式 OpenAI 响应体顶层 usage 的解析喵。
func TestExtractUsageFromOpenAIBody(t *testing.T) {
	// 正常响应体提取各 token 分类，缓存/图片/音频从 details 嵌套读入喵。
	body := []byte(`{"id":"1","object":"chat.completion","usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":20,"image_tokens":5,"audio_tokens":3,"cache_write_tokens":7},"completion_tokens_details":{"audio_tokens":2}}}`)
	usage := extractUsageFromOpenAIBody(body)
	require.NotNil(t, usage)
	assert.Equal(t, 100, usage.PromptTokens)
	assert.Equal(t, 50, usage.CompletionTokens)
	assert.Equal(t, 20, usage.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 5, usage.PromptTokensDetails.ImageTokens)
	assert.Equal(t, 3, usage.PromptTokensDetails.AudioTokens)
	assert.Equal(t, 7, usage.PromptTokensDetails.CacheWriteTokens)
	assert.Equal(t, 7, usage.PromptTokensDetails.CacheCreationTokensTotal())
	assert.Equal(t, 2, usage.CompletionTokenDetails.AudioTokens)

	// 缺失 usage 字段返回 nil，表示无可计费信息喵。
	require.Nil(t, extractUsageFromOpenAIBody([]byte(`{"id":"1","object":"chat.completion"}`)))

	// 非法 JSON 返回 nil，不崩溃喵。
	require.Nil(t, extractUsageFromOpenAIBody([]byte(`not-json`)))

	// usage 全零返回 nil，避免空 usage 混入日志喵。
	require.Nil(t, extractUsageFromOpenAIBody([]byte(`{"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`)))
}

// TestExtractUsageFromSSELine 验证流式 data 行 usage 的提取与跳过规则喵。
func TestExtractUsageFromSSELine(t *testing.T) {
	// 带 usage 的 data 行被提取喵。
	usage := &dto.Usage{}
	extractUsageFromSSELine([]byte(`data: {"usage":{"prompt_tokens":30,"completion_tokens":10,"total_tokens":40}}`), usage)
	assert.Equal(t, 30, usage.PromptTokens)
	assert.Equal(t, 10, usage.CompletionTokens)

	// 无 usage 的 data 行不改动已有值喵。
	extractUsageFromSSELine([]byte(`data: {"choices":[]}`), usage)
	assert.Equal(t, 30, usage.PromptTokens)

	// 最后的非空 usage 覆盖之前值，符合流式末尾 usage 语义喵。
	extractUsageFromSSELine([]byte(`data: {"usage":{"prompt_tokens":90,"completion_tokens":20,"total_tokens":110}}`), usage)
	assert.Equal(t, 90, usage.PromptTokens)

	// [DONE]、空 data、非 data 行、全零 usage 均被跳过喵。
	usage = &dto.Usage{}
	extractUsageFromSSELine([]byte(`data: [DONE]`), usage)
	extractUsageFromSSELine([]byte(`data:`), usage)
	extractUsageFromSSELine([]byte(`event: done`), usage)
	extractUsageFromSSELine([]byte(`data: {"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`), usage)
	assert.Equal(t, 0, usage.PromptTokens)

	// 空目标不崩溃喵。
	extractUsageFromSSELine([]byte(`data: {"usage":{"prompt_tokens":1}}`), nil)
}

// TestExtractUsageFromSSEBytes 验证已缓冲 SSE 文本的批量提取喵。
func TestExtractUsageFromSSEBytes(t *testing.T) {
	// 多行缓冲中最后出现的 usage 生效喵。
	buffered := []byte("data: {}\n\ndata: {\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3,\"total_tokens\":10}}\n")
	usage := &dto.Usage{}
	extractUsageFromSSEBytes(buffered, usage)
	assert.Equal(t, 7, usage.PromptTokens)
	assert.Equal(t, 3, usage.CompletionTokens)

	// 空缓冲不改动目标喵。
	usage = &dto.Usage{}
	extractUsageFromSSEBytes(nil, usage)
	assert.Equal(t, 0, usage.PromptTokens)
}

// TestReadLimitedSSELine 验证单行超长时被截断到安全上限喵。
func TestReadLimitedSSELine(t *testing.T) {
	// 空 reader 直接返回 EOF，不崩溃喵。
	line, err := readLimitedSSELine(nil)
	require.Equal(t, io.EOF, err)
	assert.Len(t, line, 0)
}
