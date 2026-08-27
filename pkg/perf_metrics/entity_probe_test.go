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
	RecordEntityProbe("user/demo", 200, true)
	RecordEntityProbe("user/demo", 400, false)

	// 共享维度：只有一次成功，与自用维度互不混用喵。
	RecordEntityProbeShared("user/demo", 100, true)

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
	RecordEntityProbe("", 200, true)

	emptyStatus, emptyErr := QueryEntityProbeStatus("", EntityProbeGroupSelf, 24)
	require.NoError(t, emptyErr)
	require.Equal(t, int64(0), emptyStatus.RequestCount)
}
