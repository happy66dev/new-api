package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserGroupAccessAllowsAutoWhenCandidatesRemain(t *testing.T) {
	access := UserGroupAccess{
		UsableGroups: map[string]string{"default": "Default"},
		AutoGroups:   []string{"default"},
	}

	assert.True(t, access.Allows("auto"))
	assert.True(t, access.Allows("default"))
	assert.False(t, access.Allows("vip"))
}

func TestUserGroupAccessRejectsAutoWithoutUsableCandidates(t *testing.T) {
	access := UserGroupAccess{
		UsableGroups: map[string]string{"default": "Default"},
	}

	assert.False(t, access.Allows("auto"))
}

func TestFilterAutoGroupsPreservesOrderAndRemovesUnavailableDuplicates(t *testing.T) {
	usableGroups := map[string]string{
		"default": "Default",
		"vip":     "VIP",
	}

	groups := filterAutoGroups(
		[]string{"vip", "private", "default", "vip"},
		usableGroups,
	)

	assert.Equal(t, []string{"vip", "default"}, groups)
}

func TestGetRequestUserGroupAccessReusesCachedResolution(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	cached := UserGroupAccess{
		UsableGroups: map[string]string{"vip": "VIP"},
		AutoGroups:   []string{"vip"},
	}
	common.SetContextKey(c, constant.ContextKeyUserGroupAccess, cached)

	actual := GetRequestUserGroupAccess(c)

	assert.Equal(t, cached, actual)
}

// configureCustomGroupAccessTest 配置 custom 模式所需的站点分组与可用分组设置，结束后还原喵。
func configureCustomGroupAccessTest(t *testing.T) {
	t.Helper()
	originalRatios := ratio_setting.GroupRatio2JSONString()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalAccessMode := console_setting.GetConsoleSetting().GroupAccessMode
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1}`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		console_setting.GetConsoleSetting().GroupAccessMode = originalAccessMode
	})
}

func TestResolveUserGroupAccessCustomModeUsesOnlyWhitelist(t *testing.T) {
	configureCustomGroupAccessTest(t)

	access := ResolveUserGroupAccessForUser(7, "default", GroupAccessModeCustom, `["vip","missing"]`)

	// 白名单内的 vip 放行，白名单外的 default 即使等于用户所属分组也不放行喵。
	assert.True(t, access.Allows("vip"))
	assert.False(t, access.Allows("default"))
	// 已被删除的分组不在倍率配置里，即便写进白名单也不放行喵。
	assert.False(t, access.Allows("missing"))
	// 旧口径的自动分组在 custom 模式下不返回喵。
	assert.Empty(t, access.AutoGroups)
	assert.True(t, access.HasCustomMode())
}

func TestResolveUserGroupAccessCustomModeEmptyWhitelistAllowsBuiltinPseudoGroupsOnly(t *testing.T) {
	configureCustomGroupAccessTest(t)

	access := ResolveUserGroupAccessForUser(7, "default", GroupAccessModeCustom, "")

	// 留空表示只放行虚拟模型与自定上游两个内置伪分组喵。
	assert.True(t, access.AllowsBuiltinGroup(constant.GroupVirtual))
	assert.True(t, access.AllowsBuiltinGroup(constant.GroupUserUpstream))
	// 用户共享分组必须显式勾选才放行喵。
	assert.False(t, access.AllowsBuiltinGroup(constant.GroupUserShared))
	// 任何真实分组都不放行，包括用户自己所属的 default 喵。
	assert.False(t, access.Allows("default"))
}

func TestResolveUserGroupAccessCustomModeHonorsExplicitUserShared(t *testing.T) {
	configureCustomGroupAccessTest(t)

	access := ResolveUserGroupAccessForUser(7, "default", GroupAccessModeCustom, `["user-shared"]`)

	assert.True(t, access.AllowsBuiltinGroup(constant.GroupUserShared))
	// 显式勾选的用户共享分组通过 Allows 也判定为可访问，供令牌分组校验使用喵。
	assert.True(t, access.Allows(constant.GroupUserShared))
}

func TestResolveUserGroupAccessLegacyModeKeepsBuiltinGroupsUnset(t *testing.T) {
	configureCustomGroupAccessTest(t)

	access := ResolveUserGroupAccessForUser(7, "default", GroupAccessModeLegacy, `["user-shared"]`)

	// legacy 模式不填充内置放行位，内置伪分组沿用旧行为全部放行喵。
	assert.Nil(t, access.BuiltinGroups)
	assert.True(t, access.AllowsBuiltinGroup(constant.GroupUserShared))
	assert.False(t, access.HasCustomMode())
	// 旧口径下全局可用分组与用户所属分组依旧生效喵。
	assert.True(t, access.Allows("default"))
	assert.True(t, access.Allows("vip"))
}

func TestResolveUserGroupAccessFollowsGlobalModeWhenUserModeEmpty(t *testing.T) {
	configureCustomGroupAccessTest(t)
	console_setting.GetConsoleSetting().GroupAccessMode = "custom"

	access := ResolveUserGroupAccessForUser(7, "default", GroupAccessModeAuto, `["vip"]`)

	// 用户未选模式时跟随全局 custom，白名单外的分组不被放行喵。
	assert.True(t, access.Allows("vip"))
	assert.False(t, access.Allows("default"))
	assert.True(t, access.HasCustomMode())
}

func TestIsUserSelectableGroupForUserAcceptsBuiltinGroupsInCustomMode(t *testing.T) {
	configureCustomGroupAccessTest(t)

	// 内置伪分组不在倍率配置里，但 custom 模式下仍应可选喵。
	assert.True(t, IsUserSelectableGroupForUser(7, "default", constant.GroupVirtual, GroupAccessModeCustom, ""))
	// 真实分组必须同时满足白名单与倍率配置喵。
	assert.False(t, IsUserSelectableGroupForUser(7, "default", "vip", GroupAccessModeCustom, ""))
	assert.True(t, IsUserSelectableGroupForUser(7, "default", "vip", GroupAccessModeCustom, `["vip"]`))
	// auto 永远不是可直接选择的分组喵。
	assert.False(t, IsUserSelectableGroupForUser(7, "default", "auto", GroupAccessModeCustom, ""))
}
