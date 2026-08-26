package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newSessionVirtualModelTestDB 构造带虚拟模型完整表结构的临时数据库喵。
func newSessionVirtualModelTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, testDB.AutoMigrate(
		&model.VirtualModel{},
		&model.VirtualModelCandidate{},
		&model.VirtualModelInternalCandidate{},
		&model.VirtualModelCustomCandidate{},
		&model.VirtualModelFailureRule{},
		&model.VirtualModelGlobalFailureRule{},
		&model.VirtualModelTokenBinding{},
		&model.VirtualModelManualFreeze{},
		&model.VirtualModelCustomFreezeState{},
		&model.VirtualModelInternalFreezeState{},
	))
	return testDB
}

// TestHandleVirtualModelRequestSessionAuth 验证会话态请求（无 token_id）可调用自己的启用虚拟模型喵。
func TestHandleVirtualModelRequestSessionAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 中间件测试库没有 TestMain，为虚拟模型授权断言临时准备独立数据库喵。
	testDB := newSessionVirtualModelTestDB(t)
	oldDB := model.DB
	model.DB = testDB
	defer func() { model.DB = oldDB }()
	// 虚拟模型功能开关默认关闭，测试期间显式开启；OptionMap 可能尚未初始化需先建表喵。
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	oldOption := common.OptionMap["VirtualModelEnabled"]
	common.OptionMap["VirtualModelEnabled"] = "true"
	defer func() { common.OptionMap["VirtualModelEnabled"] = oldOption }()

	// 构造启用虚拟模型及一个内部候选喵。
	virtualModel := &model.VirtualModel{OwnerUserID: 7, NormalizedName: "session-test", Enabled: true}
	require.NoError(t, testDB.Create(virtualModel).Error)
	candidate := &model.VirtualModelCandidate{VirtualModelID: virtualModel.ID, StableOrder: 0, SourceType: model.VirtualModelSourceInternal, Enabled: true}
	require.NoError(t, testDB.Create(candidate).Error)
	require.NoError(t, testDB.Create(&model.VirtualModelInternalCandidate{CandidateID: candidate.ID, GroupName: "default", RealModelName: "gpt-4o"}).Error)

	// 构造带会话身份 id 的 JSON 聊天请求，不设置 token_id 模拟游乐场 /pg 路径喵。
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/pg/chat/completions", strings.NewReader(`{"model":"virtual/session-test","messages":[{"role":"user","content":"hi"}]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", 7)

	// 会话态请求必须能激活自己的虚拟模型候选喵。
	activated := handleVirtualModelRequest(ctx, &ModelRequest{Model: "virtual/session-test"})
	require.True(t, activated)
	// 候选改写后请求体的顶层 model 必须是内部候选的真实模型喵。
	require.Equal(t, "gpt-4o", ctx.GetString("original_model"))
}
