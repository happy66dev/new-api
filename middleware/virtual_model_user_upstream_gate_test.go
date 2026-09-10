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
	"gorm.io/gorm"
)

// setUserUpstreamMasterOptionForTest 测试期间把总开关(UserUpstreamEnabled)置为指定值并在结束后恢复喵。
func setUserUpstreamMasterOptionForTest(t *testing.T, enabled string) {
	t.Helper()
	// 喵~防御：OptionMap 可能尚未初始化，先补默认空映射避免写空指针喵。
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	oldMaster := common.OptionMap[model.UserUpstreamEnabledKey]
	common.OptionMap[model.UserUpstreamEnabledKey] = enabled
	t.Cleanup(func() { common.OptionMap[model.UserUpstreamEnabledKey] = oldMaster })
}

// newUserUpstreamGateSessionContext 构造带会话身份 id=7 且可用分组含 default 的虚拟模型请求上下文喵。
func newUserUpstreamGateSessionContext(modelName string) (*gin.Context, *httptest.ResponseRecorder) {
	// gin.CreateTestContext 第二个返回是 engine，响应记录器需由调用方先构造并持有喵。
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	body := `{"model":"` + modelName + `","messages":[{"role":"user","content":"hi"}]}`
	ctx.Request = httptest.NewRequest(http.MethodPost, "/pg/chat/completions", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", 7)
	// 可用分组含 default：内部候选按 default 分组可正常接管喵。
	common.SetContextKey(ctx, constant.ContextKeyUserGroupAccess, service.UserGroupAccess{UsableGroups: map[string]string{"default": "默认分组"}, AutoGroups: []string{}})
	return ctx, recorder
}

// seedDirectFillCustomCandidate 构造一个直填加密凭据的自定义候选并落库，返回候选记录喵。
func seedDirectFillCustomCandidate(t *testing.T, db *gorm.DB, virtualModelID int, stableOrder int) *model.VirtualModelCandidate {
	t.Helper()
	// 直填候选不引用用户上游条目，凭据以本表密文为准喵。
	candidate := &model.VirtualModelCandidate{VirtualModelID: virtualModelID, StableOrder: stableOrder, SourceType: model.VirtualModelSourceCustom, Enabled: true}
	require.NoError(t, db.Create(candidate).Error)
	require.NoError(t, db.Create(&model.VirtualModelCustomCandidate{
		CandidateID: candidate.ID, EncryptedBaseURL: "encrypted-direct-base-url",
		EncryptedAPIKey: "encrypted-direct-api-key", RealModelName: "gpt-direct", AuthStyle: model.VirtualModelAuthBearer,
	}).Error)
	return candidate
}

// TestHandleVirtualModelRequestSkipsDirectFillCandidateWhenMasterDisabled 验证总开关关闭时，
// 直填自定义候选在调用链上被跳过（仅拦截不删除），由后续内部候选接管喵。
func TestHandleVirtualModelRequestSkipsDirectFillCandidateWhenMasterDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 复用会话态测试的完整虚拟模型表结构内存库喵。
	testDB := newSessionVirtualModelTestDB(t)
	oldDB := model.DB
	model.DB = testDB
	defer func() { model.DB = oldDB }()
	enableVirtualModelTestOption(t)
	// 总开关关闭：存量直填候选必须被拦截，不允许其外发请求喵。
	setUserUpstreamMasterOptionForTest(t, "false")

	// 候选链：直填自定义候选在前，default 内部候选在后喵。
	virtualModel := &model.VirtualModel{OwnerUserID: 7, NormalizedName: "gate-skip", Enabled: true}
	require.NoError(t, testDB.Create(virtualModel).Error)
	seedDirectFillCustomCandidate(t, testDB, virtualModel.ID, 0)
	fallbackCandidate := &model.VirtualModelCandidate{VirtualModelID: virtualModel.ID, StableOrder: 1, SourceType: model.VirtualModelSourceInternal, Enabled: true}
	require.NoError(t, testDB.Create(fallbackCandidate).Error)
	require.NoError(t, testDB.Create(&model.VirtualModelInternalCandidate{CandidateID: fallbackCandidate.ID, GroupName: "default", RealModelName: "gpt-4o"}).Error)

	// 构造会话请求并注入可用分组喵。
	ctx, _ := newUserUpstreamGateSessionContext("virtual/gate-skip")

	// 直填候选被跳过，内部候选接管并改写请求喵。
	activated := handleVirtualModelRequest(ctx, &ModelRequest{Model: "virtual/gate-skip"})
	require.True(t, activated)
	// 顶层 model 改写为内部候选的真实模型，说明直填候选未被激活喵。
	require.Equal(t, "gpt-4o", ctx.GetString("original_model"))
	// 当前候选索引应落在第二个候选（index=1）喵。
	executionState, foundState := getVirtualModelExecutionState(ctx)
	require.True(t, foundState)
	require.Equal(t, 1, executionState.currentCandidateIndex)
}

