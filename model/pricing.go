package model

import (
	"fmt"
	"maps"
	"strings"

	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
)

// GroupPricingEntry 是「某个分组」对某个模型的最终定价，供模型广场按分组展示真实价格喵。
//
// 为什么需要它喵：
//
//	同一个模型 id 在不同分组下可以有完全不同的计费方式和价格（比如 A 组按次、B 组按量），
//	光靠「全局价 × 分组倍率」没法表达，所以要把每个定制分组的价格单独下发给前端喵。
//
// 边界与约定喵：
//   - 只有真的配了分组定制的分组才会出现在 Pricing.GroupPricing 里；没出现的分组
//     前端继续用 Pricing 顶层的全局价，行为与改造前完全一致喵。
//   - 这里下发的是「未乘分组倍率」的基础价，分组倍率仍由前端按 group_ratio 另乘，
//     与后端「分组定制价 × 分组倍率 = 最终价」的语义保持一致喵。
//   - 数值字段刻意不加 omitempty：真配成 0（免费）时必须如实下发 0，
//     否则会被 JSON 省略、前端错误地回落到全局价喵。
type GroupPricingEntry struct {
	// QuotaType 计费方式：0 表示按量计费，1 表示按次计费，与 Pricing.QuotaType 同义喵。
	QuotaType int `json:"quota_type"`
	// ModelRatio 按量计费的输入倍率，仅 QuotaType 为 0 时有意义喵。
	ModelRatio float64 `json:"model_ratio"`
	// ModelPrice 按次计费单价（美元/次），仅 QuotaType 为 1 时有意义喵。
	ModelPrice float64 `json:"model_price"`
	// CompletionRatio 输出倍率，仅按量计费时有意义喵。
	CompletionRatio float64 `json:"completion_ratio"`
	// 以下可选倍率沿用顶层 Pricing 的指针语义：nil 表示该分组没有这项价格，前端不展示对应行喵。
	CacheRatio           *float64 `json:"cache_ratio,omitempty"`
	CreateCacheRatio     *float64 `json:"create_cache_ratio,omitempty"`
	ImageRatio           *float64 `json:"image_ratio,omitempty"`
	AudioRatio           *float64 `json:"audio_ratio,omitempty"`
	AudioCompletionRatio *float64 `json:"audio_completion_ratio,omitempty"`
	// BillingMode 为 tiered_expr 时表示该分组走阶梯计费表达式，价格全部由 BillingExpr 描述喵。
	BillingMode string `json:"billing_mode,omitempty"`
	BillingExpr string `json:"billing_expr,omitempty"`
}

type Pricing struct {
	ModelName              string                  `json:"model_name"`
	Description            string                  `json:"description,omitempty"`
	Icon                   string                  `json:"icon,omitempty"`
	Tags                   string                  `json:"tags,omitempty"`
	VendorID               int                     `json:"vendor_id,omitempty"`
	QuotaType              int                     `json:"quota_type"`
	ModelRatio             float64                 `json:"model_ratio"`
	ModelPrice             float64                 `json:"model_price"`
	OwnerBy                string                  `json:"owner_by"`
	CompletionRatio        float64                 `json:"completion_ratio"`
	CacheRatio             *float64                `json:"cache_ratio,omitempty"`
	CreateCacheRatio       *float64                `json:"create_cache_ratio,omitempty"`
	ImageRatio             *float64                `json:"image_ratio,omitempty"`
	AudioRatio             *float64                `json:"audio_ratio,omitempty"`
	AudioCompletionRatio   *float64                `json:"audio_completion_ratio,omitempty"`
	EnableGroup            []string                `json:"enable_groups"`
	SupportedEndpointTypes []constant.EndpointType `json:"supported_endpoint_types"`
	BillingMode            string                  `json:"billing_mode,omitempty"`
	BillingExpr            string                  `json:"billing_expr,omitempty"`
	// GroupPricing 存「分组名 -> 该分组定制价」，只包含真的配过定制的分组喵。
	GroupPricing map[string]GroupPricingEntry `json:"group_pricing,omitempty"`
	// 任务插件计费的 usage 字段 schema 与示例，随插件注册表下发给前端展示（上游引入）喵。
	BillingUsageSchema   map[string]jsplugin.UsageFieldSchema `json:"billing_usage_schema,omitempty"`
	BillingUsageExamples []jsplugin.UsageExample              `json:"billing_usage_examples,omitempty"`
	PricingVersion       string                               `json:"pricing_version,omitempty"`
	// 以下字段仅用于用户共享模型条目，普通模型不填充喵。
	// 共享剩余额度与上限对所有查看者可见；所有者附加的余额/可用额度字段受展示开关控制喵。
	ShareRemainingCents *int64 `json:"share_remaining_cents,omitempty"`
	ShareLimitCents     *int64 `json:"share_limit_cents,omitempty"`
	ShareOwnerUserID    int    `json:"share_owner_user_id,omitempty"`
	BalanceCents        *int64 `json:"balance_cents,omitempty"`
	AvailableCents      *int64 `json:"available_cents,omitempty"`
}

