package middleware

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestVirtualModelInflightBasic 验证虚拟模型活跃请求的进入、更新与退出喵。
func TestVirtualModelInflightBasic(t *testing.T) {
	// 进入一个活跃请求并断言计数与详情喵。
	requestID := EnterVirtualModelInflight(7, "virtual/demo")
	count, active := GetVirtualModelActiveRequests(7)
	require.Equal(t, int64(1), count)
	require.Len(t, active, 1)
	require.Equal(t, requestID, active[0].RequestID)
	require.Equal(t, "virtual/demo", active[0].ModelName)
	require.False(t, active[0].StartedAt.IsZero())

	// 更新候选序号与标签后应反映在详情里喵。
	UpdateVirtualModelInflightCandidate(7, requestID, 2, "gpt-4.1")
	_, active = GetVirtualModelActiveRequests(7)
	require.Len(t, active, 1)
	require.Equal(t, 2, active[0].CandidateIndex)
	require.Equal(t, "gpt-4.1", active[0].CandidateLabel)

	// 退出后计数归零且容器被清理，返回空列表喵。
	ExitVirtualModelInflight(7, requestID)
	count, active = GetVirtualModelActiveRequests(7)
	require.Equal(t, int64(0), count)
	require.Empty(t, active)
}

// TestVirtualModelInflightMulti 验证同一模型的多个并发请求独立计数与清理喵。
func TestVirtualModelInflightMulti(t *testing.T) {
	// 连续进入多个活跃请求，计数应为总个数喵。
	requestIDOne := EnterVirtualModelInflight(9, "virtual/multi")
	requestIDTwo := EnterVirtualModelInflight(9, "virtual/multi")
	count, active := GetVirtualModelActiveRequests(9)
	require.Equal(t, int64(2), count)
	require.Len(t, active, 2)

	// 退出其中一个后仍保留另一个的详情喵。
	ExitVirtualModelInflight(9, requestIDOne)
	count, active = GetVirtualModelActiveRequests(9)
	require.Equal(t, int64(1), count)
	require.Len(t, active, 1)
	require.Equal(t, requestIDTwo, active[0].RequestID)

	// 全部退出后容器被清理喵。
	ExitVirtualModelInflight(9, requestIDTwo)
	require.Empty(t, activeRequestsOf(9))
}

// TestVirtualModelInflightDetailCap 验证详情条目超过容量后只计数不记详情喵。
func TestVirtualModelInflightDetailCap(t *testing.T) {
	// 连续进入超过容量上限的请求，计数仍精确但详情条数被限制喵。
	requestIDs := make([]string, 0, maxVirtualActiveDetailCount+10)
	for index := 0; index < maxVirtualActiveDetailCount+10; index++ {
		requestIDs = append(requestIDs, EnterVirtualModelInflight(11, "virtual/cap"))
	}
	count, active := GetVirtualModelActiveRequests(11)
	require.Equal(t, int64(maxVirtualActiveDetailCount+10), count)
	require.Len(t, active, maxVirtualActiveDetailCount)

	// 退出全部请求后计数归零，避免污染其它用例喵。
	for _, requestID := range requestIDs {
		ExitVirtualModelInflight(11, requestID)
	}
	require.Empty(t, activeRequestsOf(11))
}

// TestUpstreamModelInflight 验证上游模型自用与共享维度的独立计数喵。
func TestUpstreamModelInflight(t *testing.T) {
	// 自用与共享各进入一个请求，分别计数喵。
	EnterUpstreamModelInflight(3, "user/demo", false)
	EnterUpstreamModelInflight(3, "user/demo", true)
	selfCount, sharedCount := GetUpstreamModelActiveCount(3)
	require.Equal(t, int64(1), selfCount)
	require.Equal(t, int64(1), sharedCount)

	// 按名称聚合时应同时计入自用与共享喵。
	require.Equal(t, int64(2), GetUpstreamModelActiveCountByName("user/demo"))
	// 不存在的模型名聚合结果为零喵。
	require.Equal(t, int64(0), GetUpstreamModelActiveCountByName("user/other"))

	// 退出自用后共享维度不受影响喵。
	ExitUpstreamModelInflight(3, false)
	selfCount, sharedCount = GetUpstreamModelActiveCount(3)
	require.Equal(t, int64(0), selfCount)
	require.Equal(t, int64(1), sharedCount)

	// 共享也退出后条目被清理，按名称聚合归零喵。
	ExitUpstreamModelInflight(3, true)
	require.Equal(t, int64(0), GetUpstreamModelActiveCountByName("user/demo"))
}

// TestUpstreamModelInflightNameAggregation 验证同名不同属主的上游模型请求按名称聚合喵。
func TestUpstreamModelInflightNameAggregation(t *testing.T) {
	// 两个属主各自拥有同名模型，请求进入后按名称聚合到同一统计喵。
	EnterUpstreamModelInflight(101, "user/plaza", false)
	EnterUpstreamModelInflight(202, "user/plaza", true)
	require.Equal(t, int64(2), GetUpstreamModelActiveCountByName("user/plaza"))

	// 清理两个条目的活跃状态喵。
	ExitUpstreamModelInflight(101, false)
	ExitUpstreamModelInflight(202, true)
	require.Equal(t, int64(0), GetUpstreamModelActiveCountByName("user/plaza"))
}

// TestInternalModelInflight 验证内部模型的活跃请求计数喵。
func TestInternalModelInflight(t *testing.T) {
	// 进入两个内部模型请求，计数准确喵。
	EnterInternalModelInflight("gpt-4o")
	EnterInternalModelInflight("gpt-4o")
	require.Equal(t, int64(2), GetInternalModelActiveCount("gpt-4o"))
	require.Equal(t, int64(0), GetInternalModelActiveCount("claude-3-5-sonnet"))

	// 退出全部后计数归零且条目清理喵。
	ExitInternalModelInflight("gpt-4o")
	ExitInternalModelInflight("gpt-4o")
	require.Equal(t, int64(0), GetInternalModelActiveCount("gpt-4o"))

	// 空模型名与未进入的退出不产生副作用喵。
	EnterInternalModelInflight("")
	ExitInternalModelInflight("missing")
}

// activeRequestsOf 返回指定虚拟模型活跃详情数量的便捷断言辅助喵。
func activeRequestsOf(modelID int64) int {
	_, active := GetVirtualModelActiveRequests(modelID)
	return len(active)
}
