package virtualmodel

import (
	"bufio"
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
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
			rewrittenBody, rewriteError := rewrittenCustomRequestBody(context, "gpt-target", "")
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
	// 点路径段必须拒绝，避免自定义凭据被发送到 Base URL 前缀外的端点喵。
	dotSegmentRequest := &url.URL{Path: "/../admin", RawPath: "/../admin"}
	if _, dotSegmentError := buildCustomUpstreamURL(baseURL, dotSegmentRequest); dotSegmentError == nil {
		t.Fatal("dot-segment path should be rejected")
	}
	// 空 URL 参数必须拒绝，避免退化到错误的根路径请求喵。
	if _, nilBuildError := buildCustomUpstreamURL(nil, request.URL); nilBuildError == nil {
		t.Fatal("nil base URL should be rejected")
	}
	// 游乐场 /pg 入口路径必须归一化为 /v1，避免上游 new-api 网关把请求打到其 UserAuth 认证的 /pg 路径喵。
	pgRequest := httptest.NewRequest(http.MethodPost, "https://gateway.example.test/pg/chat/completions?stream=true", nil)
	pgUpstreamURL, pgBuildError := buildCustomUpstreamURL(baseURL, pgRequest.URL)
	if pgBuildError != nil {
		t.Fatalf("build custom upstream URL from /pg: %v", pgBuildError)
	}
	if pgUpstreamURL.String() != "https://api.example.test/prefix/v1/chat/completions?stream=true" {
		t.Fatalf("pg upstream URL = %s, want /v1/chat/completions", pgUpstreamURL.String())
	}
}

// TestCustomHeaderFilteringAndAuthentication 验证客户端认证和 hop-by-hop 头不会穿透到上游喵。
func TestCustomHeaderFilteringAndAuthentication(t *testing.T) {
	// 构造包含危险和允许请求头的客户端请求头集合喵。
	sourceHeaders := make(http.Header)
	sourceHeaders.Set("Authorization", "Bearer client-secret")
	sourceHeaders.Set("x-api-key", "client-key")
	sourceHeaders.Set("Connection", "upgrade, X-Internal-Route")
	sourceHeaders.Set("X-Internal-Route", "internal")
	sourceHeaders.Set("Content-Length", "999")
	sourceHeaders.Set("Cookie", "session=client-secret")
	sourceHeaders.Set("Forwarded", "for=127.0.0.1")
	sourceHeaders.Set("X-Forwarded-For", "127.0.0.1")
	sourceHeaders.Set("X-Trace-Id", "trace-1")
	targetHeaders := make(http.Header)
	copyCustomUpstreamHeaders(targetHeaders, sourceHeaders)
	// 危险头必须被剔除，允许头需原样保留喵。
	if targetHeaders.Get("Authorization") != "" || targetHeaders.Get("x-api-key") != "" || targetHeaders.Get("Connection") != "" || targetHeaders.Get("X-Internal-Route") != "" || targetHeaders.Get("Content-Length") != "" || targetHeaders.Get("Cookie") != "" || targetHeaders.Get("Forwarded") != "" || targetHeaders.Get("X-Forwarded-For") != "" || targetHeaders.Get("X-Trace-Id") != "trace-1" {
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
	responseHeaders.Set("Set-Cookie", "session=unsafe; Path=/")
	responseHeaders.Set("Access-Control-Allow-Origin", "https://unsafe.example")
	responseRecorder := httptest.NewRecorder()
	// 回传 429 和有限错误正文，供客户端按原协议读取喵。
	CopyCustomPassthroughResponse(responseRecorder, responseHeaders, http.StatusTooManyRequests, []byte("busy"))
	if responseRecorder.Code != http.StatusTooManyRequests || responseRecorder.Body.String() != "busy" || responseRecorder.Header().Get("X-Upstream-Request-Id") != "upstream-1" || responseRecorder.Header().Get("Connection") != "" || responseRecorder.Header().Get("Set-Cookie") != "" || responseRecorder.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unexpected passthrough response: status=%d headers=%#v body=%q", responseRecorder.Code, responseRecorder.Header(), responseRecorder.Body.String())
	}
	// 2xx 内嵌 SSE error 事件（上游 HTTP 200 但流内报告错误）允许原样透传错误正文，供客户端解析上游真实错误喵。
	sseErrorRecorder := httptest.NewRecorder()
	CopyCustomPassthroughResponse(sseErrorRecorder, responseHeaders, http.StatusOK, []byte("data: {\"error\":{\"message\":\"upstream broke\"}}\n\n"))
	if sseErrorRecorder.Code != http.StatusOK || sseErrorRecorder.Body.String() != "data: {\"error\":{\"message\":\"upstream broke\"}}\n\n" {
		t.Fatalf("sse error passthrough should replay body: status=%d body=%q", sseErrorRecorder.Code, sseErrorRecorder.Body.String())
	}
	// 非法协议状态（1xx）不可被当作上游响应写回，防止规则逻辑伪造协议状态喵。
	invalidStatusRecorder := httptest.NewRecorder()
	CopyCustomPassthroughResponse(invalidStatusRecorder, responseHeaders, http.StatusContinue, []byte("unsafe"))
	if invalidStatusRecorder.Code == http.StatusContinue || invalidStatusRecorder.Body.Len() != 0 {
		t.Fatalf("invalid passthrough should not write a body: status=%d body=%q", invalidStatusRecorder.Code, invalidStatusRecorder.Body.String())
	}
}