type PricingVendor struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
}

var (
	pricingMap           []Pricing
	vendorsList          []PricingVendor
	supportedEndpointMap map[string]common.EndpointInfo
	lastGetPricingTime   time.Time
	updatePricingLock    sync.Mutex

	// 缓存映射：模型名 -> 启用分组 / 计费类型
	modelEnableGroups     = make(map[string][]string)
	modelQuotaTypeMap     = make(map[string]int)
	modelEnableGroupsLock = sync.RWMutex{}
)

var (
	modelSupportEndpointTypes = make(map[string][]constant.EndpointType)
	modelSupportEndpointsLock = sync.RWMutex{}
)

func GetPricing() []Pricing {
	if time.Since(lastGetPricingTime) > time.Minute*1 || len(pricingMap) == 0 {
		updatePricingLock.Lock()
		defer updatePricingLock.Unlock()
		// Double check after acquiring the lock
		if time.Since(lastGetPricingTime) > time.Minute*1 || len(pricingMap) == 0 {
			modelSupportEndpointsLock.Lock()
			defer modelSupportEndpointsLock.Unlock()
			updatePricing()
		}
	}
	return pricingMap
}

func InvalidatePricingCache() {
	updatePricingLock.Lock()
	defer updatePricingLock.Unlock()

	pricingMap = nil
	vendorsList = nil
	lastGetPricingTime = time.Time{}
}

// GetVendors 返回当前定价接口使用到的供应商信息
func GetVendors() []PricingVendor {
	if time.Since(lastGetPricingTime) > time.Minute*1 || len(pricingMap) == 0 {
		// 保证先刷新一次
		GetPricing()
	}
	return vendorsList
}

func GetModelSupportEndpointTypes(model string) []constant.EndpointType {
	if model == "" {
		return make([]constant.EndpointType, 0)
	}
	modelSupportEndpointsLock.RLock()
	defer modelSupportEndpointsLock.RUnlock()
	if endpoints, ok := modelSupportEndpointTypes[model]; ok {
		return endpoints
	}
	return make([]constant.EndpointType, 0)
}

func getPricingEndpointTypesForAbility(ability AbilityWithChannel, advancedCustomConfigs map[int]*dto.AdvancedCustomConfig) []constant.EndpointType {
	if ability.ChannelType != constant.ChannelTypeAdvancedCustom {
		return common.GetEndpointTypesByChannelType(ability.ChannelType, ability.Model)
	}
	if config := advancedCustomConfigs[ability.ChannelId]; config != nil {
		return config.SupportedEndpointTypesForModel(ability.Model)
	}
	return common.GetEndpointTypesByChannelType(ability.ChannelType, ability.Model)
}

