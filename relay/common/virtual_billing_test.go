package common

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVirtualBillingAmountIsZero 覆盖「是否没有任何计费」的判定：三种字段全零才算零值喵。
func TestVirtualBillingAmountIsZero(t *testing.T) {
	// 全零是唯一被视为「无计费」的组合，零值登记不该污染候选记录喵。
	assert.True(t, VirtualBillingAmount{}.IsZero())
	// 任意一个字段非零都表示确实产生了可展示的计费喵。
	assert.False(t, VirtualBillingAmount{Quota: 1}.IsZero())
	assert.False(t, VirtualBillingAmount{PromptTokens: 1}.IsZero())
	assert.False(t, VirtualBillingAmount{CompletionTokens: 1}.IsZero())
	// 负数同样不算零值，避免退差额被当成「无计费」丢掉喵。
	assert.False(t, VirtualBillingAmount{Quota: -1}.IsZero())
}

// TestAddVirtualBillingAmount 覆盖累加语义：三个字段各自相加，且不改动入参喵。
func TestAddVirtualBillingAmount(t *testing.T) {
	base := VirtualBillingAmount{Quota: 100, PromptTokens: 10, CompletionTokens: 5}
	delta := VirtualBillingAmount{Quota: 50, PromptTokens: 3, CompletionTokens: 2}

	sum := AddVirtualBillingAmount(base, delta)
	assert.Equal(t, VirtualBillingAmount{Quota: 150, PromptTokens: 13, CompletionTokens: 7}, sum)
	// 值语义：调用方传入的原始值不应被修改喵。
	assert.Equal(t, 100, base.Quota)
	assert.Equal(t, 10, base.PromptTokens)
}

// TestVirtualBillingNilSafety 覆盖空指针下的全部对外方法，确保调用方无需额外判空喵。
func TestVirtualBillingNilSafety(t *testing.T) {
	var billing *VirtualBilling
	// 喵~防御：登记、读取、结算、清理在空指针上都必须是安全空操作喵。
	assert.NotPanics(t, func() {
		billing.RegisterPendingCandidate(VirtualBillingAmount{Quota: 1})
		billing.ClearPendingCandidate()
		billing.AddSettled(VirtualBillingAmount{Quota: 1})
	})
	assert.Equal(t, VirtualBillingAmount{}, billing.PendingCandidate())
	assert.Equal(t, VirtualBillingAmount{}, billing.LastAttempt())
	assert.Equal(t, VirtualBillingAmount{}, billing.Total())
	amount, ok := billing.TakePendingCandidate()
	assert.Equal(t, VirtualBillingAmount{}, amount)
	assert.False(t, ok)
}

// TestVirtualBillingRegisterAndTake 覆盖「登记 → 取走」主流程与重试累加语义喵。
func TestVirtualBillingRegisterAndTake(t *testing.T) {
	billing := NewVirtualBilling()

	// 首次登记：待结算累计被写入，且最近一次尝试额度同步记录喵。
	billing.RegisterPendingCandidate(VirtualBillingAmount{Quota: 120, PromptTokens: 4, CompletionTokens: 6})
	assert.Equal(t, VirtualBillingAmount{Quota: 120, PromptTokens: 4, CompletionTokens: 6}, billing.PendingCandidate())
	assert.Equal(t, VirtualBillingAmount{Quota: 120, PromptTokens: 4, CompletionTokens: 6}, billing.LastAttempt())

	// 同一候选重试第二次：待结算按累加，但最近一次尝试只反映本次，供候选记录逐次展示喵。
	billing.RegisterPendingCandidate(VirtualBillingAmount{Quota: 80, PromptTokens: 1, CompletionTokens: 2})
	assert.Equal(t, VirtualBillingAmount{Quota: 200, PromptTokens: 5, CompletionTokens: 8}, billing.PendingCandidate())
	assert.Equal(t, VirtualBillingAmount{Quota: 80, PromptTokens: 1, CompletionTokens: 2}, billing.LastAttempt())

	// 取走时应拿到累计值，并且第二个返回值表示存在非零登记喵。
	amount, ok := billing.TakePendingCandidate()
	assert.True(t, ok)
	assert.Equal(t, VirtualBillingAmount{Quota: 200, PromptTokens: 5, CompletionTokens: 8}, amount)

	// 取走即清空，保证同一笔登记不会被结算两次喵。
	assert.Equal(t, VirtualBillingAmount{}, billing.PendingCandidate())
	assert.Equal(t, VirtualBillingAmount{}, billing.LastAttempt())
	second, ok := billing.TakePendingCandidate()
	assert.False(t, ok)
	assert.Equal(t, VirtualBillingAmount{}, second)
}

