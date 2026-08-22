package controller

import (
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// recordingBillingSettler 是记录调用次数的计费结算测试替身喵。
// 它只统计调用而不真正改动额度，用于验证守卫分支绝不触碰普通请求的计费状态喵。
type recordingBillingSettler struct {
	settleCallCount            int // Settle 被调用的次数，单位：次喵。
	refundCallCount            int // 异步 Refund 被调用的次数，单位：次喵。
	refundImmediatelyCallCount int // 同步 RefundImmediately 被调用的次数，单位：次喵。
	reserveCallCount           int // Reserve 被调用的次数，单位：次喵。
}

// Settle 记录一次结算调用并始终返回成功喵。
func (settler *recordingBillingSettler) Settle(actualQuota int) error {
	// 累加调用计数，供用例断言守卫分支是否误触结算喵。
	settler.settleCallCount++
	return nil
}

// Refund 记录一次异步退款调用喵。
func (settler *recordingBillingSettler) Refund(c *gin.Context) {
	// 累加调用计数，供用例断言守卫分支是否误触异步退款喵。
	settler.refundCallCount++
}

// RefundImmediately 记录一次同步退款调用并始终返回成功喵。
func (settler *recordingBillingSettler) RefundImmediately(c *gin.Context) error {
	// 累加调用计数，这是候选切换是否真的释放旧候选额度的关键观测点喵。
	settler.refundImmediatelyCallCount++
	return nil
}

// NeedsRefund 恒定返回真，模拟仍持有未结算预扣额度的会话喵。
func (settler *recordingBillingSettler) NeedsRefund() bool {
	return true
}

// GetPreConsumedQuota 返回固定预扣额度，便于观察额度是否被守卫分支修改喵。
func (settler *recordingBillingSettler) GetPreConsumedQuota() int {
	return 256
}

// Reserve 记录一次补扣调用并始终返回成功喵。
func (settler *recordingBillingSettler) Reserve(targetQuota int) error {
	// 累加调用计数，供用例断言守卫分支是否误触额度补扣喵。
	settler.reserveCallCount++
	return nil
}

// newCandidateSwitchTestRelayInfo 构造带记录型计费会话的旧候选 relay 信息喵。
func newCandidateSwitchTestRelayInfo(settler *recordingBillingSettler) *relaycommon.RelayInfo {
	// 只填充守卫分支会读取的字段，其余保持零值以确保失败原因唯一喵。
	return &relaycommon.RelayInfo{
		OriginModelName: "gpt-previous-candidate",
		UsingGroup:      "default",
		RequestId:       "req-base:vc71a1",
		Billing:         settler,
	}
}

// newCandidateSwitchTestBaseline 构造一个字段完整的请求级基线，用于隔离守卫分支的失败原因喵。
func newCandidateSwitchTestBaseline() *relaycommon.CandidateRelayBaseline {
	// 基线本身合法，这样用例失败时可以确定是候选身份守卫而不是基线校验拦下的请求喵。
	return &relaycommon.CandidateRelayBaseline{
		BaseRequestID:        "req-base",
		RelayFormat:          types.RelayFormatOpenAI,
		EstimatePromptTokens: 128,
	}
}

// TestSwitchToNextVirtualModelCandidateRejectsMissingInputs 验证缺少任一必需入参时立即安全失败喵。
func TestSwitchToNextVirtualModelCandidateRejectsMissingInputs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 四种缺参组合都必须被拒绝，避免用残缺基线为后备候选建立计费喵。
	missingInputCases := []struct {
		name            string // 子用例名称，描述被置空的入参喵。
		provideContext  bool   // 是否提供 Gin 上下文喵。
		provideRelay    bool   // 是否提供旧候选 relay 信息喵。
		provideBaseline bool   // 是否提供请求级基线喵。
		provideMeta     bool   // 是否提供 token 计数元数据喵。
	}{
		{name: "缺少上下文", provideContext: false, provideRelay: true, provideBaseline: true, provideMeta: true},
		{name: "缺少旧候选 relay", provideContext: true, provideRelay: false, provideBaseline: true, provideMeta: true},
		{name: "缺少请求级基线", provideContext: true, provideRelay: true, provideBaseline: false, provideMeta: true},
		{name: "缺少 token 元数据", provideContext: true, provideRelay: true, provideBaseline: true, provideMeta: false},
	}

	for _, missingInputCase := range missingInputCases {
		t.Run(missingInputCase.name, func(t *testing.T) {
			// 每个子用例都用独立的记录型计费会话，避免调用计数互相污染喵。
			settler := &recordingBillingSettler{}

			// 按子用例开关逐个装配入参，被关闭的入参保持 nil 喵。
			var ctx *gin.Context
			if missingInputCase.provideContext {
				ctx, _ = gin.CreateTestContext(httptest.NewRecorder())
			}
			var currentRelayInfo *relaycommon.RelayInfo
			if missingInputCase.provideRelay {
				currentRelayInfo = newCandidateSwitchTestRelayInfo(settler)
			}
			var baseline *relaycommon.CandidateRelayBaseline
			if missingInputCase.provideBaseline {
				baseline = newCandidateSwitchTestBaseline()
			}
			var meta *types.TokenCountMeta
			if missingInputCase.provideMeta {
				meta = &types.TokenCountMeta{}
			}

			candidateRelayInfo, switchError := switchToNextVirtualModelCandidate(ctx, currentRelayInfo, baseline, types.RelayFormatOpenAI, meta)
			// 必须返回错误且不返回半成品 relay，避免外层继续用它请求上游喵。
			require.Error(t, switchError)
			require.Nil(t, candidateRelayInfo)
			// 该错误属于内部前提不满足，禁止触发原生重试逻辑喵。
			require.True(t, types.IsSkipRetryError(switchError))
			// 关键断言：守卫分支绝不能改动旧候选的计费状态喵。
			require.Zero(t, settler.refundImmediatelyCallCount)
			require.Zero(t, settler.refundCallCount)
			require.Zero(t, settler.settleCallCount)
			require.Zero(t, settler.reserveCallCount)
		})
	}
}

