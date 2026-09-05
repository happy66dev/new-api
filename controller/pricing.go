package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func filterPricingByUsableGroups(pricing []model.Pricing, usableGroup map[string]string) []model.Pricing {
	if len(pricing) == 0 {
		return pricing
	}
	if len(usableGroup) == 0 {
		return []model.Pricing{}
	}

	filtered := make([]model.Pricing, 0, len(pricing))
	for _, item := range pricing {
		if common.StringsContains(item.EnableGroup, "all") {
			filtered = append(filtered, item)
			continue
		}
		for _, group := range item.EnableGroup {
			if _, ok := usableGroup[group]; ok {
				filtered = append(filtered, item)
				break
			}
		}
	}
	return filtered
}

func GetPricing(c *gin.Context) {
	pricing := model.GetPricing()
	userId, exists := c.Get("id")
	usableGroup := map[string]string{}
	groupRatio := map[string]float64{}
	for s, f := range ratio_setting.GetGroupRatioCopy() {
		groupRatio[s] = f
	}
	var group string
	if exists {
		user, err := model.GetUserCache(userId.(int))
		if err == nil {
			group = user.Group
			for g := range groupRatio {
				ratio, ok := ratio_setting.GetGroupGroupRatio(group, g)
				if ok {
					groupRatio[g] = ratio
				}
			}
		}
	}

	userID := 0
	if exists {
		userID, _ = userId.(int)
	}
	usableGroup = service.GetUserUsableGroupsForUser(userID, group)
	visibleGroups := console_setting.GetModelSquareVisibleGroups()
	pricingGroups := make(map[string]string, len(usableGroup)+len(visibleGroups))
	for groupName, description := range usableGroup {
		pricingGroups[groupName] = description
	}
	for _, groupName := range visibleGroups {
		if _, exists := pricingGroups[groupName]; !exists {
			pricingGroups[groupName] = setting.GetUsableGroupDescription(groupName)
		}
	}
	pricing = filterPricingByUsableGroups(pricing, pricingGroups)
	// 追加共享中的用户上游模型条目，供模型广场在 user-shared 分组展示喵。
	pricing = appendSharedUpstreamPricing(pricing, userId)
	// 收集分组描述：以系统全局描述为底，再叠加当前用户可用分组的描述（上游引入 group 描述）喵。
	groupDescriptions := setting.GetGroupDescriptionsCopy()
	for groupName, description := range usableGroup {
		groupDescriptions[groupName] = description
	}
	// check groupRatio contains usableGroup
	for group := range ratio_setting.GetGroupRatioCopy() {
		if _, ok := pricingGroups[group]; !ok {
			delete(groupRatio, group)
		}
	}

	c.JSON(200, gin.H{
		"success":             true,
		"data":                pricing,
		"vendors":             model.GetVendors(),
		"group_ratio":         groupRatio,
		"usable_group":        usableGroup,
		"group_descriptions":  groupDescriptions,
		"model_square_groups": visibleGroups,
		"supported_endpoint":  model.GetSupportedEndpointMap(),
		"auto_groups":         service.GetUserAutoGroupForUser(userID, group),
		"pricing_version":     "a42d372ccf0b5dd13ecf71203521f9d2",
	})
}

// appendSharedUpstreamPricing 把共享中的用户上游模型追加为定价条目喵。
func appendSharedUpstreamPricing(pricing []model.Pricing, viewerUserID any) []model.Pricing {
	viewerID := -1
	if viewerUserID != nil {
		if id, ok := viewerUserID.(int); ok {
			viewerID = id
		}
	}
	// 按查看者 id 过滤白名单/黑名单，被挡用户看不到对应共享模型喵。
	sharedModels, err := model.GetSharedUserUpstreamModels(viewerID)
	// 喵~防御：共享模型查询失败不影响普通定价返回喵。
	if err != nil {
		return pricing
	}
	for _, sharedModel := range sharedModels {
		pricing = append(pricing, buildSharedUpstreamPricing(sharedModel, viewerID))
	}
	return pricing
}

