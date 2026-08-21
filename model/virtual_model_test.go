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

// TestGetFirstEnabledVirtualModelCandidate 验证候选顺序和自定义候选阻断语义喵。
func TestGetFirstEnabledVirtualModelCandidate(t *testing.T) {
	// 使用独立内存数据库隔离候选顺序测试喵。
	database, err := gorm.Open(sqlite.Open("file:virtual-model-candidate-test?mode=memory&cache=shared"), &gorm.Config{})
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
	// 创建候选顺序查询所需的表结构，包括自定义候选关联表喵。
	require.NoError(t, database.AutoMigrate(&VirtualModelCandidate{}, &VirtualModelInternalCandidate{}, &VirtualModelCustomCandidate{}))
	// 创建顺序靠前的自定义候选和其加密配置，验证快照不会跳过它选择后续内部候选喵。
	customCandidate := VirtualModelCandidate{VirtualModelID: 91, StableOrder: 0, SourceType: VirtualModelSourceCustom, Enabled: true, MaxRetries: 2, TimeoutSeconds: 45}
	require.NoError(t, database.Create(&customCandidate).Error)
	require.NoError(t, database.Create(&VirtualModelCustomCandidate{CandidateID: customCandidate.ID, EncryptedBaseURL: "encrypted-url", EncryptedAPIKey: "encrypted-key", CredentialVersion: 1, RealModelName: "custom-model", AuthStyle: VirtualModelAuthBearer}).Error)
	// 创建顺序靠后的内部候选及其目标分组和真实模型喵。
	internalCandidate := VirtualModelCandidate{VirtualModelID: 91, StableOrder: 1, SourceType: VirtualModelSourceInternal, Enabled: true}
	require.NoError(t, database.Create(&internalCandidate).Error)
	require.NoError(t, database.Create(&VirtualModelInternalCandidate{CandidateID: internalCandidate.ID, GroupName: "default", RealModelName: "gpt-test"}).Error)
	// 查询必须返回排序第一的自定义候选，保留其完整加密快照供安全执行器使用喵。
	candidateSnapshot, queryError := GetFirstEnabledVirtualModelCandidate(91)
	require.NoError(t, queryError)
	require.Equal(t, VirtualModelSourceCustom, candidateSnapshot.SourceType)
	require.Empty(t, candidateSnapshot.GroupName)
	require.Equal(t, "custom-model", candidateSnapshot.RealModelName)
	require.Equal(t, "encrypted-url", candidateSnapshot.EncryptedBaseURL)
	require.Equal(t, "encrypted-key", candidateSnapshot.EncryptedAPIKey)
	require.Equal(t, 1, candidateSnapshot.CredentialVersion)
	require.Equal(t, VirtualModelAuthBearer, candidateSnapshot.AuthStyle)
	require.Equal(t, 2, candidateSnapshot.MaxRetries)
	require.Equal(t, 45, candidateSnapshot.TimeoutSeconds)
	// 禁用自定义候选后，查询应稳定返回下一个启用内部候选喵。
	require.NoError(t, database.Model(&VirtualModelCandidate{}).Where("id = ?", customCandidate.ID).Update("enabled", false).Error)
	candidateSnapshot, queryError = GetFirstEnabledVirtualModelCandidate(91)
	require.NoError(t, queryError)
	require.Equal(t, VirtualModelSourceInternal, candidateSnapshot.SourceType)
	require.Equal(t, "default", candidateSnapshot.GroupName)
	require.Equal(t, "gpt-test", candidateSnapshot.RealModelName)
	// 读取全部启用候选时必须按 stable_order 稳定排序且保留运行参数喵。
	candidateSnapshots, snapshotsError := GetEnabledVirtualModelCandidateSnapshots(91)
	require.NoError(t, snapshotsError)
	require.Len(t, candidateSnapshots, 1)
	require.Equal(t, internalCandidate.ID, candidateSnapshots[0].CandidateID)
}
