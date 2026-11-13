package controller

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func GetGroups(c *gin.Context) {
	groupNames := make([]string, 0)
	for groupName := range ratio_setting.GetGroupRatioCopy() {
		groupNames = append(groupNames, groupName)
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    groupNames,
	})
}

// loadRequestGroupAccess 读取当前登录用户的分组访问权限，读取失败时退回旧口径喵。
func loadRequestGroupAccess(c *gin.Context) service.UserGroupAccess {
	// 复用 service 层缓存：令牌鉴权路径已经解析过的结果直接命中，不再重复回查用户缓存喵。
	return service.GetRequestUserGroupAccess(c)
}

// GetUserGroups 返回当前用户可用的分组列表，是游乐场/令牌/日志筛选的分组来源喵。
func GetUserGroups(c *gin.Context) {
	usableGroups := make(map[string]map[string]interface{})
	userId := c.GetInt("id")
	userGroup, _ := model.GetUserGroup(userId, false)
	groupAccess := loadRequestGroupAccess(c)
	userUsableGroups := groupAccess.UsableGroups
	for groupName := range ratio_setting.GetGroupRatioCopy() {
		// UserUsableGroups contains the groups that the user can use
		if desc, ok := userUsableGroups[groupName]; ok {
			usableGroups[groupName] = map[string]interface{}{
				"ratio":         service.GetUserGroupRatio(userGroup, groupName),
				"desc":          desc,
				"default_model": ratio_setting.GetGroupDefaultModel(groupName),
			}
		}
	}
	// 自动分组既然是旧口径的产物，custom 模式下不再出现，避免用户选到被旧规则驱动的东西喵。
	if !groupAccess.HasCustomMode() && len(service.GetUserAutoGroupForUser(userId, userGroup, service.GroupAccessModeLegacy, "")) > 0 {
		autoDescription := strings.TrimSpace(setting.AutoGroupDescription)
		if autoDescription == "" {
			autoDescription = setting.GetUsableGroupDescription("auto")
		}
		usableGroups["auto"] = map[string]interface{}{
			"ratio":         "自动",
			"desc":          autoDescription,
			"default_model": ratio_setting.GetGroupDefaultModel("auto"),
		}
	}
	// 用户共享分组是硬编码虚拟分组，不在站点分组配置中，按放行位手动追加到可用分组喵。
	// legacy 模式下放行位为空（AllowsBuiltinGroup 恒 true），保持与旧版本一致的展示喵。
	if groupAccess.AllowsBuiltinGroup(constant.GroupUserShared) {
		if desc, ok := legacyUserSharedGroupDescription(); ok {
			usableGroups[constant.GroupUserShared] = map[string]interface{}{
				"ratio":         service.GetUserGroupRatio(userGroup, constant.GroupUserShared),
				"desc":          desc,
				"default_model": ratio_setting.GetGroupDefaultModel(constant.GroupUserShared),
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    usableGroups,
		// 内置伪分组放行位透传给前端，游乐场据此决定是否追加"虚拟模型/自定上游"分类喵。
		"builtin_groups":           groupAccess.BuiltinGroups,
		"group_access_custom_mode": groupAccess.HasCustomMode(),
	})
}

// legacyUserSharedGroupDescription 读取用户共享分组的展示描述喵。
// 共享总开关关闭时不再展示该分组，避免用户看到一个永远没有模型的分组喵。
func legacyUserSharedGroupDescription() (string, bool) {
	if !model.UserUpstreamSharingFeatureEnabled() {
		return "", false
	}
	return "用户共享", true
}
