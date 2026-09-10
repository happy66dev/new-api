package common

import "sync"

// 虚拟模型计费累计
//
// 一次虚拟模型请求会依次尝试多个候选，被跳过的候选在原生 new-api 口径下同样是会计费的
// （上游已经真实消耗了 token）。为了让「候选级日志」与「请求日志总体累计额度」都能拿到
// 准确的数字，这里用一个小结构承载两种累计喵：
//
//   - PendingCandidate：当前候选已经在探测失败时按原生口径登记、但尚未真正结算的额度；
//   - RequestTotal：本次请求全部候选已经结算的额度累计。
//
// 两者都由 service 层通过 context 写入，供 controller 结算与 model 写日志时读取喵。

// VirtualBillingAmount 表示一次候选结算产生的额度与 token 统计喵。
type VirtualBillingAmount struct {
	Quota            int // new-api 额度，单位：quota（与消费日志 Quota 列同口径）喵。
	PromptTokens     int // 输入 token 数，单位：个喵。
	CompletionTokens int // 输出 token 数，单位：个喵。
}

// IsZero 判断本次结算是否没有任何额度与 token，零值无需登记也不该出现在日志里喵。
func (amount VirtualBillingAmount) IsZero() bool {
	return amount.Quota == 0 && amount.PromptTokens == 0 && amount.CompletionTokens == 0
}

// AddVirtualBillingAmount 累加另一次结算的额度与 token，返回累加后的结果喵。
//
// 输入：base 为已有累计值，delta 为本次新增量，两者都使用 quota / token 口径喵。
// 输出：累加后的新值（值语义，便于调用方直接赋值回字段）喵。
func AddVirtualBillingAmount(base VirtualBillingAmount, delta VirtualBillingAmount) VirtualBillingAmount {
	base.Quota += delta.Quota
	base.PromptTokens += delta.PromptTokens
	base.CompletionTokens += delta.CompletionTokens
	return base
}

// VirtualBilling 承载一次虚拟模型请求的候选级与请求级计费累计喵。
//
// 主人注意：结算发生在渠道 handler 返回之后的主处理协程，但探测阶段存在 scanner 协程，
// 为避免未来出现并发写入造成数据竞争，这里所有读写都用互斥锁保护喵。
type VirtualBilling struct {
	mu sync.Mutex // 保护下面三个累计字段的互斥锁喵。
	// pendingCandidate 当前候选已登记但尚未结算的计费（被跳过的候选）喵。
	pendingCandidate VirtualBillingAmount
	// lastAttempt 当前候选最近一次尝试登记的计费，供候选尝试记录展示单次额度喵。
	lastAttempt VirtualBillingAmount
	// requestTotal 本次请求全部候选已结算的计费累计喵。
	requestTotal VirtualBillingAmount
}

// NewVirtualBilling 创建一个空的虚拟模型计费累计喵。
func NewVirtualBilling() *VirtualBilling {
	return &VirtualBilling{}
}

// RegisterPendingCandidate 把一次「跳过候选」的估算计费累加登记到当前候选喵。
// 同一候选多轮重试时按累加处理：每次上游调用都真实消耗了额度喵。
// 同时记录本次尝试的额度，供候选尝试记录展示「这一次尝试算了多少」喵。
func (billing *VirtualBilling) RegisterPendingCandidate(amount VirtualBillingAmount) {
	// 喵~防御：累计对象为空或本次没有任何额度时不写入，避免制造无意义的零值状态喵。
	if billing == nil || amount.IsZero() {
		return
	}
	billing.mu.Lock()
	defer billing.mu.Unlock()
	billing.pendingCandidate = AddVirtualBillingAmount(billing.pendingCandidate, amount)
	billing.lastAttempt = amount
}

// PendingCandidate 返回当前候选累计待结算的计费额度副本，不改变登记状态喵。
// 供候选失败尝试记录在对账时展示该候选的估算额度喵。
func (billing *VirtualBilling) PendingCandidate() VirtualBillingAmount {
	// 喵~防御：累计对象为空时返回零值，调用方按「无登记」处理喵。
	if billing == nil {
		return VirtualBillingAmount{}
	}
	billing.mu.Lock()
	defer billing.mu.Unlock()
	return billing.pendingCandidate
}

// LastAttempt 返回当前候选最近一次尝试登记的计费额度副本喵。
// 候选尝试记录用它展示单次尝试的估算额度，避免把多轮重试的累计值记到某一次尝试上喵。
func (billing *VirtualBilling) LastAttempt() VirtualBillingAmount {
	// 喵~防御：累计对象为空时返回零值喵。
	if billing == nil {
		return VirtualBillingAmount{}
	}
	billing.mu.Lock()
	defer billing.mu.Unlock()
	return billing.lastAttempt
}

// TakePendingCandidate 取走当前候选登记的待结算计费并清空，保证同一次登记只被结算一次喵。
//
// 输出：登记金额（可能为零值）与是否存在非零登记；调用方据第二个返回值决定是否结算喵。
func (billing *VirtualBilling) TakePendingCandidate() (VirtualBillingAmount, bool) {
	// 喵~防御：累计对象为空时没有任何登记喵。
	if billing == nil {
		return VirtualBillingAmount{}, false
	}
	billing.mu.Lock()
	defer billing.mu.Unlock()
	amount := billing.pendingCandidate
	// 无论是否有登记都清空，避免候选切换后旧登记被下一个候选误结算喵。
	billing.pendingCandidate = VirtualBillingAmount{}
	billing.lastAttempt = VirtualBillingAmount{}
	return amount, !amount.IsZero()
}

// ClearPendingCandidate 丢弃当前候选登记的待结算计费喵。
// 候选最终成功时调用：成功路径会按真实 usage 结算，探测阶段的估算不能再计一次喵。
func (billing *VirtualBilling) ClearPendingCandidate() {
	// 喵~防御：累计对象为空时无需清理喵。
	if billing == nil {
		return
	}
	billing.mu.Lock()
	defer billing.mu.Unlock()
	billing.pendingCandidate = VirtualBillingAmount{}
	billing.lastAttempt = VirtualBillingAmount{}
}

// AddSettled 把一次已经真正结算的额度累加进请求级总额喵。
func (billing *VirtualBilling) AddSettled(amount VirtualBillingAmount) {
	// 喵~防御：累计对象为空或本次没有结算额度时不写入喵。
	if billing == nil || amount.IsZero() {
		return
	}
	billing.mu.Lock()
	defer billing.mu.Unlock()
	billing.requestTotal = AddVirtualBillingAmount(billing.requestTotal, amount)
}

// Total 返回本次请求全部候选已结算的累计额度与 token 副本喵。
func (billing *VirtualBilling) Total() VirtualBillingAmount {
	// 喵~防御：累计对象为空时返回零值，调用方按「无计费」处理喵。
	if billing == nil {
		return VirtualBillingAmount{}
	}
	billing.mu.Lock()
	defer billing.mu.Unlock()
	return billing.requestTotal
}
