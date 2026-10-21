package service

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBillingSessionUsesEffectiveRequestGroupForSubscriptionWhitelist(t *testing.T) {
	truncate(t)
	gin.SetMode(gin.TestMode)

	user := &model.User{
		Username: "subscription-whitelist",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    100,
	}
	require.NoError(t, model.DB.Create(user).Error)
	plan := &model.SubscriptionPlan{
		Title:                   "VIP subscription",
		DurationUnit:            model.SubscriptionDurationMonth,
		DurationValue:           1,
		TotalAmount:             100,
		WalletOnlyGroupsEnabled: true,
		WalletOnlyGroupsMode:    "whitelist",
		WalletOnlyGroups:        "vip",
	}
	require.NoError(t, model.DB.Create(plan).Error)
	model.InvalidateSubscriptionPlanCache(plan.Id)
	subscription := &model.UserSubscription{
		UserId:      user.Id,
		PlanId:      plan.Id,
		AmountTotal: plan.TotalAmount,
		Status:      "active",
		StartTime:   time.Now().Add(-time.Hour).Unix(),
		EndTime:     time.Now().Add(time.Hour).Unix(),
	}
	require.NoError(t, model.DB.Create(subscription).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		RequestId:       "subscription-whitelist-request",
		UserId:          user.Id,
		UserGroup:       user.Group,
		UsingGroup:      "vip",
		OriginModelName: "test-model",
		IsPlayground:    true,
		UserSetting: dto.UserSetting{
			BillingPreference: "subscription_first",
		},
	}

	session, apiErr := NewBillingSession(ctx, info, 10)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	assert.Equal(t, BillingSourceSubscription, info.BillingSource)
	assert.Equal(t, subscription.Id, info.SubscriptionId)

	var reloadedUser model.User
	require.NoError(t, model.DB.Select("quota").First(&reloadedUser, user.Id).Error)
	assert.Equal(t, 100, reloadedUser.Quota)
	var reloadedSubscription model.UserSubscription
	require.NoError(t, model.DB.Select("amount_used").First(&reloadedSubscription, subscription.Id).Error)
	assert.EqualValues(t, 10, reloadedSubscription.AmountUsed)
}

// recordingFundingSource 是记录调用次数的资金来源测试替身喵。
// 它不触碰数据库，只统计各生命周期方法被调用了几次，用于验证退款绝不重复执行喵。
type recordingFundingSource struct {
	source          string // 资金来源标识，决定订阅专属退款分支是否生效喵。
	preConsumeCalls int    // PreConsume 被调用的次数，单位：次喵。
	settleCalls     int    // Settle 被调用的次数，单位：次喵。
	refundCalls     int    // Refund 被调用的次数，这是重复退款的关键观测点喵。
	refundError     error  // 预设的退款错误，用于模拟资金来源退款失败喵。
}

// Source 返回预设的资金来源标识喵。
func (funding *recordingFundingSource) Source() string { return funding.source }

// PreConsume 记录一次预扣调用并始终成功喵。
func (funding *recordingFundingSource) PreConsume(amount int) error {
	// 累加调用计数，便于确认预扣阶段没有被退款路径误触喵。
	funding.preConsumeCalls++
	return nil
}

// Settle 记录一次结算调用并始终成功喵。
func (funding *recordingFundingSource) Settle(delta int) error {
	// 累加调用计数，用例据此断言已退款的会话绝不会再提交结算喵。
	funding.settleCalls++
	return nil
}

// Refund 记录一次退款调用并返回预设错误喵。
func (funding *recordingFundingSource) Refund() error {
	// 累加调用计数，钱包退款是非幂等加法操作，这个计数必须恒为一喵。
	funding.refundCalls++
	return funding.refundError
}

