package common

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubBillingSettler 是仅用于测试的计费会话替身，用来模拟上一个候选残留的计费状态喵。
type stubBillingSettler struct{}

// Settle 在测试替身里不做任何真实结算喵。
func (settler *stubBillingSettler) Settle(actualQuota int) error { return nil }

// Refund 在测试替身里不做任何真实退款喵。
func (settler *stubBillingSettler) Refund(c *gin.Context) {}

// RefundImmediately 在测试替身里直接返回成功喵。
func (settler *stubBillingSettler) RefundImmediately(c *gin.Context) error { return nil }

// NeedsRefund 在测试替身里始终报告无需退款喵。
func (settler *stubBillingSettler) NeedsRefund() bool { return false }

// GetPreConsumedQuota 在测试替身里始终返回零预扣额度喵。
func (settler *stubBillingSettler) GetPreConsumedQuota() int { return 0 }

// Reserve 在测试替身里不做任何真实补扣喵。
func (settler *stubBillingSettler) Reserve(targetQuota int) error { return nil }

// newCandidateTestContext 构造一个「已经切换到指定候选」的 Gin 测试上下文喵。
// 该辅助函数只写入 genBaseRelayInfo 会读取的请求级上下文键，不涉及任何真实数据库喵。
func newCandidateTestContext(t *testing.T, realModelName string, groupName string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	// 构造一个可重放的 JSON 请求体，模拟中间件已把顶层 model 改写成候选真实模型喵。
	requestBody := `{"model":"` + realModelName + `","messages":[{"role":"user","content":"hi"}]}`
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(requestBody))
	ginContext.Request.Header.Set("Content-Type", "application/json")
	// 写入候选真实模型与固定分组，模拟 applyInternalVirtualModelCandidate 的效果喵。
	common.SetContextKey(ginContext, constant.ContextKeyOriginalModel, realModelName)
	common.SetContextKey(ginContext, constant.ContextKeyTokenGroup, groupName)
	common.SetContextKey(ginContext, constant.ContextKeyUsingGroup, groupName)
	// 写入请求级令牌与用户信息，保证候选级 RelayInfo 能继承同一个调用者身份喵。
	common.SetContextKey(ginContext, constant.ContextKeyTokenId, 7)
	common.SetContextKey(ginContext, constant.ContextKeyTokenKey, "token-key")
	common.SetContextKey(ginContext, constant.ContextKeyUserId, 11)
	common.SetContextKey(ginContext, constant.ContextKeyUserGroup, "user-group")
	return ginContext
}

// newPollutedCandidateRelayInfo 构造一个「已经跑过一轮且状态被污染」的候选 RelayInfo 喵。
// 它模拟候选 A 执行完毕后残留的 Channel、计费、定价、重试与响应状态喵。
func newPollutedCandidateRelayInfo(t *testing.T, ginContext *gin.Context, modelName string) *RelayInfo {
	t.Helper()
	info, generateError := GenRelayInfo(ginContext, types.RelayFormatOpenAI, &dto.GeneralOpenAIRequest{Model: modelName}, nil)
	require.NoError(t, generateError)
	require.NotNil(t, info)
	// 污染 Channel 元数据，模拟候选 A 已经选中过某个上游渠道喵。
	info.ChannelMeta = &ChannelMeta{ChannelId: 31, ChannelType: 1, ApiKey: "channel-secret", UpstreamModelName: modelName}
	// 污染计费状态，模拟候选 A 已经建立了计费会话并完成预扣喵。
	info.Billing = &stubBillingSettler{}
	info.FinalPreConsumedQuota = 512
	info.BillingSource = "wallet"
	info.SubscriptionId = 3
	info.SubscriptionPreConsumed = 8
	info.SubscriptionPostDelta = 2
	// 污染定价结果，模拟候选 A 已经按自己的模型与分组算过价格喵。
	info.PriceData = hosttypes.PriceData{ModelRatio: 2.5, QuotaToPreConsume: 512, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1.5}}
	// 污染重试与错误状态，模拟候选 A 已经耗尽原生 Channel 重试喵。
	info.RetryIndex = 3
	info.LastError = types.NewError(assert.AnError, types.ErrorCodeDoRequestFailed)
	// 污染响应计数，模拟候选 A 已经统计过若干次响应转发喵。
	info.SendResponseCount = 9
	info.ReceivedResponseCount = 11
	return info
}

