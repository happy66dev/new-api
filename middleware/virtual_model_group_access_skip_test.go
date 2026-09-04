package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// enableVirtualModelTestOption 测试期间开启虚拟模型功能开关，结束后还原喵。
func enableVirtualModelTestOption(t *testing.T) {
	t.Helper()
	// 喵~防御：OptionMap 可能尚未初始化，先补默认空映射避免空指针喵。
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	oldOption := common.OptionMap["VirtualModelEnabled"]
	common.OptionMap["VirtualModelEnabled"] = "true"
	t.Cleanup(func() { common.OptionMap["VirtualModelEnabled"] = oldOption })
}

// newGroupAccessSessionContext 构造带会话身份 id=7 的 JSON 请求上下文，并把调用者可用分组注入缓存喵。
// usableGroups 只含 default，模拟该用户已失去特殊可用分组 vip 喵。
func newGroupAccessSessionContext(modelName string) (*gin.Context, *httptest.ResponseRecorder) {
	// gin.CreateTestContext 第二个返回是 engine，响应记录器需由调用方先构造并持有喵。
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	body := `{"model":"` + modelName + `","messages":[{"role":"user","content":"hi"}]}`
	ctx.Request = httptest.NewRequest(http.MethodPost, "/pg/chat/completions", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", 7)
	// 可用分组只含 default：特殊可用分组 vip 已因用户分组改变而丢失喵。
	common.SetContextKey(ctx, constant.ContextKeyUserGroupAccess, service.UserGroupAccess{UsableGroups: map[string]string{"default": "默认分组"}, AutoGroups: []string{}})
	return ctx, recorder
}

// TestHandleVirtualModelRequestSkipsLostGroupInternalCandidate 验证失去特殊可用分组的内部候选
// 在请求时被当作链上不存在（跳过），由后续仍有权限的候选接管喵。
func TestHandleVirtualModelRequestSkipsLostGroupInternalCandidate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 复用会话态测试的完整虚拟模型表结构内存库喵。
	testDB := newSessionVirtualModelTestDB(t)
	oldDB := model.DB
	model.DB = testDB
	defer func() { model.DB = oldDB }()
	enableVirtualModelTestOption(t)

	// 候选链：vip 分组（曾因特殊可用分组规则可用，现已失去）在前，default 分组在后喵。
	virtualModel := &model.VirtualModel{OwnerUserID: 7, NormalizedName: "g-access", Enabled: true}
	require.NoError(t, testDB.Create(virtualModel).Error)
	lostGroupCandidate := &model.VirtualModelCandidate{VirtualModelID: virtualModel.ID, StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true}
	require.NoError(t, testDB.Create(lostGroupCandidate).Error)
	require.NoError(t, testDB.Create(&model.VirtualModelInternalCandidate{CandidateID: lostGroupCandidate.ID, GroupName: "vip", RealModelName: "gpt-blocked"}).Error)
	validCandidate := &model.VirtualModelCandidate{VirtualModelID: virtualModel.ID, StableOrder: 1, SourceType: model.VirtualModelSourceInternal, Enabled: true}
	require.NoError(t, testDB.Create(validCandidate).Error)
	require.NoError(t, testDB.Create(&model.VirtualModelInternalCandidate{CandidateID: validCandidate.ID, GroupName: "default", RealModelName: "gpt-4o"}).Error)

	// 构造会话请求并注入仅含 default 的可用分组喵。
	ctx, _ := newGroupAccessSessionContext("virtual/g-access")

	// 无权候选 vip 必须被跳过，default 候选接管并改写请求喵。
	activated := handleVirtualModelRequest(ctx, &ModelRequest{Model: "virtual/g-access"})
	require.True(t, activated)
	// 顶层 model 改写为 default 候选的真实模型，说明 vip 候选未被激活喵。
	require.Equal(t, "gpt-4o", ctx.GetString("original_model"))
	// 当前候选索引应落在第二个候选（index=1）喵。
	executionState, foundState := getVirtualModelExecutionState(ctx)
	require.True(t, foundState)
	require.Equal(t, 1, executionState.currentCandidateIndex)
}

// TestActivateNextVirtualModelCandidateAllCandidatesLostGroup 验证全部内部候选都失去可用分组时，
// 激活推进返回 false 且不落到任何候选（即不会走到 403 越权 abort，交由链耗尽兜底）喵。
func TestActivateNextVirtualModelCandidateAllCandidatesLostGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newGroupAccessSessionContext("virtual/g-all-lost")

	// 链上只有一个 vip 分组候选，而调用者可用分组只剩 default 喵。
	executionState := &virtualModelExecutionState{
		virtualModelName: "g-all-lost",
		virtualModelID:   1,
		executionSnapshot: &model.VirtualModelExecutionSnapshot{
			Candidates: []model.VirtualModelCandidateSnapshot{
				{CandidateID: 91, VirtualModelID: 1, StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true, GroupName: "vip", RealModelName: "gpt-blocked"},
			},
			FailureRulesByCandidateID: make(map[int][]model.VirtualModelFailureRule),
			GlobalFailureRules:        []model.VirtualModelFailureRule{},
		},
		manualFrozenCandidateIDs:        make(map[int]bool),
		internalFreezeStatesByCandidate: make(map[int]model.VirtualModelInternalFreezeState),
		ruleRetryCounts:                 make(map[int]int),
		currentCandidateIndex:           -1,
		skippedCandidateIDs:             make(map[int]bool),
	}
	common.SetContextKey(ctx, constant.ContextKeyVirtualModelExecutionState, executionState)

	// 唯一候选被跳过，激活推进失败且不推进索引喵。
	activated := activateNextVirtualModelCandidate(ctx, executionState)
	require.False(t, activated)
	require.Equal(t, -1, executionState.currentCandidateIndex)
	require.Equal(t, "", executionState.currentCandidateAttemptID)
}
