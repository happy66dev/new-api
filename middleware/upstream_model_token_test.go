package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	virtualmodelservice "github.com/QuantumNous/new-api/service/virtualmodel"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestHandleUserUpstreamModelRequestInjectsRelay 验证非虚拟 user/xxx 直调改为注入临时渠道走原生 relay 中转链喵。
// handle 阶段只做授权/预扣/解密/渠道注入，不再同步透传与写日志；
// relay 成功结算与失败退款由 SettleUpstreamModelRelaySuccess / HandleUpstreamModelRelayFailure hook 完成喵。
func TestHandleUserUpstreamModelRequestInjectsRelay(t *testing.T) {
	// 开发模式允许本地 http mock 上游，测试结束后自动恢复环境变量喵。
	t.Setenv("VIRTUAL_MODEL_INSECURE_UPSTREAM", "1")
	// 配置随机测试主密钥，使凭据加解密全链路可跑通喵。
	t.Setenv(virtualmodelservice.CredentialMasterKeyEnvironmentName, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	gin.SetMode(gin.TestMode)
	// 构造独立数据库：上游模型、探测状态与日志表齐全喵。
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, testDB.AutoMigrate(&model.UserUpstreamModel{}, &model.EntityProbeState{}, &model.Log{}))
	oldDB := model.DB
	oldLogDB := model.LOG_DB
	model.DB = testDB
	model.LOG_DB = testDB
	defer func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
	}()

	// 用真实凭据加密生成模型条目，使解密与注入全链路可跑通喵。
	baseURLCipher, version, encryptError := virtualmodelservice.EncryptCredential("https://upstream.example.com")
	require.NoError(t, encryptError)
	apiKeyCipher, _, apiKeyError := virtualmodelservice.EncryptCredential("sk-test")
	require.NoError(t, apiKeyError)
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{
		OwnerUserID:          7,
		NormalizedName:       "demo",
		DisplayName:          "Demo",
		Enabled:              true,
		EncryptedBaseURL:     baseURLCipher,
		EncryptedAPIKey:      apiKeyCipher,
		CredentialVersion:    version,
		RealModelName:        "gpt-4o",
		BalanceCents:         1000,
		AvailableCents:       800,
		AuthStyle:            "bearer",
		ModelRatio:           "1",
		CompletionRatio:      "1",
		CacheRatio:           "1",
		CacheCreationRatio:   "1",
		CacheCreation5mRatio: "1",
		CacheCreation1hRatio: "1",
		ImageRatio:           "1",
		AudioRatio:           "1",
		AudioCompletionRatio: "1",
		Version:              1,
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/demo","stream":true,"messages":[]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", 7)

	handled := handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/demo"})
	require.False(t, handled)
	// 注入 relay：标记已设置，临时渠道（类型/base_url/key）写入 context 供 relay 读取喵。
	require.True(t, IsUpstreamModelRelayRequest(ctx))
	require.Equal(t, constant.ChannelTypeOpenAI, common.GetContextKeyInt(ctx, constant.ContextKeyChannelType))
	require.Equal(t, "sk-test", common.GetContextKeyString(ctx, constant.ContextKeyChannelKey))
	require.Equal(t, "https://upstream.example.com", common.GetContextKeyString(ctx, constant.ContextKeyChannelBaseUrl))
	require.Equal(t, "gpt-4o", common.GetContextKeyString(ctx, constant.ContextKeySelectedModel))
	// handle 阶段不写日志、不写响应；透传、结算与错误输出全部交由 relay 层喵。
	var logCount int64
	require.NoError(t, testDB.Model(&model.Log{}).Count(&logCount).Error)
	require.Zero(t, logCount)
	require.Equal(t, http.StatusOK, recorder.Code)
}

