package service

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// snapshotTaskGroupPricingConfig 备份并还原任务重算测试会改动的定价配置喵。
func snapshotTaskGroupPricingConfig(t *testing.T) {
	t.Helper()
	previousGroupPricing := ratio_setting.GroupModelPricing2JSONString()
	previousModelRatio := ratio_setting.ModelRatio2JSONString()
	previousModelPrice := ratio_setting.ModelPrice2JSONString()
	previousGroupRatio := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupModelPricingByJSONString(previousGroupPricing))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousModelRatio))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousModelPrice))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroupRatio))
	})
}

// TestRecalculateTaskQuotaByTokensRespectsGroupOverride 验证任务按 token 重算时使用
// 「任务所属分组」的定制倍率，而不是全局倍率喵。
// 改造前这里只按模型名读全局倍率，会让 B 组完成的任务被按 A 组/全局的价重算一遍喵。
func TestRecalculateTaskQuotaByTokensRespectsGroupOverride(t *testing.T) {
	snapshotTaskGroupPricingConfig(t)

	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"test-model":1}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"cheap":1,"per-call":1}`))
	require.NoError(t, ratio_setting.UpdateGroupModelPricingByJSONString(`{
		"cheap":{"test-model":{"model_ratio":0.25}},
		"per-call":{"test-model":{"billing_mode":"per_call","model_price":0.02}}
	}`))

	tests := []struct {
		name        string
		group       string
		wantRecalc  bool
		wantQuota   int
		description string
	}{
		{
			name:        "没配定制的分组继续吃全局倍率",
			group:       "default",
			wantRecalc:  true,
			wantQuota:   800,
			description: "800 token × 全局倍率 1 = 800",
		},
		{
			name:        "定制了更低倍率的分组按定制倍率重算",
			group:       "cheap",
			wantRecalc:  true,
			wantQuota:   200,
			description: "800 token × 定制倍率 0.25 = 200",
		},
		{
			name:        "定制成按次计费的分组不做 token 重算",
			group:       "per-call",
			wantRecalc:  false,
			wantQuota:   50,
			description: "按次计费的任务价格已定，保持预扣额度不变",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			truncate(t)
			const userID, tokenID, channelID = 61, 61, 61
			const initialQuota, preConsumed, tokenRemain = 10_000, 50, 8_000
			seedUser(t, userID, initialQuota)
			seedToken(t, tokenID, userID, "sk-group-pricing-recalc", tokenRemain)
			seedChannel(t, channelID)

			task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
			// 任务上记录的分组就是它当初实际执行时用的分组，重算必须以它为准喵。
			task.Group = testCase.group

			recalculated := RecalculateTaskQuotaByTokens(context.Background(), task, 800)

			assert.Equal(t, testCase.wantRecalc, recalculated, testCase.description)
			assert.Equal(t, testCase.wantQuota, task.Quota, testCase.description)
		})
	}
}

// TestRecalculateTaskQuotaByTokensSkipsWhenGroupUnpriced 验证倍率在该分组下没配时
// 不做重算，避免拿兜底倍率算出一笔莫名其妙的账喵。
func TestRecalculateTaskQuotaByTokensSkipsWhenGroupUnpriced(t *testing.T) {
	snapshotTaskGroupPricingConfig(t)

	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	require.NoError(t, ratio_setting.UpdateGroupModelPricingByJSONString(`{}`))

	truncate(t)
	const userID, tokenID, channelID = 62, 62, 62
	seedUser(t, userID, 10_000)
	seedToken(t, tokenID, userID, "sk-group-pricing-unpriced", 8_000)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, 50, tokenID, BillingSourceWallet, 0)
	task.Group = "default"

	assert.False(t, RecalculateTaskQuotaByTokens(context.Background(), task, 800))
	assert.Equal(t, 50, task.Quota)
}

// TestRecalculateTaskQuotaByTokensSkipsWithoutGroup 验证任务与用户都拿不到分组时直接跳过重算喵。
// 喵~防御：分组为空会让分组定制价整体失效，此时宁可保留预扣额度也不要按错误口径扣钱喵。
func TestRecalculateTaskQuotaByTokensSkipsWithoutGroup(t *testing.T) {
	snapshotTaskGroupPricingConfig(t)

	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"test-model":1}`))

	truncate(t)
	const channelID = 63
	seedChannel(t, channelID)

	// UserId 指向一个不存在的用户，所以既没有 task.Group 也查不到用户分组喵。
	task := makeTask(9999, channelID, 50, 0, BillingSourceWallet, 0)
	task.Group = ""

	assert.False(t, RecalculateTaskQuotaByTokens(context.Background(), task, 800))
	assert.Equal(t, 50, task.Quota)
}
