package helper

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	virtualmodelservice "github.com/QuantumNous/new-api/service/virtualmodel"
	"github.com/gin-gonic/gin"
)

// errProbeEndedBeforeContent 标记流在放流前结束（空流或内容不足），统一携带卡流哨兵供失败规则匹配喵。
var errProbeEndedBeforeContent = fmt.Errorf("%w: stream ended before content threshold", types.ErrStalledStream)

// StreamProbeConfig 描述流式候选放流前的健康探测参数，仅虚拟模型内部候选启用喵。
type StreamProbeConfig struct {
	// MinContentChars 放流前需累积的内容字符门槛喵。
	MinContentChars int
	// StallTimeout 静默多久判定卡流，收到字节后计时重置喵。
	StallTimeout time.Duration
	// ProbeTotalTimeout 探测阶段总预算，只管放流前的健康确认喵。
	ProbeTotalTimeout time.Duration
}

// streamProbeConfigFromContext 从请求上下文读取内部候选的探测参数，未启用探测时返回 nil 喵。
func streamProbeConfigFromContext(c *gin.Context) *StreamProbeConfig {
	// 喵~防御：缺少上下文时按未启用处理喵。
	if c == nil {
		return nil
	}
	// 只有虚拟模型内部候选才写入探测参数，普通请求读取不到喵。
	paramsValue, found := common.GetContextKey(c, constant.ContextKeyVirtualModelProbeParameters)
	if !found {
		return nil
	}
	probeParameters, ok := paramsValue.(virtualmodelservice.ProbeParameters)
	if !ok {
		return nil
	}
	// 把秒数转换为时长，探测参数为零时返回 nil 表示不启用喵。
	if probeParameters.StallTimeoutSeconds <= 0 || probeParameters.MinContentChars <= 0 || probeParameters.ProbeTotalTimeoutSeconds <= 0 {
		return nil
	}
	return &StreamProbeConfig{
		MinContentChars:   probeParameters.MinContentChars,
		StallTimeout:      time.Duration(probeParameters.StallTimeoutSeconds) * time.Second,
		ProbeTotalTimeout: time.Duration(probeParameters.ProbeTotalTimeoutSeconds) * time.Second,
	}
}

// streamProbeState 记录一次流式探测的运行状态喵。
type streamProbeState struct {
	config *StreamProbeConfig // 探测参数喵。

	// mu 保护探测缓存与内容计数喵。
	// 主人注意：缓存行由 scanner goroutine 追加，而主循环在卡流/总预算耗尽分支也要读取缓存快照，
	// 两个 goroutine 并发访问同一份切片会让 -race 报数据竞争，因此读写都必须经过下面的方法喵。
	mu                   sync.Mutex
	buffer               []string // 探测阶段缓存的 SSE data 行喵。
	bufferedContentChars int      // 累积内容字符数喵。

	failedChan chan error // 探测失败错误通道，带缓冲防阻塞喵。
}