// loadPricingAdvancedCustomConfigs runs inside updatePricing while
// updatePricingLock is held, and nests channelSyncLock.RLock. This defines the
// global lock order updatePricingLock -> channelSyncLock: any code path holding
// channelSyncLock must release it before touching the pricing cache (see
// InitChannelCache / CacheUpdateChannel), otherwise it deadlocks.
// The returned configs are pointers shared with the channel cache; they are
// replaced wholesale on update and never mutated in place, so reading them after
// RUnlock is safe.
func loadPricingAdvancedCustomConfigs(enableAbilities []AbilityWithChannel) map[int]*dto.AdvancedCustomConfig {
	channelIDs := make([]int, 0)
	seen := make(map[int]struct{})
	for _, ability := range enableAbilities {
		if ability.ChannelType != constant.ChannelTypeAdvancedCustom {
			continue
		}
		if _, exists := seen[ability.ChannelId]; exists {
			continue
		}
		seen[ability.ChannelId] = struct{}{}
		channelIDs = append(channelIDs, ability.ChannelId)
	}
	if len(channelIDs) == 0 {
		return nil
	}

	configs := make(map[int]*dto.AdvancedCustomConfig, len(channelIDs))
	if common.MemoryCacheEnabled {
		channelSyncLock.RLock()
		defer channelSyncLock.RUnlock()
		for _, channelID := range channelIDs {
			if config := channel2advancedCustomConfig[channelID]; config != nil {
				configs[channelID] = config
			}
		}
		return configs
	}

	for _, channelID := range channelIDs {
		channel, err := CacheGetChannel(channelID)
		if err != nil {
			common.SysLog(fmt.Sprintf("load advanced custom channel settings error: channel_id=%d, error=%v", channelID, err))
			continue
		}
		if channel.Type != constant.ChannelTypeAdvancedCustom {
			continue
		}
		if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
			configs[channelID] = config
		}
	}
	return configs
}

func appendPricingEndpoint(endpoints []string, endpoint string) []string {
	if endpoint == "" || common.StringsContains(endpoints, endpoint) {
		return endpoints
	}
	return append(endpoints, endpoint)
}

