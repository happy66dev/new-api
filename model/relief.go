package model

import (
	"errors"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"gorm.io/gorm"
)

// ReliefSubscriptionSource 救济金订阅在 user_subscriptions.source 里的标记值，用它把救济订阅从普通订阅里筛出来喵~
const ReliefSubscriptionSource = "relief"

// ReliefPlanTitle 救济金隐藏套餐的标题：启动时据此查找/复用，避免重复创建喵~
const ReliefPlanTitle = "救济金"

// FindReliefPlan 按标题查找救济金隐藏套餐；查不到返回 (nil, nil) 表示"还没建过"喵~
func FindReliefPlan() (*SubscriptionPlan, error) {
	// 按标题取最早创建的那一条（正常只会有一条）
	var plan SubscriptionPlan
	query := DB.Where("title = ?", ReliefPlanTitle).Order("id asc").Limit(1).Find(&plan)
	if query.Error != nil {
		// 喵~防御：查询出错如实上抛，调用方据此决定是否放弃建套餐
		return nil, query.Error
	}
	// 一条都没有说明尚未创建过
	if query.RowsAffected == 0 {
		return nil, nil
	}
	return &plan, nil
}

// CreateReliefPlan 新建一张救济金隐藏套餐喵~
//
// 口径（主人拍板）：
//   - 总额度 grantUsd 美元（换算成内部 quota），有效期 1 天；
//   - 不按天/周/月重置（QuotaResetPeriod=never），不设五小时额度；
//   - 隐藏（Enabled=false）不在用户订阅商店展示；购买次数不限（领取叠加/冷却由发放逻辑控制）。
//
// 输入：单次发放额度（美元）
// 输出：创建好的套餐（含自增 id）或错误
func CreateReliefPlan(grantUsd float64) (*SubscriptionPlan, error) {
	// 喵~防御：发放额度必须为正，否则建出来的套餐没有意义
	if grantUsd <= 0 {
		return nil, errors.New("救济金发放额度必须为正")
	}
	// 美元换算成内部 quota：四舍五入到整数 quota，避免浮点误差留小数
	// $200 * 500000 = 1亿，远在 int64 安全范围内
	grantQuota := int64(math.Round(grantUsd * common.QuotaPerUnit))

	// 组装隐藏套餐：1 天有效、总额度制、无重置、无五小时/周/月子额度
	plan := &SubscriptionPlan{
		Title:               ReliefPlanTitle,          // 标题（兼作识别键）
		Subtitle:            "系统自动创建的救济金套餐，请勿手动购买或删除", // 给管理员看的说明（后台可见）
		PriceAmount:         0,                        // 不售卖，价格 0
		Currency:            "USD",                    // 币种占位
		DurationUnit:        SubscriptionDurationDay,  // 有效期单位：天
		DurationValue:       1,                        // 有效期 1 天
		Enabled:             false,                    // 隐藏：不在用户订阅商店展示
		AllowBalancePay:     common.GetPointer(false), // 不允许用钱包余额购买（本就不走购买流程）
		AllowWalletOverflow: common.GetPointer(false), // 救济额度用尽后不回落扣钱包（救济是封顶赠额）
		MaxPurchasePerUser:  0,                        // 0=不限次数：救济可反复领，叠加/冷却在发放逻辑里控制
		TotalAmount:         grantQuota,               // 总额度（内部 quota）
		FiveHourLimit:       0,                        // 不设五小时额度
		WeeklyLimit:         0,                        // 不设周额度
		MonthlyLimit:        0,                        // 不设月额度
		QuotaResetPeriod:    SubscriptionResetNever,   // 不重置：总额度一次性发放
	}
	// 落库创建
	if err := DB.Create(plan).Error; err != nil {
		// 喵~防御：创建失败上抛，让启动流程记录原因而不是静默吞掉
		return nil, err
	}
	// 刷新套餐缓存，确保随后立刻能按 id 读到
	InvalidateSubscriptionPlanCache(plan.Id)
	return plan, nil
}

