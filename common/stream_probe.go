package common

import (
	"strings"

	"github.com/tidwall/gjson"
)

// StreamProbeContentChars 估算一个 SSE 数据事件的内容大小喵。
// 注意：这里返回的是 UTF-8 字节数（len），并非 Unicode 字符数——对 CJK 文本每个汉字占 3 字节，
// 门槛语义按字节口径解读（与既有调用方一致），不要改成 RuneCountInString 以免改变探测放流时机喵。
// 优先提取常见流式格式的文本字段（OpenAI 聊天、Claude、Gemini、Responses、百度、Dify），
// 提取失败时回退到整个负载的可见字节数，保证非主流格式不会被探测永久阻塞喵。
func StreamProbeContentChars(data string) int {
	// 喵~防御：空负载按零处理，避免空事件被误认为有内容喵。
	if strings.TrimSpace(data) == "" {
		return 0
	}
	// 依次尝试常见格式的文本字段，命中即返回其内容字节数喵。
	// 推理增量（reasoning/thinking）同样视为业务内容：长思考模型若不算推理，
	// 探测会一直等回答正文才放流，把「首次写客户端」拖到流结束喵。
	contentFields := []string{
		"choices.0.delta.content",              // OpenAI 聊天流式内容增量喵。
		"choices.0.delta.reasoning_content",    // OpenAI 推理增量（DeepSeek R1 风格），长思考模型也视为业务内容喵。
		"choices.0.delta.reasoning",            // 部分中转站的推理增量字段别名喵。
		"choices.0.message.content",            // OpenAI 消息体内容喵。
		"choices.0.message.reasoning_content",  // OpenAI 消息体推理内容（伪流/非流式分片兼容）喵。
		"delta.text",                           // Claude 流式增量喵。
		"delta.thinking",                       // Claude 思考增量（thinking_delta 事件）喵。
		"delta.reasoning_content",              // 顶层 delta 推理增量（部分中转站无 choices 包裹）喵。
		"delta.reasoning",                      // 顶层 delta 推理别名喵。
		"candidates.0.content.parts.0.text",    // Gemini 流式文本喵。
		"output.0.content.0.text",              // OpenAI Responses 流式喵。
		"result",                               // 百度文心流式喵。
		"answer",                               // Dify 流式喵。
	}
	for _, field := range contentFields {
		if value := gjson.Get(data, field); value.Exists() && value.Type == gjson.String {
			if text := strings.TrimSpace(value.String()); text != "" {
				// len(text) 返回 UTF-8 字节数，是既有口径喵。
				return len(text)
			}
		}
	}
	// 兜底：无法识别格式时数可见字节数，避免未知格式被永久阻塞喵。
	visibleByteCount := 0
	for _, character := range data {
		if character > ' ' {
			visibleByteCount++
		}
	}
	return visibleByteCount
}
