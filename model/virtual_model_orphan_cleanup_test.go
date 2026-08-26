package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDeleteOrphanVirtualModelInternalCandidatesByModel 验证模型删除后的候选清理语义喵。
func TestDeleteOrphanVirtualModelInternalCandidatesByModel(t *testing.T) {
	truncateTables(t)
	// 测试库未迁移的表先建表，保证清理逻辑可执行喵。
	require.NoError(t, DB.AutoMigrate(&VirtualModelCandidate{}, &VirtualModelInternalCandidate{}, &VirtualModelCustomCandidate{}, &VirtualModelFailureRule{}, &VirtualModelManualFreeze{}, &Ability{}))
	// 手动清空虚拟模型相关表，避免测试间累积，其它表由 truncateTables 兜底清理喵。
	for _, statement := range []string{
		"DELETE FROM virtual_model_failure_rules",
		"DELETE FROM virtual_model_internal_candidates",
		"DELETE FROM virtual_model_custom_candidates",
		"DELETE FROM virtual_model_manual_freezes",
		"DELETE FROM virtual_model_candidates",
		"DELETE FROM abilities",
	} {
		require.NoError(t, DB.Exec(statement).Error)
	}

	// 构造两个引用同一模型的内部候选：default 分组有渠道支撑、vip 分组无渠道支撑喵。
	supportedCandidate := VirtualModelCandidate{VirtualModelID: 1, StableOrder: 0, SourceType: VirtualModelSourceInternal, Enabled: true}
	require.NoError(t, DB.Create(&supportedCandidate).Error)
	require.NoError(t, DB.Create(&VirtualModelInternalCandidate{CandidateID: supportedCandidate.ID, GroupName: "default", RealModelName: "gpt-4"}).Error)
	orphanCandidate := VirtualModelCandidate{VirtualModelID: 1, StableOrder: 1, SourceType: VirtualModelSourceInternal, Enabled: true}
	require.NoError(t, DB.Create(&orphanCandidate).Error)
	require.NoError(t, DB.Create(&VirtualModelInternalCandidate{CandidateID: orphanCandidate.ID, GroupName: "vip", RealModelName: "gpt-4"}).Error)

	// 只在 default 分组提供 gpt-4 的启用渠道能力，vip 分组没有喵。
	require.NoError(t, DB.Create(&Ability{Group: "default", Model: "gpt-4", ChannelId: 9, Enabled: true}).Error)

	// 清理后仅 default 分组有渠道支撑的候选保留，vip 分组无渠道候选应被删除喵。
	deletedCount, err := DeleteOrphanVirtualModelInternalCandidatesByModel("gpt-4")
	require.NoError(t, err)
	require.Equal(t, 1, deletedCount)
	// 保留的候选应是 default 分组的支持候选喵。
	var remainingInternalCandidate VirtualModelCandidate
	require.NoError(t, DB.First(&remainingInternalCandidate, supportedCandidate.ID).Error)
	require.Equal(t, supportedCandidate.ID, remainingInternalCandidate.ID)
	// 无渠道支撑的 vip 分组候选应已删除喵。
	var orphanGoneCount int64
	require.NoError(t, DB.Model(&VirtualModelCandidate{}).Where("id = ?", orphanCandidate.ID).Count(&orphanGoneCount).Error)
	require.Zero(t, orphanGoneCount)
}

// TestDeleteOrphanVirtualModelInternalCandidatesByModelSafeGuards 验证空模型名等防御边界喵。
func TestDeleteOrphanVirtualModelInternalCandidatesByModelSafeGuards(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&VirtualModelCandidate{}, &VirtualModelInternalCandidate{}, &Ability{}))
	// 空模型名直接跳过，不产生任何删除喵。
	deletedCount, err := DeleteOrphanVirtualModelInternalCandidatesByModel("")
	require.NoError(t, err)
	require.Zero(t, deletedCount)
	// 没有候选引用该模型时安全返回零喵。
	deletedCount, err = DeleteOrphanVirtualModelInternalCandidatesByModel("nonexistent-model")
	require.NoError(t, err)
	require.Zero(t, deletedCount)
}
