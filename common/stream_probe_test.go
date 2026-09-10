package common

import "testing"

// TestStreamProbeContentChars 验证不同流式格式的内容字节数提取与兜底喵。
// 注意：口径为 UTF-8 字节数（len），CJK 文本按字节而非字符计数喵。
func TestStreamProbeContentChars(t *testing.T) {
	// 定义各 SSE 格式负载与预期内容字节数的表格测试喵。
	testCases := []struct {
		name string
		data string
		want int
	}{
		{name: "openai chat delta", data: `{"choices":[{"delta":{"content":"Hello"}}]}`, want: 5},
		{name: "deepseek reasoning content", data: `{"choices":[{"delta":{"reasoning_content":"Think"}}]}`, want: 5},
		{name: "deepseek reasoning alias", data: `{"choices":[{"delta":{"reasoning":"Deep"}}]}`, want: 4},
		{name: "reasoning preferred over empty content", data: `{"choices":[{"delta":{"content":"","reasoning_content":"abc"}}]}`, want: 3},
		{name: "message reasoning content", data: `{"choices":[{"message":{"reasoning_content":"Msg"}}]}`, want: 3},
		{name: "claude thinking delta", data: `{"delta":{"thinking":"Mull"}}`, want: 4},
		{name: "top level delta reasoning", data: `{"delta":{"reasoning_content":"Top"}}`, want: 3},
		{name: "openai message content", data: `{"choices":[{"message":{"content":"Hi"}}]}`, want: 2},
		{name: "claude delta text", data: `{"delta":{"text":"World"}}`, want: 5},
		{name: "gemini parts text", data: `{"candidates":[{"content":{"parts":[{"text":"Gemini"}]}}]}`, want: 6},
		{name: "responses output text", data: `{"output":[{"content":[{"text":"Response"}]}]}`, want: 8},
		{name: "baidu result counts utf8 bytes", data: `{"result":"百度"}`, want: len("百度")},
		{name: "dify answer", data: `{"answer":"Dify"}`, want: 4},
		{name: "empty payload", data: "", want: 0},
		{name: "blank payload", data: "   ", want: 0},
		{name: "unknown format falls back to visible bytes", data: `{"unexpected":"abc"}`, want: 20},
	}
	// 逐项断言内容字节数提取结果喵。
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := StreamProbeContentChars(testCase.data); got != testCase.want {
				t.Fatalf("StreamProbeContentChars(%q) = %d, want %d", testCase.data, got, testCase.want)
			}
		})
	}
}

// TestStreamProbeContentText 验证 SSE 数据事件到业务文本的还原喵。
// 该函数供虚拟模型跳过候选按原生口径估算 token 使用：必须只返回真正的业务文本，
// 未命中已知格式时返回空串（而不是把整段 JSON 当文本），避免虚增估算额度喵。
func TestStreamProbeContentText(t *testing.T) {
	// 定义各协议负载与预期还原文本的表格测试喵。
	testCases := []struct {
		name string
		data string
		want string
	}{
		{name: "openai chat delta", data: `{"choices":[{"delta":{"content":"Hello"}}]}`, want: "Hello"},
		{name: "openai message content", data: `{"choices":[{"message":{"content":"Hi"}}]}`, want: "Hi"},
		{name: "deepseek reasoning content", data: `{"choices":[{"delta":{"reasoning_content":"Think"}}]}`, want: "Think"},
		{name: "deepseek reasoning alias", data: `{"choices":[{"delta":{"reasoning":"Deep"}}]}`, want: "Deep"},
		{name: "claude delta text", data: `{"delta":{"text":"World"}}`, want: "World"},
		{name: "claude thinking delta", data: `{"delta":{"thinking":"Mull"}}`, want: "Mull"},
		{name: "gemini parts text", data: `{"candidates":[{"content":{"parts":[{"text":"Gemini"}]}}]}`, want: "Gemini"},
		{name: "responses output text", data: `{"output":[{"content":[{"text":"Response"}]}]}`, want: "Response"},
		{name: "baidu result", data: `{"result":"百度"}`, want: "百度"},
		{name: "dify answer", data: `{"answer":"Dify"}`, want: "Dify"},
		{name: "empty payload", data: "", want: ""},
		{name: "blank payload", data: "   ", want: ""},
		// 喵~防御：未知格式必须返回空串，绝不能把整段 JSON 当成模型输出文本喵。
		{name: "unknown format returns empty", data: `{"unexpected":"abc"}`, want: ""},
		// 喵~防御：非法 JSON 同样退化为空串，不参与计费估算喵。
		{name: "invalid json returns empty", data: `{"choices":[`, want: ""},
		// 喵~防御：心跳等非业务事件不含文本，返回空串喵。
		{name: "heartbeat returns empty", data: `{"type":"ping"}`, want: ""},
	}
	// 逐项断言业务文本还原结果喵。
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := StreamProbeContentText(testCase.data); got != testCase.want {
				t.Fatalf("StreamProbeContentText(%q) = %q, want %q", testCase.data, got, testCase.want)
			}
		})
	}
}
