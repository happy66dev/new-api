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
	for _, column := range []string{"normalized_name", "display_name", "description", "enabled", "real_model_name", "auth_style", "api_type", "timeout_seconds", "version", "updated_time", "share_whitelist", "share_blacklist", "share_list_mode"} {
		require.Contains(t, upstreamModelUpdateColumns, column)
	}
}

// TestUpstreamModelUpdateColumnsFullCoverage 用表驱动遍历断言全部可编辑列都在更新白名单中喵。
func TestUpstreamModelUpdateColumnsFullCoverage(t *testing.T) {
	// 可编辑列全集：新增可编辑字段时必须同步加入白名单，否则 GORM Select 更新会静默跳过该列喵。
	requiredColumns := []string{
		"normalized_name", "display_name", "description", "enabled", "icon",
		"encrypted_base_url", "encrypted_api_key", "base_url_fingerprint", "api_key_fingerprint", "credential_version",
		"real_model_name", "auth_style", "api_type", "timeout_seconds", "custom_headers", "field_replacements",
		"model_ratio", "completion_ratio", "cache_ratio", "cache_creation_ratio", "cache_creation_5m_ratio", "cache_creation_1h_ratio",
		"image_ratio", "audio_ratio", "audio_completion_ratio",
		"balance_cents", "available_cents", "balance_check_enabled", "balance_check_path",
		"share_enabled", "share_limit_cents", "share_whitelist", "share_blacklist", "share_list_mode", "show_balance_enabled",
		"version", "updated_time",
	}
	// 先把白名单转成集合，再逐个断言可编辑列都在其中，防止漏列导致修改不落库喵。
	covered := make(map[string]bool, len(upstreamModelUpdateColumns))
	for _, column := range upstreamModelUpdateColumns {
		covered[column] = true
	}
	for _, column := range requiredColumns {
		require.True(t, covered[column], "更新白名单缺少可编辑列: %s", column)
	}
}

// TestSaveUpstreamModelFieldsAPITypeValidation 验证 api_type 归一化、空值保留与非法值拒绝喵。
func TestSaveUpstreamModelFieldsAPITypeValidation(t *testing.T) {
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
	baseInput := upstreamModelInput{
		NormalizedName: "api-type-model",
		DisplayName:    "API Type Model",
		RealModelName:  "gpt-4o",
		AuthStyle:      "bearer",
		Version:        1,
	}
	// 大小写混写必须归一化为小写 anthropic 喵。
	normalizedInput := baseInput
	normalizedInput.APIType = "AnThRoPiC"
	normalizedExisting := newExisting()
	require.NoError(t, saveUpstreamModelFields(normalizedInput, 7, normalizedExisting))
	require.Equal(t, "anthropic", normalizedExisting.APIType)
	// 显式 openai 原样保存喵。
	openAIInput := baseInput
	openAIInput.APIType = "openai"
	openAIExisting := newExisting()
	require.NoError(t, saveUpstreamModelFields(openAIInput, 7, openAIExisting))
	require.Equal(t, "openai", openAIExisting.APIType)
	// 空值编辑时保留原值：既有值为 anthropic 时不因空提交被重置喵。
	keepInput := baseInput
	keepExisting := newExisting()
	keepExisting.APIType = "anthropic"
	require.NoError(t, saveUpstreamModelFields(keepInput, 7, keepExisting))
	require.Equal(t, "anthropic", keepExisting.APIType)
	// 喵~防御：非法 api_type（如 gpt）必须拒绝保存喵。
	invalidInput := baseInput
	invalidInput.APIType = "gpt"
	require.Error(t, saveUpstreamModelFields(invalidInput, 7, newExisting()))
}

// TestSaveUpstreamModelFieldsShareListValidation 验证共享名单模式、互斥与长度校验喵。
func TestSaveUpstreamModelFieldsShareListValidation(t *testing.T) {
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
	baseInput := upstreamModelInput{
		NormalizedName: "share-list-model",
		DisplayName:    "Share List Model",
		RealModelName:  "gpt-4o",
		AuthStyle:      "bearer",
		Version:        1,
	}
	// 白名单模式下黑名单必须被清空，与前端只发 share_whitelist 的语义一致喵。
	whitelistInput := baseInput
	whitelistInput.ShareListMode = "whitelist"
	whitelistInput.ShareWhitelist = "11,22"
	whitelistInput.ShareBlacklist = "3,4"
	whitelistExisting := newExisting()
	require.NoError(t, saveUpstreamModelFields(whitelistInput, 7, whitelistExisting))
	require.Equal(t, "whitelist", whitelistExisting.ShareListMode)
	require.Equal(t, "11,22", whitelistExisting.ShareWhitelist)
	require.Equal(t, "", whitelistExisting.ShareBlacklist)
	// 黑名单模式下白名单必须被清空喵。
	blacklistInput := baseInput
	blacklistInput.ShareListMode = "blacklist"
	blacklistInput.ShareWhitelist = "11,22"
	blacklistInput.ShareBlacklist = "3,4"
	blacklistExisting := newExisting()
	require.NoError(t, saveUpstreamModelFields(blacklistInput, 7, blacklistExisting))
	require.Equal(t, "blacklist", blacklistExisting.ShareListMode)
	require.Equal(t, "", blacklistExisting.ShareWhitelist)
	require.Equal(t, "3,4", blacklistExisting.ShareBlacklist)
	// 空模式表示解除名单限制，两列都被清空喵。
	emptyModeInput := baseInput
	emptyModeInput.ShareListMode = ""
	emptyModeInput.ShareWhitelist = "11,22"
	emptyModeInput.ShareBlacklist = "3,4"
	emptyModeExisting := newExisting()
	require.NoError(t, saveUpstreamModelFields(emptyModeInput, 7, emptyModeExisting))
	require.Equal(t, "", emptyModeExisting.ShareListMode)
	require.Equal(t, "", emptyModeExisting.ShareWhitelist)
	require.Equal(t, "", emptyModeExisting.ShareBlacklist)
	// 空名单内容合法：白名单模式下允许空名单（等价于仅属主可用）喵。
	emptyListInput := baseInput
	emptyListInput.ShareListMode = "whitelist"
	emptyListExisting := newExisting()
	require.NoError(t, saveUpstreamModelFields(emptyListInput, 7, emptyListExisting))
	require.Equal(t, "", emptyListExisting.ShareWhitelist)
	// 喵~防御：非法名单模式必须拒绝保存喵。
	invalidModeInput := baseInput
	invalidModeInput.ShareListMode = "denylist"
	require.Error(t, saveUpstreamModelFields(invalidModeInput, 7, newExisting()))
	// 喵~防御：超过 1000 字符的名单必须拒绝保存，避免超长文本拖慢查询喵。
	oversizeInput := baseInput
	oversizeInput.ShareListMode = "whitelist"
	oversizeInput.ShareWhitelist = strings.Repeat("9", 1001)
	require.Error(t, saveUpstreamModelFields(oversizeInput, 7, newExisting()))
}