func TestNewCandidateRelayBaselineRejectsIncompleteRequestInfo(t *testing.T) {
	// 空 RelayInfo 不能派生基线喵。
	baseline, baselineError := NewCandidateRelayBaseline(nil)
	require.Error(t, baselineError)
	require.Nil(t, baseline)

	// 缺少请求标识时不能派生候选幂等键喵。
	baseline, baselineError = NewCandidateRelayBaseline(&RelayInfo{RelayFormat: types.RelayFormatOpenAI, RequestId: "   "})
	require.Error(t, baselineError)
	require.Nil(t, baseline)

	// 缺少协议格式时无法为候选重建 RelayInfo 喵。
	baseline, baselineError = NewCandidateRelayBaseline(&RelayInfo{RequestId: "req-1"})
	require.Error(t, baselineError)
	require.Nil(t, baseline)
}

func TestNewCandidateRelayBaselineOnlyCapturesRequestLevelFields(t *testing.T) {
	billingRequestInput := &billingexpr.RequestInput{}
	info := &RelayInfo{
		RequestId:             "  req-baseline  ",
		RelayFormat:           types.RelayFormatClaude,
		BillingRequestInput:   billingRequestInput,
		ForcePreConsume:       true,
		IsChannelTest:         true,
		FinalPreConsumedQuota: 999,
		BillingSource:         "subscription",
		RetryIndex:            4,
		ChannelMeta:           &ChannelMeta{ChannelId: 5},
		Billing:               &stubBillingSettler{},
	}
	info.SetEstimatePromptTokens(1234)

	baseline, baselineError := NewCandidateRelayBaseline(info)
	require.NoError(t, baselineError)
	require.NotNil(t, baseline)

	// 请求标识被去除首尾空白后保存喵。
	assert.Equal(t, "req-baseline", baseline.BaseRequestID)
	assert.Equal(t, types.RelayFormat(types.RelayFormatClaude), baseline.RelayFormat)
	assert.Equal(t, 1234, baseline.EstimatePromptTokens)
	assert.Same(t, billingRequestInput, baseline.BillingRequestInput)
	assert.True(t, baseline.ForcePreConsume)
	assert.True(t, baseline.IsChannelTest)
}

func TestCandidateRequestIDComposesBoundedIdempotencyKey(t *testing.T) {
	// 正常路径按固定分隔符拼接，两个候选得到不同的幂等键喵。
	firstRequestID, firstError := CandidateRequestID("req-base", "vc42a1")
	require.NoError(t, firstError)
	assert.Equal(t, "req-base:vc42a1", firstRequestID)

	secondRequestID, secondError := CandidateRequestID("req-base", "vc43a2")
	require.NoError(t, secondError)
	assert.NotEqual(t, firstRequestID, secondRequestID)

	// 首尾空白被裁剪，避免拼出带空格的幂等键喵。
	trimmedRequestID, trimmedError := CandidateRequestID("  req-base  ", "  vc42a1  ")
	require.NoError(t, trimmedError)
	assert.Equal(t, "req-base:vc42a1", trimmedRequestID)
}