// TestHandleUserUpstreamModelRequestVirtualLogToken 验证虚拟模型 user/xxx 候选成功日志（type=9）token 正确喵。
func TestHandleUserUpstreamModelRequestVirtualLogToken(t *testing.T) {
	// 开发模式允许本地 http mock 上游，测试结束后自动恢复环境变量喵。
	t.Setenv("VIRTUAL_MODEL_INSECURE_UPSTREAM", "1")
	// 配置随机测试主密钥，使凭据加解密全链路可跑通喵。
	t.Setenv(virtualmodelservice.CredentialMasterKeyEnvironmentName, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	gin.SetMode(gin.TestMode)
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, testDB.AutoMigrate(&model.UserUpstreamModel{}, &model.EntityProbeState{}, &model.Log{}))
	oldDB := model.DB
	oldLogDB := model.LOG_DB
	model.DB = testDB
	model.LOG_DB = testDB
	defer func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
	}()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":300,\"completion_tokens\":120,\"total_tokens\":420}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	baseURLCipher, version, encryptError := virtualmodelservice.EncryptCredential(upstream.URL)
	require.NoError(t, encryptError)
	apiKeyCipher, _, apiKeyError := virtualmodelservice.EncryptCredential("sk-test")
	require.NoError(t, apiKeyError)
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{
		OwnerUserID:          7,
		NormalizedName:       "demo",
		DisplayName:          "Demo",
		Enabled:              true,
		EncryptedBaseURL:     baseURLCipher,
		EncryptedAPIKey:      apiKeyCipher,
		CredentialVersion:    version,
		RealModelName:        "gpt-4o",
		BalanceCents:         1000,
		AvailableCents:       800,
		AuthStyle:            "bearer",
		ModelRatio:           "1",
		CompletionRatio:      "1",
		CacheRatio:           "1",
		CacheCreationRatio:   "1",
		CacheCreation5mRatio: "1",
		CacheCreation1hRatio: "1",
		ImageRatio:           "1",
		AudioRatio:           "1",
		AudioCompletionRatio: "1",
		Version:              1,
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"virtual/vm","stream":true,"messages":[]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", 7)
	// 虚拟模型执行状态：单内部候选 user/demo，成功路径写 type=9 日志喵。
	executionState := &virtualModelExecutionState{
		virtualModelName:      "virtual/vm",
		virtualModelID:        3,
		ownerUserID:           7,
		startTime:             time.Now(),
		currentCandidateIndex: 0,
		executionSnapshot: &model.VirtualModelExecutionSnapshot{
			Candidates: []model.VirtualModelCandidateSnapshot{
				{CandidateID: 71, VirtualModelID: 3, StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true, GroupName: "default", RealModelName: "user/demo"},
			},
			FailureRulesByCandidateID: map[int][]model.VirtualModelFailureRule{},
		},
	}
	common.SetContextKey(ctx, constant.ContextKeyVirtualModelExecutionState, executionState)
	// 标记虚拟模型上下文：日志归入 type=9 且附带候选尝试序列喵。
	common.SetContextKey(ctx, constant.ContextKeyVirtualLogType, model.LogTypeVirtualModel)
	common.SetContextKey(ctx, constant.ContextKeyVirtualCandidateSeq, 1)
	attempts := make([]model.VirtualModelCandidateAttemptRecord, 0, 4)
	common.SetContextKey(ctx, constant.ContextKeyVirtualCandidateAttempts, &attempts)

	handled := handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/demo"})
	require.False(t, handled)
	require.Equal(t, http.StatusOK, recorder.Code)

	// 虚拟模型日志必须归入 type=9 且携带真实 token 计数喵。
	var logRecord model.Log
	require.NoError(t, testDB.Where("type = ?", model.LogTypeVirtualModel).First(&logRecord).Error)
	require.Equal(t, 300, logRecord.PromptTokens)
	require.Equal(t, 120, logRecord.CompletionTokens)
}

