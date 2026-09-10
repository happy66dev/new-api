package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newVirtualBillingTestContext 构造一个已初始化虚拟模型计费累计的最小 Gin 上下文喵。
func newVirtualBillingTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	// 与 middleware.distributor 的处理保持一致：虚拟模型请求开始时写入累计容器喵。
	common.SetContextKey(ctx, constant.ContextKeyVirtualBilling, relaycommon.NewVirtualBilling())
	return ctx
}

// newVirtualBillingTestRelayInfo 构造一个价格数据完备的候选 relay，供算费链路使用喵。
func newVirtualBillingTestRelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatOpenAI,
		FinalRequestRelayFormat: types.RelayFormatOpenAI,
		OriginModelName:         "virtual/test-chain",
		UserId:                  9001,
		StartTime:               time.Now(),
		// 渠道元信息以指针嵌入，跳过候选的额度统计需要 ChannelId 定位渠道喵。
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         0,
			UpstreamModelName: "gpt-4o",
		},
		PriceData: hosttypes.PriceData{
			ModelRatio:      1,
			CompletionRatio: 2,
			GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
	}
}

// TestVirtualBillingFromContext 覆盖累计容器的读取与全部防御分支喵。
func TestVirtualBillingFromContext(t *testing.T) {
	// 喵~防御：空上下文没有累计容器喵。
	assert.Nil(t, virtualBillingFromContext(nil))

	gin.SetMode(gin.TestMode)
	plainCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	// 喵~防御：普通请求没有该 key，必须返回 nil，保证既有行为不变喵。
	assert.Nil(t, virtualBillingFromContext(plainCtx))

	// 喵~防御：key 存在但类型不符时按无累计处理，不得 panic 让请求失败喵。
	common.SetContextKey(plainCtx, constant.ContextKeyVirtualBilling, "not-a-billing")
	assert.Nil(t, virtualBillingFromContext(plainCtx))

	// 正常路径：写入的累计容器应能原样取回喵。
	virtualCtx := newVirtualBillingTestContext()
	require.NotNil(t, virtualBillingFromContext(virtualCtx))
}

// TestRegisterSkippedCandidateBillingRequiresVirtualContext 覆盖非虚拟请求不产生任何登记喵。
func TestRegisterSkippedCandidateBillingRequiresVirtualContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	plainCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	relayInfo := newVirtualBillingTestRelayInfo()

	usage := &dto.Usage{PromptTokens: 100, CompletionTokens: 20}
	// 喵~防御：缺少上下文或不带虚拟上下文时都必须静默跳过，普通请求绝不能被登记计费喵。
	assert.NotPanics(t, func() {
		RegisterSkippedCandidateBilling(nil, relayInfo, usage)
		RegisterSkippedCandidateBilling(plainCtx, nil, usage)
		RegisterSkippedCandidateBilling(plainCtx, relayInfo, usage)
	})

	// 非虚拟上下文没有容器，Settle 也必须直接返回 false 走原有退款逻辑喵。
	assert.False(t, SettlePendingCandidateBilling(plainCtx, relayInfo))
	assert.NotPanics(t, func() { ClearPendingCandidateBilling(plainCtx) })
}

// TestRegisterSkippedCandidateBillingMatchesTextQuotaSummary 覆盖登记额度与算费摘要同口径喵。
func TestRegisterSkippedCandidateBillingMatchesTextQuotaSummary(t *testing.T) {
	ctx := newVirtualBillingTestContext()
	relayInfo := newVirtualBillingTestRelayInfo()
	usage := &dto.Usage{PromptTokens: 1000, CompletionTokens: 200}

	// 先算一次期望摘要，确认登记值与成功结算用的是同一套价格口径喵。
	expectedSummary := calculateTextQuotaSummary(ctx, relayInfo, usage)
	require.Positive(t, expectedSummary.Quota)

	RegisterSkippedCandidateBilling(ctx, relayInfo, usage)

	pending := virtualBillingFromContext(ctx).PendingCandidate()
	assert.Equal(t, expectedSummary.Quota, pending.Quota)
	assert.Equal(t, expectedSummary.PromptTokens, pending.PromptTokens)
	assert.Equal(t, expectedSummary.CompletionTokens, pending.CompletionTokens)
}

