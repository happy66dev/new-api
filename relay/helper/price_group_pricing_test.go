package helper

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// snapshotGroupPricingConfig 备份并还原本文件会改动的全部定价配置喵。
// 这些配置都是包级全局变量，测试之间必须隔离，否则后面的用例会读到前面留下的脏价格喵。
func snapshotGroupPricingConfig(t *testing.T) {
	t.Helper()
	previousGroupPricing := ratio_setting.GroupModelPricing2JSONString()
	previousModelPrice := ratio_setting.ModelPrice2JSONString()
	previousModelRatio := ratio_setting.ModelRatio2JSONString()
	previousCompletionRatio := ratio_setting.CompletionRatio2JSONString()
	previousCacheRatio := ratio_setting.CacheRatio2JSONString()
	previousGroupRatio := ratio_setting.GroupRatio2JSONString()
	// billing_setting 走 config 注册表，用 SaveToDB/LoadFromDB 这一对做快照与还原喵。
	savedConfig := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		savedConfig[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupModelPricingByJSONString(previousGroupPricing))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousModelPrice))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousModelRatio))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(previousCompletionRatio))
		require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(previousCacheRatio))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroupRatio))
		require.NoError(t, config.GlobalConfig.LoadFromDB(savedConfig))
	})
}

// newGroupPricingContext 造一个最小可用的请求上下文与 RelayInfo 喵。
// autoGroup 非空时模拟 auto 分组重试后的「最终分组」，用来验证价格是按最终分组解析的喵。
func newGroupPricingContext(
	model string,
	group string,
	autoGroup string,
) (*gin.Context, *relaycommon.RelayInfo) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("group", group)
	// auto 分组重试时上下文里会带上最终分组，HandleGroupRatio 会据此改写 UsingGroup 喵。
	if autoGroup != "" {
		ctx.Set("auto_group", autoGroup)
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: model,
		UserGroup:       group,
		UsingGroup:      group,
	}
	return ctx, info
}

// TestModelPriceHelperPerGroupBillingModes 覆盖主人的核心场景喵：
// 同一个模型 A 组按次、B 组按量、C/D/E 三组按量但价格各不相同喵。
func TestModelPriceHelperPerGroupBillingModes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	snapshotGroupPricingConfig(t)

	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"deepseek-chat":1}`))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"deepseek-chat":1}`))
	// 五个分组倍率都设成 1，先把「分组定制价本身」验干净，倍率叠加另有专门用例喵。
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(
		`{"group-a":1,"group-b":1,"group-c":1,"group-d":1,"group-e":1,"group-none":1}`))
	require.NoError(t, ratio_setting.UpdateGroupModelPricingByJSONString(`{
		"group-a":{"deepseek-chat":{"billing_mode":"per_call","model_price":0.02}},
		"group-b":{"deepseek-chat":{"billing_mode":"per_token","model_ratio":0.5}},
		"group-c":{"deepseek-chat":{"model_ratio":0.27}},
		"group-d":{"deepseek-chat":{"model_ratio":0.35}},
		"group-e":{"deepseek-chat":{"model_ratio":0.4}}
	}`))

	// A 组按次：预扣额度 = 按次价 × QuotaPerUnit × 分组倍率喵。
	ctx, info := newGroupPricingContext("deepseek-chat", "group-a", "")
	priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	assert.True(t, priceData.UsePrice)
	assert.Equal(t, 0.02, priceData.ModelPrice)
	assert.Equal(t, int(0.02*common.QuotaPerUnit), priceData.QuotaToPreConsume)
	assert.Equal(t, "group-a", priceData.PricingGroupOverride)

	// B/C/D/E 组按量：预扣额度 = prompt token 数 × 模型倍率 × 分组倍率喵。
	for _, testCase := range []struct {
		group      string
		modelRatio float64
	}{
		{group: "group-b", modelRatio: 0.5},
		{group: "group-c", modelRatio: 0.27},
		{group: "group-d", modelRatio: 0.35},
		{group: "group-e", modelRatio: 0.4},
	} {
		ctx, info := newGroupPricingContext("deepseek-chat", testCase.group, "")
		priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
		require.NoError(t, err, testCase.group)
		assert.False(t, priceData.UsePrice, testCase.group)
		assert.Equal(t, testCase.modelRatio, priceData.ModelRatio, testCase.group)
		assert.Equal(t, int(1000*testCase.modelRatio), priceData.QuotaToPreConsume, testCase.group)
		assert.Equal(t, testCase.group, priceData.PricingGroupOverride, testCase.group)
	}

	// 没配定制的分组继续吃全局倍率，且不应被标记成分组定制来源喵。
	ctx, info = newGroupPricingContext("deepseek-chat", "group-none", "")
	priceData, err = ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	assert.Equal(t, 1.0, priceData.ModelRatio)
	assert.Empty(t, priceData.PricingGroupOverride)
}

