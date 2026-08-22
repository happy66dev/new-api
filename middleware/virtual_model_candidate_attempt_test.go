package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// newVirtualModelAttemptTestContext 构造一个只含虚拟模型执行状态的测试上下文喵。
// 传入 nil 执行状态时表示模拟普通模型请求，即上下文里根本没有虚拟模型执行状态喵。
func newVirtualModelAttemptTestContext(executionState *virtualModelExecutionState) *gin.Context {
	// 使用 gin 官方测试上下文，避免依赖真实 HTTP 服务器与路由喵。
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	// 仅在需要模拟虚拟模型请求时写入执行状态，普通请求保持上下文干净喵。
	if executionState != nil {
		common.SetContextKey(ctx, constant.ContextKeyVirtualModelExecutionState, executionState)
	}
	return ctx
}

// newVirtualModelAttemptTestState 构造带两个候选的执行状态，便于验证索引与尝试标识的组合语义喵。
func newVirtualModelAttemptTestState() *virtualModelExecutionState {
	// 第一个候选为内部候选，第二个候选为自定义候选，用于区分不同来源的返回结果喵。
	return &virtualModelExecutionState{
		virtualModelName: "candidate-attempt-test",
		ownerUserID:      41,
		executionSnapshot: &model.VirtualModelExecutionSnapshot{
			Candidates: []model.VirtualModelCandidateSnapshot{
				{CandidateID: 71, VirtualModelID: 9, StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true, GroupName: "default", RealModelName: "gpt-test"},
				{CandidateID: 72, VirtualModelID: 9, StableOrder: 1, SourceType: model.VirtualModelSourceCustom, Enabled: true, RealModelName: "custom-test"},
			},
			FailureRulesByCandidateID: map[int][]model.VirtualModelFailureRule{},
		},
		// 默认置为未激活状态，由各用例按需推进候选索引与尝试标识喵。
		currentCandidateIndex: -1,
	}
}

// TestGetActiveVirtualModelCandidateAttemptRejectsNonVirtualRequest 验证普通请求不会被误判为虚拟候选请求喵。
func TestGetActiveVirtualModelCandidateAttemptRejectsNonVirtualRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 普通模型请求的上下文里没有虚拟模型执行状态，必须返回 false 让调用方保留原计费语义喵。
	candidateAttempt, foundCandidateAttempt := GetActiveVirtualModelCandidateAttempt(newVirtualModelAttemptTestContext(nil))
	require.False(t, foundCandidateAttempt)
	// 未命中时返回值必须是零值，避免调用方误用残留候选编号喵。
	require.Equal(t, VirtualModelCandidateAttempt{}, candidateAttempt)
}

// TestGetActiveVirtualModelCandidateAttemptRejectsNilContext 验证空上下文不会导致断言 panic 喵。
func TestGetActiveVirtualModelCandidateAttemptRejectsNilContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 喵~防御：中间件组合异常时上下文可能为 nil，此时必须安全返回 false 而不是崩溃喵。
	_, foundCandidateAttempt := GetActiveVirtualModelCandidateAttempt(nil)
	require.False(t, foundCandidateAttempt)
}

// TestGetActiveVirtualModelCandidateAttemptRejectsInactiveStates 验证候选尚未激活或已耗尽时一律不返回身份喵。
func TestGetActiveVirtualModelCandidateAttemptRejectsInactiveStates(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 三种非法运行时组合都必须被拦截，避免调用方用过期候选建立计费喵。
	inactiveStateCases := []struct {
		name                  string // 子用例名称，描述被模拟的非法运行时组合喵。
		currentCandidateIndex int    // 当前候选索引，负数或越界都视为未激活喵。
		candidateAttemptID    string // 当前候选尝试标识，空串表示切换未完成喵。
	}{
		{name: "候选索引尚未推进", currentCandidateIndex: -1, candidateAttemptID: "vc71a1"},
		{name: "候选索引越界", currentCandidateIndex: 2, candidateAttemptID: "vc71a1"},
		{name: "尝试标识为空", currentCandidateIndex: 0, candidateAttemptID: ""},
	}

	for _, inactiveStateCase := range inactiveStateCases {
		t.Run(inactiveStateCase.name, func(t *testing.T) {
			// 按子用例设定索引与尝试标识，其余字段保持有效以确保失败原因唯一喵。
			executionState := newVirtualModelAttemptTestState()
			executionState.currentCandidateIndex = inactiveStateCase.currentCandidateIndex
			executionState.currentCandidateAttemptID = inactiveStateCase.candidateAttemptID
			executionState.candidateAttemptSequence = 1
			// 任一条件不满足都必须返回 false，保证候选级计费只在候选真正激活后建立喵。
			_, foundCandidateAttempt := GetActiveVirtualModelCandidateAttempt(newVirtualModelAttemptTestContext(executionState))
			require.False(t, foundCandidateAttempt)
		})
	}
}