// ReliefOutstandingQuota 计算全局"已发放且仍占用救济池"的额度 x，单位：内部 quota 喵~
//
// 口径（主人拍板）：
//   - 发放即占满：有效期内（status=active 且 end_time>now）的救济订阅，按**发放总额** amount_total 计入 x；
//   - 到期自动回收：其余（已过期/已取消/已失效）的救济订阅，只按**已用额** amount_used 计入 x。
//
// 于是某笔救济订阅一旦跨过 end_time，它对 x 的贡献就从"总额"降到"已用额"，
// 差额（未用部分）瞬间从 x 扣除 = 自动返还救济池，无需依赖后台任务的执行时机喵~
func ReliefOutstandingQuota() (int64, error) {
	// 当前数据库时间戳（秒），用于判定订阅是否仍在有效期内
	now := GetDBTimestamp()

	// 第一桶：仍在有效期内的救济订阅，按发放总额求和
	var activeTotal int64
	if err := DB.Model(&UserSubscription{}).
		Where("source = ? AND status = ? AND end_time > ?", ReliefSubscriptionSource, "active", now).
		Select("COALESCE(SUM(amount_total), 0)").
		Scan(&activeTotal).Error; err != nil {
		// 喵~防御：聚合查询失败直接把错误上抛，让调用方决定如何展示
		return 0, err
	}

	// 第二桶：不在有效期内（已过期/取消/到期）的救济订阅，只按已用额求和
	var restUsed int64
	if err := DB.Model(&UserSubscription{}).
		Where("source = ? AND NOT (status = ? AND end_time > ?)", ReliefSubscriptionSource, "active", now).
		Select("COALESCE(SUM(amount_used), 0)").
		Scan(&restUsed).Error; err != nil {
		// 喵~防御：第二桶失败同样上抛
		return 0, err
	}

	// x = 有效期内的发放总额 + 其余订阅的已用额
	return activeTotal + restUsed, nil
}

// reliefSubscriptionHasRemaining 判断单个订阅"是否还有足够可用额度"（各已配额度窗口剩余都不低于门槛）喵~
//
// 这是一个只读的近似判定（不真正写库做 reset）：对于已到重置点的窗口，视为已归零（剩余=满额）；
// 覆盖主人要求的"总额度 / 五小时 / 周 / 月"几类额度，任一已配窗口的剩余额度低于门槛就算该订阅不可用喵~
//
// 口径（主人拍板）：订阅可用额度（五小时/周/总额度等）低于门槛（约 10 美元，与钱包余额门槛一致）
// 就不再算"可用"——因为就算有一点点残额也发挥不了作用，用户仍应能领救济金喵~
//
// 输入：订阅、当前时间戳 now、单窗口可用额度门槛 minRemainingQuota（内部 quota 单位）
// 输出：true 表示仍有足够可用额度，false 表示不可用
func reliefSubscriptionHasRemaining(sub *UserSubscription, now int64, minRemainingQuota int64) bool {
	// 喵~防御：空指针按"不可用"处理，避免下面取字段崩溃
	if sub == nil {
		return false
	}

	// windowAvailable 判断单个额度窗口是否"够用"：剩余额度为正且不低于门槛
	// 输入：窗口上限 limit、已用 used、该窗口下次重置时间 nextReset
	// 已过重置点则本周期已用视为归零（剩余=满额）
	windowAvailable := func(limit, used, nextReset int64) bool {
		// 已过重置点的话，本周期已用视为归零
		if nextReset > 0 && nextReset <= now {
			used = 0
		}
		// 剩余额度 = 上限 - 已用
		remaining := limit - used
		// 必须剩余为正、且不低于门槛额度，才算该窗口可用
		return remaining > 0 && remaining >= minRemainingQuota
	}

	// 总额度：amount_total>0 才是有限额度（0=不限量、<0=纯权益不含额度，前者恒有余额、后者在调用处已排除）
	if sub.AmountTotal > 0 {
		if !windowAvailable(sub.AmountTotal, sub.AmountUsed, sub.NextResetTime) {
			return false
		}
	}

	// 五小时额度：配置了（limit>0）才检查
	if sub.FiveHourLimit > 0 {
		if !windowAvailable(sub.FiveHourLimit, sub.FiveHourUsed, sub.FiveHourNextResetTime) {
			return false
		}
	}

	// 周额度：同上
	if sub.WeeklyLimit > 0 {
		if !windowAvailable(sub.WeeklyLimit, sub.WeeklyUsed, sub.WeeklyNextResetTime) {
			return false
		}
	}

	// 月额度：同上
	if sub.MonthlyLimit > 0 {
		if !windowAvailable(sub.MonthlyLimit, sub.MonthlyUsed, sub.MonthlyNextResetTime) {
			return false
		}
	}

	// 所有已配额度窗口剩余都不低于门槛 → 这个订阅是"可用的"
	return true
}