func TestCandidateRequestIDRejectsUnsafeOrOversizedInput(t *testing.T) {
	// 空基线标识无法保证唯一性喵。
	_, emptyBaseError := CandidateRequestID("", "vc42a1")
	require.Error(t, emptyBaseError)

	// 空尝试标识无法区分候选喵。
	_, emptyAttemptError := CandidateRequestID("req-base", "   ")
	require.Error(t, emptyAttemptError)

	// 含冒号、空格或换行的尝试标识会破坏日志与幂等键解析喵。
	for _, unsafeAttemptID := range []string{"vc42:a1", "vc42 a1", "vc42\na1", "vc42/a1", "vc42:../a1"} {
		_, unsafeError := CandidateRequestID("req-base", unsafeAttemptID)
		require.Error(t, unsafeError, "attempt id %q must be rejected", unsafeAttemptID)
	}

	// 尝试标识超过 32 字符时直接拒绝喵。
	_, longAttemptError := CandidateRequestID("req-base", strings.Repeat("a", maximumCandidateAttemptIDLength+1))
	require.Error(t, longAttemptError)

	// 拼接结果超过 64 字符时必须提前失败，避免数据库静默截断喵。
	_, overflowError := CandidateRequestID(strings.Repeat("b", maximumCandidateRequestIDLength), "vc42a1")
	require.Error(t, overflowError)

	// 恰好等于上限时允许通过，保证边界值可用喵。
	boundaryBaseLength := maximumCandidateRequestIDLength - len(":vc42a1")
	boundaryRequestID, boundaryError := CandidateRequestID(strings.Repeat("b", boundaryBaseLength), "vc42a1")
	require.NoError(t, boundaryError)
	assert.Len(t, boundaryRequestID, maximumCandidateRequestIDLength)
}

func TestNewCandidateRelayInfoIsolatesConsecutiveCandidates(t *testing.T) {
	// 候选 A 先在自己的分组里跑完并残留 Channel、计费、定价、重试与响应状态喵。
	firstCandidateContext := newCandidateTestContext(t, "model-a", "group-a")
	firstCandidateInfo := newPollutedCandidateRelayInfo(t, firstCandidateContext, "model-a")
	firstCandidateInfo.RequestId = "req-base"
	firstCandidateInfo.SetEstimatePromptTokens(321)

	// 基线从候选 A 的 RelayInfo 抽取，但只允许带走请求级字段喵。
	baseline, baselineError := NewCandidateRelayBaseline(firstCandidateInfo)
	require.NoError(t, baselineError)

	// 候选 B 使用不同的真实模型与固定分组，中间件已把上下文切换过去喵。
	secondCandidateContext := newCandidateTestContext(t, "model-b", "group-b")
	secondCandidateInfo, candidateError := NewCandidateRelayInfo(
		secondCandidateContext,
		baseline,
		CandidateRelayIdentity{CandidateID: 43, CandidateAttemptID: "vc43a2", RealModelName: "model-b", GroupName: "group-b"},
		&dto.GeneralOpenAIRequest{Model: "model-b"},
	)
	require.NoError(t, candidateError)
	require.NotNil(t, secondCandidateInfo)

	// 候选 B 必须是全新对象，绝不能与候选 A 共享同一个 RelayInfo 喵。
	assert.NotSame(t, firstCandidateInfo, secondCandidateInfo)

	// 候选身份：模型与分组固定为候选 B 自己的配置喵。
	assert.Equal(t, "model-b", secondCandidateInfo.OriginModelName)
	assert.Equal(t, "group-b", secondCandidateInfo.TokenGroup)
	assert.Equal(t, "group-b", secondCandidateInfo.UsingGroup)

	// 候选幂等标识独立于候选 A，且长度不超过数据库列宽喵。
	assert.Equal(t, "req-base:vc43a2", secondCandidateInfo.RequestId)
	assert.NotEqual(t, firstCandidateInfo.RequestId, secondCandidateInfo.RequestId)
	assert.LessOrEqual(t, len(secondCandidateInfo.RequestId), maximumCandidateRequestIDLength)

	// 请求级基线字段被正确回填，候选切换不会丢失入口估算与计费偏好喵。
	assert.Equal(t, 321, secondCandidateInfo.GetEstimatePromptTokens())
	assert.Equal(t, types.RelayFormat(types.RelayFormatOpenAI), secondCandidateInfo.RelayFormat)

	// 候选 A 的全部可变状态都不得出现在候选 B 上喵。
	require.NoError(t, AssertCandidateRelayInfoIsolated(secondCandidateInfo))
	assert.Nil(t, secondCandidateInfo.ChannelMeta)
	assert.Nil(t, secondCandidateInfo.Billing)
	assert.Zero(t, secondCandidateInfo.FinalPreConsumedQuota)
	assert.Empty(t, secondCandidateInfo.BillingSource)
	assert.Zero(t, secondCandidateInfo.SubscriptionId)
	assert.Zero(t, secondCandidateInfo.RetryIndex)
	assert.Nil(t, secondCandidateInfo.LastError)
	assert.Zero(t, secondCandidateInfo.SendResponseCount)
	assert.Zero(t, secondCandidateInfo.ReceivedResponseCount)
	assert.True(t, candidatePriceDataIsZero(secondCandidateInfo.PriceData))

	// 候选 A 自身状态不被工厂修改，避免隐式清空掩盖未结算状态喵。
	assert.NotNil(t, firstCandidateInfo.ChannelMeta)
	assert.NotNil(t, firstCandidateInfo.Billing)
	assert.Equal(t, 512, firstCandidateInfo.FinalPreConsumedQuota)
}

