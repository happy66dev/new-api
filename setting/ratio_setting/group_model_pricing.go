package ratio_setting

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
)

// 分组定制计费方式的取值喵。
// 同一个模型 id 在不同分组下可以是完全不同的计费方式，用来贴合各家上游的真实报价喵。
const (
	// GroupBillingModeInherit 表示该分组不指定计费方式，继续沿用全局判定：
	// 全局配了按次价就按次，否则按量喵。
	GroupBillingModeInherit = ""
	// GroupBillingModePerToken 表示该分组强制按量计费，用倍率乘 token 数结算喵。
	GroupBillingModePerToken = "per_token"
	// GroupBillingModePerCall 表示该分组强制按次计费，每次请求收固定美元价喵。
	GroupBillingModePerCall = "per_call"
)

// GroupModelPriceOverride 是「某个分组 × 某个模型」的定价覆盖项喵。
// 所有价格字段都用 *float64：nil 表示该字段继续继承全局配置，非 nil 才真正覆盖喵。
// 用指针而不是裸 float64 是为了区分「没填」和「显式填 0（真免费）」这两种完全不同的意图喵。
type GroupModelPriceOverride struct {
	// BillingMode 该分组下此模型的计费方式，取值见上方 GroupBillingMode* 常量喵。
	BillingMode string `json:"billing_mode,omitempty"`
	// ModelPrice 按次计费单价，单位是美元/次喵。
	ModelPrice *float64 `json:"model_price,omitempty"`
	// ModelRatio 按量计费的输入倍率，实际输入价 = 倍率 × 2 美元/百万 token 喵。
	ModelRatio *float64 `json:"model_ratio,omitempty"`
	// CompletionRatio 输出倍率，输出价 = 输入价 × 该倍率喵。
	CompletionRatio *float64 `json:"completion_ratio,omitempty"`
	// CacheRatio 缓存命中（读取）倍率；上游没有缓存优惠时填 1 即可喵。
	CacheRatio *float64 `json:"cache_ratio,omitempty"`
	// CreateCacheRatio 缓存写入倍率，未配置时全局默认 1.25 喵。
	CreateCacheRatio *float64 `json:"create_cache_ratio,omitempty"`
	// ImageRatio 图片输入倍率喵。
	ImageRatio *float64 `json:"image_ratio,omitempty"`
	// AudioRatio 音频输入倍率喵。
	AudioRatio *float64 `json:"audio_ratio,omitempty"`
	// AudioCompletionRatio 音频输出倍率喵。
	AudioCompletionRatio *float64 `json:"audio_completion_ratio,omitempty"`
}

// ResolvedModelPricing 是「某分组 × 某模型」经过分组定制与全局配置合并后的最终定价快照喵。
// 计费链路只认这个快照，从预扣费到结算全程复用同一份，避免中途重复读配置导致口径漂移喵。
type ResolvedModelPricing struct {
	// UsePrice 为 true 表示按次计费，false 表示按量计费喵。
	UsePrice bool
	// ModelPrice 按次计费单价（美元/次），仅 UsePrice 为 true 时有意义喵。
	ModelPrice float64
	// ModelRatio 按量计费的输入倍率喵。
	ModelRatio float64
	// ModelRatioConfigured 表示倍率是真配过的，而不是「未配置」时的兜底值喵。
	// 未配置时计费链路要按未定价模型处理（报错或走用户的接受未定价开关）喵。
	ModelRatioConfigured bool
	// MatchedModelName 是通配符归一化之后真正命中的模型名，报错文案要用它喵。
	MatchedModelName string
	// CompletionRatio 输出倍率喵。
	CompletionRatio float64
	// 以下可选倍率用指针透传「是否配置过」，模型广场靠它决定要不要展示对应价格行喵。
	CacheRatio           *float64
	CreateCacheRatio     *float64
	ImageRatio           *float64
	AudioRatio           *float64
	AudioCompletionRatio *float64
	// OverrideGroup 命中定制价的分组名；为空表示这次完全走全局配置喵。
	OverrideGroup string
}

// FromGroupOverride 表示这份定价是否来自分组定制，供日志审计与前端标记使用喵。
func (p ResolvedModelPricing) FromGroupOverride() bool {
	return p.OverrideGroup != ""
}

// CacheRatioValue 返回缓存读取倍率，未配置时回退全局默认值 1（等价于没有缓存优惠）喵。
func (p ResolvedModelPricing) CacheRatioValue() float64 {
	// 喵~防御：指针为空说明该分组与全局都没配，按 1 处理，缓存 token 与普通输入同价喵。
	if p.CacheRatio == nil {
		return 1
	}
	return *p.CacheRatio
}

