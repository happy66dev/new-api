package controller

import (
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// reliefSiteKey 救济池绑定的站点：救济金按 AutoCheckin 的 AgentRouter 池余额计算喵~
const reliefSiteKey = "agentrouter"

// reliefEligibility 汇总一次救济金领取资格评估的结果，GET 展示与 POST 领取共用喵~
type reliefEligibility struct {
	eligible          bool    // 是否满足全部领取条件
	reasonCode        string  // 不满足时的机器码（前端据此翻译成本地化文案），满足为空串
	cooldownRemaining int64   // 冷却剩余秒数，0 表示已可领
	poolUsd           float64 // AgentRouter 池余额（美元）
	outstandingUsd    float64 // 已发放且仍占用救济池的额度 x（美元）
	remainingUsd      float64 // 救济金剩余（美元，可为负）= 池余额 - 储备金 - x
	grantUsd          float64 // 单次发放额度（美元），优先取套餐真实总额
}

// evaluateRelief 评估某用户此刻的救济金领取资格喵~
// 输入：用户 id、救济金配置、当前时间戳（秒）
// 输出：评估结果与错误；任何底层查询失败都如实上抛，不吞错
func evaluateRelief(userId int, setting *operation_setting.ReliefSetting, now int64) (reliefEligibility, error) {
	// 评估结果，先填入与资格无关的池/额度数值
	result := reliefEligibility{}

	// AgentRouter 池余额（美元）：来自后台定时刷新的只读快照，不阻塞请求
	result.poolUsd = service.GetAutoCheckinPoolUSD(reliefSiteKey)

	// 已发放且仍占池的额度 x（内部 quota），换算成美元
	outstandingQuota, err := model.ReliefOutstandingQuota()
	if err != nil {
		// 喵~防御：x 算不出来直接上抛，调用方回错误而不是展示错数
		return result, err
	}
	// QuotaPerUnit 是"1 美元 = 多少 quota"的换算系数
	result.outstandingUsd = float64(outstandingQuota) / common.QuotaPerUnit

	// 救济金剩余 = 池余额 - 储备金 - 已发放 x（后端允许为负，前端再夹 0 展示）
	result.remainingUsd = result.poolUsd - setting.ReserveAmount - result.outstandingUsd

	// 单次发放额度：优先用救济金套餐的真实总额，取不到再回落到配置值
	result.grantUsd = setting.GrantAmountUsd
	if setting.PlanId > 0 {
		if plan, planErr := model.GetSubscriptionPlanById(setting.PlanId); planErr == nil && plan.TotalAmount > 0 {
			// 套餐总额是内部 quota，换算成美元
			result.grantUsd = float64(plan.TotalAmount) / common.QuotaPerUnit
		}
	}

	// 条件一：用户钱包余额必须低于门槛（美元换算成 quota 再比）
	userQuota, err := model.GetUserQuota(userId, true)
	if err != nil {
		return result, err
	}
	// 门槛额度（内部 quota）：美元门槛 * 换算系数，钱包与订阅可用额度共用这同一个门槛
	thresholdQuota := setting.BalanceThresholdUsd * common.QuotaPerUnit
	balanceLow := float64(userQuota) < thresholdQuota

	// 条件二：用户当前没有"可用"的订阅。
	// 口径（主人拍板）：订阅各项额度（五小时/周/总额度等）剩余低于门槛（同为约 10 美元）就不算可用，
	// 所以这里把同一个门槛额度传下去判断。
	hasConsumable, err := model.UserHasConsumableSubscription(userId, int64(thresholdQuota))
	if err != nil {
		return result, err
	}

	// 条件三：冷却已过。冷却秒数 = 配置小时 * 3600
	cooldownSeconds := int64(setting.CooldownHours * 3600)
	lastGrant, err := model.LastReliefGrantTime(userId)
	if err != nil {
		return result, err
	}
	// 冷却剩余 = 上次领取时刻 + 冷却秒数 - 现在，夹到 >=0
	remaining := lastGrant + cooldownSeconds - now
	if remaining < 0 {
		remaining = 0
	}
	result.cooldownRemaining = remaining

	// 条件四：救济池还够发一笔
	poolHasRoom := result.remainingUsd >= result.grantUsd

	// 按优先级给出"为什么不能领"的机器码；全部满足才算 eligible
	switch {
	case !balanceLow:
		result.reasonCode = "balance_too_high"
	case hasConsumable:
		result.reasonCode = "subscription_available"
	case remaining > 0:
		result.reasonCode = "cooldown"
	case !poolHasRoom:
		result.reasonCode = "pool_empty"
	default:
		result.eligible = true
	}

	return result, nil
}

// GetReliefStatus 处理 GET /api/user/relief：返回救济金卡片所需的全部展示数据与领取资格喵~
func GetReliefStatus(c *gin.Context) {
	// 读取救济金配置（指针，仅读不改）
	setting := operation_setting.GetReliefSetting()
	// 功能未启用：直接回一个 enabled=false 的精简数据，前端据此隐藏卡片
	if !setting.Enabled {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    gin.H{"enabled": false},
		})
		return
	}

	// 从登录态拿当前用户 id
	userId := c.GetInt("id")
	// 当前时间戳（秒），用于算冷却剩余
	now := common.GetTimestamp()

	// 评估领取资格（含池余额、x、剩余、冷却等）
	eligibility, err := evaluateRelief(userId, setting, now)
	if err != nil {
		// 喵~防御：底层查询失败如实回错，不展示错数据误导用户
		common.ApiError(c, err)
		return
	}

	// 前端展示的"救济金剩余"夹到 >=0（后端值可为负，仅用于判断够不够发）
	remainingForDisplay := eligibility.remainingUsd
	if remainingForDisplay < 0 {
		remainingForDisplay = 0
	}

	// 组装返回体：金额字段统一用美元，冷却用秒，reason_code 交前端翻译
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"enabled":                    true,                          // 功能已启用
			"eligible":                   eligibility.eligible,          // 本次是否可领
			"reason_code":                eligibility.reasonCode,        // 不可领的机器码（前端本地化）
			"remaining_usd":              remainingForDisplay,           // 救济金剩余（美元，已夹 0）
			"grant_amount_usd":           eligibility.grantUsd,          // 单次发放额度（美元）
			"balance_threshold_usd":      setting.BalanceThresholdUsd,   // 领取门槛（美元）
			"cooldown_hours":             setting.CooldownHours,         // 冷却时长（小时）
			"cooldown_remaining_seconds": eligibility.cooldownRemaining, // 冷却剩余（秒）
		},
	})
}

