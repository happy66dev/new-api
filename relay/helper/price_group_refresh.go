package helper

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// RefreshPriceDataForSelectedGroup 在最终分组确定或变化后，按新分组重新解析定价并就地更新 PriceData 喵。
//
// 为什么必须有这一步喵：
//
//	auto 分组重试会让同一个请求先后落到不同分组，而分组定制定价允许同一个模型 id
//	在不同分组下用不同的计费方式和价格。结算阶段（calculateTextQuotaSummary）只读
//	relayInfo.PriceData 这份快照，所以分组一变就必须把快照里的价格口径换成新分组的，
//	否则就会拿 A 组的价格去结算实际由 B 组上游完成的请求喵。
//
// 边界与约定喵：
//   - 只刷新「价格口径」，不动已经预扣的额度。预扣与实际消耗的差额由 SettleBilling
//     统一补扣或退还；阶梯计费的预扣补足仍由 PrepareTieredBillingForSelectedGroup 负责喵。
//   - 不改 FreeModel。它的语义是「请求开始时是否跳过了预扣费」，属于历史事实，不该被后续分组改写喵。
//   - 返回 error 只在新分组的阶梯表达式缺失或跑不通时发生，调用方应当中断这次尝试喵。
func RefreshPriceDataForSelectedGroup(c *gin.Context, info *relaycommon.RelayInfo) error {
	// 喵~防御：缺少上下文或 relayInfo 时什么都不做，避免空指针打断重试链喵。
	if c == nil || info == nil {
		return nil
	}
	groupRatioInfo := HandleGroupRatio(c, info)
	info.PriceData.GroupRatioInfo = groupRatioInfo

	// 新分组走阶梯表达式：刷新表达式与计费快照，价格细节全部由表达式自己描述喵。
	if billing_setting.GetBillingModeForGroup(info.UsingGroup, info.OriginModelName) == billing_setting.BillingModeTieredExpr {
		return refreshTieredPricingForGroup(c, info, groupRatioInfo)
	}

	// 新分组不走表达式：若上一个分组是表达式计费，必须丢掉快照，
	// 否则结算会继续按旧分组的表达式算钱喵。
	info.TieredBillingSnapshot = nil

	pricing := ratio_setting.ResolveModelPricing(info.UsingGroup, info.OriginModelName)
	applyResolvedPricingToPriceData(&info.PriceData, pricing)
	logger.LogDebug(c, "price_data refreshed for group %s: %s", info.UsingGroup, info.PriceData.ToSetting())
	return nil
}

// applyResolvedPricingToPriceData 把定价快照里的价格口径写回 PriceData 喵。
// 只覆盖价格与倍率字段，预扣额度、OtherRatios、FreeModel 等请求级状态一律保留喵。
func applyResolvedPricingToPriceData(priceData *hosttypes.PriceData, pricing ratio_setting.ResolvedModelPricing) {
	priceData.UsePrice = pricing.UsePrice
	priceData.ModelPrice = pricing.ModelPrice
	priceData.ModelRatio = pricing.ModelRatio
	priceData.CompletionRatio = pricing.CompletionRatio
	priceData.CacheRatio = pricing.CacheRatioValue()
	priceData.CacheCreationRatio = pricing.CreateCacheRatioValue()
	priceData.CacheCreation5mRatio = priceData.CacheCreationRatio
	// 1h 缓存写入价固定是 5min 的固定倍数，与首次定价口径保持一致喵。
	priceData.CacheCreation1hRatio = priceData.CacheCreationRatio * claudeCacheCreation1hMultiplier
	priceData.ImageRatio = pricing.ImageRatioValue()
	priceData.AudioRatio = pricing.AudioRatioValue()
	priceData.AudioCompletionRatio = pricing.AudioCompletionRatioValue()
	priceData.PricingGroupOverride = pricing.OverrideGroup
}

