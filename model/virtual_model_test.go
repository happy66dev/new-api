package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestNormalizeVirtualModelName 验证虚拟模型名称的规范化和非法输入防御喵。
func TestNormalizeVirtualModelName(t *testing.T) {
	// 定义正常输入、带前缀输入和非法输入，覆盖调用入口的主要边界喵。
	testCases := []struct {
		name        string
		input       string
		expected    string
		expectError bool
	}{
		{name: "plain name", input: "Research_Model-1", expected: "research_model-1"},
		{name: "virtual prefix", input: "virtual/Research_Model-1", expected: "research_model-1"},
		{name: "empty name", input: "", expectError: true},
		{name: "nested path", input: "team/model", expectError: true},
		{name: "unsupported unicode", input: "模型", expectError: true},
		{name: "space", input: "model name", expectError: true},
	}
	// 逐项执行名称校验并检查规范化结果，避免非法资源进入数据库喵。
	for _, testCase := range testCases {
		// 运行单个子测试，失败时明确指出输入边界喵。
		t.Run(testCase.name, func(t *testing.T) {
			// 调用被测名称规范化函数，获取结果和错误状态喵。
			normalizedName, err := NormalizeVirtualModelName(testCase.input)
			// 校验错误预期，避免错误输入被静默接受喵。
			if (err != nil) != testCase.expectError {
				t.Fatalf("NormalizeVirtualModelName(%q) error = %v, wantError = %v", testCase.input, err, testCase.expectError)
			}
			// 正常输入必须得到稳定的小写名称，便于 owner + name 唯一查询喵。
			if !testCase.expectError && normalizedName != testCase.expected {
				t.Fatalf("NormalizeVirtualModelName(%q) = %q, want %q", testCase.input, normalizedName, testCase.expected)
			}
		})
	}
}

// TestValidateVirtualModelConfiguration 验证模型所有权和安全参数边界喵。
func TestValidateVirtualModelConfiguration(t *testing.T) {
	// 构造一份合法模型作为基准，后续逐项验证防御条件喵。
	validModel := &VirtualModel{OwnerUserID: 7, NormalizedName: "private-model", DisplayName: "Private Model", TotalTimeoutSeconds: 120, MaxLoopRounds: 2}
	// 合法配置必须通过服务端校验，保证默认控制面可以保存喵。
	if err := ValidateVirtualModelConfiguration(validModel); err != nil {
		t.Fatalf("valid model rejected: %v", err)
	}
	// 空所有者必须被拒绝，防止客户端伪造或遗漏 owner 条件喵。
	invalidOwner := *validModel
	invalidOwner.OwnerUserID = 0
	if err := ValidateVirtualModelConfiguration(&invalidOwner); err == nil {
		t.Fatal("model without owner should be rejected")
	}
	// 超长循环超时必须被拒绝，防止请求占用资源无界增长喵。
	invalidTimeout := *validModel
	invalidTimeout.TotalTimeoutSeconds = 3601
	if err := ValidateVirtualModelConfiguration(&invalidTimeout); err == nil {
		t.Fatal("model with excessive timeout should be rejected")
	}
}

// TestGetVirtualModelsByOwnerToken 验证用户和 API Key 双重隔离以及启用状态过滤喵。
func TestGetVirtualModelsByOwnerToken(t *testing.T) {
	// 使用独立内存数据库，避免测试污染真实数据库连接喵。
	database, err := gorm.Open(sqlite.Open("file:virtual-model-query-test?mode=memory&cache=shared"), &gorm.Config{})
	// 喵~防御：数据库初始化失败时立即终止测试，避免后续空指针或假阳性喵。
	require.NoError(t, err)
	// 保存全局数据库指针，测试结束后恢复调用方环境喵。
	originalDatabase := DB
	DB = database
	// 恢复全局数据库指针并关闭临时连接，避免资源泄漏喵。
	t.Cleanup(func() {
		DB = originalDatabase
		sqlDatabase, closeError := database.DB()
		if closeError == nil {
			_ = sqlDatabase.Close()
		}
	})
	// 创建授权查询需要的两张表喵。
	require.NoError(t, database.AutoMigrate(&VirtualModel{}, &VirtualModelTokenBinding{}))
	// 插入当前用户授权且启用的模型作为正向样本喵。
	enabledModel := VirtualModel{OwnerUserID: 7, NormalizedName: "enabled-model", DisplayName: "Enabled", Enabled: true}
	require.NoError(t, database.Create(&enabledModel).Error)
	require.NoError(t, database.Create(&VirtualModelTokenBinding{VirtualModelID: enabledModel.ID, TokenID: 11, OwnerUserID: 7}).Error)
	// 插入停用模型，验证停用资源不会进入目录喵。
	disabledModel := VirtualModel{OwnerUserID: 7, NormalizedName: "disabled-model", DisplayName: "Disabled", Enabled: false}
	require.NoError(t, database.Create(&disabledModel).Error)
	require.NoError(t, database.Create(&VirtualModelTokenBinding{VirtualModelID: disabledModel.ID, TokenID: 11, OwnerUserID: 7}).Error)
	// 插入其他用户的同名授权，验证同名资源仍然保持用户隔离喵。
	otherOwnerModel := VirtualModel{OwnerUserID: 8, NormalizedName: "other-model", DisplayName: "Other", Enabled: true}
	require.NoError(t, database.Create(&otherOwnerModel).Error)
	require.NoError(t, database.Create(&VirtualModelTokenBinding{VirtualModelID: otherOwnerModel.ID, TokenID: 11, OwnerUserID: 8}).Error)
	// 查询当前用户和当前 API Key 的可调用模型喵。
	virtualModels, queryError := GetVirtualModelsByOwnerToken(7, 11)
	// 查询必须成功且只返回唯一的启用模型喵。
	require.NoError(t, queryError)
	require.Len(t, virtualModels, 1)
	require.Equal(t, "enabled-model", virtualModels[0].NormalizedName)
	// 使用其他 API Key 查询时不得返回当前绑定关系喵。
	otherTokenModels, otherTokenError := GetVirtualModelsByOwnerToken(7, 12)
	// 喵~防御：未绑定 API Key 应返回空集合而不是数据库错误喵。
	require.NoError(t, otherTokenError)
	require.Empty(t, otherTokenModels)
}