func updatePricing() {
	//modelRatios := common.GetModelRatios()
	enableAbilities, err := GetAllEnableAbilityWithChannels()
	if err != nil {
		common.SysLog(fmt.Sprintf("GetAllEnableAbilityWithChannels error: %v", err))
		return
	}
	// 预加载模型元数据与供应商一次，避免循环查询
	var allMeta []Model
	_ = DB.Find(&allMeta).Error
	metaMap := make(map[string]*Model)
	prefixList := make([]*Model, 0)
	suffixList := make([]*Model, 0)
	containsList := make([]*Model, 0)
	for i := range allMeta {
		m := &allMeta[i]
		if m.NameRule == NameRuleExact {
			metaMap[m.ModelName] = m
		} else {
			switch m.NameRule {
			case NameRulePrefix:
				prefixList = append(prefixList, m)
			case NameRuleSuffix:
				suffixList = append(suffixList, m)
			case NameRuleContains:
				containsList = append(containsList, m)
			}
		}
	}

	// 将非精确规则模型匹配到 metaMap
	for _, m := range prefixList {
		for _, pricingModel := range enableAbilities {
			if strings.HasPrefix(pricingModel.Model, m.ModelName) {
				if _, exists := metaMap[pricingModel.Model]; !exists {
					metaMap[pricingModel.Model] = m
				}
			}
		}
	}
	for _, m := range suffixList {
		for _, pricingModel := range enableAbilities {
			if strings.HasSuffix(pricingModel.Model, m.ModelName) {
				if _, exists := metaMap[pricingModel.Model]; !exists {
					metaMap[pricingModel.Model] = m
				}
			}
		}
	}
	for _, m := range containsList {
		for _, pricingModel := range enableAbilities {
			if strings.Contains(pricingModel.Model, m.ModelName) {
				if _, exists := metaMap[pricingModel.Model]; !exists {
					metaMap[pricingModel.Model] = m
				}
			}
		}
	}

	// 预加载供应商
	var vendors []Vendor
	_ = DB.Find(&vendors).Error
	vendorMap := make(map[int]*Vendor)
	for i := range vendors {
		vendorMap[vendors[i].Id] = &vendors[i]
	}

	// 初始化默认供应商映射
	initDefaultVendorMapping(metaMap, vendorMap, enableAbilities)

	// 构建对前端友好的供应商列表
	vendorsList = make([]PricingVendor, 0, len(vendorMap))
	for _, v := range vendorMap {
		vendorsList = append(vendorsList, PricingVendor{
			ID:          v.Id,
			Name:        v.Name,
			Description: v.Description,
			Icon:        v.Icon,
		})
	}

	modelGroupsMap := make(map[string]*types.Set[string])

	for _, ability := range enableAbilities {
		groups, ok := modelGroupsMap[ability.Model]
		if !ok {
			groups = types.NewSet[string]()
			modelGroupsMap[ability.Model] = groups
		}
		groups.Add(ability.Group)
	}

	//这里使用切片而不是Set，因为一个模型可能支持多个端点类型，并且第一个端点是优先使用端点
	modelSupportEndpointsStr := make(map[string][]string)
	advancedCustomConfigs := loadPricingAdvancedCustomConfigs(enableAbilities)

	// 先根据已有能力填充原生端点
	for _, ability := range enableAbilities {
		endpoints := modelSupportEndpointsStr[ability.Model]
		channelTypes := getPricingEndpointTypesForAbility(ability, advancedCustomConfigs)
		for _, channelType := range channelTypes {
			if !common.StringsContains(endpoints, string(channelType)) {
				endpoints = append(endpoints, string(channelType))
			}
		}
		modelSupportEndpointsStr[ability.Model] = endpoints
	}

	// 再补充模型自定义端点：若配置有效则追加到已有推断，不再裁剪渠道真实能力
	for modelName, meta := range metaMap {
		if strings.TrimSpace(meta.Endpoints) == "" {
			continue
		}
		var raw map[string]interface{}
		if err := common.Unmarshal([]byte(meta.Endpoints), &raw); err == nil {
			endpoints := modelSupportEndpointsStr[modelName]
			for k, v := range raw {
				switch v.(type) {
				case string, map[string]interface{}:
					endpoints = appendPricingEndpoint(endpoints, k)
				}
			}
			if len(endpoints) > 0 {
				modelSupportEndpointsStr[modelName] = endpoints
			}
		}
	}

	modelSupportEndpointTypes = make(map[string][]constant.EndpointType)
	for model, endpoints := range modelSupportEndpointsStr {
		supportedEndpoints := make([]constant.EndpointType, 0)
		for _, endpointStr := range endpoints {
			endpointType := constant.EndpointType(endpointStr)
			supportedEndpoints = append(supportedEndpoints, endpointType)
		}
		modelSupportEndpointTypes[model] = supportedEndpoints
	}

	// 构建全局 supportedEndpointMap（默认 + 自定义覆盖）
	supportedEndpointMap = make(map[string]common.EndpointInfo)
	// 1. 默认端点
	for _, endpoints := range modelSupportEndpointTypes {
		for _, et := range endpoints {
			if info, ok := common.GetDefaultEndpointInfo(et); ok {
				if _, exists := supportedEndpointMap[string(et)]; !exists {
					supportedEndpointMap[string(et)] = info
				}
			}
		}
	}
	// 2. 自定义端点（models 表）覆盖默认
	for _, meta := range metaMap {
		if strings.TrimSpace(meta.Endpoints) == "" {
			continue
		}
		var raw map[string]interface{}
		if err := common.Unmarshal([]byte(meta.Endpoints), &raw); err == nil {
			for k, v := range raw {
				switch val := v.(type) {
				case string:
					supportedEndpointMap[k] = common.EndpointInfo{Path: val, Method: "POST"}
				case map[string]interface{}:
					ep := common.EndpointInfo{Method: "POST"}
					if p, ok := val["path"].(string); ok {
						ep.Path = p
					}
					if m, ok := val["method"].(string); ok {
						ep.Method = strings.ToUpper(m)
					}
					supportedEndpointMap[k] = ep
				default:
					// ignore unsupported types
				}
			}
		}
	}

	pricingMap = make([]Pricing, 0)
	pluginGeneration := jsplugin.DefaultRegistry.Generation()
	for model, groups := range modelGroupsMap {
		pricing := Pricing{
			ModelName:              model,
			EnableGroup:            groups.Items(),
			SupportedEndpointTypes: modelSupportEndpointTypes[model],
		}

		// 补充模型元数据（描述、标签、供应商、状态）
		if meta, ok := metaMap[model]; ok {
			// 若模型被禁用(status!=1)，则直接跳过，不返回给前端
			if meta.Status != 1 {
				continue
			}
			pricing.Description = meta.Description
			pricing.Icon = meta.Icon
			pricing.Tags = meta.Tags
			pricing.VendorID = meta.VendorID
		}
		modelPrice, findPrice := ratio_setting.GetModelPrice(model, false)
		if findPrice {
			pricing.ModelPrice = modelPrice
			pricing.QuotaType = 1
		} else {
			modelRatio, _, _ := ratio_setting.GetModelRatio(model)
			pricing.ModelRatio = modelRatio
			pricing.CompletionRatio = ratio_setting.GetCompletionRatio(model)
			pricing.QuotaType = 0
		}
		if cacheRatio, ok := ratio_setting.GetCacheRatio(model); ok {
			pricing.CacheRatio = &cacheRatio
		}
		if createCacheRatio, ok := ratio_setting.GetCreateCacheRatio(model); ok {
			pricing.CreateCacheRatio = &createCacheRatio
		}
		if imageRatio, ok := ratio_setting.GetImageRatio(model); ok {
			pricing.ImageRatio = &imageRatio
		}
		if ratio_setting.ContainsAudioRatio(model) {
			audioRatio := ratio_setting.GetAudioRatio(model)
			pricing.AudioRatio = &audioRatio
		}
		if ratio_setting.ContainsAudioCompletionRatio(model) {
			audioCompletionRatio := ratio_setting.GetAudioCompletionRatio(model)
			pricing.AudioCompletionRatio = &audioCompletionRatio
		}
		if billingMode := billing_setting.GetBillingMode(model); billingMode == "tiered_expr" {
			if expr, ok := billing_setting.GetBillingExpr(model); ok && strings.TrimSpace(expr) != "" {
				pricing.BillingMode = billingMode
				pricing.BillingExpr = expr
			}
		} else if target, resolved := ResolveTaskModelAlias(pluginGeneration, model); resolved && target.Declared != "" {
			if tailMode := billing_setting.GetBillingMode(target.Declared); tailMode == "tiered_expr" {
				if expr, ok := billing_setting.GetBillingExpr(target.Declared); ok && strings.TrimSpace(expr) != "" {
					pricing.BillingMode = tailMode
					pricing.BillingExpr = expr
				}
			}
		}
		// 逐个启用分组算一遍该分组的定制价：只有真配了定制的分组才下发，
		// 没配的分组前端继续用顶层全局价，老站点的展示行为完全不变喵。
		pricing.GroupPricing = buildGroupPricingEntries(model, groups.Items())

		plugin, ok := pluginGeneration.GetByModel(model)
		if !ok {
			if target, resolved := ResolveTaskModelAlias(pluginGeneration, model); resolved {
				plugin, ok = pluginGeneration.Get(target.PluginKey)
			}
		}
		if ok && plugin != nil && len(plugin.Meta.UsageSchema) > 0 {
			pricing.BillingUsageSchema = make(map[string]jsplugin.UsageFieldSchema, len(plugin.Meta.UsageSchema))
			for key, field := range plugin.Meta.UsageSchema {
				field.Enum = append([]string(nil), field.Enum...)
				field.Description = maps.Clone(field.Description)
				pricing.BillingUsageSchema[key] = field
			}
			if len(plugin.Meta.UsageExamples) > 0 {
				pricing.BillingUsageExamples = make([]jsplugin.UsageExample, len(plugin.Meta.UsageExamples))
				for index, example := range plugin.Meta.UsageExamples {
					facts := make(map[string]any, len(example.Facts))
					for key, value := range example.Facts {
						facts[key] = value
					}
					pricing.BillingUsageExamples[index] = jsplugin.UsageExample{
						Label: example.Label,
						Facts: facts,
					}
				}
			}
		}
		pricingMap = append(pricingMap, pricing)
	}

	// 防止大更新后数据不通用
	if len(pricingMap) > 0 {
		pricingMap[0].PricingVersion = "5a90f2b86c08bd983a9a2e6d66c255f4eaef9c4bc934386d2b6ae84ef0ff1f1f"
	}

	// 刷新缓存映射，供高并发快速查询
	modelEnableGroupsLock.Lock()
	modelEnableGroups = make(map[string][]string)
	modelQuotaTypeMap = make(map[string]int)
	for _, p := range pricingMap {
		modelEnableGroups[p.ModelName] = p.EnableGroup
		modelQuotaTypeMap[p.ModelName] = p.QuotaType
	}
	modelEnableGroupsLock.Unlock()

	lastGetPricingTime = time.Now()
}

