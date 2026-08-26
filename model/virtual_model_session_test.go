package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGetEnabledVirtualModelByOwnerName 验证会话态按 owner 查询启用虚拟模型的边界喵。
func TestGetEnabledVirtualModelByOwnerName(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&VirtualModel{}))
	require.NoError(t, DB.Exec("DELETE FROM virtual_models").Error)

	// 构造启用与停用的同名虚拟模型，以及另一用户的虚拟模型喵。
	require.NoError(t, DB.Create(&VirtualModel{OwnerUserID: 7, NormalizedName: "session-model", Enabled: true}).Error)
	require.NoError(t, DB.Create(&VirtualModel{OwnerUserID: 8, NormalizedName: "session-model", Enabled: true}).Error)
	require.NoError(t, DB.Create(&VirtualModel{OwnerUserID: 7, NormalizedName: "disabled-model", Enabled: false}).Error)

	// 启用模型可被 owner 会话态查询到喵。
	enabledModel, err := GetEnabledVirtualModelByOwnerName(7, "session-model")
	require.NoError(t, err)
	require.Equal(t, "session-model", enabledModel.NormalizedName)

	// 停用模型不能被查询到，避免会话态调用已停用的虚拟模型喵。
	_, err = GetEnabledVirtualModelByOwnerName(7, "disabled-model")
	require.Error(t, err)

	// 跨用户查询必须隐藏资源存在性喵。
	_, err = GetEnabledVirtualModelByOwnerName(7, "other-owner-model")
	require.Error(t, err)

	// 无效身份或空名称直接返回资源不存在喵。
	_, err = GetEnabledVirtualModelByOwnerName(0, "session-model")
	require.Error(t, err)
	_, err = GetEnabledVirtualModelByOwnerName(7, "")
	require.Error(t, err)
}
