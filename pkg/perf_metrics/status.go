package perfmetrics

import (
	"math"
	"sort"
	"time"

	"github.com/QuantumNous/new-api/model"
)

// QueryStatus aggregates configured-group relay metrics and, when enabled,
// low-frequency flexible active probes. Probe samples have no cache fields.
func QueryStatus(hours int, groups []string, cacheExcludedModels []string) (StatusResult, error) {
	if hours <= 0 {
		hours = 24
	}
	if hours > 24*30 {
		hours = 24 * 30
	}
	endTs := time.Now().Unix()
	startTs := endTs - int64(hours)*3600
	rows, err := model.GetPerfGroupSummaryBucketsAll(startTs, endTs, groups)
	if err != nil {
		return StatusResult{}, err
	}
	var cacheRows []model.PerfGroupCacheTokenSummaryBucket
	if len(cacheExcludedModels) > 0 {
		cacheRows, err = model.GetPerfGroupCacheTokenSummaryBuckets(startTs, endTs, groups, cacheExcludedModels)
		if err != nil {
			return StatusResult{}, err
		}
	}

	allowed := allowedGroupSet(groups)
	excludedModels := make(map[string]struct{}, len(cacheExcludedModels))
	for _, modelName := range cacheExcludedModels {
		excludedModels[modelName] = struct{}{}
	}
	buckets := make(map[string]map[int64]counters)
	for _, row := range rows {
		if allowed != nil {
			if _, ok := allowed[row.Group]; !ok {
				continue
			}
		}
		if _, ok := buckets[row.Group]; !ok {
			buckets[row.Group] = make(map[int64]counters)
		}
		value := counters{
			requestCount:     row.RequestCount,
			successCount:     row.SuccessCount,
			totalLatencyMs:   row.TotalLatencyMs,
			ttftSumMs:        row.TtftSumMs,
			ttftCount:        row.TtftCount,
			cacheHitCount:    row.CacheHitCount,
			cacheSampleCount: row.CacheSampleCount,
			cachedTokens:     row.CachedTokens,
			inputTokens:      row.InputTokens,
		}
		buckets[row.Group][row.BucketTs] = value
	}
	// 被排除模型的缓存样本/命中计数在 Go 侧按模型行扣除，只影响被排除模型自己，不再清零整个聚合窗口喵。
	if len(excludedModels) > 0 {
		var excludedRows []model.PerfMetric
		if err := model.DB.Model(&model.PerfMetric{}).
			Where("bucket_ts >= ? AND bucket_ts <= ? AND model_name IN ?", startTs, endTs, cacheExcludedModels).
			Find(&excludedRows).Error; err != nil {
			return StatusResult{}, err
		}
		for _, excludedRow := range excludedRows {
			// 喵~防御：被排除模型行不在可见分组内时跳过，避免越权影响其他分组统计喵。
			if allowed != nil {
				if _, ok := allowed[excludedRow.Group]; !ok {
					continue
				}
			}
			groupBuckets, groupExists := buckets[excludedRow.Group]
			// 喵~防御：聚合窗口没有该分组/桶时无需扣除，避免凭空造出零请求桶喵。
			if !groupExists {
				continue
			}
			value, bucketExists := groupBuckets[excludedRow.BucketTs]
			if !bucketExists {
				continue
			}
			// 喵~防御：扣除后不得为负，避免被排除模型行多于聚合值时出现负数缓存统计喵。
			value.cacheSampleCount -= excludedRow.CacheSampleCount
			if value.cacheSampleCount < 0 {
				value.cacheSampleCount = 0
			}
			value.cacheHitCount -= excludedRow.CacheHitCount
			if value.cacheHitCount < 0 {
				value.cacheHitCount = 0
			}
			groupBuckets[excludedRow.BucketTs] = value
			buckets[excludedRow.Group] = groupBuckets
		}
	}
	for _, row := range cacheRows {
		if _, ok := buckets[row.Group]; !ok {
			buckets[row.Group] = make(map[int64]counters)
		}
		value := buckets[row.Group][row.BucketTs]
		value.cachedTokens = row.CachedTokens
		value.inputTokens = row.InputTokens
		buckets[row.Group][row.BucketTs] = value
	}

	hotBuckets.Range(func(key, value any) bool {
		bucket := key.(bucketKey)
		if bucket.bucketTs < startTs || bucket.bucketTs > endTs {
			return true
		}
		if allowed != nil {
			if _, ok := allowed[bucket.group]; !ok {
				return true
			}
		}
		if _, ok := buckets[bucket.group]; !ok {
			buckets[bucket.group] = make(map[int64]counters)
		}
		current := buckets[bucket.group][bucket.bucketTs]
		hot := value.(*atomicBucket).snapshot()
		if _, excluded := excludedModels[bucket.model]; excluded {
			hot.cacheHitCount = 0
			hot.cacheSampleCount = 0
			hot.cachedTokens = 0
			hot.inputTokens = 0
		}
		mergeStatusCounters(&current, hot)
		buckets[bucket.group][bucket.bucketTs] = current
		return true
	})

	result := make([]StatusGroup, 0, len(groups))
	for _, group := range groups {
		groupBuckets := buckets[group]
		timestamps := make([]int64, 0, len(groupBuckets))
		total := counters{}
		for ts, value := range groupBuckets {
			timestamps = append(timestamps, ts)
			mergeStatusCounters(&total, value)
		}
		sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })
		rates := make([]float64, 0, len(timestamps))
		history := make([]StatusHistoryPoint, 0, len(timestamps))
		for _, ts := range timestamps {
			value := groupBuckets[ts]
			rates = append(rates, math.Round(successRate(value)*100)/100)
			cacheRate := cacheTokenRate(value)
			history = append(history, StatusHistoryPoint{
				Ts:               ts,
				AvgLatencyMs:     avg(value.totalLatencyMs, value.requestCount),
				AvgTtftMs:        avg(value.ttftSumMs, value.ttftCount),
				TtftSampleCount:  value.ttftCount,
				CacheHitRate:     math.Round(cacheRate*100) / 100,
				CacheSampleCount: value.cacheSampleCount,
				CacheInputTokens: value.inputTokens,
			})
		}
		if len(rates) > 24 {
			rates = rates[len(rates)-24:]
		}
		if len(history) > 24 {
			history = history[len(history)-24:]
		}
		cacheRate := cacheTokenRate(total)
		result = append(result, StatusGroup{
			Group:            group,
			Availability:     math.Round(successRate(total)*100) / 100,
			AvgLatencyMs:     avg(total.totalLatencyMs, total.requestCount),
			AvgTtftMs:        avg(total.ttftSumMs, total.ttftCount),
			CacheHitRate:     math.Round(cacheRate*100) / 100,
			CacheSampleCount: total.cacheSampleCount,
			CacheInputTokens: total.inputTokens,
			RequestCount:     total.requestCount,
			Availability24:   rates,
			History24:        history,
		})
	}
	return StatusResult{Groups: result}, nil
}

func mergeStatusCounters(target *counters, value counters) {
	target.requestCount += value.requestCount
	target.successCount += value.successCount
	target.totalLatencyMs += value.totalLatencyMs
	target.ttftSumMs += value.ttftSumMs
	target.ttftCount += value.ttftCount
	target.outputTokens += value.outputTokens
	target.generationMs += value.generationMs
	target.cacheHitCount += value.cacheHitCount
	target.cacheSampleCount += value.cacheSampleCount
	target.cachedTokens += value.cachedTokens
	target.inputTokens += value.inputTokens
	target.cacheCreation5mTokens += value.cacheCreation5mTokens
	target.cacheCreation1hTokens += value.cacheCreation1hTokens
}

func cacheTokenRate(value counters) float64 {
	if value.inputTokens <= 0 || value.cachedTokens <= 0 {
		return 0
	}
	inputTokens := value.inputTokens
	if value.cachedTokens > inputTokens {
		inputTokens = adjustedCacheInputTokens(inputTokens, value.cachedTokens)
	}
	return float64(value.cachedTokens) / float64(inputTokens) * 100
}
