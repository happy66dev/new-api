package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUserUpstreamFeatureAccessors 验证两个功能开关的默认读取行为喵。
func TestUserUpstreamFeatureAccessors(t *testing.T) {
	// 保存旧值并在结束后恢复，避免污染其它测试喵。
	oldMaster, oldShare := common.OptionMap[UserUpstreamEnabledKey], common.OptionMap[UserUpstreamSharingEnabledKey]
	defer func() {
		common.OptionMap[UserUpstreamEnabledKey] = oldMaster
		common.OptionMap[UserUpstreamSharingEnabledKey] = oldShare
	}()

	// 值等于 true 时两个访问器都返回开启喵。
	common.OptionMap[UserUpstreamEnabledKey] = "true"
	common.OptionMap[UserUpstreamSharingEnabledKey] = "true"
	assert.True(t, UserUpstreamFeatureEnabled())
	assert.True(t, UserUpstreamSharingFeatureEnabled())

	// 值非 true（含缺失、空串）一律视为关闭，避免误判喵。
	common.OptionMap[UserUpstreamEnabledKey] = "false"
	common.OptionMap[UserUpstreamSharingEnabledKey] = ""
	assert.False(t, UserUpstreamFeatureEnabled())
	assert.False(t, UserUpstreamSharingFeatureEnabled())
}

// migrateFeatureTestTables 迁移本次开关清理涉及的数据库表并清空历史数据喵。
func migrateFeatureTestTables(t *testing.T) {
	// 逐个迁移，保证不同测试环境（仅迁移用到的表）都能自建所需表喵。
	for _, table := range []any{
		&Option{},
		&UserUpstreamModel{},
		&VirtualModelCandidate{},
		&VirtualModelCustomCandidate{},
		&VirtualModelFailureRule{},
		&EntityProbeState{},
	} {
		require.NoError(t, DB.AutoMigrate(table), "自动迁移表失败")
	}
	// 清空各表旧数据，保证断言只看本次插入喵；options 只清理本功能的两把开关行，避免破坏其它测试依赖喵。
	require.NoError(t, DB.Where("key IN ?", []string{UserUpstreamEnabledKey, UserUpstreamSharingEnabledKey}).Delete(&Option{}).Error)
	for _, target := range []any{
		&UserUpstreamModel{}, &VirtualModelCandidate{},
		&VirtualModelCustomCandidate{}, &VirtualModelFailureRule{}, &EntityProbeState{},
	} {
		require.NoError(t, DB.Where("1 = 1").Delete(target).Error, "清空表失败")
	}
}

// saveFeatureOptionState 记录并设置两开关为指定值，返回恢复旧值的闭包喵。
func saveFeatureOptionState(t *testing.T, master string, share string) func() {
	t.Helper()
	oldMaster, oldShare := common.OptionMap[UserUpstreamEnabledKey], common.OptionMap[UserUpstreamSharingEnabledKey]
	common.OptionMap[UserUpstreamEnabledKey] = master
	common.OptionMap[UserUpstreamSharingEnabledKey] = share
	return func() {
		common.OptionMap[UserUpstreamEnabledKey] = oldMaster
		common.OptionMap[UserUpstreamSharingEnabledKey] = oldShare
	}
}

// seedFeatureCleanupFixture 构造一个被虚拟候选引用且共享中的上游模型喵。
func seedFeatureCleanupFixture(t *testing.T) (*UserUpstreamModel, *VirtualModelCandidate) {
	t.Helper()
	// 上游模型：开启共享，数据行后续必须保留（仅存档）喵。
	upstream := &UserUpstreamModel{
		OwnerUserID:      7,
		NormalizedName:   "alpha",
		DisplayName:      "Alpha",
		Enabled:          true,
		ShareEnabled:     true,
		EncryptedBaseURL: "encrypted-base-url",
		EncryptedAPIKey:  "encrypted-api-key",
		RealModelName:    "gpt-4o",
		AuthStyle:        "bearer",
		Version:          1,
		CreatedTime:      100,
		UpdatedTime:      100,
	}
	require.NoError(t, DB.Create(upstream).Error)

	// 虚拟模型候选 + 引用上游的自定义候选 + 一条失败规则，供"删除引用"断言用喵。
	candidate := &VirtualModelCandidate{
		VirtualModelID: 11, StableOrder: 0, SourceType: VirtualModelSourceCustom,
		Enabled: true, Version: 1, CreatedTime: 100, UpdatedTime: 100,
	}
	require.NoError(t, DB.Create(candidate).Error)
	upstreamID := int64(upstream.ID)
	require.NoError(t, DB.Create(&VirtualModelCustomCandidate{
		CandidateID: candidate.ID, UpstreamModelID: &upstreamID,
		EncryptedBaseURL: "enc-url", EncryptedAPIKey: "enc-key", RealModelName: "gpt-4o", AuthStyle: VirtualModelAuthBearer,
	}).Error)
	require.NoError(t, DB.Create(&VirtualModelFailureRule{
		CandidateID: candidate.ID, RuleOrder: 0, Action: VirtualModelActionNext,
	}).Error)
	return upstream, candidate
}