// TestRegisterSkippedCandidateBillingAccumulatesRetries 覆盖同一候选多轮重试的累加语义喵。
func TestRegisterSkippedCandidateBillingAccumulatesRetries(t *testing.T) {
	ctx := newVirtualBillingTestContext()
	relayInfo := newVirtualBillingTestRelayInfo()
	billing := virtualBillingFromContext(ctx)

	firstUsage := &dto.Usage{PromptTokens: 100, CompletionTokens: 20}
	secondUsage := &dto.Usage{PromptTokens: 300, CompletionTokens: 60}
	firstQuota := calculateTextQuotaSummary(ctx, relayInfo, firstUsage).Quota
	secondQuota := calculateTextQuotaSummary(ctx, relayInfo, secondUsage).Quota

	RegisterSkippedCandidateBilling(ctx, relayInfo, firstUsage)
	RegisterSkippedCandidateBilling(ctx, relayInfo, secondUsage)

	// 待结算额度按「每次上游调用都真实消耗」累加，最近一次尝试额度只反映本轮喵。
	assert.Equal(t, firstQuota+secondQuota, billing.PendingCandidate().Quota)
	assert.Equal(t, secondQuota, billing.LastAttempt().Quota)
	assert.Equal(t, 400, billing.PendingCandidate().PromptTokens)
	assert.Equal(t, 80, billing.PendingCandidate().CompletionTokens)
}

// TestRegisterSkippedCandidateBillingNilUsageFallsBackToPromptOnly 覆盖没有上游 usage 的兜底口径喵。
func TestRegisterSkippedCandidateBillingNilUsageFallsBackToPromptOnly(t *testing.T) {
	ctx := newVirtualBillingTestContext()
	relayInfo := newVirtualBillingTestRelayInfo()
	// 请求侧 prompt 估算来自上下文，与原生兜底同一个来源喵。
	relayInfo.SetEstimatePromptTokens(500)

	// usage 为 nil 时按原生口径退化为「只计 prompt 估算」，completion 恒为 0 喵。
	RegisterSkippedCandidateBilling(ctx, relayInfo, nil)

	pending := virtualBillingFromContext(ctx).PendingCandidate()
	assert.Equal(t, 0, pending.CompletionTokens)
	assert.Equal(t, 500, pending.PromptTokens)
	assert.Positive(t, pending.Quota)
}

// TestSettlePendingCandidateBillingConsumesRegistrationOnce 覆盖结算的一次性与请求级累计喵。
func TestSettlePendingCandidateBillingConsumesRegistrationOnce(t *testing.T) {
	ctx := newVirtualBillingTestContext()
	relayInfo := newVirtualBillingTestRelayInfo()
	billing := virtualBillingFromContext(ctx)

	// 没有登记时结算必须返回 false，让候选切换继续走原有退款路径喵。
	assert.False(t, SettlePendingCandidateBilling(ctx, relayInfo))

	usage := &dto.Usage{PromptTokens: 1000, CompletionTokens: 200}
	expectedQuota := calculateTextQuotaSummary(ctx, relayInfo, usage).Quota
	RegisterSkippedCandidateBilling(ctx, relayInfo, usage)

	// 计费会话为空（免费候选/未建会话）时仍然要把登记额度计入请求级总额供日志对账喵。
	assert.True(t, SettlePendingCandidateBilling(ctx, relayInfo))
	assert.Equal(t, expectedQuota, billing.Total().Quota)
	assert.Equal(t, 1000, billing.Total().PromptTokens)
	assert.Equal(t, 200, billing.Total().CompletionTokens)

	// 同一次登记只允许结算一次：重复调用不得二次计费喵。
	assert.False(t, SettlePendingCandidateBilling(ctx, relayInfo))
	assert.Equal(t, expectedQuota, billing.Total().Quota)
}