// TestHandleUserUpstreamModelRequestRequestLevelTiming 验证虚拟模型 user/xxx 候选成功日志耗时取请求级口径喵。
// 请求入口比候选启动早 5 秒时，日志总耗时与首字都应按请求入口计（约 5 秒），而非候选级（趋近 0 秒）喵。
func TestHandleUserUpstreamModelRequestRequestLevelTiming(t *testing.T) {
	// 开发模式允许本地 http mock 上游，测试结束后自动恢复环境变量喵。
	t.Setenv("VIRTUAL_MODEL_INSECURE_UPSTREAM", "1")
	// 配置随机测试主密钥，使凭据加解密全链路可跑通喵。
	t.Setenv(virtualmodelservice.CredentialMasterKeyEnvironmentName, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	gin.SetMode(gin.TestMode)
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, testDB.AutoMigrate(&model.UserUpstreamModel{}, &model.EntityProbeState{}, &model.Log{}))
	oldDB := model.DB
	oldLogDB := model.LOG_DB
	model.DB = testDB
	model.LOG_DB = testDB
	defer func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
	}()

	// mock 上游立即返回流式响应，候选级耗时趋近于零，用于区分请求级与候选级口径喵。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	baseURLCipher, version, encryptError := virtualmodelservice.EncryptCredential(upstream.URL)
	require.NoError(t, encryptError)
	apiKeyCipher, _, apiKeyError := virtualmodelservice.EncryptCredential("sk-test")
	require.NoError(t, apiKeyError)
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{
		OwnerUserID:          7,
		NormalizedName:       "demo",
		DisplayName:          "Demo",
		Enabled:              true,
		EncryptedBaseURL:     baseURLCipher,
		EncryptedAPIKey:      apiKeyCipher,
		CredentialVersion:    version,
		RealModelName:        "gpt-4o",
		BalanceCents:         1000,
		AvailableCents:       800,
		AuthStyle:            "bearer",
		ModelRatio:           "1",
		CompletionRatio:      "1",
		CacheRatio:           "1",
		CacheCreationRatio:   "1",
		CacheCreation5mRatio: "1",
		CacheCreation1hRatio: "1",
		ImageRatio:           "1",
		AudioRatio:           "1",
		AudioCompletionRatio: "1",
		Version:              1,
	}).Error)

	// 虚拟模型执行状态：请求入口设为 5 秒前，模拟前面候选尝试已消耗的时间喵。
	executionState := &virtualModelExecutionState{
		virtualModelName:      "virtual/timing",
		virtualModelID:        3,
		ownerUserID:           7,
		startTime:             time.Now().Add(-5 * time.Second),
		currentCandidateIndex: 0,
		executionSnapshot: &model.VirtualModelExecutionSnapshot{
			Candidates: []model.VirtualModelCandidateSnapshot{
				{CandidateID: 90, VirtualModelID: 3, StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true, GroupName: "default", RealModelName: "gpt-4o"},
			},
			FailureRulesByCandidateID: map[int][]model.VirtualModelFailureRule{},
		},
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"virtual/timing","stream":true,"messages":[]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", 7)
	// 标记虚拟模型执行状态与日志归入 type=9，模拟真实虚拟模型请求链路喵。
	common.SetContextKey(ctx, constant.ContextKeyVirtualModelExecutionState, executionState)
	common.SetContextKey(ctx, constant.ContextKeyVirtualLogType, model.LogTypeVirtualModel)
	common.SetContextKey(ctx, constant.ContextKeyVirtualCandidateSeq, 1)
	attempts := make([]model.VirtualModelCandidateAttemptRecord, 0, 4)
	common.SetContextKey(ctx, constant.ContextKeyVirtualCandidateAttempts, &attempts)

	handled := handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/demo"})
	require.False(t, handled)
	require.Equal(t, http.StatusOK, recorder.Code)

	// 总耗时必须取请求级（约 5 秒），而非候选级（趋近 0 秒）喵。
	var logRecord model.Log
	require.NoError(t, testDB.Where("type = ?", model.LogTypeVirtualModel).First(&logRecord).Error)
	require.GreaterOrEqual(t, logRecord.UseTime, 4)
	// 首字延迟取请求级首字（首次写响应减请求入口），同样接近 5 秒且不超过总耗时喵。
	var other map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(logRecord.Other, &other))
	frtValue, foundFRT := other["frt"]
	require.True(t, foundFRT, "日志 Other 必须携带请求级首字 frt 喵")
	frtMs, validFRT := frtValue.(float64)
	require.True(t, validFRT, "frt 必须是数值毫秒喵")
	require.GreaterOrEqual(t, int64(frtMs), int64(4000))
	// 首字不超过总耗时；UseTime 为秒级向下取整，容忍一秒舍入误差喵。
	require.LessOrEqual(t, int64(frtMs), int64(logRecord.UseTime)*1000+1000)

	// 候选尝试序列的成功候选耗时必须是模型级（该候选自己），而非请求级（约 5 秒）喵。
	candidatesValue, foundCandidates := other["candidates"]
	require.True(t, foundCandidates, "日志必须携带候选尝试序列 candidates 喵")
	candidates, validCandidates := candidatesValue.([]interface{})
	require.True(t, validCandidates, "candidates 必须是数组喵")
	require.NotEmpty(t, candidates, "候选尝试序列不得为空喵")
	successAttempt, validAttempt := candidates[0].(map[string]interface{})
	require.True(t, validAttempt, "候选尝试必须是对象喵")
	attemptElapsedMs, validElapsed := successAttempt["elapsed_ms"].(float64)
	require.True(t, validElapsed, "候选尝试必须带 elapsed_ms 喵")
	require.Less(t, attemptElapsedMs, float64(1000), "成功候选总耗时应为模型级（该候选自己，小于 1 秒），而非请求级（约 5 秒）喵")
	attemptTtftMs, validTtft := successAttempt["ttft_ms"].(float64)
	require.True(t, validTtft, "候选尝试必须带 ttft_ms 喵")
	require.Less(t, attemptTtftMs, float64(1000), "成功候选首字应为模型级上游 TTFT（小于 1 秒），而非请求级（约 5 秒）喵")
}

