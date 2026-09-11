package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setUserUpstreamEnabledOptionForAnomalyTest 测试期间设置用户自定上游总开关并在结束后恢复喵。
func setUserUpstreamEnabledOptionForAnomalyTest(t *testing.T, enabled bool) {
	t.Helper()
	// 喵~防御：OptionMap 可能尚未初始化，先补默认空映射避免写空指针喵。
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	oldValue := common.OptionMap[model.UserUpstreamEnabledKey]
	if enabled {
		common.OptionMap[model.UserUpstreamEnabledKey] = "true"
	} else {
		common.OptionMap[model.UserUpstreamEnabledKey] = "false"
	}
	t.Cleanup(func() { common.OptionMap[model.UserUpstreamEnabledKey] = oldValue })
}

// newAnomalyTestDB 构造候选被动变化测试需要的内存 SQLite 数据库喵。
func newAnomalyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	// 迁移扫描判定用到的表：虚拟模型链、上游模型与 abilities 喵。
	require.NoError(t, testDB.AutoMigrate(
		&model.VirtualModel{}, &model.VirtualModelCandidate{}, &model.VirtualModelInternalCandidate{},
		&model.VirtualModelCustomCandidate{}, &model.VirtualModelFailureRule{}, &model.VirtualModelGlobalFailureRule{},
		&model.UserUpstreamModel{}, &model.Ability{},
	))
	return testDB
}

// TestJudgeVirtualModelCandidateAnomalyInternalReasons 验证内部候选四类被动原因码的判定，
// 通过注入 usableGroups 与分组启模型缓存保证结果确定喵。
func TestJudgeVirtualModelCandidateAnomalyInternalReasons(t *testing.T) {
	// 可用分组只含 default，vip 分组已因降级/共享停止失去访问喵。
	usableGroups := map[string]string{"default": "默认分组"}
	// 分组启模型缓存：default 分组当前只有 gpt-4o，gpt-old 已被下架喵。
	enabledModelsByGroup := map[string]map[string]bool{
		"default": {"gpt-4o": true},
	}

	// 未配置分组：永远无法执行的配置缺陷喵。
	noGroupCandidate := &model.VirtualModelCandidateSnapshot{SourceType: model.VirtualModelSourceInternal, GroupName: "", RealModelName: "gpt-4o"}
	assert.Equal(t, virtualModelAnomalyGroupNotConfigured, judgeVirtualModelCandidateAnomaly(noGroupCandidate, 7, usableGroups, enabledModelsByGroup))

	// auto 分组：虚拟模型不支持自动分组喵。
	autoCandidate := &model.VirtualModelCandidateSnapshot{SourceType: model.VirtualModelSourceInternal, GroupName: "auto", RealModelName: "gpt-4o"}
	assert.Equal(t, virtualModelAnomalyAutoGroupNotSupported, judgeVirtualModelCandidateAnomaly(autoCandidate, 7, usableGroups, enabledModelsByGroup))

	// 分组降级：vip 分组已不可访问喵。
	lostGroupCandidate := &model.VirtualModelCandidateSnapshot{SourceType: model.VirtualModelSourceInternal, GroupName: "vip", RealModelName: "gpt-4o"}
	assert.Equal(t, virtualModelAnomalyGroupNotAccessible, judgeVirtualModelCandidateAnomaly(lostGroupCandidate, 7, usableGroups, enabledModelsByGroup))

	// 节点模型被删除：default 分组下已没有 gpt-old 这个模型喵。
	deletedModelCandidate := &model.VirtualModelCandidateSnapshot{SourceType: model.VirtualModelSourceInternal, GroupName: "default", RealModelName: "gpt-old"}
	assert.Equal(t, virtualModelAnomalyModelNotAvailable, judgeVirtualModelCandidateAnomaly(deletedModelCandidate, 7, usableGroups, enabledModelsByGroup))

	// 完全可用的内部候选：分组可访问且模型存在喵。
	healthyCandidate := &model.VirtualModelCandidateSnapshot{SourceType: model.VirtualModelSourceInternal, GroupName: "default", RealModelName: "gpt-4o"}
	assert.Empty(t, judgeVirtualModelCandidateAnomaly(healthyCandidate, 7, usableGroups, enabledModelsByGroup))
}

