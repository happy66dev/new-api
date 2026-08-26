package model

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNormalizeUserUpstreamModelName 验证上游模型名规范化的边界喵。
func TestNormalizeUserUpstreamModelName(t *testing.T) {
	// 正常名称统一转为小写喵。
	normalizedName, err := NormalizeUserUpstreamModelName("My-Upstream_1")
	require.NoError(t, err)
	assert.Equal(t, "my-upstream_1", normalizedName)

	// 带 upstream/ 前缀时自动剥离喵。
	normalizedName, err = NormalizeUserUpstreamModelName("upstream/My-Model")
	require.NoError(t, err)
	assert.Equal(t, "my-model", normalizedName)

	// 空白输入拒绝喵。
	_, err = NormalizeUserUpstreamModelName("   ")
	require.Error(t, err)

	// 非法字符（中文、空格、路径分隔符）拒绝喵。
	for _, invalidName := range []string{"中文模型", "my model", "a/b", "a\\b", "a.."} {
		_, err := NormalizeUserUpstreamModelName(invalidName)
		assert.Error(t, err, "名称 %q 应被拒绝", invalidName)
	}

	// 超长名称拒绝喵。
	_, err = NormalizeUserUpstreamModelName(strings.Repeat("a", 97))
	require.Error(t, err)
}

// TestUserUpstreamModelCRUD 验证用户上游模型完整 CRUD 流程喵。
func TestUserUpstreamModelCRUD(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&UserUpstreamModel{}))
	require.NoError(t, DB.Exec("DELETE FROM user_upstream_models").Error)

	// 创建一条用户上游模型，凭据密文直接写入喵。
	created := &UserUpstreamModel{
		OwnerUserID:      7,
		NormalizedName:   "alpha",
		DisplayName:      "Alpha 上游",
		Enabled:          true,
		EncryptedBaseURL: "encrypted-base-url",
		EncryptedAPIKey:  "encrypted-api-key",
		RealModelName:    "gpt-4o",
		AuthStyle:        "bearer",
		ModelRatio:       "18.5",
		Version:          1,
		CreatedTime:      100,
		UpdatedTime:      100,
	}
	require.NoError(t, DB.Create(created).Error)

	// 属主可查询到，名称与模型名符合预期喵。
	fetched, err := GetUserUpstreamModelByOwnerID(created.ID, 7)
	require.NoError(t, err)
	assert.Equal(t, "alpha", fetched.NormalizedName)
	assert.Equal(t, "upstream/alpha", fetched.UserUpstreamModelName())

	// 列表只返回属主自己的模型喵。
	list, err := GetUserUpstreamModelsByOwner(7)
	require.NoError(t, err)
	assert.Len(t, list, 1)

	// 启用模型可被属主按名称查到喵。
	enabled, err := GetEnabledUserUpstreamModelByOwnerName(7, "alpha")
	require.NoError(t, err)
	assert.Equal(t, "alpha", enabled.NormalizedName)

	// 跨用户查询必须隐藏资源存在性喵。
	_, err = GetUserUpstreamModelByOwnerID(created.ID, 8)
	require.Error(t, err)
	_, err = GetEnabledUserUpstreamModelByOwnerName(8, "alpha")
	require.Error(t, err)

	// 停用模型不能被会话态查询到喵。
	require.NoError(t, DB.Model(created).Update("enabled", false).Error)
	_, err = GetEnabledUserUpstreamModelByOwnerName(7, "alpha")
	require.Error(t, err)

	// 版本错误的删除被拒绝喵。
	require.NoError(t, DB.Model(created).Update("enabled", true).Error)
	require.NoError(t, DB.Model(created).Update("version", 2).Error)
	err = DeleteUserUpstreamModelByOwnerWithVersion(created.ID, 7, 1)
	require.Error(t, err)

	// 正确版本的删除成功，重复删除返回记录不存在喵。
	err = DeleteUserUpstreamModelByOwnerWithVersion(created.ID, 7, 2)
	require.NoError(t, err)
	err = DeleteUserUpstreamModelByOwnerWithVersion(created.ID, 7, 2)
	require.Error(t, err)
}

