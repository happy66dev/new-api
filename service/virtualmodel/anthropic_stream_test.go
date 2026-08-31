package virtualmodel

import (
	"bufio"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCustomProbeContentCharsAnthropicMetadata 验证 Anthropic 元数据事件不计为内容字符喵。
func TestCustomProbeContentCharsAnthropicMetadata(t *testing.T) {
	// message_start 携带较长 JSON（含消息元数据），此前会被兜底计数导致提前放流喵。
	messageStart := `{"type":"message_start","message":{"id":"msg_123","type":"message","role":"assistant","model":"claude-sonnet-4","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":25,"output_tokens":1}}}`
	// 元数据事件不得算作业务内容，保证探测等到真实文本增量才放流喵。
	assert.Equal(t, 0, customProbeContentChars(messageStart))
	// content_block_start 同样为元数据喵。
	assert.Equal(t, 0, customProbeContentChars(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`))
	// message_delta 仅携带 usage，不得计数喵。
	assert.Equal(t, 0, customProbeContentChars(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":15}}`))
}

// TestCustomProbeContentCharsAnthropicDelta 验证 Anthropic 文本增量事件按 delta.text 计数喵。
func TestCustomProbeContentCharsAnthropicDelta(t *testing.T) {
	// content_block_delta 的 delta.text 是唯一内容来源（按 UTF-8 字节数计数）喵。
	assert.Equal(t, len("你好世界"), customProbeContentChars(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"你好世界"}}`))
	// 工具调用的 input_json_delta 无 delta.text，不计入文本内容喵。
	assert.Equal(t, 0, customProbeContentChars(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"北京\"}"}}`))
	// Anthropic 思考增量（thinking_delta）：长思考模型的推理也视为业务内容，探测在思考阶段就放流喵。
	assert.Equal(t, 4, customProbeContentChars(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Mull"}}`))
}

// TestCustomProbeContentCharsOpenAI 验证 OpenAI 聊天增量按 delta.content 计数喵。
func TestCustomProbeContentCharsOpenAI(t *testing.T) {
	assert.Equal(t, 5, customProbeContentChars(`{"choices":[{"delta":{"content":"hello"}}]}`))
	// DeepSeek 推理增量：无顶层 type 时经 StreamProbeContentChars 兜底计数，长思考模型提前放流喵。
	assert.Equal(t, 5, customProbeContentChars(`{"choices":[{"delta":{"reasoning_content":"Think"}}]}`))
	// 部分中转站的推理字段别名同样计数喵。
	assert.Equal(t, 4, customProbeContentChars(`{"choices":[{"delta":{"reasoning":"Deep"}}]}`))
	// 无 type 字段的未知负载保留兜底可见字符计数，避免非主流格式探测永久阻塞喵。
	assert.Positive(t, customProbeContentChars(`{"foo":"bar"}`))
}

// TestIsCustomStreamEndEvent 验证流结束判定兼容 [DONE] 与 Anthropic message_stop 喵。
func TestIsCustomStreamEndEvent(t *testing.T) {
	// OpenAI 结束哨兵喵。
	assert.True(t, isCustomStreamEndEvent([]byte("data: [DONE]\n\n")))
	// Anthropic 结束事件喵。
	assert.True(t, isCustomStreamEndEvent([]byte(`data: {"type":"message_stop"}`)))
	// 普通内容增量不算结束喵。
	assert.False(t, isCustomStreamEndEvent([]byte(`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}`)))
	// 非 data 行不算结束喵。
	assert.False(t, isCustomStreamEndEvent([]byte("event: message_delta")))
}

// TestEnsureAnthropicVersionHeader 验证 Anthropic 请求补缺省版本头喵。
func TestEnsureAnthropicVersionHeader(t *testing.T) {
	// /v1/messages 路径且缺省时补默认版本喵。
	headers := http.Header{}
	ensureAnthropicVersionHeader(headers, "/v1/messages")
	assert.Equal(t, "2023-06-01", headers.Get("anthropic-version"))
	// 已有版本头不覆盖客户端显式值喵。
	headers.Set("anthropic-version", "2024-01-01")
	ensureAnthropicVersionHeader(headers, "/v1/messages")
	assert.Equal(t, "2024-01-01", headers.Get("anthropic-version"))
	// 非 messages 路径不补版本头喵。
	otherHeaders := http.Header{}
	ensureAnthropicVersionHeader(otherHeaders, "/v1/chat/completions")
	assert.Equal(t, "", otherHeaders.Get("anthropic-version"))
	// 空头不 panic 喵。
	ensureAnthropicVersionHeader(nil, "/v1/messages")
}

// TestProbeCustomStreamingResponseAnthropic 验证 Anthropic 流只在真实文本增量后才放流喵。
func TestProbeCustomStreamingResponseAnthropic(t *testing.T) {
	// mock 上游按 Anthropic 流式顺序回包：message_start → content_block_delta → message_stop 喵。
	streamText := "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"role\":\"assistant\",\"content\":[]}}\n\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"你好\"}}\n\n" +
		"data: {\"type\":\"message_stop\"}\n\n"
	reader := bufio.NewReader(strings.NewReader(streamText))
	buffered, probeError := probeCustomStreamingResponse(reader, ProbeParameters{
		StallTimeoutSeconds:      5,
		MinContentChars:          1,
		ProbeTotalTimeoutSeconds: 10,
	})
	require.NoError(t, probeError)
	// 放流缓冲必须已包含 message_start 与真实内容增量（探测等到 content_block_delta 才结束）喵。
	require.Contains(t, string(buffered), "message_start")
	require.Contains(t, string(buffered), "text_delta")
	// 剩余流中仍包含 message_stop 结束事件，供转发循环继续读取喵。
	remainingText := streamText[len(buffered):]
	require.Contains(t, remainingText, "message_stop")
}

// TestProbeCustomStreamingResponseAnthropicTimeoutGuard 验证只发元数据且不结束的流按静默超时返回卡流喵。
func TestProbeCustomStreamingResponseAnthropicTimeoutGuard(t *testing.T) {
	pipeReader, pipeWriter := io.Pipe()
	defer pipeWriter.Close()
	reader := bufio.NewReader(pipeReader)
	go func() {
		// 只写一个 message_start 元数据事件后保持打开不再写入，模拟上游静默喵。
		_, _ = pipeWriter.Write([]byte("data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\"}}\n\n"))
	}()
	probeStart := time.Now()
	_, probeError := probeCustomStreamingResponse(reader, ProbeParameters{
		StallTimeoutSeconds:      1,
		MinContentChars:          10,
		ProbeTotalTimeoutSeconds: 10,
	})
	// 元数据不计数 + 无 EOF 时必须按静默超时返回卡流错误喵。
	require.Error(t, probeError)
	require.Less(t, time.Since(probeStart), 5*time.Second)
}
