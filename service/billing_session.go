package service

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// BillingSession — 统一计费会话
// ---------------------------------------------------------------------------

// billingAttemptState 表示单次计费尝试所处的生命周期阶段喵。
// 用显式状态取代多个互相牵制的布尔标志，避免"已退款还能再结算"这类组合非法却无人拦截喵。
type billingAttemptState int

const (
	// billingAttemptStatePending 表示会话已创建但尚未成功预扣任何额度喵。
	billingAttemptStatePending billingAttemptState = iota
	// billingAttemptStateReserved 表示预扣已完成，会话正持有可退还或可结算的额度喵。
	billingAttemptStateReserved
	// billingAttemptStateSettled 是终态，表示额度已按实际消耗结算，禁止再退款喵。
	billingAttemptStateSettled
	// billingAttemptStateRefunded 是终态，表示预扣额度已全部退还，禁止再结算喵。
	billingAttemptStateRefunded
)

// String 返回状态的可读名称，仅用于日志与错误信息喵。
func (state billingAttemptState) String() string {
	// 按枚举值逐一映射，未知取值统一返回 unknown 以便排查异常喵。
	switch state {
	case billingAttemptStatePending:
		return "pending"
	case billingAttemptStateReserved:
		return "reserved"
	case billingAttemptStateSettled:
		return "settled"
	case billingAttemptStateRefunded:
		return "refunded"
	default:
		return "unknown"
	}
}

// BillingSession 封装单次请求的预扣费/结算/退款生命周期。
// 实现 relaycommon.BillingSettler 接口。
//
// 生命周期喵：pending -> reserved -> settled 或 refunded，两个终态互斥且不可回退喵。
// 虚拟模型候选切换会在同一个请求里创建多个会话，每个候选各自独立走完这条状态机喵。
type BillingSession struct {
	relayInfo        *relaycommon.RelayInfo
	funding          FundingSource
	preConsumedQuota int                 // 实际预扣额度（信任用户可能为 0）
	tokenConsumed    int                 // 令牌额度实际扣减量
	extraReserved    int                 // 发送前补充预扣的额度（订阅退款时需要单独回滚）
	trusted          bool                // 是否命中信任额度旁路
	fundingSettled   bool                // funding.Settle 已成功，资金来源已提交
	state            billingAttemptState // 当前计费尝试所处的生命周期阶段喵。
	fundingRefunded  bool                // funding.Refund 已成功，重试退款时必须跳过该步骤喵。
	extraRefunded    bool                // 订阅额外预扣已回滚，重试退款时必须跳过该步骤喵。
	tokenRefunded    bool                // 令牌预扣额度已退回，重试退款时必须跳过该步骤喵。
	refundInFlight   bool                // 是否已有异步退款协程在执行，避免并发重复退款喵。
	mu               sync.Mutex
}

// isTerminalLocked 判断会话是否已进入结算或退款终态，调用前必须持有会话锁喵。
func (s *BillingSession) isTerminalLocked() bool {
	// 两个终态都意味着资金动作已完成，后续任何资金操作都必须被拒绝或忽略喵。
	return s.state == billingAttemptStateSettled || s.state == billingAttemptStateRefunded
}