// refreshTieredPricingForGroup 按新分组重新解析阶梯计费表达式并刷新计费快照喵。
//
// 三种情况喵：
//  1. 表达式与快照里的完全一致：什么都不用做，分组倍率相关字段留给
//     PrepareTieredBillingForSelectedGroup 按新倍率补足预扣即可；
//  2. 快照是任务型用量计费（usage facts）：保持原样不动，任务的提交与结算必须用同一批
//     usage 事实，且任务链路不走这个重试路径，这里只做防御性跳过；
//  3. 其余情况（表达式变了，或上一个分组本来是倍率计费）：用冻结的请求内容与 token 估算
//     重跑一次新表达式，整份重建快照喵。
func refreshTieredPricingForGroup(c *gin.Context, info *relaycommon.RelayInfo, groupRatioInfo hosttypes.GroupRatioInfo) error {
	exprStr, ok := billing_setting.GetBillingExprForGroup(info.UsingGroup, info.OriginModelName)
	// 喵~防御：分组声明了阶梯计费却没有可用表达式时直接报错，绝不静默按 0 元放过喵。
	if !ok {
		return fmt.Errorf("model %s is configured as tiered_expr in group %s but has no billing expression",
			info.OriginModelName, info.UsingGroup)
	}
	snapshot := info.TieredBillingSnapshot
	if snapshot != nil {
		// 情况 1：表达式没变，快照不需要重建喵。
		if snapshot.ExprString == exprStr {
			return nil
		}
		// 情况 2：任务型用量计费不参与这里的重建喵。
		if snapshot.TaskUsageBilling {
			return nil
		}
	}

	// token 估算优先沿用快照里冻结的值，保证同一请求跨分组重试时估算口径稳定喵。
	promptTokens := info.GetEstimatePromptTokens()
	completionTokens := defaultTieredPreConsumeMaxTokens
	if snapshot != nil {
		if snapshot.EstimatedPromptTokens > 0 {
			promptTokens = snapshot.EstimatedPromptTokens
		}
		if snapshot.EstimatedCompletionTokens > 0 {
			completionTokens = snapshot.EstimatedCompletionTokens
		}
	}

	// 请求内容（header / body）在首次定价时就冻结了，这里必须复用同一份，
	// 否则 param() / header() 这类请求相关规则可能在重试时算出不同结果喵。
	requestInput := billingexpr.RequestInput{}
	if info.BillingRequestInput != nil {
		requestInput = *info.BillingRequestInput
	} else {
		resolvedInput, err := ResolveIncomingBillingExprRequestInput(c, info)
		if err != nil {
			return err
		}
		requestInput = resolvedInput
		info.BillingRequestInput = &requestInput
	}

	rawCost, trace, err := billingexpr.RunExprWithRequest(exprStr, billingexpr.TokenParams{
		P:   float64(promptTokens),
		C:   float64(completionTokens),
		Len: float64(promptTokens),
	}, requestInput)
	if err != nil {
		return fmt.Errorf("model %s tiered expr run failed in group %s: %w", info.OriginModelName, info.UsingGroup, err)
	}

	// 表达式系数是 $/1M token，换算成额度的口径与首次定价完全一致喵。
	quotaBeforeGroup := rawCost / 1_000_000 * common.QuotaPerUnit
	estimatedAfterGroup, err := billingexpr.QuotaRoundStrict(quotaBeforeGroup * groupRatioInfo.GroupRatio)
	if err != nil {
		return err
	}

	info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{
		BillingMode:               billing_setting.BillingModeTieredExpr,
		ModelName:                 info.OriginModelName,
		ExprString:                exprStr,
		ExprHash:                  billingexpr.ExprHashString(exprStr),
		GroupRatio:                groupRatioInfo.GroupRatio,
		EstimatedPromptTokens:     promptTokens,
		EstimatedCompletionTokens: completionTokens,
		EstimatedQuotaBeforeGroup: quotaBeforeGroup,
		EstimatedQuotaAfterGroup:  estimatedAfterGroup,
		EstimatedTier:             trace.MatchedTier,
		QuotaPerUnit:              common.QuotaPerUnit,
		ExprVersion:               billingexpr.ExprVersion(exprStr),
	}

	// 阶梯计费的价格完全由表达式描述，倍率字段要清零，
	// 免得上一个分组的倍率残留进消费日志把管理员绕晕喵。
	info.PriceData.UsePrice = false
	info.PriceData.ModelPrice = 0
	info.PriceData.ModelRatio = 0
	info.PriceData.CompletionRatio = 0
	info.PriceData.CacheRatio = 0
	info.PriceData.CacheCreationRatio = 0
	info.PriceData.CacheCreation5mRatio = 0
	info.PriceData.CacheCreation1hRatio = 0
	info.PriceData.ImageRatio = 0
	info.PriceData.AudioRatio = 0
	info.PriceData.AudioCompletionRatio = 0
	info.PriceData.PricingGroupOverride = ""
	// 表达式来自分组定制时记下来源分组，供日志审计定价来源喵。
	if _, fromGroup := billing_setting.GetGroupBillingExpr(info.UsingGroup, info.OriginModelName); fromGroup {
		info.PriceData.PricingGroupOverride = info.UsingGroup
	}
	logger.LogDebug(c, "tiered snapshot rebuilt for group %s: model=%s tier=%s quotaBeforeGroup=%.2f",
		info.UsingGroup, info.OriginModelName, trace.MatchedTier, quotaBeforeGroup)
	return nil
}
