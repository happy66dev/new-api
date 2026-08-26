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
	require.NoError(t, testDB.AutoMigrate(&model.VirtualModel{}, &model.VirtualModelCandidate{}, &model.VirtualModelInternalCandidate{}, &model.VirtualModelFailureRule{}, &model.VirtualModelTokenBinding{}))

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
