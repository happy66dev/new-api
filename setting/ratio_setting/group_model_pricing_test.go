package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// snapshotPricingConfig 备份并在测试结束时还原所有会被本文件改动的全局定价配置喵。
// ratio_setting 的配置都是包级全局变量，不备份的话前一个测试的脏数据会污染后面的测试喵。
func snapshotPricingConfig(t *testing.T) {
	t.Helper()
	previousGroupPricing := GroupModelPricing2JSONString()
	previousModelRatio := ModelRatio2JSONString()
	previousModelPrice := ModelPrice2JSONString()
	previousCompletionRatio := CompletionRatio2JSONString()
	previousCacheRatio := CacheRatio2JSONString()
	previousAudioRatio := AudioRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, UpdateGroupModelPricingByJSONString(previousGroupPricing))
		require.NoError(t, UpdateModelRatioByJSONString(previousModelRatio))
		require.NoError(t, UpdateModelPriceByJSONString(previousModelPrice))
		require.NoError(t, UpdateCompletionRatioByJSONString(previousCompletionRatio))
		require.NoError(t, UpdateCacheRatioByJSONString(previousCacheRatio))
		require.NoError(t, UpdateAudioRatioByJSONString(previousAudioRatio))
	})
}

// TestResolveModelPricingWithoutOverrideFallsBackToGlobal 验证没配分组定制时行为与改造前完全一致喵。
func TestResolveModelPricingWithoutOverrideFallsBackToGlobal(t *testing.T) {
	snapshotPricingConfig(t)

	require.NoError(t, UpdateModelPriceByJSONString(`{}`))
	require.NoError(t, UpdateModelRatioByJSONString(`{"deepseek-chat":1.5}`))
	require.NoError(t, UpdateCompletionRatioByJSONString(`{"deepseek-chat":2}`))
	require.NoError(t, UpdateGroupModelPricingByJSONString(`{}`))

	pricing := ResolveModelPricing("default", "deepseek-chat")
	assert.False(t, pricing.UsePrice)
	assert.Equal(t, 1.5, pricing.ModelRatio)
	assert.True(t, pricing.ModelRatioConfigured)
	assert.Equal(t, 2.0, pricing.CompletionRatio)
	// 没命中定制时 OverrideGroup 必须留空，日志与前端才不会误标定价来源喵。
	assert.Empty(t, pricing.OverrideGroup)
	assert.False(t, pricing.FromGroupOverride())
	// 按量计费时按次价统一归一成 -1 哨兵值，保持历史口径喵。
	assert.Equal(t, -1.0, pricing.ModelPrice)
}

// TestResolveModelPricingPerGroupBillingModes 覆盖主人的核心场景喵：
// 同一个模型 A 组按次、B 组按量、C/D/E 三组按量但价格各不相同喵。
func TestResolveModelPricingPerGroupBillingModes(t *testing.T) {
	snapshotPricingConfig(t)

	require.NoError(t, UpdateModelPriceByJSONString(`{}`))
	require.NoError(t, UpdateModelRatioByJSONString(`{"deepseek-chat":1}`))
	require.NoError(t, UpdateCompletionRatioByJSONString(`{"deepseek-chat":1}`))
	require.NoError(t, UpdateGroupModelPricingByJSONString(`{
		"group-a":{"deepseek-chat":{"billing_mode":"per_call","model_price":0.02}},
		"group-b":{"deepseek-chat":{"billing_mode":"per_token","model_ratio":0.5,"completion_ratio":3}},
		"group-c":{"deepseek-chat":{"model_ratio":0.27}},
		"group-d":{"deepseek-chat":{"model_ratio":0.35}},
		"group-e":{"deepseek-chat":{"model_ratio":0.4,"completion_ratio":4}}
	}`))

	// A 组：显式按次，单价直接来自定制项喵。
	groupA := ResolveModelPricing("group-a", "deepseek-chat")
	assert.True(t, groupA.UsePrice)
	assert.Equal(t, 0.02, groupA.ModelPrice)
	assert.Equal(t, "group-a", groupA.OverrideGroup)

	// B 组：显式按量，倍率与补全倍率都被定制覆盖喵。
	groupB := ResolveModelPricing("group-b", "deepseek-chat")
	assert.False(t, groupB.UsePrice)
	assert.Equal(t, 0.5, groupB.ModelRatio)
	assert.Equal(t, 3.0, groupB.CompletionRatio)

	// C/D/E 组：都没写 billing_mode，全局也没按次价，所以自然是按量，倍率各自不同喵。
	for _, testCase := range []struct {
		group      string
		modelRatio float64
		completion float64
	}{
		{group: "group-c", modelRatio: 0.27, completion: 1},
		{group: "group-d", modelRatio: 0.35, completion: 1},
		{group: "group-e", modelRatio: 0.4, completion: 4},
	} {
		pricing := ResolveModelPricing(testCase.group, "deepseek-chat")
		assert.False(t, pricing.UsePrice, testCase.group)
		assert.Equal(t, testCase.modelRatio, pricing.ModelRatio, testCase.group)
		assert.Equal(t, testCase.completion, pricing.CompletionRatio, testCase.group)
	}

	// 没配定制的分组继续吃全局倍率，不受其它分组的定制影响喵。
	plain := ResolveModelPricing("group-none", "deepseek-chat")
	assert.Equal(t, 1.0, plain.ModelRatio)
	assert.Empty(t, plain.OverrideGroup)
}