// UserHasConsumableSubscription 判断用户当前是否还存在"可用"的订阅（任意一个订阅各项额度剩余都不低于门槛）喵~
//
// 口径（主人拍板）：看是否存在一个订阅，其各项额度（五小时/周/总额度等）剩余都不低于门槛 minRemainingQuota；
// 低于门槛（约 10 美元）的残额不算"可用"。返回 true 表示"订阅可用"（不符合领救济金条件）；false 表示"订阅不可用"。
//
// 输入：用户 id、单窗口可用额度门槛 minRemainingQuota（内部 quota 单位）
// 输出：是否存在可用订阅，以及错误
func UserHasConsumableSubscription(userId int, minRemainingQuota int64) (bool, error) {
	// 喵~防御：非法 userId 直接报错
	if userId <= 0 {
		return false, errors.New("invalid userId")
	}

	// 取用户当前生效的请求分组（用于判断"仅钱包"订阅对该分组是否可用）
	group, err := GetUserGroup(userId, false)
	if err != nil {
		return false, err
	}
	group = strings.TrimSpace(group)

	// 当前时间戳，用于筛有效订阅与模拟窗口重置
	now := GetDBTimestamp()

	// 捞出该用户所有仍有效（active 且未过期）的订阅
	var subs []UserSubscription
	if err := DB.Where("user_id = ? AND status = ? AND end_time > ?", userId, "active", now).
		Find(&subs).Error; err != nil {
		return false, err
	}

	// 逐个订阅判断，只要有一个"可用"就返回 true
	for i := range subs {
		sub := &subs[i]
		// 纯权益订阅（amount_total<0）不含额度，不作数
		if sub.AmountTotal < 0 {
			continue
		}
		// 读套餐，判断它对当前分组是不是"仅钱包"（仅钱包订阅不能消费订阅额度）
		plan, err := GetSubscriptionPlanById(sub.PlanId)
		if err != nil {
			// 喵~防御：套餐读不出就跳过这一个，不因单个脏数据误判整体
			continue
		}
		if plan.IsWalletOnlyForGroup(group) {
			continue
		}
		// 各项已配额度剩余都不低于门槛，说明存在可用订阅
		if reliefSubscriptionHasRemaining(sub, now, minRemainingQuota) {
			return true, nil
		}
	}

	// 没有任何可用订阅
	return false, nil
}

// LastReliefGrantTime 返回用户最近一次领取救济金的开始时间（unix 秒）；从未领过返回 0 喵~
func LastReliefGrantTime(userId int) (int64, error) {
	// 喵~防御：非法 userId 直接报错
	if userId <= 0 {
		return 0, errors.New("invalid userId")
	}
	// 按 start_time 倒序取最近一条救济订阅
	var last UserSubscription
	query := DB.Where("user_id = ? AND source = ?", userId, ReliefSubscriptionSource).
		Order("start_time desc, id desc").
		Limit(1).
		Find(&last)
	if query.Error != nil {
		return 0, query.Error
	}
	// 一条都没有说明从未领取过，返回 0
	if query.RowsAffected == 0 {
		return 0, nil
	}
	return last.StartTime, nil
}

