package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newEstimateTestContext 构造带 OpenAI 聊天路径的测试上下文喵。
// 路径 /v1/chat/completions 让原生计数助手走 OpenAI 消息开销口径喵。
func newEstimateTestContext(t *testing.T) *gin.Context {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return ctx
}

// TestEstimateUsageFromTextsMessagesAndResponse 验证 messages 提取 prompt、响应文本估计 completion 喵。
func TestEstimateUsageFromTextsMessagesAndResponse(t *testing.T) {
	requestBody := []byte(`{"model":"qwen-max","messages":[{"role":"user","content":"你好，帮我写一个排序函数"}]}`)
	// 响应文本为中文，启发式计数对 CJK 必返回正数喵。
	responseText := "当然可以，以下是一个冒泡排序的实现。"
	estimated := EstimateUsageFromTexts(newEstimateTestContext(t), "qwen-max", requestBody, responseText, false)
	require.NotNil(t, estimated)
	// 估计 usage 必须打上 Estimated 标记，供日志与前端「?」展示喵。
	require.NotNil(t, estimated.BillingUsage)
	assert.True(t, estimated.BillingUsage.Estimated)
	// 两端 token 都必须为正，且总数等于两者之和喵。
	assert.Positive(t, estimated.PromptTokens)
	assert.Positive(t, estimated.CompletionTokens)
	assert.Equal(t, estimated.PromptTokens+estimated.CompletionTokens, estimated.TotalTokens)
}

// TestEstimateUsageFromTextsAllEmpty 验证请求体与响应文本都为空时返回 nil，保持「无计费信息」语义喵。
func TestEstimateUsageFromTextsAllEmpty(t *testing.T) {
	estimated := EstimateUsageFromTexts(newEstimateTestContext(t), "qwen-max", nil, "", false)
	// 无可估计素材时必须返回 nil，让前端继续显示「-」喵。
	assert.Nil(t, estimated)
	estimated = EstimateUsageFromTexts(newEstimateTestContext(t), "qwen-max", []byte{}, "", false)
	assert.Nil(t, estimated)
}

// TestEstimateUsageFromTextsPromptFallback 验证无 messages 时回退 prompt 字段（OpenAI completions 风格）喵。
func TestEstimateUsageFromTextsPromptFallback(t *testing.T) {
	requestBody := []byte(`{"model":"text-davinci-003","prompt":"写一首关于猫的诗"}`)
	estimated := EstimateUsageFromTexts(newEstimateTestContext(t), "qwen-max", requestBody, "喵喵喵，小猫真好。", false)
	require.NotNil(t, estimated)
	// 回退 prompt 字段后 prompt token 必须为正喵。
	assert.Positive(t, estimated.PromptTokens)
	assert.Positive(t, estimated.CompletionTokens)
}

// TestEstimateUsageFromTextsMultimodalOnlyText 验证 messages 数组内容只取 text part，图片 part 不参与文本估计喵。
func TestEstimateUsageFromTextsMultimodalOnlyText(t *testing.T) {
	requestBody := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":[{"type":"text","text":"描述这张图"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}]}`)
	estimated := EstimateUsageFromTexts(newEstimateTestContext(t), "qwen-max", requestBody, "这是一只可爱的猫。", false)
	require.NotNil(t, estimated)
	// 只累计文本 part 的 prompt token 必须为正喵。
	assert.Positive(t, estimated.PromptTokens)
}

// TestEstimateUsageFromTextsEmptyResponse 验证响应文本为空但请求体非空时仍返回 prompt 估计喵。
func TestEstimateUsageFromTextsEmptyResponse(t *testing.T) {
	requestBody := []byte(`{"model":"qwen-max","messages":[{"role":"user","content":"ping"}]}`)
	estimated := EstimateUsageFromTexts(newEstimateTestContext(t), "qwen-max", requestBody, "", false)
	require.NotNil(t, estimated)
	// prompt 来自请求体必须为正，completion 无文本保持零喵。
	assert.Positive(t, estimated.PromptTokens)
	assert.Equal(t, 0, estimated.CompletionTokens)
}

// TestEstimateUsageFromTextsClaudeModel 验证 claude 模型名走独立估算权重，非 OpenAI 模型不触发 tiktoken 喵。
func TestEstimateUsageFromTextsClaudeModel(t *testing.T) {
	requestBody := []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hello world"}]}`)
	estimated := EstimateUsageFromTexts(newEstimateTestContext(t), "claude-sonnet-4", requestBody, "hi there", false)
	require.NotNil(t, estimated)
	// claude 权重对英文文本同样返回正数喵。
	assert.Positive(t, estimated.PromptTokens)
	assert.Positive(t, estimated.CompletionTokens)
}

