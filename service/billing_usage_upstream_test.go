package service

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCanonicalizeUpstreamUsageAnthropic 验证 anthropic 语义的扁平 usage 规范成与内部转换完全同源喵。
// 期望：PromptTokens=原始 input_tokens（不含缓存）、InputTokens=输入+缓存读+缓存写、缓存读/写单独拆分喵。
func TestCanonicalizeUpstreamUsageAnthropic(t *testing.T) {
	// 构造透传解析得到的扁平 usage：input_tokens=100、output_tokens=20、缓存读 30、缓存写 5 喵。
	flat := &dto.Usage{
		InputTokens:  100,
		OutputTokens: 20,
	}
	flat.PromptTokensDetails.CachedTokens = 30
	flat.PromptTokensDetails.CachedCreationTokens = 5
	canonical := CanonicalizeUpstreamUsageBySemantic(flat, dto.BillingUsageSemanticAnthropic)
	require.NotNil(t, canonical)
	// 与 relaykit 的统一规范化（上游 CanonicalUsage）直接转换结果逐字段一致喵。
	internal, ok := dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{
		InputTokens:              100,
		OutputTokens:             20,
		CacheReadInputTokens:     30,
		CacheCreationInputTokens: 5,
	}).CanonicalUsage()
	require.True(t, ok, "Claude 规范化应当识别出有效的 usage")
	require.NotNil(t, internal)
	assert.Equal(t, internal.PromptTokens, canonical.PromptTokens, "prompt 列必须等于原始 input_tokens（不含缓存读取）")
	assert.Equal(t, internal.CompletionTokens, canonical.CompletionTokens)
	assert.Equal(t, internal.InputTokens, canonical.InputTokens, "input 字段必须含缓存读取+缓存写入")
	assert.Equal(t, internal.OutputTokens, canonical.OutputTokens)
	assert.Equal(t, internal.TotalTokens, canonical.TotalTokens)
	assert.Equal(t, internal.PromptTokensDetails.CachedTokens, canonical.PromptTokensDetails.CachedTokens)
	assert.Equal(t, internal.PromptTokensDetails.CachedCreationTokens, canonical.PromptTokensDetails.CachedCreationTokens)
	assert.Equal(t, 100, canonical.PromptTokens)
	assert.Equal(t, 135, canonical.InputTokens, "input 字段 = 100 输入 + 30 缓存读 + 5 缓存写")
}

// TestCanonicalizeUpstreamUsageOpenAI 验证 openai 语义的扁平 usage 规范与内部 OpenAI 转换同源喵。
func TestCanonicalizeUpstreamUsageOpenAI(t *testing.T) {
	flat := &dto.Usage{
		PromptTokens:     50,
		CompletionTokens: 10,
	}
	flat.PromptTokensDetails.CachedTokens = 5
	canonical := CanonicalizeUpstreamUsageBySemantic(flat, dto.BillingUsageSemanticOpenAI)
	require.NotNil(t, canonical)
	// relaykit 的统一规范化（上游 CanonicalUsage）作为对照口径喵。
	internal, ok := dto.NewOpenAIChatBillingUsage(flat).CanonicalUsage()
	require.True(t, ok, "OpenAI 规范化应当识别出有效的 usage")
	require.NotNil(t, internal)
	assert.Equal(t, internal.PromptTokens, canonical.PromptTokens)
	assert.Equal(t, internal.CompletionTokens, canonical.CompletionTokens)
	assert.Equal(t, internal.InputTokens, canonical.InputTokens)
	assert.Equal(t, internal.OutputTokens, canonical.OutputTokens)
	assert.Equal(t, internal.TotalTokens, canonical.TotalTokens)
	assert.Equal(t, internal.PromptTokensDetails.CachedTokens, canonical.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 50, canonical.PromptTokens)
	assert.Equal(t, 10, canonical.CompletionTokens)
}

// TestCanonicalizeUpstreamUsagePreservesEstimated 验证估算标记在规范转换后被保留喵。
func TestCanonicalizeUpstreamUsagePreservesEstimated(t *testing.T) {
	flat := &dto.Usage{
		InputTokens:  100,
		OutputTokens: 20,
		BillingUsage: &dto.BillingUsage{Estimated: true},
	}
	canonical := CanonicalizeUpstreamUsageBySemantic(flat, dto.BillingUsageSemanticAnthropic)
	require.NotNil(t, canonical)
	require.NotNil(t, canonical.BillingUsage)
	assert.True(t, canonical.BillingUsage.Estimated, "估算标记必须随规范转换保留喵")
}

// TestCanonicalizeUpstreamUsageEmpty 验证空/全零 usage 不被规范转换破坏喵。
func TestCanonicalizeUpstreamUsageEmpty(t *testing.T) {
	assert.Nil(t, CanonicalizeUpstreamUsageBySemantic(nil, dto.BillingUsageSemanticOpenAI))
	empty := CanonicalizeUpstreamUsageBySemantic(&dto.Usage{}, dto.BillingUsageSemanticOpenAI)
	require.NotNil(t, empty)
	assert.Equal(t, 0, empty.PromptTokens)
	assert.Equal(t, 0, empty.CompletionTokens)
}