// TestVirtualModelAuthStyleNormalization 验证稳定控制面认证值与旧持久化值的兼容转换喵。
func TestVirtualModelAuthStyleNormalization(t *testing.T) {
	// 定义稳定 wire 值、旧数据库值和未知值，确保未知值绝不被错误降级喵。
	testCases := []struct {
		name        string
		authStyle   VirtualModelAuthStyle
		expected    VirtualModelAuthStyle
		expectError bool
	}{
		{name: "bearer", authStyle: VirtualModelAuthBearer, expected: VirtualModelAuthBearer},
		{name: "api key", authStyle: VirtualModelAuthAPIKey, expected: VirtualModelAuthAPIKey},
		{name: "anthropic", authStyle: VirtualModelAuthAnthropic, expected: VirtualModelAuthAnthropic},
		{name: "legacy api key", authStyle: virtualModelAuthLegacyAPIKey, expected: VirtualModelAuthAPIKey},
		{name: "legacy anthropic", authStyle: virtualModelAuthLegacyAnthropic, expected: VirtualModelAuthAnthropic},
		{name: "unknown", authStyle: "invalid", expectError: true},
	}
	// 逐项校验控制面输入的归一化行为喵。
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			normalizedAuthStyle, normalizeError := NormalizeVirtualModelAuthStyle(testCase.authStyle)
			require.Equal(t, testCase.expectError, normalizeError != nil)
			if !testCase.expectError {
				require.Equal(t, testCase.expected, normalizedAuthStyle)
			}
		})
	}
	// 喵~防御：未知历史值必须原样留给执行层拒绝，不能凭猜测发送错误认证头喵。
	require.Equal(t, VirtualModelAuthStyle("invalid"), VirtualModelAuthStyleFromStorage("invalid"))
}

