package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newFailureLogTestDB 构造带 Log 表的内存库并替换全局 LOG_DB，供失败日志落库断言喵。
func newFailureLogTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, testDB.AutoMigrate(&model.Log{}))
	oldLogDB := model.LOG_DB
	model.LOG_DB = testDB
	t.Cleanup(func() {
		model.LOG_DB = oldLogDB
	})
	return testDB
}

// TestRecordVirtualModelOverallFailure 验证虚拟模型整体失败日志落库与防重喵。
func TestRecordVirtualModelOverallFailure(t *testing.T) {
	newProbeTestDB(t)
	testLogDB := newFailureLogTestDB(t)

	// 构造带请求体的虚拟模型上下文，模型名提前写入（对应 handleVirtualModelRequest 的提前设置）喵。
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"deepseek","messages":[{"role":"user","content":"你好"}]}`))
	common.SetContextKey(ctx, constant.ContextKeyVirtualModelName, "virtual/test")

	// 同一收尾路径重复调用必须被防重标记拦截，只落库一条喵。
	RecordVirtualModelOverallFailure(ctx, "virtual_model_unavailable", http.StatusServiceUnavailable)
	RecordVirtualModelOverallFailure(ctx, "virtual_model_unavailable", http.StatusServiceUnavailable)

	var count int64
	require.NoError(t, testLogDB.Model(&model.Log{}).Count(&count).Error)
	require.Equal(t, int64(1), count)

	// 校验日志字段：模型名、失败标记与错误分类喵。
	var logRecord model.Log
	require.NoError(t, testLogDB.Model(&model.Log{}).First(&logRecord).Error)
	require.Equal(t, "virtual/test", logRecord.ModelName)
	require.Contains(t, logRecord.Other, "final_success")
	require.Contains(t, logRecord.Other, "virtual_model_unavailable")
}

// TestRecordVirtualModelOverallFailureSkipsNonVirtual 验证非虚拟模型请求不产生失败日志喵。
func TestRecordVirtualModelOverallFailureSkipsNonVirtual(t *testing.T) {
	newProbeTestDB(t)
	testLogDB := newFailureLogTestDB(t)

	// 无虚拟模型名上下文：普通请求不应触发虚拟模型失败日志喵。
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	RecordVirtualModelOverallFailure(ctx, "upstream_server_error", http.StatusBadGateway)

	var count int64
	require.NoError(t, testLogDB.Model(&model.Log{}).Count(&count).Error)
	require.Equal(t, int64(0), count)
}
