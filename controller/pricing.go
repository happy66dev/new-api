package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/console_setting"
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
		"model_square_groups": visibleGroups,
		"supported_endpoint":  model.GetSupportedEndpointMap(),
		"auto_groups":         service.GetUserAutoGroupForUser(userID, group),
		"pricing_version":     "a42d372ccf0b5dd13ecf71203521f9d2",
	})
}

// appendSharedUpstreamPricing 把共享中的用户上游模型追加为定价条目喵。
func appendSharedUpstreamPricing(pricing []model.Pricing, viewerUserID any) []model.Pricing {
	sharedModels, err := model.GetSharedUserUpstreamModels()
	// 喵~防御：共享模型查询失败不影响普通定价返回喵。
	if err != nil {
		return pricing
	}
	viewerID := -1
	if viewerUserID != nil {
		if id, ok := viewerUserID.(int); ok {
			viewerID = id
		}
	}
	for _, sharedModel := range sharedModels {
		pricing = append(pricing, buildSharedUpstreamPricing(sharedModel, viewerID))
	}
	return pricing
}

// buildSharedUpstreamPricing 把共享模型视图构造为模型广场可展示的定价条目喵。
// 所有者查看自己的模型时，受展示开关控制附加余额与上限字段喵。
func buildSharedUpstreamPricing(view model.SharedUserUpstreamModelView, viewerUserID int) model.Pricing {
	modelRatio, _ := strconv.ParseFloat(view.ModelRatio, 64)
	completionRatio, _ := strconv.ParseFloat(view.CompletionRatio, 64)
	item := model.Pricing{
		ModelName:        "user/" + view.NormalizedName,
		Description:      view.Description,
		QuotaType:        0,
		ModelRatio:       modelRatio,
		CompletionRatio:  completionRatio,
		OwnerBy:          "user-shared",
		EnableGroup:      []string{constant.GroupUserShared},
		ShareOwnerUserID: view.OwnerUserID,
	}
	// 有共享上限时展示共享剩余额度；无上限时展示累计消耗作为参考喵。
	if view.ShareLimitCents > 0 {
		shareLimit := view.ShareLimitCents
		remaining := view.ShareLimitCents - view.ShareSpentCents
		// 喵~防御：剩余额度钳制非负，避免负值展示喵。
		if remaining < 0 {
			remaining = 0
		}
		item.ShareLimitCents = &shareLimit
		item.ShareRemainingCents = &remaining
	} else {
		spent := view.ShareSpentCents
		item.ShareRemainingCents = &spent
	}
	// 所有者且开启展示开关时附加余额与上限字段喵。
	if viewerUserID == view.OwnerUserID && view.ShowBalanceEnabled {
		balance := view.BalanceCents
		spendLimit := view.SpendLimitCents
		upstreamRemaining := view.UpstreamRemainingCents
		item.BalanceCents = &balance
		item.SpendLimitCents = &spendLimit
		item.UpstreamRemaining = &upstreamRemaining
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
