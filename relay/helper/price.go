package helper

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func modelPriceNotConfiguredError(modelName string, userId int) error {
	if model.IsAdmin(userId) {
		return fmt.Errorf(
			"模型 %s 的价格未配置。请前往「系统设置 → 运营设置」开启自用模式，或在「系统设置 → 分组与模型定价设置」中为该模型配置价格；"+
				"Model %s price not configured. Go to System Settings → Operation Settings to enable self-use mode, or configure the model price in System Settings → Group & Model Pricing.",
			modelName, modelName,
		)
	}
	return fmt.Errorf(
		"模型 %s 的价格尚未由管理员配置，暂时无法使用，请联系站点管理员开启该模型；"+
			"Model %s has not been priced by the administrator yet. Please contact the site administrator to enable this model.",
		modelName, modelName,
	)
}

// https://docs.claude.com/en/docs/build-with-claude/prompt-caching#1-hour-cache-duration
const claudeCacheCreation1hMultiplier = 6 / 3.75

// defaultTieredPreConsumeMaxTokens is the fallback completion-token estimate
// used for tiered expression pre-consume when the client omits max_tokens, so
// the pre-consumed quota still reflects a plausible output cost in paid groups.
const defaultTieredPreConsumeMaxTokens = 8192

// HandleGroupRatio checks for "auto_group" in the context and updates the group ratio and relayInfo.UsingGroup if present
func HandleGroupRatio(ctx *gin.Context, relayInfo *relaycommon.RelayInfo) hosttypes.GroupRatioInfo {
	groupRatioInfo := hosttypes.GroupRatioInfo{
		GroupRatio:        1.0, // default ratio
		GroupSpecialRatio: -1,
	}

	// check auto group
	autoGroup, exists := ctx.Get("auto_group")
	if exists {
		logger.LogDebug(ctx, "final group: %s", autoGroup)
		relayInfo.UsingGroup = autoGroup.(string)
	}

	// check user group special ratio
	userGroupRatio, ok := ratio_setting.GetGroupGroupRatio(relayInfo.UserGroup, relayInfo.UsingGroup)
	if ok {
		// user group special ratio
		groupRatioInfo.GroupSpecialRatio = userGroupRatio
		groupRatioInfo.GroupRatio = userGroupRatio
		groupRatioInfo.HasSpecialRatio = true
	} else {
		// normal group ratio
		groupRatioInfo.GroupRatio = ratio_setting.GetGroupRatio(relayInfo.UsingGroup)
	}

	return groupRatioInfo
}

