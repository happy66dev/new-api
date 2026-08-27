package middleware

import (
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// ActiveRequestInfo 描述一个正在处理中的虚拟模型活跃请求喵。
// 供概览实时展示「当前调用链」：请求标识、当前候选序号与候选名、启动时间喵。
type ActiveRequestInfo struct {
	RequestID      string    `json:"request_id"`       // 请求级唯一标识，用于活跃列表区分条目喵。
	ModelID        int64     `json:"model_id"`         // 所属虚拟模型编号，单位：无喵。
	ModelName      string    `json:"model_name"`       // 所属虚拟模型名（virtual/ 前缀），单位：无喵。
	CandidateIndex int       `json:"candidate_index"`  // 当前候选在链上的序号，从 1 起，未激活时为零喵。
	CandidateLabel string    `json:"candidate_label"`  // 当前候选展示名（内部候选为真实模型名），单位：无喵。
	StartedAt      time.Time `json:"started_at"`       // 请求进入活跃状态的时间点喵。
}

// maxVirtualActiveDetailCount 限制单个虚拟模型活跃详情条目数，避免海量并发把内存打爆喵。
// 超限后只保留计数不记详情，当前请求数依然准确喵。
const maxVirtualActiveDetailCount = 200

// perModelVirtualInflight 保存单个虚拟模型的活跃请求计数与详情喵。
type perModelVirtualInflight struct {
	// count 当前处理中请求总数，超限详情不记录时仍由它保证计数准确喵。
	count atomic.Int64
	// mu 保护 active 映射的并发读写喵。
	mu sync.Mutex
	// active 按请求 ID 保存活跃请求详情，仅在前 200 条内登记喵。
	active map[string]*ActiveRequestInfo
}

// upstreamModelInflightEntry 保存单个上游模型的活跃请求计数，自用与共享分别统计喵。
type upstreamModelInflightEntry struct {
	// modelName 模型探测名（user/ 前缀），供模型广场按名称聚合当前处理请求数喵。
	modelName string
	// self 属主自用调用的当前处理请求数喵。
	self atomic.Int64
	// shared 共享使用者调用的当前处理请求数喵。
	shared atomic.Int64
}

// upstreamModelInflightByID 按上游模型编号记录活跃请求喵。
// 键为 int64 模型编号，值为对应计数条目；自用与共享调用区分计数喵。
var upstreamModelInflightByID sync.Map

// internalModelInflightByName 按内部模型名记录活跃请求喵。
// 键为内部模型名（如 gpt-4o），值为原子计数器指针喵。
var internalModelInflightByName sync.Map

// virtualModelInflightByID 按虚拟模型编号记录活跃请求喵。
// 键为 int64 模型编号，值为每个模型的计数与详情容器喵。
var virtualModelInflightByID sync.Map

// inflightRequestSequence 生成请求级唯一标识的自增序号，跨全部虚拟模型共享喵。
var inflightRequestSequence atomic.Int64

// EnterVirtualModelInflight 登记一个虚拟模型请求进入活跃状态，返回请求唯一标识喵。
// 同一模型并发请求共享计数，详情条目在容量内追加喵。
func EnterVirtualModelInflight(modelID int64, modelName string) string {
	// 生成请求级唯一标识：全局序号加时间戳后缀，保证并发下不重复喵。
	requestID := formatInflightRequestID(modelID)
	container := loadVirtualModelInflightContainer(modelID)
	container.count.Add(1)
	// 详情条目不超过容量上限，超出时只计数不记详情，避免内存无界增长喵。
	container.mu.Lock()
	if len(container.active) < maxVirtualActiveDetailCount {
		container.active[requestID] = &ActiveRequestInfo{
			RequestID: requestID,
			ModelID:   modelID,
			ModelName: modelName,
			StartedAt: time.Now(),
		}
	}
	container.mu.Unlock()
	return requestID
}

// UpdateVirtualModelInflightCandidate 更新活跃请求的当前候选序号与标签，供概览展示当前调用链喵。
// 请求 ID 不在详情表中时（超限或已退出）静默跳过，不影响计数喵。
func UpdateVirtualModelInflightCandidate(modelID int64, requestID string, candidateIndex int, candidateLabel string) {
	// 空请求 ID 说明请求未进入活跃状态，直接忽略喵。
	if requestID == "" {
		return
	}
	container, found := virtualModelInflightByID.Load(modelID)
	if !found {
		return
	}
	inflight := container.(*perModelVirtualInflight)
	// 加锁后更新详情条目，避免与 Enter/Exit 并发读写竞争喵。
	inflight.mu.Lock()
	if detail, exists := inflight.active[requestID]; exists {
		detail.CandidateIndex = candidateIndex
		detail.CandidateLabel = candidateLabel
	}
	inflight.mu.Unlock()
}

// ExitVirtualModelInflight 登记一个虚拟模型请求退出活跃状态，并清理对应的详情条目喵。
// 请求全部退出后删除该模型容器，避免空容器长期驻留内存喵。
func ExitVirtualModelInflight(modelID int64, requestID string) {
	container, found := virtualModelInflightByID.Load(modelID)
	if !found {
		return
	}
	inflight := container.(*perModelVirtualInflight)
	inflight.mu.Lock()
	delete(inflight.active, requestID)
	inflight.mu.Unlock()
	// 计数减一后归零时删除容器，保持注册表只保留有活跃请求的模型喵。
	if inflight.count.Add(-1) <= 0 {
		virtualModelInflightByID.Delete(modelID)
	}
}

// GetVirtualModelActiveRequests 返回指定虚拟模型的当前处理请求数与前 200 条活跃详情喵。
func GetVirtualModelActiveRequests(modelID int64) (int64, []ActiveRequestInfo) {
	container, found := virtualModelInflightByID.Load(modelID)
	if !found {
		return 0, []ActiveRequestInfo{}
	}
	inflight := container.(*perModelVirtualInflight)
	// 加锁快照活跃详情，避免返回后调用方读到并发修改的指针喵。
	inflight.mu.Lock()
	details := make([]ActiveRequestInfo, 0, len(inflight.active))
	for _, detail := range inflight.active {
		details = append(details, *detail)
	}
	inflight.mu.Unlock()
	// 快照顺序不稳定不影响展示，计数以原子值为准喵。
	return inflight.count.Load(), details
}

// loadVirtualModelInflightContainer 读取或创建指定虚拟模型的活跃请求容器喵。
// sync.Map 值用指针持有原子计数与互斥锁，保证并发安全喵。
func loadVirtualModelInflightContainer(modelID int64) *perModelVirtualInflight {
	container, found := virtualModelInflightByID.Load(modelID)
	if found {
		return container.(*perModelVirtualInflight)
	}
	// 首次访问时创建一个带空详情映射的容器喵。
	fresh := &perModelVirtualInflight{active: make(map[string]*ActiveRequestInfo)}
	// 并发下可能重复创建，但 LoadOrStore 只保留先写入者，重复对象会被丢弃喵。
	actual, _ := virtualModelInflightByID.LoadOrStore(modelID, fresh)
	return actual.(*perModelVirtualInflight)
}

// TrackVirtualModelCandidateInflight 在候选激活时登记或更新虚拟模型活跃请求喵。
// 首次调用（requestID 为空）进入注册表并生成请求 ID，后续候选切换只更新当前候选喵。
func TrackVirtualModelCandidateInflight(modelID int64, modelName string, requestID string, candidateIndex int, candidateLabel string) string {
	// 首次候选激活时登记请求，并把模型名写入详情供展示喵。
	if requestID == "" {
		requestID = EnterVirtualModelInflight(modelID, modelName)
	}
	// 候选切换（含首次）时更新当前调用链的候选序号与标签喵。
	UpdateVirtualModelInflightCandidate(modelID, requestID, candidateIndex, candidateLabel)
	return requestID
}

// formatInflightRequestID 生成请求级唯一标识：模型编号加全局自增序号喵。
func formatInflightRequestID(modelID int64) string {
	// 自增序号保证并发不重复，模型编号便于日志定位归属喵。
	return strconv.FormatInt(modelID, 10) + "-" + strconv.FormatInt(inflightRequestSequence.Add(1), 10)
}

// EnterUpstreamModelInflight 登记一个上游模型请求进入活跃状态喵。
// isShared 为 true 时计入共享维度，否则计入自用维度喵。
func EnterUpstreamModelInflight(modelID int64, modelName string, isShared bool) {
	entry := loadUpstreamModelInflightEntry(modelID, modelName)
	if isShared {
		entry.shared.Add(1)
	} else {
		entry.self.Add(1)
	}
}

// ExitUpstreamModelInflight 登记一个上游模型请求退出活跃状态喵。
// 与 Enter 的 isShared 维度一一对应，退出后计数归零时删除条目喵。
func ExitUpstreamModelInflight(modelID int64, isShared bool) {
	entry, found := upstreamModelInflightByID.Load(modelID)
	if !found {
		return
	}
	upstreamEntry := entry.(*upstreamModelInflightEntry)
	var remaining int64
	if isShared {
		remaining = upstreamEntry.shared.Add(-1)
	} else {
		remaining = upstreamEntry.self.Add(-1)
	}
	// 自用与共享都归零时删除条目，避免空条目残留喵。
	if remaining <= 0 && upstreamEntry.self.Load() <= 0 && upstreamEntry.shared.Load() <= 0 {
		upstreamModelInflightByID.Delete(modelID)
	}
}

// loadUpstreamModelInflightEntry 读取或创建指定上游模型的活跃计数条目喵。
func loadUpstreamModelInflightEntry(modelID int64, modelName string) *upstreamModelInflightEntry {
	entry, found := upstreamModelInflightByID.Load(modelID)
	if found {
		return entry.(*upstreamModelInflightEntry)
	}
	// 首次访问创建条目并记录模型名，供名称聚合查询喵。
	fresh := &upstreamModelInflightEntry{modelName: modelName}
	actual, _ := upstreamModelInflightByID.LoadOrStore(modelID, fresh)
	return actual.(*upstreamModelInflightEntry)
}

// GetUpstreamModelActiveCount 返回指定上游模型的自用与共享活跃请求数喵。
func GetUpstreamModelActiveCount(modelID int64) (self int64, shared int64) {
	entry, found := upstreamModelInflightByID.Load(modelID)
	if !found {
		return 0, 0
	}
	upstreamEntry := entry.(*upstreamModelInflightEntry)
	return upstreamEntry.self.Load(), upstreamEntry.shared.Load()
}

// GetUpstreamModelActiveCountByName 返回指定模型名下所有上游模型的活跃请求总数喵。
// 模型广场按名称聚合展示，同名不同属主的请求一并计入喵。
func GetUpstreamModelActiveCountByName(modelName string) int64 {
	var total int64
	// 遍历注册表累加模型名一致的条目，名称匹配不到时返回零喵。
	upstreamModelInflightByID.Range(func(_, value any) bool {
		entry := value.(*upstreamModelInflightEntry)
		if entry.modelName == modelName {
			total += entry.self.Load() + entry.shared.Load()
		}
		return true
	})
	return total
}

// EnterInternalModelInflight 登记一个内部模型请求进入活跃状态喵。
func EnterInternalModelInflight(modelName string) {
	// 空模型名不登记，避免产生无法识别的统计条目喵。
	if modelName == "" {
		return
	}
	counter := loadInternalModelInflightCounter(modelName)
	counter.Add(1)
}

// ExitInternalModelInflight 登记一个内部模型请求退出活跃状态喵。
// 计数归零时删除条目，保持注册表干净喵。
func ExitInternalModelInflight(modelName string) {
	if modelName == "" {
		return
	}
	counter, found := internalModelInflightByName.Load(modelName)
	if !found {
		return
	}
	countPointer := counter.(*atomic.Int64)
	if countPointer.Add(-1) <= 0 {
		internalModelInflightByName.Delete(modelName)
	}
}

// loadInternalModelInflightCounter 读取或创建指定内部模型的活跃计数喵。
func loadInternalModelInflightCounter(modelName string) *atomic.Int64 {
	counter, found := internalModelInflightByName.Load(modelName)
	if found {
		return counter.(*atomic.Int64)
	}
	fresh := &atomic.Int64{}
	actual, _ := internalModelInflightByName.LoadOrStore(modelName, fresh)
	return actual.(*atomic.Int64)
}

// GetInternalModelActiveCount 返回指定内部模型的当前处理请求数喵。
func GetInternalModelActiveCount(modelName string) int64 {
	counter, found := internalModelInflightByName.Load(modelName)
	if !found {
		return 0
	}
	return counter.(*atomic.Int64).Load()
}