// bufferProbeData 缓存一行探测数据、累计其内容字符数，并返回累积后的内容字符总数喵。
//
// 输入：data 为已剥掉 "data:" 前缀的上游 SSE 数据行；contentChars 为该行计入门槛的内容字符数（心跳行传 0）喵。
// 输出：当前累积的内容字符总数，供调用方与内容门槛比较喵。
func (state *streamProbeState) bufferProbeData(data string, contentChars int) int {
	// 喵~防御：状态为空时无法缓存，返回零让调用方按未达门槛处理喵。
	if state == nil {
		return 0
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.buffer = append(state.buffer, data)
	state.bufferedContentChars += contentChars
	return state.bufferedContentChars
}

// takeBufferSnapshot 返回当前缓存数据行的副本，供失败路径随错误交给调用方喵。
// 返回副本而不是内部切片，避免调用方在 scanner goroutine 继续追加缓存时读到中间状态喵。
func (state *streamProbeState) takeBufferSnapshot() []string {
	// 喵~防御：状态为空时没有缓存，返回 nil 表示无内容可估算喵。
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	// 喵~防御：空缓存直接返回 nil，避免调用方拿到空切片后误以为有内容喵。
	if len(state.buffer) == 0 {
		return nil
	}
	return append([]string(nil), state.buffer...)
}

// drainBuffer 取走全部缓存数据行并清空缓存，供放流前一次性重放喵。
//
// 输入：无喵。
// 输出：已缓存的数据行；取走后内部缓存置空，避免放流后继续占用内存喵。
func (state *streamProbeState) drainBuffer() []string {
	// 喵~防御：状态为空时没有可重放的缓存喵。
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	bufferedData := state.buffer
	state.buffer = nil
	return bufferedData
}

// probeBufferedDataError 把「探测失败前缓存但从未放流」的上游数据行随错误一起带给调用方喵。
//
// 整体思路喵：虚拟模型候选在放流前被判定卡流/空流/断流而跳过时，原生 new-api 这类上游消耗是会计费的，
// 但探测缓存只存在于 scanner 内部、放流前不会交给 dataHandler，调用方拿不到任何响应内容。
// 因此这里用包装错误携带缓存行，让调用方能按原生口径估算该候选已产出的 usage 并结算喵。
//
// 输入：cause 为原始探测错误（内含卡流/断流哨兵），bufferedData 为失败前缓存的 SSE data 行喵。
// 输出：实现 error 的包装对象；Error() 与原始错误保持一致，Unwrap() 透传哨兵供失败规则分类喵。
type probeBufferedDataError struct {
	cause        error    // 原始探测错误，保留卡流/断流哨兵喵。
	bufferedData []string // 放流前缓存的上游 SSE data 行，已剥掉 data: 前缀喵。
}

// Error 返回原始错误文案，保证日志与失败规则看到的仍是卡流/断流描述喵。
func (e *probeBufferedDataError) Error() string {
	// 喵~防御：包装对象为空或缺少原因时返回空串，避免空指针喵。
	if e == nil || e.cause == nil {
		return ""
	}
	return e.cause.Error()
}

// Unwrap 透传原始探测错误，使 errors.Is/errors.As 仍能识别卡流与断流哨兵喵。
func (e *probeBufferedDataError) Unwrap() error {
	// 喵~防御：包装对象为空时无法解包，返回 nil 让 errors.Is 安全失败喵。
	if e == nil {
		return nil
	}
	return e.cause
}

// BufferedProbeDataFromError 从探测失败错误里取出放流前缓存的上游数据行喵。
//
// 输入：任意错误，允许是 NewAPIError 等多层包装（通过 Unwrap 链穿透）喵。
// 输出：缓存的数据行副本；该错误不含探测缓存时返回 nil，调用方据此退化为「没有内容可估算」喵。
func BufferedProbeDataFromError(err error) []string {
	// 喵~防御：空错误没有任何缓存内容喵。
	if err == nil {
		return nil
	}
	var probeError *probeBufferedDataError
	// 喵~防御：不是探测缓存错误时返回 nil，普通失败不得被误当成「有上游内容」喵。
	if !errors.As(err, &probeError) {
		return nil
	}
	return probeError.bufferedData
}

// wrapProbeErrorWithBufferedData 用缓存快照包装探测失败原因，供跳过候选时按原生口径结算计费喵。
func wrapProbeErrorWithBufferedData(probeError error, bufferedData []string) error {
	// 喵~防御：没有失败原因时不包装，避免造出空错误喵。
	if probeError == nil {
		return nil
	}
	return &probeBufferedDataError{cause: probeError, bufferedData: bufferedData}
}

// wrapWithBufferedData 用当前缓存快照包装探测失败原因喵。
// 主循环自行判定失败的场合（静默超时、总预算耗尽）需要先包装再交出去，供跳过候选时按原生口径结算喵。
func (state *streamProbeState) wrapWithBufferedData(probeError error) error {
	// 喵~防御：状态为空时没有缓存可附，原样返回失败原因避免丢失错误喵。
	if state == nil {
		return probeError
	}
	return wrapProbeErrorWithBufferedData(probeError, state.takeBufferSnapshot())
}

// fail 非阻塞记录一次探测失败，并自动附上放流前已缓存的数据行喵。
func (state *streamProbeState) fail(probeError error) {
	// 喵~防御：状态为空或没有失败原因时无法投递失败信号喵。
	if state == nil || probeError == nil {
		return
	}
	select {
	case state.failedChan <- state.wrapWithBufferedData(probeError):
	default:
	}
}

// isProbeHeartbeat 判断 SSE 数据是否仅为心跳事件，心跳不构成业务内容喵。
func isProbeHeartbeat(data string) bool {
	return strings.EqualFold(data, "ping") || strings.EqualFold(data, "pong")
}
