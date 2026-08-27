package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// setupPerfMetricsTestDB 初始化独立内存库与确定性分组倍率，避免与其他 controller 测试串扰喵。
func setupPerfMetricsTestDB(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	common.RedisEnabled = false

	// 保存并随后恢复所有被改动的全局状态喵。
	originalIsMasterNode := common.IsMasterNode
	originalSQLitePath := common.SQLitePath
	originalMainType := common.MainDatabaseType()
	originalLogType := common.LogDatabaseType()
	originalSQLDSN, hadSQLDSN := os.LookupEnv("SQL_DSN")
	originalRatios := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		common.IsMasterNode = originalIsMasterNode
		common.SQLitePath = originalSQLitePath
		common.SetDatabaseTypes(originalMainType, originalLogType)
		if hadSQLDSN {
			require.NoError(t, os.Setenv("SQL_DSN", originalSQLDSN))
		} else {
			require.NoError(t, os.Unsetenv("SQL_DSN"))
		}
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
	})

	// 独立内存库配合 SQL_DSN=local，让 InitDB 走 SQLite 分支喵。
	common.IsMasterNode = false
	common.SQLitePath = fmt.Sprintf("file:%s?mode=memory&cache=shared", "perf_metrics_controller_test")
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, os.Setenv("SQL_DSN", "local"))
	// 固定分组倍率，确保 summary 白名单行为确定喵。
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1}`))

	require.NoError(t, model.InitDB())
	require.NoError(t, model.DB.AutoMigrate(&model.PerfMetric{}))
	require.NoError(t, model.DB.Exec("DELETE FROM perf_metrics").Error)
}

// seedPerfMetricRow 直接写入一条 perf_metrics 落库行，供接口层测试使用喵。
func seedPerfMetricRow(t *testing.T, modelName string, group string, bucketTs int64, requestCount int64, successCount int64, latencyMs int64) {
	t.Helper()
	require.NoError(t, model.DB.Create(&model.PerfMetric{
		ModelName:      modelName,
		Group:          group,
		BucketTs:       bucketTs,
		RequestCount:   requestCount,
		SuccessCount:   successCount,
		TotalLatencyMs: latencyMs,
	}).Error)
}

// perfSummaryPayload summary 接口响应中的可读字段喵。
type perfSummaryPayload struct {
	Success bool `json:"success"`
	Data    struct {
		Models []struct {
			ModelName    string  `json:"model_name"`
			AvgLatencyMs int64   `json:"avg_latency_ms"`
			SuccessRate  float64 `json:"success_rate"`
			RequestCount int64   `json:"request_count"`
		} `json:"models"`
	} `json:"data"`
}

// decodeSummary 解析 summary 响应并断言成功喵。
func decodeSummary(t *testing.T, recorder *httptest.ResponseRecorder) perfSummaryPayload {
	t.Helper()
	var payload perfSummaryPayload
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	return payload
}

// perfMetricsPayload per-model 接口响应中的可读字段喵。
type perfMetricsPayload struct {
	Success bool `json:"success"`
	Data    struct {
		ModelName string `json:"model_name"`
		Groups    []struct {
			Group        string `json:"group"`
			AvgLatencyMs int64  `json:"avg_latency_ms"`
		} `json:"groups"`
	} `json:"data"`
}

// decodePerfMetrics 解析 per-model 响应并断言成功喵。
func decodePerfMetrics(t *testing.T, recorder *httptest.ResponseRecorder) perfMetricsPayload {
	t.Helper()
	var payload perfMetricsPayload
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	return payload
}

// TestFilterActiveGroups 验证 perf 分组过滤的白名单与隐私规则喵。
func TestFilterActiveGroups(t *testing.T) {
	setupPerfMetricsTestDB(t)

	cases := []struct {
		name     string
		group    string
		wantKept bool
	}{
		{name: "auto always kept", group: "auto", wantKept: true},
		{name: "configured ratio kept", group: "default", wantKept: true},
		{name: "shared probe group kept", group: perfmetrics.EntityProbeGroupShared, wantKept: true},
		{name: "owner self probe group dropped", group: perfmetrics.EntityProbeGroupSelf, wantKept: false},
		{name: "unknown group dropped", group: "unknown", wantKept: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := filterActiveGroups([]perfmetrics.GroupResult{{Group: tc.group}})
			if tc.wantKept {
				require.Len(t, result, 1)
				require.Equal(t, tc.group, result[0].Group)
			} else {
				require.Empty(t, result)
			}
		})
	}
}

// TestGetPerfMetricsSummaryIncludeShared 验证 summary 接口的 include_shared 门控喵。
func TestGetPerfMetricsSummaryIncludeShared(t *testing.T) {
	setupPerfMetricsTestDB(t)
	nowBucket := time.Now().Unix()
	sharedModel := "user/perf-summary-shared"
	ghostModel := "ghost-self"

	// 共享维度行：仅 include_shared=1 时才纳入汇总喵。
	seedPerfMetricRow(t, sharedModel, perfmetrics.EntityProbeGroupShared, nowBucket, 2, 2, 200)
	// 属主自用行：即使 include_shared=1 也绝不能出现喵。
	seedPerfMetricRow(t, ghostModel, perfmetrics.EntityProbeGroupSelf, nowBucket, 5, 0, 500)

	// 不带参数：共享模型与幽灵自用模型都不返回喵。
	withoutRecorder := httptest.NewRecorder()
	withoutCtx, _ := gin.CreateTestContext(withoutRecorder)
	withoutCtx.Request = httptest.NewRequest(http.MethodGet, "/api/perf-metrics/summary?hours=24", nil)
	GetPerfMetricsSummary(withoutCtx)
	require.Equal(t, http.StatusOK, withoutRecorder.Code)
	withoutData := decodeSummary(t, withoutRecorder)
	require.NotContains(t, modelNamesOf(withoutData), sharedModel)
	require.NotContains(t, modelNamesOf(withoutData), ghostModel)

	// 带 include_shared=1：共享模型返回，幽灵自用模型仍不返回喵。
	withRecorder := httptest.NewRecorder()
	withCtx, _ := gin.CreateTestContext(withRecorder)
	withCtx.Request = httptest.NewRequest(http.MethodGet, "/api/perf-metrics/summary?hours=24&include_shared=1", nil)
	GetPerfMetricsSummary(withCtx)
	require.Equal(t, http.StatusOK, withRecorder.Code)
	withData := decodeSummary(t, withRecorder)
	require.Contains(t, modelNamesOf(withData), sharedModel)
	require.NotContains(t, modelNamesOf(withData), ghostModel)
}

// modelNamesOf 提取 summary 响应中的模型名列表喵。
func modelNamesOf(payload perfSummaryPayload) []string {
	names := make([]string, 0, len(payload.Data.Models))
	for _, m := range payload.Data.Models {
		names = append(names, m.ModelName)
	}
	return names
}

// TestGetPerfMetricsGroupMappingAndPrivacy 验证 per-model 接口的分组映射与自用隐私保护喵。
func TestGetPerfMetricsGroupMappingAndPrivacy(t *testing.T) {
	setupPerfMetricsTestDB(t)
	nowBucket := time.Now().Unix()
	sharedModel := "user/perf-map-shared"
	ghostModel := "ghost-map-self"

	// 共享维度行：应被映射为 user-shared 喵。
	seedPerfMetricRow(t, sharedModel, perfmetrics.EntityProbeGroupShared, nowBucket, 1, 1, 300)
	// 属主自用行：即使显式按分组查询也绝不放行喵。
	seedPerfMetricRow(t, ghostModel, perfmetrics.EntityProbeGroupSelf, nowBucket, 1, 0, 100)

	// 共享模型：分组被映射为 user-shared，且不泄露内部分组名喵。
	sharedRecorder := httptest.NewRecorder()
	sharedCtx, _ := gin.CreateTestContext(sharedRecorder)
	sharedCtx.Request = httptest.NewRequest(http.MethodGet, "/api/perf-metrics?model="+sharedModel+"&hours=24", nil)
	GetPerfMetrics(sharedCtx)
	require.Equal(t, http.StatusOK, sharedRecorder.Code)
	sharedData := decodePerfMetrics(t, sharedRecorder)
	require.Len(t, sharedData.Data.Groups, 1)
	require.Equal(t, constant.GroupUserShared, sharedData.Data.Groups[0].Group)
	require.Equal(t, int64(300), sharedData.Data.Groups[0].AvgLatencyMs)

	// 幽灵自用模型：即使显式指定 group=__entity_probe__ 也被剥掉喵。
	ghostRecorder := httptest.NewRecorder()
	ghostCtx, _ := gin.CreateTestContext(ghostRecorder)
	ghostCtx.Request = httptest.NewRequest(http.MethodGet, "/api/perf-metrics?model="+ghostModel+"&group="+perfmetrics.EntityProbeGroupSelf+"&hours=24", nil)
	GetPerfMetrics(ghostCtx)
	require.Equal(t, http.StatusOK, ghostRecorder.Code)
	ghostData := decodePerfMetrics(t, ghostRecorder)
	require.Empty(t, ghostData.Data.Groups)
}
