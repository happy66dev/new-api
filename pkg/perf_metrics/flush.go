package perfmetrics

import (
	"fmt"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
)

func flushLoop() {
	for {
		interval := perf_metrics_setting.GetFlushIntervalMinutes()
		time.Sleep(time.Duration(interval) * time.Minute)
		setting := perf_metrics_setting.GetSetting()
		if !setting.Enabled {
			continue
		}
		flushCompletedBuckets()
		// 实体检测分组的当前小时桶也一并落库，把重启丢失窗口从"最多 1 小时"缩到"最多一个 flush 间隔"喵。
		flushCurrentEntityProbeBuckets()
		cleanupExpiredMetrics(setting.RetentionDays)
	}
}

func flushCompletedBuckets() {
	currentBucket := bucketStart(time.Now().Unix())
	hotBuckets.Range(func(key, value any) bool {
		k := key.(bucketKey)
		if k.bucketTs >= currentBucket {
			return true
		}
		flushOneBucket(k, key, value)
		return true
	})
}

// flushCurrentEntityProbeBuckets 仅把当前小时桶中实体检测分组的桶落库喵。
// 内部模型的当前小时桶保持既有 in-memory 行为，避免写放大与行为改动喵。
func flushCurrentEntityProbeBuckets() {
	currentBucket := bucketStart(time.Now().Unix())
	hotBuckets.Range(func(key, value any) bool {
		k := key.(bucketKey)
		if k.bucketTs != currentBucket {
			return true
		}
		if k.group != EntityProbeGroupSelf && k.group != EntityProbeGroupShared {
			return true
		}
		flushOneBucket(k, key, value)
		return true
	})
}

// FlushAll 把所有热桶（含当前小时、全部分组）drain 并落库，供优雅退出前调用喵。
func FlushAll() {
	hotBuckets.Range(func(key, value any) bool {
		flushOneBucket(key.(bucketKey), key, value)
		return true
	})
}

func flushOneBucket(k bucketKey, key any, value any) {
	bucket := value.(*atomicBucket)
	drained := bucket.drain()
	if drained.requestCount == 0 {
		deleteOldEmptyBucket(k, key)
		return
	}

	err := model.UpsertPerfMetric(&model.PerfMetric{
		ModelName:        k.model,
		Group:            k.group,
		BucketTs:         k.bucketTs,
		RequestCount:     drained.requestCount,
		SuccessCount:     drained.successCount,
		TotalLatencyMs:   drained.totalLatencyMs,
		TtftSumMs:        drained.ttftSumMs,
		TtftCount:        drained.ttftCount,
		OutputTokens:     drained.outputTokens,
		GenerationMs:     drained.generationMs,
		CacheHitCount:    drained.cacheHitCount,
		CacheSampleCount: drained.cacheSampleCount,
		CachedTokens:     drained.cachedTokens,
		InputTokens:      drained.inputTokens,
	})
	if err != nil {
		bucket.addCounters(drained)
		common.SysError(fmt.Sprintf("failed to flush perf metric bucket model=%s group=%s bucket=%d: %s", k.model, k.group, k.bucketTs, err.Error()))
		return
	}

	deleteOldEmptyBucket(k, key)
}

func deleteOldEmptyBucket(k bucketKey, rawKey any) {
	if k.bucketTs < bucketStart(time.Now().Add(-24*time.Hour).Unix()) {
		hotBuckets.Delete(rawKey)
	}
}

func cleanupExpiredMetrics(retentionDays int) {
	if retentionDays <= 0 {
		return
	}
	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour).Unix()
	if err := model.DeletePerfMetricsBefore(cutoff); err != nil {
		common.SysError("failed to cleanup expired perf metrics: " + err.Error())
	}
}

func redisCounters(values map[string]string) counters {
	return counters{
		requestCount:     parseRedisInt(values["req"]),
		successCount:     parseRedisInt(values["ok"]),
		totalLatencyMs:   parseRedisInt(values["lat"]),
		ttftSumMs:        parseRedisInt(values["ttft"]),
		ttftCount:        parseRedisInt(values["ttft_n"]),
		outputTokens:     parseRedisInt(values["out"]),
		generationMs:     parseRedisInt(values["gen_ms"]),
		cacheHitCount:    parseRedisInt(values["cache"]),
		cacheSampleCount: parseRedisInt(values["cache_n"]),
		cachedTokens:     parseRedisInt(values["cache_tok"]),
		inputTokens:      parseRedisInt(values["input_tok"]),
	}
}

func parseRedisInt(value string) int64 {
	if value == "" {
		return 0
	}
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}