// TestGetActiveVirtualModelCandidateAttemptReturnsActiveInternalCandidate 验证已激活内部候选返回完整可写日志的身份喵。
func TestGetActiveVirtualModelCandidateAttemptReturnsActiveInternalCandidate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 模拟第一个内部候选刚被激活，尝试序号为一喵。
	executionState := newVirtualModelAttemptTestState()
	executionState.currentCandidateIndex = 0
	executionState.currentCandidateAttemptID = "vc71a1"
	executionState.candidateAttemptSequence = 1
	executionState.loopRoundsCompleted = 0

	candidateAttempt, foundCandidateAttempt := GetActiveVirtualModelCandidateAttempt(newVirtualModelAttemptTestContext(executionState))
	require.True(t, foundCandidateAttempt)
	// 候选编号必须来自快照而不是索引，避免跳过冻结候选后关联错误的候选喵。
	require.Equal(t, 71, candidateAttempt.CandidateID)
	require.Equal(t, "vc71a1", candidateAttempt.CandidateAttemptID)
	require.Equal(t, 1, candidateAttempt.AttemptSequence)
	require.Equal(t, 0, candidateAttempt.CandidateIndex)
	// 来源类型决定 controller 是否进入候选级 relay 与计费路径，必须原样返回喵。
	require.Equal(t, model.VirtualModelSourceInternal, candidateAttempt.SourceType)
	require.Equal(t, "gpt-test", candidateAttempt.RealModelName)
	require.Equal(t, "default", candidateAttempt.GroupName)
	require.Equal(t, 0, candidateAttempt.LoopRoundsCompleted)
}

// TestGetActiveVirtualModelCandidateAttemptTracksCandidateHandoff 验证候选切换后返回的是新候选而不是旧候选喵。
func TestGetActiveVirtualModelCandidateAttemptTracksCandidateHandoff(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 先激活第一个内部候选，模拟首次尝试喵。
	executionState := newVirtualModelAttemptTestState()
	executionState.currentCandidateIndex = 0
	executionState.currentCandidateAttemptID = "vc71a1"
	executionState.candidateAttemptSequence = 1
	ctx := newVirtualModelAttemptTestContext(executionState)

	firstCandidateAttempt, foundFirstAttempt := GetActiveVirtualModelCandidateAttempt(ctx)
	require.True(t, foundFirstAttempt)
	require.Equal(t, 71, firstCandidateAttempt.CandidateID)

	// 模拟第一个候选失败后切换到第二个自定义候选，尝试序号必须继续递增喵。
	executionState.currentCandidateIndex = 1
	executionState.candidateAttemptSequence = 2
	executionState.currentCandidateAttemptID = "vc72a2"
	executionState.loopRoundsCompleted = 1

	secondCandidateAttempt, foundSecondAttempt := GetActiveVirtualModelCandidateAttempt(ctx)
	require.True(t, foundSecondAttempt)
	require.Equal(t, 72, secondCandidateAttempt.CandidateID)
	// 两次尝试标识必须不同，否则候选级计费幂等键会互相覆盖导致重复扣费或漏退款喵。
	require.NotEqual(t, firstCandidateAttempt.CandidateAttemptID, secondCandidateAttempt.CandidateAttemptID)
	require.Equal(t, "vc72a2", secondCandidateAttempt.CandidateAttemptID)
	require.Equal(t, 2, secondCandidateAttempt.AttemptSequence)
	// 自定义候选不走内部 relay，来源类型必须如实返回以便 controller 拒绝为其建立内部计费喵。
	require.Equal(t, model.VirtualModelSourceCustom, secondCandidateAttempt.SourceType)
	require.Equal(t, 1, secondCandidateAttempt.LoopRoundsCompleted)
}

// TestGetActiveVirtualModelCandidateAttemptRejectsCorruptedStateValue 验证上下文键被其他类型污染时安全降级喵。
func TestGetActiveVirtualModelCandidateAttemptRejectsCorruptedStateValue(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 喵~防御：中间件顺序错误可能让同名上下文键存入非执行状态类型，此时必须返回 false 而不是断言 panic 喵。
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyVirtualModelExecutionState, "not-an-execution-state")

	_, foundCandidateAttempt := GetActiveVirtualModelCandidateAttempt(ctx)
	require.False(t, foundCandidateAttempt)
}
