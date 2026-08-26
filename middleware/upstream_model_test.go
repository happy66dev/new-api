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

// TestIsUserUpstreamModelRequest 验证 user/ 前缀识别喵。
func TestIsUserUpstreamModelRequest(t *testing.T) {
	// 带前缀的模型名应被识别为用户上游模型喵。
	require.True(t, isUserUpstreamModelRequest("user/alpha"))
	require.True(t, isUserUpstreamModelRequest("  user/alpha  "))

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
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/missing"}`))
	ctx.Set("id", 7)
	handled := handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/missing"})
	require.False(t, handled)
	require.Equal(t, http.StatusNotFound, recorder.Code)

	// 停用模型同样返回 404，不暴露停用状态喵。
	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/disabled"}`))
	ctx.Set("id", 7)
	handled = handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/disabled"})
	require.False(t, handled)
	require.Equal(t, http.StatusNotFound, recorder.Code)

	// 跨用户访问隐藏资源存在性，同样 404 喵。
	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/alpha"}`))
	ctx.Set("id", 8)
	handled = handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/alpha"})
	require.False(t, handled)
	require.Equal(t, http.StatusNotFound, recorder.Code)

	// 无效模型名直接拒绝，不触发数据库查询喵。
	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/坏名"}`))
	ctx.Set("id", 7)
	handled = handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/坏名"})
	require.False(t, handled)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

// TestHandleUserUpstreamModelRequestQuota 验证请求前硬检查拦截余额不足与上限耗尽的模型喵。
func TestHandleUserUpstreamModelRequestQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 构造独立测试库，替换全局 DB 并在结束后恢复喵。
	testDB := newUpstreamModelTestDB(t)
	oldDB := model.DB
	model.DB = testDB
	defer func() { model.DB = oldDB }()

	// 余额为 0 的模型被硬检查拦截，返回额度不足喵。
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{OwnerUserID: 7, NormalizedName: "empty-balance", Enabled: true, BalanceCents: 0}).Error)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/empty-balance"}`))
	ctx.Set("id", 7)
	handled := handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/empty-balance"})
	require.False(t, handled)
	require.Equal(t, http.StatusConflict, recorder.Code)

	// 自用上限已耗尽的模型被拦截喵。
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{OwnerUserID: 7, NormalizedName: "limit-reached", Enabled: true, BalanceCents: 500, SpendLimitCents: 300, TotalSpentCents: 300}).Error)
	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/limit-reached"}`))
	ctx.Set("id", 7)
	handled = handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/limit-reached"})
	require.False(t, handled)
	require.Equal(t, http.StatusConflict, recorder.Code)

	// 余额充足且上限未耗尽：硬检查放行，走到凭据解密阶段，密文无效返回 503 而非 409 喵。
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{OwnerUserID: 7, NormalizedName: "quota-ok", Enabled: true, BalanceCents: 500, SpendLimitCents: 300, TotalSpentCents: 100, EncryptedBaseURL: "bad-enc", EncryptedAPIKey: "bad-enc", CredentialVersion: 1, RealModelName: "gpt-4o"}).Error)
	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/quota-ok"}`))
	ctx.Set("id", 7)
	handled = handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/quota-ok"})
	require.False(t, handled)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}

// TestHandleUserUpstreamModelRequestShared 验证共享调用的授权回退、额度耗尽停止与属主自用不受影响喵。
func TestHandleUserUpstreamModelRequestShared(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 构造独立测试库，替换全局 DB 并在结束后恢复喵。
	testDB := newUpstreamModelTestDB(t)
	oldDB := model.DB
	model.DB = testDB
	defer func() { model.DB = oldDB }()

	// 用户 7 拥有一个共享中的模型，余额 100 分、共享额度 1000 分喵。
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{OwnerUserID: 7, NormalizedName: "shared", Enabled: true, ShareEnabled: true, ShareLimitCents: 1000, ShareSpentCents: 0, BalanceCents: 100, EncryptedBaseURL: "bad-enc", EncryptedAPIKey: "bad-enc", CredentialVersion: 1, RealModelName: "gpt-4o"}).Error)

	// 用户 8 调用共享模型：授权回退到共享路径并放行，密文无效返回 503 而非 404 喵。
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/shared"}`))
	ctx.Set("id", 8)
	handled := handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/shared"})
	require.False(t, handled)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)

	// 共享额度耗尽后，用户 8 再调用返回 404（共享停止）喵。
	require.NoError(t, testDB.Model(&model.UserUpstreamModel{}).Where("normalized_name = ?", "shared").Update("share_spent_cents", 1000).Error)
	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/shared"}`))
	ctx.Set("id", 8)
	handled = handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/shared"})
	require.False(t, handled)
	require.Equal(t, http.StatusNotFound, recorder.Code)

	// 属主自己调用同一模型不受共享耗尽影响：属主查询优先，自用仍放行到透传喵。
	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/shared"}`))
	ctx.Set("id", 7)
	handled = handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/shared"})
	require.False(t, handled)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}