func ModelPriceHelper(c *gin.Context, info *relaycommon.RelayInfo, promptTokens int, meta *types.TokenCountMeta) (hosttypes.PriceData, error) {
	// 上游引入「计费模型名」解析：带 @修饰符 的请求（如 model@thinking:on）回落到有价的基础模型，
	// 命中思考后缀豁免名单（黑名单里的 re: 规则）时保持原名，按字面价计费喵。
	if matched := resolveBillingModelName(info.GetOriginModelName()); matched != "" && matched != info.OriginModelName {
		info.BillingModelName = matched
	}
	// 必须先确定最终分组再读价格：分组定制定价允许同一个模型 id 在不同分组下用不同计费方式
	// （比如 A 组按次、B 组按量），所以 UsingGroup 一定要在取任何价格之前定下来喵。
	groupRatioInfo := HandleGroupRatio(c, info)

	// 阶梯计费表达式同样支持按分组覆盖，按「最终分组 + 计费模型名」判一次计费方式喵。
	if billing_setting.GetBillingModeForGroup(info.UsingGroup, info.GetBillingModelName()) == billing_setting.BillingModeTieredExpr {
		return modelPriceHelperTiered(c, info, promptTokens, meta, groupRatioInfo)
	}

	// 合并分组定制价与全局价，得到该分组下此模型真正生效的定价快照喵。
	// 模型名用「计费模型名」：带 @修饰符 的请求在上方已归一到有价的基础模型喵。
	pricing := ratio_setting.ResolveModelPricing(info.UsingGroup, info.GetBillingModelName())
	modelPrice := pricing.ModelPrice
	usePrice := pricing.UsePrice

	var preConsumedQuota int
	var modelRatio float64
	var completionRatio float64
	var cacheRatio float64
	var imageRatio float64
	var cacheCreationRatio float64
	var cacheCreationRatio5m float64
	var cacheCreationRatio1h float64
	var audioRatio float64
	var audioCompletionRatio float64
	var freeModel bool
	if !usePrice {
		preConsumedTokens := common.Max(promptTokens, common.PreConsumedQuota)
		if meta.MaxTokens != 0 {
			preConsumedTokens += meta.MaxTokens
		}
		modelRatio = pricing.ModelRatio
		// 该分组与全局都没给这个模型配倍率时，除非用户开了「接受未定价模型」，否则直接拒绝请求喵。
		if !pricing.ModelRatioConfigured && !info.UserSetting.AcceptUnsetRatioModel {
			return hosttypes.PriceData{}, modelPriceNotConfiguredError(pricing.MatchedModelName, info.UserId)
		}
		completionRatio = pricing.CompletionRatio
		cacheRatio = pricing.CacheRatioValue()
		cacheCreationRatio = pricing.CreateCacheRatioValue()
		cacheCreationRatio5m = cacheCreationRatio
		// 固定1h和5min缓存写入价格的比例
		cacheCreationRatio1h = cacheCreationRatio * claudeCacheCreation1hMultiplier
		imageRatio = pricing.ImageRatioValue()
		audioRatio = pricing.AudioRatioValue()
		audioCompletionRatio = pricing.AudioCompletionRatioValue()
		ratio := modelRatio * groupRatioInfo.GroupRatio
		quota, err := common.QuotaFromFloatStrict(float64(preConsumedTokens) * ratio)
		if err != nil {
			return hosttypes.PriceData{}, err
		}
		preConsumedQuota = quota
	} else {
		if meta.ImagePriceRatio != 0 {
			modelPrice = modelPrice * meta.ImagePriceRatio
		}
	}

	// check if free model pre-consume is disabled
	if !operation_setting.GetQuotaSetting().EnableFreeModelPreConsume {
		// if model price or ratio is 0, do not pre-consume quota
		if groupRatioInfo.GroupRatio == 0 {
			preConsumedQuota = 0
			freeModel = true
		} else if usePrice {
			if modelPrice == 0 {
				preConsumedQuota = 0
				freeModel = true
			}
		} else {
			if modelRatio == 0 {
				preConsumedQuota = 0
				freeModel = true
			}
		}
	}

	priceData := hosttypes.PriceData{
		FreeModel:            freeModel,
		ModelPrice:           modelPrice,
		ModelRatio:           modelRatio,
		CompletionRatio:      completionRatio,
		GroupRatioInfo:       groupRatioInfo,
		UsePrice:             usePrice,
		CacheRatio:           cacheRatio,
		ImageRatio:           imageRatio,
		AudioRatio:           audioRatio,
		AudioCompletionRatio: audioCompletionRatio,
		CacheCreationRatio:   cacheCreationRatio,
		CacheCreation5mRatio: cacheCreationRatio5m,
		CacheCreation1hRatio: cacheCreationRatio1h,
		QuotaToPreConsume:    preConsumedQuota,
		// 记下命中的分组定制价来源，供消费日志审计「这笔为什么是这个价」喵。
		PricingGroupOverride: pricing.OverrideGroup,
	}
	if usePrice {
		for name, ratio := range meta.BillingRatios {
			priceData.AddOtherRatio(name, ratio)
		}
		quotaToPreConsume := priceData.ApplyOtherRatiosToFloat(modelPrice * common.QuotaPerUnit * groupRatioInfo.GroupRatio)
		quota, err := common.QuotaFromFloatStrict(quotaToPreConsume)
		if err != nil {
			return hosttypes.PriceData{}, err
		}
		priceData.QuotaToPreConsume = quota
	}

	if common.DebugEnabled {
		logger.LogDebug(c, "model_price_helper result: %s", priceData.ToSetting())
	}
	info.PriceData = priceData
	return priceData, nil
}

