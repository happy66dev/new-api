package service

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// snapshotAudioPricingConfig 备份并还原本文件改动的定价配置喵。
// ratio_setting 的配置是包级全局变量，不隔离的话会污染同包内其它测试喵。
func snapshotAudioPricingConfig(t *testing.T) {
	t.Helper()
	previousGroupPricing := ratio_setting.GroupModelPricing2JSONString()
	previousCompletionRatio := ratio_setting.CompletionRatio2JSONString()
	previousAudioRatio := ratio_setting.AudioRatio2JSONString()
	previousAudioCompletionRatio := ratio_setting.AudioCompletionRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupModelPricingByJSONString(previousGroupPricing))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(previousCompletionRatio))
		require.NoError(t, ratio_setting.UpdateAudioRatioByJSONString(previousAudioRatio))
		require.NoError(t, ratio_setting.UpdateAudioCompletionRatioByJSONString(previousAudioCompletionRatio))
	})
}

// TestCalculateAudioQuotaUsesGroupOverrides 验证音频扣费按「最终分组」解析各项倍率喵。
// 改造前这里只按模型名读全局倍率，会让「A 组音频加价高、B 组便宜」这种配置全部按同一个价扣钱喵。
func TestCalculateAudioQuotaUsesGroupOverrides(t *testing.T) {
	snapshotAudioPricingConfig(t)

	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"gpt-audio":2}`))
	require.NoError(t, ratio_setting.UpdateAudioRatioByJSONString(`{"gpt-audio":10}`))
	require.NoError(t, ratio_setting.UpdateAudioCompletionRatioByJSONString(`{"gpt-audio":1}`))
	// cheap-audio 分组把音频输入倍率压到 4、输出倍率提到 2；其余字段留空表示继承全局喵。
	require.NoError(t, ratio_setting.UpdateGroupModelPricingByJSONString(`{
		"cheap-audio":{"gpt-audio":{"audio_ratio":4,"audio_completion_ratio":2}}
	}`))

	// 构造一次「文本 100 进 / 100 出、音频 100 进 / 100 出」的请求喵。
	newQuotaInfo := func(group string) QuotaInfo {
		return QuotaInfo{
			InputDetails:  TokenDetails{TextTokens: 100, AudioTokens: 100},
			OutputDetails: TokenDetails{TextTokens: 100, AudioTokens: 100},
			ModelName:     "gpt-audio",
			Group:         group,
			UsePrice:      false,
			ModelRatio:    1,
			GroupRatio:    1,
		}
	}

	// 全局口径：100 + 100×2 + 100×10 + 100×10×1 = 2300 喵。
	globalQuota, clamp := calculateAudioQuota(newQuotaInfo("default"))
	require.Nil(t, clamp)
	assert.Equal(t, 2300, globalQuota)

	// 定制分组：100 + 100×2 + 100×4 + 100×4×2 = 1500 喵。
	scopedQuota, clamp := calculateAudioQuota(newQuotaInfo("cheap-audio"))
	require.Nil(t, clamp)
	assert.Equal(t, 1500, scopedQuota)
}

// TestCalculateAudioQuotaGroupOverridesCompletionRatio 验证补全倍率也能按分组覆盖喵。
func TestCalculateAudioQuotaGroupOverridesCompletionRatio(t *testing.T) {
	snapshotAudioPricingConfig(t)

	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"gpt-audio":4}`))
	require.NoError(t, ratio_setting.UpdateAudioRatioByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateAudioCompletionRatioByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateGroupModelPricingByJSONString(`{
		"flat":{"gpt-audio":{"completion_ratio":1}}
	}`))

	newQuotaInfo := func(group string) QuotaInfo {
		return QuotaInfo{
			InputDetails:  TokenDetails{TextTokens: 100},
			OutputDetails: TokenDetails{TextTokens: 100},
			ModelName:     "gpt-audio",
			Group:         group,
			ModelRatio:    1,
			GroupRatio:    1,
		}
	}

	// 全局：100 + 100×4 = 500 喵。
	globalQuota, clamp := calculateAudioQuota(newQuotaInfo("default"))
	require.Nil(t, clamp)
	assert.Equal(t, 500, globalQuota)

	// flat 分组把补全倍率定制成 1：100 + 100×1 = 200 喵。
	scopedQuota, clamp := calculateAudioQuota(newQuotaInfo("flat"))
	require.Nil(t, clamp)
	assert.Equal(t, 200, scopedQuota)
}

// TestCalculateAudioQuotaPerCallIgnoresRatios 验证按次计费分支完全不看倍率，
// 也就不会被分组定制的音频倍率影响喵。
func TestCalculateAudioQuotaPerCallIgnoresRatios(t *testing.T) {
	snapshotAudioPricingConfig(t)

	require.NoError(t, ratio_setting.UpdateAudioRatioByJSONString(`{"gpt-audio":10}`))
	require.NoError(t, ratio_setting.UpdateGroupModelPricingByJSONString(`{
		"cheap-audio":{"gpt-audio":{"audio_ratio":1}}
	}`))

	info := QuotaInfo{
		InputDetails:  TokenDetails{AudioTokens: 100},
		OutputDetails: TokenDetails{AudioTokens: 100},
		ModelName:     "gpt-audio",
		Group:         "cheap-audio",
		UsePrice:      true,
		ModelPrice:    0.02,
		GroupRatio:    2,
	}

	// 按次：0.02 × QuotaPerUnit × 分组倍率 2，与任何倍率无关喵。
	quota, clamp := calculateAudioQuota(info)
	require.Nil(t, clamp)
	assert.Equal(t, int(0.02*2*500000), quota)
}
