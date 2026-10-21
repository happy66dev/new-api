package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// ---------------------------------------------------------------------------
// FundingSource — 资金来源接口（钱包 or 订阅）
// ---------------------------------------------------------------------------

// FundingSource 抽象了预扣费的资金来源。
type FundingSource interface {
	// Source 返回资金来源标识："wallet" 或 "subscription"
	Source() string
	// PreConsume 从该资金来源预扣 amount 额度
	PreConsume(amount int) error
	// Settle 根据差额调整资金来源（正数补扣，负数退还）
	Settle(delta int) error
	// Refund 退还所有预扣费
	Refund() error
}

// ---------------------------------------------------------------------------
// WalletFunding — 钱包资金来源实现
// ---------------------------------------------------------------------------

// ErrInsufficientWalletQuota 钱包原子预扣失败（余额不足），未发生任何扣减。
// BillingSession 据此映射为 ErrorCodeInsufficientUserQuota，
// 使 wallet_first 等计费偏好可以回退到订阅。
var ErrInsufficientWalletQuota = errors.New("wallet quota insufficient")

func checkinGroupEligible(group string) bool {
	configured := operation_setting.GetCheckinSetting().DeductibleGroups
	for _, candidate := range strings.Split(configured, ",") {
		if strings.TrimSpace(candidate) == group {
			return true
		}
	}
	return false
}

type WalletFunding struct {
	userId             int
	consumed           int // 实际预扣的钱包额度
	checkinConsumed    int
	checkinEligible    bool
	lastReserveCheckin int
	lastReserveWallet  int
}

func (w *WalletFunding) Source() string { return BillingSourceWallet }

func (w *WalletFunding) PreConsume(amount int) error {
	if amount <= 0 {
		return nil
	}
	checkinBefore := w.checkinConsumed
	remaining := amount
	var err error
	remaining, err = w.consumeCheckinCredit(remaining)
	if err != nil {
		return err
	}
	reserved, err := model.TryReserveUserQuota(w.userId, remaining)
	if err != nil {
		w.restoreCheckinCredit(w.checkinConsumed - checkinBefore)
		return err
	}
	if !reserved {
		w.restoreCheckinCredit(w.checkinConsumed - checkinBefore)
		return ErrInsufficientWalletQuota
	}
	w.consumed += remaining
	return nil
}

func (w *WalletFunding) Settle(delta int) error {
	if delta == 0 {
		return nil
	}
	if delta > 0 {
		checkinBefore := w.checkinConsumed
		remaining, err := w.consumeCheckinCredit(delta)
		if err != nil {
			return err
		}
		if remaining == 0 {
			return nil
		}
		if err := model.DecreaseUserQuota(w.userId, remaining, false); err != nil {
			w.restoreCheckinCredit(w.checkinConsumed - checkinBefore)
			return err
		}
		return nil
	}
	refund := -delta
	if w.checkinConsumed > 0 {
		creditRefund := refund
		if creditRefund > w.checkinConsumed {
			creditRefund = w.checkinConsumed
		}
		if err := model.IncreaseUserCheckinQuota(w.userId, creditRefund); err != nil {
			return err
		}
		w.checkinConsumed -= creditRefund
		refund -= creditRefund
	}
	if refund == 0 {
		return nil
	}
	return model.IncreaseUserQuota(w.userId, refund, false)
}

func (w *WalletFunding) Refund() error {
	if w.checkinConsumed > 0 {
		if err := model.IncreaseUserCheckinQuota(w.userId, w.checkinConsumed); err != nil {
			return err
		}
		w.checkinConsumed = 0
	}
	if w.consumed <= 0 {
		return nil
	}
	// IncreaseUserQuota 是 quota += N 的非幂等操作，不能重试，否则会多退额度。
	// 订阅的 RefundSubscriptionPreConsume 有 requestId 幂等保护所以可以重试。
	return model.IncreaseUserQuota(w.userId, w.consumed, false)
}

func (w *WalletFunding) SetCheckinEligible(value bool) { w.checkinEligible = value }

// ReserveAdditional applies the same source priority as the initial pre-consume
// while allowing the wallet portion to go negative for post-estimate settlement.
func (w *WalletFunding) ReserveAdditional(amount int) error {
	w.lastReserveCheckin = 0
	w.lastReserveWallet = 0
	if amount <= 0 {
		return nil
	}
	remainingBefore := amount
	remaining, err := w.consumeCheckinCredit(amount)
	if err != nil {
		return err
	}
	w.lastReserveCheckin = remainingBefore - remaining
	if remaining == 0 {
		return nil
	}
	if err := model.DecreaseUserQuota(w.userId, remaining, false); err != nil {
		w.restoreCheckinCredit(w.lastReserveCheckin)
		w.lastReserveCheckin = 0
		return err
	}
	w.consumed += remaining
	w.lastReserveWallet = remaining
	return nil
}