// TestDeleteVirtualModelByOwnerWithVersion 验证硬删除释放同名资源并清理所有敏感关联配置喵。
func TestDeleteVirtualModelByOwnerWithVersion(t *testing.T) {
	// 使用独立内存数据库隔离硬删除与重建测试喵。
	database, openError := gorm.Open(sqlite.Open("file:virtual-model-delete-test?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, openError)
	originalDatabase := DB
	DB = database
	t.Cleanup(func() {
		DB = originalDatabase
		sqlDatabase, databaseError := database.DB()
		if databaseError == nil {
			_ = sqlDatabase.Close()
		}
	})
	// 创建删除路径依赖的全部关联表喵。
	require.NoError(t, database.AutoMigrate(&VirtualModel{}, &VirtualModelCandidate{}, &VirtualModelInternalCandidate{}, &VirtualModelCustomCandidate{}, &VirtualModelFailureRule{}, &VirtualModelGlobalFailureRule{}, &VirtualModelTokenBinding{}, &VirtualModelManualFreeze{}, &VirtualModelInternalFreezeState{}, &VirtualModelAuditLog{}, &EntityProbeState{}))
	virtualModel := VirtualModel{OwnerUserID: 7, NormalizedName: "reusable-name", DisplayName: "Reusable", Enabled: true, Version: 3, TotalTimeoutSeconds: 120, MaxLoopRounds: 1}
	require.NoError(t, database.Create(&virtualModel).Error)
	customCandidate := VirtualModelCandidate{VirtualModelID: virtualModel.ID, StableOrder: 0, SourceType: VirtualModelSourceCustom, Enabled: true, TimeoutSeconds: 60, Version: 1}
	require.NoError(t, database.Create(&customCandidate).Error)
	require.NoError(t, database.Create(&VirtualModelCustomCandidate{CandidateID: customCandidate.ID, EncryptedBaseURL: "encrypted-url", EncryptedAPIKey: "encrypted-key", CredentialVersion: 1, RealModelName: "custom", AuthStyle: VirtualModelAuthBearer}).Error)
	require.NoError(t, database.Create(&VirtualModelFailureRule{CandidateID: customCandidate.ID, RuleOrder: 0, Action: VirtualModelActionNext}).Error)
	require.NoError(t, database.Create(&VirtualModelManualFreeze{CandidateID: customCandidate.ID, OperatorID: 7, StartedAt: 1, ExpiresAt: 2}).Error)
	require.NoError(t, database.Create(&VirtualModelTokenBinding{VirtualModelID: virtualModel.ID, TokenID: 11, OwnerUserID: 7}).Error)
	// 构造内部候选自动冻结状态，验证模型删除时一并硬删除避免残留孤儿喵。
	require.NoError(t, database.Create(&VirtualModelInternalFreezeState{OwnerUserID: 7, CandidateID: customCandidate.ID, FrozenUntil: 999, ConsecutiveFails: 1, UpdatedTime: 3}).Error)
	// 喵~防御：陈旧版本不得删除模型或它的任何关联数据喵。
	require.EqualError(t, DeleteVirtualModelByOwnerWithVersion(virtualModel.ID, 7, 7, 2), "virtual_model_version_conflict")
	var preservedCandidateCount int64
	require.NoError(t, database.Model(&VirtualModelCandidate{}).Where("id = ?", customCandidate.ID).Count(&preservedCandidateCount).Error)
	require.Equal(t, int64(1), preservedCandidateCount)
	// 使用精确版本删除后，所有关联数据和密文必须消失，而仅保留不可还原审计记录喵。
	require.NoError(t, DeleteVirtualModelByOwnerWithVersion(virtualModel.ID, 7, 7, 3))
	for _, table := range []any{&VirtualModel{}, &VirtualModelCandidate{}, &VirtualModelCustomCandidate{}, &VirtualModelFailureRule{}, &VirtualModelManualFreeze{}, &VirtualModelInternalFreezeState{}, &VirtualModelTokenBinding{}} {
		var count int64
		require.NoError(t, database.Model(table).Count(&count).Error)
		require.Zero(t, count)
	}
	var auditCount int64
	require.NoError(t, database.Model(&VirtualModelAuditLog{}).Where("virtual_model_id = ?", virtualModel.ID).Count(&auditCount).Error)
	require.Equal(t, int64(1), auditCount)
	// 喵~防御：硬删除后同一 owner 必须能够重建同名模型，验证唯一索引未被软删除行占用喵。
	recreatedModel := VirtualModel{OwnerUserID: 7, NormalizedName: "reusable-name", DisplayName: "Recreated", Enabled: true, Version: 1, TotalTimeoutSeconds: 120, MaxLoopRounds: 1}
	require.NoError(t, database.Create(&recreatedModel).Error)
}

// TestGetActiveVirtualModelManualFreezes 验证手动冻结到期时间戳映射：只返回仍在冻结期内的候选喵。
func TestGetActiveVirtualModelManualFreezes(t *testing.T) {
	// 使用独立内存数据库隔离手动冻结查询测试喵。
	database, err := gorm.Open(sqlite.Open("file:virtual-model-manual-freeze-test?mode=memory&cache=shared"), &gorm.Config{})
	// 喵~防御：数据库初始化失败时终止测试，避免无效断言掩盖错误喵。
	require.NoError(t, err)
	// 保存并替换全局数据库连接，以覆盖实际查询函数喵。
	originalDatabase := DB
	DB = database
	// 恢复数据库指针并释放临时连接，避免测试间资源泄漏喵。
	t.Cleanup(func() {
		DB = originalDatabase
		sqlDatabase, closeError := database.DB()
		if closeError == nil {
			_ = sqlDatabase.Close()
		}
	})
	require.NoError(t, database.AutoMigrate(&VirtualModelManualFreeze{}))

	// 候选 1 冻结中（到期 1000 秒）、候选 2 已过期（到期 500 秒）、候选 3 未开始（开始 2000 秒）喵。
	require.NoError(t, database.Create(&VirtualModelManualFreeze{CandidateID: 1, OperatorID: 7, StartedAt: 100, ExpiresAt: 1000}).Error)
	require.NoError(t, database.Create(&VirtualModelManualFreeze{CandidateID: 2, OperatorID: 7, StartedAt: 100, ExpiresAt: 500}).Error)
	require.NoError(t, database.Create(&VirtualModelManualFreeze{CandidateID: 3, OperatorID: 7, StartedAt: 2000, ExpiresAt: 3000}).Error)

	// 当前时间 900 秒：只应返回冻结中的候选 1 及其到期时间戳喵。
	frozenUntil, queryError := GetActiveVirtualModelManualFreezes([]int{1, 2, 3}, 900)
	require.NoError(t, queryError)
	require.Equal(t, map[int]int64{1: 1000}, frozenUntil)

	// 空候选集合与非法时间戳返回空映射，不执行查询喵。
	empty, queryError := GetActiveVirtualModelManualFreezes(nil, 900)
	require.NoError(t, queryError)
	require.Empty(t, empty)
	invalid, queryError := GetActiveVirtualModelManualFreezes([]int{1}, 0)
	require.NoError(t, queryError)
	require.Empty(t, invalid)
}
