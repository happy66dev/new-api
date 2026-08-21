package virtualmodel

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// TestRewrittenCustomRequestBody 验证仅替换 JSON 顶层模型并拒绝不安全请求体喵。
func TestRewrittenCustomRequestBody(t *testing.T) {
	// 关闭 Gin 调试输出，避免测试日志混入无关路由信息喵。
	gin.SetMode(gin.TestMode)
	// 定义正常 JSON、非法 JSON、非 JSON、缺失模型和类型错误边界喵。
	testCases := []struct {
		name        string
		contentType string
		requestBody string
		expectError bool
	}{
		{name: "replace top-level model", contentType: "application/json", requestBody: `{"model":"virtual/demo","nested":{"model":"preserve"}}`},
		{name: "invalid JSON", contentType: "application/json", requestBody: `{`, expectError: true},
		{name: "non JSON", contentType: "text/plain", requestBody: `model=virtual/demo`, expectError: true},
		{name: "missing model", contentType: "application/json", requestBody: `{}`, expectError: true},
		{name: "model wrong type", contentType: "application/json", requestBody: `{"model":3}`, expectError: true},
	}
	// 逐项创建请求上下文并验证可安全重写的请求体结果喵。
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "https://gateway.example.test/v1/chat/completions", bytes.NewBufferString(testCase.requestBody))
			request.Header.Set("Content-Type", testCase.contentType)
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = request
			rewrittenBody, rewriteError := rewrittenCustomRequestBody(context, "gpt-target")
			if (rewriteError != nil) != testCase.expectError {
				t.Fatalf("rewrittenCustomRequestBody() error=%v wantError=%v", rewriteError, testCase.expectError)
			}
			if !testCase.expectError {
				expectedBody := `{"model":"gpt-target","nested":{"model":"preserve"}}`
				if string(rewrittenBody) != expectedBody {
					t.Fatalf("rewritten body = %s, want %s", rewrittenBody, expectedBody)
				}
			}
		})
	}
}

// TestBuildCustomUpstreamURL 验证 Base URL 前缀、escaped path 和 query 的保留喵。
func TestBuildCustomUpstreamURL(t *testing.T) {
	// 解析已通过控制面校验的 HTTPS Base URL喵。
	baseURL, validationError := ValidateCustomBaseURL("https://api.example.test/prefix")
	if validationError != nil {
		t.Fatalf("validate base URL: %v", validationError)
	}
	// 创建带转义路径和查询参数的客户端请求 URL喵。
	request := httptest.NewRequest(http.MethodPost, "https://gateway.example.test/v1/chat%2Fcompletions?stream=true&x=1", nil)
	upstreamURL, buildError := buildCustomUpstreamURL(baseURL, request.URL)
	if buildError != nil {
		t.Fatalf("build custom upstream URL: %v", buildError)
	}
	// 最终 URL 必须保留 Base path、原始转义 path 与原始 query喵。
	if upstreamURL.String() != "https://api.example.test/prefix/v1/chat%2Fcompletions?stream=true&x=1" {
		t.Fatalf("upstream URL = %s", upstreamURL.String())
	}
	// 空 URL 参数必须拒绝，避免退化到错误的根路径请求喵。
	if _, nilBuildError := buildCustomUpstreamURL(nil, request.URL); nilBuildError == nil {
		t.Fatal("nil base URL should be rejected")
	}
}

