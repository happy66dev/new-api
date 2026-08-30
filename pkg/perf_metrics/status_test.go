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