// TestSaveUpstreamModelFieldsRoundTrip 验证 description/timeout_seconds/api_type/共享名单字段经白名单更新后完整落库喵。
func TestSaveUpstreamModelFieldsRoundTrip(t *testing.T) {
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

	// 直接插入一行初始记录，模拟已存在的上游模型（凭据为占位密文即可，更新路径不要求解密）喵。
	initialRow := &model.UserUpstreamModel{
		OwnerUserID:      7,
		NormalizedName:   "roundtrip-model",
		DisplayName:      "Old Name",
		Description:      "Old Desc",
		RealModelName:    "gpt-4o",
		AuthStyle:        "bearer",
		APIType:          "openai",
		TimeoutSeconds:   30,
		EncryptedBaseURL: "dummy-encrypted-base-url",
		EncryptedAPIKey:  "dummy-encrypted-api-key",
		ShareListMode:    "blacklist",
		ShareBlacklist:   "1,2",
		Version:          1,
	}
	require.NoError(t, db.Create(initialRow).Error)
	// 内存库首条记录 ID 从 1 开始喵。
	require.Equal(t, int64(1), initialRow.ID)

	// 加载既有记录副本，走与 UpdateUserUpstreamModel 相同的更新路径保存新值喵。
	loaded, loadError := model.GetUserUpstreamModelByOwnerID(initialRow.ID, 7)
	require.NoError(t, loadError)

	input := upstreamModelInput{
		NormalizedName:       "roundtrip-model",
		DisplayName:          "New Name",
		Description:          "New Desc",
		Icon:                 "OpenAI.Color",
		Enabled:              true,
		RealModelName:        "claude-3-7-sonnet",
		AuthStyle:            "bearer",
		APIType:              "AnThRoPiC",
		TimeoutSeconds:       120,
		ModelRatio:           "2",
		CompletionRatio:      "1.5",
		CacheRatio:           "1",
		CacheCreationRatio:   "1",
		CacheCreation5mRatio: "1",
		CacheCreation1hRatio: "1",
		ImageRatio:           "1",
		AudioRatio:           "1",
		AudioCompletionRatio: "1",
		BalanceCents:         100,
		AvailableCents:       200,
		ShareEnabled:         true,
		ShareLimitCents:      300,
		ShareListMode:        "whitelist",
		ShareWhitelist:       "11,22",
		ShareBlacklist:       "1,2",
		ShowBalanceEnabled:   true,
		Version:              1,
	}
	// 保存前记录旧版本号，作为更新 WHERE 条件基准喵。
	existingVersion := loaded.Version
	require.NoError(t, saveUpstreamModelFields(input, 7, loaded))
	// 版本号递增，且新值已写入内存对象喵。
	require.Equal(t, existingVersion+1, loaded.Version)
	require.Equal(t, "New Desc", loaded.Description)
	require.Equal(t, 120, loaded.TimeoutSeconds)
	require.Equal(t, "anthropic", loaded.APIType)
	require.Equal(t, "whitelist", loaded.ShareListMode)
	require.Equal(t, "11,22", loaded.ShareWhitelist)
	require.Equal(t, "", loaded.ShareBlacklist)

	// 复刻 UpdateUserUpstreamModel 的精确更新模式：WHERE 绑定旧版本 + Select 白名单更新喵。
	updateResult := model.DB.Model(loaded).
		Where("id = ? AND owner_user_id = ? AND version = ?", initialRow.ID, 7, existingVersion).
		Select(upstreamModelUpdateColumns).
		Updates(loaded)
	require.NoError(t, updateResult.Error)
	require.Equal(t, int64(1), updateResult.RowsAffected)

	// 重新读库验证全部新值已真实落库，而不是只停留在内存对象喵。
	refreshed, refreshError := model.GetUserUpstreamModelByOwnerID(initialRow.ID, 7)
	require.NoError(t, refreshError)
	require.Equal(t, "New Desc", refreshed.Description)
	require.Equal(t, 120, refreshed.TimeoutSeconds)
	require.Equal(t, "anthropic", refreshed.APIType)
	require.Equal(t, "whitelist", refreshed.ShareListMode)
	require.Equal(t, "11,22", refreshed.ShareWhitelist)
	require.Equal(t, "", refreshed.ShareBlacklist)
}