// TestCustomHeaderFilteringAndAuthentication 验证客户端认证和 hop-by-hop 头不会穿透到上游喵。
func TestCustomHeaderFilteringAndAuthentication(t *testing.T) {
	// 构造包含危险和允许请求头的客户端请求头集合喵。
	sourceHeaders := make(http.Header)
	sourceHeaders.Set("Authorization", "Bearer client-secret")
	sourceHeaders.Set("x-api-key", "client-key")
	sourceHeaders.Set("Connection", "upgrade")
	sourceHeaders.Set("Content-Length", "999")
	sourceHeaders.Set("Cookie", "session=client-secret")
	sourceHeaders.Set("Forwarded", "for=127.0.0.1")
	sourceHeaders.Set("X-Forwarded-For", "127.0.0.1")
	sourceHeaders.Set("X-Trace-Id", "trace-1")
	targetHeaders := make(http.Header)
	copyCustomUpstreamHeaders(targetHeaders, sourceHeaders)
	// 危险头必须被剔除，允许头需原样保留喵。
	if targetHeaders.Get("Authorization") != "" || targetHeaders.Get("x-api-key") != "" || targetHeaders.Get("Connection") != "" || targetHeaders.Get("Content-Length") != "" || targetHeaders.Get("Cookie") != "" || targetHeaders.Get("Forwarded") != "" || targetHeaders.Get("X-Forwarded-For") != "" || targetHeaders.Get("X-Trace-Id") != "trace-1" {
		t.Fatalf("unexpected filtered headers: %#v", targetHeaders)
	}
	// Bearer 认证必须成为唯一 Authorization 值喵。
	if authenticationError := applyCustomCandidateAuth(targetHeaders, model.VirtualModelAuthBearer, "upstream-secret"); authenticationError != nil {
		t.Fatalf("apply bearer auth: %v", authenticationError)
	}
	if targetHeaders.Get("Authorization") != "Bearer upstream-secret" || targetHeaders.Get("x-api-key") != "" {
		t.Fatalf("unexpected bearer headers: %#v", targetHeaders)
	}
	// x-api-key 认证必须移除旧 Bearer 头并注入唯一上游 key喵。
	if authenticationError := applyCustomCandidateAuth(targetHeaders, model.VirtualModelAuthAPIKey, "key-secret"); authenticationError != nil {
		t.Fatalf("apply x-api-key auth: %v", authenticationError)
	}
	if targetHeaders.Get("Authorization") != "" || targetHeaders.Get("x-api-key") != "key-secret" {
		t.Fatalf("unexpected x-api-key headers: %#v", targetHeaders)
	}
	// 非法认证风格必须拒绝，避免未认证外发请求喵。
	if authenticationError := applyCustomCandidateAuth(targetHeaders, "invalid", "secret"); authenticationError == nil {
		t.Fatal("invalid auth style should be rejected")
	}
}

// TestCustomPassthroughResponse 验证 passthrough 仅复制安全头并保留受限错误状态与正文喵。
func TestCustomPassthroughResponse(t *testing.T) {
	// 构造包含安全追踪头和危险连接头的上游失败响应喵。
	responseHeaders := make(http.Header)
	responseHeaders.Set("X-Upstream-Request-Id", "upstream-1")
	responseHeaders.Set("Connection", "close")
	responseRecorder := httptest.NewRecorder()
	// 回传 429 和有限错误正文，供客户端按原协议读取喵。
	CopyCustomPassthroughResponse(responseRecorder, responseHeaders, http.StatusTooManyRequests, []byte("busy"))
	if responseRecorder.Code != http.StatusTooManyRequests || responseRecorder.Body.String() != "busy" || responseRecorder.Header().Get("X-Upstream-Request-Id") != "upstream-1" || responseRecorder.Header().Get("Connection") != "" {
		t.Fatalf("unexpected passthrough response: status=%d headers=%#v body=%q", responseRecorder.Code, responseRecorder.Header(), responseRecorder.Body.String())
	}
	// 非法成功状态不可被当作上游错误写回，防止规则逻辑伪造成功响应喵。
	invalidStatusRecorder := httptest.NewRecorder()
	CopyCustomPassthroughResponse(invalidStatusRecorder, responseHeaders, http.StatusOK, []byte("unsafe"))
	if invalidStatusRecorder.Code != http.StatusOK || invalidStatusRecorder.Body.Len() != 0 {
		t.Fatalf("invalid passthrough should not write a body: status=%d body=%q", invalidStatusRecorder.Code, invalidStatusRecorder.Body.String())
	}
}

func TestNormalizeCustomCandidateExecutionFailure(t *testing.T) {
	// HTTP 状态失败应生成包含状态、分类和正文摘要的结构化失败喵。
	failure := NormalizeCandidateFailure(http.StatusTooManyRequests, http.Header{"Retry-After": []string{"9"}}, []byte("busy"), nil)
	executionFailure := &CustomCandidateExecutionFailure{Failure: failure}
	if executionFailure.Error() != "custom candidate execution failed: rate_limited" || executionFailure.Failure.RetryAfter != 9 {
		t.Fatalf("unexpected execution failure: %#v", executionFailure)
	}
	// 空失败对象必须返回受控错误文本，避免错误路径 panic 喵。
	var nilFailure *CustomCandidateExecutionFailure
	if nilFailure.Error() != "custom candidate execution failed" {
		t.Fatalf("nil execution failure error = %q", nilFailure.Error())
	}
}
