package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRecordEntityProbeStateCounted 验证计入成功率的记录累积请求数与成功数喵。
func TestRecordEntityProbeStateCounted(t *testing.T) {
	// 独立内存库保证用例隔离喵。
	setupEntityProbeStateTestDB(t)

	// 两次成功一次失败：请求数 3、成功数 2、最近一次为失败并携带受控错误喵。
	require.NoError(t, RecordEntityProbeCounted(EntityProbeScopeUpstream, 7, 0, 1, 1000, true, 200, ""))
	require.NoError(t, RecordEntityProbeCounted(EntityProbeScopeUpstream, 7, 0, 1, 2000, true, 150, ""))
	require.NoError(t, RecordEntityProbeCounted(EntityProbeScopeUpstream, 7, 0, 1, 3000, false, 900, "rate_limited"))

	state, err := GetEntityProbeState(EntityProbeScopeUpstream, 7)
	require.NoError(t, err)
	require.Equal(t, int64(3), state.RequestCount)
	require.Equal(t, int64(2), state.SuccessCount)
	require.Equal(t, int64(3000), state.LastAt)
	require.False(t, state.LastSuccess)
	require.Equal(t, int64(900), state.LastLatencyMs)
	require.Equal(t, "rate_limited", state.LastError)
}

// TestEntityProbeStateLastFailureSurvivesSuccess 验证最近一次失败在后续成功时仍保留喵。
func TestEntityProbeStateLastFailureSurvivesSuccess(t *testing.T) {
	setupEntityProbeStateTestDB(t)

	// 先失败一次，再成功一次：last_error 反映最近一次成功，但失败历史必须保留喵。
	require.NoError(t, RecordEntityProbeCounted(EntityProbeScopeUpstream, 7, 0, 1, 2000, false, 900, "rate_limited"))
	require.NoError(t, RecordEntityProbeCounted(EntityProbeScopeUpstream, 7, 0, 1, 3000, true, 150, ""))

	state, err := GetEntityProbeState(EntityProbeScopeUpstream, 7)
	require.NoError(t, err)
	// 最近一次调用是成功，LastError 为空喵。
	require.True(t, state.LastSuccess)
	require.Empty(t, state.LastError)
	// 最近一次失败信息必须保留：时间 2000 与错误分类 rate_limited 喵。
	require.Equal(t, int64(2000), state.LastFailureAt)
	require.Equal(t, "rate_limited", state.LastFailureError)

	// 再失败一次：最近失败信息更新为最新失败喵。
	require.NoError(t, RecordEntityProbeCounted(EntityProbeScopeUpstream, 7, 0, 1, 4000, false, 800, "timeout"))
	state, err = GetEntityProbeState(EntityProbeScopeUpstream, 7)
	require.NoError(t, err)
	require.Equal(t, int64(4000), state.LastFailureAt)
	require.Equal(t, "timeout", state.LastFailureError)
}

// TestTouchEntityProbeLastAtDoesNotChangeCounts 验证配置态请求只更新最近时间不改计数喵。
func TestTouchEntityProbeLastAtDoesNotChangeCounts(t *testing.T) {
	setupEntityProbeStateTestDB(t)

	// 先计入一次成功，再触达一次配置态请求喵。
	require.NoError(t, RecordEntityProbeCounted(EntityProbeScopeUpstreamShared, 9, 0, 2, 1000, true, 100, ""))
	require.NoError(t, TouchEntityProbeLastAt(EntityProbeScopeUpstreamShared, 9, 0, 2, 5000))

	state, err := GetEntityProbeState(EntityProbeScopeUpstreamShared, 9)
	require.NoError(t, err)
	require.Equal(t, int64(1), state.RequestCount)
	require.Equal(t, int64(1), state.SuccessCount)
	require.Equal(t, int64(5000), state.LastAt)
}

// TestEntityProbeStateScopeIsolation 验证不同作用域与实体互不干扰喵。
func TestEntityProbeStateScopeIsolation(t *testing.T) {
	setupEntityProbeStateTestDB(t)

	// 同一实体 id 在不同作用域下是不同状态行喵。
	require.NoError(t, RecordEntityProbeCounted(EntityProbeScopeUpstream, 7, 0, 1, 1000, true, 100, ""))
	require.NoError(t, RecordEntityProbeCounted(EntityProbeScopeUpstreamShared, 7, 0, 1, 2000, false, 300, "timeout"))

	selfState, selfErr := GetEntityProbeState(EntityProbeScopeUpstream, 7)
	require.NoError(t, selfErr)
	require.Equal(t, int64(1), selfState.RequestCount)
	sharedState, sharedErr := GetEntityProbeState(EntityProbeScopeUpstreamShared, 7)
	require.NoError(t, sharedErr)
	require.Equal(t, int64(1), sharedState.RequestCount)
	require.False(t, sharedState.LastSuccess)
}

// TestDeleteEntityProbeStates 验证删除联动清理喵。
func TestDeleteEntityProbeStates(t *testing.T) {
	setupEntityProbeStateTestDB(t)

	require.NoError(t, RecordEntityProbeCounted(EntityProbeScopeUpstream, 7, 0, 1, 1000, true, 100, ""))
	require.NoError(t, RecordEntityProbeCounted(EntityProbeScopeVirtualCandidate, 3, 7, 1, 2000, false, 400, "rate_limited"))

	// 删除上游模型状态行后自用维度消失，候选维度保留喵。
	require.NoError(t, DeleteEntityProbeStates(EntityProbeScopeUpstream, 7))
	_, selfErr := GetEntityProbeState(EntityProbeScopeUpstream, 7)
	require.Error(t, selfErr)

	// 删除虚拟模型整体与候选节点状态行喵。
	require.NoError(t, DeleteVirtualEntityProbeStates(7))
	_, candidateErr := GetEntityProbeState(EntityProbeScopeVirtualCandidate, 3)
	require.Error(t, candidateErr)
}

// setupEntityProbeStateTestDB 确保状态表已迁移并清空存量行，保证用例隔离喵。
func setupEntityProbeStateTestDB(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&EntityProbeState{}))
	require.NoError(t, DB.Exec("DELETE FROM entity_probe_states").Error)
}
