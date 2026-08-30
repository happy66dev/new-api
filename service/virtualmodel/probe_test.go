package virtualmodel

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	relaykitypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

// blockingProbeReader 阻塞读直到 release 通道关闭，用于模拟上游静默喵。
type blockingProbeReader struct {
	release <-chan struct{}
}

// Read 在 release 关闭前永不返回数据喵。
func (reader blockingProbeReader) Read(_ []byte) (int, error) {
	<-reader.release
	return 0, io.EOF
}

// slowHeartbeatProbeReader 每行之间停顿，模拟上游持续但缓慢的心跳喵。
type slowHeartbeatProbeReader struct {
	content []byte
	offset  int
}

// Read 每次调用先停顿再返回一行内容，避免快速心跳提前撑爆探测缓冲喵。
func (reader *slowHeartbeatProbeReader) Read(buffer []byte) (int, error) {
	time.Sleep(50 * time.Millisecond)
	n := copy(buffer, reader.content[reader.offset:])
	reader.offset = (reader.offset + n) % len(reader.content)
	return n, nil
}

// TestProbeCustomStreamingResponseContentThreshold 验证内容字符累积达到门槛后才放流喵。
func TestProbeCustomStreamingResponseContentThreshold(t *testing.T) {
	// 两行内容合计 11 字符，门槛 8 时第二行读入后放流，返回全部已缓冲行喵。
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n"
	reader := bufio.NewReader(strings.NewReader(sse))
	buffer, err := probeCustomStreamingResponse(reader, ProbeParameters{StallTimeoutSeconds: 60, MinContentChars: 8, ProbeTotalTimeoutSeconds: 60})
	require.NoError(t, err)
	require.Contains(t, string(buffer), "Hello")
	require.Contains(t, string(buffer), "world")
}

// TestProbeCustomStreamingResponseSingleEventMeetsThreshold 验证单个事件即可达门槛喵。
func TestProbeCustomStreamingResponseSingleEventMeetsThreshold(t *testing.T) {
	// 单行内容 5 字符大于门槛 3，第一行即放流喵。
	reader := bufio.NewReader(strings.NewReader("data: {\"delta\":{\"text\":\"Hi!\"}}\n"))
	buffer, err := probeCustomStreamingResponse(reader, ProbeParameters{StallTimeoutSeconds: 60, MinContentChars: 3, ProbeTotalTimeoutSeconds: 60})
	require.NoError(t, err)
	require.Contains(t, string(buffer), "Hi!")
}

// TestProbeCustomStreamingResponseStallTimeout 验证静默超时被识别为卡流哨兵喵。
func TestProbeCustomStreamingResponseStallTimeout(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	reader := bufio.NewReader(blockingProbeReader{release: release})
	_, err := probeCustomStreamingResponse(reader, ProbeParameters{StallTimeoutSeconds: 1, MinContentChars: 10, ProbeTotalTimeoutSeconds: 60})
	require.Error(t, err)
	require.True(t, errors.Is(err, relaykitypes.ErrStalledStream))
}

// TestProbeCustomStreamingResponseEmptyStream 验证仅零字节空响应判定空流失败喵。
// 已缓冲任何事件字节（含 [DONE]）的流视为合法空回复放流，兼容上游返回空 choices 的场景喵。
func TestProbeCustomStreamingResponseEmptyStream(t *testing.T) {
	// 仅 [DONE] 事件：缓冲非空，按新语义放流为空回复喵。
	reader := bufio.NewReader(strings.NewReader("data: [DONE]\n"))
	precommitBytes, err := probeCustomStreamingResponse(reader, ProbeParameters{StallTimeoutSeconds: 60, MinContentChars: 10, ProbeTotalTimeoutSeconds: 60})
	require.NoError(t, err)
	require.Equal(t, "data: [DONE]\n", string(precommitBytes))

	// 零字节空响应：没有任何可回放字节，判定空流故障喵。
	emptyReader := bufio.NewReader(strings.NewReader(""))
	_, err = probeCustomStreamingResponse(emptyReader, ProbeParameters{StallTimeoutSeconds: 60, MinContentChars: 10, ProbeTotalTimeoutSeconds: 60})
	require.Error(t, err)
}

// TestProbeCustomStreamingResponseErrorEvent 验证上游 error 事件在提交前转为失败喵。
func TestProbeCustomStreamingResponseErrorEvent(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("data: {\"error\":{\"message\":\"boom\"}}\n"))
	_, err := probeCustomStreamingResponse(reader, ProbeParameters{StallTimeoutSeconds: 60, MinContentChars: 10, ProbeTotalTimeoutSeconds: 60})
	require.Error(t, err)
}

