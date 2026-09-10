package service

import (
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

const (
	BillingSourceWallet       = "wallet"
	BillingSourceSubscription = "subscription"
)

// PreConsumeBilling 根据用户计费偏好创建 BillingSession 并执行预扣费。
// 会话存储在 relayInfo.Billing 上，供后续 Settle / Refund 使用。
func PreConsumeBilling(c *gin.Context, preConsumedQuota int, relayInfo *relaycommon.RelayInfo) *types.NewAPIError {
	if relayInfo != nil && relayInfo.QuotaClamp != nil {
		return types.NewErrorWithStatusCode(
			relayInfo.QuotaClamp,
			types.ErrorCodeModelPriceError,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
	if preConsumedQuota < 0 {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("pre-consume quota cannot be negative: %d", preConsumedQuota),
			types.ErrorCodeModelPriceError,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
	session, apiErr := NewBillingSession(c, relayInfo, preConsumedQuota)
	if apiErr != nil {
		return apiErr
	}
	relayInfo.Billing = session
	return nil
}

// ---------------------------------------------------------------------------
// SettleBilling — 后结算辅助函数
// ---------------------------------------------------------------------------

// SettleBilling 执行计费结算。如果 RelayInfo 上有 BillingSession 则通过 session 结算，
// 否则回退到旧的 PostConsumeQuota 路径（兼容按次计费等场景）。
func SettleBilling(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, actualQuota int) error {
	if relayInfo.Billing != nil {
		preConsumed := relayInfo.Billing.GetPreConsumedQuota()
		delta := actualQuota - preConsumed

		if delta > 0 {
			logger.LogInfo(ctx, fmt.Sprintf("预扣费后补扣费：%s（实际消耗：%s，预扣费：%s）",
				logger.FormatQuota(delta),
				logger.FormatQuota(actualQuota),
				logger.FormatQuota(preConsumed),
			))
		} else if delta < 0 {
			logger.LogInfo(ctx, fmt.Sprintf("预扣费后返还扣费：%s（实际消耗：%s，预扣费：%s）",
				logger.FormatQuota(-delta),
				logger.FormatQuota(actualQuota),
				logger.FormatQuota(preConsumed),
			))
		} else {
			logger.LogInfo(ctx, fmt.Sprintf("预扣费与实际消耗一致，无需调整：%s（按次计费）",
				logger.FormatQuota(actualQuota),
			))
		}

		if err := relayInfo.Billing.Settle(actualQuota); err != nil {
			return err
		}

		// 发送额度通知（订阅计费使用订阅剩余额度）
		if actualQuota != 0 {
			if relayInfo.BillingSource == BillingSourceSubscription {
				checkAndSendSubscriptionQuotaNotify(relayInfo)
			} else {
				checkAndSendQuotaNotify(relayInfo, actualQuota-preConsumed, preConsumed)
			}
		}
		return nil
	}

	// 回退：无 BillingSession 时使用旧路径
	quotaDelta := actualQuota - relayInfo.FinalPreConsumedQuota
	if quotaDelta != 0 {
		return PostConsumeQuota(relayInfo, quotaDelta, relayInfo.FinalPreConsumedQuota, true)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 虚拟模型候选级计费：登记 / 结算 / 累计
// ---------------------------------------------------------------------------

// virtualBillingFromContext 读取虚拟模型请求的候选级与请求级计费累计容器喵。
//
// 输入：Gin 上下文喵。
// 输出：累计容器；非虚拟模型请求或尚未初始化时返回 nil，调用方据此跳过全部候选级计费逻辑喵。
func virtualBillingFromContext(c *gin.Context) *relaycommon.VirtualBilling {
	// 喵~防御：空上下文没有累计容器喵。
	if c == nil {
		return nil
	}
	value, found := common.GetContextKey(c, constant.ContextKeyVirtualBilling)
	// 喵~防御：普通请求没有该 key，直接返回 nil 保证行为不变喵。
	if !found {
		return nil
	}
	billing, ok := value.(*relaycommon.VirtualBilling)
	// 喵~防御：类型不符时按无累计处理，避免类型断言 panic 让请求失败喵。
	if !ok {
		return nil
	}
	return billing
}

// accumulateVirtualRequestBilling 把一次候选已结算的额度累加进虚拟模型请求级总额喵。
//
// 输入：Gin 上下文与本次结算金额（quota + token 口径）喵。
// 输出：无；非虚拟请求或零金额时不做任何写入喵。
func accumulateVirtualRequestBilling(c *gin.Context, amount relaycommon.VirtualBillingAmount) {
	billing := virtualBillingFromContext(c)
	// 喵~防御：非虚拟请求或零金额不写入，避免普通请求被污染喵。
	if billing == nil || amount.IsZero() {
		return
	}
	billing.AddSettled(amount)
}

// RegisterSkippedCandidateBilling 登记一个「上游已被调用、但即将被跳过」的候选计费额度喵。
//
// 整体思路喵：虚拟模型候选在放流前被判定卡流/空流/断流时会被跳过，但这类上游消耗在原生 new-api
// 口径下是会计费的（原生会按已有响应文本兜底估算 usage 并结算）。这里复用与 PostTextConsumeQuota
// 完全相同的算费链路算出额度，先登记到当前候选，等候选真正进入终态失败时再一次性结算喵。
//
// 输入：Gin 上下文、候选自己的 relay、按原生口径估算出的 usage（可为 nil，表示只有请求侧 token）喵。
// 输出：无；额度与 token 记入 context 的候选级登记，非虚拟请求不生效喵。
func RegisterSkippedCandidateBilling(c *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage) {
	// 喵~防御：缺少上下文、候选 relay 或非虚拟模型请求时跳过，普通请求绝不产生登记喵。
	if c == nil || relayInfo == nil || virtualBillingFromContext(c) == nil {
		return
	}
	originUsage := usage
	billingUsage := effectiveBillingUsage(usage)
	// 复用与成功结算同一套摘要计算，保证跳过候选与成功候选的价格口径一致喵。
	summary := calculateTextQuotaSummary(c, relayInfo, billingUsage)
	var tieredUsedVars map[string]bool
	if snap := relayInfo.TieredBillingSnapshot; snap != nil {
		tieredUsedVars = billingexpr.UsedVars(snap.ExprString)
	}
	// 阶梯计费与成功结算保持一致：只有拿到上游 usage 时才走阶梯表达式喵。
	if originUsage != nil {
		tieredOk, tieredQuota, tieredRes := TryTieredSettle(relayInfo, BuildTieredTokenParams(billingUsage, summary.IsClaudeUsageSemantic, tieredUsedVars))
		if tieredOk {
			summary.Quota = composeTieredTextQuota(relayInfo, summary, tieredQuota, tieredRes)
		}
	}
	// 喵~防御：任何情况下都不得登记负数额度，异常时钳到零并留日志，避免跳过候选反而给用户退钱喵。
	quota := summary.Quota
	if quota < 0 {
		common.SysError(fmt.Sprintf("virtual model skipped candidate quota is negative: %d, model=%s", quota, summary.ModelName))
		quota = 0
	}
	virtualBillingFromContext(c).RegisterPendingCandidate(relaycommon.VirtualBillingAmount{
		Quota:            quota,
		PromptTokens:     summary.PromptTokens,
		CompletionTokens: summary.CompletionTokens,
	})
}

// SettlePendingCandidateBilling 在候选进入终态失败时结算其登记的计费额度喵。
//
// 输入：Gin 上下文与当前候选的 relay 喵。
// 输出：true 表示已按登记额度结算（计费会话进入 settled 终态，调用方随后的退款调用会自动幂等失效）；
// false 表示没有待结算登记或结算失败，调用方应按原逻辑退还预扣喵。
func SettlePendingCandidateBilling(c *gin.Context, relayInfo *relaycommon.RelayInfo) bool {
	billing := virtualBillingFromContext(c)
	// 喵~防御：非虚拟请求或缺少候选 relay 时不做任何结算喵。
	if billing == nil || relayInfo == nil {
		return false
	}
	pendingAmount, foundPending := billing.TakePendingCandidate()
	// 喵~防御：没有登记额度时返回 false，让候选切换继续走原有退款路径喵。
	if !foundPending {
		return false
	}
	// 免费候选或未建立计费会话时没有可结算的预扣，但仍要把登记额度计入请求级总额供日志对账喵。
	if relayInfo.Billing != nil {
		if settleErr := SettleBilling(c, relayInfo, pendingAmount.Quota); settleErr != nil {
			logger.LogError(c, "虚拟模型跳过候选结算失败: "+settleErr.Error())
			// 结算失败时不声明已结算，让调用方继续退款，避免额度既没扣也没退喵。
			return false
		}
	}
	// 已扣费额度同步进用户与渠道的已用额度统计；跳过候选不增加请求数，避免请求数被候选链放大喵。
	model.UpdateUserUsedQuota(relayInfo.UserId, pendingAmount.Quota)
	model.UpdateChannelUsedQuota(relayInfo.ChannelId, pendingAmount.Quota)
	accumulateVirtualRequestBilling(c, pendingAmount)
	return true
}

// ClearPendingCandidateBilling 丢弃当前候选登记的计费额度喵。
//
// 候选最终成功时调用：成功路径会按真实 usage 结算，探测阶段登记的估算额度不能再计一次喵。
func ClearPendingCandidateBilling(c *gin.Context) {
	billing := virtualBillingFromContext(c)
	// 喵~防御：非虚拟请求没有登记可清理喵。
	if billing == nil {
		return
	}
	billing.ClearPendingCandidate()
}