// TestSettleUpstreamModelRelaySuccess 验证自定义上游 relay 成功后 hook 完成独立 RMB 结算并写 type=8 日志喵。
func TestSettleUpstreamModelRelaySuccess(t *testing.T) {
	// 开发模式允许本地 http mock 上游，测试结束后自动恢复环境变量喵。
	t.Setenv("VIRTUAL_MODEL_INSECURE_UPSTREAM", "1")
	// 配置随机测试主密钥，使凭据加解密全链路可跑通喵。
	t.Setenv(virtualmodelservice.CredentialMasterKeyEnvironmentName, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	gin.SetMode(gin.TestMode)
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, testDB.AutoMigrate(&model.UserUpstreamModel{}, &model.EntityProbeState{}, &model.Log{}))
	oldDB := model.DB
	oldLogDB := model.LOG_DB
	model.DB = testDB
	model.LOG_DB = testDB
	defer func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
	}()

	// 用真实凭据加密生成模型条目，使解密与注入全链路可跑通喵。
	baseURLCipher, version, encryptError := virtualmodelservice.EncryptCredential("https://upstream.example.com")
	require.NoError(t, encryptError)
	apiKeyCipher, _, apiKeyError := virtualmodelservice.EncryptCredential("sk-test")
	require.NoError(t, apiKeyError)
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{
		OwnerUserID:          7,
		NormalizedName:       "demo",
		DisplayName:          "Demo",
		Enabled:              true,
		EncryptedBaseURL:     baseURLCipher,
		EncryptedAPIKey:      apiKeyCipher,
		CredentialVersion:    version,
		RealModelName:        "gpt-4o",
		BalanceCents:         1000,
		AvailableCents:       800,
		AuthStyle:            "bearer",
		ModelRatio:           "1",
		CompletionRatio:      "1",
		CacheRatio:           "1",
		CacheCreationRatio:   "1",
		CacheCreation5mRatio: "1",
		CacheCreation1hRatio: "1",
		ImageRatio:           "1",
		AudioRatio:           "1",
		AudioCompletionRatio: "1",
		Version:              1,
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/demo","stream":true,"messages":[]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", 7)

	handled := handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/demo"})
	require.False(t, handled)
	require.True(t, IsUpstreamModelRelayRequest(ctx))

	// 模拟 relay 成功拿到上游 usage（真实 token 计数落库）喵。
	usage := &dto.Usage{PromptTokens: 100, CompletionTokens: 50}
	common.SetContextKey(ctx, constant.ContextKeyUpstreamModelUsage, usage)
	SettleUpstreamModelRelaySuccess(ctx, 0)

	// 日志必须落库且携带真实 token 计数喵。
	var logRecord model.Log
	require.NoError(t, testDB.Where("type = ?", model.LogTypeCustomUpstream).First(&logRecord).Error)
	require.Equal(t, 100, logRecord.PromptTokens)
	require.Equal(t, 50, logRecord.CompletionTokens)
	// 结算标记：预扣已差额结算，Distribute 兜底退款不再触发喵。
	relayCtx := getUserUpstreamModelRelayContext(ctx)
	require.NotNil(t, relayCtx)
	require.True(t, relayCtx.settled)
}

