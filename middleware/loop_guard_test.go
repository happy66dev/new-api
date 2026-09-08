package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
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

// TestLoopGuardSelfMarksAndStripsThenPasses 验证本实例标记不再在入口一刀切拒绝：
// 剥除标记头、写入上下文标记并放行，交给分发层按命名空间裁定是否构成真实递归喵。
func TestLoopGuardSelfMarksAndStripsThenPasses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(LoopGuard())
	router.GET("/v1/test", func(c *gin.Context) {
		// 标记头应在到达处理函数前被剥除，避免合法单跳请求把标记继续外泄喵。
		require.Empty(t, c.Request.Header.Get(common.LoopGuardHeaderKey), "self guard header should be stripped before downstream")
		// 本实例回环标记应写入上下文，供分发层判断是否自递归喵。
		require.True(t, isLoopGuardFromSelfRequest(c), "self loop guard flag should be set on context")
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	// 用本实例 ID 构造标记，模拟本实例转发出去又回到本实例的单跳请求喵。
	guardValue := common.BuildLoopGuardValue(common.NewRequestId())
	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.Header.Set(common.LoopGuardHeaderKey, guardValue)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
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

// TestShouldRejectSelfRecursionLoop 验证防风暴只拦真正的自递归喵。
// 携带本实例回环标记时：再入 user/ 或 virtual/ 需拦截；再入普通模型（单跳终止）放行；无标记一律放行喵。
func TestShouldRejectSelfRecursionLoop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 构造携带本实例回环标记的上下文喵。
	markedRecorder := httptest.NewRecorder()
	markedCtx, _ := gin.CreateTestContext(markedRecorder)
	common.SetContextKey(markedCtx, constant.ContextKeyLoopGuardFromSelf, true)
	// 构造无标记的普通上下文喵。
	plainRecorder := httptest.NewRecorder()
	plainCtx, _ := gin.CreateTestContext(plainRecorder)

	tests := []struct {
		name      string
		ctx       *gin.Context
		modelName string
		want      bool
	}{
		{name: "self marker + virtual model", ctx: markedCtx, modelName: "virtual/demo", want: true},
		{name: "self marker + user upstream model", ctx: markedCtx, modelName: "user/demo", want: true},
		{name: "self marker + normal model is single hop", ctx: markedCtx, modelName: "gpt-4o", want: false},
		{name: "no marker + virtual model", ctx: plainCtx, modelName: "virtual/demo", want: false},
		{name: "no marker + user upstream model", ctx: plainCtx, modelName: "user/demo", want: false},
		{name: "no marker + normal model", ctx: plainCtx, modelName: "gpt-4o", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, shouldRejectSelfRecursionLoop(tc.ctx, tc.modelName))
		})
	}
}
