package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReliefSubscriptionHasRemaining 覆盖"单订阅是否还花得下一笔"的只读判定：
// 正常有余、各额度窗口用满、重置点已过视为归零、空指针防御等分支喵~
func TestReliefSubscriptionHasRemaining(t *testing.T) {
	// 固定一个"现在"时间戳，构造重置点在其前/后的用例
	const now int64 = 1_000_000

	// 边界：空指针必须按"不可用"处理，禁止崩溃
	assert.False(t, reliefSubscriptionHasRemaining(nil, now))

	// 总额度还有余量 → 可用
	assert.True(t, reliefSubscriptionHasRemaining(&UserSubscription{AmountTotal: 100, AmountUsed: 40}, now))

	// 总额度用满 → 不可用
	assert.False(t, reliefSubscriptionHasRemaining(&UserSubscription{AmountTotal: 100, AmountUsed: 100}, now))

	// 总额度用满但已过重置点（NextResetTime<=now）→ 本周期视为归零 → 可用
	assert.True(t, reliefSubscriptionHasRemaining(&UserSubscription{AmountTotal: 100, AmountUsed: 100, NextResetTime: now - 1}, now))

	// 五小时额度用满（且重置点在未来）→ 不可用
	assert.False(t, reliefSubscriptionHasRemaining(&UserSubscription{AmountTotal: 100, AmountUsed: 10, FiveHourLimit: 50, FiveHourUsed: 50, FiveHourNextResetTime: now + 100}, now))

	// 五小时额度用满但已过五小时重置点 → 归零 → 可用
	assert.True(t, reliefSubscriptionHasRemaining(&UserSubscription{AmountTotal: 100, AmountUsed: 10, FiveHourLimit: 50, FiveHourUsed: 50, FiveHourNextResetTime: now - 1}, now))

	// 周额度用满 → 不可用
	assert.False(t, reliefSubscriptionHasRemaining(&UserSubscription{WeeklyLimit: 30, WeeklyUsed: 30, WeeklyNextResetTime: now + 100}, now))

	// 月额度用满 → 不可用
	assert.False(t, reliefSubscriptionHasRemaining(&UserSubscription{MonthlyLimit: 30, MonthlyUsed: 30, MonthlyNextResetTime: now + 100}, now))

	// 未配置任何额度（都为 0）→ 视为恒有余额（不限量）→ 可用
	assert.True(t, reliefSubscriptionHasRemaining(&UserSubscription{}, now))
}

// TestReliefOutstandingQuota 覆盖全局已占池额度 x 的两桶口径：
// 有效期内（active 且 end_time>now）按发放总额计入，其余只按已用额计入喵~
func TestReliefOutstandingQuota(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()

	// 有效期内的救济订阅：按 amount_total(1000) 计入 x
	require.NoError(t, DB.Create(&UserSubscription{UserId: 1, Source: ReliefSubscriptionSource, Status: "active", AmountTotal: 1000, AmountUsed: 200, StartTime: now - 100, EndTime: now + 3600}).Error)
	// 已过期的救济订阅：只按 amount_used(300) 计入 x（未用部分已瞬间返还救济池）
	require.NoError(t, DB.Create(&UserSubscription{UserId: 2, Source: ReliefSubscriptionSource, Status: "active", AmountTotal: 1000, AmountUsed: 300, StartTime: now - 7200, EndTime: now - 1}).Error)
	// 非救济来源订阅：完全不计入 x
	require.NoError(t, DB.Create(&UserSubscription{UserId: 3, Source: "order", Status: "active", AmountTotal: 5000, AmountUsed: 4000, StartTime: now - 100, EndTime: now + 3600}).Error)

	// x = 有效期内发放总额(1000) + 其余订阅已用额(300) = 1300
	x, err := ReliefOutstandingQuota()
	require.NoError(t, err)
	assert.EqualValues(t, 1300, x)
}

// TestLastReliefGrantTime 覆盖"最近一次领取时间"：取最新一条救济订阅的 start_time，从未领过返回 0 喵~
func TestLastReliefGrantTime(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()

	// 边界：非法 userId 必须报错
	_, err := LastReliefGrantTime(0)
	require.Error(t, err)

	// 从未领取过返回 0
	last, err := LastReliefGrantTime(10)
	require.NoError(t, err)
	assert.EqualValues(t, 0, last)

	// 两条救济订阅，应取较新的 start_time
	require.NoError(t, DB.Create(&UserSubscription{UserId: 10, Source: ReliefSubscriptionSource, Status: "active", AmountTotal: 100, StartTime: now - 7200, EndTime: now - 3600}).Error)
	require.NoError(t, DB.Create(&UserSubscription{UserId: 10, Source: ReliefSubscriptionSource, Status: "active", AmountTotal: 100, StartTime: now - 60, EndTime: now + 3600}).Error)
	last, err = LastReliefGrantTime(10)
	require.NoError(t, err)
	assert.EqualValues(t, now-60, last)
}

// TestUserHasConsumableSubscription 覆盖"订阅是否可用"判定：
// 存在任一额度未用完的有效订阅即为可用；全部用满/已过期/纯权益则不可用喵~
func TestUserHasConsumableSubscription(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()

	// 边界：非法 userId 必须报错
	_, err := UserHasConsumableSubscription(0)
	require.Error(t, err)

	// 先建一个套餐（订阅会按 plan_id 读取它判定是否"仅钱包"）
	plan := &SubscriptionPlan{Id: 7001, Title: "普通套餐", DurationUnit: SubscriptionDurationMonth, DurationValue: 1, TotalAmount: 1000}
	require.NoError(t, DB.Create(plan).Error)

	// 用户 20：有一个总额度还没用满的有效订阅 → 可用
	require.NoError(t, DB.Create(&User{Id: 20, Username: "u20", Group: "default", AffCode: "aff20"}).Error)
	require.NoError(t, DB.Create(&UserSubscription{UserId: 20, PlanId: plan.Id, Source: "order", Status: "active", AmountTotal: 1000, AmountUsed: 400, StartTime: now - 100, EndTime: now + 3600}).Error)
	ok, err := UserHasConsumableSubscription(20)
	require.NoError(t, err)
	assert.True(t, ok)

	// 用户 21：唯一的有效订阅总额度已用满 → 不可用
	require.NoError(t, DB.Create(&User{Id: 21, Username: "u21", Group: "default", AffCode: "aff21"}).Error)
	require.NoError(t, DB.Create(&UserSubscription{UserId: 21, PlanId: plan.Id, Source: "order", Status: "active", AmountTotal: 1000, AmountUsed: 1000, StartTime: now - 100, EndTime: now + 3600}).Error)
	ok, err = UserHasConsumableSubscription(21)
	require.NoError(t, err)
	assert.False(t, ok)

	// 用户 22：没有任何有效订阅 → 不可用
	require.NoError(t, DB.Create(&User{Id: 22, Username: "u22", Group: "default", AffCode: "aff22"}).Error)
	ok, err = UserHasConsumableSubscription(22)
	require.NoError(t, err)
	assert.False(t, ok)
}
