package perfmetrics

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

// setupFlushTestDB 初始化独立内存库并恢复全局状态，避免与其他测试的 SQLite 内存库串扰喵。
func setupFlushTestDB(t *testing.T) {
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
	common.SQLitePath = fmt.Sprintf("file:%s?mode=memory&cache=shared", "perf_metrics_flush_test")
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, os.Setenv("SQL_DSN", "local"))

	require.NoError(t, model.InitDB())
	require.NoError(t, model.DB.AutoMigrate(&model.PerfMetric{}))
}

// cleanupHotBucketKey 删除测试写入的指定模型+分组热桶，避免全局 hotBuckets 串扰后续测试喵。
func cleanupHotBucketKey(t *testing.T, modelName string, group string) {
	t.Helper()
	hotBuckets.Range(func(key, value any) bool {
		k := key.(bucketKey)
		if k.model == modelName && k.group == group {
			hotBuckets.Delete(key)
		}
		return true
	})
}

// seedBucket 直接往全局热桶写入一个指定时间桶，绕过设置开关以便独立验证 flush 行为喵。
func seedBucket(modelName string, group string, bucketTs int64, samples ...Sample) *atomicBucket {
	bucket := &atomicBucket{}
	for _, sample := range samples {
		bucket.add(sample)
	}
	hotBuckets.Store(bucketKey{model: modelName, group: group, bucketTs: bucketTs}, bucket)
	return bucket
}

// TestFlushOneBucketPersistsAndDrains 验证单个已完成桶 flush 后落库且热桶被 drain 清零喵。
func TestFlushOneBucketPersistsAndDrains(t *testing.T) {
	setupFlushTestDB(t)
	modelName := "flush-one-bucket"
	group := "default"
	t.Cleanup(func() { cleanupHotBucketKey(t, modelName, group) })

	// 过去一小时的历史桶，模拟已结束小时的数据喵。
	oldBucketTs := bucketStart(time.Now().Unix()) - 3600
	bucket := seedBucket(modelName, group, oldBucketTs,
		Sample{Model: modelName, Group: group, LatencyMs: 200, Success: true},
	)

	flushOneBucket(bucketKey{model: modelName, group: group, bucketTs: oldBucketTs}, bucketKey{model: modelName, group: group, bucketTs: oldBucketTs}, bucket)

	// 断言 DB 已落库一行且计数正确喵。
	rows, queryErr := model.GetPerfMetrics(modelName, group, oldBucketTs-1, oldBucketTs+1)
	require.NoError(t, queryErr)
	require.Len(t, rows, 1)
	require.Equal(t, int64(1), rows[0].RequestCount)
	require.Equal(t, int64(1), rows[0].SuccessCount)
	require.Equal(t, int64(200), rows[0].TotalLatencyMs)

	// 断言热桶已被 drain 清零喵。
	require.Equal(t, int64(0), bucket.requestCount.Load())
}

// TestFlushCurrentEntityProbeBucketsOnlyEntityGroups 验证当前小时桶只落实体检测分组喵。
func TestFlushCurrentEntityProbeBucketsOnlyEntityGroups(t *testing.T) {
	setupFlushTestDB(t)
	sharedModel := "flush-entity-shared"
	defaultModel := "flush-entity-default"
	t.Cleanup(func() {
		cleanupHotBucketKey(t, sharedModel, EntityProbeGroupShared)
		cleanupHotBucketKey(t, defaultModel, "default")
	})

	currentBucket := bucketStart(time.Now().Unix())
	// 当前小时桶：共享实体分组与普通 default 分组各一个喵。
	sharedBucket := seedBucket(sharedModel, EntityProbeGroupShared, currentBucket,
		Sample{Model: sharedModel, Group: EntityProbeGroupShared, LatencyMs: 100, Success: true},
	)
	defaultBucket := seedBucket(defaultModel, "default", currentBucket,
		Sample{Model: defaultModel, Group: "default", LatencyMs: 50, Success: true},
	)

	flushCurrentEntityProbeBuckets()

	// 共享实体分组当前桶已落库喵。
	sharedRows, sharedErr := model.GetPerfMetrics(sharedModel, EntityProbeGroupShared, currentBucket-1, currentBucket+1)
	require.NoError(t, sharedErr)
	require.Len(t, sharedRows, 1)
	// 共享实体分组热桶已被 drain 清零喵。
	require.Equal(t, int64(0), sharedBucket.requestCount.Load())

	// default 分组当前桶未落库，且热桶数据原样保留喵。
	defaultRows, defaultErr := model.GetPerfMetrics(defaultModel, "default", currentBucket-1, currentBucket+1)
	require.NoError(t, defaultErr)
	require.Len(t, defaultRows, 0)
	require.Equal(t, int64(1), defaultBucket.requestCount.Load())
}

// TestFlushAllIncludesCurrentBucketAnyGroup 验证 FlushAll 会落库当前小时桶的任意分组喵。
func TestFlushAllIncludesCurrentBucketAnyGroup(t *testing.T) {
	setupFlushTestDB(t)
	modelName := "flush-all-current"
	group := "default"
	t.Cleanup(func() { cleanupHotBucketKey(t, modelName, group) })

	currentBucket := bucketStart(time.Now().Unix())
	seedBucket(modelName, group, currentBucket,
		Sample{Model: modelName, Group: group, LatencyMs: 120, Success: true},
	)

	FlushAll()

	rows, queryErr := model.GetPerfMetrics(modelName, group, currentBucket-1, currentBucket+1)
	require.NoError(t, queryErr)
	require.Len(t, rows, 1)
	require.Equal(t, int64(1), rows[0].RequestCount)
	require.Equal(t, int64(120), rows[0].TotalLatencyMs)
}

// TestFlushDoesNotDoubleCountOnSecondFlush 验证同一当前桶重复 flush 只做加性合并不重复计数喵。
func TestFlushDoesNotDoubleCountOnSecondFlush(t *testing.T) {
	setupFlushTestDB(t)
	modelName := "flush-twice"
	group := EntityProbeGroupShared
	t.Cleanup(func() { cleanupHotBucketKey(t, modelName, group) })

	currentBucket := bucketStart(time.Now().Unix())
	// 第一次：记录一个样本并 flush 当前实体分组喵。
	RecordEntityProbeShared(modelName, 150, true, EntityProbeExtras{})
	flushCurrentEntityProbeBuckets()

	// 第二次：再记录一个样本并 flush，DB 行应累计为 2 而非翻倍喵。
	RecordEntityProbeShared(modelName, 250, true, EntityProbeExtras{})
	flushCurrentEntityProbeBuckets()

	rows, queryErr := model.GetPerfMetrics(modelName, group, currentBucket-1, currentBucket+1)
	require.NoError(t, queryErr)
	require.Len(t, rows, 1)
	require.Equal(t, int64(2), rows[0].RequestCount)
	require.Equal(t, int64(400), rows[0].TotalLatencyMs)
}
