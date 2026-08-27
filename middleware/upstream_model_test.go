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
	require.NoError(t, testDB.AutoMigrate(&model.UserUpstreamModel{}, &model.EntityProbeState{}))
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
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{OwnerUserID: 7, NormalizedName: "empty-balance", Enabled: true, BalanceCents: 0, AvailableCents: 100}).Error)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/empty-balance"}`))
	ctx.Set("id", 7)
	handled := handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/empty-balance"})
	require.False(t, handled)
	require.Equal(t, http.StatusConflict, recorder.Code)

	// 可用额度为 0 的模型被硬检查拦截喵。
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{OwnerUserID: 7, NormalizedName: "available-empty", Enabled: true, BalanceCents: 500, AvailableCents: 0}).Error)
	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/available-empty"}`))
	ctx.Set("id", 7)
	handled = handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/available-empty"})
	require.False(t, handled)
	require.Equal(t, http.StatusConflict, recorder.Code)

	// 余额与可用额度均充足：硬检查放行，走到凭据解密阶段，密文无效返回 503 而非 409 喵。
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{OwnerUserID: 7, NormalizedName: "quota-ok", Enabled: true, BalanceCents: 500, AvailableCents: 300, EncryptedBaseURL: "bad-enc", EncryptedAPIKey: "bad-enc", CredentialVersion: 1, RealModelName: "gpt-4o"}).Error)
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

	// 用户 7 拥有一个共享中的模型，余额 100 分、可用 200 分、共享额度 1000 分喵。
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{OwnerUserID: 7, NormalizedName: "shared", Enabled: true, ShareEnabled: true, ShareLimitCents: 1000, BalanceCents: 100, AvailableCents: 200, EncryptedBaseURL: "bad-enc", EncryptedAPIKey: "bad-enc", CredentialVersion: 1, RealModelName: "gpt-4o"}).Error)

	// 用户 8 调用共享模型：授权回退到共享路径并放行，密文无效返回 503 而非 404 喵。
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/shared"}`))
	ctx.Set("id", 8)
	handled := handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/shared"})
	require.False(t, handled)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)

	// 共享额度耗尽（递减到 0）后，用户 8 再调用返回 404（共享停止）喵。
	require.NoError(t, testDB.Model(&model.UserUpstreamModel{}).Where("normalized_name = ?", "shared").Update("share_limit_cents", 0).Error)
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

// TestHandleUserUpstreamModelProbeStateConfig 验证配置态请求只更新最近时间、不计入成功率喵。
func TestHandleUserUpstreamModelProbeStateConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 构造独立测试库，替换全局 DB 并在结束后恢复喵。
	testDB := newUpstreamModelTestDB(t)
	oldDB := model.DB
	model.DB = testDB
	defer func() { model.DB = oldDB }()

	// 余额为 0 的启用模型：请求被硬检查拦截，状态行只触达最近时间喵。
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{OwnerUserID: 7, NormalizedName: "empty-balance", Enabled: true, BalanceCents: 0, AvailableCents: 100}).Error)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/empty-balance"}`))
	ctx.Set("id", 7)
	require.False(t, handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/empty-balance"}))
	require.Equal(t, http.StatusConflict, recorder.Code)

	var emptyBalanceModel model.UserUpstreamModel
	require.NoError(t, testDB.Where("normalized_name = ?", "empty-balance").First(&emptyBalanceModel).Error)
	state, stateErr := model.GetEntityProbeState(model.EntityProbeScopeUpstream, emptyBalanceModel.ID)
	require.NoError(t, stateErr)
	require.Equal(t, int64(0), state.RequestCount)
	require.Equal(t, int64(0), state.SuccessCount)
	require.True(t, state.LastAt > 0)

	// 停用模型：启用查询失败后回退任意状态查询，同样只触达最近时间喵。
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{OwnerUserID: 7, NormalizedName: "disabled", Enabled: false}).Error)
	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/disabled"}`))
	ctx.Set("id", 7)
	require.False(t, handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/disabled"}))
	require.Equal(t, http.StatusNotFound, recorder.Code)

	var disabledModel model.UserUpstreamModel
	require.NoError(t, testDB.Where("normalized_name = ?", "disabled").First(&disabledModel).Error)
	state, stateErr = model.GetEntityProbeState(model.EntityProbeScopeUpstream, disabledModel.ID)
	require.NoError(t, stateErr)
	require.Equal(t, int64(0), state.RequestCount)
	require.True(t, state.LastAt > 0)
}

// TestHandleUserUpstreamModelProbeStateDecryptFailure 验证凭据解密失败计入失败样本喵。
func TestHandleUserUpstreamModelProbeStateDecryptFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 构造独立测试库，替换全局 DB 并在结束后恢复喵。
	testDB := newUpstreamModelTestDB(t)
	oldDB := model.DB
	model.DB = testDB
	defer func() { model.DB = oldDB }()

	// 余额充足但密文无效的模型：解密失败返回 503，且计入一次失败喵。
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{OwnerUserID: 7, NormalizedName: "bad-cred", Enabled: true, BalanceCents: 500, AvailableCents: 300, EncryptedBaseURL: "bad-enc", EncryptedAPIKey: "bad-enc", CredentialVersion: 1, RealModelName: "gpt-4o"}).Error)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/bad-cred"}`))
	ctx.Set("id", 7)
	require.False(t, handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/bad-cred"}))
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)

	var badCredModel model.UserUpstreamModel
	require.NoError(t, testDB.Where("normalized_name = ?", "bad-cred").First(&badCredModel).Error)
	state, stateErr := model.GetEntityProbeState(model.EntityProbeScopeUpstream, badCredModel.ID)
	require.NoError(t, stateErr)
	require.Equal(t, int64(1), state.RequestCount)
	require.Equal(t, int64(0), state.SuccessCount)
	require.False(t, state.LastSuccess)
	require.Equal(t, "credential_error", state.LastError)
}