// newReservedBillingSessionForTest 构造一个处于 reserved 阶段且持有可退额度的计费会话喵。
// 通过 IsPlayground 跳过真实令牌额度回退，让用例只观察资金来源与订阅额外预扣两个步骤喵。
func newReservedBillingSessionForTest(funding *recordingFundingSource, subscriptionId int, extraReserved int) *BillingSession {
	// relayInfo 只填充退款路径会读取的字段，其余保持零值确保失败原因唯一喵。
	relayInfo := &relaycommon.RelayInfo{
		UserId:       4242,
		IsPlayground: true,
		// SubscriptionId 指向不存在的订阅时，订阅额外预扣回滚步骤会失败，用于模拟部分失败喵。
		SubscriptionId: subscriptionId,
	}
	return &BillingSession{
		relayInfo:        relayInfo,
		funding:          funding,
		preConsumedQuota: 100,
		// tokenConsumed 大于零才会让 needsRefund 判定仍有额度待退还喵。
		tokenConsumed: 100,
		extraReserved: extraReserved,
		state:         billingAttemptStateReserved,
	}
}

// TestBillingSessionRefundSkipsCompletedStepsAfterPartialFailure 验证退款部分失败后重试不会重复退还资金喵。
// 这是虚拟模型候选切换引入的真实风险：RefundImmediately 失败后外层 defer 仍会再调用一次退款喵。
func TestBillingSessionRefundSkipsCompletedStepsAfterPartialFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	// 资金来源退款成功，但订阅额外预扣回滚会因订阅不存在而失败喵。
	funding := &recordingFundingSource{source: BillingSourceSubscription}
	session := newReservedBillingSessionForTest(funding, 999999, 50)

	// 第一次退款：资金来源已退还，订阅额外预扣回滚失败，整体返回错误喵。
	firstRefundError := session.RefundImmediately(ctx)
	require.Error(t, firstRefundError)
	require.Equal(t, 1, funding.refundCalls)
	// 仍有步骤未完成，会话必须留在 reserved 阶段让上层可以重试喵。
	require.Equal(t, billingAttemptStateReserved, session.state)
	require.True(t, session.NeedsRefund())

	// 第二次退款：关键断言，已成功的资金来源退款绝不能被重复执行喵。
	secondRefundError := session.RefundImmediately(ctx)
	require.Error(t, secondRefundError)
	require.Equal(t, 1, funding.refundCalls)

	// 订阅编号归零后额外预扣回滚变成空操作，退款得以补齐剩余步骤喵。
	session.relayInfo.SubscriptionId = 0
	require.NoError(t, session.RefundImmediately(ctx))
	require.Equal(t, billingAttemptStateRefunded, session.state)
	require.False(t, session.NeedsRefund())
	// 补齐剩余步骤时同样不允许再次调用资金来源退款喵。
	require.Equal(t, 1, funding.refundCalls)

	// 进入终态后的任何退款调用都必须是幂等空操作喵。
	require.NoError(t, session.RefundImmediately(ctx))
	require.Equal(t, 1, funding.refundCalls)
}

// TestBillingSessionRetriesFundingRefundWhenFundingStepFailed 验证资金来源退款失败时允许重试该步骤喵。
func TestBillingSessionRetriesFundingRefundWhenFundingStepFailed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	// 首次退款让资金来源直接失败，此时没有任何额度被退还喵。
	funding := &recordingFundingSource{source: BillingSourceWallet, refundError: errors.New("wallet refund unavailable")}
	session := newReservedBillingSessionForTest(funding, 0, 0)

	require.Error(t, session.RefundImmediately(ctx))
	require.Equal(t, 1, funding.refundCalls)
	// 资金来源未成功退还时不得标记完成，否则用户额度会被永久占用喵。
	require.Equal(t, billingAttemptStateReserved, session.state)

	// 故障恢复后重试必须真正再执行一次资金来源退款并进入终态喵。
	funding.refundError = nil
	require.NoError(t, session.RefundImmediately(ctx))
	require.Equal(t, 2, funding.refundCalls)
	require.Equal(t, billingAttemptStateRefunded, session.state)
}

