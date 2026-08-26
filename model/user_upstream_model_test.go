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

	// 带 user/ 前缀时自动剥离喵。
	normalizedName, err = NormalizeUserUpstreamModelName("user/My-Model")
	require.NoError(t, err)
	assert.Equal(t, "my-model", normalizedName)

	// 兼容旧前缀 upstream/ 的存量请求喵。
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
	assert.Equal(t, "user/alpha", fetched.UserUpstreamModelName())

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

// TestDeductUserUpstreamModelCharge 验证独立计费三账户递减扣减与钳制语义喵。
func TestDeductUserUpstreamModelCharge(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&UserUpstreamModel{}))
	require.NoError(t, DB.Exec("DELETE FROM user_upstream_models").Error)

	// 创建余额 1000 分、可用额度 800 分、共享额度 600 分的模型喵。
	created := &UserUpstreamModel{
		OwnerUserID:     7,
		NormalizedName:  "billing",
		Enabled:         true,
		BalanceCents:    1000,
		AvailableCents:  800,
		ShareLimitCents: 600,
		Version:         1,
		CreatedTime:     100,
		UpdatedTime:     100,
	}
	require.NoError(t, DB.Create(created).Error)

	// 正常自用扣费：余额与可用额度同时减少，共享额度不受影响喵。
	require.NoError(t, DeductUserUpstreamModelCharge(created.ID, 7, 300, false))
	fetched, err := GetUserUpstreamModelByOwnerID(created.ID, 7)
	require.NoError(t, err)
	assert.Equal(t, int64(700), fetched.BalanceCents)
	assert.Equal(t, int64(500), fetched.AvailableCents)
	assert.Equal(t, int64(600), fetched.ShareLimitCents)

	// 可用额度不足以覆盖扣费时钳制到 0，绝不产生负值喵。
	require.NoError(t, DeductUserUpstreamModelCharge(created.ID, 7, 800, false))
	fetched, err = GetUserUpstreamModelByOwnerID(created.ID, 7)
	require.NoError(t, err)
	assert.Equal(t, int64(0), fetched.BalanceCents)
	assert.Equal(t, int64(0), fetched.AvailableCents)
	assert.Equal(t, int64(600), fetched.ShareLimitCents)

	// 负数费用直接拒绝，不产生任何写入喵。
	beforeBalance := fetched.BalanceCents
	beforeAvailable := fetched.AvailableCents
	err = DeductUserUpstreamModelCharge(created.ID, 7, -10, false)
	require.Error(t, err)
	fetched, _ = GetUserUpstreamModelByOwnerID(created.ID, 7)
	assert.Equal(t, beforeBalance, fetched.BalanceCents)
	assert.Equal(t, beforeAvailable, fetched.AvailableCents)

	// 零费用不写库，各账户保持不变喵。
	require.NoError(t, DeductUserUpstreamModelCharge(created.ID, 7, 0, false))
	fetched, _ = GetUserUpstreamModelByOwnerID(created.ID, 7)
	assert.Equal(t, int64(0), fetched.BalanceCents)
	assert.Equal(t, int64(0), fetched.AvailableCents)
	assert.Equal(t, int64(600), fetched.ShareLimitCents)

	// 共享调用扣「余额+可用+共享」三个账户：余额/可用已为 0 保持钳 0，共享 600-250=350 喵。
	require.NoError(t, DeductUserUpstreamModelCharge(created.ID, 7, 250, true))
	fetched, _ = GetUserUpstreamModelByOwnerID(created.ID, 7)
	assert.Equal(t, int64(0), fetched.BalanceCents)
	assert.Equal(t, int64(0), fetched.AvailableCents)
	assert.Equal(t, int64(350), fetched.ShareLimitCents)

	// 共享费用超过共享余额时钳制到 0，绝不产生负共享额度喵。
	require.NoError(t, DeductUserUpstreamModelCharge(created.ID, 7, 400, true))
	fetched, _ = GetUserUpstreamModelByOwnerID(created.ID, 7)
	assert.Equal(t, int64(0), fetched.ShareLimitCents)

	// 无效属主或 ID 返回记录不存在，避免跨用户扣费喵。
	require.Error(t, DeductUserUpstreamModelCharge(created.ID, 8, 10, false))
	require.Error(t, DeductUserUpstreamModelCharge(0, 7, 10, false))
}

