package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestApplyInternalVirtualModelCandidateClearsStaleChannelContext 验证激活新内部候选时会清空上一候选遗留的渠道上下文喵。
// 这是 A→B 候选切换不命中 A 渠道的关键前提：渠道上下文为空后 relay 的 getChannel 才会按 B 分组重选喵。
func TestApplyInternalVirtualModelCandidateClearsStaleChannelContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := `{"model":"vm","messages":[{"role":"user","content":"hi"}]}`
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	// 注入调用者可用分组：候选分组 default 在可用范围内喵。
	common.SetContextKey(ctx, constant.ContextKeyUserGroupAccess, service.UserGroupAccess{UsableGroups: map[string]string{"default": "默认分组"}, AutoGroups: []string{}})
	// 预置上一个候选（候选 A）遗留的渠道上下文，模拟 A 曾命中渠道 77 喵。
	common.SetContextKey(ctx, constant.ContextKeyChannelId, 77)
	common.SetContextKey(ctx, constant.ContextKeyChannelName, "stale-channel-a")
	common.SetContextKey(ctx, constant.ContextKeyChannelType, 1)
	common.SetContextKey(ctx, constant.ContextKeyChannelKey, "sk-stale")
	common.SetContextKey(ctx, constant.ContextKeyChannelBaseUrl, "https://stale.example")

	// 切换到候选 B：default 分组的 gpt-4o 喵。
	candidate := &model.VirtualModelCandidateSnapshot{CandidateID: 5, VirtualModelID: 1, StableOrder: 1, SourceType: model.VirtualModelSourceInternal, Enabled: true, GroupName: "default", RealModelName: "gpt-4o"}
	activated := applyInternalVirtualModelCandidate(ctx, &ModelRequest{Model: "virtual/vm"}, "virtual/vm", candidate)
	require.True(t, activated)
	// 上一候选遗留的渠道上下文必须被清空，使 relay 层按 B 分组重新选渠道喵。
	require.Equal(t, 0, common.GetContextKeyInt(ctx, constant.ContextKeyChannelId))
	require.Empty(t, common.GetContextKeyString(ctx, constant.ContextKeyChannelName))
	require.Equal(t, 0, common.GetContextKeyInt(ctx, constant.ContextKeyChannelType))
	require.Empty(t, common.GetContextKeyString(ctx, constant.ContextKeyChannelKey))
	require.Empty(t, common.GetContextKeyString(ctx, constant.ContextKeyChannelBaseUrl))
	// 分组与模型上下文已切换到新候选 B 喵。
	require.Equal(t, "default", common.GetContextKeyString(ctx, constant.ContextKeyUsingGroup))
	require.Equal(t, "gpt-4o", ctx.GetString("original_model"))
}