// buildSharedUpstreamPricing 把共享模型视图构造为模型广场可展示的定价条目喵。
// 用户配置的 ModelRatio/CompletionRatio/CacheRatio 语义是「RMB 元/百万 token」，
// 而模型广场按「model_ratio×2=美元/百万 token」展示并乘回汇率，故此处统一换算：
// model_ratio=输入价/(汇率×2)、completion_ratio=输出价/输入价、cache_ratio=缓存价/输入价，
// 使前端最终显示回用户设置的 RMB 原价喵。仅影响展示，共享调用实际计费仍走 RMB 原配置喵。
// 所有者查看自己的模型时，受展示开关控制附加余额与上限字段喵。
func buildSharedUpstreamPricing(view model.SharedUserUpstreamModelView, viewerUserID int) model.Pricing {
	inputPrice, _ := strconv.ParseFloat(view.ModelRatio, 64)
	outputPrice, _ := strconv.ParseFloat(view.CompletionRatio, 64)
	cachePrice, _ := strconv.ParseFloat(view.CacheRatio, 64)
	// 汇率取操作设置（RMB/美元），非正时按 1 兜底，避免除零与负价喵。
	usdRate := operation_setting.USDExchangeRate
	if usdRate <= 0 {
		usdRate = 1
	}
	modelRatio := 0.0
	completionRatio := 0.0
	var cacheRatio *float64
	// 输入价非正时全部倍率归零，避免构造负价或除以零喵。
	if inputPrice > 0 {
		// 换算模型广场倍率：显示价=model_ratio×2×汇率=输入 RMB 价喵。
		modelRatio = inputPrice / (usdRate * 2)
		// 输出/缓存价换算为相对输入价的倍率，前端按 model_ratio×倍率×2 展示 RMB 原值喵。
		if outputPrice > 0 {
			completionRatio = outputPrice / inputPrice
		}
		if cachePrice > 0 {
			convertedCacheRatio := cachePrice / inputPrice
			cacheRatio = &convertedCacheRatio
		}
	}
	item := model.Pricing{
		ModelName:   "user/" + view.NormalizedName,
		Description: view.Description,
		// 图标键名透传给模型广场卡片，前端用 getLobeIcon 渲染；为空时回退首字母占位喵。
		Icon:             view.Icon,
		QuotaType:        0,
		ModelRatio:       modelRatio,
		CompletionRatio:  completionRatio,
		CacheRatio:       cacheRatio,
		OwnerBy:          "user-shared",
		EnableGroup:      []string{constant.GroupUserShared},
		ShareOwnerUserID: view.OwnerUserID,
	}
	// 共享额度是递减账户，剩余额度即当前共享额度本身；耗尽后模型不再出现在共享列表喵。
	if view.ShareLimitCents > 0 {
		shareLimit := view.ShareLimitCents
		item.ShareLimitCents = &shareLimit
		remaining := view.ShareLimitCents
		// 喵~防御：剩余额度钳制非负，避免负值展示喵。
		if remaining < 0 {
			remaining = 0
		}
		item.ShareRemainingCents = &remaining
	} else {
		// 共享额度未设置时不展示剩余额度喵。
		item.ShareRemainingCents = nil
	}
	// 所有者且开启展示开关时附加余额与可用额度字段喵。
	if viewerUserID == view.OwnerUserID && view.ShowBalanceEnabled {
		balance := view.BalanceCents
		available := view.AvailableCents
		item.BalanceCents = &balance
		item.AvailableCents = &available
	}
	return item
}

func ResetModelRatio(c *gin.Context) {
	defaultStr := ratio_setting.DefaultModelRatio2JSONString()
	err := model.UpdateOption("ModelRatio", defaultStr)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	err = ratio_setting.UpdateModelRatioByJSONString(defaultStr)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(200, gin.H{
		"success": true,
		"message": "重置模型倍率成功",
	})
}