// TestResolveModelPricingPerFieldInheritance 验证「没写的字段继承全局、写了 0 就是真免费」喵。
func TestResolveModelPricingPerFieldInheritance(t *testing.T) {
	snapshotPricingConfig(t)

	require.NoError(t, UpdateModelPriceByJSONString(`{}`))
	require.NoError(t, UpdateModelRatioByJSONString(`{"claude-3":2}`))
	require.NoError(t, UpdateCompletionRatioByJSONString(`{"claude-3":5}`))
	require.NoError(t, UpdateCacheRatioByJSONString(`{"claude-3":0.1}`))
	require.NoError(t, UpdateAudioRatioByJSONString(`{"claude-3":8}`))
	// 只覆盖模型倍率与缓存倍率，其余字段留空表示继承全局喵。
	require.NoError(t, UpdateGroupModelPricingByJSONString(`{
		"vip":{"claude-3":{"model_ratio":1.2,"cache_ratio":0}}
	}`))

	pricing := ResolveModelPricing("vip", "claude-3")
	assert.Equal(t, 1.2, pricing.ModelRatio)
	// 补全倍率没被覆盖，必须继承全局的 5 喵。
	assert.Equal(t, 5.0, pricing.CompletionRatio)
	// 缓存倍率显式写了 0，代表这个分组缓存 token 真免费，绝不能被当成「没配」而回落 0.1 喵。
	require.NotNil(t, pricing.CacheRatio)
	assert.Equal(t, 0.0, *pricing.CacheRatio)
	assert.Equal(t, 0.0, pricing.CacheRatioValue())
	// 音频倍率没被覆盖，继续继承全局喵。
	require.NotNil(t, pricing.AudioRatio)
	assert.Equal(t, 8.0, *pricing.AudioRatio)
}

// TestResolveModelPricingPerCallForcedBackToPerToken 验证 per_token 能把全局按次价强制掰回按量喵。
func TestResolveModelPricingPerCallForcedBackToPerToken(t *testing.T) {
	snapshotPricingConfig(t)

	require.NoError(t, UpdateModelPriceByJSONString(`{"suno-v3":0.1}`))
	require.NoError(t, UpdateModelRatioByJSONString(`{"suno-v3":3}`))
	require.NoError(t, UpdateGroupModelPricingByJSONString(`{
		"token-group":{"suno-v3":{"billing_mode":"per_token"}}
	}`))

	// 全局配了按次价，不定制的分组仍然按次喵。
	global := ResolveModelPricing("default", "suno-v3")
	assert.True(t, global.UsePrice)
	assert.Equal(t, 0.1, global.ModelPrice)

	// 定制成 per_token 的分组必须变回按量，且按次价归一成 -1 哨兵喵。
	forced := ResolveModelPricing("token-group", "suno-v3")
	assert.False(t, forced.UsePrice)
	assert.Equal(t, -1.0, forced.ModelPrice)
	assert.Equal(t, 3.0, forced.ModelRatio)
}

// TestResolveModelPricingNegativePriceDegradesToPerToken 验证按次价缺失时降级成按量，绝不产生负额度喵。
func TestResolveModelPricingNegativePriceDegradesToPerToken(t *testing.T) {
	snapshotPricingConfig(t)

	require.NoError(t, UpdateModelPriceByJSONString(`{}`))
	require.NoError(t, UpdateModelRatioByJSONString(`{"ghost-model":1}`))
	// 声明了按次但全局与定制都没有可用单价，此时 GetModelPrice 返回 -1 哨兵喵。
	require.NoError(t, UpdateGroupModelPricingByJSONString(`{
		"broken":{"ghost-model":{"billing_mode":"per_call"}}
	}`))

	pricing := ResolveModelPricing("broken", "ghost-model")
	assert.False(t, pricing.UsePrice)
	assert.Equal(t, -1.0, pricing.ModelPrice)
}

// TestResolveModelPricingUnpricedModelBecomesPricedInGroup 验证分组定制能让全局未定价的模型在本分组可用喵。
func TestResolveModelPricingUnpricedModelBecomesPricedInGroup(t *testing.T) {
	snapshotPricingConfig(t)

	require.NoError(t, UpdateModelPriceByJSONString(`{}`))
	require.NoError(t, UpdateModelRatioByJSONString(`{}`))
	require.NoError(t, UpdateGroupModelPricingByJSONString(`{
		"pioneer":{"brand-new-model":{"model_ratio":0.8}}
	}`))

	// 全局没配倍率时必须是「未配置」，计费链路会按未定价模型处理喵。
	global := ResolveModelPricing("default", "brand-new-model")
	assert.False(t, global.ModelRatioConfigured)

	// 分组定制了倍率之后，这个分组就变成已定价了喵。
	scoped := ResolveModelPricing("pioneer", "brand-new-model")
	assert.True(t, scoped.ModelRatioConfigured)
	assert.Equal(t, 0.8, scoped.ModelRatio)
}