// TestSharedUserUpstreamModels 验证共享模型可见性、账户耗尽停止与跨用户调用授权喵。
func TestSharedUserUpstreamModels(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&UserUpstreamModel{}))
	require.NoError(t, DB.Exec("DELETE FROM user_upstream_models").Error)

	// 创建共享中（三账户都有余额）、余额耗尽、可用耗尽、共享额度耗尽与未开启共享的模型喵。
	require.NoError(t, DB.Create(&UserUpstreamModel{OwnerUserID: 7, NormalizedName: "shared-ok", Enabled: true, ShareEnabled: true, BalanceCents: 100, AvailableCents: 100, ShareLimitCents: 1000}).Error)
	require.NoError(t, DB.Create(&UserUpstreamModel{OwnerUserID: 7, NormalizedName: "shared-balance-empty", Enabled: true, ShareEnabled: true, BalanceCents: 0, AvailableCents: 100, ShareLimitCents: 1000}).Error)
	require.NoError(t, DB.Create(&UserUpstreamModel{OwnerUserID: 7, NormalizedName: "shared-available-empty", Enabled: true, ShareEnabled: true, BalanceCents: 100, AvailableCents: 0, ShareLimitCents: 1000}).Error)
	require.NoError(t, DB.Create(&UserUpstreamModel{OwnerUserID: 7, NormalizedName: "shared-exhausted", Enabled: true, ShareEnabled: true, BalanceCents: 100, AvailableCents: 100, ShareLimitCents: 0}).Error)
	require.NoError(t, DB.Create(&UserUpstreamModel{OwnerUserID: 7, NormalizedName: "not-shared", Enabled: true, ShareEnabled: false, BalanceCents: 100, AvailableCents: 100, ShareLimitCents: 1000}).Error)

	// 共享中的模型名只包含三账户都未耗尽的共享模型喵。
	names := GetSharedUserUpstreamModelNames()
	assert.Contains(t, names, "user/shared-ok")
	assert.NotContains(t, names, "user/shared-balance-empty")
	assert.NotContains(t, names, "user/shared-available-empty")
	assert.NotContains(t, names, "user/shared-exhausted")
	assert.NotContains(t, names, "user/not-shared")

	// 共享视图只包含共享中的模型喵。
	views, err := GetSharedUserUpstreamModels()
	require.NoError(t, err)
	assert.Len(t, views, 1)

	// 任一账户耗尽的模型按名称查询返回记录不存在（自动停止共享）喵。
	_, err = GetEnabledSharedUserUpstreamModelByName("shared-exhausted")
	require.Error(t, err)
	_, err = GetEnabledSharedUserUpstreamModelByName("shared-balance-empty")
	require.Error(t, err)
	_, err = GetEnabledSharedUserUpstreamModelByName("shared-available-empty")
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

// TestGetSharedUserUpstreamModelByNormalizedName 验证共享命名池查询：共享开启才占用名字喵。
func TestGetSharedUserUpstreamModelByNormalizedName(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&UserUpstreamModel{}))
	require.NoError(t, DB.Exec("DELETE FROM user_upstream_models").Error)

	// 用户7 共享开启名为 pool-a，占用了全局共享池名字喵。
	require.NoError(t, DB.Create(&UserUpstreamModel{OwnerUserID: 7, NormalizedName: "pool-a", ShareEnabled: true, Version: 1}).Error)
	// 用户8 的非共享同名模型不占用共享池名字喵。
	require.NoError(t, DB.Create(&UserUpstreamModel{OwnerUserID: 8, NormalizedName: "pool-private", ShareEnabled: false, Version: 1}).Error)

	// 共享中的名字可被查找到，属主不限定喵。
	existing, err := GetSharedUserUpstreamModelByNormalizedName("pool-a")
	require.NoError(t, err)
	assert.Equal(t, 7, existing.OwnerUserID)

	// 非共享模型的名字不在共享池中喵。
	_, err = GetSharedUserUpstreamModelByNormalizedName("pool-private")
	require.Error(t, err)

	// 空名称直接返回记录不存在喵。
	_, err = GetSharedUserUpstreamModelByNormalizedName("")
	require.Error(t, err)
}

