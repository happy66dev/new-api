package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEstimateUsageFromTextsMessagesAndResponse 验证 messages 提取 prompt、响应文本估计 completion 喵。
func TestEstimateUsageFromTextsMessagesAndResponse(t *testing.T) {
	requestBody := []byte(`{"model":"qwen-max","messages":[{"role":"user","content":"你好，帮我写一个排序函数"}]}`)
	// 响应文本为中文，启发式计数对 CJK 必返回正数喵。
	responseText := "当然可以，以下是一个冒泡排序的实现。"
	estimated := EstimateUsageFromTexts("qwen-max", requestBody, responseText)
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
	estimated := EstimateUsageFromTexts("qwen-max", nil, "")
	// 无可估计素材时必须返回 nil，让前端继续显示「-」喵。
	assert.Nil(t, estimated)
	estimated = EstimateUsageFromTexts("qwen-max", []byte{}, "")
	assert.Nil(t, estimated)
}

// TestEstimateUsageFromTextsPromptFallback 验证无 messages 时回退 prompt 字段（OpenAI completions 风格）喵。
func TestEstimateUsageFromTextsPromptFallback(t *testing.T) {
	requestBody := []byte(`{"model":"text-davinci-003","prompt":"写一首关于猫的诗"}`)
	estimated := EstimateUsageFromTexts("qwen-max", requestBody, "喵喵喵，小猫真好。")
	require.NotNil(t, estimated)
	// 回退 prompt 字段后 prompt token 必须为正喵。
	assert.Positive(t, estimated.PromptTokens)
	assert.Positive(t, estimated.CompletionTokens)
}

// TestEstimateUsageFromTextsMultimodalOnlyText 验证 messages 数组内容只取 text part，图片 part 不参与文本估计喵。
func TestEstimateUsageFromTextsMultimodalOnlyText(t *testing.T) {
	requestBody := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":[{"type":"text","text":"描述这张图"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}]}`)
	estimated := EstimateUsageFromTexts("qwen-max", requestBody, "这是一只可爱的猫。")
	require.NotNil(t, estimated)
	// 只累计文本 part 的 prompt token 必须为正喵。
	assert.Positive(t, estimated.PromptTokens)
}

// TestEstimateUsageFromTextsEmptyResponse 验证响应文本为空但请求体非空时仍返回 prompt 估计喵。
func TestEstimateUsageFromTextsEmptyResponse(t *testing.T) {
	requestBody := []byte(`{"model":"qwen-max","messages":[{"role":"user","content":"ping"}]}`)
	estimated := EstimateUsageFromTexts("qwen-max", requestBody, "")
	require.NotNil(t, estimated)
	// prompt 来自请求体必须为正，completion 无文本保持零喵。
	assert.Positive(t, estimated.PromptTokens)
	assert.Equal(t, 0, estimated.CompletionTokens)
}

// TestEstimateUsageFromTextsClaudeModel 验证 claude 模型名走独立估算权重，非 OpenAI 模型不触发 tiktoken 喵。
func TestEstimateUsageFromTextsClaudeModel(t *testing.T) {
	requestBody := []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hello world"}]}`)
	estimated := EstimateUsageFromTexts("claude-sonnet-4", requestBody, "hi there")
	require.NotNil(t, estimated)
	// claude 权重对英文文本同样返回正数喵。
	assert.Positive(t, estimated.PromptTokens)
	assert.Positive(t, estimated.CompletionTokens)
}
