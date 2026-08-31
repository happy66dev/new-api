package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestVirtualModelRequestContentPreview 验证请求内容预览的提取与截断喵。
func TestVirtualModelRequestContentPreview(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 正常请求体：返回原始 JSON 预览，含模型名与消息内容喵。
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"deepseek","messages":[{"role":"user","content":"你好"}]}`))
	got := VirtualModelRequestContentPreview(ctx)
	require.Contains(t, got, `"model":"deepseek"`)
	require.Contains(t, got, "你好")

	// 空上下文返回空串，不崩溃喵。
	require.Equal(t, "", VirtualModelRequestContentPreview(nil))

	// 超长请求体被截断到上限，不越界喵。
	longBody := `{"messages":[{"role":"user","content":"` + strings.Repeat("猫", maxVirtualModelRequestContentPreview+100) + `"}]}`
	longCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	longCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(longBody))
	gotLong := VirtualModelRequestContentPreview(longCtx)
	require.Len(t, gotLong, maxVirtualModelRequestContentPreview)

	// 空请求体返回空串喵。
	emptyCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	emptyCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	require.Equal(t, "", VirtualModelRequestContentPreview(emptyCtx))
}