// ModelPriceHelperPerCall 按次/按量计费的 PriceHelper (MJ、Task)
func ModelPriceHelperPerCall(c *gin.Context, info *relaycommon.RelayInfo) (hosttypes.PriceData, error) {
	// 同 ModelPriceHelper：先定分组再取价，否则分组定制价会按错误的分组解析喵。
	groupRatioInfo := HandleGroupRatio(c, info)


	// 合并分组定制价与全局价：任务 / MJ 这类按次场景同样支持某个分组单独定价喵。
	pricing := ratio_setting.ResolveModelPricing(info.UsingGroup, info.OriginModelName)
	modelPrice := pricing.ModelPrice
	usePrice := pricing.UsePrice
	var modelRatio float64

	if !usePrice {
		// 分组与全局都没配按次价时，再退一步看内置默认价表（MJ 等老模型靠它兜底）喵。
		if defaultPrice, ok := ratio_setting.GetDefaultModelPriceMap()[info.OriginModelName]; ok {
			modelPrice = defaultPrice
			usePrice = true
		} else {
			// 沿用旧版 GetModelPrice(printErr=true) 的这条日志，方便管理员发现漏配喵。
			// 与旧版的唯一差别：内置默认价表能兜住的模型不再刷这条日志，噪音更少喵。
			common.SysError("model price not found: " + pricing.MatchedModelName)
			modelRatio = pricing.ModelRatio
			// 倍率也没配且用户没开「接受未定价模型」时直接拒绝，避免静默按兜底倍率扣费喵。
			if !pricing.ModelRatioConfigured && !info.UserSetting.AcceptUnsetRatioModel {
				return hosttypes.PriceData{}, modelPriceNotConfiguredError(pricing.MatchedModelName, info.UserId)
			}
		}
	}

	var quota int
	freeModel := false

	if usePrice {
		var err error
		quota, err = common.QuotaFromFloatStrict(modelPrice * common.QuotaPerUnit * groupRatioInfo.GroupRatio)
		if err != nil {
			return hosttypes.PriceData{}, err
		}
		if !operation_setting.GetQuotaSetting().EnableFreeModelPreConsume {
			if groupRatioInfo.GroupRatio == 0 || modelPrice == 0 {
				quota = 0
				freeModel = true
			}
		}
	} else {
		// 按量计费：以模型倍率的一半作为预扣额度
		var err error
		quota, err = common.QuotaFromFloatStrict(modelRatio / 2 * common.QuotaPerUnit * groupRatioInfo.GroupRatio)
		if err != nil {
			return hosttypes.PriceData{}, err
		}
		modelPrice = -1
		if !operation_setting.GetQuotaSetting().EnableFreeModelPreConsume {
			if groupRatioInfo.GroupRatio == 0 || modelRatio == 0 {
				quota = 0
				freeModel = true
			}
		}
	}

	priceData := hosttypes.PriceData{
		FreeModel:      freeModel,
		ModelPrice:     modelPrice,
		ModelRatio:     modelRatio,
		UsePrice:       usePrice,
		Quota:          quota,
		GroupRatioInfo: groupRatioInfo,
		// 记下命中的分组定制价来源，供任务日志审计「这笔为什么是这个价」喵。
		PricingGroupOverride: pricing.OverrideGroup,
	}
	return priceData, nil
}

// HasModelBillingConfig 判断某个模型有没有任何可用的计费配置（全局口径）喵。
// 只在拿不到分组上下文时使用；能确定分组时请用 HasModelBillingConfigForGroup 喵。
func HasModelBillingConfig(modelName string) bool {
	return HasModelBillingConfigForGroup("", modelName)
}

// HasModelBillingConfigForGroup 判断「某分组下某模型」有没有可用的计费配置喵。
// 分组定制价能让一个全局未定价的模型在该分组下变成已定价，所以模型可见性判断必须带上分组，
// 否则用户在自己分组明明有价的模型会被当成未定价而从模型列表里消失喵。
func HasModelBillingConfigForGroup(group string, modelName string) bool {
	pricing := ratio_setting.ResolveModelPricing(group, modelName)
	// 按次价可用（非哨兵值）即视为已定价喵。
	if pricing.UsePrice && pricing.ModelPrice >= 0 {
		return true
	}
	if pricing.ModelRatioConfigured {
		return true
	}
	if billing_setting.GetBillingModeForGroup(group, modelName) != billing_setting.BillingModeTieredExpr {
		return false
	}
	expr, ok := billing_setting.GetBillingExprForGroup(group, modelName)
	return ok && strings.TrimSpace(expr) != ""
}

// HasPriceOrRatioEntry 判断模型名在全局配置里是否有价格、倍率或阶梯表达式（上游引入，供计费模型名解析使用）喵。
func HasPriceOrRatioEntry(name string) bool {
	formatted := ratio_setting.FormatMatchingModelName(name)
	if _, ok := ratio_setting.GetModelPrice(formatted, false); ok {
		return true
	}
	if ratio_setting.HasConfiguredModelRatio(formatted) {
		return true
	}
	return billing_setting.GetBillingMode(formatted) == billing_setting.BillingModeTieredExpr
}

// resolveBillingModelName 把带 @修饰符 的模型名归一到真正用来计费的模型名（上游引入）喵。
// 归一顺序：原名（无修饰符时）→ 思考意图的等价写法 → 基础模型名，取第一个有价的；
// 命中思考后缀豁免名单时（CanonicalBillingModelNames 返回 nil）直接回落到基础模型名喵。
func resolveBillingModelName(origin string) string {
	var candidates []string
	if !reasoning.ParseModelModifiers(origin).HasModifiers() {
		candidates = append(candidates, origin)
	}
	candidates = append(candidates, hostreasoning.CanonicalBillingModelNames(origin)...)
	base := hostreasoning.BaseModelName(origin)
	candidates = append(candidates, base)

	seen := make(map[string]struct{}, len(candidates))
	for _, name := range candidates {
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		if HasPriceOrRatioEntry(name) {
			return name
		}
	}
	return base
}

