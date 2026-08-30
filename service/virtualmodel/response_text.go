package virtualmodel

import (
	"bufio"
	"bytes"
	"strings"

	"github.com/tidwall/gjson"
)

// estimatedResponseTextLimit 累积响应文本的最大字符数，防止超长流耗尽内存喵。
const estimatedResponseTextLimit = 256 * 1024

// appendResponseContentFromLine 从单条 SSE data 行提取文本增量并追加到有界 builder 喵。
// 格式容忍：命中 OpenAI delta/tool_calls、Anthropic delta、Responses delta 任一即提取，未知格式跳过喵。
func appendResponseContentFromLine(builder *strings.Builder, lineBytes []byte) {
	// 喵~防御：空 builder 或非 data 行直接返回喵。
	if builder == nil {
		return
	}
	trimmedLine := bytes.TrimSpace(lineBytes)
	if !bytes.HasPrefix(trimmedLine, []byte("data:")) {
		return
	}
	dataPayload := bytes.TrimSpace(trimmedLine[len("data:"):])
	// 喵~防御：空载荷或 [DONE] 事件不含内容喵。
	if len(dataPayload) == 0 || bytes.Equal(dataPayload, []byte("[DONE]")) {
		return
	}
	if !gjson.ValidBytes(dataPayload) {
		return
	}
	appendResponseContentFromPayload(builder, dataPayload)
}

// appendResponseContentFromPayload 从单个 JSON 载荷按各厂商字段提取内容文本喵。
func appendResponseContentFromPayload(builder *strings.Builder, dataPayload []byte) {
	// OpenAI Chat 增量 delta.content 喵。
	if content := gjson.GetBytes(dataPayload, "choices.0.delta.content"); content.Exists() && content.Type == gjson.String {
		builder.WriteString(content.String())
		return
	}
	// OpenAI Chat 推理增量 delta.reasoning_content 喵。
	if reasoning := gjson.GetBytes(dataPayload, "choices.0.delta.reasoning_content"); reasoning.Exists() && reasoning.Type == gjson.String {
		builder.WriteString(reasoning.String())
		return
	}
	// 非流式 OpenAI message.content（字符串形式）喵。
	if content := gjson.GetBytes(dataPayload, "choices.0.message.content"); content.Exists() && content.Type == gjson.String {
		builder.WriteString(content.String())
		return
	}
	// OpenAI Completions choices.0.text 喵。
	if text := gjson.GetBytes(dataPayload, "choices.0.text"); text.Exists() && text.Type == gjson.String {
		builder.WriteString(text.String())
		return
	}
	// OpenAI 工具调用：函数名与参数合拼，供计费近似喵。
	if toolCalls := gjson.GetBytes(dataPayload, "choices.0.delta.tool_calls"); toolCalls.Exists() && toolCalls.IsArray() {
		for _, toolCall := range toolCalls.Array() {
			builder.WriteString(toolCall.Get("function.name").String())
			builder.WriteString(toolCall.Get("function.arguments").String())
		}
		return
	}
	// OpenAI Responses 文本增量事件喵。
	if gjson.GetBytes(dataPayload, "type").String() == "response.output_text.delta" {
		if delta := gjson.GetBytes(dataPayload, "delta"); delta.Exists() && delta.Type == gjson.String {
			builder.WriteString(delta.String())
		}
		return
	}
	// Anthropic content_block_delta 文本增量喵。
	if gjson.GetBytes(dataPayload, "type").String() == "content_block_delta" {
		if deltaText := gjson.GetBytes(dataPayload, "delta.text"); deltaText.Exists() && deltaText.Type == gjson.String {
			builder.WriteString(deltaText.String())
		}
		// Anthropic 思考增量（thinking_delta）：长思考模型的推理文本也计入 completion 估算素材，避免输出被估成 0 喵。
		if thinkingText := gjson.GetBytes(dataPayload, "delta.thinking"); thinkingText.Exists() && thinkingText.Type == gjson.String {
			builder.WriteString(thinkingText.String())
		}
	}
}

// appendResponseContentFromSSEBytes 从已缓冲的 SSE 文本逐行提取内容增量喵。
func appendResponseContentFromSSEBytes(builder *strings.Builder, buffered []byte) {
	// 喵~防御：空缓冲或空 builder 直接返回喵。
	if builder == nil || len(buffered) == 0 {
		return
	}
	scanner := bufio.NewScanner(bytes.NewReader(buffered))
	scanner.Buffer(make([]byte, 4096), userUpstreamStreamLineLimit)
	for scanner.Scan() {
		appendResponseContentFromLine(builder, scanner.Bytes())
	}
	// 喵~防御：有界截断，防止超长响应文本耗尽内存喵。
	limitEstimatedResponseTextBuilder(builder)
}

// responseContentFromBody 从非流式响应体提取文本内容喵。
// 命中 OpenAI message.content/choices.0.text、Anthropic content[].text，失败时回退原始 body 喵。
func responseContentFromBody(body []byte) string {
	// 喵~防御：空正文直接返回空字符串喵。
	if len(body) == 0 {
		return ""
	}
	if gjson.ValidBytes(body) {
		// OpenAI 数组型 message.content 只取 text part 喵。
		if content := gjson.GetBytes(body, "choices.0.message.content"); content.Exists() {
			if content.Type == gjson.String {
				return limitEstimatedResponseText(content.String())
			}
			if content.IsArray() {
				var builder strings.Builder
				for _, part := range content.Array() {
					// 只累计文本类型 part，图片/音频 part 不参与文本估计喵。
					if part.Get("type").String() == "text" {
						builder.WriteString(part.Get("text").String())
					}
				}
				return limitEstimatedResponseText(builder.String())
			}
		}
		// OpenAI Completions choices.0.text 喵。
		if text := gjson.GetBytes(body, "choices.0.text"); text.Exists() && text.Type == gjson.String {
			return limitEstimatedResponseText(text.String())
		}
		// Anthropic content 数组文本块喵。
		if contentArray := gjson.GetBytes(body, "content"); contentArray.Exists() && contentArray.IsArray() {
			var builder strings.Builder
			for _, block := range contentArray.Array() {
				// 只累计文本块，thinking/工具调用块不参与文本估计喵。
				if block.Get("type").String() == "text" {
					builder.WriteString(block.Get("text").String())
				}
			}
			if builder.Len() > 0 {
				return limitEstimatedResponseText(builder.String())
			}
		}
	}
	// 兜底：无法解析时对原始 body 计数，含 JSON 结构开销喵。
	return limitEstimatedResponseText(string(body))
}

// limitEstimatedResponseText 截断超长响应文本到安全上限喵。
func limitEstimatedResponseText(text string) string {
	// 喵~防御：仅在超过上限时截断，避免拷贝短文本喵。
	if len(text) > estimatedResponseTextLimit {
		return text[:estimatedResponseTextLimit]
	}
	return text
}

// limitEstimatedResponseTextBuilder 截断 builder 内容到安全上限喵。
func limitEstimatedResponseTextBuilder(builder *strings.Builder) {
	// 喵~防御：仅在超过上限时截断，避免拷贝短文本喵。
	if builder.Len() > estimatedResponseTextLimit {
		truncated := builder.String()[:estimatedResponseTextLimit]
		builder.Reset()
		builder.WriteString(truncated)
	}
}
