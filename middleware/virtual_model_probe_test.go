package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newProbeTestDB 构造带状态表的内存库并替换全局 DB 喵。
func newProbeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, testDB.AutoMigrate(&model.EntityProbeState{}))
	oldDB := model.DB
	model.DB = testDB
	t.Cleanup(func() {
		model.DB = oldDB
	})
	return testDB
}

// newProbeTestState 构造带状态字段的虚拟模型执行状态喵。
func newProbeTestState() *virtualModelExecutionState {
	return &virtualModelExecutionState{
		virtualModelName:          "vm-probe",
		virtualModelID:            9,
		ownerUserID:               7,
		executionSnapshot:         &model.VirtualModelExecutionSnapshot{},
		currentCandidateIndex:     -1,
		skippedCandidateIDs:       make(map[int]bool),
		ruleRetryCounts:           make(map[int]int),
		startTime:                 time.Now(),
		currentCandidateStartedAt: time.Now(),
	}
}

// TestRecordVirtualModelOverallProbe 验证整体状态记录与去重喵。
func TestRecordVirtualModelOverallProbe(t *testing.T) {
	newProbeTestDB(t)
	executionState := newProbeTestState()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyVirtualModelExecutionState, executionState)

	// 记录一次整体成功喵。
	RecordVirtualModelOverallProbe(ctx, true, "")
	state, stateErr := model.GetEntityProbeState(model.EntityProbeScopeVirtual, 9)
	require.NoError(t, stateErr)
	require.Equal(t, int64(1), state.RequestCount)
	require.Equal(t, int64(1), state.SuccessCount)
	require.True(t, state.LastSuccess)

	// 再次记录失败应被去重，不新增样本喵。
	RecordVirtualModelOverallProbe(ctx, false, "upstream_unavailable")
	state, stateErr = model.GetEntityProbeState(model.EntityProbeScopeVirtual, 9)
	require.NoError(t, stateErr)
	require.Equal(t, int64(1), state.RequestCount)
	require.True(t, state.LastSuccess)
}

// TestRecordVirtualModelProbeSuccessCarriesUsage 验证成功探测携带 usage/TTFT 喵。
func TestRecordVirtualModelProbeSuccessCarriesUsage(t *testing.T) {
	newProbeTestDB(t)
	executionState := newProbeTestState()
	executionState.executionSnapshot.Candidates = []model.VirtualModelCandidateSnapshot{
		{CandidateID: 71, VirtualModelID: 9, RealModelName: "gpt-probe"},
	}
	executionState.currentCandidateIndex = 0
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyVirtualModelExecutionState, executionState)

	// 传入带 token 与缓存命中的 usage，以及首字耗时喵。
	usage := &dto.Usage{PromptTokens: 100, CompletionTokens: 20, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 30}}
	recordVirtualModelProbeSuccess(ctx, executionState, 71, usage, 88)

	// 成功样本应携带输入/输出/缓存 token 与 TTFT 喵。
	require.Equal(t, int64(100), executionState.successExtras.InputTokens)
	require.Equal(t, int64(20), executionState.successExtras.OutputTokens)
	require.Equal(t, int64(30), executionState.successExtras.CachedTokens)
	require.Equal(t, int64(88), executionState.successExtras.TtftMs)
	require.True(t, executionState.successExtras.HasTtft)

	// 候选与整体状态行应记录成功喵。
	candidateState, candidateErr := model.GetEntityProbeState(model.EntityProbeScopeVirtualCandidate, 71)
	require.NoError(t, candidateErr)
	require.True(t, candidateState.LastSuccess)
	overallState, overallErr := model.GetEntityProbeState(model.EntityProbeScopeVirtual, 9)
	require.NoError(t, overallErr)
	require.True(t, overallState.LastSuccess)
}

