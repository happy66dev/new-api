package service

import (
	"strings"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

const (
	usageBillingPathLocal              = "local"
	usageBillingPathUpstream           = "upstream"
	usageBillingPathOpenAI             = "billing-usage-openai"
	usageBillingPathOpenAIEstimated    = "billing-usage-openai-estimated"
	usageBillingPathAnthropic          = "billing-usage-anthropic"
	usageBillingPathAnthropicEstimated = "billing-usage-anthropic-estimated"
	usageBillingPathGemini             = "billing-usage-gemini"
	usageBillingPathGeminiEstimated    = "billing-usage-gemini-estimated"
)

func effectiveBillingUsage(usage *dto.Usage) *dto.Usage {
	if billingUsage, ok := usageFromBillingUsage(usage); ok {
		return billingUsage
	}
	return usage
}

func usageBillingPathForLog(isLocalCountTokens bool, usage *dto.Usage) string {
	effectiveUsage, ok := usageFromBillingUsage(usage)
	if !ok {
		if isLocalCountTokens {
			return usageBillingPathLocal
		}
		return usageBillingPathUpstream
	}

	switch effectiveUsage.UsageSemantic {
	case dto.BillingUsageSemanticOpenAI:
		if usage.BillingUsage.Estimated {
			return usageBillingPathOpenAIEstimated
		}
		return usageBillingPathOpenAI
	case dto.BillingUsageSemanticAnthropic:
		if usage.BillingUsage.Estimated {
			return usageBillingPathAnthropicEstimated
		}
		return usageBillingPathAnthropic
	case dto.BillingUsageSemanticGemini:
		if usage.BillingUsage.Estimated {
			return usageBillingPathGeminiEstimated
		}
		return usageBillingPathGemini
	}

	return usageBillingPathUpstream
}

func appendUsageBillingPathForLog(other *model.LogOther, isLocalCountTokens bool, usage *dto.Usage) {
	if other == nil {
		return
	}
	other.SetAdmin("usage_billing_path", usageBillingPathForLog(isLocalCountTokens, usage))
}

func usageFromBillingUsage(usage *dto.Usage) (*dto.Usage, bool) {
	if usage == nil || usage.BillingUsage == nil {
		return nil, false
	}
	return usage.BillingUsage.CanonicalUsage()
}

// CanonicalizeUpstreamUsageBySemantic 把自定义上游/透传解析出的扁平 usage 规范成与
// new-api 原生计费/日志完全同源的口径喵。
// semantic 取值与 dto.BillingUsageSemanticAnthropic / ...OpenAI 一致，通常按请求协议判定：
// /v1/messages 走 anthropic（input_tokens 不含缓存读取，缓存读取/写入需单独拆分），其余走 openai 喵。
// 返回的 usage 带完整规范字段（PromptTokens/CompletionTokens/InputTokens/各缓存分类/TotalTokens），
// 并保留调用方已有的估算（Estimated）标记喵。
func CanonicalizeUpstreamUsageBySemantic(usage *dto.Usage, semantic string) *dto.Usage {
	// 喵~防御：空 usage 直接返回，避免空指针喵。
	if usage == nil {
		return nil
	}
	estimated := usage.BillingUsage != nil && usage.BillingUsage.Estimated
	var canonical *dto.Usage
	if strings.EqualFold(semantic, dto.BillingUsageSemanticAnthropic) {
		// anthropic 语义：用扁平字段重建 ClaudeUsage，再走 relaykit 的统一规范化（上游 CanonicalUsage）喵。
		if billingUsage := dto.NewClaudeMessagesBillingUsage(claudeUsageFromFlatUsage(usage)); billingUsage != nil {
			canonical, _ = billingUsage.CanonicalUsage()
		}
	}
	if canonical == nil {
		// 无对应语义 token（或未命中 anthropic 分支）时回退 OpenAI 数值规范，保证任何 usage 都有确定口径喵。
		if billingUsage := dto.NewOpenAIChatBillingUsage(usage); billingUsage != nil {
			canonical, _ = billingUsage.CanonicalUsage()
		}
	}
	if canonical == nil {
		// 喵~防御：usage 全零无法构造 BillingUsage 时原样返回，避免静默丢数据喵。
		if estimated {
			markUsageEstimated(usage)
		}
		return usage
	}
	if estimated {
		markUsageEstimated(canonical)
	}
	return canonical
}

// claudeUsageFromFlatUsage 从透传解析出的扁平 usage 重建 anthropic 的 ClaudeUsage 素材喵。
// input/output 取原始值（anthropic 的 input_tokens 本身不含缓存读取），缓存读取/写入从规范分类字段回填喵。
func claudeUsageFromFlatUsage(usage *dto.Usage) *dto.ClaudeUsage {
	// 喵~防御：空 usage 返回空 ClaudeUsage，由上层判定无 token 喵。
	if usage == nil {
		return &dto.ClaudeUsage{}
	}
	// 缓存读取：优先规范 cached_tokens，其次 prompt_cache_hit_tokens 与 cache_read_input_tokens 兼容拼写喵。
	cacheReadTokens := usage.PromptTokensDetails.CachedTokens
	if cacheReadTokens == 0 {
		cacheReadTokens = usage.PromptCacheHitTokens
	}
	if cacheReadTokens == 0 {
		cacheReadTokens = usage.PromptTokensDetails.CacheReadInputTokens
	}
	// 缓存写入：优先规范 cached_creation_tokens，其次 cache_creation_input_tokens 兼容拼写喵。
	cacheCreationTokens := usage.PromptTokensDetails.CachedCreationTokens
	if cacheCreationTokens == 0 {
		cacheCreationTokens = usage.PromptTokensDetails.CacheCreationInputTokens
	}
	return &dto.ClaudeUsage{
		InputTokens:              flatInputTokens(usage),
		OutputTokens:             flatOutputTokens(usage),
		CacheReadInputTokens:     cacheReadTokens,
		CacheCreationInputTokens: cacheCreationTokens,
		ClaudeCacheCreation5mTokens: usage.ClaudeCacheCreation5mTokens,
		ClaudeCacheCreation1hTokens: usage.ClaudeCacheCreation1hTokens,
	}
}

// flatInputTokens 取扁平 usage 的原始输入 token：优先 input_tokens，其次 prompt_tokens 喵。
func flatInputTokens(usage *dto.Usage) int {
	// 喵~防御：空 usage 按零处理喵。
	if usage == nil {
		return 0
	}
	if usage.InputTokens > 0 {
		return usage.InputTokens
	}
	return usage.PromptTokens
}

// flatOutputTokens 取扁平 usage 的原始输出 token：优先 output_tokens，其次 completion_tokens 喵。
func flatOutputTokens(usage *dto.Usage) int {
	// 喵~防御：空 usage 按零处理喵。
	if usage == nil {
		return 0
	}
	if usage.OutputTokens > 0 {
		return usage.OutputTokens
	}
	return usage.CompletionTokens
}

// markUsageEstimated 给规范后的 usage 打上估算来源标记，与原生日志的「?」展示口径一致喵。
func markUsageEstimated(usage *dto.Usage) {
	// 喵~防御：空 usage 直接返回喵。
	if usage == nil {
		return
	}
	if usage.BillingUsage == nil {
		usage.BillingUsage = &dto.BillingUsage{}
	}
	usage.BillingUsage.Estimated = true
}
