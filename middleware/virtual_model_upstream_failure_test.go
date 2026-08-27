package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	virtualmodelservice "github.com/QuantumNous/new-api/service/virtualmodel"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newUpstreamFailureTestState 构造带内部候选与失败规则的 user/xxx 候选执行状态喵。
func newUpstreamFailureTestState(candidates []model.VirtualModelCandidateSnapshot, rules map[int][]model.VirtualModelFailureRule, globalRules []model.VirtualModelFailureRule) *virtualModelExecutionState {
	return &virtualModelExecutionState{
		virtualModelName: "vm-test",
		virtualModelID:   9,
		ownerUserID:      7,
		executionSnapshot: &model.VirtualModelExecutionSnapshot{
			Candidates:                candidates,
			FailureRulesByCandidateID: rules,
			GlobalFailureRules:        globalRules,
		},
		originalRequestBody:             []byte(`{"model":"vm-test","messages":[{"role":"user","content":"hi"}]}`),
		modelRequest:                    &ModelRequest{Model: "vm-test", Group: "default"},
		currentCandidateIndex:           0,
		ruleRetryCounts:                 make(map[int]int),
		internalFreezeStatesByCandidate: make(map[int]model.VirtualModelInternalFreezeState),
		skippedCandidateIDs:             make(map[int]bool),
	}
}

// newUpstreamFailureTestContext 构造带 JSON 请求、虚拟执行状态与候选尝试切片的测试上下文喵。
// 返回 recorder 供断言透传响应状态码与正文喵。
func newUpstreamFailureTestContext(executionState *virtualModelExecutionState) (*gin.Context, *[]model.VirtualModelCandidateAttemptRecord, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"vm-test","messages":[{"role":"user","content":"hi"}]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", 7)
	common.SetContextKey(ctx, constant.ContextKeyVirtualModelExecutionState, executionState)
	// 候选分组 default 属于当前用户可访问范围，供候选切换校验放行喵。
	common.SetContextKey(ctx, constant.ContextKeyUserGroupAccess, service.UserGroupAccess{UsableGroups: map[string]string{"default": "default"}, AutoGroups: []string{}})
	// 初始化候选尝试切片，供失败尝试摘要写入与断言喵。
	attempts := make([]model.VirtualModelCandidateAttemptRecord, 0, 4)
	common.SetContextKey(ctx, constant.ContextKeyVirtualCandidateAttempts, &attempts)
	return ctx, &attempts, recorder
}

// newUpstreamFailure 构造一次上游 401 无效凭据的结构化失败喵。
func newUpstreamFailure() *virtualmodelservice.CustomCandidateExecutionFailure {
	return &virtualmodelservice.CustomCandidateExecutionFailure{
		Failure:         virtualmodelservice.NormalizeCandidateFailure(http.StatusUnauthorized, nil, []byte(`{"error":"invalid token"}`), nil),
		ResponseHeaders: http.Header{},
		ResponseBody:    []byte(`{"error":"invalid token"}`),
	}
}

// TestHandleVirtualModelUpstreamFailureNext 验证 user/xxx 候选失败默认切换下一候选喵。
func TestHandleVirtualModelUpstreamFailureNext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 无任何规则命中时默认动作是 next，候选链应推进到第二个 user/xxx 候选喵。
	candidates := []model.VirtualModelCandidateSnapshot{
		{CandidateID: 71, VirtualModelID: 9, StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true, MaxRetries: 2, GroupName: "default", RealModelName: "user/upstream-a"},
		{CandidateID: 72, VirtualModelID: 9, StableOrder: 1, SourceType: model.VirtualModelSourceInternal, Enabled: true, MaxRetries: 2, GroupName: "default", RealModelName: "user/upstream-b"},
	}
	executionState := newUpstreamFailureTestState(candidates, nil, nil)
	ctx, attempts, _ := newUpstreamFailureTestContext(executionState)

	outcome := handleVirtualModelUpstreamFailure(ctx, nil, false, newUpstreamFailure(), time.Now())
	require.Equal(t, virtualModelUpstreamFailureNext, outcome)
	// 候选链推进到候选 2，模型名与分组改写为下一个 user/xxx 候选喵。
	require.Equal(t, 1, executionState.currentCandidateIndex)
	require.Equal(t, "user/upstream-b", executionState.modelRequest.Model)
	require.Equal(t, "default", executionState.modelRequest.Group)
	// 失败尝试摘要已记录，供日志展示候选链故障转移喵。
	require.Len(t, *attempts, 1)
	require.False(t, (*attempts)[0].Success)
	require.Equal(t, 71, (*attempts)[0].CandidateID)
	require.Equal(t, "upstream_client_error", (*attempts)[0].ErrorClass)
	require.Contains(t, (*attempts)[0].ErrorBody, "invalid token")
}