// TestVirtualBillingRegisterIgnoresZero 覆盖零值登记：全零不写入，避免制造无意义的待结算状态喵。
func TestVirtualBillingRegisterIgnoresZero(t *testing.T) {
	billing := NewVirtualBilling()
	billing.RegisterPendingCandidate(VirtualBillingAmount{})
	assert.Equal(t, VirtualBillingAmount{}, billing.PendingCandidate())

	// 先有真实登记，再登记零值时不应把已有额度清零，也不应覆盖最近一次尝试喵。
	billing.RegisterPendingCandidate(VirtualBillingAmount{Quota: 10})
	billing.RegisterPendingCandidate(VirtualBillingAmount{})
	assert.Equal(t, VirtualBillingAmount{Quota: 10}, billing.PendingCandidate())
	assert.Equal(t, VirtualBillingAmount{Quota: 10}, billing.LastAttempt())
}

// TestVirtualBillingClearPendingCandidate 覆盖成功路径的丢弃语义：登记被清空且不进入请求级累计喵。
func TestVirtualBillingClearPendingCandidate(t *testing.T) {
	billing := NewVirtualBilling()
	billing.RegisterPendingCandidate(VirtualBillingAmount{Quota: 300, PromptTokens: 9, CompletionTokens: 9})

	billing.ClearPendingCandidate()
	assert.Equal(t, VirtualBillingAmount{}, billing.PendingCandidate())
	_, ok := billing.TakePendingCandidate()
	assert.False(t, ok)
	// 丢弃登记不等于已结算，请求级累计必须保持为零喵。
	assert.Equal(t, VirtualBillingAmount{}, billing.Total())
}

// TestVirtualBillingTakeClearsStateAfterEmptyRegister 覆盖「候选没产生任何登记」时的取走语义喵。
func TestVirtualBillingTakeClearsStateAfterEmptyRegister(t *testing.T) {
	billing := NewVirtualBilling()
	amount, ok := billing.TakePendingCandidate()
	assert.False(t, ok)
	assert.Equal(t, VirtualBillingAmount{}, amount)
}

// TestVirtualBillingAddSettledAndTotal 覆盖请求级累计：多候选结算额度相加，零值被忽略喵。
func TestVirtualBillingAddSettledAndTotal(t *testing.T) {
	billing := NewVirtualBilling()
	// 零值结算是无意义写入，不应产生累计喵。
	billing.AddSettled(VirtualBillingAmount{})
	assert.Equal(t, VirtualBillingAmount{}, billing.Total())

	billing.AddSettled(VirtualBillingAmount{Quota: 500, PromptTokens: 20, CompletionTokens: 30})
	billing.AddSettled(VirtualBillingAmount{Quota: 250, PromptTokens: 5, CompletionTokens: 10})
	assert.Equal(t, VirtualBillingAmount{Quota: 750, PromptTokens: 25, CompletionTokens: 40}, billing.Total())
}

// TestVirtualBillingTotalIsolation 覆盖并发场景：读写累计不产生数据竞争，且总额与登记互不干扰喵。
//
// 整体思路喵：结算发生在主处理协程，探测缓存的失败信号可能来自 scanner 协程，
// 因此这里用 -race 下并发读写来确认互斥锁生效、且 PendingCandidate 与 Total 各管一份数据喵。
func TestVirtualBillingTotalIsolation(t *testing.T) {
	billing := NewVirtualBilling()
	var waitGroup sync.WaitGroup
	// 并发登记待结算（候选跳过）与结算累计（请求级），两者必须互不覆盖喵。
	for index := 0; index < 50; index++ {
		waitGroup.Add(2)
		go func() {
			defer waitGroup.Done()
			billing.RegisterPendingCandidate(VirtualBillingAmount{Quota: 1})
		}()
		go func() {
			defer waitGroup.Done()
			billing.AddSettled(VirtualBillingAmount{Quota: 2})
		}()
	}
	waitGroup.Wait()

	// 请求级累计只受 AddSettled 影响，恰好等于 50 次 × 2 喵。
	require.Equal(t, VirtualBillingAmount{Quota: 100}, billing.Total())
	// 待结算累计只受 RegisterPendingCandidate 影响，恰好等于 50 次 × 1 喵。
	assert.Equal(t, VirtualBillingAmount{Quota: 50}, billing.PendingCandidate())
}
