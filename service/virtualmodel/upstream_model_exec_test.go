package virtualmodel

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/relaykit/dto"
	relaykitypes "github.com/QuantumNous/new-api/relaykit/types"
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

	// 后续 usage 只回填缺失侧，不覆盖已有计数（同一流内的 usage 是互补事件）喵。
	extractUsageFromSSELine([]byte(`data: {"usage":{"completion_tokens":20}}`), usage)
	assert.Equal(t, 30, usage.PromptTokens)
	assert.Equal(t, 20, usage.CompletionTokens)

	// [DONE]、空 data、非 data 行、全零 usage 均不改动目标喵。
	usage = &dto.Usage{}
	extractUsageFromSSELine([]byte(`data: [DONE]`), usage)
	extractUsageFromSSELine([]byte(`data:`), usage)
	extractUsageFromSSELine([]byte(`event: done`), usage)
	extractUsageFromSSELine([]byte(`data: {"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`), usage)
	assert.Equal(t, 0, usage.PromptTokens)

	// 空目标不崩溃喵。
	extractUsageFromSSELine([]byte(`data: {"usage":{"prompt_tokens":1}}`), nil)
}

// TestExtractUsageFromSSELineAnthropicMerge 验证 Anthropic 流式 usage 跨事件合并喵。
// message_start 的 input_tokens 嵌在 message.usage，message_delta 的 output_tokens 在顶层 usage，
// 两者互补必须都能拿到；后到的全零 usage 不得清空已计数喵。
func TestExtractUsageFromSSELineAnthropicMerge(t *testing.T) {
	usage := &dto.Usage{}
	// message_start：嵌套 message.usage 提供 input_tokens，顶层 usage 全零不覆盖喵。
	extractUsageFromSSELine([]byte(`data: {"type":"message_start","message":{"usage":{"input_tokens":91,"output_tokens":1}},"usage":{"input_tokens":0,"output_tokens":0}}`), usage)
	assert.Equal(t, 91, usage.InputTokens)
	// 中间块带全零顶层 usage 不得清空已有计数喵。
	extractUsageFromSSELine([]byte(`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"},"usage":{"input_tokens":0,"output_tokens":0}}`), usage)
	assert.Equal(t, 91, usage.InputTokens)
	// message_delta：顶层 usage 提供真实 output_tokens，输入仍保留 message_start 的值喵。
	extractUsageFromSSELine([]byte(`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":91,"output_tokens":35}}`), usage)
	assert.Equal(t, 91, usage.InputTokens)
	assert.Equal(t, 35, usage.OutputTokens)
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

// TestNormalizeUpstreamModelUsage 表驱动验证各厂商 usage 字段统一到标准口径喵。
func TestNormalizeUpstreamModelUsage(t *testing.T) {
	cases := []struct {
		name  string
		input *dto.Usage
		want  *dto.Usage
	}{
		{
			name:  "OpenAI 标准字段原样保留",
			input: &dto.Usage{PromptTokens: 100, CompletionTokens: 50, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 20}},
			want:  &dto.Usage{PromptTokens: 100, CompletionTokens: 50, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 20}},
		},
		{
			// DeepSeek 只报 prompt_cache_hit_tokens，缓存量应回填到标准字段喵。
			name:  "DeepSeek 缓存命中回填",
			input: &dto.Usage{PromptTokens: 100, CompletionTokens: 50, PromptCacheHitTokens: 30},
			want:  &dto.Usage{PromptTokens: 100, CompletionTokens: 50, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 30}, PromptCacheHitTokens: 30},
		},
		{
			// Anthropic 风格报 input_tokens/output_tokens 与 input_tokens_details.cached_tokens 喵。
			name:  "Anthropic input/output 兜底回填",
			input: &dto.Usage{InputTokens: 200, OutputTokens: 80, InputTokensDetails: &dto.InputTokenDetails{CachedTokens: 40}},
			want:  &dto.Usage{PromptTokens: 200, CompletionTokens: 80, InputTokens: 200, OutputTokens: 80, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 40}, InputTokensDetails: &dto.InputTokenDetails{CachedTokens: 40}},
		},
		{
			// 标准字段已存在时不被 prompt_cache_hit_tokens 覆盖喵。
			name:  "标准字段优先",
			input: &dto.Usage{PromptTokens: 100, PromptCacheHitTokens: 30, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 25}},
			want:  &dto.Usage{PromptTokens: 100, PromptCacheHitTokens: 30, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 25}},
		},
		{
			// 异常负值统一钳制归零，防止上游把计费与命中率拉偏喵。
			name:  "负值钳制归零",
			input: &dto.Usage{PromptTokens: -5, CompletionTokens: -3, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: -2}},
			want:  &dto.Usage{PromptTokens: 0, CompletionTokens: 0, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 0}},
		},
		{
			// 推理 token 单独上报时并入输出，保证输出 token 包含思考量喵。
			name:  "推理 token 并入输出",
			input: &dto.Usage{PromptTokens: 100, CompletionTokens: 0, CompletionTokenDetails: dto.OutputTokenDetails{ReasoningTokens: 203}},
			want:  &dto.Usage{PromptTokens: 100, CompletionTokens: 203, CompletionTokenDetails: dto.OutputTokenDetails{ReasoningTokens: 203}},
		},
		{
			// completion 非零时不再叠加推理，避免把已含推理的输出重复计数喵。
			name:  "completion 已含推理不叠加",
			input: &dto.Usage{PromptTokens: 100, CompletionTokens: 300, CompletionTokenDetails: dto.OutputTokenDetails{ReasoningTokens: 203}},
			want:  &dto.Usage{PromptTokens: 100, CompletionTokens: 300, CompletionTokenDetails: dto.OutputTokenDetails{ReasoningTokens: 203}},
		},
		{
			// 空 usage 返回 nil，避免空指针喵。
			name:  "空 usage 返回 nil",
			input: nil,
			want:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeUpstreamModelUsage(tc.input)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestUsageHasTokens 验证 token 计数存在性判断覆盖 input/output 风格字段喵。
func TestUsageHasTokens(t *testing.T) {
	// 空 usage 视为无 token 喵。
	require.False(t, usageHasTokens(nil))
	// 标准字段非零视为有 token 喵。
	require.True(t, usageHasTokens(&dto.Usage{PromptTokens: 1}))
	require.True(t, usageHasTokens(&dto.Usage{CompletionTokens: 1}))
	require.True(t, usageHasTokens(&dto.Usage{TotalTokens: 1}))
	// Anthropic 风格字段非零也视为有 token，避免 usage 被误判为空喵。
	require.True(t, usageHasTokens(&dto.Usage{InputTokens: 1}))
	require.True(t, usageHasTokens(&dto.Usage{OutputTokens: 1}))
	// 推理 token 单独上报也算有计费信息，避免整条 usage 被当成无 token 重新估算喵。
	require.True(t, usageHasTokens(&dto.Usage{CompletionTokenDetails: dto.OutputTokenDetails{ReasoningTokens: 1}}))
	// 全零视为无 token 喵。
	require.False(t, usageHasTokens(&dto.Usage{}))
}

// TestExtractUsageFromSSELineDeepSeekCache 验证 DeepSeek 流式缓存字段可被解析并经归一化回填喵。
func TestExtractUsageFromSSELineDeepSeekCache(t *testing.T) {
	// DeepSeek 流式末尾事件报 prompt_cache_hit_tokens 而非标准 cached_tokens 喵。
	usage := &dto.Usage{}
	extractUsageFromSSELine([]byte(`data: {"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,"prompt_cache_hit_tokens":30}}`), usage)
	require.Equal(t, 100, usage.PromptTokens)
	require.Equal(t, 30, usage.PromptCacheHitTokens)
	// 归一化后缓存命中回填到标准字段，供计费按缓存价收取喵。
	normalized := normalizeUpstreamModelUsage(usage)
	require.Equal(t, 30, normalized.PromptTokensDetails.CachedTokens)
}

// TestFillEstimatedUsageIfMissing 验证缺失侧 token 用文本估算补全，保留真实侧喵。
func TestFillEstimatedUsageIfMissing(t *testing.T) {
	requestBody := []byte(`{"messages":[{"role":"user","content":"你好，介绍一下自己"}]}`)
	responseText := "我是 DeepSeek，很高兴认识你！"

	// 只有 prompt、无 completion：completion 用响应文本估算补上，并标记为估计来源喵。
	usage := &dto.Usage{PromptTokens: 100, CompletionTokens: 0, TotalTokens: 100}
	got := fillEstimatedUsageIfMissing(nil, "deepseek-v4-flash", usage, requestBody, responseText, false)
	require.NotNil(t, got)
	require.Equal(t, 100, got.PromptTokens, "真实 prompt 必须保留")
	require.Greater(t, got.CompletionTokens, 0, "输出 token 不能被估成 0")
	require.Equal(t, got.PromptTokens+got.CompletionTokens, got.TotalTokens)
	require.True(t, got.BillingUsage != nil && got.BillingUsage.Estimated, "补全了估算侧必须标记估计来源")

	// 两侧都有真实 token：原样返回，不标记估计喵。
	usage = &dto.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150}
	got = fillEstimatedUsageIfMissing(nil, "deepseek-v4-flash", usage, requestBody, responseText, false)
	require.NotNil(t, got)
	require.Equal(t, 100, got.PromptTokens)
	require.Equal(t, 50, got.CompletionTokens)
	require.True(t, got.BillingUsage == nil, "真实 usage 不应被标记估计")

	// 只有 completion、无 prompt：prompt 用请求体估算补上喵。
	usage = &dto.Usage{PromptTokens: 0, CompletionTokens: 30, TotalTokens: 30}
	got = fillEstimatedUsageIfMissing(nil, "deepseek-v4-flash", usage, requestBody, "", false)
	require.NotNil(t, got)
	require.Greater(t, got.PromptTokens, 0, "输入 token 不能被估成 0")
	require.Equal(t, 30, got.CompletionTokens)

	// 响应文本为空且 completion 缺失：无法估算，原样返回不崩溃喵。
	usage = &dto.Usage{PromptTokens: 100, CompletionTokens: 0, TotalTokens: 100}
	got = fillEstimatedUsageIfMissing(nil, "deepseek-v4-flash", usage, requestBody, "", false)
	require.NotNil(t, got)
	require.Equal(t, 0, got.CompletionTokens)

	// 空 usage 返回 nil，保持调用方整体估算语义喵。
	require.Nil(t, fillEstimatedUsageIfMissing(nil, "deepseek-v4-flash", nil, requestBody, responseText, false))
}