// TestHandleUpstreamModelRelayFailure 验证自定义上游 relay 最终失败时 hook 退还预扣并写 type=8 失败日志喵。
func TestHandleUpstreamModelRelayFailure(t *testing.T) {
	// 开发模式允许本地 http mock 上游，测试结束后自动恢复环境变量喵。
	t.Setenv("VIRTUAL_MODEL_INSECURE_UPSTREAM", "1")
	// 配置随机测试主密钥，使凭据加解密全链路可跑通喵。
	t.Setenv(virtualmodelservice.CredentialMasterKeyEnvironmentName, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	gin.SetMode(gin.TestMode)
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, testDB.AutoMigrate(&model.UserUpstreamModel{}, &model.EntityProbeState{}, &model.Log{}))
	oldDB := model.DB
	oldLogDB := model.LOG_DB
	model.DB = testDB
	model.LOG_DB = testDB
	defer func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
	}()

	// 用真实凭据加密生成模型条目，使解密与注入全链路可跑通喵。
	// ModelRatio=1000：输入按 min 兜底 500 token 估算，预扣 500×1000/1e6×100000=50000 单位（0.5 元），在余额内且非零，验证失败退款路径喵。
	baseURLCipher, version, encryptError := virtualmodelservice.EncryptCredential("https://upstream.example.com")
	require.NoError(t, encryptError)
	apiKeyCipher, _, apiKeyError := virtualmodelservice.EncryptCredential("sk-test")
	require.NoError(t, apiKeyError)
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{
		OwnerUserID:          7,
		NormalizedName:       "demo",
		DisplayName:          "Demo",
		Enabled:              true,
		EncryptedBaseURL:     baseURLCipher,
		EncryptedAPIKey:      apiKeyCipher,
		CredentialVersion:    version,
		RealModelName:        "gpt-4o",
		BalanceCents:         500000,
		AvailableCents:       400000,
		AuthStyle:            "bearer",
		ModelRatio:           "1000",
		CompletionRatio:      "1",
		CacheRatio:           "1",
		CacheCreationRatio:   "1",
		CacheCreation5mRatio: "1",
		CacheCreation1hRatio: "1",
		ImageRatio:           "1",
		AudioRatio:           "1",
		AudioCompletionRatio: "1",
		Version:              1,
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/demo","stream":true,"messages":[]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", 7)

	handled := handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/demo"})
	require.False(t, handled)
	require.True(t, IsUpstreamModelRelayRequest(ctx))
	// 预扣已发生：输入按 min 兜底 500 token，ModelRatio=1000 时预扣 50000 单位（0.5 元）喵。
	require.Positive(t, getUserUpstreamModelRelayContext(ctx).preConsumedCents)

	// 模拟 relay 失败：上游 429 限流喵。
	newAPIError := types.NewErrorWithStatusCode(errors.New("rate limited by upstream"), types.ErrorCode("rate_limited"), http.StatusTooManyRequests)
	HandleUpstreamModelRelayFailure(ctx, newAPIError)

	// 失败日志：type=8、模型名 user/demo、错误分类与状态码落库喵。
	var failureLog model.Log
	require.NoError(t, testDB.Where("type = ? AND model_name = ?", model.LogTypeCustomUpstream, "user/demo").First(&failureLog).Error)
	require.Contains(t, failureLog.Content, "rate_limited")
	var failureOther map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(failureLog.Other, &failureOther))
	require.Equal(t, "rate_limited", failureOther["error_class"])
	require.Equal(t, float64(http.StatusTooManyRequests), failureOther["http_status"])
	require.Equal(t, false, failureOther["final_success"])
	// 结算标记：预扣已退还，Distribute 兜底退款不再触发喵。
	relayCtx := getUserUpstreamModelRelayContext(ctx)
	require.NotNil(t, relayCtx)
	require.True(t, relayCtx.settled)
}

