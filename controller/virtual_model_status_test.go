package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
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
		&model.EntityProbeState{}, &model.PerfMetric{}, &model.UserUpstreamModel{},
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
	require.NoError(t, model.DB.Exec("DELETE FROM user_upstream_models").Error)
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
	// 活跃请求：登记一个请求并推进到候选 1，供实时概览字段断言喵。
	requestID := middleware.EnterVirtualModelInflight(int64(virtualModel.ID), "virtual/vm-alpha")
	middleware.UpdateVirtualModelInflightCandidate(int64(virtualModel.ID), requestID, 1, "gpt-probe")
	t.Cleanup(func() {
		middleware.ExitVirtualModelInflight(int64(virtualModel.ID), requestID)
	})

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/virtual-models/"+fmt.Sprintf("%d", virtualModel.ID)+"/status", nil)
	ctx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", virtualModel.ID)}}
	ctx.Set("id", 7)
	GetVirtualModelStatus(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Success bool                      `json:"success"`
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
	// 实时概览：当前请求数与活跃调用链详情喵。
	require.Equal(t, int64(1), resp.Data.CurrentRequests)
	require.Len(t, resp.Data.ActiveRequests, 1)
	require.Equal(t, requestID, resp.Data.ActiveRequests[0].RequestID)
	require.Equal(t, 1, resp.Data.ActiveRequests[0].CandidateIndex)
	require.Equal(t, "gpt-probe", resp.Data.ActiveRequests[0].CandidateLabel)
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
		Success bool                               `json:"success"`
		Data    virtualModelCandidateStatusPayload `json:"data"`
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

// TestGetVirtualModelStatusResolvesReferencedUpstreamLabel 验证候选引用用户上游模型且直填真实模型名为空时，节点标签回退为 user/<name> 喵。
func TestGetVirtualModelStatusResolvesReferencedUpstreamLabel(t *testing.T) {
	setupVirtualModelStatusTestDB(t)

	// 属主 7 注册一个被虚拟模型候选引用的上游模型，其用户级名称为 user/ref-upstream 喵。
	referencedUpstream := &model.UserUpstreamModel{
		ID:             501,
		OwnerUserID:    7,
		NormalizedName: "ref-upstream",
		DisplayName:    "Ref Upstream",
		Enabled:        true,
	}
	require.NoError(t, model.DB.Create(referencedUpstream).Error)

	// 创建引用上述上游模型的自定义候选虚拟模型，直填真实模型名留空以触发回退解析喵。
	virtualModel := &model.VirtualModel{OwnerUserID: 7, NormalizedName: "vm-ref-custom", DisplayName: "vm-ref-custom", Enabled: true, Version: 1, TotalTimeoutSeconds: 120, MaxLoopRounds: 1}
	require.NoError(t, model.DB.Create(virtualModel).Error)
	candidate := &model.VirtualModelCandidate{VirtualModelID: virtualModel.ID, StableOrder: 0, SourceType: model.VirtualModelSourceCustom, Enabled: true, TimeoutSeconds: 60, Version: 1}
	require.NoError(t, model.DB.Create(candidate).Error)
	// 仅填引用上游编号，真实模型名与直填凭据留空；执行快照会从 custom 子表读到空名称与引用编号喵。
	upstreamModelID := referencedUpstream.ID
	require.NoError(t, model.DB.Create(&model.VirtualModelCustomCandidate{
		CandidateID:      candidate.ID,
		EncryptedBaseURL: "https://example.invalid/v1",
		EncryptedAPIKey:  "enc:test",
		RealModelName:    "",
		AuthStyle:        model.VirtualModelAuthBearer,
		UpstreamModelID:  &upstreamModelID,
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/virtual-models/"+fmt.Sprintf("%d", virtualModel.ID)+"/status", nil)
	ctx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", virtualModel.ID)}}
	ctx.Set("id", 7)
	GetVirtualModelStatus(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Success bool                      `json:"success"`
		Data    virtualModelStatusPayload `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	// 候选标签应回退为引用上游的用户级名称，而非空字符串喵。
	require.Len(t, resp.Data.Candidates, 1)
	require.Equal(t, "user/ref-upstream", resp.Data.Candidates[0].Label)
}

// TestGetPerfMetricsSharedBlacklistDenied 验证黑名单用户查询 user/<name> perf-metrics 被 404 拒绝喵。
// 回归 P1-⑥：此前 user/ 模型不做共享白/黑名单校验，黑名单用户可直接读共享维度可用性/延迟喵。
func TestGetPerfMetricsSharedBlacklistDenied(t *testing.T) {
	setupVirtualModelStatusTestDB(t)
	now := time.Now().Unix()
	// 属主 7 开启共享并用黑名单屏蔽用户 9 喵。
	upstreamModel := &model.UserUpstreamModel{
		OwnerUserID:     7,
		NormalizedName:  "perf-blacklist",
		DisplayName:     "Perf Blacklist",
		Enabled:         true,
		ShareEnabled:    true,
		ShareListMode:   "blacklist",
		ShareBlacklist:  "9",
		BalanceCents:    10000,
		AvailableCents:  10000,
		ShareLimitCents: 10000,
		Version:         1,
		CreatedTime:     now,
		UpdatedTime:     now,
	}
	require.NoError(t, model.DB.Create(upstreamModel).Error)
	// 共享维度落一条 perf 数据，模拟真实共享模型有可用性数据喵。
	require.NoError(t, model.DB.Create(&model.PerfMetric{
		ModelName: "user/perf-blacklist", Group: perfmetrics.EntityProbeGroupShared,
		BucketTs: now, RequestCount: 3, SuccessCount: 3, TotalLatencyMs: 300,
	}).Error)

	// 黑名单用户 9 查询共享维度 perf-metrics 必须被拒喵。
	deniedRecorder := httptest.NewRecorder()
	deniedCtx, _ := gin.CreateTestContext(deniedRecorder)
	deniedCtx.Request = httptest.NewRequest(http.MethodGet, "/api/perf-metrics?model=user/perf-blacklist&hours=24", nil)
	deniedCtx.Set("id", 9)
	GetPerfMetrics(deniedCtx)
	require.Equal(t, http.StatusNotFound, deniedRecorder.Code)

	// 非黑名单用户 10 可以正常查看共享维度喵。
	allowedRecorder := httptest.NewRecorder()
	allowedCtx, _ := gin.CreateTestContext(allowedRecorder)
	allowedCtx.Request = httptest.NewRequest(http.MethodGet, "/api/perf-metrics?model=user/perf-blacklist&hours=24", nil)
	allowedCtx.Set("id", 10)
	GetPerfMetrics(allowedCtx)
	require.Equal(t, http.StatusOK, allowedRecorder.Code)

	// 属主 7 总是可以查看自己的模型状态喵。
	ownerRecorder := httptest.NewRecorder()
	ownerCtx, _ := gin.CreateTestContext(ownerRecorder)
	ownerCtx.Request = httptest.NewRequest(http.MethodGet, "/api/perf-metrics?model=user/perf-blacklist&hours=24", nil)
	ownerCtx.Set("id", 7)
	GetPerfMetrics(ownerCtx)
	require.Equal(t, http.StatusOK, ownerRecorder.Code)
}
