package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newUpstreamModelTestDB 构造带用户上游模型表结构的临时数据库喵。
func newUpstreamModelTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, testDB.AutoMigrate(&model.UserUpstreamModel{}))
	return testDB
}

// TestIsUserUpstreamModelRequest 验证 upstream/ 前缀识别喵。
func TestIsUserUpstreamModelRequest(t *testing.T) {
	// 带前缀的模型名应被识别为用户上游模型喵。
	require.True(t, isUserUpstreamModelRequest("upstream/alpha"))
	require.True(t, isUserUpstreamModelRequest("  upstream/alpha  "))

	// 普通模型名与虚拟模型名不应被误识别喵。
	require.False(t, isUserUpstreamModelRequest("gpt-4o"))
	require.False(t, isUserUpstreamModelRequest("virtual/alpha"))
	require.False(t, isUserUpstreamModelRequest(""))
}

// TestHandleUserUpstreamModelRequestAuth 验证用户上游模型授权边界喵。
func TestHandleUserUpstreamModelRequestAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 构造独立测试库，替换全局 DB 并在结束后恢复喵。
	testDB := newUpstreamModelTestDB(t)
	oldDB := model.DB
	model.DB = testDB
	defer func() { model.DB = oldDB }()

	// 构造一个启用与一个停用的上游模型喵。
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{OwnerUserID: 7, NormalizedName: "alpha", Enabled: true, EncryptedBaseURL: "enc", EncryptedAPIKey: "enc", RealModelName: "gpt-4o"}).Error)
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{OwnerUserID: 7, NormalizedName: "disabled", Enabled: false}).Error)

	// 模型不存在时返回统一 404，避免枚举资源喵。
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"upstream/missing"}`))
	ctx.Set("id", 7)
	handled := handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "upstream/missing"})
	require.False(t, handled)
	require.Equal(t, http.StatusNotFound, recorder.Code)

	// 停用模型同样返回 404，不暴露停用状态喵。
	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"upstream/disabled"}`))
	ctx.Set("id", 7)
	handled = handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "upstream/disabled"})
	require.False(t, handled)
	require.Equal(t, http.StatusNotFound, recorder.Code)

	// 跨用户访问隐藏资源存在性，同样 404 喵。
	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"upstream/alpha"}`))
	ctx.Set("id", 8)
	handled = handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "upstream/alpha"})
	require.False(t, handled)
	require.Equal(t, http.StatusNotFound, recorder.Code)

	// 无效模型名直接拒绝，不触发数据库查询喵。
	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"upstream/坏名"}`))
	ctx.Set("id", 7)
	handled = handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "upstream/坏名"})
	require.False(t, handled)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}
