package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildSharedUpstreamPricingRMBToRatio 验证 RMB 元/百万价格换算为模型广场倍率，前端回推应还原 RMB 原价喵。
func TestBuildSharedUpstreamPricingRMBToRatio(t *testing.T) {
	// 固定汇率便于精确断言：1 美元 = 7 RMB 喵。
	oldRate := operation_setting.USDExchangeRate
	operation_setting.USDExchangeRate = 7.0
	defer func() { operation_setting.USDExchangeRate = oldRate }()

	item := buildSharedUpstreamPricing(model.SharedUserUpstreamModelView{
		NormalizedName:  "demo",
		ModelRatio:      "2", // 输入价：2 元/百万 token 喵
		CompletionRatio: "6", // 输出价：6 元/百万 token 喵
		CacheRatio:      "1", // 缓存价：1 元/百万 token 喵
	}, 0)
	// 倍率换算：model_ratio = 输入/(汇率×2)，前端 ×2×汇率 还原 2 元喵。
	assert.InDelta(t, 2.0/(7.0*2), item.ModelRatio, 1e-9)
	// 输出倍率 = 输出/输入，前端还原 6 元喵。
	assert.InDelta(t, 3.0, item.CompletionRatio, 1e-9)
	// 缓存倍率 = 缓存/输入，前端还原 1 元喵。
	require.NotNil(t, item.CacheRatio)
	assert.InDelta(t, 0.5, *item.CacheRatio, 1e-9)
}

// TestBuildSharedUpstreamPricingZeroInput 验证输入价非正时全部倍率归零，避免负价或除零喵。
func TestBuildSharedUpstreamPricingZeroInput(t *testing.T) {
	oldRate := operation_setting.USDExchangeRate
	operation_setting.USDExchangeRate = 7.0
	defer func() { operation_setting.USDExchangeRate = oldRate }()

	item := buildSharedUpstreamPricing(model.SharedUserUpstreamModelView{
		NormalizedName:  "demo",
		ModelRatio:      "0",
		CompletionRatio: "6",
		CacheRatio:      "1",
	}, 0)
	// 输入价为零时输出/缓存倍率不得构造正倍率喵。
	assert.Equal(t, 0.0, item.ModelRatio)
	assert.Equal(t, 0.0, item.CompletionRatio)
	assert.Nil(t, item.CacheRatio)
}

// TestBuildSharedUpstreamPricingRateFallback 验证汇率非正时按 1 兜底，不产生除零喵。
func TestBuildSharedUpstreamPricingRateFallback(t *testing.T) {
	oldRate := operation_setting.USDExchangeRate
	operation_setting.USDExchangeRate = 0
	defer func() { operation_setting.USDExchangeRate = oldRate }()

	item := buildSharedUpstreamPricing(model.SharedUserUpstreamModelView{
		NormalizedName:  "demo",
		ModelRatio:      "2",
		CompletionRatio: "6",
	}, 0)
	// 汇率兜底 1 后 model_ratio = 2/2 = 1 喵。
	assert.InDelta(t, 1.0, item.ModelRatio, 1e-9)
	assert.InDelta(t, 3.0, item.CompletionRatio, 1e-9)
}