func TestNewCandidateRelayInfoRejectsInvalidInput(t *testing.T) {
	validBaseline := &CandidateRelayBaseline{BaseRequestID: "req-base", RelayFormat: types.RelayFormatOpenAI}
	validIdentity := CandidateRelayIdentity{CandidateID: 42, CandidateAttemptID: "vc42a1", RealModelName: "model-a", GroupName: "group-a"}
	validRequest := &dto.GeneralOpenAIRequest{Model: "model-a"}

	// 喵~防御：空上下文、空基线与空请求都必须被拒绝喵。
	_, nilContextError := NewCandidateRelayInfo(nil, validBaseline, validIdentity, validRequest)
	require.Error(t, nilContextError)

	candidateContext := newCandidateTestContext(t, "model-a", "group-a")
	_, nilBaselineError := NewCandidateRelayInfo(candidateContext, nil, validIdentity, validRequest)
	require.Error(t, nilBaselineError)

	_, nilRequestError := NewCandidateRelayInfo(candidateContext, validBaseline, validIdentity, nil)
	require.Error(t, nilRequestError)

	// 候选编号缺失说明调用方其实还在普通 relay 路径上喵。
	_, zeroCandidateError := NewCandidateRelayInfo(candidateContext, validBaseline,
		CandidateRelayIdentity{CandidateAttemptID: "vc42a1", RealModelName: "model-a", GroupName: "group-a"}, validRequest)
	require.Error(t, zeroCandidateError)

	// 尝试标识缺失时无法隔离计费幂等键喵。
	_, missingAttemptError := NewCandidateRelayInfo(candidateContext, validBaseline,
		CandidateRelayIdentity{CandidateID: 42, RealModelName: "model-a", GroupName: "group-a"}, validRequest)
	require.Error(t, missingAttemptError)

	// auto 分组会进入 Token AutoRoutes 的自动分支，必须拒绝喵。
	autoGroupContext := newCandidateTestContext(t, "model-a", "auto")
	_, autoGroupError := NewCandidateRelayInfo(autoGroupContext, validBaseline,
		CandidateRelayIdentity{CandidateID: 42, CandidateAttemptID: "vc42a1", RealModelName: "model-a", GroupName: "auto"}, validRequest)
	require.Error(t, autoGroupError)
}