// TestProbeCustomStreamingResponseAnthropicNullErrorNotError 验证 Anthropic 合法事件中的 "error":null 不误判为错误喵。
// 曾因裸 "error" 子串匹配把 message_start 的 error:null 当成上游错误，导致所有 Anthropic 流式候选被误杀喵。
func TestProbeCustomStreamingResponseAnthropicNullErrorNotError(t *testing.T) {
	// 完整模拟 Anthropic 流式开头：message_start 元数据事件带 error:null，随后 content_block_delta 携带正文喵。
	sse := "event: message_start\ndata: {\"type\":\"message_start\",\"content_block\":null,\"delta\":null,\"error\":null,\"index\":0,\"message\":{\"content\":[],\"id\":\"242c3b8ae45cd2dc6958c2\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello!\"}}\n\n"
	reader := bufio.NewReader(strings.NewReader(sse))
	buffer, err := probeCustomStreamingResponse(reader, ProbeParameters{StallTimeoutSeconds: 60, MinContentChars: 3, ProbeTotalTimeoutSeconds: 60})
	require.NoError(t, err)
	require.Contains(t, string(buffer), "Hello!")
}

// TestProbeCustomStreamingResponseAnthropicErrorEvent 验证 Anthropic 显式 type=error 事件仍被识别为上游错误喵。
func TestProbeCustomStreamingResponseAnthropicErrorEvent(t *testing.T) {
	// message_start 的 error:null 合法通过后，error 事件必须被捕获并携带 SSE 字节供回放喵。
	sse := "event: message_start\ndata: {\"type\":\"message_start\",\"error\":null}\n\n" +
		"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n"
	reader := bufio.NewReader(strings.NewReader(sse))
	_, err := probeCustomStreamingResponse(reader, ProbeParameters{StallTimeoutSeconds: 60, MinContentChars: 3, ProbeTotalTimeoutSeconds: 60})
	require.Error(t, err)
	// 应返回携带 SSE 字节的 UpstreamStreamError，供直调透传或失败规则 passthrough 原样回放喵。
	var streamError *UpstreamStreamError
	require.ErrorAs(t, err, &streamError)
}

// TestProbeCustomStreamingResponseTotalBudget 验证探测总预算耗尽被识别为卡流喵。
func TestProbeCustomStreamingResponseTotalBudget(t *testing.T) {
	// 缓慢心跳持续但不产生业务内容，1 秒总预算耗尽后判定探测失败喵。
	reader := bufio.NewReader(&slowHeartbeatProbeReader{content: []byte("data: ping\n")})
	_, err := probeCustomStreamingResponse(reader, ProbeParameters{StallTimeoutSeconds: 60, MinContentChars: 10, ProbeTotalTimeoutSeconds: 1})
	require.Error(t, err)
	require.True(t, errors.Is(err, relaykitypes.ErrStalledStream))
}

// TestProbeCustomStreamingResponseTotalBudgetHardCap 验证 stall > total 时总预算仍是硬上限喵。
// 单次读行最长阻塞 stall 秒，但总预算必须成为硬上限：完全静默的上游也会在 total 内被终止喵。
func TestProbeCustomStreamingResponseTotalBudgetHardCap(t *testing.T) {
	// stall 设为 60s 远大于 total 1s，阻塞读上游没有任何数据，读行必须在 total 内被硬性终止喵。
	release := make(chan struct{})
	defer close(release)
	reader := bufio.NewReader(blockingProbeReader{release: release})
	_, err := probeCustomStreamingResponse(reader, ProbeParameters{StallTimeoutSeconds: 60, MinContentChars: 10, ProbeTotalTimeoutSeconds: 1})
	require.Error(t, err)
	require.True(t, errors.Is(err, relaykitypes.ErrStalledStream))
}

// TestProbeCustomStreamingResponseOversizeLine 验证超长单行被拒绝喵。
func TestProbeCustomStreamingResponseOversizeLine(t *testing.T) {
	// 构造单行超过 1MB 上限的 SSE 流，探测必须报错而非无限缓冲喵。
	oversizeLine := "data: " + strings.Repeat("x", userUpstreamStreamLineLimit+1024) + "\n"
	reader := bufio.NewReader(strings.NewReader(oversizeLine))
	_, err := probeCustomStreamingResponse(reader, ProbeParameters{StallTimeoutSeconds: 60, MinContentChars: 10, ProbeTotalTimeoutSeconds: 60})
	require.Error(t, err)
}
