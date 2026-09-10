package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	relaykitypes "github.com/QuantumNous/new-api/relaykit/types"
	virtualmodelservice "github.com/QuantumNous/new-api/service/virtualmodel"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// newCustomLogTestState 构造带单个直填 custom 候选的执行状态，供提交后失败与成功日志断言喵。
func newCustomLogTestState() (*virtualModelExecutionState, []model.VirtualModelCandidateSnapshot) {
	candidates := []model.VirtualModelCandidateSnapshot{
		{CandidateID: 71, VirtualModelID: 9, StableOrder: 0, SourceType: model.VirtualModelSourceCustom, Enabled: true, RealModelName: "custom-a"},
	}
	executionState := newUpstreamFailureTestState(candidates, nil, nil)
	executionState.virtualModelName = "virtual/vm-test"
	executionState.currentCandidateIndex = 0
	executionState.startTime = time.Now().Add(-200 * time.Millisecond)
	return executionState, candidates
}

// TestRecordVirtualModelCustomCommittedFailure 验证提交后失败会追加尝试并落 type=9 整体失败日志且防重喵。
// 修复回归：此前响应已部分提交的失败既不 append 尝试也不落日志，请求全程无留痕喵。
func TestRecordVirtualModelCustomCommittedFailure(t *testing.T) {
	newProbeTestDB(t)
	testLogDB := newFailureLogTestDB(t)
	gin.SetMode(gin.TestMode)

	executionState, candidates := newCustomLogTestState()
	ctx, attempts, _ := newUpstreamFailureTestContext(executionState)
	common.SetContextKey(ctx, constant.ContextKeyVirtualModelName, "virtual/vm-test")

	startTime := time.Now().Add(-150 * time.Millisecond)
	failure := virtualmodelservice.CandidateFailure{HTTPStatus: 0, ErrorClass: "stream_cut"}
	recordVirtualModelCustomCommittedFailure(ctx, &candidates[0], "custom-a", failure, false, nil, 0, startTime)

	// 候选尝试序列应包含一条失败记录喵。
	require.Len(t, *attempts, 1)
	require.False(t, (*attempts)[0].Success)
	require.Equal(t, "custom", (*attempts)[0].Source)
	require.Equal(t, "stream_cut", (*attempts)[0].ErrorClass)
	require.Equal(t, 71, (*attempts)[0].CandidateID)
	require.Greater(t, (*attempts)[0].ElapsedMs, int64(0))

	// type=9 整体失败日志应落库，且携带 candidates 序列喵。
	var logCount int64
	require.NoError(t, testLogDB.Model(&model.Log{}).Count(&logCount).Error)
	require.Equal(t, int64(1), logCount)
	var logRecord model.Log
	require.NoError(t, testLogDB.Model(&model.Log{}).First(&logRecord).Error)
	require.Equal(t, model.LogTypeVirtualModel, logRecord.Type)
	require.Equal(t, "virtual/vm-test", logRecord.ModelName)
	require.Contains(t, logRecord.Other, "stream_cut")
	require.Contains(t, logRecord.Other, "final_success")
	var parsedOther map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(logRecord.Other, &parsedOther))
	injectedCandidates, hasCandidates := parsedOther["candidates"].([]interface{})
	require.True(t, hasCandidates, "整体失败日志必须携带候选尝试序列")
	require.Len(t, injectedCandidates, 1)

	// 防重：再次调用只会追加尝试，整体失败日志不重复落库喵。
	recordVirtualModelCustomCommittedFailure(ctx, &candidates[0], "custom-a", failure, false, nil, 0, startTime)
	require.Len(t, *attempts, 2)
	require.NoError(t, testLogDB.Model(&model.Log{}).Count(&logCount).Error)
	require.Equal(t, int64(1), logCount)
}

// TestNormalizeCommittedStreamCutClassification 验证放流后中途非 EOF 断流被归入 stream_cut 分类喵。
// 修复内容：ExecuteCustomCandidate/ExecuteUserUpstreamModel 对已提交流的中途读失败包上 ErrStreamCut 哨兵喵。
func TestNormalizeCommittedStreamCutClassification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 构造与执行函数同构的错误链：外层包 ErrStreamCut 哨兵，内层是原始网络错误喵。
	wrapped := fmt.Errorf("read committed custom upstream stream: %w", fmt.Errorf("%w: %v", relaykitypes.ErrStreamCut, errors.New("connection reset")))
	classified := virtualmodelservice.NormalizeCandidateFailure(0, nil, nil, wrapped)
	require.Equal(t, "stream_cut", classified.ErrorClass)
}

// TestRecordVirtualModelCustomSuccessLog 验证纯直填 custom 候选成功日志：type=9、token 计数、quota=0 不计费喵。
// 用户要求核对：直接填写 url/key 的候选（不计费自定义计费），日志应正常且带 token 计数喵。
func TestRecordVirtualModelCustomSuccessLog(t *testing.T) {
	newProbeTestDB(t)
	testLogDB := newFailureLogTestDB(t)
	gin.SetMode(gin.TestMode)

	executionState, _ := newCustomLogTestState()
	executionState.modelRequest = &ModelRequest{Model: "virtual/vm-test", Group: "default"}
	ctx, attempts, _ := newUpstreamFailureTestContext(executionState)
	ctx.Set("username", "tester")

	// 模拟成功路径：executeCustomVirtualModelCandidate 在写日志前已 append 成功尝试喵。
	appendVirtualModelCandidateAttempt(ctx, model.VirtualModelCandidateAttemptRecord{
		Seq: 1, CandidateID: 71, Source: "custom", Label: "custom-a", Success: true, StatusCode: http.StatusOK, ElapsedMs: 60, TtftMs: 30,
	})

	// 纯直填 custom 成功：携带上游解析出的 token 计数与请求级首字耗时写日志喵。
	usage := &dto.Usage{PromptTokens: 120, CompletionTokens: 30}
	recordVirtualModelCustomSuccess(ctx, 1, usage, 40)

	var logRecord model.Log
	require.NoError(t, testLogDB.Last(&logRecord).Error)
	require.Equal(t, model.LogTypeVirtualModel, logRecord.Type)
	require.Equal(t, "virtual/vm-test", logRecord.ModelName)
	require.Equal(t, 120, logRecord.PromptTokens)
	require.Equal(t, 30, logRecord.CompletionTokens)
	// 不计费：纯直填 custom 不走 new-api quota，日志 quota 恒为 0 喵。
	require.Equal(t, 0, logRecord.Quota)
	require.Equal(t, 1, logRecord.UseTime)

	// other 标记最终成功，并携带候选尝试序列（含成功尝试）喵。
	var parsedOther map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(logRecord.Other, &parsedOther))
	require.Equal(t, true, parsedOther["final_success"])
	injectedCandidates, hasCandidates := parsedOther["candidates"].([]interface{})
	require.True(t, hasCandidates, "纯直填 custom 成功日志应携带候选尝试序列")
	require.Len(t, injectedCandidates, 1)
	successAttempt := injectedCandidates[0].(map[string]interface{})
	require.Equal(t, true, successAttempt["success"])
	require.Equal(t, "custom", successAttempt["source"])

	// 尝试序列与日志行一一对应，确认未重复追加喵。
	require.Len(t, *attempts, 1)
}
