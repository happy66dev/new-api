package middleware

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveUserUpstreamModelTimeoutSeconds 验证上游模型墙钟超时的规整规则喵。
// 未配置或超出硬顶一律回退 600s；显式配置的 (0,600] 内更短超时原样保留喵。
func TestResolveUserUpstreamModelTimeoutSeconds(t *testing.T) {
	tests := []struct {
		name     string
		configured int
		want     int
	}{
		{name: "unset falls back to hard cap", configured: 0, want: 600},
		{name: "at hard cap stays", configured: 600, want: 600},
		{name: "above hard cap falls back", configured: 601, want: 600},
		{name: "short explicit timeout kept", configured: 90, want: 90},
		{name: "one second kept", configured: 1, want: 1},
		{name: "negative falls back to hard cap", configured: -5, want: 600},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, resolveUserUpstreamModelTimeoutSeconds(tc.configured))
		})
	}
}

// buildUpstreamModelTimeoutContext 构造带 gin 请求的上下文供硬截止注入测试使用喵。
func buildUpstreamModelTimeoutContext() *gin.Context {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	return ctx
}

// TestApplyUserUpstreamModelDeadlineDefaultsToHardCap 验证未配置超时时注入约 600s 硬截止（而不是旧的 60s）喵。
func TestApplyUserUpstreamModelDeadlineDefaultsToHardCap(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := buildUpstreamModelTimeoutContext()
	cancelDeadline := applyUserUpstreamModelDeadline(ctx, 0)
	require.NotNil(t, cancelDeadline, "hard deadline cancel function must be returned")
	defer cancelDeadline()
	deadline, hasDeadline := ctx.Request.Context().Deadline()
	require.True(t, hasDeadline, "request context must carry a hard deadline")
	remainingSeconds := time.Until(deadline).Seconds()
	assert.GreaterOrEqual(t, remainingSeconds, float64(590), "default timeout should be near the 600s hard cap, not 60s")
	assert.LessOrEqual(t, remainingSeconds, float64(610))
}

// TestApplyUserUpstreamModelDeadlineKeepsShorterConfig 验证显式配置的更短超时被完整保留喵。
func TestApplyUserUpstreamModelDeadlineKeepsShorterConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := buildUpstreamModelTimeoutContext()
	cancelDeadline := applyUserUpstreamModelDeadline(ctx, 2)
	require.NotNil(t, cancelDeadline)
	defer cancelDeadline()
	deadline, hasDeadline := ctx.Request.Context().Deadline()
	require.True(t, hasDeadline)
	remainingSeconds := time.Until(deadline).Seconds()
	assert.GreaterOrEqual(t, remainingSeconds, 1.8, "explicit shorter timeout should be honored")
	assert.LessOrEqual(t, remainingSeconds, 2.2)
}

// TestApplyUserUpstreamModelDeadlineClampedByVirtualRequestDeadline 验证虚拟模型上下文中候选截止不越过模型总 deadline 剩余预算喵。
func TestApplyUserUpstreamModelDeadlineClampedByVirtualRequestDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := buildUpstreamModelTimeoutContext()
	// 虚拟模型总 deadline 设为 3 秒后到期，候选即使未配置超时也只能用剩余 3 秒喵。
	executionState := &virtualModelExecutionState{
		requestDeadline:    time.Now().Add(3 * time.Second),
		executionSnapshot:  &model.VirtualModelExecutionSnapshot{Candidates: []model.VirtualModelCandidateSnapshot{}},
	}
	common.SetContextKey(ctx, constant.ContextKeyVirtualModelExecutionState, executionState)
	cancelDeadline := applyUserUpstreamModelDeadline(ctx, 0)
	require.NotNil(t, cancelDeadline)
	defer cancelDeadline()
	deadline, hasDeadline := ctx.Request.Context().Deadline()
	require.True(t, hasDeadline)
	remainingSeconds := time.Until(deadline).Seconds()
	assert.GreaterOrEqual(t, remainingSeconds, float64(0))
	assert.LessOrEqual(t, remainingSeconds, float64(3.1), "candidate deadline must not exceed virtual model total deadline remaining")
}

// TestApplyUserUpstreamModelDeadlineCancelClosesContext 验证调用返回的取消函数后请求上下文立即被取消喵。
func TestApplyUserUpstreamModelDeadlineCancelClosesContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := buildUpstreamModelTimeoutContext()
	cancelDeadline := applyUserUpstreamModelDeadline(ctx, 30)
	require.NotNil(t, cancelDeadline)
	cancelDeadline()
	select {
	case <-ctx.Request.Context().Done():
		// 取消函数生效：上下文已结束喵。
	default:
		t.Fatal("cancel function should close the request context")
	}
}