// TestHandleVirtualModelUpstreamFailureRetry 验证 user/xxx 候选失败规则 retry 在 MaxRetries 内重放喵。
func TestHandleVirtualModelUpstreamFailureRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	retryRules := map[int][]model.VirtualModelFailureRule{
		71: {{HTTPStatus: http.StatusUnauthorized, Action: model.VirtualModelActionRetry}},
	}
	candidates := []model.VirtualModelCandidateSnapshot{
		{CandidateID: 71, VirtualModelID: 9, StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true, MaxRetries: 2, GroupName: "default", RealModelName: "user/upstream-a"},
	}
	executionState := newUpstreamFailureTestState(candidates, retryRules, nil)
	ctx, _, _ := newUpstreamFailureTestContext(executionState)

	// 前两次失败必须返回 retry，且重试计数递增喵。
	firstOutcome := handleVirtualModelUpstreamFailure(ctx, nil, false, newUpstreamFailure(), time.Now())
	require.Equal(t, virtualModelUpstreamFailureRetry, firstOutcome)
	require.Equal(t, 1, executionState.ruleRetryCounts[71])
	secondOutcome := handleVirtualModelUpstreamFailure(ctx, nil, false, newUpstreamFailure(), time.Now())
	require.Equal(t, virtualModelUpstreamFailureRetry, secondOutcome)
	require.Equal(t, 2, executionState.ruleRetryCounts[71])

	// 第三次失败超出 MaxRetries，retry 不可继续且无后续候选可切，返回 end 喵。
	thirdOutcome := handleVirtualModelUpstreamFailure(ctx, nil, false, newUpstreamFailure(), time.Now())
	require.Equal(t, virtualModelUpstreamFailureEnd, thirdOutcome)
}

// TestHandleVirtualModelUpstreamFailurePassthrough 验证 user/xxx 候选失败规则 passthrough 透传上游错误喵。
func TestHandleVirtualModelUpstreamFailurePassthrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	passthroughRules := map[int][]model.VirtualModelFailureRule{
		71: {{HTTPStatus: http.StatusUnauthorized, Action: model.VirtualModelActionPassthrough}},
	}
	candidates := []model.VirtualModelCandidateSnapshot{
		{CandidateID: 71, VirtualModelID: 9, StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true, MaxRetries: 2, GroupName: "default", RealModelName: "user/upstream-a"},
	}
	executionState := newUpstreamFailureTestState(candidates, passthroughRules, nil)
	ctx, _, recorder := newUpstreamFailureTestContext(executionState)

	outcome := handleVirtualModelUpstreamFailure(ctx, nil, false, newUpstreamFailure(), time.Now())
	require.Equal(t, virtualModelUpstreamFailureEnd, outcome)
	// 透传后响应状态码为上游 401，正文原样回传喵。
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Contains(t, recorder.Body.String(), "invalid token")
	require.Equal(t, http.StatusUnauthorized, ctx.Writer.Status())
}