// TestModelPriceHelperGroupOverrideMultipliesGroupRatio 验证「分组定制价 × 分组倍率 = 最终价」喵。
func TestModelPriceHelperGroupOverrideMultipliesGroupRatio(t *testing.T) {
	gin.SetMode(gin.TestMode)
	snapshotGroupPricingConfig(t)

	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"deepseek-chat":1}`))
	// vip 分组倍率 0.5（打五折），custom 分组倍率 2（加倍）喵。
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"vip":0.5,"custom":2}`))
	require.NoError(t, ratio_setting.UpdateGroupModelPricingByJSONString(`{
		"vip":{"deepseek-chat":{"model_ratio":0.4}},
		"custom":{"deepseek-chat":{"billing_mode":"per_call","model_price":0.01}}
	}`))

	// 按量：1000 token × 定制倍率 0.4 × 分组倍率 0.5 = 200 喵。
	ctx, info := newGroupPricingContext("deepseek-chat", "vip", "")
	priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	assert.Equal(t, 0.5, priceData.GroupRatioInfo.GroupRatio)
	assert.Equal(t, 200, priceData.QuotaToPreConsume)

	// 按次：定制单价 0.01 × QuotaPerUnit × 分组倍率 2 喵。
	ctx, info = newGroupPricingContext("deepseek-chat", "custom", "")
	priceData, err = ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	assert.True(t, priceData.UsePrice)
	assert.Equal(t, int(0.01*common.QuotaPerUnit*2), priceData.QuotaToPreConsume)
}

// TestModelPriceHelperGroupOverrideCacheRatio 验证「这个上游没有缓存优惠」能用 cache_ratio=1 表达喵。
func TestModelPriceHelperGroupOverrideCacheRatio(t *testing.T) {
	gin.SetMode(gin.TestMode)
	snapshotGroupPricingConfig(t)

	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"deepseek-chat":1}`))
	require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(`{"deepseek-chat":0.1}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"no-cache":1,"cheap-cache":1}`))
	require.NoError(t, ratio_setting.UpdateGroupModelPricingByJSONString(`{
		"no-cache":{"deepseek-chat":{"cache_ratio":1}},
		"cheap-cache":{"deepseek-chat":{"cache_ratio":0.02}}
	}`))

	// 定制成 1 表示缓存 token 与普通输入同价，也就是「这家上游没有缓存折扣」喵。
	ctx, info := newGroupPricingContext("deepseek-chat", "no-cache", "")
	priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	assert.Equal(t, 1.0, priceData.CacheRatio)

	// 另一个分组能同时配一个更便宜的缓存价，互不影响喵。
	ctx, info = newGroupPricingContext("deepseek-chat", "cheap-cache", "")
	priceData, err = ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	assert.Equal(t, 0.02, priceData.CacheRatio)
}

// TestModelPriceHelperUsesFinalAutoGroup 验证 auto 分组重试后按「最终分组」定价喵。
// 这条最重要：如果价格在分组确定之前就读掉了，用户就会被按错误分组的价扣钱喵。
func TestModelPriceHelperUsesFinalAutoGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	snapshotGroupPricingConfig(t)

	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"deepseek-chat":1}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"switched":1}`))
	require.NoError(t, ratio_setting.UpdateGroupModelPricingByJSONString(`{
		"default":{"deepseek-chat":{"model_ratio":1}},
		"switched":{"deepseek-chat":{"model_ratio":0.2}}
	}`))

	// 请求以 default 分组进来，但 auto 分组把最终分组改成了 switched 喵。
	ctx, info := newGroupPricingContext("deepseek-chat", "default", "switched")
	priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	assert.Equal(t, "switched", info.UsingGroup)
	assert.Equal(t, 0.2, priceData.ModelRatio)
	assert.Equal(t, 200, priceData.QuotaToPreConsume)
	assert.Equal(t, "switched", priceData.PricingGroupOverride)
}