// TestJudgeVirtualModelCandidateAnomalyCustomUpstreamDisabled 验证自定义上游总开关关闭时
// 直填与引用候选一律标记不可用，开启时直填候选正常喵。
func TestJudgeVirtualModelCandidateAnomalyCustomUpstreamDisabled(t *testing.T) {
	usableGroups := map[string]string{}
	enabledModelsByGroup := map[string]map[string]bool{}

	// 直填候选：总开关关闭时不可用喵。
	directFillCandidate := &model.VirtualModelCandidateSnapshot{SourceType: model.VirtualModelSourceCustom, RealModelName: "gpt-direct"}
	setUserUpstreamEnabledOptionForAnomalyTest(t, false)
	assert.Equal(t, virtualModelAnomalyUpstreamDisabled, judgeVirtualModelCandidateAnomaly(directFillCandidate, 7, usableGroups, enabledModelsByGroup))

	// 引用候选：总开关关闭时同样不可用，且不触发上游存在性查询喵。
	upstreamID := int64(5)
	referenceCandidate := &model.VirtualModelCandidateSnapshot{SourceType: model.VirtualModelSourceCustom, UpstreamModelID: &upstreamID}
	assert.Equal(t, virtualModelAnomalyUpstreamDisabled, judgeVirtualModelCandidateAnomaly(referenceCandidate, 7, usableGroups, enabledModelsByGroup))

	// 总开关开启时直填候选可用喵。
	setUserUpstreamEnabledOptionForAnomalyTest(t, true)
	assert.Empty(t, judgeVirtualModelCandidateAnomaly(directFillCandidate, 7, usableGroups, enabledModelsByGroup))
}

// TestJudgeVirtualModelCandidateAnomalyReferencedUpstreamMissing 验证引用上游被删除后候选被标记喵。
func TestJudgeVirtualModelCandidateAnomalyReferencedUpstreamMissing(t *testing.T) {
	testDB := newAnomalyTestDB(t)
	originalDB := model.DB
	model.DB = testDB
	defer func() { model.DB = originalDB }()
	setUserUpstreamEnabledOptionForAnomalyTest(t, true)

	usableGroups := map[string]string{}
	enabledModelsByGroup := map[string]map[string]bool{}

	// 引用不存在的上游条目（9999 不存在于 user_upstream_models 表）喵。
	missingUpstreamID := int64(9999)
	missingCandidate := &model.VirtualModelCandidateSnapshot{SourceType: model.VirtualModelSourceCustom, UpstreamModelID: &missingUpstreamID}
	assert.Equal(t, virtualModelAnomalyReferencedUpstreamMissing, judgeVirtualModelCandidateAnomaly(missingCandidate, 7, usableGroups, enabledModelsByGroup))

	// 存在且归属当前用户的上游条目不算异常喵。
	existingUpstream := &model.UserUpstreamModel{
		OwnerUserID: 7, NormalizedName: "alpha", DisplayName: "Alpha", Enabled: true,
		EncryptedBaseURL: "encrypted-base-url", EncryptedAPIKey: "encrypted-api-key",
		RealModelName: "gpt-4o", AuthStyle: "bearer", Version: 1, CreatedTime: 100, UpdatedTime: 100,
	}
	require.NoError(t, testDB.Create(existingUpstream).Error)
	existingID := int64(existingUpstream.ID)
	existingCandidate := &model.VirtualModelCandidateSnapshot{SourceType: model.VirtualModelSourceCustom, UpstreamModelID: &existingID}
	assert.Empty(t, judgeVirtualModelCandidateAnomaly(existingCandidate, 7, usableGroups, enabledModelsByGroup))

	// 归属他人的上游条目对当前用户视同不存在，避免越权判定喵。
	otherOwnerUpstream := &model.UserUpstreamModel{
		OwnerUserID: 8, NormalizedName: "beta", DisplayName: "Beta", Enabled: true,
		EncryptedBaseURL: "encrypted-base-url", EncryptedAPIKey: "encrypted-api-key",
		RealModelName: "gpt-4o", AuthStyle: "bearer", Version: 1, CreatedTime: 100, UpdatedTime: 100,
	}
	require.NoError(t, testDB.Create(otherOwnerUpstream).Error)
	otherOwnerID := int64(otherOwnerUpstream.ID)
	otherOwnerCandidate := &model.VirtualModelCandidateSnapshot{SourceType: model.VirtualModelSourceCustom, UpstreamModelID: &otherOwnerID}
	assert.Equal(t, virtualModelAnomalyReferencedUpstreamMissing, judgeVirtualModelCandidateAnomaly(otherOwnerCandidate, 7, usableGroups, enabledModelsByGroup))
}

