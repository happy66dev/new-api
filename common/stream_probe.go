package common

import (
	"strings"

	"github.com/tidwall/gjson"
)

// streamProbeContentFields 保存各类流式格式的文本字段路径，探测门槛与跳过候选文本还原共用同一张表喵。
// 推理增量（reasoning/thinking）同样视为业务内容：长思考模型若不算推理，
// 探测会一直等回答正文才放流，把「首次写客户端」拖到流结束喵。
var streamProbeContentFields = []string{
	"choices.0.delta.content",             // OpenAI 聊天流式内容增量喵。
	"choices.0.delta.reasoning_content",   // OpenAI 推理增量（DeepSeek R1 风格），长思考模型也视为业务内容喵。
	"choices.0.delta.reasoning",           // 部分中转站的推理增量字段别名喵。
	"choices.0.message.content",           // OpenAI 消息体内容喵。
	"choices.0.message.reasoning_content", // OpenAI 消息体推理内容（伪流/非流式分片兼容）喵。
	"delta.text",                          // Claude 流式增量喵。
	"delta.thinking",                      // Claude 思考增量（thinking_delta 事件）喵。
	"delta.reasoning_content",             // 顶层 delta 推理增量（部分中转站无 choices 包裹）喵。
	"delta.reasoning",                     // 顶层 delta 推理别名喵。
	"candidates.0.content.parts.0.text",   // Gemini 流式文本喵。
	"output.0.content.0.text",             // OpenAI Responses 流式喵。
	"result",                              // 百度文心流式喵。
	"answer",                              // Dify 流式喵。
}

// StreamProbeContentText 从一条 SSE 数据事件里还原出业务文本，供跳过候选按原生口径估算 token 喵。
//
// 用途喵：虚拟模型候选在放流前被判定卡流/空流/断流时会跳过，这些缓存内容从未交给 dataHandler，
// 但它们在原生 new-api 口径下同样会被计费，因此需要先把数据行还原成响应文本再估算 usage 喵。
// 输入：已剥掉 "data:" 前缀的上游 SSE 数据行喵。
// 输出：命中的文本内容；未命中任何已知格式时返回空串（宁可少估也不虚增用户费用）喵。
func StreamProbeContentText(data string) string {
	// 喵~防御：空负载没有文本可还原喵。
	if strings.TrimSpace(data) == "" {
		return ""
	}
	// 依次尝试常见格式的文本字段，命中即返回喵。
	for _, field := range streamProbeContentFields {
		if value := gjson.Get(data, field); value.Exists() && value.Type == gjson.String {
			if text := strings.TrimSpace(value.String()); text != "" {
				return text
			}
		}
	}
	// 喵~防御：未知格式不强行把 JSON 元数据当正文，避免把结构字段算成用户内容喵。
	return ""
}

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
	// 命中已知格式时直接用它还原出的文本字节数喵。
	if text := StreamProbeContentText(data); text != "" {
		// len(text) 返回 UTF-8 字节数，是既有口径喵。
		return len(text)
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
