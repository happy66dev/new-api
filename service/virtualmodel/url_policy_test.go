package virtualmodel

import "testing"

// TestValidateCustomBaseURL 验证自定义上游 URL 的 HTTPS、端口与危险组成部分防御喵。
func TestValidateCustomBaseURL(t *testing.T) {
	// 定义安全 URL 和常见 SSRF、认证泄露、格式异常输入喵。
	testCases := []struct {
		name        string
		input       string
		expectError bool
	}{
		{name: "public HTTPS hostname", input: "https://api.example.com/v1", expectError: false},
		{name: "HTTP protocol", input: "http://api.example.com", expectError: true},
		{name: "nonstandard port", input: "https://api.example.com:8443", expectError: true},
		{name: "userinfo", input: "https://secret@api.example.com", expectError: true},
		{name: "fragment", input: "https://api.example.com/#fragment", expectError: true},
		{name: "literal loopback", input: "https://127.0.0.1", expectError: true},
		{name: "empty value", input: "", expectError: true},
	}
	// 逐项校验 URL 策略，确保危险值不会进入加密持久化或执行链路喵。
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			parsedURL, validateError := ValidateCustomBaseURL(testCase.input)
			if (validateError != nil) != testCase.expectError {
				t.Fatalf("ValidateCustomBaseURL(%q) error=%v wantError=%v", testCase.input, validateError, testCase.expectError)
			}
			if !testCase.expectError && SummarizeCustomBaseURL(parsedURL) != "https://api.example.com" {
				t.Fatalf("unexpected URL summary: %q", SummarizeCustomBaseURL(parsedURL))
			}
		})
	}
}
