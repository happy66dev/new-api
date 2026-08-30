package helper

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"

	"github.com/gin-gonic/gin"
)

const (
	InitialScannerBufferSize    = 64 << 10  // 64KB (64*1024)
	DefaultMaxScannerBufferSize = 128 << 20 // 64MB (64*1024*1024) default SSE buffer size
	DefaultPingInterval         = 10 * time.Second
	// streamWriteTimeout bounds a single blocked write to a slow client so the
	// unconditional wg.Wait() in cleanup can always finish. Without it, a slow
	// but connected client (full TCP buffer, no server WriteTimeout) could hang
	// the handler forever.
	streamWriteTimeout = 30 * time.Second
	// probeBufferLimit 探测/伪流阶段缓存的数据行字节上限，与自定义上游 precommit 缓冲保持一致（2MB）喵。
	probeBufferLimit = 2 * 1024 * 1024
)

// errProbeBufferLimitExceeded 标记探测/伪流阶段缓存超限，防止异常上游无限发送数据行耗尽服务内存喵。
var errProbeBufferLimitExceeded = errors.New("probe buffer limit exceeded")

// StreamProbeEnabledFromContext 判断当前请求是否启用了流式健康探测或流转伪流喵。
// 探测/伪流阶段禁止 doRequest 级心跳，避免提前向客户端写字节导致探测失败无法回切候选喵。
func StreamProbeEnabledFromContext(c *gin.Context) bool {
	// 喵~防御：缺少上下文时按未启用处理喵。
	if c == nil {
		return false
	}
	// 流转伪流开关开启即视为启用探测喵。
	if common.GetContextKeyBool(c, constant.ContextKeyVirtualModelFakeStream) {
		return true
	}
	// 探测参数存在且有效（streamProbeConfigFromContext 内部完成参数校验）才认为启用探测喵。
	return streamProbeConfigFromContext(c) != nil
}

func getScannerBufferSize() int {
	if constant.StreamScannerMaxBufferMB > 0 {
		return constant.StreamScannerMaxBufferMB << 20
	}
	return DefaultMaxScannerBufferSize
}

func NewStreamScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, InitialScannerBufferSize), getScannerBufferSize())
	return scanner
}

func copyCodexSSEHeaders(c *gin.Context, resp *http.Response) {
	if c == nil || c.Writer == nil || resp == nil {
		return
	}
	// codex
	for _, name := range []string{"X-Reasoning-Included", "X-Codex-Turn-State"} {
		values := resp.Header.Values(name)
		if !service.ShouldCopyUpstreamHeader(c, name, values) {
			continue
		}
		for _, value := range values {
			if value != "" {
				c.Writer.Header().Add(name, value)
			}
		}
	}
}

// ExtendWriteDeadline pushes the connection write deadline forward before each
// stream write. Best-effort: writers that don't support deadlines (e.g.
// httptest recorders) are silently ignored.
func ExtendWriteDeadline(c *gin.Context) {
	if c == nil || c.Writer == nil {
		return
	}
	_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(streamWriteTimeout))
}

