package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
)

//func GetPromptTokens(textRequest dto.GeneralOpenAIRequest, relayMode int) (int, error) {
//	switch relayMode {
//	case constant.RelayModeChatCompletions:
//		return CountTokenMessages(textRequest.Messages, textRequest.Model)
//	case constant.RelayModeCompletions:
//		return CountTokenInput(textRequest.Prompt, textRequest.Model), nil
//	case constant.RelayModeModerations:
//		return CountTokenInput(textRequest.Input, textRequest.Model), nil
//	}
//	return 0, errors.New("unknown relay mode")
//}

func ResponseText2Usage(c *gin.Context, responseText string, modeName string, promptTokens int) *dto.Usage {
	common.SetContextKey(c, constant.ContextKeyLocalCountTokens, true)
	usage := &dto.Usage{}
	usage.PromptTokens = promptTokens
	usage.CompletionTokens = EstimateTokenByModel(modeName, responseText)
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return usage
}

func ValidUsage(usage *dto.Usage) bool {
	return usage != nil && (usage.PromptTokens != 0 || usage.CompletionTokens != 0)
}

// DegradeZeroCompletionUsage 在请求成功但上游返回 0 输出 token 时降级补全输出侧喵。
// 触发场景：上游流式只报 prompt 未报 completion（或 completion=0 而推理 token 单独上报），
// 导致输出 token 显示与计费为 0。降级优先级：
//  1. 上游已上报的推理 token（reasoning_tokens）视为真实输出量直接并入喵；
//  2. 否则用响应文本（content + 思考 content，由调用方传入）按模型估算补全喵。
//
// 喵~防御：usage 为 nil、输出已非零或两者素材都拿不到时不改动，绝不把非零值拉低喵。
func DegradeZeroCompletionUsage(usage *dto.Usage, modelName, responseText string) {
	// 喵~防御：空 usage 或输出已非零时无需降级，直接返回喵。
	if usage == nil || usage.CompletionTokens > 0 {
		return
	}
	// 优先使用上游真实上报的推理 token，避免用估算替代权威计数喵。
	if usage.CompletionTokenDetails.ReasoningTokens > 0 {
		usage.CompletionTokens = usage.CompletionTokenDetails.ReasoningTokens
	} else {
		// 无推理 token 时用响应文本估算输出量；文本为空时估算为 0，保持原值不越权喵。
		estimatedCompletion := EstimateTokenByModel(modelName, responseText)
		if estimatedCompletion > 0 {
			usage.CompletionTokens = estimatedCompletion
		}
	}
	// 补全后刷新总量，保证计费与日志的 total 口径一致喵。
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
}