// Settle 根据实际消耗额度进行结算。
// 资金来源和令牌额度分两步提交：若资金来源已提交但令牌调整失败，
// 会标记 fundingSettled 防止 Refund 对已提交的资金来源执行退款。
func (s *BillingSession) Settle(actualQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 重复结算按幂等成功处理，避免上层多次调用造成二次扣费喵。
	if s.state == billingAttemptStateSettled {
		return nil
	}
	// 喵~防御：预扣额度已经退还后再结算会凭空多扣一次费用，必须显式报错而不是静默继续喵。
	if s.state == billingAttemptStateRefunded {
		return fmt.Errorf("billing session already refunded (state=%s), refusing to settle", s.state)
	}
	delta := actualQuota - s.preConsumedQuota
	if delta == 0 {
		s.state = billingAttemptStateSettled
		return nil
	}
	// 1) 调整资金来源（仅在尚未提交时执行，防止重复调用）
	if !s.fundingSettled {
		if err := s.funding.Settle(delta); err != nil {
			return err
		}
		s.fundingSettled = true
	}
	// 2) 调整令牌额度
	var tokenErr error
	if !s.relayInfo.IsPlayground {
		if delta > 0 {
			tokenErr = model.DecreaseTokenQuota(s.relayInfo.TokenId, s.relayInfo.TokenKey, delta)
		} else {
			tokenErr = model.IncreaseTokenQuota(s.relayInfo.TokenId, s.relayInfo.TokenKey, -delta)
		}
		if tokenErr != nil {
			// 资金来源已提交，令牌调整失败只能记录日志；标记 settled 防止 Refund 误退资金
			common.SysLog(fmt.Sprintf("error adjusting token quota after funding settled (userId=%d, tokenId=%d, delta=%d): %s",
				s.relayInfo.UserId, s.relayInfo.TokenId, delta, tokenErr.Error()))
		}
	}
	// 3) 更新 relayInfo 上的订阅 PostDelta（用于日志）
	if s.funding.Source() == BillingSourceSubscription {
		s.relayInfo.SubscriptionPostDelta += int64(delta)
	}
	s.state = billingAttemptStateSettled
	return tokenErr
}

// refundLocked 按步骤退还本次尝试的全部预扣额度，调用前必须持有会话锁喵。
//
// 整体思路喵：退款由"资金来源、订阅额外预扣、令牌额度"三个独立步骤组成，
// 其中钱包退款 IncreaseUserQuota 是非幂等的加法操作，重复执行会把额度多退给用户喵。
// 因此每个步骤成功后都会被单独标记，任一步骤失败时只返回错误而不改变已完成标记，
// 这样上层重试退款时会跳过已完成步骤，既不会多退也不会漏退喵。
// 全部步骤成功后才把会话推进到 refunded 终态喵。
//
// 输出：nil 表示全部步骤已完成；非 nil 表示仍有步骤待重试，会话保持在 reserved 阶段喵。
func (s *BillingSession) refundLocked() error {
	// 已进入终态或本就没有可退额度时按幂等成功返回喵。
	if s.isTerminalLocked() || !s.needsRefundLocked() {
		return nil
	}
	// 喵~防御：资金来源缺失时无法安全退款，必须报错让调用方感知额度仍被占用喵。
	if s.funding == nil {
		return errors.New("billing funding source is unavailable")
	}
	// 第一步：退还资金来源预扣；只有尚未成功退还过才执行，避免钱包被重复加额喵。
	if !s.fundingRefunded {
		if refundError := s.funding.Refund(); refundError != nil {
			return refundError
		}
		s.fundingRefunded = true
	}
	// 第二步：回滚发送前补充预扣的订阅额度；该额度不在 funding.Refund 覆盖范围内喵。
	if !s.extraRefunded {
		if s.extraReserved > 0 && s.funding.Source() == BillingSourceSubscription && s.relayInfo.SubscriptionId > 0 {
			if refundError := model.PostConsumeUserSubscriptionDelta(s.relayInfo.SubscriptionId, -int64(s.extraReserved)); refundError != nil {
				return refundError
			}
		}
		s.extraRefunded = true
	}
	// 第三步：退还令牌维度的预扣额度；Playground 请求不扣令牌额度所以直接跳过喵。
	if !s.tokenRefunded {
		if s.tokenConsumed > 0 && !s.relayInfo.IsPlayground {
			if refundError := model.IncreaseTokenQuota(s.relayInfo.TokenId, s.relayInfo.TokenKey, s.tokenConsumed); refundError != nil {
				return refundError
			}
		}
		s.tokenRefunded = true
	}
	s.state = billingAttemptStateRefunded
	return nil
}