// TestBillingSessionSettleAfterRefundIsRejected 验证已退款的会话拒绝再次结算喵。
func TestBillingSessionSettleAfterRefundIsRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	// 先让会话完整退款进入终态喵。
	funding := &recordingFundingSource{source: BillingSourceWallet}
	session := newReservedBillingSessionForTest(funding, 0, 0)
	require.NoError(t, session.RefundImmediately(ctx))

	// 关键断言：额度已退还后再结算会凭空多扣一次费用，必须报错并且不触碰资金来源喵。
	settleError := session.Settle(300)
	require.Error(t, settleError)
	require.Contains(t, settleError.Error(), "already refunded")
	require.Zero(t, funding.settleCalls)
	require.Equal(t, billingAttemptStateRefunded, session.state)
}

// TestBillingSessionRefundAfterSettleIsNoOp 验证已结算的会话不会被退款路径二次改动额度喵。
func TestBillingSessionRefundAfterSettleIsNoOp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	// 实际消耗与预扣一致时结算无需调整资金来源，会话直接进入 settled 终态喵。
	funding := &recordingFundingSource{source: BillingSourceWallet}
	session := newReservedBillingSessionForTest(funding, 0, 0)
	require.NoError(t, session.Settle(100))
	require.Equal(t, billingAttemptStateSettled, session.state)

	// 关键断言：已结算会话的退款必须是空操作，否则用户会白拿一次预扣额度喵。
	require.NoError(t, session.RefundImmediately(ctx))
	require.Zero(t, funding.refundCalls)
	require.False(t, session.NeedsRefund())
	// 重复结算按幂等成功处理，且不得再次提交资金来源喵。
	require.NoError(t, session.Settle(100))
	require.Zero(t, funding.settleCalls)
}

// TestBillingSessionReserveRejectedAfterTerminalState 验证终态会话不再补扣额度喵。
func TestBillingSessionReserveRejectedAfterTerminalState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	// 先完整退款，让会话进入 refunded 终态喵。
	funding := &recordingFundingSource{source: BillingSourceWallet}
	session := newReservedBillingSessionForTest(funding, 0, 0)
	require.NoError(t, session.RefundImmediately(ctx))

	// 终态会话的补扣必须在守卫处提前返回；若守卫失效会因替身资金来源不受支持而返回错误喵。
	require.NoError(t, session.Reserve(500))
	// 预扣额度不得被终态后的补扣改动，否则日志与实际额度变动会对不上账喵。
	require.Equal(t, 100, session.GetPreConsumedQuota())
}

// TestBillingSessionAsyncRefundRunsOnlyOnce 验证并发触发异步退款只会真正退还一次喵。
func TestBillingSessionAsyncRefundRunsOnlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	funding := &recordingFundingSource{source: BillingSourceWallet}
	session := newReservedBillingSessionForTest(funding, 0, 0)

	// 连续两次触发异步退款，模拟外层 defer 与错误分支同时收尾的情况喵。
	session.Refund(ctx)
	session.Refund(ctx)

	// 等待异步退款协程完成；退款必须恰好执行一次且会话进入终态喵。
	require.Eventually(t, func() bool {
		session.mu.Lock()
		defer session.mu.Unlock()
		return session.state == billingAttemptStateRefunded
	}, 3*time.Second, 10*time.Millisecond)
	require.Equal(t, 1, funding.refundCalls)

	// 终态之后再次触发退款必须是空操作喵。
	session.Refund(ctx)
	require.Equal(t, 1, funding.refundCalls)
}

// seedWalletFallbackSubscription 创建一个总额受限的订阅，供钱包兜底用例使用喵。
func seedWalletFallbackSubscription(t *testing.T, userId int, total int64) *model.UserSubscription {
	t.Helper()
	// 套餐只需要总额字段，其它默认值由模型归一化逻辑补齐喵。
	plan := &model.SubscriptionPlan{
		Title:         "wallet-fallback",
		DurationUnit:  model.SubscriptionDurationMonth,
		DurationValue: 1,
		TotalAmount:   total,
	}
	// 写入套餐后才能被订阅引用喵。
	require.NoError(t, model.DB.Create(plan).Error)
	// 失效套餐缓存，保证预扣读到刚创建的套餐喵。
	model.InvalidateSubscriptionPlanCache(plan.Id)
	// 订阅有效期覆盖当前时间，确保被预扣查询选中喵。
	subscription := &model.UserSubscription{
		UserId:      userId,
		PlanId:      plan.Id,
		AmountTotal: total,
		Status:      "active",
		StartTime:   time.Now().Add(-time.Hour).Unix(),
		EndTime:     time.Now().Add(time.Hour).Unix(),
	}
	// 落库后返回给用例继续断言喵。
	require.NoError(t, model.DB.Create(subscription).Error)
	return subscription
}

