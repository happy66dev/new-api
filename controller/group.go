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

func GetUserGroups(c *gin.Context) {
	usableGroups := make(map[string]map[string]interface{})
	userGroup := ""
	userId := c.GetInt("id")
	userGroup, _ = model.GetUserGroup(userId, false)
	userUsableGroups := service.GetUserUsableGroupsForUser(userId, userGroup)
	for groupName, _ := range ratio_setting.GetGroupRatioCopy() {
		// UserUsableGroups contains the groups that the user can use
		if desc, ok := userUsableGroups[groupName]; ok {
			usableGroups[groupName] = map[string]interface{}{
				"ratio":         service.GetUserGroupRatio(userGroup, groupName),
				"desc":          desc,
				"default_model": ratio_setting.GetGroupDefaultModel(groupName),
			}
		}
	}
	if len(service.GetUserAutoGroupForUser(userId, userGroup)) > 0 {
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
	// 用户共享分组是硬编码虚拟分组，不在站点分组配置中，手动追加到可用分组喵。
	// 这样游乐场、API Key 分组选择与虚拟模型内部候选链都能选择"用户共享"分组喵。
	if desc, ok := userUsableGroups[constant.GroupUserShared]; ok {
		usableGroups[constant.GroupUserShared] = map[string]interface{}{
			"ratio":         service.GetUserGroupRatio(userGroup, constant.GroupUserShared),
			"desc":          desc,
			"default_model": ratio_setting.GetGroupDefaultModel(constant.GroupUserShared),
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    usableGroups,
	})
}
