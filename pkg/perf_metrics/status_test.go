package perfmetrics

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

// setupStatusTestDB 初始化独立内存库并迁移 perf 统计表，供 QueryStatus 聚合测试使用喵。
// 使用独立库名，避免与其他 perf 测试共享的 SQLite 内存库串扰喵。
func setupStatusTestDB(t *testing.T) {
	t.Helper()

	// 保存并随后恢复所有被测试改动的全局状态，避免影响同包其他测试喵。
	originalIsMasterNode := common.IsMasterNode
	originalSQLitePath := common.SQLitePath
	originalMainDatabaseType := common.MainDatabaseType()
	originalLogDatabaseType := common.LogDatabaseType()
	originalSQLDSN, hadSQLDSN := os.LookupEnv("SQL_DSN")
	t.Cleanup(func() {
		common.IsMasterNode = originalIsMasterNode
		common.SQLitePath = originalSQLitePath
		common.SetDatabaseTypes(originalMainDatabaseType, originalLogDatabaseType)
		if hadSQLDSN {
			require.NoError(t, os.Setenv("SQL_DSN", originalSQLDSN))
		} else {
			require.NoError(t, os.Unsetenv("SQL_DSN"))
		}
	})

	// 使用独立库名，配合 SQL_DSN=local 让 InitDB 走 SQLite 分支并初始化列名喵。
	common.IsMasterNode = false
	common.SQLitePath = fmt.Sprintf("file:%s?mode=memory&cache=shared", "perf_metrics_status_test")
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, os.Setenv("SQL_DSN", "local"))

	require.NoError(t, model.InitDB())
	require.NoError(t, model.DB.AutoMigrate(&model.PerfMetric{}))
}

// TestQueryStatusExcludedModelsKeepOtherModelCacheSamples 验证配置排除模型 A 后，模型 B 的缓存样本数仍正确喵。
// 回归 P0-③：此前 len(excludedModels)>0 会清零整个聚合窗口的 cache 字段且不回填 sampleCount/hitCount，
// 导致配置排除模型后缓存样本数恒为 0 喵。
func TestQueryStatusExcludedModelsKeepOtherModelCacheSamples(t *testing.T) {
	setupStatusTestDB(t)
	// 使用当前时间桶，保证落在 QueryStatus 的查询窗口内喵。
	bucketTs := time.Now().Unix()
	group := "status-test-group"
	excludedModel := "user/cache-excluded"
	keptModel := "user/cache-kept"

	// 被排除模型 A：缓存样本 5、命中 3、输入 token 1000、缓存 token 800 喵。
	require.NoError(t, model.DB.Create(&model.PerfMetric{
		ModelName: excludedModel, Group: group, BucketTs: bucketTs,
		RequestCount: 10, SuccessCount: 9, TotalLatencyMs: 500,
		CacheHitCount: 3, CacheSampleCount: 5, CachedTokens: 800, InputTokens: 1000,
	}).Error)
	// 非排除模型 B：缓存样本 7、命中 6、输入 token 2000、缓存 token 1500 喵。
	require.NoError(t, model.DB.Create(&model.PerfMetric{
		ModelName: keptModel, Group: group, BucketTs: bucketTs,
		RequestCount: 10, SuccessCount: 10, TotalLatencyMs: 300,
		CacheHitCount: 6, CacheSampleCount: 7, CachedTokens: 1500, InputTokens: 2000,
	}).Error)

	// 只排除模型 A，模型 B 的缓存样本数必须完整保留喵。
	result, queryError := QueryStatus(24, []string{group}, []string{excludedModel})
	require.NoError(t, queryError)
	require.Len(t, result.Groups, 1)
	require.Equal(t, group, result.Groups[0].Group)
	// 缓存样本数应等于模型 B 的 7，而不是被清零喵。
	require.Equal(t, int64(7), result.Groups[0].CacheSampleCount)
	// 缓存输入 token 只保留模型 B 的 2000，排除模型 A 的 1000 被扣除喵。
	require.Equal(t, int64(2000), result.Groups[0].CacheInputTokens)
	// 请求数与成功数仍应包含两个模型的总和，排除模型不影响非缓存统计喵。
	require.Equal(t, int64(20), result.Groups[0].RequestCount)
	require.InDelta(t, 95.0, result.Groups[0].Availability, 0.001)
}

