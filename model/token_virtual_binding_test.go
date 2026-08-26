package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

// TestDeleteTokenByIdCleansVirtualModelBindings 验证删除单个 API Key 时联动清理虚拟模型授权绑定喵。
func TestDeleteTokenByIdCleansVirtualModelBindings(t *testing.T) {
	truncateTables(t)
	// 测试库未迁移绑定表时先建表，保证联动删除可执行喵。
	require.NoError(t, DB.AutoMigrate(&VirtualModelTokenBinding{}))
	// 手动清空绑定表避免测试间累积，tokens 表由 truncateTables 兜底清理喵。
	require.NoError(t, DB.Exec("DELETE FROM virtual_model_token_bindings").Error)
	// 构造一个待删除 token 与其在多个虚拟模型上的授权绑定喵。
	token := Token{Id: 1001, UserId: 7, Key: "vm-binding-delete-key", Name: "binding-delete", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, DB.Create(&token).Error)
	require.NoError(t, DB.Create(&VirtualModelTokenBinding{VirtualModelID: 3, TokenID: token.Id, OwnerUserID: 7, CreatedTime: 1}).Error)
	require.NoError(t, DB.Create(&VirtualModelTokenBinding{VirtualModelID: 4, TokenID: token.Id, OwnerUserID: 7, CreatedTime: 1}).Error)
	// 删除 token 后，其所有虚拟模型授权绑定必须一并消失喵。
	require.NoError(t, DeleteTokenById(token.Id, 7))
	var remainingCount int64
	require.NoError(t, DB.Model(&VirtualModelTokenBinding{}).Where("token_id = ?", token.Id).Count(&remainingCount).Error)
	require.Zero(t, remainingCount)
}

// TestBatchDeleteTokensCleansVirtualModelBindings 验证批量删除 API Key 时事务内联动清理授权绑定喵。
func TestBatchDeleteTokensCleansVirtualModelBindings(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&VirtualModelTokenBinding{}))
	require.NoError(t, DB.Exec("DELETE FROM virtual_model_token_bindings").Error)
	// 构造两个待删除 token、各自绑定以及一个不应受影响的其它 token 绑定喵。
	firstToken := Token{Id: 1002, UserId: 7, Key: "vm-binding-batch-key-a", Name: "batch-a", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	secondToken := Token{Id: 1003, UserId: 7, Key: "vm-binding-batch-key-b", Name: "batch-b", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, DB.Create(&firstToken).Error)
	require.NoError(t, DB.Create(&secondToken).Error)
	require.NoError(t, DB.Create(&VirtualModelTokenBinding{VirtualModelID: 3, TokenID: firstToken.Id, OwnerUserID: 7}).Error)
	require.NoError(t, DB.Create(&VirtualModelTokenBinding{VirtualModelID: 4, TokenID: secondToken.Id, OwnerUserID: 7}).Error)
	require.NoError(t, DB.Create(&VirtualModelTokenBinding{VirtualModelID: 5, TokenID: 999, OwnerUserID: 7}).Error)
	// 批量删除后两个 token 的绑定都必须清理，且不影响其它 token 的绑定喵。
	deletedCount, err := BatchDeleteTokens([]int{firstToken.Id, secondToken.Id}, 7)
	require.NoError(t, err)
	require.Equal(t, 2, deletedCount)
	var remainingCount int64
	require.NoError(t, DB.Model(&VirtualModelTokenBinding{}).Where("token_id IN ?", []int{firstToken.Id, secondToken.Id}).Count(&remainingCount).Error)
	require.Zero(t, remainingCount)
	var untouchedCount int64
	require.NoError(t, DB.Model(&VirtualModelTokenBinding{}).Where("token_id = ?", 999).Count(&untouchedCount).Error)
	require.Equal(t, int64(1), untouchedCount)
}
