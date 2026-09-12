package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestModelPricingAndVendorOperationRoutesAreRegistered 守住「模型定价配置」与「厂商批量操作」两组路由喵。
//
// 为什么需要这个测试喵：
//
//	这两组接口由上游提交引入并在某次 merge 中丢失了路由注册，但前端
//	web/src/features/model-pricing/api.ts 与 web/src/features/models/vendor-api.ts
//	仍在调用它们。路由一旦缺失，请求会落到中继兜底并返回 Invalid URL（而不是鉴权失败），
//	用户看到的就只是模型定价页报错。这里断言「未认证请求返回 401」，
//	因为 401 只可能来自鉴权中间件，能证明路由确实注册在鉴权链路之后喵。
func TestModelPricingAndVendorOperationRoutesAreRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	cases := []struct {
		name   string
		method string
		path   string
	}{
		// 模型定价配置：读取与批量更新（RootAuth 之后）喵。
		{"model pricing read", http.MethodGet, "/api/option/model_pricing"},
		{"model pricing update", http.MethodPatch, "/api/option/model_pricing"},
		// 厂商批量操作：预览与执行（AdminAuth 之后）喵。
		{"vendor operation preview", http.MethodPost, "/api/vendors/operations/preview"},
		{"vendor operation apply", http.MethodPost, "/api/vendors/operations"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// 喵~防御：不带任何凭据发起请求，未被注册的路由不会走到鉴权中间件喵。
			request := httptest.NewRequest(testCase.method, testCase.path, nil)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)

			require.Equal(t, http.StatusUnauthorized, response.Code)
		})
	}
}
