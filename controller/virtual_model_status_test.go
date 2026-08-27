package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// setupVirtualModelStatusTestDB 初始化共享内存库并迁移虚拟模型状态检测所需表喵。
func setupVirtualModelStatusTestDB(t *testing.T) {
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

	common.IsMasterNode = false
	common.SQLitePath = fmt.Sprintf("file:%s?mode=memory&cache=shared", "virtual_model_status_test")
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, os.Setenv("SQL_DSN", "local"))
	require.NoError(t, model.InitDB())
	require.NoError(t, model.DB.AutoMigrate(
		&model.VirtualModel{}, &model.VirtualModelCandidate{}, &model.VirtualModelInternalCandidate{},
		&model.VirtualModelCustomCandidate{}, &model.VirtualModelFailureRule{}, &model.VirtualModelGlobalFailureRule{},
		&model.EntityProbeState{}, &model.PerfMetric{},
	))
	// 共享内存库跨用例存活，需清空全部相关表避免候选唯一索引残留冲突喵。
	require.NoError(t, model.DB.Exec("DELETE FROM virtual_model_internal_candidates").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM virtual_model_custom_candidates").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM virtual_model_candidates").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM virtual_model_failure_rules").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM virtual_model_global_failure_rules").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM virtual_models").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM entity_probe_states").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM perf_metrics").Error)
}

// createStatusTestVirtualModel 创建一个含单个 internal 候选的测试虚拟模型喵。
func createStatusTestVirtualModel(t *testing.T, ownerUserID int, normalizedName string) *model.VirtualModel {
	t.Helper()
	virtualModel := &model.VirtualModel{OwnerUserID: ownerUserID, NormalizedName: normalizedName, DisplayName: normalizedName, Enabled: true, Version: 1, TotalTimeoutSeconds: 120, MaxLoopRounds: 1}
	require.NoError(t, model.DB.Create(virtualModel).Error)
	candidate := &model.VirtualModelCandidate{VirtualModelID: virtualModel.ID, StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true, TimeoutSeconds: 60, Version: 1}
	require.NoError(t, model.DB.Create(candidate).Error)
	require.NoError(t, model.DB.Create(&model.VirtualModelInternalCandidate{CandidateID: candidate.ID, GroupName: "default", RealModelName: "gpt-probe"}).Error)
	return virtualModel
}

// TestGetVirtualModelStatusOwnerView 验证虚拟模型整体状态含节点摘要喵。
func TestGetVirtualModelStatusOwnerView(t *testing.T) {
	setupVirtualModelStatusTestDB(t)
	virtualModel := createStatusTestVirtualModel(t, 7, "vm-alpha")

	// 整体维度：一次成功一次失败喵。
	perfmetrics.RecordEntityProbe("virtual/vm-alpha", 200, true, perfmetrics.EntityProbeExtras{})
	perfmetrics.RecordEntityProbe("virtual/vm-alpha", 400, false, perfmetrics.EntityProbeExtras{})
	require.NoError(t, model.RecordEntityProbeCounted(model.EntityProbeScopeVirtual, int64(virtualModel.ID), 0, 7, 1000, true, 200, ""))
	require.NoError(t, model.RecordEntityProbeCounted(model.EntityProbeScopeVirtual, int64(virtualModel.ID), 0, 7, 2000, false, 400, "rate_limited"))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/virtual-models/"+fmt.Sprintf("%d", virtualModel.ID)+"/status", nil)
	ctx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", virtualModel.ID)}}
	ctx.Set("id", 7)
	GetVirtualModelStatus(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Success bool                     `json:"success"`
		Data    virtualModelStatusPayload `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	require.Equal(t, int64(2), resp.Data.RequestCount)
	require.InDelta(t, 50.0, resp.Data.Availability, 0.001)
	require.False(t, resp.Data.LastSuccess)
	require.Equal(t, "rate_limited", resp.Data.LastError)
	require.Equal(t, 1, resp.Data.CandidateCount)
	require.Equal(t, 1, resp.Data.EnabledCandidates)
	// 节点摘要有该候选且无数据时可用性为零喵。
	require.Len(t, resp.Data.Candidates, 1)
	require.Positive(t, resp.Data.Candidates[0].CandidateID)
	require.Equal(t, "gpt-probe", resp.Data.Candidates[0].Label)
}

// TestGetVirtualModelStatusAuth 验证非属主无法查看状态喵。
func TestGetVirtualModelStatusAuth(t *testing.T) {
	setupVirtualModelStatusTestDB(t)
	virtualModel := createStatusTestVirtualModel(t, 7, "vm-private")

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/virtual-models/"+fmt.Sprintf("%d", virtualModel.ID)+"/status", nil)
	ctx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", virtualModel.ID)}}
	ctx.Set("id", 8)
	GetVirtualModelStatus(ctx)
	require.Equal(t, http.StatusNotFound, recorder.Code)
}

// TestGetVirtualModelCandidateStatus 验证候选节点状态归属校验喵。
func TestGetVirtualModelCandidateStatus(t *testing.T) {
	setupVirtualModelStatusTestDB(t)
	virtualModel := createStatusTestVirtualModel(t, 7, "vm-beta")

	// 记录候选失败喵。
	perfmetrics.RecordEntityProbe("virtual/vm-beta/candidate/1", 350, false, perfmetrics.EntityProbeExtras{})
	require.NoError(t, model.RecordEntityProbeCounted(model.EntityProbeScopeVirtualCandidate, 1, int64(virtualModel.ID), 7, 1000, false, 350, "timeout"))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/virtual-models/"+fmt.Sprintf("%d", virtualModel.ID)+"/candidates/1/status", nil)
	ctx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", virtualModel.ID)}, {Key: "candidateId", Value: "1"}}
	ctx.Set("id", 7)
	GetVirtualModelCandidateStatus(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Success bool                                `json:"success"`
		Data    virtualModelCandidateStatusPayload  `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	require.Equal(t, 1, resp.Data.CandidateID)
	require.Equal(t, "gpt-probe", resp.Data.Label)
	require.Equal(t, int64(1), resp.Data.RequestCount)
	require.False(t, resp.Data.LastSuccess)
	require.Equal(t, "timeout", resp.Data.LastError)
}

// TestGetVirtualModelCandidateStatusForeignCandidate 验证候选不属于该模型按 404 处理喵。
func TestGetVirtualModelCandidateStatusForeignCandidate(t *testing.T) {
	setupVirtualModelStatusTestDB(t)
	virtualModel := createStatusTestVirtualModel(t, 7, "vm-gamma")

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/virtual-models/"+fmt.Sprintf("%d", virtualModel.ID)+"/candidates/999/status", nil)
	ctx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", virtualModel.ID)}, {Key: "candidateId", Value: "999"}}
	ctx.Set("id", 7)
	GetVirtualModelCandidateStatus(ctx)
	require.Equal(t, http.StatusNotFound, recorder.Code)
}