// TestSubscriptionSettleFallsBackToNegativeWallet 验证结算差额超出订阅额度时改扣钱包且允许负数喵。
func TestSubscriptionSettleFallsBackToNegativeWallet(t *testing.T) {
	// 清理用例之间的数据污染喵。
	truncate(t)
	gin.SetMode(gin.TestMode)
	// 用户初始余额为 0，便于观察钱包被扣成负数喵。
	user := &model.User{Username: "settle-wallet-fallback", Password: "password", Status: common.UserStatusEnabled, Group: "default", Quota: 0}
	require.NoError(t, model.DB.Create(user).Error)
	// 订阅总额 10，只够初始预扣喵。
	subscription := seedWalletFallbackSubscription(t, user.Id, 10)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	// 构造订阅优先的请求信息喵。
	info := &relaycommon.RelayInfo{
		RequestId:       "settle-wallet-fallback-request",
		UserId:          user.Id,
		UserGroup:       "default",
		UsingGroup:      "default",
		OriginModelName: "test-model",
		IsPlayground:    true,
		UserSetting:     dto.UserSetting{BillingPreference: "subscription_first"},
	}
	// 预扣 10，订阅刚好用尽喵。
	session, apiErr := NewBillingSession(ctx, info, 10)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	require.Equal(t, subscription.Id, info.SubscriptionId)
	// 实际消耗 20，差额 10 由钱包兜底喵。
	require.NoError(t, session.Settle(20))
	// 钱包余额应被扣成 -10喵。
	var reloadedUser model.User
	require.NoError(t, model.DB.Select("quota").First(&reloadedUser, user.Id).Error)
	require.Equal(t, -10, reloadedUser.Quota)
	// 订阅只承担了初始预扣的 10，不应再增加喵。
	var reloadedSubscription model.UserSubscription
	require.NoError(t, model.DB.Select("amount_used").First(&reloadedSubscription, subscription.Id).Error)
	require.EqualValues(t, 10, reloadedSubscription.AmountUsed)
	// 钱包兜底部分不计入订阅差额日志喵。
	require.Zero(t, info.SubscriptionPostDelta)
	// 结算完成后会话必须进入终态喵。
	require.Equal(t, billingAttemptStateSettled, session.state)
}