// TestHandleUserUpstreamModelRequestVirtualFailureRefunds 验证虚拟模型 user/xxx 候选透传失败时退还预扣、活跃计数归零喵。
func TestHandleUserUpstreamModelRequestVirtualFailureRefunds(t *testing.T) {
	// 开发模式允许本地 http mock 上游，测试结束后自动恢复环境变量喵。
	t.Setenv("VIRTUAL_MODEL_INSECURE_UPSTREAM", "1")
	// 配置随机测试主密钥，使凭据加解密全链路可跑通喵。
	t.Setenv(virtualmodelservice.CredentialMasterKeyEnvironmentName, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	gin.SetMode(gin.TestMode)
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, testDB.AutoMigrate(&model.UserUpstreamModel{}, &model.EntityProbeState{}, &model.Log{}))
	oldDB := model.DB
	oldLogDB := model.LOG_DB
	model.DB = testDB
	model.LOG_DB = testDB
	defer func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
	}()

	// mock 上游返回 429 限流错误，正文为 OpenAI 风格错误喵。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited by upstream","type":"rate_limit_error"}}`))
	}))
	defer upstream.Close()

	baseURLCipher, version, encryptError := virtualmodelservice.EncryptCredential(upstream.URL)
	require.NoError(t, encryptError)
	apiKeyCipher, _, apiKeyError := virtualmodelservice.EncryptCredential("sk-test")
	require.NoError(t, apiKeyError)
	// ModelRatio=1000：输入按 min 兜底 500 token 估算，预扣 500×1000/1e6×100000=50000 单位（0.5 元），在余额内且非零喵。
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{
		OwnerUserID:          7,
		NormalizedName:       "demo",
		DisplayName:          "Demo",
		Enabled:              true,
		EncryptedBaseURL:     baseURLCipher,
		EncryptedAPIKey:      apiKeyCipher,
		CredentialVersion:    version,
		RealModelName:        "gpt-4o",
		BalanceCents:         500000,
		AvailableCents:       400000,
		ShareLimitCents:      1000000,
		AuthStyle:            "bearer",
		ModelRatio:           "1000",
		CompletionRatio:      "1",
		CacheRatio:           "1",
		CacheCreationRatio:   "1",
		CacheCreation5mRatio: "1",
		CacheCreation1hRatio: "1",
		ImageRatio:           "1",
		AudioRatio:           "1",
		AudioCompletionRatio: "1",
		Version:              1,
	}).Error)

	// 虚拟执行状态：单候选 user/demo，全局兜底规则把限流直接透传喵。
	executionState := newUpstreamFailureTestState(
		[]model.VirtualModelCandidateSnapshot{
			{CandidateID: 71, VirtualModelID: 9, StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true, MaxRetries: 0, GroupName: "default", RealModelName: "user/demo"},
		},
		nil,
		[]model.VirtualModelFailureRule{
			{HTTPStatus: http.StatusTooManyRequests, Action: model.VirtualModelActionPassthrough},
		},
	)
	executionState.startTime = time.Now().Add(-2 * time.Second)
	ctx, _, recorder := newUpstreamFailureTestContext(executionState)
	// 标记虚拟模型日志归入 type=9 并注入候选序号，与真实虚拟模型请求链路一致喵。
	common.SetContextKey(ctx, constant.ContextKeyVirtualModelName, "vm-test")
	common.SetContextKey(ctx, constant.ContextKeyVirtualLogType, model.LogTypeVirtualModel)
	common.SetContextKey(ctx, constant.ContextKeyVirtualCandidateSeq, 1)

	// 记录调用前活跃计数基线：全局计数可能被其他 relay 路径测试泄漏，只断言本次调用不新增泄漏喵。
	var createdModel model.UserUpstreamModel
	require.NoError(t, testDB.Where("normalized_name = ?", "demo").First(&createdModel).Error)
	selfBefore, sharedBefore := GetUpstreamModelActiveCount(createdModel.ID)

	handled := handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/demo", Group: "default"})
	require.False(t, handled)
	require.Equal(t, http.StatusTooManyRequests, recorder.Code)

	// 预扣已全额退还：余额 500000、可用 400000、共享 1000000 与初始值一致，绝不锁死额度喵。
	var settled model.UserUpstreamModel
	require.NoError(t, testDB.Where("normalized_name = ?", "demo").First(&settled).Error)
	require.Equal(t, int64(500000), settled.BalanceCents)
	require.Equal(t, int64(400000), settled.AvailableCents)
	require.Equal(t, int64(1000000), settled.ShareLimitCents)
	// 活跃计数回到基线：失败透传已退出活跃，本次调用不新增并发占用喵。
	self, shared := GetUpstreamModelActiveCount(settled.ID)
	require.Equal(t, selfBefore, self)
	require.Equal(t, sharedBefore, shared)
}