// TestEstimatePromptTokensFromBodyNativeOverhead 验证 OpenAI 聊天请求走原生计数口径，消息/工具/角色名开销被计入喵。
func TestEstimatePromptTokensFromBodyNativeOverhead(t *testing.T) {
	// 2 条消息 + 1 工具 + 1 角色名的 OpenAI 聊天请求喵。
	requestBody := []byte(`{"model":"qwen-max","messages":[{"role":"system","name":"猫娘","content":"你是猫娘助手"},{"role":"user","content":"你好"}],"tools":[{"type":"function","function":{"name":"search","description":"搜索","parameters":{"type":"object","properties":{}}}}]}`)
	var request dto.GeneralOpenAIRequest
	require.NoError(t, common.Unmarshal(requestBody, &request))
	meta := request.GetTokenCountMeta()
	// 原生口径 = 文本 token + 工具*8 + 消息条数*3 + 角色名*3 + 基础 3 喵。
	textTokens := CountTextToken(meta.CombineText, "qwen-max")
	expected := textTokens + meta.ToolsCount*8 + meta.MessagesCount*3 + meta.NameCount*3 + 3
	got := EstimatePromptTokensFromBody(newEstimateTestContext(t), requestBody, "qwen-max", false)
	// 必须等于原生公式，且严格大于纯文本 token（证明开销被计入）喵。
	assert.Equal(t, expected, got)
	assert.Greater(t, got, textTokens)
}

// TestEstimatePromptTokensFromBodyAnthropicPath 验证 /v1/messages 路径不加 OpenAI 消息开销，按纯文本计数喵。
func TestEstimatePromptTokensFromBodyAnthropicPath(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	requestBody := []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"你好"}]}`)
	expected := CountTextToken(promptTextFromRequestBody(requestBody), "claude-sonnet-4")
	assert.Equal(t, expected, EstimatePromptTokensFromBody(ctx, requestBody, "claude-sonnet-4", false))
}

// TestEstimatePromptTokensFromBodyFallbacks 验证空上下文与非法请求体回退纯文本计数，且不 panic 喵。
func TestEstimatePromptTokensFromBodyFallbacks(t *testing.T) {
	requestBody := []byte(`{"model":"qwen-max","messages":[{"role":"user","content":"你好"}]}`)
	// 空上下文必须安全回退，不得空指针喵。
	assert.NotPanics(t, func() {
		_ = EstimatePromptTokensFromBody(nil, requestBody, "qwen-max", false)
	})
	// 非法请求体回退纯文本计数喵。
	invalidBody := []byte(`{invalid`)
	expected := CountTextToken(promptTextFromRequestBody(invalidBody), "qwen-max")
	assert.Equal(t, expected, EstimatePromptTokensFromBody(newEstimateTestContext(t), invalidBody, "qwen-max", false))
}

// TestEstimateUsageFromTextsCompletionMode 验证流式/非流式 completion 分别走启发式与 CountTextToken 口径喵。
// 非 OpenAI 文本模型时 CountTextToken 内部回退 EstimateTokenByModel，因此两种口径结果一致喵。
func TestEstimateUsageFromTextsCompletionMode(t *testing.T) {
	requestBody := []byte(`{"model":"qwen-max","messages":[{"role":"user","content":"你好"}]}`)
	responseText := "喵喵喵，你好呀。"
	streamUsage := EstimateUsageFromTexts(newEstimateTestContext(t), "qwen-max", requestBody, responseText, true)
	nonStreamUsage := EstimateUsageFromTexts(newEstimateTestContext(t), "qwen-max", requestBody, responseText, false)
	require.NotNil(t, streamUsage)
	require.NotNil(t, nonStreamUsage)
	assert.Equal(t, EstimateTokenByModel("qwen-max", responseText), streamUsage.CompletionTokens)
	assert.Equal(t, CountTextToken(responseText, "qwen-max"), nonStreamUsage.CompletionTokens)
}

// TestEstimateUsageFromTextsPromptUsesNativeOverhead 验证估算 prompt 计入原生消息开销，严格大于纯文本计数喵。
func TestEstimateUsageFromTextsPromptUsesNativeOverhead(t *testing.T) {
	requestBody := []byte(`{"model":"qwen-max","messages":[{"role":"user","content":"你好"}]}`)
	usage := EstimateUsageFromTexts(newEstimateTestContext(t), "qwen-max", requestBody, "喵", false)
	require.NotNil(t, usage)
	textOnly := CountTextToken(promptTextFromRequestBody(requestBody), "qwen-max")
	assert.Greater(t, usage.PromptTokens, textOnly)
}
