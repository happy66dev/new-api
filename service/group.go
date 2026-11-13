package service

import (
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

// GroupAccessModeLegacy 表示沿用原 newapi 的四套旧口径（全局可用分组 + 特殊可用分组 + 分组访问表达式 + 用户所属分组）喵。
const GroupAccessModeLegacy = "legacy"

// GroupAccessModeCustom 表示只认管理员在用户页配置的可用分组白名单，旧四套口径一律不再参与喵。
const GroupAccessModeCustom = "custom"

// GroupAccessModeAuto 是"跟随全局设置"的占位值：全局分组管理模式为 custom 时按 custom 处理，否则按 legacy 处理喵。
const GroupAccessModeAuto = ""

// normalizeGroupAccessMode 把任意输入归一到三个合法值，非法值一律退回 legacy，避免脏数据放大权限喵。
func normalizeGroupAccessMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case GroupAccessModeCustom:
		return GroupAccessModeCustom
	case GroupAccessModeLegacy:
		return GroupAccessModeLegacy
	default:
		return GroupAccessModeAuto
	}
}

// IsCustomGroupAccessMode 判断给定模式是否启用"仅新分组管理"喵。
// 空模式表示跟随全局 console_setting.group_access_mode，未配置时仍按旧逻辑处理，保证升级后行为不变喵。
func IsCustomGroupAccessMode(mode string) bool {
	switch normalizeGroupAccessMode(mode) {
	case GroupAccessModeCustom:
		return true
	case GroupAccessModeLegacy:
		return false
	default:
		return console_setting.IsCustomGroupAccessModeGlobal()
	}
}

// parseUsableGroupWhitelist 解析用户白名单 JSON 数组，去重并剔除空白项喵。
// 喵~防御：JSON 非法或不是字符串数组时返回空列表（保守方向：用户只会失去分组，不会凭空获得权限）喵。
func parseUsableGroupWhitelist(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var parsed []string
	if err := common.UnmarshalJsonStr(trimmed, &parsed); err != nil {
		common.SysLog("failed to parse user usable group whitelist: " + err.Error())
		return nil
	}
	// 主人注意：白名单最多 64 项，与 registerUsableGroupWhitelistLimit 对齐，避免超长 JSON 拖慢每次鉴权喵。
	seen := make(map[string]struct{}, min(len(parsed), registerUsableGroupWhitelistLimit))
	whitelist := make([]string, 0, min(len(parsed), registerUsableGroupWhitelistLimit))
	for _, group := range parsed {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		if _, duplicate := seen[group]; duplicate {
			continue
		}
		seen[group] = struct{}{}
		whitelist = append(whitelist, group)
		if len(whitelist) == registerUsableGroupWhitelistLimit {
			break
		}
	}
	return whitelist
}

// registerUsableGroupWhitelistLimit 单个用户可用分组白名单的最大条目数，单位：个喵。
const registerUsableGroupWhitelistLimit = 64

// UserGroupAccess 描述一次请求中调用者可以访问的分组集合喵。
type UserGroupAccess struct {
	UsableGroups map[string]string
	AutoGroups   []string
	// BuiltinGroups 记录内置伪分组（虚拟模型 / 自定上游 / 用户共享）是否放行，键为伪分组名喵。
	// 只有 custom 模式会填充它；legacy 模式保持 nil，行为与旧版本完全一致喵。
	BuiltinGroups map[string]bool
	// customMode 标记本次解析是否走了"仅新分组管理"，供展示层决定是否隐藏内部候选分组喵。
	customMode bool
}

// Allows 判断调用者能否使用指定分组，真实分组与内置伪分组分别判定喵。
func (access UserGroupAccess) Allows(group string) bool {
	if group == "auto" {
		return len(access.AutoGroups) > 0
	}
	if _, ok := access.UsableGroups[group]; ok {
		return true
	}
	// custom 模式下三个内置伪分组不进 UsableGroups（它们不在倍率配置里），单独按放行位判定喵。
	return access.BuiltinGroups[group]
}

// HasCustomMode 报告本次解析是否属于"仅新分组管理"，供只在该模式下生效的展示逻辑使用喵。
func (access UserGroupAccess) HasCustomMode() bool {
	return access.customMode
}

