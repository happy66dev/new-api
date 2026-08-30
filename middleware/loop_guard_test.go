package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildLoopGuardTestRouter 构造挂载 LoopGuard 的测试路由喵。
func buildLoopGuardTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(LoopGuard())
	router.GET("/v1/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return router
}

// TestLoopGuardRejectsSelfLoopGuardHeader 验证本实例发出的标记值会被拦截喵。
func TestLoopGuardRejectsSelfLoopGuardHeader(t *testing.T) {
	router := buildLoopGuardTestRouter()
	// 用本实例 ID 构造标记，模拟请求转发出去又回到本实例喵。
	guardValue := common.BuildLoopGuardValue(common.NewRequestId())
	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.Header.Set(common.LoopGuardHeaderKey, guardValue)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
}

// TestLoopGuardAllowsForeignLoopGuardHeader 验证其他实例的标记值会放行，不误伤多级代理喵。
func TestLoopGuardAllowsForeignLoopGuardHeader(t *testing.T) {
	router := buildLoopGuardTestRouter()
	// 伪造一个与本实例不同的随机实例 ID 标记喵。
	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.Header.Set(common.LoopGuardHeaderKey, "deadbeefdeadbeefdeadbeefdeadbeef:foreign-request")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

// TestLoopGuardAllowsNoHeader 验证普通请求（无标记头）直接放行喵。
func TestLoopGuardAllowsNoHeader(t *testing.T) {
	router := buildLoopGuardTestRouter()
	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

// TestLoopGuardAllowsMalformedHeader 验证格式非法的标记头无法归属到任何实例，保守放行喵。
func TestLoopGuardAllowsMalformedHeader(t *testing.T) {
	router := buildLoopGuardTestRouter()
	// 分隔符在首尾或缺失等畸形格式，解析不出实例 ID，不拦截喵。
	for _, malformedValue := range []string{":request-id", "instance-id:", "no-separator"} {
		req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
		req.Header.Set(common.LoopGuardHeaderKey, malformedValue)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "malformed guard value should pass through: %s", malformedValue)
	}
}