func (w *WalletFunding) RollbackAdditionalReserve() {
	if w.lastReserveWallet > 0 {
		if err := model.IncreaseUserQuota(w.userId, w.lastReserveWallet, false); err != nil {
			common.SysLog("error rolling back wallet funding reserve: " + err.Error())
		} else {
			w.consumed -= w.lastReserveWallet
		}
	}
	if w.lastReserveCheckin > 0 {
		w.restoreCheckinCredit(w.lastReserveCheckin)
	}
	w.lastReserveWallet = 0
	w.lastReserveCheckin = 0
}

// consumeCheckinCredit applies available check-in credit before wallet quota.
// The conditional update in TryReserveUserCheckinQuota keeps concurrent requests
// from consuming the same credit twice.
func (w *WalletFunding) consumeCheckinCredit(amount int) (int, error) {
	if amount <= 0 || !w.checkinEligible || operation_setting.GetCheckinSetting().DeductibleGroups == "" {
		return amount, nil
	}
	available, err := model.GetUserCheckinQuota(w.userId)
	if err != nil {
		return amount, nil
	}
	credit := available
	if credit > amount {
		credit = amount
	}
	if credit <= 0 {
		return amount, nil
	}
	reserved, err := model.TryReserveUserCheckinQuota(w.userId, credit)
	if err != nil {
		return amount, err
	}
	if !reserved {
		return amount, nil
	}
	w.checkinConsumed += credit
	return amount - credit, nil
}

func (w *WalletFunding) restoreCheckinCredit(amount int) {
	if amount <= 0 {
		return
	}
	if err := model.IncreaseUserCheckinQuota(w.userId, amount); err != nil {
		common.SysLog("error restoring check-in credit: " + err.Error())
		return
	}
	w.checkinConsumed -= amount
}

// ---------------------------------------------------------------------------
// SubscriptionFunding — 订阅资金来源实现
// ---------------------------------------------------------------------------

type SubscriptionFunding struct {
	requestId      string
	userId         int
	modelName      string
	group          string
	amount         int64 // 预扣的订阅额度（subConsume）
	subscriptionId int
	preConsumed    int64
	// 以下字段在 PreConsume 成功后填充，供 RelayInfo 同步使用
	AmountTotal     int64
	AmountUsedAfter int64
	PlanId          int
	PlanTitle       string
	// subscriptionDeltaApplied 记录本次结算中真正记入订阅的增量，正数补扣、负数退款，单位：额度喵。
	subscriptionDeltaApplied int
	// extraSubscriptionReserved 记录 Reserve 补预扣中由订阅承担的额度，单位：额度喵。
	extraSubscriptionReserved int
	// extraWalletReserved 记录 Reserve 补预扣中由钱包兜底的额度，允许钱包余额为负数，单位：额度喵。
	extraWalletReserved int
	// lastReserveSubscriptionDelta 记录最近一次 Reserve 中订阅实际承担的增量，供令牌回滚使用，单位：额度喵。
	lastReserveSubscriptionDelta int
	// lastReserveWalletDelta 记录最近一次 Reserve 中钱包实际承担的增量，供令牌回滚使用，单位：额度喵。
	lastReserveWalletDelta int
	// extraSubscriptionRefunded 标记 Reserve 的订阅部分是否已成功退回，防止重试时重复退款喵。
	extraSubscriptionRefunded bool
	// extraWalletRefunded 标记 Reserve 的钱包部分是否已成功退回，防止重试时重复退款喵。
	extraWalletRefunded bool
}

func (s *SubscriptionFunding) Source() string { return BillingSourceSubscription }

func (s *SubscriptionFunding) PreConsume(_ int) error {
	// amount 参数被忽略，使用内部 s.amount（已在构造时根据 preConsumedQuota 计算）
	res, err := model.PreConsumeUserSubscriptionForGroup(s.requestId, s.userId, s.modelName, 0, s.amount, s.group)
	if err != nil {
		return err
	}
	s.subscriptionId = res.UserSubscriptionId
	s.preConsumed = res.PreConsumed
	s.AmountTotal = res.AmountTotal
	s.AmountUsedAfter = res.AmountUsedAfter
	// 获取订阅计划信息
	if planInfo, err := model.GetSubscriptionPlanInfoByUserSubscriptionId(res.UserSubscriptionId); err == nil && planInfo != nil {
		s.PlanId = planInfo.PlanId
		s.PlanTitle = planInfo.PlanTitle
	}
	return nil
}