// TestSetUserUpstreamFeatureOptionSharingDisable 验证关闭共享开关的全站清理语义喵。
func TestSetUserUpstreamFeatureOptionSharingDisable(t *testing.T) {
	truncateTables(t)
	migrateFeatureTestTables(t)
	restore := saveFeatureOptionState(t, "true", "true")
	defer restore()

	upstream, _ := seedFeatureCleanupFixture(t)

	// 关闭共享开关喵。
	require.NoError(t, SetUserUpstreamFeatureOption(UserUpstreamSharingEnabledKey, "false"))

	// 上游数据行保留（不删除存档数据）喵。
	var upstreamCount int64
	require.NoError(t, DB.Model(&UserUpstreamModel{}).Count(&upstreamCount).Error)
	assert.EqualValues(t, 1, upstreamCount)
	// 共享标志被批量撤销喵。
	var reloaded UserUpstreamModel
	require.NoError(t, DB.First(&reloaded, upstream.ID).Error)
	assert.False(t, reloaded.ShareEnabled)

	// 引用共享上游的自定义候选及其规则被删除喵。
	var candidateCount int64
	require.NoError(t, DB.Model(&VirtualModelCustomCandidate{}).Count(&candidateCount).Error)
	assert.Zero(t, candidateCount)
	var slotCount int64
	require.NoError(t, DB.Model(&VirtualModelCandidate{}).Count(&slotCount).Error)
	assert.Zero(t, slotCount)

	// 总开关保持开启、共享开关变为关闭喵。
	assert.True(t, UserUpstreamFeatureEnabled())
	assert.False(t, UserUpstreamSharingFeatureEnabled())

	// 重复关闭幂等：再次执行不报错也不改变结果喵。
	require.NoError(t, SetUserUpstreamFeatureOption(UserUpstreamSharingEnabledKey, "false"))
}

// TestSetUserUpstreamFeatureOptionMasterDisable 验证关闭总开关联动共享开关并清理全部上游引用的语义喵。
func TestSetUserUpstreamFeatureOptionMasterDisable(t *testing.T) {
	truncateTables(t)
	migrateFeatureTestTables(t)
	restore := saveFeatureOptionState(t, "true", "true")
	defer restore()

	upstream, _ := seedFeatureCleanupFixture(t)

	// 关闭总开关喵。
	require.NoError(t, SetUserUpstreamFeatureOption(UserUpstreamEnabledKey, "false"))

	// 上游数据行保留，但共享标志被清掉喵。
	var reloaded UserUpstreamModel
	require.NoError(t, DB.First(&reloaded, upstream.ID).Error)
	assert.False(t, reloaded.ShareEnabled)

	// 全部引用上游的自定义候选被删除喵。
	var candidateCount int64
	require.NoError(t, DB.Model(&VirtualModelCustomCandidate{}).Count(&candidateCount).Error)
	assert.Zero(t, candidateCount)

	// 总开关与共享开关都被联动置为关闭喵。
	assert.False(t, UserUpstreamFeatureEnabled())
	assert.False(t, UserUpstreamSharingFeatureEnabled())
}

// TestSetUserUpstreamFeatureOptionInvalidAndDependency 验证非法取值与"共享依赖总开关"的拒绝逻辑喵。
func TestSetUserUpstreamFeatureOptionInvalidAndDependency(t *testing.T) {
	truncateTables(t)
	migrateFeatureTestTables(t)
	restore := saveFeatureOptionState(t, "false", "false")
	defer restore()

	// 非法取值（非 true/false）被拒绝喵。
	require.Error(t, SetUserUpstreamFeatureOption(UserUpstreamEnabledKey, "yes"))

	// 总开关关闭时不允许单独开启共享喵。
	require.Error(t, SetUserUpstreamFeatureOption(UserUpstreamSharingEnabledKey, "true"))
}
