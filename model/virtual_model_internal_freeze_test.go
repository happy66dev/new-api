package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestVirtualModelInternalFreezeStateRoundTrip 验证内部候选冻结状态的写入、读取与过期语义喵。
func TestVirtualModelInternalFreezeStateRoundTrip(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&VirtualModelInternalFreezeState{}))
	require.NoError(t, DB.Exec("DELETE FROM virtual_model_internal_freeze_states").Error)

	// 写入一个冻结状态后应能在未过期时读取到喵。
	require.NoError(t, UpsertVirtualModelInternalFreezeState(7, 11, 2000, "rate_limited", 1000))
	states, err := GetActiveVirtualModelInternalFreezeStates(7, []int{11}, 1000)
	require.NoError(t, err)
	require.Contains(t, states, 11)
	require.Equal(t, int64(2000), states[11].FrozenUntil)
	require.Equal(t, "rate_limited", states[11].LastFailureClass)

	// 冻结已过期后不应再返回，避免过期候选被永久跳过喵。
	expiredStates, err := GetActiveVirtualModelInternalFreezeStates(7, []int{11}, 2000)
	require.NoError(t, err)
	require.NotContains(t, expiredStates, 11)
}

// TestVirtualModelInternalFreezeStateExtendOnly 验证并发冻结不会把更长冻结缩短喵。
func TestVirtualModelInternalFreezeStateExtendOnly(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&VirtualModelInternalFreezeState{}))
	require.NoError(t, DB.Exec("DELETE FROM virtual_model_internal_freeze_states").Error)

	// 先写 3000 秒的较长冻结，再尝试写 2000 秒较短冻结喵。
	require.NoError(t, UpsertVirtualModelInternalFreezeState(7, 12, 3000, "upstream_server_error", 1000))
	require.NoError(t, UpsertVirtualModelInternalFreezeState(7, 12, 2000, "upstream_client_error", 1001))

	states, err := GetActiveVirtualModelInternalFreezeStates(7, []int{12}, 1001)
	require.NoError(t, err)
	// 较长冻结必须保留，且失败计数递增、失败分类被新失败覆盖喵。
	require.Equal(t, int64(3000), states[12].FrozenUntil)
	require.Equal(t, 2, states[12].ConsecutiveFails)
	require.Equal(t, "upstream_client_error", states[12].LastFailureClass)
}

// TestVirtualModelInternalFreezeStateClearWithVersion 验证成功请求只清除请求启动时观察到的冻结版本喵。
func TestVirtualModelInternalFreezeStateClearWithVersion(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&VirtualModelInternalFreezeState{}))
	require.NoError(t, DB.Exec("DELETE FROM virtual_model_internal_freeze_states").Error)

	require.NoError(t, UpsertVirtualModelInternalFreezeState(7, 13, 3000, "rate_limited", 1000))
	// 错误的更新时间版本不匹配，不得清除冻结喵。
	require.NoError(t, ClearVirtualModelInternalFreezeState(7, 13, 999, 1001))
	states, err := GetActiveVirtualModelInternalFreezeStates(7, []int{13}, 1001)
	require.NoError(t, err)
	require.Equal(t, int64(3000), states[13].FrozenUntil)

	// 匹配的版本清除后不再返回，失败计数归零喵。
	require.NoError(t, ClearVirtualModelInternalFreezeState(7, 13, 1000, 1002))
	clearedStates, err := GetActiveVirtualModelInternalFreezeStates(7, []int{13}, 1002)
	require.NoError(t, err)
	require.NotContains(t, clearedStates, 13)
}

// TestVirtualModelInternalFreezeStateSafeGuards 验证冻结函数对非法输入的防御边界喵。
func TestVirtualModelInternalFreezeStateSafeGuards(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&VirtualModelInternalFreezeState{}))
	require.NoError(t, DB.Exec("DELETE FROM virtual_model_internal_freeze_states").Error)

	// 无效所有者、空候选集合或非法时间不执行查询喵。
	states, err := GetActiveVirtualModelInternalFreezeStates(0, []int{11}, 1000)
	require.NoError(t, err)
	require.Empty(t, states)
	states, err = GetActiveVirtualModelInternalFreezeStates(7, nil, 1000)
	require.NoError(t, err)
	require.Empty(t, states)
	// 非法候选编号或过期冻结时间拒绝写入喵。
	require.Error(t, UpsertVirtualModelInternalFreezeState(7, 0, 2000, "rate_limited", 1000))
	require.Error(t, UpsertVirtualModelInternalFreezeState(7, 11, 500, "rate_limited", 1000))
	// 清除函数的非法输入安全返回空操作喵。
	require.NoError(t, ClearVirtualModelInternalFreezeState(0, 11, 1000, 1001))
	require.NoError(t, ClearVirtualModelInternalFreezeState(7, 11, 0, 1001))
}

