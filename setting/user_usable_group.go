package setting

import (
	"sync"

	"github.com/QuantumNous/new-api/common"
)

var userUsableGroups = map[string]string{
	"default": "默认分组",
	"vip":     "vip分组",
}
var groupDescriptions = map[string]string{}
var userUsableGroupsMutex sync.RWMutex

func GetUserUsableGroupsCopy() map[string]string {
	userUsableGroupsMutex.RLock()
	defer userUsableGroupsMutex.RUnlock()

	copyUserUsableGroups := make(map[string]string)
	for k, v := range userUsableGroups {
		copyUserUsableGroups[k] = v
	}
	return copyUserUsableGroups
}

func GetGroupDescriptionsCopy() map[string]string {
	userUsableGroupsMutex.RLock()
	defer userUsableGroupsMutex.RUnlock()
	descriptions := make(map[string]string, len(groupDescriptions)+len(userUsableGroups))
	for k, v := range userUsableGroups {
		descriptions[k] = v
	}
	for k, v := range groupDescriptions {
		descriptions[k] = v
	}
	return descriptions
}

func UserUsableGroups2JSONString() string {
	userUsableGroupsMutex.RLock()
	defer userUsableGroupsMutex.RUnlock()

	jsonBytes, err := common.Marshal(userUsableGroups)
	if err != nil {
		common.SysLog("error marshalling user groups: " + err.Error())
	}
	return string(jsonBytes)
}

func GroupDescriptions2JSONString() string {
	userUsableGroupsMutex.RLock()
	defer userUsableGroupsMutex.RUnlock()
	jsonBytes, err := common.Marshal(groupDescriptions)
	if err != nil {
		common.SysLog("error marshalling group descriptions: " + err.Error())
	}
	return string(jsonBytes)
}

func UpdateUserUsableGroupsByJSONString(jsonStr string) error {
	userUsableGroupsMutex.Lock()
	defer userUsableGroupsMutex.Unlock()

	var values map[string]string
	if err := common.UnmarshalJsonStr(jsonStr, &values); err != nil {
		return err
	}
	userUsableGroups = values
	return nil
}

func UpdateGroupDescriptionsByJSONString(jsonStr string) error {
	var values map[string]string
	if err := common.UnmarshalJsonStr(jsonStr, &values); err != nil {
		return err
	}
	userUsableGroupsMutex.Lock()
	defer userUsableGroupsMutex.Unlock()
	groupDescriptions = values
	return nil
}

func GetUsableGroupDescription(groupName string) string {
	userUsableGroupsMutex.RLock()
	defer userUsableGroupsMutex.RUnlock()

	if desc, ok := groupDescriptions[groupName]; ok && desc != "" {
		return desc
	}
	if desc, ok := userUsableGroups[groupName]; ok {
		return desc
	}
	return groupName
}
