package virtualmodel

import "testing"

// TestValidateCustomBaseURL 验证自定义上游 URL 的 http/非 443 端口放宽、危险组成部分与字面 IP 防御喵。
func TestValidateCustomBaseURL(t *testing.T) {
	// 定义安全 URL 和常见 SSRF、认证泄露、格式异常输入喵。
	testCases := []struct {
		name        string
		input       string
		expectError bool
		summary     string
	}{
		{name: "public HTTPS hostname", input: "https://api.example.com/v1", expectError: false, summary: "https://api.example.com"},
		// http 协议与非 443 端口已放宽，公网可达性由拨号阶段公网 IP 校验兜底喵。
		{name: "HTTP protocol", input: "http://api.example.com", expectError: false, summary: "http://api.example.com"},
		{name: "nonstandard HTTPS port", input: "https://api.example.com:8443", expectError: false, summary: "https://api.example.com"},
		{name: "HTTP nonstandard port", input: "http://api.example.com:8080", expectError: false, summary: "http://api.example.com"},
		// 危险组成部分与字面 IP 仍拒绝，防认证混淆与内网探测喵。
		{name: "userinfo", input: "https://secret@api.example.com", expectError: true},
		{name: "fragment", input: "https://api.example.com/#fragment", expectError: true},
		{name: "literal loopback", input: "https://127.0.0.1", expectError: true},
		{name: "literal public IP", input: "http://8.8.8.8:80", expectError: true},
		// 非 http/https scheme 与畸形端口拒绝喵。
		{name: "ftp protocol", input: "ftp://api.example.com", expectError: true},
		{name: "zero port", input: "http://api.example.com:0", expectError: true},
		{name: "port overflow", input: "https://api.example.com:70000", expectError: true},
		{name: "empty value", input: "", expectError: true},
	}
	// 逐项校验 URL 策略，确保危险值不会进入加密持久化或执行链路喵。
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			parsedURL, validateError := ValidateCustomBaseURL(testCase.input)
			if (validateError != nil) != testCase.expectError {
				t.Fatalf("ValidateCustomBaseURL(%q) error=%v wantError=%v", testCase.input, validateError, testCase.expectError)
			}
			// 合法输入摘要必须保留原始 scheme，与真实协议一致喵。
			if !testCase.expectError && SummarizeCustomBaseURL(parsedURL) != testCase.summary {
				t.Fatalf("unexpected URL summary: %q want %q", SummarizeCustomBaseURL(parsedURL), testCase.summary)
			}
		})
	}
}
