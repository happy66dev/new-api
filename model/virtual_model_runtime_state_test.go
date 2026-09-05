package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestDeleteVirtualModelCandidateRuntimeState 验证候选路由目标变更后的运行态清理范围喵。
// 必须清掉该候选绑定在候选 id 上的全部运行态，同时绝不误删其它候选、其它作用域、其它分组或用户配置喵。
func TestDeleteVirtualModelCandidateRuntimeState(t *testing.T) {
	// model 单元测试不经过 InitDB，手动按 sqlite 语义初始化保留字列引用，避免 group 过滤条件为空喵。
	commonGroupCol = "`group`"
	// 使用独立内存数据库，避免运行态清理测试污染全局连接喵。
	database, openError := gorm.Open(sqlite.Open("file:virtual-model-runtime-state-clear-test?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, openError)
	// 测试结束后关闭临时数据库连接，避免资源泄漏喵。
	t.Cleanup(func() {
		sqlDatabase, closeError := database.DB()
		if closeError == nil {
			_ = sqlDatabase.Close()
		}
	})
	// 迁移候选运行态清理涉及的整表集合喵。
	require.NoError(t, database.AutoMigrate(&VirtualModelInternalFreezeState{}, &VirtualModelManualFreeze{}, &EntityProbeState{}, &PerfMetric{}, &VirtualModelCustomFreezeState{}, &VirtualModelFailureRule{}))

	// 构造被清理候选（编号 13、属主 7、模型名 virtual/alpha）的全部运行态与对照样本喵。
	require.NoError(t, database.Create(&VirtualModelInternalFreezeState{OwnerUserID: 7, CandidateID: 13, FrozenUntil: 999, ConsecutiveFails: 3, LastFailureClass: "upstream_server_error", UpdatedTime: 900}).Error)
	require.NoError(t, database.Create(&VirtualModelManualFreeze{CandidateID: 13, OperatorID: 7, StartedAt: 800, ExpiresAt: 999}).Error)
	require.NoError(t, database.Create(&EntityProbeState{Scope: EntityProbeScopeVirtualCandidate, EntityID: 13, VirtualID: 9, OwnerUserID: 7, LastAt: 900, LastSuccess: false, RequestCount: 5, SuccessCount: 2}).Error)
	require.NoError(t, database.Create(&PerfMetric{ModelName: "virtual/alpha/candidate/13", Group: EntityProbeSelfGroupName, BucketTs: 700, RequestCount: 5, SuccessCount: 2, TotalLatencyMs: 5000}).Error)

	// 对照样本：不应被误删的同库数据喵。
	require.NoError(t, database.Create(&VirtualModelInternalFreezeState{OwnerUserID: 7, CandidateID: 14, FrozenUntil: 999, UpdatedTime: 900}).Error)
	require.NoError(t, database.Create(&VirtualModelInternalFreezeState{OwnerUserID: 8, CandidateID: 13, FrozenUntil: 999, UpdatedTime: 900}).Error)
	require.NoError(t, database.Create(&VirtualModelManualFreeze{CandidateID: 14, OperatorID: 7, StartedAt: 800, ExpiresAt: 999}).Error)
	require.NoError(t, database.Create(&EntityProbeState{Scope: EntityProbeScopeVirtualCandidate, EntityID: 14, VirtualID: 9, OwnerUserID: 7, LastAt: 900, LastSuccess: true, RequestCount: 1, SuccessCount: 1}).Error)
	require.NoError(t, database.Create(&EntityProbeState{Scope: EntityProbeScopeVirtual, EntityID: 9, VirtualID: 0, OwnerUserID: 7, LastAt: 900, LastSuccess: false, RequestCount: 6, SuccessCount: 2}).Error)
	require.NoError(t, database.Create(&EntityProbeState{Scope: EntityProbeScopeUpstream, EntityID: 13, VirtualID: 0, OwnerUserID: 7, LastAt: 900, LastSuccess: true, RequestCount: 1, SuccessCount: 1}).Error)
	require.NoError(t, database.Create(&PerfMetric{ModelName: "virtual/alpha/candidate/14", Group: EntityProbeSelfGroupName, BucketTs: 700, RequestCount: 1, SuccessCount: 1, TotalLatencyMs: 100}).Error)
	require.NoError(t, database.Create(&PerfMetric{ModelName: "virtual/alpha", Group: EntityProbeSelfGroupName, BucketTs: 700, RequestCount: 6, SuccessCount: 2, TotalLatencyMs: 6000}).Error)
	require.NoError(t, database.Create(&PerfMetric{ModelName: "virtual/alpha/candidate/13", Group: EntityProbeSharedGroupName, BucketTs: 700, RequestCount: 1, SuccessCount: 1, TotalLatencyMs: 100}).Error)
	require.NoError(t, database.Create(&VirtualModelCustomFreezeState{OwnerUserID: 7, IdentityDigest: "shared-identity", FrozenUntil: 999, ConsecutiveFails: 2, UpdatedTime: 900}).Error)
	require.NoError(t, database.Create(&VirtualModelFailureRule{CandidateID: 13, RuleOrder: 0, HTTPStatus: 429, Action: VirtualModelActionNext}).Error)

	// 执行目标变更重置：清空候选 13 在属主 7 与模型 virtual/alpha 下的运行态喵。
	require.NoError(t, DeleteVirtualModelCandidateRuntimeState(database, 7, "virtual/alpha", 13))

	// 目标行的内部自动冻结与手动冻结必须被删除喵。
	assertCandidateRuntimeRowDeleted(t, database, &VirtualModelInternalFreezeState{}, "owner_user_id = ? AND candidate_id = ?", 7, 13)
	assertCandidateRuntimeRowDeleted(t, database, &VirtualModelManualFreeze{}, "candidate_id = ?", 13)
	// 候选探测状态行与被清理候选的 perf 候选桶必须被删除喵。
	assertCandidateRuntimeRowDeleted(t, database, &EntityProbeState{}, "scope = ? AND entity_id = ?", EntityProbeScopeVirtualCandidate, 13)
	assertCandidateRuntimeRowDeleted(t, database, &PerfMetric{}, "model_name = ? AND "+commonGroupCol+" = ?", "virtual/alpha/candidate/13", EntityProbeSelfGroupName)

	// 对照样本必须完整保留：别的候选、别的属主、整体/上游作用域、共享分组与用户配置都不该被动喵。
	require.Equal(t, int64(1), countModelRows(database, &VirtualModelInternalFreezeState{}, "candidate_id = ? AND owner_user_id = ?", 14, 7))
	require.Equal(t, int64(1), countModelRows(database, &VirtualModelInternalFreezeState{}, "candidate_id = ? AND owner_user_id = ?", 13, 8))
	require.Equal(t, int64(1), countModelRows(database, &VirtualModelManualFreeze{}, "candidate_id = ?", 14))
	require.Equal(t, int64(1), countModelRows(database, &EntityProbeState{}, "scope = ? AND entity_id = ?", EntityProbeScopeVirtualCandidate, 14))
	require.Equal(t, int64(1), countModelRows(database, &EntityProbeState{}, "scope = ? AND entity_id = ?", EntityProbeScopeVirtual, 9))
	require.Equal(t, int64(1), countModelRows(database, &EntityProbeState{}, "scope = ? AND entity_id = ?", EntityProbeScopeUpstream, 13))
	require.Equal(t, int64(1), countModelRows(database, &PerfMetric{}, "model_name = ? AND "+commonGroupCol+" = ?", "virtual/alpha/candidate/14", EntityProbeSelfGroupName))
	require.Equal(t, int64(1), countModelRows(database, &PerfMetric{}, "model_name = ? AND "+commonGroupCol+" = ?", "virtual/alpha", EntityProbeSelfGroupName))
	require.Equal(t, int64(1), countModelRows(database, &PerfMetric{}, "model_name = ? AND "+commonGroupCol+" = ?", "virtual/alpha/candidate/13", EntityProbeSharedGroupName))
	require.Equal(t, int64(1), countModelRows(database, &VirtualModelCustomFreezeState{}, "owner_user_id = ? AND identity_digest = ?", 7, "shared-identity"))
	require.Equal(t, int64(1), countModelRows(database, &VirtualModelFailureRule{}, "candidate_id = ?", 13))

	// 幂等性：对已清空的候选再次调用仍成功且无副作用喵。
	require.NoError(t, DeleteVirtualModelCandidateRuntimeState(database, 7, "virtual/alpha", 13))
	require.Equal(t, int64(0), countModelRows(database, &VirtualModelInternalFreezeState{}, "candidate_id = ? AND owner_user_id = ?", 13, 7))
}

// TestDeleteVirtualModelCandidateRuntimeStateGuards 验证清理 helper 的防御分支喵。
func TestDeleteVirtualModelCandidateRuntimeStateGuards(t *testing.T) {
	// 空数据库连接必须返回错误，避免调用方把空操作误当成功喵。
	err := DeleteVirtualModelCandidateRuntimeState(nil, 7, "virtual/alpha", 13)
	require.Error(t, err)
	// 非法属主或候选编号按无操作处理，返回 nil 且不抛错喵。
	require.NoError(t, DeleteVirtualModelCandidateRuntimeState(&gorm.DB{}, 0, "virtual/alpha", 13))
	require.NoError(t, DeleteVirtualModelCandidateRuntimeState(&gorm.DB{}, 7, "", 13))
	require.NoError(t, DeleteVirtualModelCandidateRuntimeState(&gorm.DB{}, 7, "virtual/alpha", 0))
}

// assertCandidateRuntimeRowDeleted 断言指定条件的目标行在库中已不存在喵。
func assertCandidateRuntimeRowDeleted(t *testing.T, database *gorm.DB, modelValue any, query string, args ...any) {
	t.Helper()
	// 空连接或无效模型值直接失败，避免空指针掩盖真实结果喵。
	if database == nil || modelValue == nil {
		t.Fatal("assertCandidateRuntimeRowDeleted requires non-nil database and model value")
	}
	// 逐行查询目标，存在则说明清理失败喵。
	result := database.Model(modelValue).Where(query, args...).First(modelValue)
	require.ErrorIs(t, result.Error, gorm.ErrRecordNotFound)
}

// countModelRows 统计数据库中满足条件的目标行数，供「不应误删」断言使用喵。
func countModelRows(database *gorm.DB, modelValue any, query string, args ...any) int64 {
	if database == nil || modelValue == nil {
		return -1
	}
	// 执行条件计数并返回行数喵。
	var rowCount int64
	if database.Model(modelValue).Where(query, args...).Count(&rowCount).Error != nil {
		return -1
	}
	return rowCount
}
