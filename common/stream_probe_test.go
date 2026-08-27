package common

import "testing"

// TestStreamProbeContentChars 验证不同流式格式的内容字符提取与兜底喵。
func TestStreamProbeContentChars(t *testing.T) {
	// 定义各 SSE 格式负载与预期内容字符数的表格测试喵。
	testCases := []struct {
		name string
		data string
		want int
	}{
		{name: "openai chat delta", data: `{"choices":[{"delta":{"content":"Hello"}}]}`, want: 5},
		{name: "openai message content", data: `{"choices":[{"message":{"content":"Hi"}}]}`, want: 2},
		{name: "claude delta text", data: `{"delta":{"text":"World"}}`, want: 5},
		{name: "gemini parts text", data: `{"candidates":[{"content":{"parts":[{"text":"Gemini"}]}}]}`, want: 6},
		{name: "responses output text", data: `{"output":[{"content":[{"text":"Response"}]}]}`, want: 8},
		{name: "baidu result counts bytes", data: `{"result":"百度"}`, want: len("百度")},
		{name: "dify answer", data: `{"answer":"Dify"}`, want: 4},
		{name: "empty payload", data: "", want: 0},
		{name: "blank payload", data: "   ", want: 0},
		{name: "unknown format falls back to visible chars", data: `{"unexpected":"abc"}`, want: 20},
	}
	// 逐项断言内容字符提取结果喵。
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := StreamProbeContentChars(testCase.data); got != testCase.want {
				t.Fatalf("StreamProbeContentChars(%q) = %d, want %d", testCase.data, got, testCase.want)
			}
		})
	}
}
