package service

import (
	"strings"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/tidwall/gjson"
)

// estimatedPromptTextLimit 提取 prompt 文本的最大字符数，防止超大请求体拖慢计数喵。
const estimatedPromptTextLimit = 512 * 1024

// EstimateUsageFromTexts 在上游未返回 token 计数量时，根据请求与响应文本构造估计 usage 参与计费喵。
// modelName 用于选择计数模型（OpenAI 文本模型走 tiktoken，其余启发式估计）；全零时返回 nil 保持「无计费信息」语义喵。
func EstimateUsageFromTexts(modelName string, requestBody []byte, responseText string) *dto.Usage {
	// 喵~防御：请求体与响应文本都为空时无任何可估计素材，返回 nil 喵。
	if len(requestBody) == 0 && len(responseText) == 0 {
		return nil
	}
	// prompt 取自请求体 messages 文本，completion 取自响应文本，语义与上游返回的 token 一致喵。
	promptTokens := CountTextToken(promptTextFromRequestBody(requestBody), modelName)
	completionTokens := EstimateTokenByModel(modelName, responseText)
	// 喵~防御：两边都估计不出 token 时返回 nil，与 usageHasTokens 语义一致（前端仍显示「-」）喵。
	if promptTokens <= 0 && completionTokens <= 0 {
		return nil
	}
	return &dto.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
		// Estimated 标记本 usage 由文本估算而来，供日志/前端识别「?」喵。
		BillingUsage: &dto.BillingUsage{Estimated: true},
	}
}

// promptTextFromRequestBody 从请求体提取对话文本用于 prompt token 估计喵。
// 优先 messages 数组的文本内容，缺失时回退 prompt 字段，再回退原始请求体喵。
func promptTextFromRequestBody(requestBody []byte) string {
	// 喵~防御：空请求体直接返回空字符串喵。
	if len(requestBody) == 0 {
		return ""
	}
	var builder strings.Builder
	messages := gjson.GetBytes(requestBody, "messages")
	// messages 数组存在时逐条提取角色与文本内容喵。
	if messages.Exists() && messages.IsArray() {
		for _, message := range messages.Array() {
			role := message.Get("role").String()
			content := message.Get("content")
			// 字符串内容直接追加；数组内容只取 type==text 的文本 part，图片/音频 part 无法文本估计喵。
			if content.Type == gjson.String {
				builder.WriteString(role)
				builder.WriteString(": ")
				builder.WriteString(content.String())
				builder.WriteString("\n")
				continue
			}
			if content.IsArray() {
				for _, part := range content.Array() {
					// 只累计文本类型 part，其余模态（图片/音频）不参与文本估计喵。
					if part.Get("type").String() == "text" {
						builder.WriteString(role)
						builder.WriteString(": ")
						builder.WriteString(part.Get("text").String())
						builder.WriteString("\n")
					}
				}
			}
		}
		if builder.Len() > 0 {
			return limitEstimatedPromptText(builder.String())
		}
	}
	// messages 缺失时回退 prompt 字段（OpenAI completions 单字符串）喵。
	if prompt := gjson.GetBytes(requestBody, "prompt"); prompt.Exists() && prompt.Type == gjson.String {
		return limitEstimatedPromptText(prompt.String())
	}
	// 兜底：对原始请求体本身计数，含 JSON 结构开销喵。
	return limitEstimatedPromptText(string(requestBody))
}

// limitEstimatedPromptText 截断超长 prompt 文本到安全上限，避免计数拖慢请求喵。
func limitEstimatedPromptText(text string) string {
	// 喵~防御：仅在超过上限时才截断，避免拷贝短文本喵。
	if len(text) > estimatedPromptTextLimit {
		return text[:estimatedPromptTextLimit]
	}
	return text
}