// TestCopyCustomResponseHeadersStripsContentLength 验证响应头过滤集合剥离 content-length 喵。
// 透传截断正文（64KiB 或 SSE 错误字节）时若复制上游 Content-Length，客户端会按错误长度读取悬挂喵。
func TestCopyCustomResponseHeadersStripsContentLength(t *testing.T) {
	sourceHeaders := make(http.Header)
	sourceHeaders.Set("Content-Length", "99999")
	sourceHeaders.Set("Content-Type", "application/json")
	targetHeaders := make(http.Header)
	copyCustomResponseHeaders(targetHeaders, sourceHeaders)
	if targetHeaders.Get("Content-Length") != "" {
		t.Fatalf("content-length should be stripped, got %#v", targetHeaders)
	}
	if targetHeaders.Get("Content-Type") != "application/json" {
		t.Fatalf("content-type should be preserved, got %#v", targetHeaders)
	}
}

func TestCustomStreamingPrecommitProbe(t *testing.T) {
	// 定义有效业务事件、明确错误、仅心跳和空响应，验证提交前探测不会错误放流喵。
	testCases := []struct {
		name        string
		stream      string
		expectError bool
		expected    string
	}{
		{name: "business content", stream: ": keepalive\ndata: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n", expected: ": keepalive\ndata: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n"},
		{name: "upstream error", stream: "data: {\"error\":{\"message\":\"busy\"}}\n", expectError: true},
		// 已缓冲任何事件字节的流在 EOF 前结束都放流：兼容短回复、仅心跳或非标准 SSE 上游，仅零字节空响应判定故障喵。
		{name: "heartbeat only", stream: ": ping\ndata: ping\n", expected: ": ping\ndata: ping\n"},
		{name: "short content below threshold", stream: "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n", expected: "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n"},
		{name: "empty stream", stream: "", expectError: true},
	}
	// 逐项使用同一个 buffered reader 验证探测结果可回放且错误不会提交任何响应字节喵。
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			responseReader := bufio.NewReader(bytes.NewBufferString(testCase.stream))
			// 探测参数门槛设为 3，使单行 hello 内容（5 字符）即可放流喵。
			precommitBytes, probeError := probeCustomStreamingResponse(responseReader, ProbeParameters{StallTimeoutSeconds: 60, MinContentChars: 3, ProbeTotalTimeoutSeconds: 60})
			if (probeError != nil) != testCase.expectError {
				t.Fatalf("probeCustomStreamingResponse() error=%v wantError=%v", probeError, testCase.expectError)
			}
			if !testCase.expectError && string(precommitBytes) != testCase.expected {
				t.Fatalf("precommit bytes = %q, want %q", precommitBytes, testCase.expected)
			}
		})
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