// CreateCacheRatioValue 返回缓存写入倍率，未配置时回退全局默认值 1.25 喵。
func (p ResolvedModelPricing) CreateCacheRatioValue() float64 {
	// 喵~防御：指针为空时用与 GetCreateCacheRatio 一致的兜底值，保持行为不变喵。
	if p.CreateCacheRatio == nil {
		return 1.25
	}
	return *p.CreateCacheRatio
}

// ImageRatioValue 返回图片输入倍率，未配置时回退全局默认值 1 喵。
func (p ResolvedModelPricing) ImageRatioValue() float64 {
	// 喵~防御：指针为空时与 GetImageRatio 的兜底一致，图片 token 按普通输入价计喵。
	if p.ImageRatio == nil {
		return 1
	}
	return *p.ImageRatio
}

// AudioRatioValue 返回音频输入倍率，未配置时回退全局默认值 1 喵。
func (p ResolvedModelPricing) AudioRatioValue() float64 {
	// 喵~防御：指针为空时按 1 处理，音频 token 与普通输入同价喵。
	if p.AudioRatio == nil {
		return 1
	}
	return *p.AudioRatio
}

// AudioCompletionRatioValue 返回音频输出倍率，未配置时回退全局默认值 1 喵。
func (p ResolvedModelPricing) AudioCompletionRatioValue() float64 {
	// 喵~防御：指针为空时按 1 处理，音频输出不额外加价喵。
	if p.AudioCompletionRatio == nil {
		return 1
	}
	return *p.AudioCompletionRatio
}

// groupModelPricingMap 存「分组名 -> 模型名 -> 定价覆盖项」两层映射喵。
// 沿用 groupGroupRatioMap 的嵌套 RWMap 写法，读多写少、热路径只加读锁喵。
var groupModelPricingMap = types.NewRWMap[string, map[string]GroupModelPriceOverride]()

// GroupModelPricing2JSONString 把分组定制定价序列化成 JSON，供 OptionMap 下发给管理端喵。
func GroupModelPricing2JSONString() string {
	return groupModelPricingMap.MarshalJSONString()
}

// UpdateGroupModelPricingByJSONString 从 JSON 覆盖整份分组定制定价配置喵。
// 成功后顺带失效暴露给前端的倍率缓存，避免管理端改完价还看到旧值喵。
func UpdateGroupModelPricingByJSONString(jsonStr string) error {
	return types.LoadFromJsonStringWithCallback(groupModelPricingMap, jsonStr, InvalidateExposedDataCache)
}

// GetGroupModelPricingCopy 返回整份配置的浅拷贝，供管理端展示与上游价格同步比对喵。
func GetGroupModelPricingCopy() map[string]map[string]GroupModelPriceOverride {
	return groupModelPricingMap.ReadAll()
}

// GetGroupModelOverride 查「某分组 × 某模型」有没有配定制定价喵。
// 查找口径与全局倍率保持一致：先按请求里的原始模型名精确匹配，
// 再按通配符归一化后的名字（例如 gpt-4-gizmo-* ）匹配一次喵。
func GetGroupModelOverride(group, model string) (GroupModelPriceOverride, bool) {
	// 喵~防御：分组名或模型名为空时直接判为没有定制，避免拿空 key 去查表喵。
	if group == "" || model == "" {
		return GroupModelPriceOverride{}, false
	}
	models, ok := groupModelPricingMap.Get(group)
	// 喵~防御：该分组没配过或配成空表时按没有定制处理喵。
	if !ok || len(models) == 0 {
		return GroupModelPriceOverride{}, false
	}
	if override, exists := models[model]; exists {
		return override, true
	}
	matchingName := FormatMatchingModelName(model)
	// 归一化后的名字与原名不同才值得再查一次，避免重复查同一个 key 喵。
	if matchingName != model {
		if override, exists := models[matchingName]; exists {
			return override, true
		}
	}
	return GroupModelPriceOverride{}, false
}