// TestRecordRelaySampleUsesAttemptStart 验证尝试级样本按 PerfAttemptStartTime 计算延迟与 TTFT 喵。
// 回归：虚拟模型失败规则 retry 会复用同一个 RelayInfo，若样本仍用 RelayInfo.StartTime，
// 第 N 次 try 的延迟会把前几次尝试也算进来，上一次的首字节还会污染本尝试的 TTFT 喵。
func TestRecordRelaySampleUsesAttemptStart(t *testing.T) {
	setupStatusTestDB(t)
	group := "attempt-sample-group"
	modelName := "gpt-attempt-sample"
	// 清掉全局热桶可能残留的同键样本，保证断言只看到本用例写入的数据喵。
	ClearEntityProbeHotBucket(modelName, group)
	t.Cleanup(func() {
		ClearEntityProbeHotBucket(modelName, group)
	})

	// 请求起点在 10 秒前、上一次尝试的首字节在 9 秒前；本次尝试起点在 2 秒前喵。
	relayInfo := &relaycommon.RelayInfo{
		OriginModelName:      modelName,
		UsingGroup:           group,
		IsStream:             true,
		StartTime:            time.Now().Add(-10 * time.Second),
		FirstResponseTime:    time.Now().Add(-9 * time.Second),
		PerfAttemptStartTime: time.Now().Add(-2 * time.Second),
	}
	RecordRelaySample(relayInfo, false, 0, nil)

	status, queryError := QueryStatus(1, []string{group}, nil)
	require.NoError(t, queryError)
	require.Len(t, status.Groups, 1)
	// 失败 try 必须进入分母且成功数为零喵。
	require.Equal(t, int64(1), status.Groups[0].RequestCount)
	require.InDelta(t, 0.0, status.Groups[0].Availability, 0.001)
	// 延迟按本次尝试起点计算：约 2 秒，而不是请求起点的 10 秒喵。
	require.GreaterOrEqual(t, status.Groups[0].AvgLatencyMs, int64(1900))
	require.Less(t, status.Groups[0].AvgLatencyMs, int64(3000))
	// 上一次尝试的首字节早于本次起点，TTFT 必须为零喵。
	require.Equal(t, int64(0), status.Groups[0].AvgTtftMs)

	// 清空尝试级起点后应回退到请求起点，验证零值语义喵。
	ClearEntityProbeHotBucket(modelName, group)
	relayInfo.PerfAttemptStartTime = time.Time{}
	RecordRelaySample(relayInfo, false, 0, nil)
	status, queryError = QueryStatus(1, []string{group}, nil)
	require.NoError(t, queryError)
	require.Len(t, status.Groups, 1)
	require.GreaterOrEqual(t, status.Groups[0].AvgLatencyMs, int64(9500))
	// 回退到请求起点后，9 秒前的首字节重新成为有效 TTFT 喵。
	require.GreaterOrEqual(t, status.Groups[0].AvgTtftMs, int64(900))
	// 清空热桶后只保留第二条失败样本，验证回退样本同样进入分母喵。
	require.Equal(t, int64(1), status.Groups[0].RequestCount)
	require.InDelta(t, 0.0, status.Groups[0].Availability, 0.001)
}

// TestRecordRelaySampleFakeStreamUsesUpstreamWindow 验证流转伪流样本按上游真实窗口计算 TPS 喵。
// 回归：伪流要等整段回答缓存完才回放，回放首字节距请求结束只有几毫秒，
// 若仍用「回放首字节→结算」当生成窗口，TPS 会被放大成几千 t/s 喵。
func TestRecordRelaySampleFakeStreamUsesUpstreamWindow(t *testing.T) {
	setupStatusTestDB(t)
	group := "fake-stream-sample-group"
	modelName := "gpt-fake-stream-sample"
	// 清掉全局热桶可能残留的同键样本，保证断言只看到本用例写入的数据喵。
	ClearEntityProbeHotBucket(modelName, group)
	t.Cleanup(func() {
		ClearEntityProbeHotBucket(modelName, group)
	})

	// 客户端视角：请求 10 秒前开始、回放首字节 1 秒前；上游真实窗口为 8 秒前到 2 秒前喵。
	relayInfo := &relaycommon.RelayInfo{
		OriginModelName:             modelName,
		UsingGroup:                  group,
		IsStream:                    true,
		StartTime:                   time.Now().Add(-10 * time.Second),
		FirstResponseTime:           time.Now().Add(-1 * time.Second),
		PerfUpstreamStreamStartTime: time.Now().Add(-8 * time.Second),
		PerfUpstreamStreamEndTime:   time.Now().Add(-2 * time.Second),
	}
	// 成功样本携带 600 个输出 token，供吞吐计算使用喵。
	RecordRelaySample(relayInfo, true, 600, nil)

	result, queryError := Query(QueryParams{Model: modelName, Group: group, Hours: 1})
	require.NoError(t, queryError)
	require.Len(t, result.Groups, 1)
	// 600 token ÷ 6 秒真实窗口 = 100 t/s；按旧口径会算成 600 token ÷ 1 秒 = 600 t/s 喵。
	require.InDelta(t, 100.0, result.Groups[0].AvgTps, 0.5)
	// 延迟与 TTFT 仍保留客户端感知口径：延迟约 10 秒、TTFT 约 9 秒喵。
	require.GreaterOrEqual(t, result.Groups[0].AvgLatencyMs, int64(9500))
	require.GreaterOrEqual(t, result.Groups[0].AvgTtftMs, int64(8500))

	// 清空伪流窗口后必须回退旧口径，普通流式采样不受影响喵。
	ClearEntityProbeHotBucket(modelName, group)
	relayInfo.PerfUpstreamStreamStartTime = time.Time{}
	relayInfo.PerfUpstreamStreamEndTime = time.Time{}
	// 普通流式的生成窗口为「首字节→结算」，这里设为 2 秒，期望 600 token ÷ 2 秒 = 300 t/s 喵。
	relayInfo.FirstResponseTime = time.Now().Add(-2 * time.Second)
	RecordRelaySample(relayInfo, true, 600, nil)

	result, queryError = Query(QueryParams{Model: modelName, Group: group, Hours: 1})
	require.NoError(t, queryError)
	require.Len(t, result.Groups, 1)
	// 普通流式不得被伪流窗口逻辑影响，TPS 应接近 300 t/s 喵。
	require.InDelta(t, 300.0, result.Groups[0].AvgTps, 2.0)
}
