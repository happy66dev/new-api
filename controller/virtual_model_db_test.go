package controller

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestBuildVirtualModelResponseFailureRulesTableName 回归验证候选失败规则读取使用真实模型表名喵。
// 此前 candidateResponse.FailureRules 误用 DTO 类型，GORM 会按结构体名生成
// virtual_model_failure_rule_inputs 表名，而真实表是 virtual_model_failure_rules，触发 no such table 喵。
func TestBuildVirtualModelResponseFailureRulesTableName(t *testing.T) {
	// 使用内存 SQLite 快速构造与 AutoMigrate 一致的虚拟模型相关表喵。
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, testDB.AutoMigrate(&model.VirtualModel{}, &model.VirtualModelCandidate{}, &model.VirtualModelInternalCandidate{}, &model.VirtualModelFailureRule{}, &model.VirtualModelGlobalFailureRule{}, &model.VirtualModelTokenBinding{}))

	// 喵~防御：保存并恢复全局数据库连接，避免污染其他测试用例。
	originalDB := model.DB
	model.DB = testDB
	t.Cleanup(func() { model.DB = originalDB })

	now := time.Now().Unix()
	virtualModel := &model.VirtualModel{OwnerUserID: 1, NormalizedName: "regression", DisplayName: "Regression", Enabled: true, LoopEnabled: false, TotalTimeoutSeconds: 120, MaxLoopRounds: 1, Version: 1, CreatedTime: now, UpdatedTime: now}
	require.NoError(t, testDB.Create(virtualModel).Error)
	candidate := &model.VirtualModelCandidate{VirtualModelID: virtualModel.ID, StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true, MaxRetries: 0, TimeoutSeconds: 60, Version: 1, CreatedTime: now, UpdatedTime: now}
	require.NoError(t, testDB.Create(candidate).Error)
	require.NoError(t, testDB.Create(&model.VirtualModelInternalCandidate{CandidateID: candidate.ID, GroupName: "default", RealModelName: "gpt-4o-mini"}).Error)
	require.NoError(t, testDB.Create(&model.VirtualModelFailureRule{CandidateID: candidate.ID, RuleOrder: 0, HTTPStatus: 429, Action: model.VirtualModelActionNext}).Error)

	// 构建响应应能正常读取失败规则，不再触发 no such table: virtual_model_failure_rule_inputs 喵。
	response, buildError := buildVirtualModelResponse(virtualModel)
	require.NoError(t, buildError)
	require.Len(t, response.Candidates, 1)
	require.Len(t, response.Candidates[0].FailureRules, 1)
	require.Equal(t, 429, response.Candidates[0].FailureRules[0].HTTPStatus)
}

// TestBuildVirtualModelResponseGlobalFailureRules 回归验证模型级全局兜底规则读取与响应回填喵。
// 全局规则独立建表，读取必须使用真实模型表名 virtual_model_global_failure_rules 喵。
func TestBuildVirtualModelResponseGlobalFailureRules(t *testing.T) {
	// 使用内存 SQLite 快速构造与 AutoMigrate 一致的虚拟模型相关表喵。
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, testDB.AutoMigrate(&model.VirtualModel{}, &model.VirtualModelCandidate{}, &model.VirtualModelInternalCandidate{}, &model.VirtualModelFailureRule{}, &model.VirtualModelGlobalFailureRule{}, &model.VirtualModelTokenBinding{}))

	// 喵~防御：保存并恢复全局数据库连接，避免污染其他测试用例。
	originalDB := model.DB
	model.DB = testDB
	t.Cleanup(func() { model.DB = originalDB })

	now := time.Now().Unix()
	virtualModel := &model.VirtualModel{OwnerUserID: 1, NormalizedName: "global-rules", DisplayName: "Global Rules", Enabled: true, LoopEnabled: false, TotalTimeoutSeconds: 120, MaxLoopRounds: 1, Version: 1, CreatedTime: now, UpdatedTime: now}
	require.NoError(t, testDB.Create(virtualModel).Error)
	require.NoError(t, testDB.Create(&model.VirtualModelGlobalFailureRule{VirtualModelID: virtualModel.ID, RuleOrder: 0, HTTPStatus: 500, Action: model.VirtualModelActionPassthrough}).Error)

	// 构建响应必须携带模型级全局兜底规则，供前端编辑喵。
	response, buildError := buildVirtualModelResponse(virtualModel)
	require.NoError(t, buildError)
	require.Len(t, response.GlobalFailureRules, 1)
	require.Equal(t, 500, response.GlobalFailureRules[0].HTTPStatus)
	require.Equal(t, model.VirtualModelActionPassthrough, response.GlobalFailureRules[0].Action)
}