// TestActivateNextVirtualModelCandidateAllCustomCandidatesDisabled 验证总开关关闭且链上只有
// 直填自定义候选时，候选链耗尽且不落到任何候选（交由链耗尽兜底报不可用）喵。
func TestActivateNextVirtualModelCandidateAllCustomCandidatesDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newUserUpstreamGateSessionContext("virtual/gate-all-custom")
	setUserUpstreamMasterOptionForTest(t, "false")

	// 链上只有一个直填自定义候选，而总开关已关闭喵。
	executionState := &virtualModelExecutionState{
		virtualModelName: "gate-all-custom",
		virtualModelID:   1,
		executionSnapshot: &model.VirtualModelExecutionSnapshot{
			Candidates: []model.VirtualModelCandidateSnapshot{
				{CandidateID: 92, VirtualModelID: 1, StableOrder: 0, SourceType: model.VirtualModelSourceCustom, Enabled: true, RealModelName: "gpt-direct", EncryptedBaseURL: "enc", EncryptedAPIKey: "enc", AuthStyle: model.VirtualModelAuthBearer},
			},
			FailureRulesByCandidateID: make(map[int][]model.VirtualModelFailureRule),
			GlobalFailureRules:        []model.VirtualModelFailureRule{},
		},
		manualFrozenCandidateIDs:        make(map[int]bool),
		automaticFreezeStatesByIdentity: make(map[string]model.VirtualModelCustomFreezeState),
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

// TestActivateNextVirtualModelCandidateCustomEnabledWhenMasterEnabled 对比验证总开关开启时
// 直填自定义候选可被选中（索引推进到 0），证明门禁只在关闭时生效喵。
func TestActivateNextVirtualModelCandidateCustomEnabledWhenMasterEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newUserUpstreamGateSessionContext("virtual/gate-custom-on")
	setUserUpstreamMasterOptionForTest(t, "true")

	// 链上只有一个直填自定义候选，总开关开启喵。
	executionState := &virtualModelExecutionState{
		virtualModelName: "gate-custom-on",
		virtualModelID:   1,
		executionSnapshot: &model.VirtualModelExecutionSnapshot{
			Candidates: []model.VirtualModelCandidateSnapshot{
				{CandidateID: 93, VirtualModelID: 1, StableOrder: 0, SourceType: model.VirtualModelSourceCustom, Enabled: true, RealModelName: "gpt-direct", EncryptedBaseURL: "enc", EncryptedAPIKey: "enc", AuthStyle: model.VirtualModelAuthBearer},
			},
			FailureRulesByCandidateID: make(map[int][]model.VirtualModelFailureRule),
			GlobalFailureRules:        []model.VirtualModelFailureRule{},
		},
		manualFrozenCandidateIDs:        make(map[int]bool),
		automaticFreezeStatesByIdentity: make(map[string]model.VirtualModelCustomFreezeState),
		internalFreezeStatesByCandidate: make(map[int]model.VirtualModelInternalFreezeState),
		ruleRetryCounts:                 make(map[int]int),
		currentCandidateIndex:           -1,
		skippedCandidateIDs:             make(map[int]bool),
	}
	common.SetContextKey(ctx, constant.ContextKeyVirtualModelExecutionState, executionState)

	// 总开关开启时自定义候选被选中并推进索引；即便凭据不可用也走受控处理而非前置跳过喵。
	activateNextVirtualModelCandidate(ctx, executionState)
	require.Equal(t, 0, executionState.currentCandidateIndex)
}