// TestGetGroupModelOverrideEdgeCases 覆盖查表的各类空值与通配符归一化边界喵。
func TestGetGroupModelOverrideEdgeCases(t *testing.T) {
	snapshotPricingConfig(t)

	require.NoError(t, UpdateGroupModelPricingByJSONString(`{
		"vip":{"gpt-4-gizmo-*":{"model_ratio":2}},
		"empty":{}
	}`))

	// 空分组名、空模型名都必须判为没有定制，不能拿空 key 去查表喵。
	_, ok := GetGroupModelOverride("", "gpt-4-gizmo-abc")
	assert.False(t, ok)
	_, ok = GetGroupModelOverride("vip", "")
	assert.False(t, ok)
	// 分组存在但配成空表时同样按没有定制处理喵。
	_, ok = GetGroupModelOverride("empty", "gpt-4-gizmo-abc")
	assert.False(t, ok)
	// 通配符归一化之后才命中，验证与全局倍率同一套查找口径喵。
	override, ok := GetGroupModelOverride("vip", "gpt-4-gizmo-abc")
	require.True(t, ok)
	require.NotNil(t, override.ModelRatio)
	assert.Equal(t, 2.0, *override.ModelRatio)
}

// TestCheckGroupModelPricingRejectsInvalidConfig 验证保存前的校验会拦下所有危险配置喵。
func TestCheckGroupModelPricingRejectsInvalidConfig(t *testing.T) {
	snapshotPricingConfig(t)

	require.NoError(t, UpdateModelPriceByJSONString(`{}`))

	// 合法配置：空对象、只写倍率、显式按量都应该放行喵。
	require.NoError(t, CheckGroupModelPricing(`{}`))
	require.NoError(t, CheckGroupModelPricing(`{"vip":{"m":{"model_ratio":1}}}`))
	require.NoError(t, CheckGroupModelPricing(`{"vip":{"m":{"billing_mode":"per_token"}}}`))
	// 显式写 0 是「真免费」，属于合法意图，不能被当成非法值拦掉喵。
	require.NoError(t, CheckGroupModelPricing(`{"vip":{"m":{"model_ratio":0}}}`))

	for name, jsonStr := range map[string]string{
		"非 JSON":      `not-json`,
		"结构不对":        `{"vip":1}`,
		"空分组名":        `{"":{"m":{"model_ratio":1}}}`,
		"空模型名":        `{"vip":{"":{"model_ratio":1}}}`,
		"计费方式非法":      `{"vip":{"m":{"billing_mode":"weird"}}}`,
		"负倍率":         `{"vip":{"m":{"model_ratio":-1}}}`,
		"负按次价":        `{"vip":{"m":{"model_price":-0.01}}}`,
		"按次却没有任何可用单价": `{"vip":{"m":{"billing_mode":"per_call"}}}`,
		"负缓存倍率":       `{"vip":{"m":{"cache_ratio":-0.5}}}`,
		"负音频补全倍率":     `{"vip":{"m":{"audio_completion_ratio":-2}}}`,
	} {
		assert.Error(t, CheckGroupModelPricing(jsonStr), name)
	}
}

// TestCheckGroupModelPricingPerCallAllowsGlobalPrice 验证全局配过按次价时，
// 分组只声明 per_call、不重复填单价也应该合法喵。
func TestCheckGroupModelPricingPerCallAllowsGlobalPrice(t *testing.T) {
	snapshotPricingConfig(t)

	require.NoError(t, UpdateModelPriceByJSONString(`{"midjourney":0.05}`))
	require.NoError(t, CheckGroupModelPricing(`{"vip":{"midjourney":{"billing_mode":"per_call"}}}`))
}

// TestUpdateGroupModelPricingRoundTrip 验证配置能原样写入并读回，且坏 JSON 会返回错误喵。
func TestUpdateGroupModelPricingRoundTrip(t *testing.T) {
	snapshotPricingConfig(t)

	require.NoError(t, UpdateGroupModelPricingByJSONString(`{"vip":{"m":{"model_ratio":1.25}}}`))
	copied := GetGroupModelPricingCopy()
	require.Contains(t, copied, "vip")
	require.Contains(t, copied["vip"], "m")
	require.NotNil(t, copied["vip"]["m"].ModelRatio)
	assert.Equal(t, 1.25, *copied["vip"]["m"].ModelRatio)

	// 喵~防御：坏 JSON 必须返回错误。注意底层 RWMap 会先清空再解析，
	// 所以解析失败时这份配置会变成空表——这是全站所有 Update*ByJSONString 的既有行为，
	// 真正的防线是 API 层先调 CheckGroupModelPricing 校验通过才写库喵。
	require.Error(t, UpdateGroupModelPricingByJSONString(`{oops`))
	assert.Equal(t, "{}", GroupModelPricing2JSONString())
}