// TestScanVirtualModelCandidateAnomaliesAggregates 验证扫描函数按模型聚合返回异常清单喵。
// fixture 全部使用不依赖全局分组配置的异常类型，保证测试环境判定确定喵。
func TestScanVirtualModelCandidateAnomaliesAggregates(t *testing.T) {
	testDB := newAnomalyTestDB(t)
	originalDB := model.DB
	model.DB = testDB
	defer func() { model.DB = originalDB }()
	// 总开关关闭：自定义候选（直填或引用）一律不可用，扫描必然命中喵。
	setUserUpstreamEnabledOptionForAnomalyTest(t, false)

	// 模型 A：一个启用直填自定义候选 + 一个启用 internal 空分组候选 + 一个禁用候选（不应被扫描）喵。
	virtualModelA := &model.VirtualModel{OwnerUserID: 7, NormalizedName: "alpha", DisplayName: "Alpha", Enabled: true, CreatedTime: 100, UpdatedTime: 100}
	require.NoError(t, testDB.Create(virtualModelA).Error)
	directCandidate := &model.VirtualModelCandidate{VirtualModelID: virtualModelA.ID, StableOrder: 0, SourceType: model.VirtualModelSourceCustom, Enabled: true, CreatedTime: 100, UpdatedTime: 100}
	require.NoError(t, testDB.Create(directCandidate).Error)
	require.NoError(t, testDB.Create(&model.VirtualModelCustomCandidate{CandidateID: directCandidate.ID, EncryptedBaseURL: "enc", EncryptedAPIKey: "enc", RealModelName: "gpt-direct", AuthStyle: model.VirtualModelAuthBearer}).Error)
	emptyGroupCandidate := &model.VirtualModelCandidate{VirtualModelID: virtualModelA.ID, StableOrder: 1, SourceType: model.VirtualModelSourceInternal, Enabled: true, CreatedTime: 100, UpdatedTime: 100}
	require.NoError(t, testDB.Create(emptyGroupCandidate).Error)
	require.NoError(t, testDB.Create(&model.VirtualModelInternalCandidate{CandidateID: emptyGroupCandidate.ID, GroupName: "", RealModelName: "gpt-4o"}).Error)
	disabledCandidate := &model.VirtualModelCandidate{VirtualModelID: virtualModelA.ID, StableOrder: 2, SourceType: model.VirtualModelSourceInternal, Enabled: false, CreatedTime: 100, UpdatedTime: 100}
	require.NoError(t, testDB.Create(disabledCandidate).Error)
	require.NoError(t, testDB.Create(&model.VirtualModelInternalCandidate{CandidateID: disabledCandidate.ID, GroupName: "default", RealModelName: "gpt-4o"}).Error)

	// 模型 B：internal default 候选。测试环境 default 分组默认可访问，
	// 但 abilities 表为空（没有 gpt-4o 的可用渠道），命中「节点模型被删除」喵。
	virtualModelB := &model.VirtualModel{OwnerUserID: 7, NormalizedName: "beta", DisplayName: "Beta", Enabled: true, CreatedTime: 100, UpdatedTime: 100}
	require.NoError(t, testDB.Create(virtualModelB).Error)
	internalCandidate := &model.VirtualModelCandidate{VirtualModelID: virtualModelB.ID, StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true, CreatedTime: 100, UpdatedTime: 100}
	require.NoError(t, testDB.Create(internalCandidate).Error)
	require.NoError(t, testDB.Create(&model.VirtualModelInternalCandidate{CandidateID: internalCandidate.ID, GroupName: "default", RealModelName: "gpt-4o"}).Error)

	// 他人的模型不参与扫描喵。
	otherVirtualModel := &model.VirtualModel{OwnerUserID: 8, NormalizedName: "other", DisplayName: "Other", Enabled: true, CreatedTime: 100, UpdatedTime: 100}
	require.NoError(t, testDB.Create(otherVirtualModel).Error)

	anomalies, scanError := scanVirtualModelCandidateAnomalies(7, "")
	require.NoError(t, scanError)

	// 模型 A 命中两条（直填上游关闭 + 空分组），模型 B 命中一条（分组下模型被删除）喵。
	assert.Len(t, anomalies, 3)
	reasonCodes := make([]string, 0, len(anomalies))
	modelIDs := make([]int, 0, len(anomalies))
	for _, anomaly := range anomalies {
		reasonCodes = append(reasonCodes, anomaly.ReasonCode)
		modelIDs = append(modelIDs, anomaly.VirtualModelID)
	}
	assert.Contains(t, reasonCodes, virtualModelAnomalyUpstreamDisabled)
	assert.Contains(t, reasonCodes, virtualModelAnomalyGroupNotConfigured)
	assert.Contains(t, reasonCodes, virtualModelAnomalyModelNotAvailable)
	assert.Contains(t, modelIDs, virtualModelA.ID)
	assert.Contains(t, modelIDs, virtualModelB.ID)
	assert.NotContains(t, modelIDs, otherVirtualModel.ID)
	// 禁用候选不进入扫描，因此模型 A 的 directCandidate 与空分组候选的编号应出现，禁用候选编号不出现喵。
	anomalyCandidateIDs := make([]int, 0, len(anomalies))
	for _, anomaly := range anomalies {
		anomalyCandidateIDs = append(anomalyCandidateIDs, anomaly.CandidateID)
	}
	assert.Contains(t, anomalyCandidateIDs, directCandidate.ID)
	assert.Contains(t, anomalyCandidateIDs, emptyGroupCandidate.ID)
	assert.NotContains(t, anomalyCandidateIDs, disabledCandidate.ID)
}
