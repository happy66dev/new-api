package controller

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestMain 为 controller 包测试初始化最小内存数据库喵。
func TestMain(m *testing.M) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		panic("failed to open test db: " + err.Error())
	}
	model.DB = db
	model.LOG_DB = db
	if err := db.AutoMigrate(&model.UserUpstreamModel{}); err != nil {
		panic("failed to migrate: " + err.Error())
	}
	// 喵~防御：真实运行库的历史表没有 normalized_name 唯一索引，删除内存库中 AutoMigrate 生成的索引以贴近真实约束喵。
	if err := db.Exec("DROP INDEX IF EXISTS idx_upstream_owner_name").Error; err != nil {
		panic("failed to drop index: " + err.Error())
	}
	os.Exit(m.Run())
}

// TestValidateSharedNameGlobalUniqueness 验证全局共享命名池的唯一性校验喵。
func TestValidateSharedNameGlobalUniqueness(t *testing.T) {
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
