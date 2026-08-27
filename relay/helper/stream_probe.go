package helper

import (
	"fmt"
	"strings"
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
	config               *StreamProbeConfig // 探测参数喵。
	passed               bool               // 是否已放流（探测成功）喵。
	buffer               []string           // 探测阶段缓存的 SSE data 行喵。
	bufferedContentChars int                // 累积内容字符数喵。
	failedChan           chan error         // 探测失败错误通道，带缓冲防阻塞喵。
}

// fail 非阻塞记录一次探测失败，供主循环收集喵。
func (state *streamProbeState) fail(probeError error) {
	select {
	case state.failedChan <- probeError:
	default:
	}
}

// isProbeHeartbeat 判断 SSE 数据是否仅为心跳事件，心跳不构成业务内容喵。
func isProbeHeartbeat(data string) bool {
	return strings.EqualFold(data, "ping") || strings.EqualFold(data, "pong")
}