// RefundImmediately 同步退还所有预扣费，供同一个请求切换虚拟模型内部候选前释放旧候选额度喵。
// 主人注意：该路径持有计费会话锁直到退款结束，仅用于请求内候选切换，以避免并发结算或二次退款破坏额度一致性喵。
func (s *BillingSession) RefundImmediately(c *gin.Context) error {
	// 喵~防御：空计费会话不需要退款，直接作为幂等成功返回喵。
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 已结算、已退款或无预扣状态时不得再次增加余额，避免重复退款喵。
	if s.isTerminalLocked() || !s.needsRefundLocked() {
		return nil
	}
	// 喵~防御：资金来源缺失时不能继续切换候选，避免旧预扣额度永久锁定喵。
	if s.funding == nil {
		return errors.New("billing funding source is unavailable")
	}
	logger.LogInfo(c, fmt.Sprintf("用户 %d 切换虚拟模型候选，立即返还失败候选预扣费（token_quota=%s, funding=%s）",
		s.relayInfo.UserId,
		logger.FormatQuota(s.tokenConsumed),
		s.funding.Source(),
	))
	// 退款步骤全部成功才会进入 refunded 终态；部分失败时保持 reserved，交由外层 defer 重试剩余步骤喵。
	return s.refundLocked()
}

// Refund 退还所有预扣费，幂等安全，异步执行。
func (s *BillingSession) Refund(c *gin.Context) {
	// 喵~防御：空计费会话没有任何预扣状态，直接返回避免空指针喵。
	if s == nil {
		return
	}
	s.mu.Lock()
	// 已进入终态、无可退额度或已有退款协程在跑时都不再重复投递喵。
	if s.isTerminalLocked() || !s.needsRefundLocked() || s.refundInFlight {
		s.mu.Unlock()
		return
	}
	// 标记退款进行中，防止并发调用同时投递两个协程导致钱包被多退一次喵。
	s.refundInFlight = true
	logger.LogInfo(c, fmt.Sprintf("用户 %d 请求失败, 返还预扣费（token_quota=%s, funding=%s）",
		s.relayInfo.UserId,
		logger.FormatQuota(s.tokenConsumed),
		s.funding.Source(),
	))
	s.mu.Unlock()

	gopool.Go(func() {
		// 主人注意：退款在协程内持有会话锁执行数据库操作，锁粒度等同 RefundImmediately，
		// 目的是让"哪些步骤已完成"的记录与实际数据库变更保持原子一致喵。
		s.mu.Lock()
		defer s.mu.Unlock()
		// 无论成功与否都要清除进行中标记，让后续重试仍有机会补齐剩余步骤喵。
		defer func() { s.refundInFlight = false }()
		// 退款失败只能记录日志；已完成步骤不会被重复执行，剩余步骤留待下一次退款调用重试喵。
		if refundError := s.refundLocked(); refundError != nil {
			common.SysLog("error refunding pre-consumed billing quota: " + refundError.Error())
		}
	})
}

// NeedsRefund 返回是否存在需要退还的预扣状态。
func (s *BillingSession) NeedsRefund() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.needsRefundLocked()
}

func (s *BillingSession) needsRefundLocked() bool {
	if s.isTerminalLocked() || s.fundingSettled {
		// 终态说明资金动作已完成，fundingSettled 说明资金来源已提交结算，都不能再退预扣费
		return false
	}
	if s.tokenConsumed > 0 {
		return true
	}
	// 订阅可能在 tokenConsumed=0 时仍预扣了额度
	if sub, ok := s.funding.(*SubscriptionFunding); ok && sub.preConsumed > 0 {
		return true
	}
	return false
}

// GetPreConsumedQuota 返回实际预扣的额度。
func (s *BillingSession) GetPreConsumedQuota() int {
	// 与其他生命周期方法共用会话锁，避免结算与补扣并发时读到撕裂的中间值喵。
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.preConsumedQuota
}