// WithBuiltinGroupFeatureGates 按各功能的系统总开关收敛内置伪分组放行位喵。
// 功能总开关关闭时对应伪分组必须视为不可用，否则用户会看到一个永远报错的分类喵。
func (access UserGroupAccess) WithBuiltinGroupFeatureGates() UserGroupAccess {
	// legacy 模式没有内置放行位，原样返回，行为与旧版本一致喵。
	if access.BuiltinGroups == nil {
		return access
	}
	if !model.VirtualModelFunctionEnabled() {
		access.BuiltinGroups[constant.GroupVirtual] = false
	}
	// 自定上游与用户共享都受用户上游总开关约束，与 middleware/upstream_model.go 的拒绝口径一致喵。
	if !model.UserUpstreamFeatureEnabled() {
		access.BuiltinGroups[constant.GroupUserUpstream] = false
		access.BuiltinGroups[constant.GroupUserShared] = false
	}
	// 共享专用开关关闭时，共享分组不再可用，即使管理员勾选也不放行喵。
	if !model.UserUpstreamSharingFeatureEnabled() {
		access.BuiltinGroups[constant.GroupUserShared] = false
	}
	return access
}

// AllowsBuiltinGroup 判断内置伪分组是否对调用者放行（已叠加功能总开关）喵。
// legacy 模式下内置伪分组沿用旧逻辑，全部返回 true，由各自的原有分支兜底喵。
func (access UserGroupAccess) AllowsBuiltinGroup(group string) bool {
	if access.BuiltinGroups == nil {
		return true
	}
	return access.BuiltinGroups[group]
}

func GetUserUsableGroups(userGroup string) map[string]string {
	groupsCopy := setting.GetUserUsableGroupsCopy()
	if userGroup != "" {
		specialSettings, b := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.Get(userGroup)
		if b {
			// 处理特殊可用分组
			for specialGroup, desc := range specialSettings {
				if after, ok := strings.CutPrefix(specialGroup, "-:"); ok {
					// 移除分组
					groupToRemove := after
					delete(groupsCopy, groupToRemove)
				} else if after, ok := strings.CutPrefix(specialGroup, "+:"); ok {
					// 添加分组
					groupToAdd := after
					groupsCopy[groupToAdd] = desc
				} else {
					// 直接添加分组
					groupsCopy[specialGroup] = desc
				}
			}
		}
		// 如果userGroup不在UserUsableGroups中，返回UserUsableGroups + userGroup
		if _, ok := groupsCopy[userGroup]; !ok {
			groupsCopy[userGroup] = "用户分组"
		}
	}
	// 用户共享分组硬编码追加到所有用户可用分组，共享模型归入该分组喵。
	// 共享开关(UserUpstreamSharingEnabled)关闭后不再追加，用户共享分组从所有分组列表消失喵。
	if model.UserUpstreamSharingFeatureEnabled() {
		groupsCopy[constant.GroupUserShared] = "用户共享"
	}
	return groupsCopy
}

// GetUserUsableGroupsForUser applies the optional per-group access expressions
// after the existing user-group and special-group rules have been resolved.
func GetUserUsableGroupsForUser(userID int, userGroup string) map[string]string {
	return resolveUserUsableGroups(userID, userGroup, GroupAccessModeAuto, "")
}

// ResolveUserGroupAccessByUserID 按用户编号读取其分组权限模式与白名单后再解析访问权限喵。
// 供没有 gin 上下文的离线场景（虚拟模型异常扫描、分享码导入裁定）使用，避免各处漏掉 custom 模式喵。
func ResolveUserGroupAccessByUserID(userID int, userGroup string) UserGroupAccess {
	userCache, err := model.GetUserCache(userID)
	// 喵~防御：读不到用户时退回旧口径（跟随全局 + 空白名单），保证历史行为不被缓存故障改变喵。
	if err != nil {
		common.SysLog("failed to load user cache for group access: " + err.Error())
		userCache = nil
	}
	access := ResolveUserGroupAccessForUser(userID, userGroup, GetUserGroupAccessMode(userCache), GetUserUsableGroupWhitelist(userCache))
	return access.WithBuiltinGroupFeatureGates()
}

// resolveUserUsableGroups 统一解析用户可用分组，是旧口径与新分组管理的唯一分派点喵。
// groupAccessMode 为空表示跟随全局设置；usableGroupsRaw 为该用户的白名单 JSON，仅 custom 模式使用喵。
func resolveUserUsableGroups(userID int, userGroup string, groupAccessMode string, usableGroupsRaw string) map[string]string {
	// custom 模式完全取代旧四套口径，只按白名单放行真实分组喵。
	if IsCustomGroupAccessMode(groupAccessMode) {
		return buildCustomModeUsableGroups(usableGroupsRaw)
	}
	groups := GetUserUsableGroups(userGroup)
	rules := GetGroupAccessRulesByGroup()
	if len(rules) == 0 {
		return groups
	}
	accessUser, err := loadGroupAccessUser(userID)
	if err != nil {
		for group := range rules {
			delete(groups, group)
		}
		return groups
	}
	for group, rule := range rules {
		if !accessUser.evaluateRule(rule) {
			delete(groups, group)
		}
	}
	return groups
}

