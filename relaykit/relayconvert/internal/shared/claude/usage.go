package claude

import "github.com/QuantumNous/new-api/relaykit/dto"

func UsageFromOpenAI(usage *dto.Usage) *dto.ClaudeUsage {
	if usage == nil {
		return nil
	}
	// An existing sidecar snapshots the original provider usage; carry it
	// across this bridge unchanged regardless of its dialect. Only synthesize
	// an OpenAI snapshot when no sidecar exists yet.
	existingBillingUsage := dto.CloneBillingUsage(usage.BillingUsage)
	if existingBillingUsage != nil && existingBillingUsage.ClaudeUsage != nil &&
		(existingBillingUsage.Source == dto.BillingUsageSourceClaudeMessages || existingBillingUsage.Semantic == dto.BillingUsageSemanticAnthropic) {
		result := existingBillingUsage.ClaudeUsage
		result.BillingUsage = dto.CloneBillingUsage(usage.BillingUsage)
		return result
	}
	billingUsage := existingBillingUsage
	if billingUsage == nil {
		billingUsage = dto.NewOpenAIChatBillingUsage(usage)
	}
	cacheCreation5m, cacheCreation1h := NormalizeCacheCreationSplit(
		usage.PromptTokensDetails.CachedCreationTokens,
		usage.ClaudeCacheCreation5mTokens,
		usage.ClaudeCacheCreation1hTokens,
	)
	cacheCreationTokens := usage.PromptTokensDetails.CacheCreationTokensTotal()
	// 缓存命中同样要兼容非标准拼写：cached_tokens 与中转站风格 cache_read_input_tokens 等价，取合计喵。
	cachedTokens := usage.PromptTokensDetails.CachedTokensTotal()
	inputTokens := usage.PromptTokens
	if usage.UsageSemantic != dto.BillingUsageSemanticAnthropic {
		// OpenAI-style prompt/input totals include cache reads and writes, while
		// Claude reports both separately from input_tokens.
		inputTokens = usage.PromptTokens - cachedTokens - cacheCreationTokens
		if inputTokens < 0 {
			inputTokens = 0
		}
	}
	result := &dto.ClaudeUsage{
		InputTokens:              inputTokens,
		OutputTokens:             usage.CompletionTokens,
		CacheCreationInputTokens: cacheCreationTokens,
		CacheReadInputTokens:     cachedTokens,
		BillingUsage:             billingUsage,
	}
	if cacheCreation5m > 0 || cacheCreation1h > 0 {
		result.CacheCreation = &dto.ClaudeCacheCreationUsage{
			Ephemeral5mInputTokens: cacheCreation5m,
			Ephemeral1hInputTokens: cacheCreation1h,
		}
	}
	return result
}
