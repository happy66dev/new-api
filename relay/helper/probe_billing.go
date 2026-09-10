package helper

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// maxSkippedCandidateTextBytes 限制跳过候选文本还原的最大字节数，防御异常上游的巨量缓存拖慢估算喵。
// 与探测缓存上限（2MB）同量级，超出部分丢弃：估算只需要量级正确的文本，不需要逐字精确喵。
const maxSkippedCandidateTextBytes = 2 * 1024 * 1024

// RegisterSkippedCandidateStreamBilling 在流式探测失败的候选被跳过前，按原生 new-api 口径登记它的计费额度喵。
//
// 整体思路喵：探测阶段缓存的上游数据行从未放流给客户端，但这段上游消耗在原生 new-api 里是会计费的
// （原生会对以 timeout / client_gone / scanner_error 结束的流照常结算）。因此这里把缓存的数据行
// 还原成响应文本，再用与原生兜底完全相同的 `service.ResponseText2Usage` 估算 usage，
// 连同请求侧的 prompt 估算一起交给 service 登记，等候选进入终态失败时统一结算喵。
//
// 输入：Gin 上下文、候选自己的 relay、探测失败返回的错误（内部携带放流前缓存的数据行）喵。
// 输出：无；非虚拟模型请求或没有缓存内容时退化为只登记 prompt 侧估算，保证与原生「至少计 prompt」一致喵。
func RegisterSkippedCandidateStreamBilling(c *gin.Context, info *relaycommon.RelayInfo, probeErr error) {
	// 喵~防御：缺少上下文或候选 relay 时无法估算，直接跳过登记喵。
	if c == nil || info == nil {
		return
	}
	// 把探测缓存的数据行逐条还原成响应文本；未命中已知格式的事件返回空串，不虚增文本喵。
	var responseTextBuilder strings.Builder
	for _, bufferedData := range BufferedProbeDataFromError(probeErr) {
		// 喵~防御：文本累积超过上限后停止追加，避免异常上游的巨量缓存拖慢本次估算喵。
		if responseTextBuilder.Len() >= maxSkippedCandidateTextBytes {
			break
		}
		responseTextBuilder.WriteString(common.StreamProbeContentText(bufferedData))
	}
	// 与原生兜底同一个函数：无文本时 completion 为 0，只用请求侧 prompt 估算参与计费喵。
	usage := service.ResponseText2Usage(c, responseTextBuilder.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
	service.RegisterSkippedCandidateBilling(c, info, usage)
}
