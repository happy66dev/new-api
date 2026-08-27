package perfmetrics

import (
	"fmt"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

// setupEntityProbeTestDB 通过 InitDB 初始化内存库并恢复全局状态喵。
func setupEntityProbeTestDB(t *testing.T) {
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

	// 内存共享缓存库，配合 SQL_DSN=local 让 InitDB 走 SQLite 分支并初始化列名喵。
	common.IsMasterNode = false
	common.SQLitePath = fmt.Sprintf("file:%s?mode=memory&cache=shared", "perf_metrics_entity_probe_test")
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, os.Setenv("SQL_DSN", "local"))

	require.NoError(t, model.InitDB())
	require.NoError(t, model.DB.AutoMigrate(&model.PerfMetric{}))
}

// TestRecordEntityProbeAndQueryStatus 验证实体被动统计记录后可聚合查询，自用与共享维度隔离喵。
func TestRecordEntityProbeAndQueryStatus(t *testing.T) {
	setupEntityProbeTestDB(t)

	// 自用维度：一次成功一次失败，成功率应为 50% 喵。
	RecordEntityProbe("user/demo", 200, true, EntityProbeExtras{})
	RecordEntityProbe("user/demo", 400, false, EntityProbeExtras{})

	// 共享维度：只有一次成功，与自用维度互不混用喵。
	RecordEntityProbeShared("user/demo", 100, true, EntityProbeExtras{})

	selfStatus, selfErr := QueryEntityProbeStatus("user/demo", EntityProbeGroupSelf, 24)
	require.NoError(t, selfErr)
	require.Equal(t, int64(2), selfStatus.RequestCount)
	require.InDelta(t, 50.0, selfStatus.Availability, 0.001)
	require.Equal(t, int64(300), selfStatus.AvgLatencyMs)
	require.Len(t, selfStatus.Availability24, 1)
	require.InDelta(t, 50.0, selfStatus.Availability24[0], 0.001)

	sharedStatus, sharedErr := QueryEntityProbeStatus("user/demo", EntityProbeGroupShared, 24)
	require.NoError(t, sharedErr)
	require.Equal(t, int64(1), sharedStatus.RequestCount)
	require.InDelta(t, 100.0, sharedStatus.Availability, 0.001)
}

// TestRecordEntityProbeRequiresEnabledSetting 验证性能指标关闭时实体记录不产生数据喵。
func TestRecordEntityProbeRequiresEnabledSetting(t *testing.T) {
	setupEntityProbeTestDB(t)

	// 直接调用底层 Record 的空模型保护：空模型名被忽略喵。
	RecordEntityProbe("", 200, true, EntityProbeExtras{})

	emptyStatus, emptyErr := QueryEntityProbeStatus("", EntityProbeGroupSelf, 24)
	require.NoError(t, emptyErr)
	require.Equal(t, int64(0), emptyStatus.RequestCount)
}

// TestRecordEntityProbeSharedTracksThroughputAndTTFT 验证共享探针携带 TTFT 与吞吐后可被 perf 查询聚合喵。
func TestRecordEntityProbeSharedTracksThroughputAndTTFT(t *testing.T) {
	setupEntityProbeTestDB(t)

	// 共享维度一次成功：TTFT 30ms、输出 100 token、生成时长 70ms（延迟 100ms 减 TTFT）喵。
	RecordEntityProbeShared("user/ttft-demo", 100, true, EntityProbeExtras{TtftMs: 30, HasTtft: true, OutputTokens: 100, GenerationMs: 70})

	// 通过 perf 查询聚合，验证 TTFT 均值与吞吐（100 token / 0.07s ≈ 1428.57）喵。
	result, queryErr := Query(QueryParams{Model: "user/ttft-demo", Group: EntityProbeGroupShared, Hours: 24})
	require.NoError(t, queryErr)
	require.Len(t, result.Groups, 1)
	group := result.Groups[0]
	require.Equal(t, EntityProbeGroupShared, group.Group)
	require.Equal(t, int64(30), group.AvgTtftMs)
	require.InDelta(t, 1428.57, group.AvgTps, 0.01)
}
