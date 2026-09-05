package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// setupVirtualModelCandidateResetTestDB 初始化候选链保存测试的共享内存库并迁移相关表喵。
func setupVirtualModelCandidateResetTestDB(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	common.RedisEnabled = false

	// 保存并恢复所有被改动的全局状态喵。
	originalIsMasterNode := common.IsMasterNode
	originalSQLitePath := common.SQLitePath
	originalMainType := common.MainDatabaseType()
	originalLogType := common.LogDatabaseType()
	originalSQLDSN, hadSQLDSN := os.LookupEnv("SQL_DSN")
	t.Cleanup(func() {
		common.IsMasterNode = originalIsMasterNode
		common.SQLitePath = originalSQLitePath
		common.SetDatabaseTypes(originalMainType, originalLogType)
		if hadSQLDSN {
			require.NoError(t, os.Setenv("SQL_DSN", originalSQLDSN))
		} else {
			require.NoError(t, os.Unsetenv("SQL_DSN"))
		}
	})

	// 用独立命名内存库初始化全局连接，保证候选保存相关测试互不污染喵。
	common.IsMasterNode = false
	common.SQLitePath = fmt.Sprintf("file:%s?mode=memory&cache=shared", "virtual_model_candidate_reset_test")
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, os.Setenv("SQL_DSN", "local"))
	require.NoError(t, model.InitDB())
	require.NoError(t, model.DB.AutoMigrate(
		&model.VirtualModel{}, &model.VirtualModelCandidate{}, &model.VirtualModelInternalCandidate{},
		&model.VirtualModelCustomCandidate{}, &model.VirtualModelFailureRule{}, &model.VirtualModelGlobalFailureRule{},
		&model.VirtualModelTokenBinding{}, &model.VirtualModelManualFreeze{}, &model.VirtualModelInternalFreezeState{},
		&model.VirtualModelCustomFreezeState{}, &model.VirtualModelAuditLog{}, &model.EntityProbeState{}, &model.PerfMetric{},
	))
	// 共享内存库跨用例存活，按表名逐一清空避免主键或唯一索引残留冲突喵。
	clearTables := []string{
		"virtual_models", "virtual_model_candidates", "virtual_model_internal_candidates",
		"virtual_model_custom_candidates", "virtual_model_failure_rules", "virtual_model_global_failure_rules",
		"virtual_model_token_bindings", "virtual_model_manual_freezes", "virtual_model_internal_freeze_states",
		"virtual_model_custom_freeze_states", "virtual_model_audit_logs", "entity_probe_states", "perf_metrics",
	}
	for _, tableName := range clearTables {
		require.NoError(t, model.DB.Exec("DELETE FROM "+tableName).Error)
	}
}

// createResetTestInternalCandidate 创建单候选内部虚拟模型并返回模型与候选喵。
func createResetTestInternalCandidate(t *testing.T, ownerUserID int, normalizedName string, realModelName string) (*model.VirtualModel, *model.VirtualModelCandidate) {
	t.Helper()
	virtualModel := &model.VirtualModel{OwnerUserID: ownerUserID, NormalizedName: normalizedName, DisplayName: normalizedName, Enabled: true, Version: 1, TotalTimeoutSeconds: 120, MaxLoopRounds: 1}
	require.NoError(t, model.DB.Create(virtualModel).Error)
	candidate := &model.VirtualModelCandidate{VirtualModelID: virtualModel.ID, StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true, TimeoutSeconds: 60, Version: 1}
	require.NoError(t, model.DB.Create(candidate).Error)
	require.NoError(t, model.DB.Create(&model.VirtualModelInternalCandidate{CandidateID: candidate.ID, GroupName: "default", RealModelName: realModelName}).Error)
	return virtualModel, candidate
}