// StreamScannerHandler 处理上游 SSE 流并转发给客户端喵。
// 虚拟模型内部候选开启探测模式时，先缓存内容字符达到门槛才放流；探测失败返回带卡流哨兵的错误喵。
func StreamScannerHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, dataHandler func(data string, sr *StreamResult)) *types.NewAPIError {

	if resp == nil || dataHandler == nil {
		return nil
	}

	// 无条件新建 StreamStatus
	info.StreamStatus = relaycommon.NewStreamStatus()

	ctx, cancel := context.WithCancel(context.Background())

	streamingTimeout := time.Duration(constant.StreamingTimeout) * time.Second

	// 虚拟模型内部候选的流式探测参数：仅内部候选开启，普通请求为 nil 不启用探测喵。
	probeConfig := streamProbeConfigFromContext(c)
	// 流转伪流：从 context 读取开关，开启后全量缓存到 [DONE] 再一次性流式回放喵。
	fakeStreamEnabled := common.GetContextKeyBool(c, constant.ContextKeyVirtualModelFakeStream)

	var (
		stopChan    = make(chan bool, 3) // 增加缓冲区避免阻塞
		scanner     = NewStreamScanner(resp.Body)
		ticker      = time.NewTicker(streamingTimeout)
		pingTicker  *time.Ticker
		writeMutex  sync.Mutex     // Mutex to protect concurrent writes
		wg          sync.WaitGroup // 用于等待所有 goroutine 退出
		cleanupOnce sync.Once
		stopOnce    sync.Once
	)
	// 探测运行状态、失败信号通道与探测总预算计时器，仅探测模式使用喵。
	var probeState *streamProbeState
	var probeFailedChan chan error
	var probeTotalChan <-chan time.Time
	// probePassed 用原子布尔替代裸 bool 传输放流信号，避免 scanner goroutine 与主循环之间无 happens-before 的数据竞争喵。
	// 未启用探测时该标志恒为 false，配合 probeState == nil 短路不会误入探测分支喵。
	var probePassed atomic.Bool
	// probeBufferBytes 探测阶段已缓存数据行的累计字节数，用于伪流全量缓存上限控制喵。
	var probeBufferBytes int
	if probeConfig != nil {
		probeFailedChan = make(chan error, 1)
		probeState = &streamProbeState{config: probeConfig, failedChan: probeFailedChan}
		probeTotalTimer := time.NewTimer(probeConfig.ProbeTotalTimeout)
		defer probeTotalTimer.Stop()
		probeTotalChan = probeTotalTimer.C
	}

	stop := func() {
		stopOnce.Do(func() {
			close(stopChan)
		})
	}

	generalSettings := operation_setting.GetGeneralSetting()
	pingEnabled := generalSettings.PingIntervalEnabled && !info.DisablePing
	// 探测阶段禁止发送心跳，避免提前向客户端写入字节破坏探测失败回切候选喵。
	// 流转伪流模式同样禁止心跳：客户端连接的是流式但体验是非流，提前写字节会破坏一次性回放喵。
	if probeConfig != nil || fakeStreamEnabled {
		pingEnabled = false
	}
	pingInterval := time.Duration(generalSettings.PingIntervalSeconds) * time.Second
	if pingInterval <= 0 {
		pingInterval = DefaultPingInterval
	}

	if pingEnabled {
		pingTicker = time.NewTicker(pingInterval)
	}

	logger.LogDebug(c, "relay timeout seconds: %d", common.RelayTimeout)
	logger.LogDebug(c, "relay max idle conns: %d", common.RelayMaxIdleConns)
	logger.LogDebug(c, "relay max idle conns per host: %d", common.RelayMaxIdleConnsPerHost)
	logger.LogDebug(c, "streaming timeout seconds: %d", int64(streamingTimeout.Seconds()))
	logger.LogDebug(c, "ping interval seconds: %d", int64(pingInterval.Seconds()))

	cleanup := func() {
		cleanupOnce.Do(func() {
			cancel()
			stop()
			if resp.Body != nil {
				_ = resp.Body.Close()
			}

			ticker.Stop()
			if pingTicker != nil {
				pingTicker.Stop()
			}

			wg.Wait()
		})
	}
	// Ensure gin.Context is not returned to Gin's pool while any stream goroutine can still use it.
	defer cleanup()

	scanner.Split(bufio.ScanLines)
	copyCodexSSEHeaders(c, resp)
	SetEventStreamHeaders(c)

	ctx = context.WithValue(ctx, "stop_chan", stopChan)

	// Handle ping data sending with improved error handling
	if pingEnabled && pingTicker != nil {
		wg.Add(1)
		gopool.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					logger.LogError(c, fmt.Sprintf("ping goroutine panic: %v", r))
					info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPanic, fmt.Errorf("ping panic: %v", r))
					stop()
				}
				logger.LogDebug(c, "ping goroutine exited")
				wg.Done()
			}()

			// 添加超时保护，防止 goroutine 无限运行
			maxPingDuration := 30 * time.Minute // 最大 ping 持续时间
			pingTimeout := time.NewTimer(maxPingDuration)
			defer pingTimeout.Stop()

			for {
				select {
				case <-pingTicker.C:
					var err error
					func() {
						writeMutex.Lock()
						defer writeMutex.Unlock()
						ExtendWriteDeadline(c)
						err = PingData(c)
					}()
					if err != nil {
						logger.LogError(c, "ping data error: "+err.Error())
						info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPingFail, err)
						return
					}
					logger.LogDebug(c, "ping data sent")
				case <-ctx.Done():
					return
				case <-stopChan:
					return
				case <-c.Request.Context().Done():
					// 监听客户端断开连接
					return
				case <-pingTimeout.C:
					logger.LogError(c, "ping goroutine max duration reached")
					return
				}
			}
		})
	}

	dataChan := make(chan string, 10)

	wg.Add(1)
	gopool.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				logger.LogError(c, fmt.Sprintf("data handler goroutine panic: %v", r))
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPanic, fmt.Errorf("handler panic: %v", r))
			}
			stop()
			wg.Done()
		}()
		sr := newStreamResult(info.StreamStatus)
		for data := range dataChan {
			sr.reset()
			func() {
				writeMutex.Lock()
				defer writeMutex.Unlock()
				ExtendWriteDeadline(c)
				dataHandler(data, sr)
			}()
			if sr.IsStopped() {
				return
			}
		}
	})

	// Scanner goroutine with improved error handling
	wg.Add(1)
	common.RelayCtxGo(ctx, func() {
		defer func() {
			close(dataChan)
			if r := recover(); r != nil {
				logger.LogError(c, fmt.Sprintf("scanner goroutine panic: %v", r))
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPanic, fmt.Errorf("scanner panic: %v", r))
			}
			stop()
			logger.LogDebug(c, "scanner goroutine exited")
			wg.Done()
		}()

		for scanner.Scan() {
			// 检查是否需要停止
			select {
			case <-stopChan:
				return
			case <-ctx.Done():
				return
			default:
			}

			// 探测阶段用静默超时计时，放流后恢复普通流式空闲超时喵。
			if probeState != nil && !probePassed.Load() {
				ticker.Reset(probeState.config.StallTimeout)
			} else {
				ticker.Reset(streamingTimeout)
			}
			data := scanner.Text()
			logger.LogDebug(c, "stream scanner data: %s", data)

			if len(data) < 6 {
				continue
			}
			if data[:5] != "data:" && data[:6] != "[DONE]" {
				continue
			}
			data = data[5:]
			data = strings.TrimSpace(data)
			if data == "" {
				continue
			}
			if !strings.HasPrefix(data, "[DONE]") {
				// 探测阶段：先缓存数据直到内容字符达到门槛，避免"假成功流"喵。
				if probeState != nil && !probePassed.Load() {
					probeState.buffer = append(probeState.buffer, data)
					// 累计缓存字节数，伪流/探测模式都受上限保护，避免异常上游耗尽服务内存喵。
					probeBufferBytes += len(data)
					// 主人注意：伪流/探测全量缓存若无上限，恶意或异常上游可持续发送数据行导致内存无界增长喵。
					// 超限即终止该候选：普通模式按卡流、伪流模式按断流分类，供虚拟模型失败规则编排喵。
					if probeBufferBytes > probeBufferLimit {
						bufferOverflowError := fmt.Errorf("%w: %s", types.ErrStreamCut, errProbeBufferLimitExceeded)
						if !fakeStreamEnabled {
							bufferOverflowError = fmt.Errorf("%w: %s", types.ErrStalledStream, errProbeBufferLimitExceeded)
						}
						probeState.fail(bufferOverflowError)
						info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, bufferOverflowError)
						return
					}
					// 流转伪流：全量缓存所有 data 行，不做内容门槛放流，等 [DONE] 后一次性回放喵。
					if fakeStreamEnabled {
						continue
					}
					// 心跳不构成业务内容，只缓存重放不参与门槛计数喵。
					if !isProbeHeartbeat(data) {
						probeState.bufferedContentChars += common.StreamProbeContentChars(data)
					}
					if probeState.bufferedContentChars >= probeState.config.MinContentChars {
						// 放流：先重放已缓存数据，再继续正常边收边放喵。
						probePassed.Store(true)
						ticker.Reset(streamingTimeout)
						for _, bufferedData := range probeState.buffer {
							info.SetFirstResponseTime()
							info.ReceivedResponseCount++
							select {
							case dataChan <- bufferedData:
							case <-ctx.Done():
								return
							case <-stopChan:
								return
							}
						}
						probeState.buffer = nil
					}
					continue
				}
				info.SetFirstResponseTime()
				info.ReceivedResponseCount++

				select {
				case dataChan <- data:
				case <-ctx.Done():
					return
				case <-stopChan:
					return
				}
			} else {
				if probeState != nil && !probePassed.Load() {
					// 流转伪流：收到 [DONE] 即流完整，一次性回放全部缓存并结束喵。
					if fakeStreamEnabled {
						probePassed.Store(true)
						ticker.Reset(streamingTimeout)
						for _, bufferedData := range probeState.buffer {
							info.SetFirstResponseTime()
							info.ReceivedResponseCount++
							select {
							case dataChan <- bufferedData:
							case <-ctx.Done():
								return
							case <-stopChan:
								return
							}
						}
						probeState.buffer = nil
						// 回放 [DONE] 结束标记，客户端据此关闭流喵。
						info.ReceivedResponseCount++
						select {
						case dataChan <- "[DONE]":
						case <-ctx.Done():
							return
						case <-stopChan:
							return
						}
						info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
						logger.LogDebug(c, "received [DONE], replaying buffered fake stream")
						return
					}
					// 探测阶段收到 [DONE] 而内容不足：空流失败喵。
					probeState.fail(errProbeEndedBeforeContent)
					info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonEOF, errProbeEndedBeforeContent)
					return
				}
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
				logger.LogDebug(c, "received [DONE], stopping scanner")
				return
			}
		}

		if err := scanner.Err(); err != nil {
			if err != io.EOF {
				logger.LogError(c, "scanner error: "+err.Error())
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, err)
			}
		}
		if probeState != nil && !probePassed.Load() {
			// 探测阶段 EOF 而内容不足：普通模式按空流失败，伪流模式按断流失败喵。
			if fakeStreamEnabled {
				probeState.fail(fmt.Errorf("%w: upstream stream cut before [DONE]", types.ErrStreamCut))
			} else {
				probeState.fail(errProbeEndedBeforeContent)
			}
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonEOF, errProbeEndedBeforeContent)
			return
		}
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
	})

	// 主循环等待完成或超时喵。
	var probeFailedError error
	select {
	case <-ticker.C:
		if probeState != nil && !probePassed.Load() {
			// 探测阶段静默超时：普通模式判定卡流，伪流模式判定断流，均不向客户端写任何字节喵。
			cutError := types.ErrStalledStream
			if fakeStreamEnabled {
				cutError = types.ErrStreamCut
			}
			probeFailure := fmt.Errorf("%w: stream silent before business content", cutError)
			probeFailedError = probeFailure
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonTimeout, probeFailure)
		} else {
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonTimeout, nil)
		}
	case probeFailure := <-probeFailedChan:
		// scanner goroutine 报告的探测失败（空流、内容不足、伪流断流等）喵。
		probeFailedError = probeFailure
	case <-probeTotalChan:
		if probeState != nil && !probePassed.Load() {
			// 探测总预算耗尽：普通模式判定卡流，伪流模式判定断流喵。
			cutError := types.ErrStalledStream
			if fakeStreamEnabled {
				cutError = types.ErrStreamCut
			}
			probeFailure := fmt.Errorf("%w: probe phase exceeded total budget", cutError)
			probeFailedError = probeFailure
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonTimeout, probeFailure)
		}
	case <-stopChan:
		// scanner goroutine 触发停止后，探测失败错误从通道非阻塞取一次喵。
		select {
		case probeFailure := <-probeFailedChan:
			probeFailedError = probeFailure
		default:
		}
	case <-c.Request.Context().Done():
		// 客户端断开：立即 cleanup 关闭上游 resp.Body，解除 scanner 阻塞并让上游停止生成，
		// 避免为已放弃的请求继续消费上游 token。
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, c.Request.Context().Err())
	}

	cleanup()
	if info.StreamStatus.IsNormalEnd() && !info.StreamStatus.HasErrors() {
		logger.LogInfo(c, fmt.Sprintf("stream ended: %s", info.StreamStatus.Summary()))
	} else {
		logger.LogError(c, fmt.Sprintf("stream ended: %s, received=%d", info.StreamStatus.Summary(), info.ReceivedResponseCount))
	}
	if probeFailedError != nil {
		// 探测失败返回带卡流哨兵的错误，供虚拟模型候选链按失败规则编排喵。
		return types.NewError(probeFailedError, types.ErrorCodeVirtualModelProbeFailed, types.ErrOptionWithStatusCode(http.StatusBadGateway))
	}
	return nil
}