func (s *BillingSession) Reserve(targetQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 已进入终态、命中信任旁路或目标额度未超过当前预扣时都不需要补扣喵。
	if s.isTerminalLocked() || s.trusted || targetQuota <= s.preConsumedQuota {
		return nil
	}

	delta := targetQuota - s.preConsumedQuota
	if delta <= 0 {
		return nil
	}

	if err := s.reserveFunding(delta); err != nil {
		return err
	}
	if err := s.reserveToken(delta); err != nil {
		s.rollbackFundingReserve(delta)
		return err
	}

	s.preConsumedQuota += delta
	s.tokenConsumed += delta
	s.extraReserved += delta
	s.syncRelayInfo()
	return nil
}

// ---------------------------------------------------------------------------
// PreConsume — 统一预扣费入口（含信任额度旁路）
// ---------------------------------------------------------------------------

// preConsume 执行预扣费：信任检查 -> 令牌预扣 -> 资金来源预扣。
// 任一步骤失败时原子回滚已完成的步骤。
func (s *BillingSession) preConsume(c *gin.Context, quota int) *types.NewAPIError {
	effectiveQuota := quota

	// ---- 信任额度旁路 ----
	if s.shouldTrust(c) {
		s.trusted = true
		effectiveQuota = 0
		logger.LogInfo(c, fmt.Sprintf("用户 %d 额度充足, 信任且不需要预扣费 (funding=%s)", s.relayInfo.UserId, s.funding.Source()))
	} else if effectiveQuota > 0 {
		logger.LogInfo(c, fmt.Sprintf("用户 %d 需要预扣费 %s (funding=%s)", s.relayInfo.UserId, logger.FormatQuota(effectiveQuota), s.funding.Source()))
	}

	// ---- 1) 预扣令牌额度 ----
	if effectiveQuota > 0 {
		if err := PreConsumeTokenQuota(s.relayInfo, effectiveQuota); err != nil {
			return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		s.tokenConsumed = effectiveQuota
	}

	// ---- 2) 预扣资金来源 ----
	if err := s.funding.PreConsume(effectiveQuota); err != nil {
		// 预扣费失败，回滚令牌额度
		if s.tokenConsumed > 0 && !s.relayInfo.IsPlayground {
			if rollbackErr := model.IncreaseTokenQuota(s.relayInfo.TokenId, s.relayInfo.TokenKey, s.tokenConsumed); rollbackErr != nil {
				common.SysLog(fmt.Sprintf("error rolling back token quota (userId=%d, tokenId=%d, amount=%d, fundingErr=%s): %s",
					s.relayInfo.UserId, s.relayInfo.TokenId, s.tokenConsumed, err.Error(), rollbackErr.Error()))
			}
			s.tokenConsumed = 0
		}
		// TODO: model 层应定义哨兵错误（如 ErrNoActiveSubscription），用 errors.Is 替代字符串匹配
		if errors.Is(err, ErrInsufficientWalletQuota) {
			userQuota, quotaErr := model.GetUserQuota(s.relayInfo.UserId, false)
			if quotaErr != nil {
				userQuota = 0
			}
			return types.NewErrorWithStatusCode(
				fmt.Errorf("用户额度不足, 剩余额度: %s", logger.FormatQuota(userQuota)),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		errMsg := err.Error()
		if strings.Contains(errMsg, "no active subscription") || strings.Contains(errMsg, "subscription quota insufficient") {
			return types.NewErrorWithStatusCode(fmt.Errorf("订阅额度不足或未配置订阅: %s", errMsg), types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}

	s.preConsumedQuota = effectiveQuota
	// 预扣阶段完成，会话进入持有额度的 reserved 阶段，后续只能走结算或退款其中一条路喵。
	s.state = billingAttemptStateReserved

	// ---- 同步 RelayInfo 兼容字段 ----
	s.syncRelayInfo()

	return nil
}

func (s *BillingSession) reserveFunding(delta int) error {
	switch funding := s.funding.(type) {
	case *WalletFunding:
		if err := funding.ReserveAdditional(delta); err != nil {
			return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
		}
		return nil
	case *SubscriptionFunding:
		if err := model.PostConsumeUserSubscriptionDelta(funding.subscriptionId, int64(delta)); err != nil {
			return types.NewErrorWithStatusCode(
				fmt.Errorf("订阅额度不足或未配置订阅: %s", err.Error()),
				types.ErrorCodeInsufficientUserQuota,
				http.StatusForbidden,
				types.ErrOptionWithSkipRetry(),
				types.ErrOptionWithNoRecordErrorLog(),
			)
		}
		return nil
	default:
		return types.NewError(fmt.Errorf("unsupported funding source: %s", s.funding.Source()), types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
}

func (s *BillingSession) rollbackFundingReserve(delta int) {
	switch funding := s.funding.(type) {
	case *WalletFunding:
		funding.RollbackAdditionalReserve()
	case *SubscriptionFunding:
		if err := model.PostConsumeUserSubscriptionDelta(funding.subscriptionId, -int64(delta)); err != nil {
			common.SysLog("error rolling back subscription funding reserve: " + err.Error())
		}
	}
}

func (s *BillingSession) reserveToken(delta int) error {
	if delta <= 0 || s.relayInfo.IsPlayground {
		return nil
	}
	if err := PreConsumeTokenQuota(s.relayInfo, delta); err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
	return nil
}

// shouldTrust 统一信任额度检查，适用于钱包和订阅。
func (s *BillingSession) shouldTrust(c *gin.Context) bool {
	// 异步任务（ForcePreConsume=true）必须预扣全额，不允许信任旁路
	if s.relayInfo.ForcePreConsume {
		return false
	}

	trustQuota := common.GetTrustQuota()
	if trustQuota <= 0 {
		return false
	}

	// 检查令牌是否充足
	tokenTrusted := s.relayInfo.TokenUnlimited
	if !tokenTrusted {
		tokenQuota := c.GetInt("token_quota")
		tokenTrusted = tokenQuota > trustQuota
	}
	if !tokenTrusted {
		return false
	}

	switch s.funding.Source() {
	case BillingSourceWallet:
		return s.relayInfo.UserQuota > trustQuota
	case BillingSourceSubscription:
		// 订阅不能启用信任旁路。原因：
		// 1. PreConsumeUserSubscription 要求 amount>0 来创建预扣记录并锁定订阅
		// 2. SubscriptionFunding.PreConsume 忽略参数，始终用 s.amount 预扣
		// 3. 若信任旁路将 effectiveQuota 设为 0，会导致 preConsumedQuota 与实际订阅预扣不一致
		return false
	default:
		return false
	}
}

// syncRelayInfo 将 BillingSession 的状态同步到 RelayInfo 的兼容字段上。
func (s *BillingSession) syncRelayInfo() {
	info := s.relayInfo
	info.FinalPreConsumedQuota = s.preConsumedQuota
	info.BillingSource = s.funding.Source()

	if sub, ok := s.funding.(*SubscriptionFunding); ok {
		info.SubscriptionId = sub.subscriptionId
		info.SubscriptionPreConsumed = sub.preConsumed + int64(s.extraReserved)
		info.SubscriptionPostDelta = 0
		info.SubscriptionAmountTotal = sub.AmountTotal
		info.SubscriptionAmountUsedAfterPreConsume = sub.AmountUsedAfter + int64(s.extraReserved)
		info.SubscriptionPlanId = sub.PlanId
		info.SubscriptionPlanTitle = sub.PlanTitle
	} else {
		info.SubscriptionId = 0
		info.SubscriptionPreConsumed = 0
	}
}

// ---------------------------------------------------------------------------
// NewBillingSession 工厂 — 根据计费偏好创建会话并处理回退
// ---------------------------------------------------------------------------

// NewBillingSession 根据用户计费偏好创建 BillingSession，处理 subscription_first / wallet_first 的回退。
func NewBillingSession(c *gin.Context, relayInfo *relaycommon.RelayInfo, preConsumedQuota int) (*BillingSession, *types.NewAPIError) {
	if relayInfo == nil {
		return nil, types.NewError(fmt.Errorf("relayInfo is nil"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	pref := common.NormalizeBillingPreference(relayInfo.UserSetting.BillingPreference)

	// 钱包路径需要先检查用户额度
	tryWallet := func() (*BillingSession, *types.NewAPIError) {
		userQuota, err := model.GetUserQuota(relayInfo.UserId, false)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		checkinCredit := 0
		if checkinGroupEligible(relayInfo.UsingGroup) {
			checkinCredit, _ = model.GetUserCheckinQuota(relayInfo.UserId)
		}
		if userQuota <= 0 && checkinCredit <= 0 {
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("用户额度不足, 剩余额度: %s", logger.FormatQuota(userQuota)),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		if userQuota+checkinCredit-preConsumedQuota < 0 {
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("预扣费额度失败, 用户剩余额度: %s, 需要预扣费额度: %s", logger.FormatQuota(userQuota), logger.FormatQuota(preConsumedQuota)),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		relayInfo.UserQuota = userQuota

		walletFunding := &WalletFunding{userId: relayInfo.UserId}
		walletFunding.SetCheckinEligible(checkinGroupEligible(relayInfo.UsingGroup))
		session := &BillingSession{
			relayInfo: relayInfo,
			funding:   walletFunding,
		}
		if apiErr := session.preConsume(c, preConsumedQuota); apiErr != nil {
			return nil, apiErr
		}
		return session, nil
	}

	trySubscription := func() (*BillingSession, *types.NewAPIError) {
		subConsume := int64(preConsumedQuota)
		if subConsume <= 0 {
			subConsume = 1
		}
		session := &BillingSession{
			relayInfo: relayInfo,
			funding: &SubscriptionFunding{
				requestId: relayInfo.RequestId,
				userId:    relayInfo.UserId,
				modelName: relayInfo.OriginModelName,
				group:     relayInfo.UsingGroup,
				amount:    subConsume,
			},
		}
		// 必须传 subConsume 而非 preConsumedQuota，保证 SubscriptionFunding.amount、
		// preConsume 参数和 FinalPreConsumedQuota 三者一致，避免订阅多扣费。
		if apiErr := session.preConsume(c, int(subConsume)); apiErr != nil {
			return nil, apiErr
		}
		return session, nil
	}

	checkSubscriptionAvailability := func() (bool, bool, *types.NewAPIError) {
		hasAny, hasEligible, err := model.GetActiveSubscriptionAvailabilityForGroup(relayInfo.UserId, relayInfo.UsingGroup)
		if err != nil {
			return false, false, types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		return hasAny, hasEligible, nil
	}

	switch pref {
	case "subscription_only":
		hasAny, hasEligible, apiErr := checkSubscriptionAvailability()
		if apiErr != nil {
			return nil, apiErr
		}
		if hasAny && !hasEligible {
			return tryWallet()
		}
		return trySubscription()
	case "wallet_only":
		return tryWallet()
	case "wallet_first":
		session, walletErr := tryWallet()
		if walletErr != nil {
			if walletErr.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
				hasAny, hasEligible, apiErr := checkSubscriptionAvailability()
				if apiErr != nil {
					return nil, apiErr
				}
				if hasAny && !hasEligible {
					return nil, walletErr
				}
				return trySubscription()
			}
			return nil, walletErr
		}
		return session, nil
	case "subscription_first":
		fallthrough
	default:
		hasAny, hasEligibleSubscription, apiErr := checkSubscriptionAvailability()
		if apiErr != nil {
			return nil, apiErr
		}
		if hasAny && !hasEligibleSubscription {
			return tryWallet()
		}
		if !hasEligibleSubscription {
			return tryWallet()
		}
		session, apiErr := trySubscription()
		if apiErr != nil {
			if apiErr.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
				// 仅当用户的活跃订阅允许钱包回退时才回退到钱包，否则返回订阅额度不足错误
				allowOverflow, overflowErr := model.UserActiveSubscriptionsAllowWalletOverflowForGroup(relayInfo.UserId, relayInfo.UsingGroup)
				if overflowErr != nil {
					return nil, types.NewError(overflowErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
				}
				if allowOverflow {
					return tryWallet()
				}
				return nil, apiErr
			}
			return nil, apiErr
		}
		return session, nil
	}
}
