package virtualmodel

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAppendResponseContentFromLineOpenAIDelta 验证 OpenAI 流式 delta.content 增量被提取喵。
func TestAppendResponseContentFromLineOpenAIDelta(t *testing.T) {
	var builder strings.Builder
	// 标准 OpenAI 流式 chunk，delta.content 为文本增量喵。
	appendResponseContentFromLine(&builder, []byte(`data: {"choices":[{"delta":{"content":"你好"}}]}`))
	// 非 data 行必须被忽略，不污染文本喵。
	appendResponseContentFromLine(&builder, []byte(`event: message_delta`))
	// 空载荷与 [DONE] 事件不产生内容喵。
	appendResponseContentFromLine(&builder, []byte(`data: [DONE]`))
	// 提取结果应只含 OpenAI 文本增量喵。
	assert.Equal(t, "你好", builder.String())
}

// TestAppendResponseContentFromLineAnthropicDelta 验证 Anthropic content_block_delta 文本增量被提取喵。
func TestAppendResponseContentFromLineAnthropicDelta(t *testing.T) {
	var builder strings.Builder
	// Anthropic 流式文本增量事件，delta.text 为文本喵。
	appendResponseContentFromLine(&builder, []byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"好的"}}`))
	// 非文本增量（工具调用开始）不得计入文本喵。
	appendResponseContentFromLine(&builder, []byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta"}}`))
	assert.Equal(t, "好的", builder.String())
}

// TestAppendResponseContentFromLineDeepSeekReasoning 验证 DeepSeek 推理增量被提取，不被空 content 截断喵。
func TestAppendResponseContentFromLineDeepSeekReasoning(t *testing.T) {
	var builder strings.Builder
	// 推理 chunk：content 为空但 reasoning_content 有文本，此前会被空 content 直接 return 跳过喵。
	appendResponseContentFromLine(&builder, []byte(`data: {"choices":[{"delta":{"reasoning_content":"深入思考","content":""}}]}`))
	// 纯 reasoning 块（无 content 字段）也应提取喵。
	appendResponseContentFromLine(&builder, []byte(`data: {"choices":[{"delta":{"reasoning_content":"再想想"}}]}`))
	// 答案块：content 非空正常提取喵。
	appendResponseContentFromLine(&builder, []byte(`data: {"choices":[{"delta":{"content":"结论","reasoning_content":""}}]}`))
	// 推理与答案文本都应进入响应文本，供 completion 估算避免输出为 0 喵。
	assert.Equal(t, "深入思考再想想结论", builder.String())
}

// TestAppendResponseContentFromLineToolCalls 验证 OpenAI 工具调用参数被合拼供计费近似喵。
func TestAppendResponseContentFromLineToolCalls(t *testing.T) {
	var builder strings.Builder
	// 工具调用增量：函数名与参数片段合拼进文本喵。
	appendResponseContentFromLine(&builder, []byte(`data: {"choices":[{"delta":{"tool_calls":[{"function":{"name":"get_weather","arguments":"{\"city\":\"北京\"}"}}]}}]}`))
	// 断言函数名与参数都被计入，供 token 估计使用喵。
	assert.Contains(t, builder.String(), "get_weather")
	assert.Contains(t, builder.String(), `"city":"北京"`)
}

// TestAppendResponseContentFromLineResponsesDelta 验证 OpenAI Responses 文本增量事件被提取喵。
func TestAppendResponseContentFromLineResponsesDelta(t *testing.T) {
	var builder strings.Builder
	// Responses 风格增量事件，type 为 response.output_text.delta 喵。
	appendResponseContentFromLine(&builder, []byte(`data: {"type":"response.output_text.delta","delta":"片段"}`))
	assert.Equal(t, "片段", builder.String())
}

// TestAppendResponseContentFromSSEBytesMixed 验证多事件混合流逐行提取并累积喵。
func TestAppendResponseContentFromSSEBytesMixed(t *testing.T) {
	var builder strings.Builder
	// 混合流：OpenAI 增量 + Anthropic 增量 + 元数据事件，顺序拼接喵。
	appendResponseContentFromSSEBytes(&builder, []byte("data: {\"choices\":[{\"delta\":{\"content\":\"A\"}}]}\n\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"B\"}}\n\n"))
	assert.Equal(t, "AB", builder.String())
}

// TestResponseContentFromBodyOpenAIString 验证非流式 OpenAI 字符串 message.content 被提取喵。
func TestResponseContentFromBodyOpenAIString(t *testing.T) {
	body := []byte(`{"id":"1","object":"chat.completion","choices":[{"message":{"content":"结果文本"}}]}`)
	// 字符串形式直接作为响应文本喵。
	assert.Equal(t, "结果文本", responseContentFromBody(body))
}

// TestResponseContentFromBodyOpenAIArrayParts 验证非流式 OpenAI 数组 message.content 只取 text part 喵。
func TestResponseContentFromBodyOpenAIArrayParts(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":[{"type":"text","text":"正文"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AA"}}]}}]}`)
	// 图片 part 不参与文本估计，只保留 text part 喵。
	assert.Equal(t, "正文", responseContentFromBody(body))
}

// TestResponseContentFromBodyAnthropic 验证非流式 Anthropic content 数组文本块被提取喵。
func TestResponseContentFromBodyAnthropic(t *testing.T) {
	body := []byte(`{"content":[{"type":"text","text":"第一段"},{"type":"tool_use","name":"x"},{"type":"text","text":"第二段"}]}`)
	// 只累计文本块，工具调用块不参与估计喵。
	assert.Equal(t, "第一段第二段", responseContentFromBody(body))
}

// TestResponseContentFromBodyInvalidFallback 验证非法 JSON 正文回退原始 body 计数喵。
func TestResponseContentFromBodyInvalidFallback(t *testing.T) {
	body := []byte(`this is not json`)
	// 无法解析时对原始正文计数，供估计兜底喵。
	assert.Equal(t, "this is not json", responseContentFromBody(body))
}

// TestLimitEstimatedResponseText 验证超长响应文本被截断到安全上限喵。
func TestLimitEstimatedResponseText(t *testing.T) {
	require.Greater(t, estimatedResponseTextLimit, 10)
	longText := strings.Repeat("猫", estimatedResponseTextLimit+10)
	// 截断后长度必须等于上限，防止超长流耗尽内存喵。
	assert.Len(t, limitEstimatedResponseText(longText), estimatedResponseTextLimit)
	// 短文本不被截断，避免无谓拷贝喵。
	assert.Equal(t, "短", limitEstimatedResponseText("短"))
}