// TestHandleUserUpstreamModelRequestDecryptFailureRefunds 验证凭据解密失败时退还预扣、退出活跃计数喵。
func TestHandleUserUpstreamModelRequestDecryptFailureRefunds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 构造独立测试库，替换全局 DB 并在结束后恢复喵。
	testDB := newUpstreamModelTestDB(t)
	oldDB := model.DB
	model.DB = testDB
	defer func() { model.DB = oldDB }()

	// 余额充足但密文无效的模型：解密失败返回 503，且预扣必须全额退还喵。
	// ModelRatio=1000：输入按 min 兜底 500 token 估算，预扣 50000 单位（0.5 元）喵。
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{OwnerUserID: 7, NormalizedName: "bad-cred", Enabled: true, BalanceCents: 500000, AvailableCents: 400000, EncryptedBaseURL: "bad-enc", EncryptedAPIKey: "bad-enc", CredentialVersion: 1, RealModelName: "gpt-4o", ModelRatio: "1000"}).Error)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/bad-cred","messages":[]}`))
	ctx.Set("id", 7)
	// 记录调用前活跃计数基线：全局计数可能被其他 relay 路径测试泄漏，只断言本次调用不新增泄漏喵。
	var createdModel model.UserUpstreamModel
	require.NoError(t, testDB.Where("normalized_name = ?", "bad-cred").First(&createdModel).Error)
	selfBefore, sharedBefore := GetUpstreamModelActiveCount(createdModel.ID)

	require.False(t, handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/bad-cred"}))
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)

	// 预扣已全额退还：余额与可用额度回到初始值喵。
	var settled model.UserUpstreamModel
	require.NoError(t, testDB.Where("normalized_name = ?", "bad-cred").First(&settled).Error)
	require.Equal(t, int64(500000), settled.BalanceCents)
	require.Equal(t, int64(400000), settled.AvailableCents)
	// 活跃计数回到基线：解密失败已退出活跃，本次调用不新增并发占用喵。
	self, shared := GetUpstreamModelActiveCount(settled.ID)
	require.Equal(t, selfBefore, self)
	require.Equal(t, sharedBefore, shared)
}

