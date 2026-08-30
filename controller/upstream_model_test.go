package controller

import (
	"strings"
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

// TestSaveUpstreamModelFieldsTimeoutBounds 验证自用上游模型超时秒数的边界校验喵。
func TestSaveUpstreamModelFieldsTimeoutBounds(t *testing.T) {
	// 复用自建内存库避免依赖全局 model.DB 喵。
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

	// 每次保存前重建既有模型对象，避免 saveUpstreamModelFields 递增版本触发乐观锁冲突喵。
	newExisting := func() *model.UserUpstreamModel {
		return &model.UserUpstreamModel{ID: 1, Version: 1}
	}
	validInput := upstreamModelInput{
		NormalizedName: "timeout-model",
		DisplayName:    "Timeout Model",
		RealModelName:  "gpt-4o",
		AuthStyle:      "bearer",
		Version:        1,
	}
	// 零值表示沿用默认 60 秒，必须允许保存喵。
	validInput.TimeoutSeconds = 0
	zeroExisting := newExisting()
	require.NoError(t, saveUpstreamModelFields(validInput, 7, zeroExisting))
	require.Equal(t, 0, zeroExisting.TimeoutSeconds)
	// 合法上界 600 秒允许保存喵。
	validInput.TimeoutSeconds = 600
	maxExisting := newExisting()
	require.NoError(t, saveUpstreamModelFields(validInput, 7, maxExisting))
	require.Equal(t, 600, maxExisting.TimeoutSeconds)
	// 喵~防御：负数与超过 600 秒的超时必须拒绝保存喵。
	invalidInput := validInput
	invalidInput.TimeoutSeconds = -1
	require.Error(t, saveUpstreamModelFields(invalidInput, 7, newExisting()))
	invalidInput.TimeoutSeconds = 601
	require.Error(t, saveUpstreamModelFields(invalidInput, 7, newExisting()))
}

// TestSaveUpstreamModelFieldsIcon 验证图标键名保存与超长拒绝喵。
func TestSaveUpstreamModelFieldsIcon(t *testing.T) {
	// 复用自建内存库避免依赖全局 model.DB 喵。
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

	// 每次保存前重建既有模型对象，避免 saveUpstreamModelFields 递增版本触发乐观锁冲突喵。
	newExisting := func() *model.UserUpstreamModel {
		return &model.UserUpstreamModel{ID: 1, Version: 1}
	}
	validInput := upstreamModelInput{
		NormalizedName: "icon-model",
		DisplayName:    "Icon Model",
		RealModelName:  "gpt-4o",
		AuthStyle:      "bearer",
		Icon:           "OpenAI.Color",
		Version:        1,
	}
	// 合法图标键名必须写入模型对象，供随后的 Updates 落库喵。
	iconExisting := newExisting()
	require.NoError(t, saveUpstreamModelFields(validInput, 7, iconExisting))
	require.Equal(t, "OpenAI.Color", iconExisting.Icon)
	// 喵~防御：超过 128 字符的图标键名必须拒绝保存，避免污染模型广场展示喵。
	oversizeInput := validInput
	oversizeInput.Icon = strings.Repeat("x", 129)
	require.Error(t, saveUpstreamModelFields(oversizeInput, 7, newExisting()))
}

// TestUpstreamModelUpdateColumnsIncludeIcon 验证更新白名单必须包含 icon 列喵。
// 曾因 Select 白名单漏列 icon 导致图标修改不落库、保存后重新进入仍为空，此测试守护该回归喵。
func TestUpstreamModelUpdateColumnsIncludeIcon(t *testing.T) {
	// 白名单必须覆盖 icon，否则 GORM Select 更新会跳过该列喵。
	require.Contains(t, upstreamModelUpdateColumns, "icon")
	// 关键列仍然存在，避免误删导致更新路径功能倒退喵。
	for _, column := range []string{"normalized_name", "display_name", "enabled", "real_model_name", "version", "updated_time"} {
		require.Contains(t, upstreamModelUpdateColumns, column)
	}
}