// TestRecordVirtualModelInternalFailureThreshold 验证自动避险：连续失败未达阈值不冻结、达到阈值才冻结并清零计数喵。
func TestRecordVirtualModelInternalFailureThreshold(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&VirtualModelInternalFreezeState{}))
	require.NoError(t, DB.Exec("DELETE FROM virtual_model_internal_freeze_states").Error)

	// 首次失败：阈值 3，计数 1，未达阈值不冻结喵。
	reached, err := RecordVirtualModelInternalFailure(7, 21, 3, 3000, "upstream_server_error", 1000)
	require.NoError(t, err)
	require.False(t, reached)
	// 未冻结时活动状态查询不返回该候选，证明没有产生冻结喵。
	states, err := GetActiveVirtualModelInternalFreezeStates(7, []int{21}, 1001)
	require.NoError(t, err)
	require.NotContains(t, states, 21)

	// 第二次失败：计数 2，仍未达阈值不冻结喵。
	reached, err = RecordVirtualModelInternalFailure(7, 21, 3, 3000, "upstream_server_error", 1002)
	require.NoError(t, err)
	require.False(t, reached)

	// 第三次失败：计数 3 达到阈值，触发冻结且计数清零供冻结到期后重新攒喵。
	reached, err = RecordVirtualModelInternalFailure(7, 21, 3, 3000, "upstream_server_error", 1003)
	require.NoError(t, err)
	require.True(t, reached)
	states, err = GetActiveVirtualModelInternalFreezeStates(7, []int{21}, 1004)
	require.NoError(t, err)
	require.Contains(t, states, 21)
	require.Equal(t, int64(3000), states[21].FrozenUntil)
	require.Equal(t, 0, states[21].ConsecutiveFails)
}

// TestRecordVirtualModelInternalFailureThresholdOneAndReset 验证阈值 1 每次失败即冻结、成功清除后重新计数喵。
func TestRecordVirtualModelInternalFailureThresholdOneAndReset(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&VirtualModelInternalFreezeState{}))
	require.NoError(t, DB.Exec("DELETE FROM virtual_model_internal_freeze_states").Error)

	// 阈值 1：首次失败即触发冻结喵。
	reached, err := RecordVirtualModelInternalFailure(7, 22, 1, 3000, "rate_limited", 1000)
	require.NoError(t, err)
	require.True(t, reached)

	// 成功调用清除冻结后，再次失败从计数 1 重新开始喵。
	states, err := GetActiveVirtualModelInternalFreezeStates(7, []int{22}, 1001)
	require.NoError(t, err)
	require.Contains(t, states, 22)
	require.NoError(t, ClearVirtualModelInternalFreezeState(7, 22, states[22].UpdatedTime, 1002))
	reached, err = RecordVirtualModelInternalFailure(7, 22, 2, 3000, "rate_limited", 1003)
	require.NoError(t, err)
	require.False(t, reached)
}

// TestRecordVirtualModelInternalFailureSafeGuards 验证自动避险计数的非法输入防御边界喵。
func TestRecordVirtualModelInternalFailureSafeGuards(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&VirtualModelInternalFreezeState{}))
	require.NoError(t, DB.Exec("DELETE FROM virtual_model_internal_freeze_states").Error)

	// 非正阈值、无效所有者或候选编号按无操作返回，不产生冻结喵。
	reached, err := RecordVirtualModelInternalFailure(7, 23, 0, 3000, "rate_limited", 1000)
	require.NoError(t, err)
	require.False(t, reached)
	reached, err = RecordVirtualModelInternalFailure(0, 23, 3, 3000, "rate_limited", 1000)
	require.NoError(t, err)
	require.False(t, reached)
	reached, err = RecordVirtualModelInternalFailure(7, 0, 3, 3000, "rate_limited", 1000)
	require.NoError(t, err)
	require.False(t, reached)
}