// TestHandleVirtualModelUpstreamFailureGlobalFallback 验证候选无规则时回退全局兜底规则喵。
func TestHandleVirtualModelUpstreamFailureGlobalFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 候选 71 未配置规则，全局兜底规则匹配 401 时按 passthrough 处理喵。
	globalRules := []model.VirtualModelFailureRule{
		{HTTPStatus: http.StatusUnauthorized, Action: model.VirtualModelActionPassthrough},
	}
	candidates := []model.VirtualModelCandidateSnapshot{
		{CandidateID: 71, VirtualModelID: 9, StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true, MaxRetries: 1, GroupName: "default", RealModelName: "user/upstream-a"},
	}
	executionState := newUpstreamFailureTestState(candidates, nil, globalRules)
	ctx, _, recorder := newUpstreamFailureTestContext(executionState)

	outcome := handleVirtualModelUpstreamFailure(ctx, nil, false, newUpstreamFailure(), time.Now())
	require.Equal(t, virtualModelUpstreamFailureEnd, outcome)
	// 全局兜底 passthrough 生效，上游 401 原样回传喵。
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

// TestHandleVirtualModelUpstreamFailureFreeze 验证 user/xxx 候选失败规则 freeze 持久化冻结状态喵。
func TestHandleVirtualModelUpstreamFailureFreeze(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 冻结断言需要持久化表，同时迁移候选探测表避免失败探测写库报错噪音喵。
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	oldDB := model.DB
	model.DB = testDB
	defer func() { model.DB = oldDB }()
	require.NoError(t, testDB.AutoMigrate(&model.VirtualModelInternalFreezeState{}, &model.EntityProbeState{}))

	freezeRules := map[int][]model.VirtualModelFailureRule{
		71: {{HTTPStatus: http.StatusUnauthorized, Action: model.VirtualModelActionFreeze, FreezeSeconds: 30}},
	}
	candidates := []model.VirtualModelCandidateSnapshot{
		{CandidateID: 71, VirtualModelID: 9, StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true, MaxRetries: 1, GroupName: "default", RealModelName: "user/upstream-a"},
	}
	executionState := newUpstreamFailureTestState(candidates, freezeRules, nil)
	ctx, _, _ := newUpstreamFailureTestContext(executionState)

	outcome := handleVirtualModelUpstreamFailure(ctx, nil, false, newUpstreamFailure(), time.Now())
	// 单候选链 freeze 后无后续候选可切，返回 end 喵。
	require.Equal(t, virtualModelUpstreamFailureEnd, outcome)
	// 内存快照必须记录冻结，使本次请求后续激活立即跳过该候选喵。
	freezeState, wasFrozen := executionState.internalFreezeStatesByCandidate[71]
	require.True(t, wasFrozen)
	require.Positive(t, freezeState.FrozenUntil)
	// 冻结状态必须持久化到数据库喵。
	var storedState model.VirtualModelInternalFreezeState
	require.NoError(t, testDB.Where("owner_user_id = ? AND candidate_id = ?", 7, 71).First(&storedState).Error)
	require.Positive(t, storedState.FrozenUntil)
}

// TestVirtualModelUpstreamRetryDelayDeadline 验证 retry 退避服从总 deadline 喵。
func TestVirtualModelUpstreamRetryDelayDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	candidates := []model.VirtualModelCandidateSnapshot{
		{CandidateID: 71, VirtualModelID: 9, StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true, MaxRetries: 2, GroupName: "default", RealModelName: "user/upstream-a"},
	}
	// 总 deadline 已过去：不允许继续重试喵。
	expiredState := newUpstreamFailureTestState(candidates, nil, nil)
	expiredState.requestDeadline = time.Now().Add(-time.Second)
	expiredCtx, _, _ := newUpstreamFailureTestContext(expiredState)
	_, canRetry := virtualModelUpstreamRetryDelay(expiredCtx)
	require.False(t, canRetry)

	// 总 deadline 充足且未记录重试：首次退避为 1 秒喵。
	livingState := newUpstreamFailureTestState(candidates, nil, nil)
	livingState.requestDeadline = time.Now().Add(30 * time.Second)
	livingCtx, _, _ := newUpstreamFailureTestContext(livingState)
	delay, canRetry := virtualModelUpstreamRetryDelay(livingCtx)
	require.True(t, canRetry)
	require.Equal(t, 1*time.Second, delay)
}