// TestRewrittenCustomRequestBodyFieldReplacements 验证请求字段替换只作用于参数标量且绝不触碰 messages 喵。
func TestRewrittenCustomRequestBodyFieldReplacements(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testCases := []struct {
		name              string
		fieldReplacements string
		requestBody       string
		expectedBody      string
		expectError       bool
	}{
		// 命中映射旧值时替换为新值喵。
		{name: "replace reasoning_effort max to xhigh", fieldReplacements: `{"reasoning_effort":{"max":"xhigh"}}`, requestBody: `{"model":"m","reasoning_effort":"max"}`, expectedBody: `{"model":"target","reasoning_effort":"xhigh"}`},
		// 非字符串标量（数字）不替换，防止误改数值参数喵。
		{name: "non-string scalar skipped", fieldReplacements: `{"temperature":{"0":"1"}}`, requestBody: `{"model":"m","temperature":0}`, expectedBody: `{"model":"target","temperature":0}`},
		// 当前值未命中映射时原样保留喵。
		{name: "unmatched value preserved", fieldReplacements: `{"reasoning_effort":{"max":"xhigh"}}`, requestBody: `{"model":"m","reasoning_effort":"high"}`, expectedBody: `{"model":"target","reasoning_effort":"high"}`},
		// messages 开头的路径一律跳过，绝不改写对话内容喵。
		{name: "messages path never replaced", fieldReplacements: `{"messages.0.content":{"hi":"hello"}}`, requestBody: `{"model":"m","messages":[{"content":"hi"}]}`, expectedBody: `{"model":"target","messages":[{"content":"hi"}]}`},
		// 空配置保持请求体原样喵。
		{name: "empty config keeps body", fieldReplacements: "", requestBody: `{"model":"m","reasoning_effort":"max"}`, expectedBody: `{"model":"target","reasoning_effort":"max"}`},
		// 非法映射表（新值非字符串）直接拒绝喵。
		{name: "invalid replacements rejected", fieldReplacements: `{"reasoning_effort":{"max":123}}`, requestBody: `{"model":"m"}`, expectError: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "https://gateway.example.test/v1/chat/completions", bytes.NewBufferString(testCase.requestBody))
			request.Header.Set("Content-Type", "application/json")
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = request
			rewrittenBody, rewriteError := rewrittenCustomRequestBody(context, "target", testCase.fieldReplacements)
			if (rewriteError != nil) != testCase.expectError {
				t.Fatalf("rewrittenCustomRequestBody() error=%v wantError=%v", rewriteError, testCase.expectError)
			}
			if !testCase.expectError && string(rewrittenBody) != testCase.expectedBody {
				t.Fatalf("rewritten body = %s, want %s", rewrittenBody, testCase.expectedBody)
			}
		})
	}
}

// TestApplyCustomUpstreamHeaders 验证自定义请求头覆盖、危险头拒绝与 * 标记语义喵。
func TestApplyCustomUpstreamHeaders(t *testing.T) {
	// 合法配置覆盖同名客户端头，* 标记仅表示全部请求生效喵。
	targetHeaders := make(http.Header)
	targetHeaders.Set("User-Agent", "client-ua")
	if err := applyCustomUpstreamHeaders(targetHeaders, `{"*":true,"User-Agent":"Kilo-Code/7.3.50"}`); err != nil {
		t.Fatalf("apply custom headers: %v", err)
	}
	if targetHeaders.Get("User-Agent") != "Kilo-Code/7.3.50" {
		t.Fatalf("User-Agent = %q, want override", targetHeaders.Get("User-Agent"))
	}
	// 认证与 hop-by-hop 危险头必须拒绝，防止伪造凭据或破坏代理语义喵。
	if err := applyCustomUpstreamHeaders(targetHeaders, `{"authorization":"Bearer leak"}`); err == nil {
		t.Fatal("authorization header should be rejected")
	}
	if err := applyCustomUpstreamHeaders(targetHeaders, `{"Connection":"close"}`); err == nil {
		t.Fatal("connection header should be rejected")
	}
	// 非字符串头值必须拒绝，避免不可序列化值进入上游喵。
	if err := applyCustomUpstreamHeaders(targetHeaders, `{"X-Trace":123}`); err == nil {
		t.Fatal("non-string header value should be rejected")
	}
	// 空配置直接跳过，非法 JSON 拒绝喵。
	if err := applyCustomUpstreamHeaders(targetHeaders, ""); err != nil {
		t.Fatalf("empty config should pass: %v", err)
	}
	if err := applyCustomUpstreamHeaders(targetHeaders, `{bad`); err == nil {
		t.Fatal("invalid JSON should be rejected")
	}
	// 导出校验函数与执行路径同源，保存时提前拒绝危险配置喵。
	if err := ValidateCustomUpstreamHeadersJSON(`{"User-Agent":"ua"}`); err != nil {
		t.Fatalf("validate custom headers: %v", err)
	}
	if err := ValidateCustomUpstreamHeadersJSON(`{"host":"evil"}`); err == nil {
		t.Fatal("validate should reject host header")
	}
}

