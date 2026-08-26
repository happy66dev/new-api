package model

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNormalizeUserUpstreamModelName 验证上游模型名规范化的边界喵。
func TestNormalizeUserUpstreamModelName(t *testing.T) {
	// 正常名称统一转为小写喵。
	normalizedName, err := NormalizeUserUpstreamModelName("My-Upstream_1")
	require.NoError(t, err)
	assert.Equal(t, "my-upstream_1", normalizedName)

	// 带 upstream/ 前缀时自动剥离喵。
	normalizedName, err = NormalizeUserUpstreamModelName("upstream/My-Model")
	require.NoError(t, err)
	assert.Equal(t, "my-model", normalizedName)

	// 空白输入拒绝喵。
	_, err = NormalizeUserUpstreamModelName("   ")
	require.Error(t, err)

	// 非法字符（中文、空格、路径分隔符）拒绝喵。
	for _, invalidName := range []string{"中文模型", "my model", "a/b", "a\\b", "a.."} {
		_, err := NormalizeUserUpstreamModelName(invalidName)
		assert.Error(t, err, "名称 %q 应被拒绝", invalidName)
	}

	// 超长名称拒绝喵。
	_, err = NormalizeUserUpstreamModelName(strings.Repeat("a", 97))
	require.Error(t, err)
}

// TestUserUpstreamModelCRUD 验证用户上游模型完整 CRUD 流程喵。
func TestUserUpstreamModelCRUD(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&UserUpstreamModel{}))
	require.NoError(t, DB.Exec("DELETE FROM user_upstream_models").Error)

	// 创建一条用户上游模型，凭据密文直接写入喵。
	created := &UserUpstreamModel{
		OwnerUserID:      7,
		NormalizedName:   "alpha",
		DisplayName:      "Alpha 上游",
		Enabled:          true,
		EncryptedBaseURL: "encrypted-base-url",
		EncryptedAPIKey:  "encrypted-api-key",
		RealModelName:    "gpt-4o",
		AuthStyle:        "bearer",
		ModelRatio:       "18.5",
		Version:          1,
		CreatedTime:      100,
		UpdatedTime:      100,
	}
	require.NoError(t, DB.Create(created).Error)

	// 属主可查询到，名称与模型名符合预期喵。
	fetched, err := GetUserUpstreamModelByOwnerID(created.ID, 7)
	require.NoError(t, err)
	assert.Equal(t, "alpha", fetched.NormalizedName)
	assert.Equal(t, "upstream/alpha", fetched.UserUpstreamModelName())

	// 列表只返回属主自己的模型喵。
	list, err := GetUserUpstreamModelsByOwner(7)
	require.NoError(t, err)
	assert.Len(t, list, 1)

	// 启用模型可被属主按名称查到喵。
	enabled, err := GetEnabledUserUpstreamModelByOwnerName(7, "alpha")
	require.NoError(t, err)
	assert.Equal(t, "alpha", enabled.NormalizedName)

	// 跨用户查询必须隐藏资源存在性喵。
	_, err = GetUserUpstreamModelByOwnerID(created.ID, 8)
	require.Error(t, err)
	_, err = GetEnabledUserUpstreamModelByOwnerName(8, "alpha")
	require.Error(t, err)

	// 停用模型不能被会话态查询到喵。
	require.NoError(t, DB.Model(created).Update("enabled", false).Error)
	_, err = GetEnabledUserUpstreamModelByOwnerName(7, "alpha")
	require.Error(t, err)

	// 版本错误的删除被拒绝喵。
	require.NoError(t, DB.Model(created).Update("enabled", true).Error)
	require.NoError(t, DB.Model(created).Update("version", 2).Error)
	err = DeleteUserUpstreamModelByOwnerWithVersion(created.ID, 7, 1)
	require.Error(t, err)

	// 正确版本的删除成功，重复删除返回记录不存在喵。
	err = DeleteUserUpstreamModelByOwnerWithVersion(created.ID, 7, 2)
	require.NoError(t, err)
	err = DeleteUserUpstreamModelByOwnerWithVersion(created.ID, 7, 2)
	require.Error(t, err)
}