// TestDeductUserUpstreamModelCharge 验证独立计费扣减的余额钳制与累计语义喵。
func TestDeductUserUpstreamModelCharge(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&UserUpstreamModel{}))
	require.NoError(t, DB.Exec("DELETE FROM user_upstream_models").Error)

	// 创建余额 1000 分、自用上限 800 分、共享上限 600 分的模型喵。
	created := &UserUpstreamModel{
		OwnerUserID:     7,
		NormalizedName:  "billing",
		Enabled:         true,
		BalanceCents:    1000,
		SpendLimitCents: 800,
		TotalSpentCents: 0,
		ShareLimitCents: 600,
		ShareSpentCents: 0,
		Version:         1,
		CreatedTime:     100,
		UpdatedTime:     100,
	}
	require.NoError(t, DB.Create(created).Error)

	// 正常自用扣费：余额减少、自用累计增加喵。
	require.NoError(t, DeductUserUpstreamModelCharge(created.ID, 7, 300, false))
	fetched, err := GetUserUpstreamModelByOwnerID(created.ID, 7)
	require.NoError(t, err)
	assert.Equal(t, int64(700), fetched.BalanceCents)
	assert.Equal(t, int64(300), fetched.TotalSpentCents)

	// 余额不足以覆盖扣费时钳制到 0，绝不产生负余额喵。
	require.NoError(t, DeductUserUpstreamModelCharge(created.ID, 7, 800, false))
	fetched, err = GetUserUpstreamModelByOwnerID(created.ID, 7)
	require.NoError(t, err)
	assert.Equal(t, int64(0), fetched.BalanceCents)
	assert.Equal(t, int64(1100), fetched.TotalSpentCents)

	// 负数费用直接拒绝，不产生任何写入喵。
	beforeBalance := fetched.BalanceCents
	beforeSpent := fetched.TotalSpentCents
	err = DeductUserUpstreamModelCharge(created.ID, 7, -10, false)
	require.Error(t, err)
	fetched, _ = GetUserUpstreamModelByOwnerID(created.ID, 7)
	assert.Equal(t, beforeBalance, fetched.BalanceCents)
	assert.Equal(t, beforeSpent, fetched.TotalSpentCents)

	// 零费用不写库，余额与累计保持不变喵。
	require.NoError(t, DeductUserUpstreamModelCharge(created.ID, 7, 0, false))
	fetched, _ = GetUserUpstreamModelByOwnerID(created.ID, 7)
	assert.Equal(t, int64(0), fetched.BalanceCents)
	assert.Equal(t, int64(1100), fetched.TotalSpentCents)

	// 共享调用免费：只累计共享消耗，余额与自用累计不变喵。
	require.NoError(t, DeductUserUpstreamModelCharge(created.ID, 7, 250, true))
	fetched, _ = GetUserUpstreamModelByOwnerID(created.ID, 7)
	assert.Equal(t, int64(0), fetched.BalanceCents)
	assert.Equal(t, int64(1100), fetched.TotalSpentCents)
	assert.Equal(t, int64(250), fetched.ShareSpentCents)

	// 无效属主或 ID 返回记录不存在，避免跨用户扣费喵。
	require.Error(t, DeductUserUpstreamModelCharge(created.ID, 8, 10, false))
	require.Error(t, DeductUserUpstreamModelCharge(0, 7, 10, false))
}

// TestSharedUserUpstreamModels 验证共享模型可见性、额度耗尽停止与跨用户调用授权喵。
func TestSharedUserUpstreamModels(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&UserUpstreamModel{}))
	require.NoError(t, DB.Exec("DELETE FROM user_upstream_models").Error)

	// 创建共享中（有额度）、无共享上限、共享额度耗尽与未开启共享的四种模型喵。
	require.NoError(t, DB.Create(&UserUpstreamModel{OwnerUserID: 7, NormalizedName: "shared-ok", Enabled: true, ShareEnabled: true, ShareLimitCents: 1000, ShareSpentCents: 100}).Error)
	require.NoError(t, DB.Create(&UserUpstreamModel{OwnerUserID: 7, NormalizedName: "shared-unlimited", Enabled: true, ShareEnabled: true, ShareLimitCents: 0}).Error)
	require.NoError(t, DB.Create(&UserUpstreamModel{OwnerUserID: 7, NormalizedName: "shared-exhausted", Enabled: true, ShareEnabled: true, ShareLimitCents: 500, ShareSpentCents: 500}).Error)
	require.NoError(t, DB.Create(&UserUpstreamModel{OwnerUserID: 7, NormalizedName: "not-shared", Enabled: true, ShareEnabled: false}).Error)

	// 共享中的模型名只包含未耗尽的共享模型喵。
	names := GetSharedUserUpstreamModelNames()
	assert.Contains(t, names, "upstream/shared-ok")
	assert.Contains(t, names, "upstream/shared-unlimited")
	assert.NotContains(t, names, "upstream/shared-exhausted")
	assert.NotContains(t, names, "upstream/not-shared")

	// 共享额度的剩余视图字段正确喵。
	views, err := GetSharedUserUpstreamModels()
	require.NoError(t, err)
	assert.Len(t, views, 2)

	// 共享耗尽模型按名称查询返回记录不存在（停止共享）喵。
	_, err = GetEnabledSharedUserUpstreamModelByName("shared-exhausted")
	require.Error(t, err)

	// 共享中模型可被任意用户按名称查找到喵。
	shared, err := GetEnabledSharedUserUpstreamModelByName("shared-ok")
	require.NoError(t, err)
	assert.Equal(t, 7, shared.OwnerUserID)
	assert.Equal(t, int64(1000), shared.ShareLimitCents)

	// 空名称与未开启共享的名称都查不到喵。
	_, err = GetEnabledSharedUserUpstreamModelByName("")
	require.Error(t, err)
	_, err = GetEnabledSharedUserUpstreamModelByName("not-shared")
	require.Error(t, err)
}