// TestSaveCustomCandidateCarriesUpstreamModelID 验证自定义候选保存透传用户上游模型引用喵。
// 引用条目后凭据直填可选，保存不要求地址与密钥喵。
func TestSaveCustomCandidateCarriesUpstreamModelID(t *testing.T) {
	// 使用内存 SQLite 快速构造候选相关表喵。
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, testDB.AutoMigrate(&model.VirtualModel{}, &model.VirtualModelCandidate{}, &model.VirtualModelInternalCandidate{}, &model.VirtualModelCustomCandidate{}, &model.VirtualModelFailureRule{}))

	// 喵~防御：保存并恢复全局数据库连接，避免污染其他测试用例。
	originalDB := model.DB
	model.DB = testDB
	t.Cleanup(func() { model.DB = originalDB })

	now := time.Now().Unix()
	candidate := &model.VirtualModelCandidate{VirtualModelID: 1, StableOrder: 0, SourceType: model.VirtualModelSourceCustom, Enabled: true, Version: 1, CreatedTime: now, UpdatedTime: now}
	require.NoError(t, testDB.Create(candidate).Error)

	// 带引用的候选保存成功，无需直填凭据喵。
	upstreamModelID := int64(42)
	saveError := saveVirtualModelCandidateSourceConfig(testDB, candidate, virtualModelCandidateInput{
		SourceType:      model.VirtualModelSourceCustom,
		UpstreamModelID: &upstreamModelID,
	}, true)
	require.NoError(t, saveError)
	var saved model.VirtualModelCustomCandidate
	require.NoError(t, testDB.Where("candidate_id = ?", candidate.ID).First(&saved).Error)
	require.NotNil(t, saved.UpstreamModelID)
	require.Equal(t, int64(42), *saved.UpstreamModelID)

	// 无引用的直填候选仍要求真实模型与凭据：空配置被校验拦截喵。
	saveError = saveVirtualModelCandidateSourceConfig(testDB, candidate, virtualModelCandidateInput{
		SourceType: model.VirtualModelSourceCustom,
	}, false)
	require.Error(t, saveError)
}

// TestBuildVirtualModelResponseFrozenUntil 验证手动冻结状态回填到候选响应，供调用链页面展示已冻结徽章喵。
func TestBuildVirtualModelResponseFrozenUntil(t *testing.T) {
	// 使用内存 SQLite 快速构造虚拟模型、候选与手动冻结表喵。
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, testDB.AutoMigrate(&model.VirtualModel{}, &model.VirtualModelCandidate{}, &model.VirtualModelInternalCandidate{}, &model.VirtualModelFailureRule{}, &model.VirtualModelGlobalFailureRule{}, &model.VirtualModelTokenBinding{}, &model.VirtualModelManualFreeze{}))

	// 喵~防御：保存并恢复全局数据库连接，避免污染其他测试用例。
	originalDB := model.DB
	model.DB = testDB
	t.Cleanup(func() { model.DB = originalDB })

	now := time.Now().Unix()
	virtualModel := &model.VirtualModel{OwnerUserID: 1, NormalizedName: "frozen", DisplayName: "Frozen", Enabled: true, LoopEnabled: false, TotalTimeoutSeconds: 120, MaxLoopRounds: 1, Version: 1, CreatedTime: now, UpdatedTime: now}
	require.NoError(t, testDB.Create(virtualModel).Error)
	candidate := &model.VirtualModelCandidate{VirtualModelID: virtualModel.ID, StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true, MaxRetries: 0, TimeoutSeconds: 60, Version: 1, CreatedTime: now, UpdatedTime: now}
	require.NoError(t, testDB.Create(candidate).Error)
	require.NoError(t, testDB.Create(&model.VirtualModelInternalCandidate{CandidateID: candidate.ID, GroupName: "default", RealModelName: "gpt-4o-mini"}).Error)

	// 手动冻结：从现在起 600 秒后到期，供回填断言喵。
	expiresAt := now + 600
	require.NoError(t, testDB.Create(&model.VirtualModelManualFreeze{CandidateID: candidate.ID, OperatorID: 1, StartedAt: now, ExpiresAt: expiresAt}).Error)

	// 构建响应必须携带当前仍生效的冻结到期时间喵。
	response, buildError := buildVirtualModelResponse(virtualModel)
	require.NoError(t, buildError)
	require.Len(t, response.Candidates, 1)
	require.Equal(t, expiresAt, response.Candidates[0].FrozenUntil)

	// 冻结到期后响应不再携带冻结状态喵。
	require.NoError(t, testDB.Model(&model.VirtualModelManualFreeze{}).Where("candidate_id = ?", candidate.ID).Update("expires_at", now-10).Error)
	response, buildError = buildVirtualModelResponse(virtualModel)
	require.NoError(t, buildError)
	require.Equal(t, int64(0), response.Candidates[0].FrozenUntil)
}