// TestSwitchToNextVirtualModelCandidateRejectsNonVirtualRequest 验证普通模型请求误入候选切换时不动其计费喵。
func TestSwitchToNextVirtualModelCandidateRejectsNonVirtualRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 普通请求的上下文里没有虚拟模型执行状态，也就没有已激活候选喵。
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	settler := &recordingBillingSettler{}
	currentRelayInfo := newCandidateSwitchTestRelayInfo(settler)

	candidateRelayInfo, switchError := switchToNextVirtualModelCandidate(ctx, currentRelayInfo, newCandidateSwitchTestBaseline(), types.RelayFormatOpenAI, &types.TokenCountMeta{})
	// 没有候选身份时必须直接返回内部错误，绝不进入候选级计费路径喵。
	require.Error(t, switchError)
	require.Nil(t, candidateRelayInfo)
	require.True(t, types.IsSkipRetryError(switchError))
	// 关键断言：普通请求的预扣额度绝不能被虚拟模型代码路径同步退款掉喵。
	require.Zero(t, settler.refundImmediatelyCallCount)
	require.Zero(t, settler.refundCallCount)
	require.Zero(t, settler.settleCallCount)
	// 旧候选 relay 的计费会话必须保持原样，供外层 defer 按普通请求语义收尾喵。
	require.Same(t, settler, currentRelayInfo.Billing)
}

// TestSwitchToNextVirtualModelCandidateKeepsBillingWhenRelayInfoHasNoSession 验证无计费会话时也能安全走守卫分支喵。
func TestSwitchToNextVirtualModelCandidateKeepsBillingWhenRelayInfoHasNoSession(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 喵~防御：免费模型候选不会建立计费会话，Billing 为 nil 时守卫分支不得空指针崩溃喵。
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	currentRelayInfo := &relaycommon.RelayInfo{OriginModelName: "free-previous-candidate", UsingGroup: "default"}

	candidateRelayInfo, switchError := switchToNextVirtualModelCandidate(ctx, currentRelayInfo, newCandidateSwitchTestBaseline(), types.RelayFormatOpenAI, &types.TokenCountMeta{})
	// 没有候选身份仍然要返回错误，但过程中不允许因 Billing 为 nil 而 panic 喵。
	require.Error(t, switchError)
	require.Nil(t, candidateRelayInfo)
	require.Nil(t, currentRelayInfo.Billing)
}