func (s *SubscriptionFunding) Settle(delta int) error {
	// 零增量无需改动任何资金来源，直接视为成功喵。
	if delta == 0 {
		return nil
	}
	// 负数增量表示退还多扣的额度，必须回到当初预扣的订阅，不能改退钱包喵。
	if delta < 0 {
		// 喵~防御：订阅编号缺失时无法定位退款目标，直接报错避免额度丢失喵。
		if s.subscriptionId <= 0 {
			return errors.New("subscription id is missing")
		}
		// 把负数增量记回原订阅，并记录实际记入订阅的增量供日志使用喵。
		if err := model.PostConsumeUserSubscriptionDelta(s.subscriptionId, int64(delta)); err != nil {
			return err
		}
		s.subscriptionDeltaApplied = delta
		return nil
	}
	// 正数增量优先记入原订阅；订阅总额或窗口额度不足时才用钱包兜底喵。
	if s.subscriptionId > 0 {
		// 尝试把补扣全部记入订阅喵。
		if err := model.PostConsumeUserSubscriptionDelta(s.subscriptionId, int64(delta)); err == nil {
			s.subscriptionDeltaApplied = delta
			return nil
		} else {
			// 记录订阅补扣失败原因，便于管理员核对为何改扣钱包喵。
			common.SysLog(fmt.Sprintf("subscription settle fallback to wallet: subscriptionId=%d, userId=%d, delta=%d, reason=%s", s.subscriptionId, s.userId, delta, err.Error()))
		}
	}
	// 钱包兜底允许余额为负数，保证实际消耗不会被漏扣喵。
	if err := model.DecreaseUserQuota(s.userId, delta, false); err != nil {
		return err
	}
	// 本次正增量没有记入订阅，因此订阅侧增量为零喵。
	s.subscriptionDeltaApplied = 0
	return nil
}

// refundExtraReserved 退还 Reserve 阶段分摊到订阅与钱包的两部分额度喵。
// 两部分分别打标记，任一步失败后重试只补做未完成的部分，避免重复退款喵。
func (s *SubscriptionFunding) refundExtraReserved() error {
	// 订阅部分尚未退还且确有额度时才执行，负增量写回订阅是幂等保护外的普通增量操作喵。
	if !s.extraSubscriptionRefunded && s.extraSubscriptionReserved > 0 {
		// 喵~防御：订阅编号缺失时无法退款，必须报错让上层保留待重试状态喵。
		if s.subscriptionId <= 0 {
			return errors.New("subscription id is missing")
		}
		// 把 Reserve 时记入订阅的额度原路退回喵。
		if err := model.PostConsumeUserSubscriptionDelta(s.subscriptionId, -int64(s.extraSubscriptionReserved)); err != nil {
			return err
		}
		// 标记订阅部分已完成，后续重试不再重复退回喵。
		s.extraSubscriptionRefunded = true
	}
	// 钱包部分尚未退还且确有额度时才执行，钱包加额是非幂等操作喵。
	if !s.extraWalletRefunded && s.extraWalletReserved > 0 {
		// 把 Reserve 时从钱包兜底扣掉的额度退回用户余额喵。
		if err := model.IncreaseUserQuota(s.userId, s.extraWalletReserved, false); err != nil {
			return err
		}
		// 标记钱包部分已完成，后续重试不再重复退回喵。
		s.extraWalletRefunded = true
	}
	return nil
}

func (s *SubscriptionFunding) Refund() error {
	if s.preConsumed <= 0 {
		return nil
	}
	return refundWithRetry(func() error {
		return model.RefundSubscriptionPreConsume(s.requestId)
	})
}

// refundWithRetry 尝试多次执行退款操作以提高成功率，只能用于基于事务的退款函数！！！！！！
// try to refund with retries, only for refund functions based on transactions!!!
func refundWithRetry(fn func() error) error {
	if fn == nil {
		return nil
	}
	const maxAttempts = 3
	var lastErr error
	for i := range maxAttempts {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if i < maxAttempts-1 {
			time.Sleep(time.Duration(200*(i+1)) * time.Millisecond)
		}
	}
	return lastErr
}