// TestSubscriptionReserveFallsBackToNegativeWalletAndRefundsSplit 验证补预扣分摊到钱包后可原路退回喵。
func TestSubscriptionReserveFallsBackToNegativeWalletAndRefundsSplit(t *testing.T) {
	// 清理用例之间的数据污染喵。
	truncate(t)
	gin.SetMode(gin.TestMode)
	// 用户初始余额为 0，便于观察钱包被扣成负数喵。
	user := &model.User{Username: "reserve-wallet-fallback", Password: "password", Status: common.UserStatusEnabled, Group: "default", Quota: 0}
	require.NoError(t, model.DB.Create(user).Error)
	// 订阅总额 20，先由订阅承担一部分，再由钱包兜底剩余部分喵。
	subscription := seedWalletFallbackSubscription(t, user.Id, 20)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	// 构造订阅优先的请求信息喵。
	info := &relaycommon.RelayInfo{
		RequestId:       "reserve-wallet-fallback-request",
		UserId:          user.Id,
		UserGroup:       "default",
		UsingGroup:      "default",
		OriginModelName: "test-model",
		IsPlayground:    true,
		UserSetting:     dto.UserSetting{BillingPreference: "subscription_first"},
	}
	// 初始预扣 5，订阅剩余 15喵。
	session, apiErr := NewBillingSession(ctx, info, 5)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	// 第一次补预扣 5，全部由订阅承担喵。
	require.NoError(t, session.Reserve(10))
	// 第二次补预扣 15，订阅只剩 10，预期由钱包兜底 15喵。
	require.NoError(t, session.Reserve(25))
	// 钱包余额应被扣成 -15喵。
	var reloadedUser model.User
	require.NoError(t, model.DB.Select("quota").First(&reloadedUser, user.Id).Error)
	require.Equal(t, -15, reloadedUser.Quota)
	// 订阅承担初始 5 加补预扣 5，共 10喵。
	var reloadedSubscription model.UserSubscription
	require.NoError(t, model.DB.Select("amount_used").First(&reloadedSubscription, subscription.Id).Error)
	require.EqualValues(t, 10, reloadedSubscription.AmountUsed)
	// 取到订阅资金来源，直接验证分摊退款逻辑喵。
	funding, ok := session.funding.(*SubscriptionFunding)
	require.True(t, ok)
	require.Equal(t, 5, funding.extraSubscriptionReserved)
	require.Equal(t, 15, funding.extraWalletReserved)
	// 退款后钱包恢复为 0，订阅只保留初始预扣的 5喵。
	require.NoError(t, funding.refundExtraReserved())
	require.NoError(t, model.DB.Select("quota").First(&reloadedUser, user.Id).Error)
	require.Zero(t, reloadedUser.Quota)
	require.NoError(t, model.DB.Select("amount_used").First(&reloadedSubscription, subscription.Id).Error)
	require.EqualValues(t, 5, reloadedSubscription.AmountUsed)
	// 重复退款必须被分摊标记挡住，钱包不能再次加额喵。
	require.NoError(t, funding.refundExtraReserved())
	require.NoError(t, model.DB.Select("quota").First(&reloadedUser, user.Id).Error)
	require.Zero(t, reloadedUser.Quota)
}

// TestPostConsumeQuotaLegacySubscriptionFallsBackToNegativeWallet 验证旧结算路径订阅补扣失败时改扣钱包喵。
func TestPostConsumeQuotaLegacySubscriptionFallsBackToNegativeWallet(t *testing.T) {
	// 清理用例之间的数据污染喵。
	truncate(t)
	// 用户初始余额为 0，便于观察钱包被扣成负数喵。
	user := &model.User{Username: "legacy-settle-wallet-fallback", Password: "password", Status: common.UserStatusEnabled, Group: "default", Quota: 0}
	require.NoError(t, model.DB.Create(user).Error)
	// 订阅总额 5 且已用尽，后续补扣必须由钱包兜底喵。
	subscription := seedWalletFallbackSubscription(t, user.Id, 5)
	require.NoError(t, model.DB.Model(&model.UserSubscription{}).Where("id = ?", subscription.Id).Update("amount_used", 5).Error)
	// 构造旧结算路径需要的请求信息喵。
	info := &relaycommon.RelayInfo{
		UserId:         user.Id,
		BillingSource:  BillingSourceSubscription,
		SubscriptionId: subscription.Id,
		IsPlayground:   true,
	}
	// 直接调用旧结算函数，补扣 8 预期由钱包承担喵。
	result, err := postConsumeQuotaWithResult(info, 8, 0, false)
	require.NoError(t, err)
	require.True(t, result.FundingApplied)
	// 钱包余额应被扣成 -8喵。
	var reloadedUser model.User
	require.NoError(t, model.DB.Select("quota").First(&reloadedUser, user.Id).Error)
	require.Equal(t, -8, reloadedUser.Quota)
	// 订阅已用额度保持 5，不应被钱包兜底部分污染喵。
	var reloadedSubscription model.UserSubscription
	require.NoError(t, model.DB.Select("amount_used").First(&reloadedSubscription, subscription.Id).Error)
	require.EqualValues(t, 5, reloadedSubscription.AmountUsed)
	// 钱包兜底部分不计入订阅差额日志喵。
	require.Zero(t, info.SubscriptionPostDelta)
}