// TestReplaceVirtualModelCandidatesResetsStateOnTargetChange 验证把内部候选真实模型 A 改成 B 时，
// 旧目标（A）累积到候选 id 上的冻结与探测状态被清除，而失败规则等用户配置被保留喵。
func TestReplaceVirtualModelCandidatesResetsStateOnTargetChange(t *testing.T) {
	setupVirtualModelCandidateResetTestDB(t)
	virtualModel, candidate := createResetTestInternalCandidate(t, 7, "vm-target-change", "model-a")

	// 预置旧目标 A 时期的运行态：内部自动冻结、手动冻结、候选探测行与 perf 候选桶喵。
	require.NoError(t, model.DB.Create(&model.VirtualModelInternalFreezeState{OwnerUserID: 7, CandidateID: candidate.ID, FrozenUntil: 9999999999, ConsecutiveFails: 3, LastFailureClass: "upstream_server_error", UpdatedTime: 1000}).Error)
	require.NoError(t, model.DB.Create(&model.VirtualModelManualFreeze{CandidateID: candidate.ID, OperatorID: 7, StartedAt: 900, ExpiresAt: 9999999999}).Error)
	require.NoError(t, model.DB.Create(&model.EntityProbeState{Scope: model.EntityProbeScopeVirtualCandidate, EntityID: int64(candidate.ID), VirtualID: int64(virtualModel.ID), OwnerUserID: 7, LastAt: 1000, LastSuccess: false, LastError: "upstream_server_error", RequestCount: 4, SuccessCount: 1}).Error)
	require.NoError(t, model.DB.Create(&model.PerfMetric{ModelName: fmt.Sprintf("virtual/vm-target-change/candidate/%d", candidate.ID), Group: model.EntityProbeSelfGroupName, BucketTs: 900, RequestCount: 4, SuccessCount: 1, TotalLatencyMs: 4000}).Error)
	// 预置一条失败规则，验证用户配置在目标变更后仍然保留喵。
	require.NoError(t, model.DB.Create(&model.VirtualModelFailureRule{CandidateID: candidate.ID, RuleOrder: 0, HTTPStatus: 429, Action: model.VirtualModelActionNext}).Error)

	// 构造把真实模型从 model-a 改成 model-b 的候选链保存请求喵。
	replaceBody, marshalError := common.Marshal(map[string]any{
		"version": 1,
		"candidates": []map[string]any{{
			"id": candidate.ID, "source_type": string(model.VirtualModelSourceInternal),
			"enabled": true, "group_name": "default", "real_model_name": "model-b", "timeout_seconds": 60,
		}},
	})
	require.NoError(t, marshalError)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/virtual-models/%d/candidates", virtualModel.ID), bytes.NewReader(replaceBody))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", virtualModel.ID)}}
	ctx.Set("id", 7)
	ReplaceVirtualModelCandidates(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)

	// 来源配置必须已切到模型 B 喵。
	var storedInternal model.VirtualModelInternalCandidate
	require.NoError(t, model.DB.Where("candidate_id = ?", candidate.ID).First(&storedInternal).Error)
	require.Equal(t, "model-b", storedInternal.RealModelName)

	// 旧目标 A 的运行态必须被清除喵。
	require.Equal(t, int64(0), resetTestCount(t, &model.VirtualModelInternalFreezeState{}, "owner_user_id = ? AND candidate_id = ?", 7, candidate.ID))
	require.Equal(t, int64(0), resetTestCount(t, &model.VirtualModelManualFreeze{}, "candidate_id = ?", candidate.ID))
	require.Equal(t, int64(0), resetTestCount(t, &model.EntityProbeState{}, "scope = ? AND entity_id = ?", model.EntityProbeScopeVirtualCandidate, candidate.ID))
	require.Equal(t, int64(0), resetTestCount(t, &model.PerfMetric{}, "model_name = ?", fmt.Sprintf("virtual/vm-target-change/candidate/%d", candidate.ID)))

	// 用户配置的失败规则必须保留，模型版本已推进喵。
	require.Equal(t, int64(1), resetTestCount(t, &model.VirtualModelFailureRule{}, "candidate_id = ?", candidate.ID))
	var updatedVirtualModel model.VirtualModel
	require.NoError(t, model.DB.First(&updatedVirtualModel, virtualModel.ID).Error)
	require.Equal(t, int64(2), updatedVirtualModel.Version)
}

// TestReplaceVirtualModelCandidatesKeepsStateOnReordering 验证仅重排/重存相同目标不清空运行态喵。
func TestReplaceVirtualModelCandidatesKeepsStateOnReordering(t *testing.T) {
	setupVirtualModelCandidateResetTestDB(t)
	virtualModel, candidate := createResetTestInternalCandidate(t, 7, "vm-noop-resave", "model-a")

	// 预置一个不会被清空的内部自动冻结与探测行喵。
	require.NoError(t, model.DB.Create(&model.VirtualModelInternalFreezeState{OwnerUserID: 7, CandidateID: candidate.ID, FrozenUntil: 9999999999, ConsecutiveFails: 2, LastFailureClass: "timeout", UpdatedTime: 1000}).Error)
	require.NoError(t, model.DB.Create(&model.EntityProbeState{Scope: model.EntityProbeScopeVirtualCandidate, EntityID: int64(candidate.ID), VirtualID: int64(virtualModel.ID), OwnerUserID: 7, LastAt: 1000, LastSuccess: false, LastError: "timeout", RequestCount: 2, SuccessCount: 0}).Error)

	// 提交与现有一致的候选链（目标仍是 model-a/default，仅触发一次重存）喵。
	replaceBody, marshalError := common.Marshal(map[string]any{
		"version": 1,
		"candidates": []map[string]any{{
			"id": candidate.ID, "source_type": string(model.VirtualModelSourceInternal),
			"enabled": true, "group_name": "default", "real_model_name": "model-a", "timeout_seconds": 60,
		}},
	})
	require.NoError(t, marshalError)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/virtual-models/%d/candidates", virtualModel.ID), bytes.NewReader(replaceBody))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", virtualModel.ID)}}
	ctx.Set("id", 7)
	ReplaceVirtualModelCandidates(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)

	// 路由目标未变化时，既有冻结与探测状态必须原样保留喵。
	require.Equal(t, int64(1), resetTestCount(t, &model.VirtualModelInternalFreezeState{}, "owner_user_id = ? AND candidate_id = ?", 7, candidate.ID))
	require.Equal(t, int64(1), resetTestCount(t, &model.EntityProbeState{}, "scope = ? AND entity_id = ?", model.EntityProbeScopeVirtualCandidate, candidate.ID))
}

// resetTestCount 统计测试库中满足条件的目标行数喵。
func resetTestCount(t *testing.T, modelValue any, query string, args ...any) int64 {
	t.Helper()
	if modelValue == nil {
		t.Fatal("resetTestCount requires a non-nil model value")
	}
	var rowCount int64
	require.NoError(t, model.DB.Model(modelValue).Where(query, args...).Count(&rowCount).Error)
	return rowCount
}