// buildCustomModeUsableGroups 把白名单转换为可用分组表喵。
// 白名单为空时只保留内置伪分组（虚拟模型 + 自定上游），不含用户共享分组——
// 这与"虚拟模型分组默认开启、共享需显式授权"的产品语义一致喵。
func buildCustomModeUsableGroups(usableGroupsRaw string) map[string]string {
	whitelist := parseUsableGroupWhitelist(usableGroupsRaw)
	validRatioGroups := ratio_setting.GetGroupRatioCopy()
	groups := make(map[string]string, len(whitelist))
	for _, group := range whitelist {
		// 内置伪分组不在倍率配置里，交给 BuiltinGroups 单独判定，这里跳过避免重复喵。
		if isBuiltinGroupName(group) {
			continue
		}
		// 喵~防御：已被删除的分组不再放行，防止白名单里的历史残留重新授予权限喵。
		if _, exists := validRatioGroups[group]; !exists {
			continue
		}
		groups[group] = setting.GetUsableGroupDescription(group)
	}
	return groups
}

// builtinGroupNames 三个内置伪分组名，前端"可用分组"编辑器与后端校验共用同一份清单喵。
var builtinGroupNames = []string{constant.GroupVirtual, constant.GroupUserUpstream, constant.GroupUserShared}

// isBuiltinGroupName 判断分组名是否为内置伪分组喵。
func isBuiltinGroupName(group string) bool {
	return slices.Contains(builtinGroupNames, group)
}

// GetBuiltinGroupNamesCopy 返回内置伪分组名清单的副本，供控制器做入参校验喵。
func GetBuiltinGroupNamesCopy() []string {
	return slices.Clone(builtinGroupNames)
}

// resolveBuiltinGroupPermissions 解析三个内置伪分组的放行位喵。
// legacy 模式返回 nil（行为与旧版本一致，由原有的硬编码逻辑各自处理）喵。
func resolveBuiltinGroupPermissions(groupAccessMode string, usableGroupsRaw string) map[string]bool {
	if !IsCustomGroupAccessMode(groupAccessMode) {
		return nil
	}
	whitelist := parseUsableGroupWhitelist(usableGroupsRaw)
	// 虚拟模型与自定上游默认放行，对应"虚拟模型(分组)默认开启"的产品要求喵。
	// 主人注意：这两个放行位还要叠加各自的功能总开关，总开关关闭时不暴露分组，见 WithBuiltinGroupFeatureGates 喵。
	permissions := map[string]bool{
		constant.GroupVirtual:      true,
		constant.GroupUserUpstream: true,
		// 用户共享分组默认关闭，必须管理员显式勾选才放行喵。
		constant.GroupUserShared: false,
	}
	for _, group := range whitelist {
		// 只有内置伪分组需要落到放行位，真实分组已进 UsableGroups 表喵。
		if _, isBuiltin := permissions[group]; isBuiltin {
			permissions[group] = true
		}
	}
	return permissions
}

func GetGroupAccessRulesByGroup() map[string]console_setting.GroupAccessRule {
	rules := make(map[string]console_setting.GroupAccessRule)
	for _, rule := range console_setting.GetGroupAccessRules() {
		if rule.Group != "" {
			rules[rule.Group] = rule
		}
	}
	return rules
}

func GroupInUserUsableGroups(userGroup, groupName string) bool {
	_, ok := GetUserUsableGroups(userGroup)[groupName]
	return ok
}

func ResolveUserGroupAccess(userGroup string) UserGroupAccess {
	usableGroups := GetUserUsableGroups(userGroup)
	autoGroups := filterAutoGroups(setting.GetAutoGroups(), usableGroups)
	return UserGroupAccess{
		UsableGroups: usableGroups,
		AutoGroups:   autoGroups,
	}
}