func TestNewCandidateRelayInfoRejectsUnsupportedRelayFormats(t *testing.T) {
	candidateContext := newCandidateTestContext(t, "model-a", "group-a")
	validIdentity := CandidateRelayIdentity{CandidateID: 42, CandidateAttemptID: "vc42a1", RealModelName: "model-a", GroupName: "group-a"}

	// WebSocket 与任务类协议没有可安全重放的 JSON 请求体，禁止进入候选隔离路径喵。
	unsupportedFormats := []types.RelayFormat{
		types.RelayFormatOpenAIRealtime,
		types.RelayFormatUnrealSpeechWebSocket,
		types.RelayFormatTask,
		types.RelayFormatMjProxy,
		"",
	}
	for _, unsupportedFormat := range unsupportedFormats {
		baseline := &CandidateRelayBaseline{BaseRequestID: "req-base", RelayFormat: unsupportedFormat}
		_, formatError := NewCandidateRelayInfo(candidateContext, baseline, validIdentity, &dto.GeneralOpenAIRequest{Model: "model-a"})
		require.Error(t, formatError, "relay format %q must be rejected", unsupportedFormat)
	}
}

func TestNewCandidateRelayInfoRejectsContextIdentityMismatch(t *testing.T) {
	baseline := &CandidateRelayBaseline{BaseRequestID: "req-base", RelayFormat: types.RelayFormatOpenAI}
	candidateRequest := &dto.GeneralOpenAIRequest{Model: "model-b"}

	// 上下文仍停留在候选 A 的模型上，说明候选推进顺序被破坏喵。
	staleModelContext := newCandidateTestContext(t, "model-a", "group-b")
	_, modelMismatchError := NewCandidateRelayInfo(staleModelContext, baseline,
		CandidateRelayIdentity{CandidateID: 43, CandidateAttemptID: "vc43a2", RealModelName: "model-b", GroupName: "group-b"}, candidateRequest)
	require.Error(t, modelMismatchError)

	// 上下文仍停留在候选 A 的分组上，会让 Channel 选择与分组倍率错位喵。
	staleGroupContext := newCandidateTestContext(t, "model-b", "group-a")
	_, groupMismatchError := NewCandidateRelayInfo(staleGroupContext, baseline,
		CandidateRelayIdentity{CandidateID: 43, CandidateAttemptID: "vc43a2", RealModelName: "model-b", GroupName: "group-b"}, candidateRequest)
	require.Error(t, groupMismatchError)
}