// TestBufferCustomStreamToDone 验证流转伪流全量缓存到 [DONE] 与断流分类喵。
func TestBufferCustomStreamToDone(t *testing.T) {
	// 完整流到 [DONE]：返回全部行（含 [DONE] 行），供一次性回放喵。
	completeStream := "data: {\"a\":1}\n\ndata: {\"b\":2}\n\ndata: [DONE]\n"
	buffered, err := bufferCustomStreamToDone(bufio.NewReader(bytes.NewBufferString(completeStream)), 60*time.Second, 60*time.Second)
	require.NoError(t, err)
	assert.Contains(t, string(buffered), `data: {"a":1}`)
	assert.Contains(t, string(buffered), "[DONE]")

	// EOF 未见 [DONE]：判定断流，返回带 ErrStreamCut 的错误喵。
	cutStream := "data: {\"a\":1}\n"
	_, err = bufferCustomStreamToDone(bufio.NewReader(bytes.NewBufferString(cutStream)), 60*time.Second, 60*time.Second)
	require.Error(t, err)
	assert.True(t, errors.Is(err, relaykitypes.ErrStreamCut), "EOF before [DONE] should classify as stream cut")

	// 空 reader：直接判定断流，不崩溃喵。
	_, err = bufferCustomStreamToDone(nil, 60*time.Second, 60*time.Second)
	require.Error(t, err)
	assert.True(t, errors.Is(err, relaykitypes.ErrStreamCut))
}

// TestBufferCustomStreamToDoneTotalBudgetHardCap 验证伪流阶段 stall > total 时总预算仍是硬上限喵。
func TestBufferCustomStreamToDoneTotalBudgetHardCap(t *testing.T) {
	// 完全静默的上游：stall 设为 60s 远大于 total 1s，读行必须在 total 内被硬性终止并归入断流喵。
	release := make(chan struct{})
	defer close(release)
	reader := bufio.NewReader(blockingProbeReader{release: release})
	_, err := bufferCustomStreamToDone(reader, 60*time.Second, time.Second)
	require.Error(t, err)
	assert.True(t, errors.Is(err, relaykitypes.ErrStreamCut))
}