func modelPriceHelperTiered(c *gin.Context, info *relaycommon.RelayInfo, promptTokens int, meta *types.TokenCountMeta, groupRatioInfo hosttypes.GroupRatioInfo) (hosttypes.PriceData, error) {
	// 计费模型名（上游口径）：没有别名归一时就等于 OriginModelName，供错误文案与阶梯快照记录用喵。
	billingModelName := info.GetBillingModelName()
	// 表达式按最终分组解析：分组级表达式优先，没配再回落全局表达式；模型名同样取计费模型名喵。
	exprStr, ok := billing_setting.GetBillingExprForGroup(info.UsingGroup, info.GetBillingModelName())
	if !ok {
		return hosttypes.PriceData{}, fmt.Errorf("model %s is configured as tiered_expr but has no billing expression", billingModelName)
	}
	if info.RelayFormat == types.RelayFormatOpenAIRealtime && billingexpr.UsesFixedPricing(exprStr) {
		return hosttypes.PriceData{}, fmt.Errorf("fixed pricing is not supported for Realtime requests")
	}

	estimatedCompletionTokens := meta.MaxTokens
	if estimatedCompletionTokens == 0 && groupRatioInfo.GroupRatio != 0 {
		estimatedCompletionTokens = defaultTieredPreConsumeMaxTokens
	}

	requestInput, err := ResolveIncomingBillingExprRequestInput(c, info)
	if err != nil {
		return hosttypes.PriceData{}, err
	}

	rawCost, trace, err := billingexpr.RunExprWithRequest(exprStr, billingexpr.TokenParams{
		P:   float64(promptTokens),
		C:   float64(estimatedCompletionTokens),
		Len: float64(promptTokens),
	}, requestInput)
	if err != nil {
		return hosttypes.PriceData{}, fmt.Errorf("model %s tiered expr run failed: %w", billingModelName, err)
	}

	// Expression coefficients are $/1M tokens prices; convert to quota the same way per-call billing does.
	quotaBeforeGroup := rawCost / 1_000_000 * common.QuotaPerUnit
	preConsumedQuota, err := billingexpr.QuotaRoundStrict(quotaBeforeGroup * groupRatioInfo.GroupRatio)
	if err != nil {
		return hosttypes.PriceData{}, err
	}

	freeModel := false
	if !operation_setting.GetQuotaSetting().EnableFreeModelPreConsume {
		if groupRatioInfo.GroupRatio == 0 {
			preConsumedQuota = 0
			freeModel = true
		}
	}

	exprHash := billingexpr.ExprHashString(exprStr)
	snapshot := &billingexpr.BillingSnapshot{
		BillingMode:               billing_setting.BillingModeTieredExpr,
		ModelName:                 billingModelName,
		ExprString:                exprStr,
		ExprHash:                  exprHash,
		GroupRatio:                groupRatioInfo.GroupRatio,
		EstimatedPromptTokens:     promptTokens,
		EstimatedCompletionTokens: estimatedCompletionTokens,
		EstimatedQuotaBeforeGroup: quotaBeforeGroup,
		EstimatedQuotaAfterGroup:  preConsumedQuota,
		EstimatedTier:             trace.MatchedTier,
		EstimatedBillingUnit:      trace.BillingUnit,
		EstimatedFixedPrice:       trace.FixedPrice,
		QuotaPerUnit:              common.QuotaPerUnit,
		ExprVersion:               billingexpr.ExprVersion(exprStr),
	}
	info.TieredBillingSnapshot = snapshot
	info.BillingRequestInput = &requestInput

	priceData := hosttypes.PriceData{
		FreeModel:         freeModel,
		GroupRatioInfo:    groupRatioInfo,
		QuotaToPreConsume: preConsumedQuota,
	}
	// 表达式来自分组定制时记下来源分组，供日志审计「这笔为什么按这个表达式算」喵。
	if _, fromGroup := billing_setting.GetGroupBillingExpr(info.UsingGroup, info.GetBillingModelName()); fromGroup {
		priceData.PricingGroupOverride = info.UsingGroup
	}

	logger.LogDebug(c, "model_price_helper_tiered result: model=%s preConsume=%d quotaBeforeGroup=%.2f groupRatio=%.2f tier=%s", billingModelName, preConsumedQuota, quotaBeforeGroup, groupRatioInfo.GroupRatio, trace.MatchedTier)

	info.PriceData = priceData
	return priceData, nil
}
