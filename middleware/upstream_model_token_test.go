package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	virtualmodelservice "github.com/QuantumNous/new-api/service/virtualmodel"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestHandleUserUpstreamModelRequestTokenLog 验证 user/xxx 候选成功时日志 token 正确落库喵。
func TestHandleUserUpstreamModelRequestTokenLog(t *testing.T) {
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

	// mock 上游按 OpenAI 流式格式回包，末尾带 usage 事件喵。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":50,\"total_tokens\":150}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	// 用真实凭据加密生成模型条目，使解密与透传全链路可跑通喵。
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
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/demo","stream":true,"messages":[]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", 7)

	handled := handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/demo"})
	require.False(t, handled)
	require.Equal(t, http.StatusOK, recorder.Code)

	// 日志必须落库且携带真实 token 计数喵。
	var logRecord model.Log
	require.NoError(t, testDB.Where("type = ?", model.LogTypeCustomUpstream).First(&logRecord).Error)
	require.Equal(t, 100, logRecord.PromptTokens)
	require.Equal(t, 50, logRecord.CompletionTokens)
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
}

// TestHandleUserUpstreamModelRequestPassthroughUpstreamError 验证直调 user/xxx 时上游 HTTP 错误原样透传喵。
// 上游 4xx/5xx 必须透传其状态码与错误正文，而不是被替换为通用 unavailable 错误喵。
func TestHandleUserUpstreamModelRequestPassthroughUpstreamError(t *testing.T) {
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

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/demo","messages":[]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", 7)

	handled := handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/demo"})
	require.False(t, handled)
	// 上游 429 必须原样透传状态码与正文，而不是被替换为 502 unavailable 喵。
	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Contains(t, recorder.Body.String(), "rate limited by upstream")
	require.NotContains(t, recorder.Body.String(), "user upstream model is unavailable")

	// 失败也必须写 type=8 日志，内容含上游错误体摘要喵。
	var failureLog model.Log
	require.NoError(t, testDB.Where("type = ? AND model_name = ?", model.LogTypeCustomUpstream, "user/demo").First(&failureLog).Error)
	require.Contains(t, failureLog.Content, "rate limited by upstream")
	var failureOther map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(failureLog.Other, &failureOther))
	require.Equal(t, "rate_limited", failureOther["error_class"])
	require.Equal(t, float64(http.StatusTooManyRequests), failureOther["http_status"])
	require.Equal(t, false, failureOther["final_success"])
}

// TestHandleUserUpstreamModelRequestSseErrorPassthrough 验证直调 user/xxx 流式时上游 2xx 内嵌 SSE error 事件原样透传喵。
// 上游 HTTP 200 但在 SSE 流内报告 error 事件时，错误正文必须透传给客户端，而不是被替换为 502 unavailable 喵。
func TestHandleUserUpstreamModelRequestSseErrorPassthrough(t *testing.T) {
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

	// mock 上游返回 200 + SSE error 事件（流内业务错误），这是 OpenAI 兼容上游常见的错误形态喵。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"error\":{\"message\":\"upstream sse broken\",\"type\":\"invalid_request_error\"}}\n\n"))
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
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"user/demo","stream":true,"messages":[]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", 7)

	handled := handleUserUpstreamModelRequest(ctx, &ModelRequest{Model: "user/demo"})
	require.False(t, handled)
	// 上游 SSE error 事件必须原样透传（HTTP 200 + 错误正文），而不是被替换为 502 unavailable 喵。
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "upstream sse broken")
	require.NotContains(t, recorder.Body.String(), "user upstream model is unavailable")

	// 失败也必须写 type=8 日志，内容含上游 SSE 错误事件摘要喵。
	var failureLog model.Log
	require.NoError(t, testDB.Where("type = ? AND model_name = ?", model.LogTypeCustomUpstream, "user/demo").First(&failureLog).Error)
	require.Contains(t, failureLog.Content, "upstream sse broken")
	var failureOther map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(failureLog.Other, &failureOther))
	require.Equal(t, "upstream_error", failureOther["error_class"])
	require.Equal(t, float64(http.StatusOK), failureOther["http_status"])
	require.Equal(t, false, failureOther["final_success"])
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
