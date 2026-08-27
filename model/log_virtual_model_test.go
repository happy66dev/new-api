package model

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newVirtualLogTestContext 构造带用户名与虚拟模型上下文的测试 Gin 上下文喵。
func newVirtualLogTestContext(t *testing.T) *gin.Context {
	t.Helper()
	// 创建测试上下文并注入用户名，保证日志写入用户名可识别喵。
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"virtual/demo","messages":[]}`))
	ctx.Set("username", "tester")
	// 标记虚拟模型日志类型与候选序号，模拟 internal 候选请求上下文喵。
	common.SetContextKey(ctx, constant.ContextKeyVirtualLogType, LogTypeVirtualModel)
	common.SetContextKey(ctx, constant.ContextKeyVirtualCandidateSeq, 2)
	return ctx
}

// TestRecordConsumeLogVirtualModelType 验证虚拟模型 internal 候选走消费日志时覆盖为 type=9 并写候选序号渠道喵。
func TestRecordConsumeLogVirtualModelType(t *testing.T) {
	// 清空日志表保证断言独立喵。
	require.NoError(t, LOG_DB.Exec("DELETE FROM logs").Error)

	ctx := newVirtualLogTestContext(t)
	// 注入候选尝试序列，验证 internal 成功尝试被追加且最终日志携带 candidates 喵。
	attempts := []VirtualModelCandidateAttemptRecord{
		{Seq: 1, CandidateID: 11, Source: "custom", Label: "up-a", Success: false, StatusCode: 429, ErrorClass: "rate_limited", ErrorMessage: "rate_limited"},
	}
	common.SetContextKey(ctx, constant.ContextKeyVirtualCandidateAttempts, &attempts)

	// 记录一条消费日志，期望被虚拟模型上下文覆盖为 type=9、渠道字段为候选序号 2 喵。
	RecordConsumeLog(ctx, 7, RecordConsumeLogParams{
		ChannelId:        99,
		PromptTokens:     10,
		CompletionTokens: 20,
		ModelName:        "gpt-4o",
		Quota:            300,
		Group:            "default",
		Other:            map[string]interface{}{"prompt_tokens": 10},
		// 请求级毫秒耗时与首字耗时随 internal 成功尝试写入候选序列喵。
		UseTimeMs:   1234,
		FirstByteMs: 300,
	})

	var log Log
	require.NoError(t, LOG_DB.Last(&log).Error)
	// 日志类型必须为虚拟模型，渠道字段为候选序号而非真实渠道 id 喵。
	assert.Equal(t, LogTypeVirtualModel, log.Type)
	assert.Equal(t, 2, log.ChannelId)
	// 候选尝试序列应注入 Other，且 internal 成功尝试被追加到末尾喵。
	var parsedOther map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &parsedOther))
	candidates, hasCandidates := parsedOther["candidates"].([]interface{})
	require.True(t, hasCandidates, "candidates should be injected into other")
	require.Len(t, candidates, 2)
	// internal 成功尝试携带请求级总耗时与首字耗时，供详情展示模型级耗时喵。
	internalSuccess := candidates[1].(map[string]interface{})
	require.Equal(t, true, internalSuccess["success"])
	require.Equal(t, float64(1234), internalSuccess["elapsed_ms"])
	require.Equal(t, float64(300), internalSuccess["ttft_ms"])
}

// TestRecordVirtualModelLogType 验证 RecordVirtualModelLog 固定写 type=9 喵。
func TestRecordVirtualModelLogType(t *testing.T) {
	// 清空日志表保证断言独立喵。
	require.NoError(t, LOG_DB.Exec("DELETE FROM logs").Error)

	ctx := newVirtualLogTestContext(t)
	RecordVirtualModelLog(ctx, 7, RecordVirtualModelLogParams{
		ModelName:      "virtual/demo",
		UseTimeSeconds: 3,
		IsStream:       true,
		Group:          "default",
		Other:          map[string]interface{}{"virtual_model": "virtual/demo", "final_success": true},
	})

	var log Log
	require.NoError(t, LOG_DB.Last(&log).Error)
	assert.Equal(t, LogTypeVirtualModel, log.Type)
	assert.Equal(t, "virtual/demo", log.ModelName)
}

// TestInjectVirtualModelAttempts 验证候选尝试序列注入与普通请求无副作用喵。
func TestInjectVirtualModelAttempts(t *testing.T) {
	// 普通上下文无尝试切片时注入不产生 candidates 字段喵。
	recorder := httptest.NewRecorder()
	plainCtx, _ := gin.CreateTestContext(recorder)
	plainCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	other := map[string]interface{}{"virtual_model": "virtual/demo"}
	InjectVirtualModelAttempts(plainCtx, other)
	_, hasCandidates := other["candidates"]
	assert.False(t, hasCandidates)

	// 虚拟模型上下文有尝试切片时注入 candidates 喵。
	ctx := newVirtualLogTestContext(t)
	attempts := []VirtualModelCandidateAttemptRecord{
		{Seq: 1, CandidateID: 5, Source: "internal", Label: "gpt-4o", Success: true, StatusCode: 200},
	}
	common.SetContextKey(ctx, constant.ContextKeyVirtualCandidateAttempts, &attempts)
	other = map[string]interface{}{"virtual_model": "virtual/demo"}
	InjectVirtualModelAttempts(ctx, other)
	// 喵~防御：空上下文与空 other 必须安全返回喵。
	InjectVirtualModelAttempts(nil, other)
	InjectVirtualModelAttempts(ctx, nil)
	injected, hasCandidates := other["candidates"]
	assert.True(t, hasCandidates)
	assert.Len(t, injected, 1)
}