// TestHandleUserUpstreamModelRequestVirtualFailureLog 验证虚拟模型 user/xxx 候选失败（passthrough）写 type=9 日志喵。
func TestHandleUserUpstreamModelRequestVirtualFailureLog(t *testing.T) {
	// 开发模式允许本地 http mock 上游，测试结束后自动恢复环境变量喵。
	t.Setenv("VIRTUAL_MODEL_INSECURE_UPSTREAM", "1")
	// 配置随机测试主密钥，使凭据加解密全链路可跑通喵。
	t.Setenv(virtualmodelservice.CredentialMasterKeyEnvironmentName, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	gin.SetMode(gin.TestMode)
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, testDB.AutoMigrate(&model.UserUpstreamModel{}, &model.EntityProbeState{}, &model.Log{}))
	oldDB := model.DB
	oldLogDB := model.LOG_DB
	model.DB = testDB
	model.LOG_DB = testDB
	defer func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
	}()

	// mock 上游返回 429 限流错误，正文为 OpenAI 风格错误喵。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited by upstream","type":"rate_limit_error"}}`))
	}))
	defer upstream.Close()

	baseURLCipher, version, encryptError := virtualmodelservice.EncryptCredential(upstream.URL)
	require.NoError(t, encryptError)
	apiKeyCipher, _, apiKeyError := virtualmodelservice.EncryptCredential("sk-test")
	require.NoError(t, apiKeyError)
	require.NoError(t, testDB.Create(&model.UserUpstreamModel{
		OwnerUserID:          7,
		NormalizedName:       "demo",
		DisplayName:          "Demo",
		Enabled:              true,
		EncryptedBaseURL:     baseURLCipher,
		EncryptedAPIKey:      apiKeyCipher,
		CredentialVersion:    version,
		RealModelName:        "gpt-4o",
		BalanceCents:         1000,
		AvailableCents:       800,
		AuthStyle:            "bearer",
		ModelRatio:           "1",
		CompletionRatio:      "1",
		CacheRatio:           "1",
		CacheCreationRatio:   "1",
		CacheCreation5mRatio: "1",
		CacheCreation1hRatio: "1",
		ImageRatio:           "1",
		AudioRatio:           "1",
		AudioCompletionRatio: "1",
		Version:              1,
	}).Error)

	// 虚拟执行状态：单候选 user/demo，全局兜底规则把限流直接透传喵。
	executionState := newUpstreamFailureTestState(
		[]model.VirtualModelCandidateSnapshot{
			{CandidateID: 71, VirtualModelID: 9, StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true, MaxRetries: 0, GroupName: "default", RealModelName: "user/demo"},
		},
		nil,
		[]model.VirtualModelFailureRule{
			{HTTPStatus: http.StatusTooManyRequests, Action: model.VirtualModelActionPassthrough},
		},
	)
	executionState.startTime = time.Now().Add(-2 * time.Second)
	ctx, _, recorder := newUpstreamFailureTestContext(executionState)
	// 标记虚拟模型日志归入 type=9 并注入候选序号，与真实虚拟模型请求链路一致喵。
	common.SetContextKey(ctx, constant.ContextKeyVirtualModelName, "vm-test")
	common.SetContextKey(ctx, constant.ContextKeyVirtualLogType, model.LogTypeVirtualModel)
	common.SetContextKey(ctx, constant.ContextKeyVirtualCandidateSeq, 1)

	handled := handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/demo", Group: "default"})
	require.False(t, handled)
	require.Equal(t, http.StatusTooManyRequests, recorder.Code)

	// 虚拟模型失败日志归入 type=9，内容含上游错误摘要喵。
	var failureLog model.Log
	require.NoError(t, testDB.Where("type = ? AND model_name = ?", model.LogTypeVirtualModel, "user/demo").First(&failureLog).Error)
	require.Contains(t, failureLog.Content, "rate limited by upstream")
	var failureOther map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(failureLog.Other, &failureOther))
	require.Equal(t, "rate_limited", failureOther["error_class"])
	require.Equal(t, false, failureOther["final_success"])
}
