package middleware

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
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

// TestSettleUserUpstreamModelChargeWritesLog 验证结算链路把 usage 令牌与首字延迟写入日志喵。
func TestSettleUserUpstreamModelChargeWritesLog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 构造独立测试库，替换全局 DB 与日志库并在结束后恢复喵。
	testDB := newUpstreamModelTestDB(t)
	require.NoError(t, testDB.AutoMigrate(&model.Log{}))
	oldDB := model.DB
	oldLogDB := model.LOG_DB
	model.DB = testDB
	model.LOG_DB = testDB
	defer func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
	}()

	// 属主 7 的上游模型：输入 10 元/M、缓存 0.1 元/M，余额充足喵。
	upstreamModel := &model.UserUpstreamModel{OwnerUserID: 7, NormalizedName: "alpha", Enabled: true, RealModelName: "gpt-4o", ModelRatio: "10", CacheRatio: "0.1", BalanceCents: 100000, AvailableCents: 100000}
	require.NoError(t, testDB.Create(upstreamModel).Error)

	// 结算收到的是已归一化 usage：1000 输入含 200 缓存命中、100 输出喵。
	usage := &dto.Usage{PromptTokens: 1000, CompletionTokens: 100, TotalTokens: 1100, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 200}}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/alpha"}`))
	ctx.Set("id", 7)
	// 自用结算（未预扣，直接扣费路径），TTFT 123 毫秒应写入日志 other["frt"] 喵。
	settleUserUpstreamModelCharge(ctx, 7, upstreamModel, usage, "default", false, 5, false, 123, 0)

	// 日志行应记录输入/输出令牌，类型为自定上游喵。
	var logRow model.Log
	require.NoError(t, testDB.Where("model_name = ?", "user/alpha").Order("id desc").First(&logRow).Error)
	require.Equal(t, model.LogTypeCustomUpstream, logRow.Type)
	require.Equal(t, 1000, logRow.PromptTokens)
	require.Equal(t, 100, logRow.CompletionTokens)

	// other 字段应包含缓存命中数与首字延迟，供日志详情与 Timing 展示喵。
	other := map[string]interface{}{}
	require.NoError(t, common.Unmarshal([]byte(logRow.Other), &other))
	require.Equal(t, float64(200), other["cached_tokens"])
	require.Equal(t, float64(123), other["frt"])
}

// TestEstimateUserUpstreamModelCostCents 验证请求前预估费用：输入按 body 字节/4 估算、输出按 max_tokens 上限喵。
func TestEstimateUserUpstreamModelCostCents(t *testing.T) {
	// 模型输入价 10 元/M、输出价 20 元/M，用于精确计算预期费用喵。
	upstreamModel := &model.UserUpstreamModel{ModelRatio: "10", CompletionRatio: "20"}
	// 300 次 hello world 共 3600 字符，body 长度远超最小兜底，输入估算取真实值喵。
	longContent := strings.Repeat("hello world ", 300)
	// 带 max_tokens=2000 的请求体：输出按 2000 token 上限预估喵。
	body := []byte(`{"messages":[{"role":"user","content":"` + longContent + `"}],"max_tokens":2000}`)
	expectedPromptTokens := len(body) / 4
	// 输入与输出分别乘各自单价后 /1e6 转元、×100 转分，与结算函数口径一致，四舍五入取整喵。
	expectedCents := int64(math.Round(float64(expectedPromptTokens*10+2000*20) / 1e4))
	require.Equal(t, expectedCents, estimateUserUpstreamModelCostCents(upstreamModel, body))

	// 同一请求体去掉 max_tokens：输出部分不参与预估，费用显著更小喵。
	bodyNoMax := []byte(`{"messages":[{"role":"user","content":"` + longContent + `"}]}`)
	expectedPromptTokensNoMax := len(bodyNoMax) / 4
	expectedCentsNoMax := int64(math.Round(float64(expectedPromptTokensNoMax*10) / 1e4))
	require.Equal(t, expectedCentsNoMax, estimateUserUpstreamModelCostCents(upstreamModel, bodyNoMax))
	// 输出上限使预扣金额更高，保证预扣覆盖潜在超额喵。
	require.Greater(t, estimateUserUpstreamModelCostCents(upstreamModel, body), estimateUserUpstreamModelCostCents(upstreamModel, bodyNoMax))

	// 空 body 与 nil 兜底：输入按最小 500 token 估算，费用非零喵。
	emptyModel := &model.UserUpstreamModel{ModelRatio: "10"}
	require.Greater(t, estimateUserUpstreamModelCostCents(emptyModel, nil), int64(0))
	require.Greater(t, estimateUserUpstreamModelCostCents(emptyModel, []byte{}), int64(0))

	// 空模型按零费用预估，避免空指针喵。
	require.Equal(t, int64(0), estimateUserUpstreamModelCostCents(nil, body))

	// 模型未定价（输入价为 0）时预估费用为零，不产生预扣喵。
	zeroModel := &model.UserUpstreamModel{ModelRatio: "0"}
	require.Equal(t, int64(0), estimateUserUpstreamModelCostCents(zeroModel, body))
}
