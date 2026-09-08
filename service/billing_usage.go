package service

import (
	"strings"

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

func appendUsageBillingPathForLog(other map[string]interface{}, isLocalCountTokens bool, usage *dto.Usage) {
	if other == nil {
		return
	}
	adminInfo, ok := other["admin_info"].(map[string]interface{})
	if !ok || adminInfo == nil {
		adminInfo = make(map[string]interface{})
		other["admin_info"] = adminInfo
	}
	adminInfo["usage_billing_path"] = usageBillingPathForLog(isLocalCountTokens, usage)
}

func usageFromBillingUsage(usage *dto.Usage) (*dto.Usage, bool) {
	if usage == nil || usage.BillingUsage == nil {
		return nil, false
	}
	billingUsage := usage.BillingUsage
	source := strings.TrimSpace(billingUsage.Source)
	semantic := strings.TrimSpace(billingUsage.Semantic)

	if billingUsage.OpenAIUsage != nil &&
		(strings.EqualFold(source, dto.BillingUsageSourceOAIChat) ||
			strings.EqualFold(source, dto.BillingUsageSourceOAIResponses) ||
			strings.EqualFold(semantic, dto.BillingUsageSemanticOpenAI)) {
		return usageFromOpenAIBillingUsage(billingUsage), true
	}

	if billingUsage.ClaudeUsage != nil &&
		(strings.EqualFold(source, dto.BillingUsageSourceClaudeMessages) ||
			strings.EqualFold(semantic, dto.BillingUsageSemanticAnthropic)) {
		return usageFromClaudeBillingUsage(billingUsage), true
	}

	if billingUsage.GeminiUsageMetadata != nil &&
		(strings.EqualFold(source, dto.BillingUsageSourceGeminiChat) ||
			strings.EqualFold(semantic, dto.BillingUsageSemanticGemini)) {
		return usageFromGeminiBillingUsage(billingUsage), true
	}

	return nil, false
}

func usageFromOpenAIBillingUsage(billingUsage *dto.BillingUsage) *dto.Usage {
	usage := *billingUsage.OpenAIUsage
	if usage.PromptTokens == 0 && usage.InputTokens > 0 {
		usage.PromptTokens = usage.InputTokens
	}
	if usage.CompletionTokens == 0 && usage.OutputTokens > 0 {
		usage.CompletionTokens = usage.OutputTokens
	}
	if usage.InputTokens == 0 && usage.PromptTokens > 0 {
		usage.InputTokens = usage.PromptTokens
	}
	if usage.OutputTokens == 0 && usage.CompletionTokens > 0 {
		usage.OutputTokens = usage.CompletionTokens
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	if inputDetails := usage.InputTokensDetails; inputDetails != nil {
		if usage.PromptTokensDetails.CachedTokens == 0 && inputDetails.CachedTokens > 0 {
			usage.PromptTokensDetails.CachedTokens = inputDetails.CachedTokens
		}
		if usage.PromptTokensDetails.CachedCreationTokens == 0 && inputDetails.CachedCreationTokens > 0 {
			usage.PromptTokensDetails.CachedCreationTokens = inputDetails.CachedCreationTokens
		}
		if usage.PromptTokensDetails.CacheWriteTokens == 0 && inputDetails.CacheWriteTokens > 0 {
			usage.PromptTokensDetails.CacheWriteTokens = inputDetails.CacheWriteTokens
		}
		// 中转站风格的缓存写入/命中与视频 token 一并从 input_tokens_details 并入喵。
		if usage.PromptTokensDetails.CacheCreationInputTokens == 0 && inputDetails.CacheCreationInputTokens > 0 {
			usage.PromptTokensDetails.CacheCreationInputTokens = inputDetails.CacheCreationInputTokens
		}
		if usage.PromptTokensDetails.CacheReadInputTokens == 0 && inputDetails.CacheReadInputTokens > 0 {
			usage.PromptTokensDetails.CacheReadInputTokens = inputDetails.CacheReadInputTokens
		}
		if usage.PromptTokensDetails.VideoTokens == 0 && inputDetails.VideoTokens > 0 {
			usage.PromptTokensDetails.VideoTokens = inputDetails.VideoTokens
		}
		if usage.PromptTokensDetails.TextTokens == 0 && inputDetails.TextTokens > 0 {
			usage.PromptTokensDetails.TextTokens = inputDetails.TextTokens
		}
		if usage.PromptTokensDetails.ImageTokens == 0 && inputDetails.ImageTokens > 0 {
			usage.PromptTokensDetails.ImageTokens = inputDetails.ImageTokens
		}
		if usage.PromptTokensDetails.AudioTokens == 0 && inputDetails.AudioTokens > 0 {
			usage.PromptTokensDetails.AudioTokens = inputDetails.AudioTokens
		}
	}
	if usage.PromptTokensDetails.CachedTokens == 0 && usage.PromptCacheHitTokens > 0 {
		usage.PromptTokensDetails.CachedTokens = usage.PromptCacheHitTokens
	}
	usage.UsageSemantic = dto.BillingUsageSemanticOpenAI
	usage.UsageSource = billingUsage.Source
	usage.BillingUsage = dto.CloneBillingUsage(billingUsage)
	return &usage
}

func usageFromClaudeBillingUsage(billingUsage *dto.BillingUsage) *dto.Usage {
	claudeUsage := billingUsage.ClaudeUsage
	cacheCreation5m := claudeUsage.GetCacheCreation5mTokens()
	if cacheCreation5m == 0 {
		cacheCreation5m = claudeUsage.ClaudeCacheCreation5mTokens
	}
	cacheCreation1h := claudeUsage.GetCacheCreation1hTokens()
	if cacheCreation1h == 0 {
		cacheCreation1h = claudeUsage.ClaudeCacheCreation1hTokens
	}

	usage := &dto.Usage{
		PromptTokens:                claudeUsage.InputTokens,
		CompletionTokens:            claudeUsage.OutputTokens,
		TotalTokens:                 claudeUsage.InputTokens + claudeUsage.OutputTokens,
		InputTokens:                 claudeUsage.InputTokens + claudeUsage.CacheReadInputTokens + claudeUsage.CacheCreationInputTokens,
		OutputTokens:                claudeUsage.OutputTokens,
		UsageSemantic:               dto.BillingUsageSemanticAnthropic,
		UsageSource:                 dto.BillingUsageSourceClaudeMessages,
		BillingUsage:                dto.CloneBillingUsage(billingUsage),
		ClaudeCacheCreation5mTokens: cacheCreation5m,
		ClaudeCacheCreation1hTokens: cacheCreation1h,
	}
	usage.PromptTokensDetails.CachedTokens = claudeUsage.CacheReadInputTokens
	usage.PromptTokensDetails.CachedCreationTokens = claudeUsage.CacheCreationInputTokens
	return usage
}

func usageFromGeminiBillingUsage(billingUsage *dto.BillingUsage) *dto.Usage {
	metadata := *billingUsage.GeminiUsageMetadata
	promptTokens := metadata.PromptTokenCount + metadata.ToolUsePromptTokenCount
	usage := &dto.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: metadata.CandidatesTokenCount + metadata.ThoughtsTokenCount,
		TotalTokens:      metadata.TotalTokenCount,
		UsageSemantic:    dto.BillingUsageSemanticGemini,
		UsageSource:      dto.BillingUsageSourceGeminiChat,
		BillingUsage:     dto.CloneBillingUsage(billingUsage),
	}
	usage.CompletionTokenDetails.ReasoningTokens = metadata.ThoughtsTokenCount
	usage.PromptTokensDetails.CachedTokens = metadata.CachedContentTokenCount

	for _, detail := range metadata.PromptTokensDetails {
		addGeminiInputTokenDetail(&usage.PromptTokensDetails, detail)
	}
	for _, detail := range metadata.ToolUsePromptTokensDetails {
		addGeminiInputTokenDetail(&usage.PromptTokensDetails, detail)
	}
	for _, detail := range metadata.CandidatesTokensDetails {
		switch detail.Modality {
		case "IMAGE":
			usage.CompletionTokenDetails.ImageTokens += detail.TokenCount
		case "AUDIO":
			usage.CompletionTokenDetails.AudioTokens += detail.TokenCount
		case "TEXT":
			usage.CompletionTokenDetails.TextTokens += detail.TokenCount
		}
	}

	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	} else if usage.CompletionTokens <= 0 {
		usage.CompletionTokens = usage.TotalTokens - usage.PromptTokens
	}
	if usage.PromptTokens > 0 && usage.PromptTokensDetails.TextTokens == 0 && usage.PromptTokensDetails.AudioTokens == 0 {
		usage.PromptTokensDetails.TextTokens = usage.PromptTokens
	}
	return usage
}

func addGeminiInputTokenDetail(details *dto.InputTokenDetails, detail dto.GeminiPromptTokensDetails) {
	switch detail.Modality {
	case "AUDIO":
		details.AudioTokens += detail.TokenCount
	case "IMAGE":
		details.ImageTokens += detail.TokenCount
	case "TEXT":
		details.TextTokens += detail.TokenCount
	}
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
		// anthropic 语义：用扁平字段重建 ClaudeUsage，再走原生 usageFromClaudeBillingUsage 转换喵。
		if billingUsage := dto.NewClaudeMessagesBillingUsage(claudeUsageFromFlatUsage(usage)); billingUsage != nil {
			canonical = usageFromClaudeBillingUsage(billingUsage)
		}
	}
	if canonical == nil {
		// 无对应语义 token（或未命中 anthropic 分支）时回退 OpenAI 数值规范，保证任何 usage 都有确定口径喵。
		if billingUsage := dto.NewOpenAIChatBillingUsage(usage); billingUsage != nil {
			canonical = usageFromOpenAIBillingUsage(billingUsage)
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
