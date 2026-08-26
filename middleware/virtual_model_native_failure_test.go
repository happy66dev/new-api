package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newNativeFailureTestContext 构造带 JSON 请求与虚拟模型执行状态的测试上下文喵。
func newNativeFailureTestContext(executionState *virtualModelExecutionState) *gin.Context {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"vm-test","messages":[{"role":"user","content":"hi"}]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(ctx, constant.ContextKeyVirtualModelExecutionState, executionState)
	return ctx
}

// newNativeFailureTestState 构造单个内部候选与失败规则的执行状态喵。
func newNativeFailureTestState(rules map[int][]model.VirtualModelFailureRule) *virtualModelExecutionState {
	return &virtualModelExecutionState{
		virtualModelName: "vm-test",
		ownerUserID:      7,
		executionSnapshot: &model.VirtualModelExecutionSnapshot{
			Candidates: []model.VirtualModelCandidateSnapshot{
				{CandidateID: 71, VirtualModelID: 9, StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true, MaxRetries: 2, GroupName: "default", RealModelName: "gpt-test"},
			},
			FailureRulesByCandidateID: rules,
		},
		originalRequestBody:             []byte(`{"model":"vm-test","messages":[{"role":"user","content":"hi"}]}`),
		modelRequest:                    &ModelRequest{Model: "vm-test", Group: "default"},
		currentCandidateIndex:           0,
		ruleRetryCounts:                 make(map[int]int),
		internalFreezeStatesByCandidate: make(map[int]model.VirtualModelInternalFreezeState),
		skippedCandidateIDs:             make(map[int]bool),
	}
}

// newNativeFailureTestError 构造一次上游 503 失败的受限错误喵。
func newNativeFailureTestError() *types.NewAPIError {
	return types.NewErrorWithStatusCode(errors.New("upstream failure"), types.ErrorCode("upstream_server_error"), http.StatusServiceUnavailable)
}

// TestAdvanceVirtualModelAfterNativeFailureRetry 验证内部候选失败规则 retry 在 MaxRetries 内重放当前候选喵。
func TestAdvanceVirtualModelAfterNativeFailureRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 候选 71 配置 retry 失败规则，MaxRetries 为 2 喵。
	retryRules := map[int][]model.VirtualModelFailureRule{
		71: {{HTTPStatus: http.StatusServiceUnavailable, Action: model.VirtualModelActionRetry}},
	}
	executionState := newNativeFailureTestState(retryRules)
	ctx := newNativeFailureTestContext(executionState)

	// 前两次失败必须重放当前内部候选喵。
	firstDecision := AdvanceVirtualModelAfterNativeFailure(ctx, newNativeFailureTestError())
	require.True(t, firstDecision.RetryCurrentCandidate)
	require.Equal(t, 1, executionState.ruleRetryCounts[71])
	secondDecision := AdvanceVirtualModelAfterNativeFailure(ctx, newNativeFailureTestError())
	require.True(t, secondDecision.RetryCurrentCandidate)
	require.Equal(t, 2, executionState.ruleRetryCounts[71])

	// 第三次失败超出 MaxRetries，retry 不再允许且无后续候选可切喵。
	thirdDecision := AdvanceVirtualModelAfterNativeFailure(ctx, newNativeFailureTestError())
	require.False(t, thirdDecision.RetryCurrentCandidate)
	require.False(t, thirdDecision.NextCandidateActivated)
}

// TestAdvanceVirtualModelAfterNativeFailureFreeze 验证内部候选失败规则 freeze 写入冻结状态并跳过候选喵。
func TestAdvanceVirtualModelAfterNativeFailureFreeze(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 中间件测试库没有 TestMain，为冻结断言临时准备独立数据库喵。
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	oldDB := model.DB
	model.DB = testDB
	defer func() { model.DB = oldDB }()
	require.NoError(t, testDB.AutoMigrate(&model.VirtualModelInternalFreezeState{}))

	// 候选 71 配置 freeze 失败规则，冻结时长为 30 秒喵。
	freezeRules := map[int][]model.VirtualModelFailureRule{
		71: {{HTTPStatus: http.StatusServiceUnavailable, Action: model.VirtualModelActionFreeze, FreezeSeconds: 30}},
	}
	executionState := newNativeFailureTestState(freezeRules)
	ctx := newNativeFailureTestContext(executionState)

	decision := AdvanceVirtualModelAfterNativeFailure(ctx, newNativeFailureTestError())
	// 单个候选链无后续候选，freeze 后不再激活下一候选喵。
	require.False(t, decision.RetryCurrentCandidate)
	require.False(t, decision.NextCandidateActivated)
	// 内存快照必须记录冻结，使本次请求后续激活立即跳过该候选喵。
	freezeState, wasFrozen := executionState.internalFreezeStatesByCandidate[71]
	require.True(t, wasFrozen)
	require.Positive(t, freezeState.FrozenUntil)
	// 冻结状态必须持久化到数据库喵。
	var storedState model.VirtualModelInternalFreezeState
	require.NoError(t, testDB.Where("owner_user_id = ? AND candidate_id = ?", 7, 71).First(&storedState).Error)
	require.Positive(t, storedState.FrozenUntil)
}

// TestAdvanceVirtualModelAfterNativeFailureSafeGuards 验证内部候选失败编排的防御边界喵。
func TestAdvanceVirtualModelAfterNativeFailureSafeGuards(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 空上下文直接返回空决策喵。
	emptyDecision := AdvanceVirtualModelAfterNativeFailure(nil, newNativeFailureTestError())
	require.False(t, emptyDecision.RetryCurrentCandidate)
	require.False(t, emptyDecision.NextCandidateActivated)

	// 失败规则 passthrough 时不做任何候选切换喵。
	passthroughRules := map[int][]model.VirtualModelFailureRule{
		71: {{HTTPStatus: http.StatusServiceUnavailable, Action: model.VirtualModelActionPassthrough}},
	}
	passthroughDecision := AdvanceVirtualModelAfterNativeFailure(newNativeFailureTestContext(newNativeFailureTestState(passthroughRules)), newNativeFailureTestError())
	require.False(t, passthroughDecision.RetryCurrentCandidate)
	require.False(t, passthroughDecision.NextCandidateActivated)
}
