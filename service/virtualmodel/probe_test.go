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

// TestProbeCustomStreamingResponseEmptyStream 验证流在达门槛前结束视为空流失败喵。
func TestProbeCustomStreamingResponseEmptyStream(t *testing.T) {
	// 只有 [DONE] 事件的行在探测阶段被跳过，EOF 后内容不足判定空流喵。
	reader := bufio.NewReader(strings.NewReader("data: [DONE]\n"))
	_, err := probeCustomStreamingResponse(reader, ProbeParameters{StallTimeoutSeconds: 60, MinContentChars: 10, ProbeTotalTimeoutSeconds: 60})
	require.Error(t, err)
}

// TestProbeCustomStreamingResponseErrorEvent 验证上游 error 事件在提交前转为失败喵。
func TestProbeCustomStreamingResponseErrorEvent(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("data: {\"error\":{\"message\":\"boom\"}}\n"))
	_, err := probeCustomStreamingResponse(reader, ProbeParameters{StallTimeoutSeconds: 60, MinContentChars: 10, ProbeTotalTimeoutSeconds: 60})
	require.Error(t, err)
}

// TestProbeCustomStreamingResponseTotalBudget 验证探测总预算耗尽被识别为卡流喵。
func TestProbeCustomStreamingResponseTotalBudget(t *testing.T) {
	// 缓慢心跳持续但不产生业务内容，1 秒总预算耗尽后判定探测失败喵。
	reader := bufio.NewReader(&slowHeartbeatProbeReader{content: []byte("data: ping\n")})
	_, err := probeCustomStreamingResponse(reader, ProbeParameters{StallTimeoutSeconds: 60, MinContentChars: 10, ProbeTotalTimeoutSeconds: 1})
	require.Error(t, err)
	require.True(t, errors.Is(err, relaykitypes.ErrStalledStream))
}