func TestAssertCandidateRelayInfoIsolatedDetectsLeakedCandidateState(t *testing.T) {
	// 空指针无法断言隔离性，视为违规喵。
	require.Error(t, AssertCandidateRelayInfoIsolated(nil))

	// 逐项构造「只泄漏一个字段」的候选 RelayInfo，确认每一项都能被单独识别出来喵。
	leakedStateCases := []struct {
		name    string
		pollute func(info *RelayInfo)
	}{
		{"channel meta", func(info *RelayInfo) { info.ChannelMeta = &ChannelMeta{ChannelId: 1} }},
		{"task relay info", func(info *RelayInfo) { info.TaskRelayInfo = &TaskRelayInfo{Action: "submit"} }},
		{"billing session", func(info *RelayInfo) { info.Billing = &stubBillingSettler{} }},
		{"pre consumed quota", func(info *RelayInfo) { info.FinalPreConsumedQuota = 1 }},
		{"billing source", func(info *RelayInfo) { info.BillingSource = "wallet" }},
		{"subscription id", func(info *RelayInfo) { info.SubscriptionId = 1 }},
		{"subscription pre consumed", func(info *RelayInfo) { info.SubscriptionPreConsumed = 1 }},
		{"subscription post delta", func(info *RelayInfo) { info.SubscriptionPostDelta = 1 }},
		{"price data ratio", func(info *RelayInfo) { info.PriceData = hosttypes.PriceData{ModelRatio: 1} }},
		{"price data free model", func(info *RelayInfo) { info.PriceData = hosttypes.PriceData{FreeModel: true} }},
		{"price data group ratio", func(info *RelayInfo) {
			info.PriceData = hosttypes.PriceData{GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}
		}},
		{"price data other ratio", func(info *RelayInfo) { info.PriceData.AddOtherRatio("web_search", 2) }},
		{"tiered snapshot", func(info *RelayInfo) { info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{} }},
		{"quota clamp", func(info *RelayInfo) { info.QuotaClamp = &common.QuotaClamp{} }},
		{"retry index", func(info *RelayInfo) { info.RetryIndex = 1 }},
		{"last error", func(info *RelayInfo) { info.LastError = types.NewError(assert.AnError, types.ErrorCodeDoRequestFailed) }},
		{"send response count", func(info *RelayInfo) { info.SendResponseCount = 1 }},
		{"received response count", func(info *RelayInfo) { info.ReceivedResponseCount = 1 }},
		{"stream status", func(info *RelayInfo) { info.StreamStatus = &StreamStatus{} }},
		{"runtime headers override", func(info *RelayInfo) { info.UseRuntimeHeadersOverride = true }},
		{"param override audit", func(info *RelayInfo) { info.ParamOverrideAudit = []string{"drop:store"} }},
	}
	for _, leakedStateCase := range leakedStateCases {
		t.Run(leakedStateCase.name, func(t *testing.T) {
			candidateContext := newCandidateTestContext(t, "model-a", "group-a")
			info, generateError := GenRelayInfo(candidateContext, types.RelayFormatOpenAI, &dto.GeneralOpenAIRequest{Model: "model-a"}, nil)
			require.NoError(t, generateError)
			// 干净的候选 RelayInfo 必须先通过断言，确保用例只检验单个泄漏字段喵。
			require.NoError(t, AssertCandidateRelayInfoIsolated(info))
			leakedStateCase.pollute(info)
			require.Error(t, AssertCandidateRelayInfoIsolated(info))
		})
	}
}

func TestAssertCandidateRelayInfoIsolatedRejectsAlreadySentResponse(t *testing.T) {
	candidateContext := newCandidateTestContext(t, "model-a", "group-a")
	info, generateError := GenRelayInfo(candidateContext, types.RelayFormatOpenAI, &dto.GeneralOpenAIRequest{Model: "model-a"}, nil)
	require.NoError(t, generateError)
	require.NoError(t, AssertCandidateRelayInfoIsolated(info))

	// 一旦已经向客户端写出响应，就绝不允许把该 RelayInfo 当成新候选继续使用喵。
	// 这里直接把首字节时间推到开始时间之后，避免依赖 Windows 时钟粒度导致断言不稳定喵。
	info.FirstResponseTime = info.StartTime.Add(time.Millisecond)
	require.True(t, info.HasSendResponse())
	require.Error(t, AssertCandidateRelayInfoIsolated(info))
}

func TestCandidatePriceDataIsZeroRecognizesFreshPricing(t *testing.T) {
	// 全零定价代表本候选尚未计算价格喵。
	assert.True(t, candidatePriceDataIsZero(hosttypes.PriceData{}))

	// 任一定价字段非零都说明沿用了上一个候选的定价结果喵。
	assert.False(t, candidatePriceDataIsZero(hosttypes.PriceData{UsePrice: true}))
	assert.False(t, candidatePriceDataIsZero(hosttypes.PriceData{ModelPrice: 0.01}))
	assert.False(t, candidatePriceDataIsZero(hosttypes.PriceData{CompletionRatio: 3}))
	assert.False(t, candidatePriceDataIsZero(hosttypes.PriceData{CacheRatio: 0.5}))
	assert.False(t, candidatePriceDataIsZero(hosttypes.PriceData{ImageRatio: 2}))
	assert.False(t, candidatePriceDataIsZero(hosttypes.PriceData{AudioCompletionRatio: 2}))
	assert.False(t, candidatePriceDataIsZero(hosttypes.PriceData{Quota: 10}))
	assert.False(t, candidatePriceDataIsZero(hosttypes.PriceData{QuotaToPreConsume: 10}))
}