// TestParseCustomUpstreamHeaderOverrides 验证自定义头解析 helper：剥除 "*" 标记并过滤危险头喵。
// 该 helper 供原生 relay 中转链复用，行为必须与虚拟模型直连路径一致喵。
func TestParseCustomUpstreamHeaderOverrides(t *testing.T) {
	// 合法配置：* 标记被剥除（只是“全部请求生效”语义标记），真实请求头保留字符串值喵。
	overrides, err := ParseCustomUpstreamHeaderOverrides(`{"*":true,"User-Agent":"Kilo-Code/7.3.50"}`)
	require.NoError(t, err)
	require.Len(t, overrides, 1)
	require.Equal(t, "Kilo-Code/7.3.50", overrides["User-Agent"])
	// 喵~防御：* 绝不能出现在输出映射里，否则 relay 链路会误触发客户端头全量透传喵。
	_, hasWildcard := overrides["*"]
	require.False(t, hasWildcard)

	// 空配置返回空映射且不报错，避免无意义解析喵。
	emptyOverrides, err := ParseCustomUpstreamHeaderOverrides("")
	require.NoError(t, err)
	require.Nil(t, emptyOverrides)
	// 纯空白配置同样视为空喵。
	blankOverrides, err := ParseCustomUpstreamHeaderOverrides("  ")
	require.NoError(t, err)
	require.Nil(t, blankOverrides)

	// 非法 JSON 必须拒绝喵。
	_, err = ParseCustomUpstreamHeaderOverrides(`{bad`)
	require.Error(t, err)

	// 认证与 hop-by-hop 危险头必须拒绝，防止伪造凭据或破坏代理语义喵。
	_, err = ParseCustomUpstreamHeaderOverrides(`{"authorization":"Bearer leak"}`)
	require.Error(t, err)
	_, err = ParseCustomUpstreamHeaderOverrides(`{"Connection":"close"}`)
	require.Error(t, err)
	_, err = ParseCustomUpstreamHeaderOverrides(`{"host":"evil"}`)
	require.Error(t, err)

	// 空头名与非字符串头值必须拒绝喵。
	_, err = ParseCustomUpstreamHeaderOverrides(`{"":"value"}`)
	require.Error(t, err)
	_, err = ParseCustomUpstreamHeaderOverrides(`{"X-Trace":123}`)
	require.Error(t, err)
}

// TestValidateFieldReplacementsJSON 验证字段替换映射表保存校验规则喵。
func TestValidateFieldReplacementsJSON(t *testing.T) {
	// 合法映射表通过喵。
	if err := ValidateFieldReplacementsJSON(`{"reasoning_effort":{"max":"xhigh"}}`); err != nil {
		t.Fatalf("validate valid replacements: %v", err)
	}
	// messages 开头路径拒绝，对话内容绝不替换喵。
	if err := ValidateFieldReplacementsJSON(`{"messages.0.content":{"hi":"hello"}}`); err == nil {
		t.Fatal("messages path should be rejected")
	}
	// 空配置通过喵。
	if err := ValidateFieldReplacementsJSON(""); err != nil {
		t.Fatalf("empty config should pass: %v", err)
	}
	// 非法 JSON 拒绝喵。
	if err := ValidateFieldReplacementsJSON(`{bad`); err == nil {
		t.Fatal("invalid JSON should be rejected")
	}
	// 空字段路径拒绝喵。
	if err := ValidateFieldReplacementsJSON(`{"":{"a":"b"}}`); err == nil {
		t.Fatal("empty path should be rejected")
	}
}