// GrantReliefSubscription 给用户发放一笔救济金（= 创建一张 source=relief 的订阅）喵~
//
// 事务内再次做"冷却 + 不叠加"的最终校验，保证并发/多实例下不会重复发放：
//   - 锁用户行，使同一用户的领取请求串行；
//   - 冷却：距上次领取不足 cooldownSeconds 直接拒绝；
//   - 不叠加：已有有效救济订阅时拒绝（必须用完或到期才能再领）。
//
// 输入：用户 id、救济金套餐 id、冷却秒数
// 输出：本次发放的订阅，或错误
func GrantReliefSubscription(userId int, planId int, cooldownSeconds int64) (*UserSubscription, error) {
	// 喵~防御：参数非法直接报错
	if userId <= 0 || planId <= 0 {
		return nil, errors.New("invalid userId or planId")
	}

	// 先读套餐（走缓存），校验它确实是一个带额度的有效套餐
	plan, err := GetSubscriptionPlanById(planId)
	if err != nil {
		return nil, errors.New("救济金套餐不存在，请管理员在设置里配置")
	}
	// 喵~防御：救济金必须有正的发放总额，否则发了也没意义
	if plan.TotalAmount <= 0 {
		return nil, errors.New("救济金套餐未配置有效的发放总额")
	}

	// 当前数据库时间戳
	now := GetDBTimestamp()

	// 本次创建出来的订阅
	var created *UserSubscription

	err = DB.Transaction(func(tx *gorm.DB) error {
		// 锁用户行：让同一用户的并发领取串行执行，避免双发
		var userRow User
		if err := lockForUpdate(tx).Select("id").Where("id = ?", userId).First(&userRow).Error; err != nil {
			return err
		}

		// 冷却复查：取最近一条救济订阅的开始时间
		var last UserSubscription
		lastQuery := tx.Where("user_id = ? AND source = ?", userId, ReliefSubscriptionSource).
			Order("start_time desc, id desc").
			Limit(1).
			Find(&last)
		if lastQuery.Error != nil {
			return lastQuery.Error
		}
		// 距上次领取还没过冷却期就拒绝
		if lastQuery.RowsAffected > 0 && last.StartTime+cooldownSeconds > now {
			return errors.New("救济金冷却中，请稍后再来")
		}

		// 不叠加：已有一张仍有效的救济订阅时拒绝（必须用完或到期后才能再领）
		var activeCount int64
		if err := tx.Model(&UserSubscription{}).
			Where("user_id = ? AND source = ? AND status = ? AND end_time > ?", userId, ReliefSubscriptionSource, "active", now).
			Count(&activeCount).Error; err != nil {
			return err
		}
		if activeCount > 0 {
			return errors.New("已有有效救济金，用完或到期后再来领")
		}

		// 真正发放：用现成的"从套餐建订阅"事务函数，source 标记为 relief
		sub, err := CreateUserSubscriptionFromPlanTx(tx, userId, plan, ReliefSubscriptionSource)
		if err != nil {
			return err
		}
		created = sub
		return nil
	})
	if err != nil {
		return nil, err
	}

	// 事务成功后刷新该用户的订阅限流缓存（与管理员发放订阅一致）
	InvalidateUserSubscriptionRateLimitCache(userId)
	return created, nil
}

// ReclaimDueReliefSubscriptions 回收任务：扫描在 (since, now] 内到期的救济订阅，记审计日志"未用部分返还救济池"喵~
//
// 池额度（见 ReliefOutstandingQuota）本就按 end_time 实时把未用部分从 x 扣除、瞬间返还救济池，
// 本函数只负责把"回收了多少"显式记进用户日志，便于主人事后对账；它不改动任何额度数据喵~
//
// 输入：上次扫描到的 end_time 水位线（秒）
// 输出：新的水位线（= 本次的 now），以及错误
func ReclaimDueReliefSubscriptions(since int64) (int64, error) {
	// 当前时间戳作为本轮扫描上界，并作为新水位线返回
	now := GetDBTimestamp()
	// 喵~防御：水位线异常（为 0 或大于 now）时，只看最近一小段，避免启动时把历史全刷一遍日志
	if since <= 0 || since > now {
		since = now - int64(autoCheckinReclaimLookbackSeconds)
	}

	// 捞出 end_time 落在 (since, now] 的救济订阅（这些就是刚刚到期的那批）
	var subs []UserSubscription
	if err := DB.Where("source = ? AND end_time > ? AND end_time <= ?", ReliefSubscriptionSource, since, now).
		Find(&subs).Error; err != nil {
		return since, err
	}

	// 逐个记一条审计日志；只有发放总额为正、且确有未用额度时才记
	for i := range subs {
		sub := &subs[i]
		// 未用额度 = 发放总额 - 已用额，夹到 >=0
		unused := sub.AmountTotal - sub.AmountUsed
		if sub.AmountTotal <= 0 || unused <= 0 {
			continue
		}
		// 写一条系统日志，带上发放/已用/返还三个数，口径清晰可对账
		RecordLog(sub.UserId, LogTypeSystem, reliefReclaimLogText(sub.AmountTotal, sub.AmountUsed, unused))
	}

	// 返回新水位线
	return now, nil
}

// autoCheckinReclaimLookbackSeconds 回收任务水位线异常时的回看窗口（秒），默认 10 分钟喵~
const autoCheckinReclaimLookbackSeconds = 600

// reliefReclaimLogText 拼回收审计日志文案（内部日志，可带语气词，但保持数值清晰）喵~
func reliefReclaimLogText(total int64, used int64, unused int64) string {
	// logger.LogQuota 接收 int：这些是额度展示值，$200 级别远在 int 安全范围内，转换安全
	return "救济金到期回收：本次发放 " + logger.LogQuota(int(total)) +
		"，已用 " + logger.LogQuota(int(used)) +
		"，返还未使用 " + logger.LogQuota(int(unused)) + " 到救济池喵~"
}