// TestApplyVirtualModelSuccessProbe 验证从 context 读取 usage 与 TTFT 填充成功样本喵。
func TestApplyVirtualModelSuccessProbe(t *testing.T) {
	newProbeTestDB(t)
	executionState := newProbeTestState()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyVirtualModelExecutionState, executionState)

	// 无 usage 时 TTFT 仍可填充喵。
	ApplyVirtualModelSuccessProbe(ctx, 77)
	require.Equal(t, int64(77), executionState.successExtras.TtftMs)
	require.True(t, executionState.successExtras.HasTtft)

	// 带 usage 的 context 覆盖 token 喵。
	common.SetContextKey(ctx, constant.ContextKeyVirtualModelSuccessUsage, &dto.Usage{PromptTokens: 50, CompletionTokens: 5, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 2}})
	ApplyVirtualModelSuccessProbe(ctx, 0)
	require.Equal(t, int64(50), executionState.successExtras.InputTokens)
	require.Equal(t, int64(5), executionState.successExtras.OutputTokens)
	require.Equal(t, int64(2), executionState.successExtras.CachedTokens)
}

// TestRecordVirtualModelCandidateProbe 验证候选节点状态记录喵。
func TestRecordVirtualModelCandidateProbe(t *testing.T) {
	newProbeTestDB(t)
	executionState := newProbeTestState()
	// 模拟已激活候选 71 喵。
	executionState.executionSnapshot.Candidates = []model.VirtualModelCandidateSnapshot{
		{CandidateID: 71, VirtualModelID: 9, RealModelName: "gpt-probe"},
	}
	executionState.currentCandidateIndex = 0
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyVirtualModelExecutionState, executionState)

	RecordActiveVirtualModelCandidateProbe(ctx, false, "rate_limited")

	state, stateErr := model.GetEntityProbeState(model.EntityProbeScopeVirtualCandidate, 71)
	require.NoError(t, stateErr)
	require.Equal(t, int64(9), state.VirtualID)
	require.Equal(t, int64(1), state.RequestCount)
	require.Equal(t, int64(0), state.SuccessCount)
	require.False(t, state.LastSuccess)
	require.Equal(t, "rate_limited", state.LastError)
}

// TestAdvanceVirtualModelAfterNativeFailureRecordsProbe 验证内部候选失败终局记录候选与整体失败喵。
func TestAdvanceVirtualModelAfterNativeFailureRecordsProbe(t *testing.T) {
	newProbeTestDB(t)
	// 复用原生失败测试的状态构造：单内部候选 + retry 规则喵。
	retryRules := map[int][]model.VirtualModelFailureRule{
		71: {{HTTPStatus: http.StatusServiceUnavailable, Action: model.VirtualModelActionRetry}},
	}
	executionState := newNativeFailureTestState(retryRules)
	executionState.virtualModelID = 9
	executionState.startTime = time.Now()
	executionState.currentCandidateStartedAt = time.Now()
	ctx := newNativeFailureTestContext(executionState)

	// 前两次失败触发 retry 喵。
	require.True(t, AdvanceVirtualModelAfterNativeFailure(ctx, newNativeFailureTestError()).RetryCurrentCandidate)
	require.True(t, AdvanceVirtualModelAfterNativeFailure(ctx, newNativeFailureTestError()).RetryCurrentCandidate)

	// 第三次失败超出 MaxRetries 且无后备候选：候选与整体都应记录失败喵。
	thirdDecision := AdvanceVirtualModelAfterNativeFailure(ctx, newNativeFailureTestError())
	require.False(t, thirdDecision.RetryCurrentCandidate)
	require.False(t, thirdDecision.NextCandidateActivated)

	candidateState, candidateErr := model.GetEntityProbeState(model.EntityProbeScopeVirtualCandidate, 71)
	require.NoError(t, candidateErr)
	require.Equal(t, int64(1), candidateState.RequestCount)
	require.False(t, candidateState.LastSuccess)

	overallState, overallErr := model.GetEntityProbeState(model.EntityProbeScopeVirtual, 9)
	require.NoError(t, overallErr)
	require.Equal(t, int64(1), overallState.RequestCount)
	require.False(t, overallState.LastSuccess)
}
