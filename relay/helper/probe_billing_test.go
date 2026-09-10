package helper

import (
	"errors"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBufferedProbeDataFromError 覆盖「从探测失败错误里取回放流前缓存」的全部路径喵。
// 虚拟模型跳过候选的计费估算完全依赖这份缓存，取错会让计费口径偏离原生 new-api 喵。
func TestBufferedProbeDataFromError(t *testing.T) {
	// 喵~防御：空错误没有任何缓存内容，必须返回 nil 而不是 panic 喵。
	assert.Nil(t, BufferedProbeDataFromError(nil))

	// 普通错误（非探测失败）必须返回 nil，避免被误当成「有上游内容」而虚增计费喵。
	assert.Nil(t, BufferedProbeDataFromError(errors.New("plain error")))
	assert.Nil(t, BufferedProbeDataFromError(types.ErrStalledStream))

	// 探测失败错误应能取回缓存的数据行喵。
	buffered := []string{`{"choices":[{"delta":{"content":"Hello"}}]}`, `{"choices":[{"delta":{"content":"World"}}]}`}
	probeError := wrapProbeErrorWithBufferedData(types.ErrStalledStream, buffered)
	require.NotNil(t, probeError)
	assert.Equal(t, buffered, BufferedProbeDataFromError(probeError))

	// 包装后错误文案必须仍是原始哨兵描述，保证失败规则与日志看到的口径不变喵。
	assert.Equal(t, types.ErrStalledStream.Error(), probeError.Error())
	// 哨兵必须可穿透包装被 errors.Is 识别，否则失败分类会退化喵。
	assert.True(t, errors.Is(probeError, types.ErrStalledStream))

	// 再套一层 fmt 包装（模拟 controller / 渠道 handler 的二次包装）仍应能穿透取到缓存喵。
	nested := fmt.Errorf("channel handler: %w", probeError)
	assert.Equal(t, buffered, BufferedProbeDataFromError(nested))

	// 无缓存的探测失败：返回 nil，调用方据此退化为「只计 prompt 估算」喵。
	emptyProbeError := wrapProbeErrorWithBufferedData(types.ErrStalledStream, nil)
	assert.Nil(t, BufferedProbeDataFromError(emptyProbeError))
}

// TestWrapProbeErrorWithBufferedDataNilCause 覆盖没有失败原因时不做包装的防御分支喵。
func TestWrapProbeErrorWithBufferedDataNilCause(t *testing.T) {
	// 喵~防御：没有原因时返回 nil，避免造出一个「无原因」的空错误喵。
	assert.Nil(t, wrapProbeErrorWithBufferedData(nil, []string{"data"}))
}

// TestProbeBufferedDataErrorNilSafety 覆盖包装类型自身的空值防御喵。
func TestProbeBufferedDataErrorNilSafety(t *testing.T) {
	var probeError *probeBufferedDataError
	// 喵~防御：空指针上调用 Error / Unwrap 必须安全，避免日志打印时 panic 喵。
	assert.Equal(t, "", probeError.Error())
	assert.Nil(t, probeError.Unwrap())
}

// TestStreamProbeStateFailCarriesBuffer 覆盖 scanner 协程投递失败时的缓存附带行为喵。
func TestStreamProbeStateFailCarriesBuffer(t *testing.T) {
	// 构造一个只用于投递失败信号的最小探测状态：失败通道带缓冲，投递不阻塞喵。
	probeState := &streamProbeState{failedChan: make(chan error, 1)}
	probeState.bufferProbeData(`{"choices":[{"delta":{"content":"Partial"}}]}`, 7)

	// 投递一次卡流失败：错误应携带放流前已缓存的数据行喵。
	probeState.fail(types.ErrStalledStream)
	failedError := <-probeState.failedChan
	require.Error(t, failedError)
	assert.Equal(t, []string{`{"choices":[{"delta":{"content":"Partial"}}]}`}, BufferedProbeDataFromError(failedError))

	// 错误携带的缓存来自 takeBufferSnapshot 的副本：改动它不得影响 scanner 内部缓存，
	// 否则后续候选估算会读到被改写的「上游内容」喵。
	snapshot := BufferedProbeDataFromError(failedError)
	require.Len(t, snapshot, 1)
	snapshot[0] = "mutated"
	assert.Equal(t, []string{`{"choices":[{"delta":{"content":"Partial"}}]}`}, probeState.takeBufferSnapshot())
}

// TestStreamProbeStateFailNilSafety 覆盖投递失败时的空值与非阻塞防御喵。
func TestStreamProbeStateFailNilSafety(t *testing.T) {
	// 喵~防御：空状态或空错误时投递必须静默跳过，不得 panic 喵。
	var nilState *streamProbeState
	assert.NotPanics(t, func() {
		nilState.fail(types.ErrStalledStream)
		nilState.wrapWithBufferedData(types.ErrStalledStream)
	})

	// 喵~防御：没有缓存的状态也应原样返回失败原因，不丢失错误分类喵。
	probeState := &streamProbeState{failedChan: make(chan error, 1)}
	probeState.fail(nil)
	assert.Len(t, probeState.failedChan, 0)
	assert.True(t, errors.Is(probeState.wrapWithBufferedData(types.ErrStreamCut), types.ErrStreamCut))

	// 喵~防御：失败通道已满时投递不得阻塞 scanner 协程喵。
	probeState.fail(types.ErrStalledStream)
	assert.NotPanics(t, func() { probeState.fail(types.ErrStreamCut) })
	assert.Len(t, probeState.failedChan, 1)
}