// ResolveModelPricing 合并「分组定制价」与「全局定价」，产出该分组下此模型的最终定价快照喵。
//
// 整体思路分三步喵：
//  1. 先把全局定价铺成底：全局配了按次价就先按次，否则按量，并带上各类可选倍率；
//  2. 再看该分组有没有定制项，有就逐字段覆盖，字段为 nil 的继续继承全局；
//  3. 最后由 billing_mode 拍定最终是按次还是按量——它既能把只配了倍率的模型强制改成按次，
//     也能把配了按次价的模型强制改回按量，用来贴合不同上游各自的报价方式喵。
//
// 边界与约定喵：
//   - 本函数不乘分组倍率（GroupRatio）。语义是「分组定制价 × 分组倍率 = 最终价」，
//     分组倍率由调用方在拿到快照后统一乘，这样 VIP 折扣仍能叠加在定制价之上喵。
//   - group 传空串时等价于「没有任何定制」，直接返回纯全局定价，老调用方行为不变喵。
//   - 阶梯计费表达式不在这里处理，它归 billing_setting 管，由调用方先判表达式再回落到本函数喵。
func ResolveModelPricing(group, model string) ResolvedModelPricing {
	globalPrice, hasPrice := GetModelPrice(model, false)
	modelRatio, ratioConfigured, matchedName := GetModelRatio(model)
	resolved := ResolvedModelPricing{
		UsePrice:             hasPrice,
		ModelPrice:           globalPrice,
		ModelRatio:           modelRatio,
		ModelRatioConfigured: ratioConfigured,
		MatchedModelName:     matchedName,
		CompletionRatio:      GetCompletionRatio(model),
	}
	// 可选倍率只在全局真的配过时才带上指针，模型广场靠这个决定要不要展示对应价格行喵。
	if ratio, ok := GetCacheRatio(model); ok {
		resolved.CacheRatio = &ratio
	}
	if ratio, ok := GetCreateCacheRatio(model); ok {
		resolved.CreateCacheRatio = &ratio
	}
	if ratio, ok := GetImageRatio(model); ok {
		resolved.ImageRatio = &ratio
	}
	if ContainsAudioRatio(model) {
		ratio := GetAudioRatio(model)
		resolved.AudioRatio = &ratio
	}
	if ContainsAudioCompletionRatio(model) {
		ratio := GetAudioCompletionRatio(model)
		resolved.AudioCompletionRatio = &ratio
	}

	override, hasOverride := GetGroupModelOverride(group, model)
	// 没有分组定制就直接返回纯全局定价，热路径少走一段分支喵。
	if !hasOverride {
		return normalizeResolvedPricing(resolved)
	}
	resolved.OverrideGroup = group
	return normalizeResolvedPricing(applyGroupPriceOverride(resolved, override, hasPrice))
}

// normalizeResolvedPricing 收口按次价哨兵值，保证快照对外语义始终一致喵。
//
// 两条约定喵：
//  1. ModelPrice 只在按次计费时有意义；按量计费时统一写成 -1，
//     与历史上 GetModelPrice 未命中返回 -1 的口径一致，
//     这样消费日志里的 model_price 字段含义不变，前端判「是否按次」的逻辑也不用改喵。
//  2. 万一声明了按次却拿不到有效单价（负数或哨兵），就降级成按量计费，
//     绝不让负单价流进额度计算变成给用户返钱；后面按量分支会正常报未定价错误喵。
func normalizeResolvedPricing(resolved ResolvedModelPricing) ResolvedModelPricing {
	// 喵~防御：按次价缺失或为负时降级成按量，避免负额度这种资金安全问题喵。
	if resolved.UsePrice && resolved.ModelPrice < 0 {
		resolved.UsePrice = false
	}
	if !resolved.UsePrice {
		resolved.ModelPrice = -1
	}
	return resolved
}

// applyGroupPriceOverride 把一条分组定制项叠加到全局底价上，产出最终定价快照喵。
// hasGlobalPrice 是「全局本来有没有配按次价」，用于 billing_mode 留空时判断该按次还是按量喵。
func applyGroupPriceOverride(resolved ResolvedModelPricing, override GroupModelPriceOverride, hasGlobalPrice bool) ResolvedModelPricing {
	hasPrice := hasGlobalPrice
	// 定制了按次价：覆盖单价，并记下「这个分组是有按次价可用的」喵。
	if override.ModelPrice != nil {
		resolved.ModelPrice = *override.ModelPrice
		hasPrice = true
	}
	// 定制了输入倍率：覆盖倍率，同时把「已配置」置真，让原本未定价的模型在本分组变成已定价喵。
	if override.ModelRatio != nil {
		resolved.ModelRatio = *override.ModelRatio
		resolved.ModelRatioConfigured = true
	}
	if override.CompletionRatio != nil {
		resolved.CompletionRatio = *override.CompletionRatio
	}
	// 可选倍率逐个覆盖；这里复制一份局部变量再取地址，避免多个快照共享同一个指针喵。
	if override.CacheRatio != nil {
		ratio := *override.CacheRatio
		resolved.CacheRatio = &ratio
	}
	if override.CreateCacheRatio != nil {
		ratio := *override.CreateCacheRatio
		resolved.CreateCacheRatio = &ratio
	}
	if override.ImageRatio != nil {
		ratio := *override.ImageRatio
		resolved.ImageRatio = &ratio
	}
	if override.AudioRatio != nil {
		ratio := *override.AudioRatio
		resolved.AudioRatio = &ratio
	}
	if override.AudioCompletionRatio != nil {
		ratio := *override.AudioCompletionRatio
		resolved.AudioCompletionRatio = &ratio
	}

	// 最后由计费方式拍定按次还是按量：显式指定优先，留空则看这个分组有没有按次价可用喵。
	switch strings.TrimSpace(override.BillingMode) {
	case GroupBillingModePerCall:
		resolved.UsePrice = true
	case GroupBillingModePerToken:
		resolved.UsePrice = false
	default:
		resolved.UsePrice = hasPrice
	}
	return resolved
}