// GetSupportedEndpointMap 返回全局端点到路径的映射
func GetSupportedEndpointMap() map[string]common.EndpointInfo {
	return supportedEndpointMap
}

// buildGroupPricingEntries 为一个模型算出「哪些分组配了定制价、各自的价格是多少」喵。
//
// 整体思路喵：
//  1. 逐个遍历该模型的启用分组；
//  2. 先看这个分组有没有任何定制痕迹（定价覆盖项 / 分组级计费方式 / 分组级表达式），
//     没有就跳过，让前端回落到顶层全局价；
//  3. 有定制就按该分组解析一份完整定价快照，连同阶梯表达式一起放进结果里喵。
//
// 边界与约定喵：
//   - 返回 nil 表示这个模型完全没有分组定制，JSON 里会被 omitempty 省掉，
//     老前端拿到的响应与改造前一模一样喵。
//   - 下发的是未乘分组倍率的基础价，倍率仍由前端另乘，口径与后端计费一致喵。
//
// 主人注意：这里对每个「模型 × 启用分组」都会解析一次定价，复杂度是 O(模型数 × 分组数)。
// 好在 updatePricing 有 1 分钟缓存且只在管理端改配置后重建，正常站点规模（几百模型 × 几十分组）
// 一次几万次内存查表可以接受；若将来模型或分组数量爆炸，建议改成只遍历
// GetGroupModelPricingCopy() 里真正配过的分组喵。
func buildGroupPricingEntries(modelName string, groups []string) map[string]GroupPricingEntry {
	// 喵~防御：模型名为空或没有启用分组时直接返回 nil，避免下发空对象污染响应喵。
	if modelName == "" || len(groups) == 0 {
		return nil
	}
	var entries map[string]GroupPricingEntry
	for _, group := range groups {
		// 喵~防御：跳过空分组名，它既查不到定制也不可能被请求命中喵。
		if group == "" {
			continue
		}
		_, hasPriceOverride := ratio_setting.GetGroupModelOverride(group, modelName)
		// 分组级计费方式与表达式只需要知道「有没有配」，用来判断这个分组值不值得单独下发喵。
		_, hasGroupMode := billing_setting.GetGroupBillingMode(group, modelName)
		_, hasGroupExpr := billing_setting.GetGroupBillingExpr(group, modelName)
		// 三种定制痕迹都没有就说明该分组完全继承全局，不需要单独下发喵。
		if !hasPriceOverride && !hasGroupMode && !hasGroupExpr {
			continue
		}

		entry := GroupPricingEntry{}
		// 先判这个分组最终到底走不走阶梯计费：分组自己声明的优先，没声明就沿用全局计费方式，
		// 判断口径必须和计费链路的 GetBillingModeForGroup 完全一致喵。
		if billing_setting.GetBillingModeForGroup(group, modelName) == billing_setting.BillingModeTieredExpr {
			if expr, hasExpr := billing_setting.GetBillingExprForGroup(group, modelName); hasExpr && strings.TrimSpace(expr) != "" {
				entry.BillingMode = billing_setting.BillingModeTieredExpr
				entry.BillingExpr = expr
			}
		}
		// 走阶梯计费时价格全由表达式描述，倍率字段留空；否则按倍率/按次定价解析喵。
		// 约定：entry.BillingMode 为空就代表「这个分组不是阶梯计费」，
		// 前端可以完全信任这条 entry，不需要再回落去看模型的全局计费方式喵。
		if entry.BillingMode == "" {
			// 口径与计费链路的 ResolveModelPricing 完全一致，保证展示价和实扣价同源喵。
			pricing := ratio_setting.ResolveModelPricing(group, modelName)
			if pricing.UsePrice {
				entry.QuotaType = 1
				entry.ModelPrice = pricing.ModelPrice
			} else {
				entry.QuotaType = 0
				entry.ModelRatio = pricing.ModelRatio
				entry.CompletionRatio = pricing.CompletionRatio
			}
			entry.CacheRatio = pricing.CacheRatio
			entry.CreateCacheRatio = pricing.CreateCacheRatio
			entry.ImageRatio = pricing.ImageRatio
			entry.AudioRatio = pricing.AudioRatio
			entry.AudioCompletionRatio = pricing.AudioCompletionRatio
		}
		// 惰性建表：绝大多数模型没有任何分组定制，能省掉一次 map 分配喵。
		if entries == nil {
			entries = make(map[string]GroupPricingEntry, len(groups))
		}
		entries[group] = entry
	}
	return entries
}
