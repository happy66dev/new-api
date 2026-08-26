package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestValidateSharedNameGlobalUniqueness 验证全局共享命名池的唯一性校验喵。
func TestValidateSharedNameGlobalUniqueness(t *testing.T) {
	// controller 包其他测试文件会替换并关闭全局 model.DB，故本用例自建内存库且结束后恢复喵。
	oldDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	// 喵~防御：清理时关闭自建库并恢复调用前的全局 DB，避免污染同包后续测试喵。
	t.Cleanup(func() {
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
		model.DB = oldDB
	})

	require.NoError(t, db.AutoMigrate(&model.UserUpstreamModel{}))
	// 喵~防御：真实运行库的历史表没有 normalized_name 唯一索引，删除内存库中 AutoMigrate 生成的索引以贴近真实约束喵。
	require.NoError(t, db.Exec("DROP INDEX IF EXISTS idx_upstream_owner_name").Error)

	require.NoError(t, model.DB.Exec("DELETE FROM user_upstream_models").Error)

	// 用户7 共享开启名为 alpha，占用了全局共享池名字喵。
	require.NoError(t, model.DB.Create(&model.UserUpstreamModel{OwnerUserID: 7, NormalizedName: "alpha", ShareEnabled: true, Version: 1}).Error)
	// 用户8 的非共享模型不占用共享池名字喵。
	require.NoError(t, model.DB.Create(&model.UserUpstreamModel{OwnerUserID: 8, NormalizedName: "private-name", ShareEnabled: false, Version: 1}).Error)

	// 用户8 想共享 alpha：与他人共享冲突，拒绝喵。
	require.Error(t, validateSharedNameGlobalUniqueness(8, "alpha", 0))

	// 用户7 更新自己的共享 alpha：排除自身条目，允许喵。
	require.NoError(t, validateSharedNameGlobalUniqueness(7, "alpha", 1))

	// 用户8 想共享无人占用的名字：允许喵。
	require.NoError(t, validateSharedNameGlobalUniqueness(8, "beta", 0))

	// 空名不触发校验喵。
	require.NoError(t, validateSharedNameGlobalUniqueness(8, "  ", 0))

	// 用户7 关闭共享后，共享池名字释放，用户8 可共享 alpha 喵。
	require.NoError(t, model.DB.Model(&model.UserUpstreamModel{}).Where("id = ?", 1).Update("share_enabled", false).Error)
	require.NoError(t, validateSharedNameGlobalUniqueness(8, "alpha", 0))
}
