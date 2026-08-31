package service

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// estimatedPromptTextLimit 提取 prompt 文本的最大字符数，防止超大请求体拖慢计数喵。
const estimatedPromptTextLimit = 512 * 1024

// EstimateUsageFromTexts 在上游未返回 token 计数量时，根据请求与响应文本构造估计 usage 参与计费喵。
// modelName 用于选择计数模型（OpenAI 文本模型走 tiktoken，其余启发式估计）；全零时返回 nil 保持「无计费信息」语义喵。
// prompt 复用 new-api 原生计数口径（EstimatePromptTokensFromBody），completion 与原生通道一致：
// 流式用 EstimateTokenByModel 启发式，非流式用 CountTextToken（OpenAI 文本模型走 tiktoken）喵。
func EstimateUsageFromTexts(c *gin.Context, modelName string, requestBody []byte, responseText string, isStream bool) *dto.Usage {
	// 喵~防御：请求体与响应文本都为空时无任何可估计素材，返回 nil 喵。
	if len(requestBody) == 0 && len(responseText) == 0 {
		return nil
	}
	// prompt 按 new-api 原生口径估算（含消息/工具开销与媒体 token），语义与上游返回的输入 token 一致喵。
	promptTokens := EstimatePromptTokensFromBody(c, requestBody, modelName, isStream)
	// completion 取自响应文本，计数方式与原生通道一致：流式启发式、非流式 OpenAI 文本模型走 tiktoken 喵。
	var completionTokens int
	if isStream {
		completionTokens = EstimateTokenByModel(modelName, responseText)
	} else {
		completionTokens = CountTextToken(responseText, modelName)
	}
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

// EstimatePromptTokensFromBody 按 new-api 原生计数口径（EstimateRequestToken）估算请求体的 prompt token 数喵。
// 复用 dto.GeneralOpenAIRequest.GetTokenCountMeta() 提取消息/工具/媒体元数据，再走与原生一致的数学：
// CountTextToken(文本) + 工具定义*8 + 消息条数*3 + 角色名*3 + 3（OpenAI 聊天格式），媒体按固定常量近似喵。
// Anthropic /v1/messages 与无法按 OpenAI 聊天解析的请求体回退纯文本计数，保证任何请求体都能安全估算喵。
func EstimatePromptTokensFromBody(c *gin.Context, requestBody []byte, modelName string, isStream bool) int {
	// 喵~防御：空请求体无法估算，返回 0 喵。
	if len(requestBody) == 0 {
		return 0
	}
	// 喵~防御：上下文缺失时回退纯文本计数，避免空指针喵。
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return CountTextToken(promptTextFromRequestBody(requestBody), modelName)
	}
	// 路径以 /v1/messages 结尾时为 Anthropic 格式：原生对 Claude 请求不加 OpenAI 消息开销，直接按文本计数喵。
	if strings.HasSuffix(strings.TrimSpace(c.Request.URL.Path), "/v1/messages") {
		return CountTextToken(promptTextFromRequestBody(requestBody), modelName)
	}
	var request dto.GeneralOpenAIRequest
	// 喵~防御：请求体非法或无法解析时回退纯文本计数喵。
	if err := common.Unmarshal(requestBody, &request); err != nil {
		return CountTextToken(promptTextFromRequestBody(requestBody), modelName)
	}
	// 非 OpenAI 聊天请求（messages/prompt/input 全缺）时回退纯文本计数，避免把格式开销算进无关请求喵。
	if len(request.Messages) == 0 && request.Prompt == nil && request.Input == nil {
		return CountTextToken(promptTextFromRequestBody(requestBody), modelName)
	}
	meta := request.GetTokenCountMeta()
	// 文本 token：OpenAI 文本模型走 tiktoken，其余厂商启发式，与原生 CountTextToken 一致喵。
	tokens := CountTextToken(meta.CombineText, modelName)
	// OpenAI 消息格式开销：工具定义 + 消息条数 + 角色名 + 基础 pad，与 EstimateRequestToken 完全相同喵。
	tokens += meta.ToolsCount*8 + meta.MessagesCount*3 + meta.NameCount*3 + 3
	// 媒体 token：按原生固定常量近似（图片取 520 为原生非 OpenAI 文本模型兜底值）。
	// 主人注意：此处不触发 URL 图片的网络拉取与尺寸解码，避免结算阶段引入额外延迟，图片 token 为近似值喵。
	for _, file := range meta.Files {
		switch file.FileType {
		case types.FileTypeAudio:
			tokens += 256
		case types.FileTypeVideo:
			tokens += 4096 * 2
		case types.FileTypeFile:
			tokens += 4096
		default:
			tokens += 520
		}
	}
	return tokens
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
