package upstreammodel

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/shopspring/decimal"
)

// CalculateUpstreamModelCostCents 按每类型直接价格计算一次请求的 RMB 费用（分）喵。
// 每个价格字段代表"每百万该类型 token 的 RMB 元"，各分类 token 数乘各自价格后求和喵。
func CalculateUpstreamModelCostCents(upstreamModel *model.UserUpstreamModel, usage *dto.Usage) (int64, error) {
	// 喵~防御：空对象或空 usage 直接返回零费用，不产生计费喵。
	if upstreamModel == nil || usage == nil {
		return 0, nil
	}
	modelRatio := parseModelRatio(upstreamModel.ModelRatio)
	// 输入价格为零表示未定价，费用为 0 喵。
	if modelRatio.IsZero() {
		return 0, nil
	}
	// 其余分类价格解析失败时回退为 1 元，避免单个坏配置导致整次计费失败喵。
	completionRatio := parseRatio(upstreamModel.CompletionRatio)
	cacheRatio := parseRatio(upstreamModel.CacheRatio)
	cacheCreationRatio := parseRatio(upstreamModel.CacheCreationRatio)
	cacheCreation5mRatio := parseRatio(upstreamModel.CacheCreation5mRatio)
	cacheCreation1hRatio := parseRatio(upstreamModel.CacheCreation1hRatio)
	imageRatio := parseRatio(upstreamModel.ImageRatio)
	audioRatio := parseRatio(upstreamModel.AudioRatio)
	audioCompletionRatio := parseRatio(upstreamModel.AudioCompletionRatio)

	// 提取各分类 token 数量，缓存命中与缓存写入从基础输入中扣除喵。
	promptTokens := int64(usage.PromptTokens)
	cachedTokens := int64(usage.PromptTokensDetails.CachedTokens)
	cacheCreationTokens := int64(usage.PromptTokensDetails.CacheCreationTokensTotal())
	imageTokens := int64(usage.PromptTokensDetails.ImageTokens)
	audioTokens := int64(usage.PromptTokensDetails.AudioTokens)
	completionTokens := int64(usage.CompletionTokens)
	// 音频输出 token 从输出明细中提取，从普通输出扣除后按音频输出价计费喵。
	audioCompletionTokens := int64(usage.CompletionTokenDetails.AudioTokens)
	// Claude 语义下缓存写入按 5m/1h 拆分计费喵。
	cacheCreation5mTokens := int64(usage.ClaudeCacheCreation5mTokens)
	cacheCreation1hTokens := int64(usage.ClaudeCacheCreation1hTokens)

	// 基础输入扣掉缓存/图片/音频后钳制非负，避免分类重叠导致负基础费用喵。
	promptBaseTokens := promptTokens - cachedTokens - cacheCreationTokens - imageTokens - audioTokens
	if promptBaseTokens < 0 {
		promptBaseTokens = 0
	}

	// 普通输出扣掉音频输出后钳制非负，防止音频输出被重复计入喵。
	textCompletionTokens := completionTokens - audioCompletionTokens
	if textCompletionTokens < 0 {
		textCompletionTokens = 0
	}

	// 缓存写入剩余量（总写入扣掉 5m/1h 拆分部分）钳制非负喵。
	remainingCacheCreationTokens := cacheCreationTokens - cacheCreation5mTokens - cacheCreation1hTokens
	if remainingCacheCreationTokens < 0 {
		remainingCacheCreationTokens = 0
	}

	// 各分类 token 数乘各自价格（每百万 token RMB 元）后求和，再 /1e6 转元、×100 转分喵。
	costDecimal := decimal.NewFromInt(promptBaseTokens).Mul(modelRatio).
		Add(decimal.NewFromInt(cachedTokens).Mul(cacheRatio)).
		Add(decimal.NewFromInt(remainingCacheCreationTokens).Mul(cacheCreationRatio)).
		Add(decimal.NewFromInt(cacheCreation5mTokens).Mul(cacheCreation5mRatio)).
		Add(decimal.NewFromInt(cacheCreation1hTokens).Mul(cacheCreation1hRatio)).
		Add(decimal.NewFromInt(imageTokens).Mul(imageRatio)).
		Add(decimal.NewFromInt(audioTokens).Mul(audioRatio)).
		Add(decimal.NewFromInt(textCompletionTokens).Mul(completionRatio)).
		Add(decimal.NewFromInt(audioCompletionTokens).Mul(audioCompletionRatio)).
		Div(decimal.NewFromInt(1_000_000)).Mul(decimal.NewFromInt(100))
	costCents, clamp := common.QuotaFromDecimalChecked(costDecimal)
	// 主人注意：费用转分可能因异常上游 token 数触发饱和钳制，超大的 usage 会被钳制到安全上限喵。
	if clamp != nil {
		common.SysError("user upstream model charge clamped: " + clamp.Error())
	}
	return int64(costCents), nil
}

// parseModelRatio 解析输入价格（每百万 token RMB 元）；空值回退为一元，非法值回退为零（不计费）喵。
func parseModelRatio(value string) decimal.Decimal {
	// 喵~防御：空白输入按默认 1 元处理喵。
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return decimal.NewFromInt(1)
	}
	parsed, parseError := decimal.NewFromString(trimmed)
	// 喵~防御：解析失败或负数一律回退为零；shopspring 十进制只解析有限小数，无需再判 NaN/Inf 喵。
	if parseError != nil || parsed.IsNegative() {
		return decimal.Zero
	}
	return parsed
}

// parseRatio 解析分类价格（每百万 token RMB 元）；空值或非法值统一回退为一元喵。
func parseRatio(value string) decimal.Decimal {
	// 喵~防御：空白输入按 1 元处理喵。
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return decimal.NewFromInt(1)
	}
	parsed, parseError := decimal.NewFromString(trimmed)
	// 喵~防御：解析失败、非正或零一律回退为一元，避免坏配置把费用算成负数；无需再判 NaN/Inf 喵。
	if parseError != nil || parsed.IsNegative() || parsed.IsZero() {
		return decimal.NewFromInt(1)
	}
	return parsed
}