// CheckGroupModelPricing 在保存前校验分组定制定价配置喵。
// 计费相关配置一旦写进去就会直接影响真金白银，所以这里宁可拦下来也不放过可疑值喵。
func CheckGroupModelPricing(jsonStr string) error {
	pricing := make(map[string]map[string]GroupModelPriceOverride)
	if err := common.Unmarshal([]byte(jsonStr), &pricing); err != nil {
		return fmt.Errorf("分组定制定价必须是「分组 -> 模型 -> 定价」的 JSON 对象: %w", err)
	}
	for group, models := range pricing {
		// 喵~防御：分组名为空会让定价永远命中不到任何请求，属于配置错误，直接拦喵。
		if strings.TrimSpace(group) == "" {
			return errors.New("分组名不能为空")
		}
		for modelName, override := range models {
			// 喵~防御：模型名为空同理，命中不到任何模型喵。
			if strings.TrimSpace(modelName) == "" {
				return fmt.Errorf("分组 %s 下存在空的模型名", group)
			}
			if err := checkGroupModelOverride(group, modelName, override); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkGroupModelOverride 校验单条「分组 × 模型」定制项的计费方式与各项价格喵。
func checkGroupModelOverride(group, modelName string, override GroupModelPriceOverride) error {
	billingMode := strings.TrimSpace(override.BillingMode)
	switch billingMode {
	case GroupBillingModeInherit, GroupBillingModePerToken, GroupBillingModePerCall:
		// 三种合法取值，继续往下校验价格喵。
	default:
		return fmt.Errorf("分组 %s 模型 %s 的计费方式 %q 无效，只能是空、%s 或 %s",
			group, modelName, override.BillingMode, GroupBillingModePerToken, GroupBillingModePerCall)
	}

	// 逐个校验价格与倍率：必须是有限的非负数，负数会算出负额度变成给用户充钱喵。
	numericFields := map[string]*float64{
		"按次价格":   override.ModelPrice,
		"模型倍率":   override.ModelRatio,
		"补全倍率":   override.CompletionRatio,
		"缓存倍率":   override.CacheRatio,
		"缓存写入倍率": override.CreateCacheRatio,
		"图片倍率":   override.ImageRatio,
		"音频倍率":   override.AudioRatio,
		"音频补全倍率": override.AudioCompletionRatio,
	}
	for label, value := range numericFields {
		// 未填的字段表示继承全局，跳过不校验喵。
		if value == nil {
			continue
		}
		// 喵~防御：NaN 与 Inf 会一路污染到额度计算，必须在入口就拦住喵。
		if math.IsNaN(*value) || math.IsInf(*value, 0) {
			return fmt.Errorf("分组 %s 模型 %s 的%s必须是有限数值", group, modelName, label)
		}
		// 喵~防御：负价格会产生负额度（等于给用户返钱），绝对不允许喵。
		if *value < 0 {
			return fmt.Errorf("分组 %s 模型 %s 的%s不能为负数", group, modelName, label)
		}
	}

	// 指定了按次计费却既没填定制单价、全局也没配按次价，会静默变成 0 元/次，必须拦喵。
	if billingMode == GroupBillingModePerCall && override.ModelPrice == nil {
		if _, ok := GetModelPrice(modelName, false); !ok {
			return fmt.Errorf("分组 %s 模型 %s 指定了按次计费，但没有填按次价格，且全局也没有配置按次价", group, modelName)
		}
	}
	return nil
}