// TestModelPriceHelperGroupTieredExpression 验证分组级阶梯表达式优先于全局表达式喵。
func TestModelPriceHelperGroupTieredExpression(t *testing.T) {
	gin.SetMode(gin.TestMode)
	snapshotGroupPricingConfig(t)

	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"expr-model":1}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"expr-group":1,"plain":1}`))
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.group_billing_mode": `{"expr-group":{"expr-model":"tiered_expr"}}`,
		"billing_setting.group_billing_expr": `{"expr-group":{"expr-model":"p * 2"}}`,
	}))

	// 该分组走表达式：1000 prompt token × 2 美元/百万 token 的系数口径喵。
	ctx, info := newGroupPricingContext("expr-model", "expr-group", "")
	priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.NotNil(t, info.TieredBillingSnapshot)
	assert.Positive(t, priceData.QuotaToPreConsume)
	// 表达式来自分组定制，必须标出来源分组供日志审计喵。
	assert.Equal(t, "expr-group", priceData.PricingGroupOverride)

	// 没配分组表达式的分组继续走普通倍率计费，不受影响喵。
	ctx, info = newGroupPricingContext("expr-model", "plain", "")
	priceData, err = ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	assert.Nil(t, info.TieredBillingSnapshot)
	assert.Equal(t, 1.0, priceData.ModelRatio)
	assert.Empty(t, priceData.PricingGroupOverride)
}

// TestModelPriceHelperGroupOverrideMakesUnpricedModelUsable 验证全局未定价的模型
// 在配了定制价的分组里可以正常计费，而在其它分组仍然按未定价拒绝喵。
func TestModelPriceHelperGroupOverrideMakesUnpricedModelUsable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	snapshotGroupPricingConfig(t)

	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"pioneer":1,"other":1}`))
	require.NoError(t, ratio_setting.UpdateGroupModelPricingByJSONString(`{
		"pioneer":{"brand-new-model":{"model_ratio":0.8}}
	}`))

	ctx, info := newGroupPricingContext("brand-new-model", "pioneer", "")
	priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	assert.Equal(t, 0.8, priceData.ModelRatio)
	assert.Equal(t, 800, priceData.QuotaToPreConsume)

	// 其它分组既没有定制价也没有全局价，且用户没开「接受未定价模型」，必须报错喵。
	ctx, info = newGroupPricingContext("brand-new-model", "other", "")
	_, err = ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.Error(t, err)
}

// TestModelPriceHelperPerCallGroupOverride 验证任务 / MJ 走的按次通道也支持分组定制价喵。
func TestModelPriceHelperPerCallGroupOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	snapshotGroupPricingConfig(t)

	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"suno-v3":0.1}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"suno-v3":3}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"cheap":1,"token-group":1}`))
	require.NoError(t, ratio_setting.UpdateGroupModelPricingByJSONString(`{
		"cheap":{"suno-v3":{"model_price":0.03}},
		"token-group":{"suno-v3":{"billing_mode":"per_token"}}
	}`))

	// 不定制的分组继续用全局按次价喵。
	ctx, info := newGroupPricingContext("suno-v3", "default", "")
	priceData, err := ModelPriceHelperPerCall(ctx, info)
	require.NoError(t, err)
	assert.True(t, priceData.UsePrice)
	assert.Equal(t, int(0.1*common.QuotaPerUnit), priceData.Quota)
	assert.Empty(t, priceData.PricingGroupOverride)

	// 定制了更低按次价的分组按定制价扣喵。
	ctx, info = newGroupPricingContext("suno-v3", "cheap", "")
	priceData, err = ModelPriceHelperPerCall(ctx, info)
	require.NoError(t, err)
	assert.True(t, priceData.UsePrice)
	assert.Equal(t, int(0.03*common.QuotaPerUnit), priceData.Quota)
	assert.Equal(t, "cheap", priceData.PricingGroupOverride)

	// 定制成按量的分组必须掰回按量：预扣额度取倍率的一半喵。
	ctx, info = newGroupPricingContext("suno-v3", "token-group", "")
	priceData, err = ModelPriceHelperPerCall(ctx, info)
	require.NoError(t, err)
	assert.False(t, priceData.UsePrice)
	assert.Equal(t, int(3.0/2*common.QuotaPerUnit), priceData.Quota)
}

// TestHasModelBillingConfigForGroup 验证模型可见性判断是分组感知的喵。
// 只在某个分组配了定制价的模型，不能因为全局没配就从那个分组的模型列表里消失喵。
func TestHasModelBillingConfigForGroup(t *testing.T) {
	snapshotGroupPricingConfig(t)

	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateGroupModelPricingByJSONString(`{
		"pioneer":{"brand-new-model":{"model_ratio":0.8}},
		"per-call-group":{"call-only-model":{"billing_mode":"per_call","model_price":0.05}}
	}`))
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":       `{}`,
		"billing_setting.billing_expr":       `{}`,
		"billing_setting.group_billing_mode": `{"expr-group":{"expr-only-model":"tiered_expr"}}`,
		"billing_setting.group_billing_expr": `{"expr-group":{"expr-only-model":"p * 2"}}`,
	}))

	// 全局口径下这三个模型都算未定价喵。
	assert.False(t, HasModelBillingConfig("brand-new-model"))
	assert.False(t, HasModelBillingConfig("call-only-model"))
	assert.False(t, HasModelBillingConfig("expr-only-model"))

	// 配了定制的分组里它们都是已定价的：倍率、按次价、分组表达式三条路径各覆盖一次喵。
	assert.True(t, HasModelBillingConfigForGroup("pioneer", "brand-new-model"))
	assert.True(t, HasModelBillingConfigForGroup("per-call-group", "call-only-model"))
	assert.True(t, HasModelBillingConfigForGroup("expr-group", "expr-only-model"))

	// 没配定制的分组仍然算未定价，避免把定制价泄漏给别的分组喵。
	assert.False(t, HasModelBillingConfigForGroup("other", "brand-new-model"))
	assert.False(t, HasModelBillingConfigForGroup("other", "expr-only-model"))
}
