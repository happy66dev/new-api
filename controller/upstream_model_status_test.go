package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// setupUpstreamStatusTestDB 初始化共享内存库并迁移状态检测所需表喵。
func setupUpstreamStatusTestDB(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	common.RedisEnabled = false

	// 保存并恢复所有被改动的全局状态，避免影响同包其他测试喵。
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

	// 内存共享缓存库配合 SQL_DSN=local，让 InitDB 走 SQLite 分支并初始化列名喵。
	common.IsMasterNode = false
	common.SQLitePath = fmt.Sprintf("file:%s?mode=memory&cache=shared", "upstream_status_test")
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, os.Setenv("SQL_DSN", "local"))
	require.NoError(t, model.InitDB())
	require.NoError(t, model.DB.AutoMigrate(&model.UserUpstreamModel{}, &model.EntityProbeState{}, &model.PerfMetric{}))
	require.NoError(t, model.DB.Exec("DELETE FROM user_upstream_models").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM entity_probe_states").Error)
}

// createStatusTestUpstreamModel 创建一个可共享的测试上游模型喵。
func createStatusTestUpstreamModel(t *testing.T, ownerUserID int, normalizedName string, shareEnabled bool) *model.UserUpstreamModel {
	t.Helper()
	created := &model.UserUpstreamModel{
		OwnerUserID:       ownerUserID,
		NormalizedName:    normalizedName,
		DisplayName:       normalizedName,
		Enabled:           true,
		EncryptedBaseURL:  "enc",
		EncryptedAPIKey:   "enc",
		RealModelName:     "gpt-4o",
		BalanceCents:      5000,
		AvailableCents:    5000,
		ShareEnabled:      shareEnabled,
		ShareLimitCents:   1000,
	}
	require.NoError(t, model.DB.Create(created).Error)
	return created
}

// TestGetUserUpstreamModelStatusOwnerView 验证属主视角状态包含自用统计与共享维度喵。
func TestGetUserUpstreamModelStatusOwnerView(t *testing.T) {
	setupUpstreamStatusTestDB(t)
	upstreamModel := createStatusTestUpstreamModel(t, 7, "alpha", true)

	// 属主自用维度：一次成功一次失败喵。
	perfmetrics.RecordEntityProbe("user/alpha", 200, true)
	perfmetrics.RecordEntityProbe("user/alpha", 400, false)
	require.NoError(t, model.RecordEntityProbeCounted(model.EntityProbeScopeUpstream, upstreamModel.ID, 0, 7, 1000, true, 200, ""))
	require.NoError(t, model.RecordEntityProbeCounted(model.EntityProbeScopeUpstream, upstreamModel.ID, 0, 7, 2000, false, 400, "rate_limited"))

	// 共享维度：一次成功喵。
	perfmetrics.RecordEntityProbeShared("user/alpha", 300, true)
	require.NoError(t, model.RecordEntityProbeCounted(model.EntityProbeScopeUpstreamShared, upstreamModel.ID, 0, 7, 3000, true, 300, ""))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/upstream-models/"+fmt.Sprintf("%d", upstreamModel.ID)+"/status?include_shared=true", nil)
	ctx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", upstreamModel.ID)}}
	ctx.Set("id", 7)
	GetUserUpstreamModelStatus(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Success bool                       `json:"success"`
		Data    upstreamModelStatusPayload `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	require.Equal(t, int64(2), resp.Data.RequestCount)
	require.InDelta(t, 50.0, resp.Data.Availability, 0.001)
	require.Equal(t, int64(300), resp.Data.AvgLatencyMs)
	require.False(t, resp.Data.LastSuccess)
	require.Equal(t, "rate_limited", resp.Data.LastError)
	// 属主可查看共享调用维度喵。
	require.NotNil(t, resp.Data.Shared)
	require.Equal(t, int64(1), resp.Data.Shared.RequestCount)
	require.InDelta(t, 100.0, resp.Data.Shared.Availability, 0.001)
	// 共享维度聚合内不携带错误明细喵。
	require.Empty(t, resp.Data.Shared.LastError)
}

// TestGetUserUpstreamModelStatusAuth 验证非属主无法查看属主状态喵。
func TestGetUserUpstreamModelStatusAuth(t *testing.T) {
	setupUpstreamStatusTestDB(t)
	upstreamModel := createStatusTestUpstreamModel(t, 7, "alpha", false)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/upstream-models/"+fmt.Sprintf("%d", upstreamModel.ID)+"/status", nil)
	ctx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", upstreamModel.ID)}}
	ctx.Set("id", 8)
	GetUserUpstreamModelStatus(ctx)
	require.Equal(t, http.StatusNotFound, recorder.Code)
}

// TestGetSharedUserUpstreamModelStatus 验证共享使用者视角只返回聚合且无错误明细喵。
func TestGetSharedUserUpstreamModelStatus(t *testing.T) {
	setupUpstreamStatusTestDB(t)
	// 使用独立模型名，避免全局内存热桶与属主用例的样本串扰喵。
	upstreamModel := createStatusTestUpstreamModel(t, 7, "beta", true)

	// 共享维度：一次成功一次失败喵。
	perfmetrics.RecordEntityProbeShared("user/beta", 300, true)
	perfmetrics.RecordEntityProbeShared("user/beta", 500, false)
	require.NoError(t, model.RecordEntityProbeCounted(model.EntityProbeScopeUpstreamShared, upstreamModel.ID, 0, 7, 3000, true, 300, ""))
	require.NoError(t, model.RecordEntityProbeCounted(model.EntityProbeScopeUpstreamShared, upstreamModel.ID, 0, 7, 4000, false, 500, "rate_limited"))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/upstream-models/shared/beta/status", nil)
	ctx.Params = gin.Params{{Key: "name", Value: "beta"}}
	ctx.Set("id", 8)
	GetSharedUserUpstreamModelStatus(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	// JSON 数字解码为 float64，按数值比较喵。
	require.Equal(t, float64(2), resp.Data["request_count"])
	require.InDelta(t, 50.0, resp.Data["availability"].(float64), 0.001)
	require.False(t, resp.Data["last_success"].(bool))
	// 共享聚合响应绝不携带 last_error 喵。
	_, hasError := resp.Data["last_error"]
	require.False(t, hasError)
	_, hasAvailability24 := resp.Data["availability_24h"]
	require.False(t, hasAvailability24)
}

// TestGetSharedUserUpstreamModelStatusNotShared 验证不在共享中的模型按 404 处理喵。
func TestGetSharedUserUpstreamModelStatusNotShared(t *testing.T) {
	setupUpstreamStatusTestDB(t)
	createStatusTestUpstreamModel(t, 7, "private", false)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/upstream-models/shared/private/status", nil)
	ctx.Set("id", 8)
	GetSharedUserUpstreamModelStatus(ctx)
	require.Equal(t, http.StatusNotFound, recorder.Code)
}

// TestGetSharedUserUpstreamModelStatusEmptyName 验证空名称按 404 处理喵。
func TestGetSharedUserUpstreamModelStatusEmptyName(t *testing.T) {
	setupUpstreamStatusTestDB(t)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/upstream-models/shared/%20/status", nil)
	ctx.Set("id", 8)
	// 手动设置空白 name 参数，模拟空名称请求喵。
	ctx.Params = gin.Params{{Key: "name", Value: strings.TrimSpace(" ")}}
	GetSharedUserUpstreamModelStatus(ctx)
	require.Equal(t, http.StatusNotFound, recorder.Code)
}
