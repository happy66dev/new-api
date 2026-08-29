package dto

import (
	"testing"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInputTokenDetailsCacheCreationTokensTotal 验证缓存写入总数为各风格字段的最大值喵。
func TestInputTokenDetailsCacheCreationTokensTotal(t *testing.T) {
	// 三种缓存写入拼写（标准/OpenAI 原生/中转站）取最大值且不双计，避免同一批 token 重复计费喵。
	assert.Equal(t, 0, (InputTokenDetails{}).CacheCreationTokensTotal())
	assert.Equal(t, 5, (InputTokenDetails{CachedCreationTokens: 5}).CacheCreationTokensTotal())
	assert.Equal(t, 7, (InputTokenDetails{CachedCreationTokens: 5, CacheWriteTokens: 7}).CacheCreationTokensTotal())
	assert.Equal(t, 9, (InputTokenDetails{CachedCreationTokens: 5, CacheCreationInputTokens: 9}).CacheCreationTokensTotal())
	assert.Equal(t, 9, (InputTokenDetails{CachedCreationTokens: 5, CacheWriteTokens: 7, CacheCreationInputTokens: 9}).CacheCreationTokensTotal())
	// 负值钳制为零，绝不产生负费用喵。
	assert.Equal(t, 0, (InputTokenDetails{CachedCreationTokens: -3}).CacheCreationTokensTotal())
}

// TestInputTokenDetailsCachedTokensTotal 验证缓存命中总数为标准与中转站风格之和喵。
func TestInputTokenDetailsCachedTokensTotal(t *testing.T) {
	// 两种缓存命中拼写（cached_tokens / cache_read_input_tokens）累加，避免漏计任一上游的缓存折扣喵。
	assert.Equal(t, 0, (InputTokenDetails{}).CachedTokensTotal())
	assert.Equal(t, 5, (InputTokenDetails{CachedTokens: 5}).CachedTokensTotal())
	assert.Equal(t, 6, (InputTokenDetails{CacheReadInputTokens: 6}).CachedTokensTotal())
	assert.Equal(t, 11, (InputTokenDetails{CachedTokens: 5, CacheReadInputTokens: 6}).CachedTokensTotal())
	// 负值钳制为零喵。
	assert.Equal(t, 0, (InputTokenDetails{CachedTokens: -2, CacheReadInputTokens: 1}).CachedTokensTotal())
}

// TestHasOpenAIUsageTokensRecognizesNonStandardFields 验证非标准 usage 字段非零即视为有 token 喵。
func TestHasOpenAIUsageTokensRecognizesNonStandardFields(t *testing.T) {
	require.False(t, HasOpenAIUsageTokens(&Usage{}))
	require.True(t, HasOpenAIUsageTokens(&Usage{PromptTokensDetails: InputTokenDetails{CacheCreationInputTokens: 1}}))
	require.True(t, HasOpenAIUsageTokens(&Usage{PromptTokensDetails: InputTokenDetails{CacheReadInputTokens: 1}}))
	require.True(t, HasOpenAIUsageTokens(&Usage{PromptTokensDetails: InputTokenDetails{VideoTokens: 1}}))
	require.True(t, HasOpenAIUsageTokens(&Usage{CompletionTokenDetails: OutputTokenDetails{VideoTokens: 1}}))
	require.True(t, HasOpenAIUsageTokens(&Usage{CompletionTokenDetails: OutputTokenDetails{AcceptedPredictionTokens: 1}}))
	require.True(t, HasOpenAIUsageTokens(&Usage{CompletionTokenDetails: OutputTokenDetails{RejectedPredictionTokens: 1}}))
}

// TestUnmarshalUsageNonStandardFields 验证中转站非标准 usage JSON 能被完整解析进 Usage 结构喵。
func TestUnmarshalUsageNonStandardFields(t *testing.T) {
	raw := []byte(`{
		"prompt_tokens": 84,
		"completion_tokens": 10,
		"total_tokens": 94,
		"prompt_tokens_details": {
			"audio_tokens": 0,
			"cached_tokens": 3,
			"cache_write_tokens": 2,
			"cache_creation_input_tokens": 4,
			"cache_read_input_tokens": 5,
			"text_tokens": 0,
			"image_tokens": 0,
			"video_tokens": 6
		},
		"completion_tokens_details": {
			"audio_tokens": 0,
			"reasoning_tokens": 10,
			"accepted_prediction_tokens": 7,
			"rejected_prediction_tokens": 1,
			"text_tokens": 0,
			"image_tokens": 0,
			"video_tokens": 8
		}
	}`)
	var usage Usage
	require.NoError(t, kitutil.Unmarshal(raw, &usage))
	// 输入侧新字段必须全部落库，供缓存/视频计费识别喵。
	require.Equal(t, 3, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 5, usage.PromptTokensDetails.CacheReadInputTokens)
	require.Equal(t, 4, usage.PromptTokensDetails.CacheCreationInputTokens)
	require.Equal(t, 6, usage.PromptTokensDetails.VideoTokens)
	// 输出侧新字段必须全部落库，供日志展示喵。
	require.Equal(t, 8, usage.CompletionTokenDetails.VideoTokens)
	require.Equal(t, 7, usage.CompletionTokenDetails.AcceptedPredictionTokens)
	require.Equal(t, 1, usage.CompletionTokenDetails.RejectedPredictionTokens)
	// 归一化：缓存命中与缓存写入各取对应口径总和喵。
	require.Equal(t, 8, usage.PromptTokensDetails.CachedTokensTotal())
	require.Equal(t, 4, usage.PromptTokensDetails.CacheCreationTokensTotal())
}