// ResolveUserGroupAccessForUser 解析指定用户的分组访问权限，是鉴权与展示的统一入口喵。
// 额外接收该用户的模式与白名单，使 custom 模式下的判定完全以管理员配置为准喵。
func ResolveUserGroupAccessForUser(userID int, userGroup string, groupAccessMode string, usableGroupsRaw string) UserGroupAccess {
	usableGroups := resolveUserUsableGroups(userID, userGroup, groupAccessMode, usableGroupsRaw)
	autoGroups := filterAutoGroups(setting.GetAutoGroups(), usableGroups)
	return UserGroupAccess{
		UsableGroups:  usableGroups,
		AutoGroups:    autoGroups,
		BuiltinGroups: resolveBuiltinGroupPermissions(groupAccessMode, usableGroupsRaw),
		customMode:    IsCustomGroupAccessMode(groupAccessMode),
	}
}

func filterAutoGroups(configuredGroups []string, usableGroups map[string]string) []string {
	autoGroups := make([]string, 0, len(configuredGroups))
	seen := make(map[string]struct{}, len(configuredGroups))
	for _, group := range configuredGroups {
		if _, duplicate := seen[group]; duplicate {
			continue
		}
		if _, ok := usableGroups[group]; !ok {
			continue
		}
		seen[group] = struct{}{}
		autoGroups = append(autoGroups, group)
	}
	return autoGroups
}

func IsUserSelectableGroup(userGroup, groupName string) bool {
	if groupName == "" || groupName == "auto" {
		return false
	}
	return GroupInUserUsableGroups(userGroup, groupName) && ratio_setting.ContainsGroupRatio(groupName)
}

// IsUserSelectableGroupForUser 判断指定用户能否在令牌/游乐场里选择该分组喵。
// custom 模式下只认白名单解析结果，内置伪分组（虚拟模型/自定上游/用户共享）单独判定喵。
func IsUserSelectableGroupForUser(userID int, userGroup, groupName string, groupAccessMode string, usableGroupsRaw string) bool {
	if groupName == "" || groupName == "auto" {
		return false
	}
	// custom 模式下三个内置伪分组不在倍率配置里，按放行位判定，不要求倍率存在喵。
	if IsCustomGroupAccessMode(groupAccessMode) && isBuiltinGroupName(groupName) {
		return resolveBuiltinGroupPermissions(groupAccessMode, usableGroupsRaw)[groupName]
	}
	_, ok := resolveUserUsableGroups(userID, userGroup, groupAccessMode, usableGroupsRaw)[groupName]
	return ok && ratio_setting.ContainsGroupRatio(groupName)
}