// TestSyncUserUpstreamModelAvailable 验证「一键设为可用额度」把嗅探结果写入可用账户喵。
func TestSyncUserUpstreamModelAvailable(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&UserUpstreamModel{}))
	require.NoError(t, DB.Exec("DELETE FROM user_upstream_models").Error)

	// 创建可用额度 100 分、嗅探剩余 2500 分的模型喵。
	created := &UserUpstreamModel{
		OwnerUserID:            7,
		NormalizedName:         "sync-available",
		Enabled:                true,
		BalanceCents:           500,
		AvailableCents:         100,
		UpstreamRemainingCents: 2500,
		Version:                1,
		CreatedTime:            100,
		UpdatedTime:            100,
	}
	require.NoError(t, DB.Create(created).Error)

	// 一键设为可用：可用额度被嗅探结果覆盖，余额不变喵。
	require.NoError(t, SyncUserUpstreamModelAvailable(created.ID, 7))
	fetched, err := GetUserUpstreamModelByOwnerID(created.ID, 7)
	require.NoError(t, err)
	assert.Equal(t, int64(2500), fetched.AvailableCents)
	assert.Equal(t, int64(500), fetched.BalanceCents)

	// 无效参数返回记录不存在喵。
	require.Error(t, SyncUserUpstreamModelAvailable(created.ID, 8))
	require.Error(t, SyncUserUpstreamModelAvailable(0, 7))
}

// TestMigrateUserUpstreamAvailableCents 验证旧「使用上限」幂等回填为「可用额度」喵。
func TestMigrateUserUpstreamAvailableCents(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&UserUpstreamModel{}))
	require.NoError(t, DB.Exec("DELETE FROM user_upstream_models").Error)

	// 旧使用上限为 800 且可用为 0 的行应回填为 800；可用已设置的行不受影响喵。
	require.NoError(t, DB.Create(&UserUpstreamModel{OwnerUserID: 7, NormalizedName: "migrate-a", Enabled: true, SpendLimitCents: 800, AvailableCents: 0}).Error)
	require.NoError(t, DB.Create(&UserUpstreamModel{OwnerUserID: 7, NormalizedName: "migrate-b", Enabled: true, SpendLimitCents: 900, AvailableCents: 300}).Error)
	require.NoError(t, DB.Create(&UserUpstreamModel{OwnerUserID: 7, NormalizedName: "migrate-c", Enabled: true, SpendLimitCents: 0, AvailableCents: 0}).Error)

	require.NoError(t, migrateUserUpstreamAvailableCents())

	// 回填后可用额度与旧使用上限一致喵。
	var fetchedA UserUpstreamModel
	require.NoError(t, DB.Where("normalized_name = ?", "migrate-a").First(&fetchedA).Error)
	assert.Equal(t, int64(800), fetchedA.AvailableCents)
	// 已设置可用额度的行不被覆盖喵。
	var fetchedB UserUpstreamModel
	require.NoError(t, DB.Where("normalized_name = ?", "migrate-b").First(&fetchedB).Error)
	assert.Equal(t, int64(300), fetchedB.AvailableCents)
	// 旧使用上限为 0 的行保持 0 喵。
	var fetchedC UserUpstreamModel
	require.NoError(t, DB.Where("normalized_name = ?", "migrate-c").First(&fetchedC).Error)
	assert.Equal(t, int64(0), fetchedC.AvailableCents)

	// 幂等：再次运行不回填已回填的行喵。
	require.NoError(t, migrateUserUpstreamAvailableCents())
	var fetchedAgain UserUpstreamModel
	require.NoError(t, DB.Where("normalized_name = ?", "migrate-a").First(&fetchedAgain).Error)
	assert.Equal(t, int64(800), fetchedAgain.AvailableCents)
}
