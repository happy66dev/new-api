package common

import (
	"strings"

	"github.com/tidwall/gjson"
)

// StreamProbeContentChars 估算一个 SSE 数据事件的“内容字符数”喵。
// 优先提取常见流式格式的文本字段（OpenAI 聊天、Claude、Gemini、Responses、百度、Dify），
// 提取失败时回退到整个负载的可见字符数，保证非主流格式不会被探测永久阻塞喵。
func StreamProbeContentChars(data string) int {
	// 喵~防御：空负载按零处理，避免空事件被误认为有内容喵。
	if strings.TrimSpace(data) == "" {
		return 0
	}
	// 依次尝试常见格式的文本字段，命中即返回其内容长度喵。
	contentFields := []string{
		"choices.0.delta.content",      // OpenAI 聊天流式增量喵。
		"choices.0.message.content",    // OpenAI 消息体内容喵。
		"delta.text",                   // Claude 流式增量喵。
		"candidates.0.content.parts.0.text", // Gemini 流式文本喵。
		"output.0.content.0.text",      // OpenAI Responses 流式喵。
		"result",                       // 百度文心流式喵。
		"answer",                       // Dify 流式喵。
	}
	for _, field := range contentFields {
		if value := gjson.Get(data, field); value.Exists() && value.Type == gjson.String {
			if text := strings.TrimSpace(value.String()); text != "" {
				return len(text)
			}
		}
	}
	// 兜底：无法识别格式时数可见字符数，避免未知格式被永久阻塞喵。
	visibleCharacterCount := 0
	for _, character := range data {
		if character > ' ' {
			visibleCharacterCount++
		}
	}
	return visibleCharacterCount
}
