package perfmetrics

import (
	"math"
	"sort"
	"time"

	"github.com/QuantumNous/new-api/model"
)

// 实体被动统计的固定分组，与状态检测页的分组统计完全隔离，防止实体状态混入分组画像喵。
const (
	// EntityProbeGroupSelf 自用维度的固定分组喵。
	EntityProbeGroupSelf = "__entity_probe__"
	// EntityProbeGroupShared 共享调用维度的固定分组喵。
	EntityProbeGroupShared = "__entity_probe_shared__"
)

// EntityProbeExtras 携带实体调用的吞吐与首字节（TTFT）明细喵。
// 空值时自然跳过吞吐/TTFT 累加，与内部模型语义对齐喵。
type EntityProbeExtras struct {
	// TtftMs 从发起请求到收到响应头的时间，单位：毫秒喵。
	TtftMs int64
	// HasTtft 标记是否测量到有效的 TTFT（失败分支记零）喵。
	HasTtft bool
	// OutputTokens 本次调用的完成 token 数，用于吞吐计算喵。
	OutputTokens int64
	// GenerationMs 生成时长（毫秒），有 TTFT 时近似为 latency - ttft 喵。
	GenerationMs int64
}

// RecordEntityProbe 记录一次实体真实调用的被动统计（自用维度）喵。
// 直接复用 perf_metrics.Record，跟随 perf_metrics_setting 的 Enabled 开关，不新增独立开关喵。
func RecordEntityProbe(modelName string, latencyMs int64, success bool, extras EntityProbeExtras) {
	Record(Sample{
		Model: modelName, Group: EntityProbeGroupSelf, LatencyMs: latencyMs, Success: success,
		TtftMs: extras.TtftMs, HasTtft: extras.HasTtft, OutputTokens: extras.OutputTokens, GenerationMs: extras.GenerationMs,
	})
}

// RecordEntityProbeShared 记录一次共享调用的被动统计（共享维度），供共享使用者的聚合视图使用喵。
func RecordEntityProbeShared(modelName string, latencyMs int64, success bool, extras EntityProbeExtras) {
	Record(Sample{
		Model: modelName, Group: EntityProbeGroupShared, LatencyMs: latencyMs, Success: success,
		TtftMs: extras.TtftMs, HasTtft: extras.HasTtft, OutputTokens: extras.OutputTokens, GenerationMs: extras.GenerationMs,
	})
}

// EntityProbeStatus 单个实体在窗口内的被动统计聚合喵。
type EntityProbeStatus struct {
	// Availability 成功率百分比，无数据时为零喵。
	Availability float64
	// AvgLatencyMs 平均端到端延迟，单位：毫秒喵。
	AvgLatencyMs int64
	// RequestCount 计入的请求总数喵。
	RequestCount int64
	// Availability24 最近 24 个有效小时桶的成功率序列喵。
	Availability24 []float64
}

// QueryEntityProbeStatus 查询实体在窗口内的被动统计聚合，合并落库与内存热桶喵。
func QueryEntityProbeStatus(modelName string, group string, hours int) (EntityProbeStatus, error) {
	status := EntityProbeStatus{Availability24: []float64{}}
	// 喵~防御：空模型名直接返回空聚合，避免无意义查询喵。
	if modelName == "" {
		return status, nil
	}
	if hours <= 0 {
		hours = 24
	}
	// 喵~防御：窗口上限 30 天，避免异常参数放大查询范围喵。
	if hours > 24*30 {
		hours = 24 * 30
	}
	endTs := time.Now().Unix()
	startTs := endTs - int64(hours)*3600
	rows, err := model.GetPerfMetrics(modelName, group, startTs, endTs)
	if err != nil {
		return status, err
	}
	merged := map[int64]counters{}
	for _, row := range rows {
		merged[row.BucketTs] = counters{
			requestCount:   row.RequestCount,
			successCount:   row.SuccessCount,
			totalLatencyMs: row.TotalLatencyMs,
		}
	}
	// 内存热桶合并：只并入同一实体的同一分组，保证读到的状态包含尚未落库的最近样本喵。
	hotBuckets.Range(func(key, value any) bool {
		bucket := key.(bucketKey)
		if bucket.model != modelName || bucket.group != group || bucket.bucketTs < startTs || bucket.bucketTs > endTs {
			return true
		}
		snapshot := value.(*atomicBucket).snapshot()
		current := merged[bucket.bucketTs]
		current.requestCount += snapshot.requestCount
		current.successCount += snapshot.successCount
		current.totalLatencyMs += snapshot.totalLatencyMs
		merged[bucket.bucketTs] = current
		return true
	})
	timestamps := make([]int64, 0, len(merged))
	for ts := range merged {
		timestamps = append(timestamps, ts)
	}
	sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })
	total := counters{}
	rates := make([]float64, 0, len(timestamps))
	for _, ts := range timestamps {
		value := merged[ts]
		total.requestCount += value.requestCount
		total.successCount += value.successCount
		total.totalLatencyMs += value.totalLatencyMs
		rates = append(rates, math.Round(successRate(value)*100)/100)
	}
	// 只保留最近 24 个有效桶，与前端 AvailabilityBars 的 24 点语义对齐喵。
	if len(rates) > 24 {
		rates = rates[len(rates)-24:]
	}
	status.RequestCount = total.requestCount
	status.AvgLatencyMs = avg(total.totalLatencyMs, total.requestCount)
	status.Availability = math.Round(successRate(total)*100) / 100
	status.Availability24 = rates
	return status, nil
}