// DoReliefClaim 处理 POST /api/user/relief/claim：再校验资格后发放一笔救济金订阅喵~
func DoReliefClaim(c *gin.Context) {
	// 读取救济金配置
	setting := operation_setting.GetReliefSetting()
	// 功能未启用直接拒绝
	if !setting.Enabled {
		common.ApiErrorMsg(c, "救济金功能未启用")
		return
	}
	// 喵~防御：套餐未配置（plan_id<=0）时无法发放，提示管理员去配置
	if setting.PlanId <= 0 {
		common.ApiErrorMsg(c, "救济金套餐未配置，请联系管理员")
		return
	}

	// 当前用户与时间戳
	userId := c.GetInt("id")
	now := common.GetTimestamp()

	// 领取前再评估一次资格（防止前端绕过或状态已变）
	eligibility, err := evaluateRelief(userId, setting, now)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// 不满足条件：按机器码翻译成中性中文提示（对外文案不带语气词）
	if !eligibility.eligible {
		common.ApiErrorMsg(c, reliefReasonMessage(eligibility.reasonCode))
		return
	}

	// 冷却秒数，传入事务做并发安全的最终复查
	cooldownSeconds := int64(setting.CooldownHours * 3600)
	// 真正发放：事务内锁用户行、复查冷却与不叠加，再从套餐创建一张 relief 订阅
	sub, err := model.GrantReliefSubscription(userId, setting.PlanId, cooldownSeconds)
	if err != nil {
		// 发放失败（冷却中/已有有效救济金/套餐异常等）：原样回其错误消息
		common.ApiErrorMsg(c, err.Error())
		return
	}

	// 记一条用户系统日志，便于主人对账：本次发放了多少额度
	model.RecordLog(userId, model.LogTypeSystem, fmt.Sprintf("领取救济金，获得额度 %s（有效期至订阅到期）", logger.LogQuota(int(sub.AmountTotal))))

	// 返回成功：带上发放额度与订阅到期时间，前端可据此刷新余额与倒计时
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "领取成功",
		"data": gin.H{
			"amount_total": sub.AmountTotal, // 本次发放的总额度（内部 quota）
			"end_time":     sub.EndTime,     // 订阅到期时间戳（秒）
		},
	})
}

// reliefReasonMessage 把领取失败的机器码翻译成中性中文提示（对外文案，禁用语气词）喵~
func reliefReasonMessage(reasonCode string) string {
	// 逐个机器码映射到用户可读的中性中文
	switch reasonCode {
	case "balance_too_high":
		// 余额高于门槛
		return "当前钱包余额高于领取门槛，暂不可领取"
	case "subscription_available":
		// 还有可用订阅额度
		return "您当前仍有可用的订阅额度，暂不可领取"
	case "cooldown":
		// 冷却中
		return "救济金冷却中，请稍后再试"
	case "pool_empty":
		// 救济池不足
		return "救济池额度不足，暂时无法发放"
	default:
		// 兜底：未知原因统一提示不满足条件
		return "当前不满足救济金领取条件"
	}
}