// TestSettlePendingCandidateBillingUpdatesUsedQuotaOnly 覆盖跳过候选结算后的统计口径喵。
// 跳过候选确实扣了用户额度，因此已用额度必须同步增加；但它没有向客户端产出响应，
// 也不该被算作一次成功请求，所以请求数必须保持不变，避免请求数被候选链放大喵。
func TestSettlePendingCandidateBillingUpdatesUsedQuotaOnly(t *testing.T) {
	truncate(t)
	// 造一个额度充足的真实用户，用于观察已用额度与请求数的落库结果喵。
	seedUser(t, 9001, 1000000)

	ctx := newVirtualBillingTestContext()
	relayInfo := newVirtualBillingTestRelayInfo()
	usage := &dto.Usage{PromptTokens: 1000, CompletionTokens: 200}
	expectedQuota := calculateTextQuotaSummary(ctx, relayInfo, usage).Quota

	RegisterSkippedCandidateBilling(ctx, relayInfo, usage)
	require.True(t, SettlePendingCandidateBilling(ctx, relayInfo))

	var settledUser model.User
	require.NoError(t, model.DB.Where("id = ?", 9001).First(&settledUser).Error)
	// 已用额度按登记的估算额度增加，保证「日志额度 ≤ 用户已用额度」的口径成立喵。
	assert.Equal(t, expectedQuota, settledUser.UsedQuota)
	// 喵~防御：跳过候选不得增加请求数，否则候选链会让请求数虚高喵。
	assert.Equal(t, 0, settledUser.RequestCount)
}

// TestClearPendingCandidateBillingDropsWithoutSettling 覆盖成功路径的丢弃语义喵。
func TestClearPendingCandidateBillingDropsWithoutSettling(t *testing.T) {
	ctx := newVirtualBillingTestContext()
	relayInfo := newVirtualBillingTestRelayInfo()
	billing := virtualBillingFromContext(ctx)

	RegisterSkippedCandidateBilling(ctx, relayInfo, &dto.Usage{PromptTokens: 1000, CompletionTokens: 200})
	require.Positive(t, billing.PendingCandidate().Quota)

	// 候选最终成功时会按真实 usage 结算，探测阶段的登记必须被丢弃且不进入请求级累计喵。
	ClearPendingCandidateBilling(ctx)
	assert.Equal(t, relaycommon.VirtualBillingAmount{}, billing.PendingCandidate())
	assert.False(t, SettlePendingCandidateBilling(ctx, relayInfo))
	assert.Equal(t, 0, billing.Total().Quota)
}

// TestAccumulateVirtualRequestBillingIgnoresNonVirtualAndZero 覆盖请求级累计的写入门槛喵。
func TestAccumulateVirtualRequestBillingIgnoresNonVirtualAndZero(t *testing.T) {
	gin.SetMode(gin.TestMode)
	plainCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	// 喵~防御：非虚拟请求不能因为累计而被污染喵。
	assert.NotPanics(t, func() {
		accumulateVirtualRequestBilling(plainCtx, relaycommon.VirtualBillingAmount{Quota: 100})
	})

	ctx := newVirtualBillingTestContext()
	billing := virtualBillingFromContext(ctx)
	// 喵~防御：零金额写入无意义，直接忽略喵。
	accumulateVirtualRequestBilling(ctx, relaycommon.VirtualBillingAmount{})
	assert.Equal(t, 0, billing.Total().Quota)

	accumulateVirtualRequestBilling(ctx, relaycommon.VirtualBillingAmount{Quota: 100, PromptTokens: 5})
	accumulateVirtualRequestBilling(ctx, relaycommon.VirtualBillingAmount{Quota: 50, PromptTokens: 1})
	assert.Equal(t, 150, billing.Total().Quota)
	assert.Equal(t, 6, billing.Total().PromptTokens)
}