// GetUserAutoGroup 根据用户分组获取自动分组设置
func GetUserAutoGroup(userGroup string) []string {
	autoGroups := make([]string, 0)
	seen := make(map[string]struct{})
	for _, group := range setting.GetAutoGroups() {
		if !IsUserSelectableGroup(userGroup, group) {
			continue
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		autoGroups = append(autoGroups, group)
	}
	return autoGroups
}

// GetUserAutoGroupForUser 解析指定用户可用的自动分组序列，custom 模式下只保留白名单内的真实分组喵。
func GetUserAutoGroupForUser(userID int, userGroup string, groupAccessMode string, usableGroupsRaw string) []string {
	usableGroups := resolveUserUsableGroups(userID, userGroup, groupAccessMode, usableGroupsRaw)
	autoGroups := make([]string, 0)
	seen := make(map[string]struct{})
	for _, group := range setting.GetAutoGroups() {
		if _, ok := usableGroups[group]; !ok || !ratio_setting.ContainsGroupRatio(group) {
			continue
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		autoGroups = append(autoGroups, group)
	}
	return autoGroups
}

// GetUserGroupAccessMode 从用户缓存实体读取分组权限模式，缺失时返回跟随全局的空值喵。
func GetUserGroupAccessMode(userBase *model.UserBase) string {
	// 喵~防御：用户缓存缺失时按跟随全局处理，避免空指针喵。
	if userBase == nil {
		return GroupAccessModeAuto
	}
	return normalizeGroupAccessMode(userBase.GroupAccessMode)
}

// GetUserUsableGroupWhitelist 从用户缓存实体读取可用分组白名单原始 JSON 喵。
func GetUserUsableGroupWhitelist(userBase *model.UserBase) string {
	// 喵~防御：用户缓存缺失时返回空白名单，custom 模式下只会落到"仅内置伪分组"的保守结果喵。
	if userBase == nil {
		return ""
	}
	return userBase.UsableGroups
}

// GetRequestUserGroupAccess 读取当前请求的分组访问权限，优先复用上下文中已解析好的结果喵。
func GetRequestUserGroupAccess(c *gin.Context) UserGroupAccess {
	if cached, ok := common.GetContextKey(c, constant.ContextKeyUserGroupAccess); ok {
		if access, valid := cached.(UserGroupAccess); valid {
			return access
		}
	}
	userCache, err := model.GetUserCache(c.GetInt("id"))
	// 喵~防御：用户缓存读取失败时退回旧口径（空模式 + 空白名单），保证历史行为不因缓存抖动而改变喵。
	if err != nil {
		common.SysLog("failed to load user cache for group access: " + err.Error())
		userCache = nil
	}
	access := ResolveUserGroupAccessForUser(
		c.GetInt("id"),
		common.GetContextKeyString(c, constant.ContextKeyUserGroup),
		GetUserGroupAccessMode(userCache),
		GetUserUsableGroupWhitelist(userCache),
	)
	// 内置伪分组的放行位叠加功能总开关，后续所有 Allows/BuiltinGroups 读取都是最终结论喵。
	access = access.WithBuiltinGroupFeatureGates()
	common.SetContextKey(c, constant.ContextKeyUserGroupAccess, access)
	return access
}

// FilterUserTokenAutoGroups applies current permissions before the
// per-token limit. It intentionally does not fall back to the global Auto list.
func FilterUserTokenAutoGroups(userGroup string, groups []string) []string {
	return FilterUserTokenAutoGroupsForUser(0, userGroup, groups, GroupAccessModeAuto, "")
}

// FilterUserTokenAutoGroupsForUser 过滤用户为令牌配置的自动分组序列，custom 模式只保留白名单内分组喵。
func FilterUserTokenAutoGroupsForUser(userID int, userGroup string, groups []string, groupAccessMode string, usableGroupsRaw string) []string {
	maxCount := setting.GetMaxTokenAutoGroups()
	filtered := make([]string, 0, min(len(groups), maxCount))
	seen := make(map[string]struct{})
	for _, group := range groups {
		if !IsUserSelectableGroupForUser(userID, userGroup, group, groupAccessMode, usableGroupsRaw) {
			continue
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		filtered = append(filtered, group)
		if len(filtered) == maxCount {
			break
		}
	}
	return filtered
}

// GetRequestAutoGroups resolves the ordered Auto groups for the current token.
// The absence of the context value means that the token inherits the complete
// global Auto list; a present (even empty) value is an explicit token snapshot.
func GetRequestAutoGroups(c *gin.Context, userGroup string) []string {
	value, ok := common.GetContextKey(c, constant.ContextKeyTokenAutoGroups)
	userID := c.GetInt("id")
	// 分组权重的两种来源（模式与白名单）在这里只解析一次，避免循环内重复回查用户缓存喵。
	groupAccessMode := GroupAccessModeAuto
	usableGroupsRaw := ""
	if userCache, err := model.GetUserCache(userID); err == nil {
		groupAccessMode = GetUserGroupAccessMode(userCache)
		usableGroupsRaw = GetUserUsableGroupWhitelist(userCache)
	}
	if !ok {
		return GetUserAutoGroupForUser(userID, userGroup, groupAccessMode, usableGroupsRaw)
	}
	groups, ok := value.([]string)
	if !ok {
		return []string{}
	}
	return FilterUserTokenAutoGroupsForUser(userID, userGroup, groups, groupAccessMode, usableGroupsRaw)
}

// GetGroupsEnabledModels 按 groups 顺序获取各分组启用的模型并去重喵。
// viewerID 用于用户共享分组：白名单/黑名单过滤对查看者不可见的模型喵。
func GetGroupsEnabledModels(groups []string, viewerID int) []string {
	seen := make(map[string]struct{})
	models := make([]string, 0)
	for _, group := range groups {
		// 用户共享分组直接查询共享中的用户上游模型，不走 abilities 表喵。
		if group == constant.GroupUserShared {
			for _, modelName := range model.GetSharedUserUpstreamModelNames(viewerID) {
				if _, ok := seen[modelName]; !ok {
					seen[modelName] = struct{}{}
					models = append(models, modelName)
				}
			}
			continue
		}
		for _, modelName := range model.GetGroupEnabledModels(group) {
			if _, ok := seen[modelName]; !ok {
				seen[modelName] = struct{}{}
				models = append(models, modelName)
			}
		}
	}
	return models
}

// GetUserGroupRatio 获取用户使用某个分组的倍率
// userGroup 用户分组
// group 需要获取倍率的分组
func GetUserGroupRatio(userGroup, group string) float64 {
	ratio, ok := ratio_setting.GetGroupGroupRatio(userGroup, group)
	if ok {
		return ratio
	}
	return ratio_setting.GetGroupRatio(group)
}
