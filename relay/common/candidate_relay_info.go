package common

// ---------------------------------------------------------------------------
// 候选级 RelayInfo 隔离
//
// 一次 virtual/<name> 请求会按候选链依次尝试多个候选喵。旧实现把同一个 RelayInfo
// 清空几个字段后反复复用，导致 Channel、定价、计费、重试与响应状态可能跨候选残留喵。
// 本文件提供三件事喵：
//  1. CandidateRelayBaseline：从入口 RelayInfo 抽取「跨候选共享且与候选无关」的请求级基线喵。
//  2. NewCandidateRelayInfo：为当前候选用原生工厂重新创建 RelayInfo，只覆盖基线白名单字段喵。
//  3. AssertCandidateRelayInfoIsolated：断言新候选没有携带任何上一候选的可变状态喵。
//
// ---------------------------------------------------------------------------

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// maximumCandidateRequestIDLength 是候选级幂等请求标识允许的最大字符数喵。
// 该上限与 logs.request_id 以及订阅预扣记录 request_id 的 varchar(64) 列宽一致；
// 超长会在订阅唯一索引上被静默截断甚至互相冲突，所以必须在拼接阶段就拒绝喵。
const maximumCandidateRequestIDLength = 64

// maximumCandidateAttemptIDLength 是候选尝试标识允许的最大字符数，单位：字符喵。
const maximumCandidateAttemptIDLength = 32

// candidateRequestIDSeparator 分隔请求级基线标识与候选尝试标识喵。
const candidateRequestIDSeparator = ":"

// candidateForbiddenGroupName 是虚拟模型候选禁止使用的自动分组名称喵。
// 候选必须固定分组，进入 auto 分支会与 Token AutoRoutes 的原生语义互相污染喵。
const candidateForbiddenGroupName = "auto"

// CandidateRelayBaseline 保存一次虚拟模型请求里所有候选共享、且与具体候选无关的字段喵。
// 该结构只允许保存请求级输入；禁止保存 Channel、计费、定价、重试或响应状态，
// 否则它自己就会变成第二个跨候选共享的可变容器喵。
type CandidateRelayBaseline struct {
	BaseRequestID        string                    // 请求级唯一标识，用于派生候选幂等 RequestId 喵。
	RelayFormat          types.RelayFormat         // 客户端协议格式，候选切换不得改变它喵。
	EstimatePromptTokens int                       // 入口估算的 prompt token 数量，单位：个喵。
	BillingRequestInput  *billingexpr.RequestInput // 非 JSON 请求的计费表达式输入，JSON 请求为空喵。
	ForcePreConsume      bool                      // 是否强制全额预扣（异步任务用），虚拟模型通常为 false 喵。
	IsChannelTest        bool                      // 是否为渠道测试请求，虚拟模型通常为 false 喵。
}

// NewCandidateRelayBaseline 从入口 RelayInfo 抽取候选共享基线喵。
// 只读取请求级字段，任何候选级状态都不会被抽取，保证基线本身是不可变的纯输入喵。
func NewCandidateRelayBaseline(info *RelayInfo) (*CandidateRelayBaseline, error) {
	// 喵~防御：空 RelayInfo 无法提供请求基线，直接报错而不是构造半空快照喵。
	if info == nil {
		return nil, errors.New("candidate relay baseline requires a request relay info")
	}
	// 去掉首尾空白，避免拼接出带空格的幂等键喵。
	baseRequestID := strings.TrimSpace(info.RequestId)
	// 喵~防御：缺少请求标识时无法派生候选幂等键，必须拒绝以避免订阅预扣互相覆盖喵。
	if baseRequestID == "" {
		return nil, errors.New("candidate relay baseline requires a non-empty request id")
	}
	// 喵~防御：缺少协议格式时无法为候选重建 RelayInfo，直接拒绝喵。
	if info.RelayFormat == "" {
		return nil, errors.New("candidate relay baseline requires a relay format")
	}
	return &CandidateRelayBaseline{
		BaseRequestID:        baseRequestID,
		RelayFormat:          info.RelayFormat,
		EstimatePromptTokens: info.GetEstimatePromptTokens(),
		BillingRequestInput:  info.BillingRequestInput,
		ForcePreConsume:      info.ForcePreConsume,
		IsChannelTest:        info.IsChannelTest,
	}, nil
}

// CandidateRelayIdentity 描述当前候选的身份、路由目标与关联标识喵。
type CandidateRelayIdentity struct {
	CandidateID        int    // 候选编号，用于冻结、失败规则与审计关联喵。
	CandidateAttemptID string // 候选尝试唯一标识，用于计费幂等键与结构化日志喵。
	RealModelName      string // 候选真实上游模型名称喵。
	GroupName          string // 候选固定分组名称，禁止使用 auto 喵。
}

// CandidateRequestID 把请求级标识与候选尝试标识拼成候选专属的幂等请求标识喵。
// 这样订阅预扣、退款与结算都落在各自候选的唯一键上，不会互相覆盖或误判为重复请求喵。
func CandidateRequestID(baseRequestID string, candidateAttemptID string) (string, error) {
	// 去掉两端空白，保证拼接结果稳定可比对喵。
	trimmedBaseRequestID := strings.TrimSpace(baseRequestID)
	trimmedCandidateAttemptID := strings.TrimSpace(candidateAttemptID)
	// 喵~防御：任一段为空都无法保证候选幂等键唯一，必须拒绝喵。
	if trimmedBaseRequestID == "" || trimmedCandidateAttemptID == "" {
		return "", errors.New("candidate request id requires both a base request id and a candidate attempt id")
	}
	// 喵~防御：尝试标识只允许受限字符集，避免把凭据、URL 或换行注入幂等键与日志喵。
	if validationError := validateCandidateAttemptID(trimmedCandidateAttemptID); validationError != nil {
		return "", validationError
	}
	// 用固定分隔符拼接，便于日志按前缀检索同一请求下的全部候选尝试喵。
	candidateRequestID := trimmedBaseRequestID + candidateRequestIDSeparator + trimmedCandidateAttemptID
	// 喵~防御：超过数据库列宽的幂等键会被静默截断并可能撞唯一索引，必须提前失败喵。
	if len(candidateRequestID) > maximumCandidateRequestIDLength {
		return "", fmt.Errorf("candidate request id length %d exceeds the %d character limit", len(candidateRequestID), maximumCandidateRequestIDLength)
	}
	return candidateRequestID, nil
}

// validateCandidateAttemptID 校验候选尝试标识的长度与字符集喵。
func validateCandidateAttemptID(candidateAttemptID string) error {
	// 喵~防御：空标识无法区分候选尝试，直接拒绝喵。
	if candidateAttemptID == "" {
		return errors.New("candidate attempt id must not be empty")
	}
	// 喵~防御：过长标识会挤占请求标识预算并可能被数据库截断，直接拒绝喵。
	if len(candidateAttemptID) > maximumCandidateAttemptIDLength {
		return fmt.Errorf("candidate attempt id length %d exceeds the %d character limit", len(candidateAttemptID), maximumCandidateAttemptIDLength)
	}
	for _, character := range candidateAttemptID {
		// 判断当前字符是否为小写字母喵。
		isLowerCaseLetter := character >= 'a' && character <= 'z'
		// 判断当前字符是否为大写字母喵。
		isUpperCaseLetter := character >= 'A' && character <= 'Z'
		// 判断当前字符是否为数字喵。
		isDigit := character >= '0' && character <= '9'
		// 喵~防御：只放行字母、数字、下划线与连字符，其余字符一律拒绝喵。
		if !isLowerCaseLetter && !isUpperCaseLetter && !isDigit && character != '_' && character != '-' {
			return errors.New("candidate attempt id contains an unsupported character")
		}
	}
	return nil
}

// NewCandidateRelayInfo 为当前候选创建与其他候选完全隔离的 RelayInfo 喵。
//
// 整体思路喵：
//  1. 先校验候选身份，并确认 Gin 上下文里的模型与分组已经切换到该候选，
//     避免中间件尚未推进候选时就为错误的候选建立计费；
//  2. 再调用原生 GenRelayInfo 从零构造 RelayInfo，由它按协议格式补齐
//     ClaudeConvertInfo、ResponsesUsageInfo 等子结构，避免手写复制时遗漏；
//  3. 最后只把基线白名单字段覆盖回去，并断言 Channel、计费、定价、重试与响应
//     状态全部处于初始值，从结构上保证上一个候选的状态不会泄漏过来喵。
//
// 输入：候选所属 Gin 上下文、请求级基线、候选身份、按候选真实模型重新解析出的请求对象喵。
// 输出：候选专属 RelayInfo；任何隔离前提不满足时返回错误，绝不返回半成品喵。
// 边界条件：拒绝 WebSocket 与任务类协议，它们没有可安全重放的 JSON 请求体喵。
func NewCandidateRelayInfo(c *gin.Context, baseline *CandidateRelayBaseline, identity CandidateRelayIdentity, request dto.Request) (*RelayInfo, error) {
	// 喵~防御：缺少 Gin 上下文或原始 HTTP 请求时无法读取候选路由，直接拒绝喵。
	if c == nil || c.Request == nil {
		return nil, errors.New("candidate relay info requires an active gin context")
	}
	// 喵~防御：缺少请求基线时会丢失 token 估算与计费输入，拒绝构造喵。
	if baseline == nil {
		return nil, errors.New("candidate relay info requires a request baseline")
	}
	// 喵~防御：请求对象为空会让原生工厂拿不到协议信息，拒绝构造喵。
	if request == nil {
		return nil, errors.New("candidate relay info requires a re-parsed candidate request")
	}
	// 校验候选身份字段齐全且分组不是 auto 喵。
	if identityError := validateCandidateRelayIdentity(identity); identityError != nil {
		return nil, identityError
	}
	// 喵~防御：WebSocket 与任务类协议无法重放 JSON 请求体，不允许进入候选切换路径喵。
	if !candidateRelayFormatSupported(baseline.RelayFormat) {
		return nil, fmt.Errorf("relay format %s does not support virtual model candidate isolation", baseline.RelayFormat)
	}
	// 校验上下文已经切换到目标候选，避免把费用记在上一个候选的模型或分组上喵。
	if contextError := assertCandidateContextMatchesIdentity(c, identity); contextError != nil {
		return nil, contextError
	}
	// 先算出候选幂等标识，失败时不会产生任何副作用喵。
	candidateRequestID, requestIDError := CandidateRequestID(baseline.BaseRequestID, identity.CandidateAttemptID)
	if requestIDError != nil {
		return nil, requestIDError
	}
	// 由原生工厂重新创建 RelayInfo，确保协议子结构齐备且候选级字段都是初始值喵。
	info, generateError := GenRelayInfo(c, baseline.RelayFormat, request, nil)
	if generateError != nil {
		return nil, generateError
	}
	// 喵~防御：工厂返回空指针时不得继续覆盖字段，避免空指针解引用喵。
	if info == nil {
		return nil, errors.New("candidate relay info factory returned an empty relay info")
	}
	// 固定当前候选的真实模型，禁止沿用上一个候选的模型名喵。
	info.OriginModelName = strings.TrimSpace(identity.RealModelName)
	// 固定当前候选的分组，令牌分组与使用分组必须一致以避免进入 auto 跨组重试喵。
	info.TokenGroup = strings.TrimSpace(identity.GroupName)
	info.UsingGroup = strings.TrimSpace(identity.GroupName)
	// 覆盖候选幂等标识，使预扣、退款与结算记录都落在本次候选尝试上喵。
	info.RequestId = candidateRequestID
	// 回填请求级基线：这些字段在入口只计算一次，候选切换不应重新推导喵。
	info.SetEstimatePromptTokens(baseline.EstimatePromptTokens)
	info.BillingRequestInput = baseline.BillingRequestInput
	info.ForcePreConsume = baseline.ForcePreConsume
	info.IsChannelTest = baseline.IsChannelTest
	// 最后做结构性断言，任何残留的候选级状态都会在这里被拦下喵。
	if isolationError := AssertCandidateRelayInfoIsolated(info); isolationError != nil {
		return nil, isolationError
	}
	return info, nil
}

// validateCandidateRelayIdentity 校验候选身份字段齐全，且分组是固定的非 auto 分组喵。
func validateCandidateRelayIdentity(identity CandidateRelayIdentity) error {
	// 喵~防御：候选编号必须为正，零值说明调用方其实还在普通 relay 路径上喵。
	if identity.CandidateID <= 0 {
		return errors.New("candidate relay identity requires a positive candidate id")
	}
	// 喵~防御：尝试标识缺失或含非法字符时无法隔离幂等键与日志，直接拒绝喵。
	if attemptIDError := validateCandidateAttemptID(strings.TrimSpace(identity.CandidateAttemptID)); attemptIDError != nil {
		return attemptIDError
	}
	// 喵~防御：真实模型为空会让定价与 Channel 选择退化到上一个候选，必须拒绝喵。
	if strings.TrimSpace(identity.RealModelName) == "" {
		return errors.New("candidate relay identity requires a real model name")
	}
	// 去掉两端空白后再判断分组，避免" auto "这类输入绕过检查喵。
	groupName := strings.TrimSpace(identity.GroupName)
	// 喵~防御：分组为空或为 auto 时会进入 Token AutoRoutes 的自动分支，破坏候选边界喵。
	if groupName == "" || groupName == candidateForbiddenGroupName {
		return errors.New("candidate relay identity requires a fixed non-auto group name")
	}
	return nil
}

// candidateRelayFormatSupported 判断协议格式是否支持候选级隔离喵。
func candidateRelayFormatSupported(relayFormat types.RelayFormat) bool {
	switch relayFormat {
	// 喵~防御：空格式无法映射到任何原生工厂，直接拒绝喵。
	case "":
		return false
	// WebSocket 与任务类协议无法用同一份 JSON 请求体重放候选，明确排除喵。
	case types.RelayFormatOpenAIRealtime, types.RelayFormatUnrealSpeechWebSocket, types.RelayFormatTask, types.RelayFormatMjProxy:
		return false
	default:
		return true
	}
}

// assertCandidateContextMatchesIdentity 校验 Gin 上下文已经切换到目标候选喵。
// 若上下文还停留在上一个候选，说明候选推进顺序被破坏，必须在建立计费之前失败喵。
func assertCandidateContextMatchesIdentity(c *gin.Context, identity CandidateRelayIdentity) error {
	// 读取中间件写入的当前候选真实模型喵。
	contextModelName := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)
	// 喵~防御：上下文模型与候选不一致时继续执行会把费用记到错误的模型上喵。
	if contextModelName != strings.TrimSpace(identity.RealModelName) {
		return errors.New("candidate relay context model does not match the candidate identity")
	}
	// 候选分组要求令牌分组与使用分组同时等于候选固定分组喵。
	expectedGroupName := strings.TrimSpace(identity.GroupName)
	contextTokenGroup := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
	contextUsingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	// 喵~防御：分组不一致会让 Channel 选择与分组倍率错位，必须拒绝喵。
	if contextTokenGroup != expectedGroupName || contextUsingGroup != expectedGroupName {
		return errors.New("candidate relay context group does not match the candidate identity")
	}
	return nil
}

// AssertCandidateRelayInfoIsolated 断言候选级 RelayInfo 未携带上一候选的任何可变状态喵。
// 这是候选隔离的结构性护栏：如果有人改回「清空几个字段后复用同一个 RelayInfo」，
// 这里会立刻返回明确的违规原因，而不是让错误的计费语义悄悄上线喵。
func AssertCandidateRelayInfoIsolated(info *RelayInfo) error {
	// 喵~防御：空指针无法断言隔离性，视为违规喵。
	if info == nil {
		return errors.New("candidate relay info must not be nil")
	}
	// 逐项列出禁止跨候选复制的字段，命中任意一项都返回明确的违规原因喵。
	switch {
	case info.ChannelMeta != nil:
		return errors.New("candidate relay info must not carry channel meta from a previous candidate")
	case info.TaskRelayInfo != nil:
		return errors.New("candidate relay info must not carry task relay info from a previous candidate")
	case info.Billing != nil:
		return errors.New("candidate relay info must not carry a billing session from a previous candidate")
	case info.FinalPreConsumedQuota != 0:
		return errors.New("candidate relay info must not carry a pre-consumed quota from a previous candidate")
	case info.BillingSource != "":
		return errors.New("candidate relay info must not carry a billing source from a previous candidate")
	case info.SubscriptionId != 0 || info.SubscriptionPreConsumed != 0 || info.SubscriptionPostDelta != 0:
		return errors.New("candidate relay info must not carry subscription reservation state from a previous candidate")
	case !candidatePriceDataIsZero(info.PriceData):
		return errors.New("candidate relay info must not carry price data from a previous candidate")
	case info.TieredBillingSnapshot != nil:
		return errors.New("candidate relay info must not carry a tiered billing snapshot from a previous candidate")
	case info.QuotaClamp != nil:
		return errors.New("candidate relay info must not carry a quota clamp from a previous candidate")
	case info.RetryIndex != 0:
		return errors.New("candidate relay info must not carry a retry index from a previous candidate")
	case info.LastError != nil:
		return errors.New("candidate relay info must not carry the last error from a previous candidate")
	case info.SendResponseCount != 0 || info.ReceivedResponseCount != 0:
		return errors.New("candidate relay info must not carry response counters from a previous candidate")
	case info.StreamStatus != nil:
		return errors.New("candidate relay info must not carry stream status from a previous candidate")
	case info.convOptions != nil:
		return errors.New("candidate relay info must not carry a converter options cache from a previous candidate")
	case info.UseRuntimeHeadersOverride || len(info.RuntimeHeadersOverride) != 0 || len(info.ParamOverrideAudit) != 0:
		return errors.New("candidate relay info must not carry channel override state from a previous candidate")
	case info.HasSendResponse():
		return errors.New("candidate relay info must not report an already sent response")
	}
	return nil
}

// candidatePriceDataIsZero 判断定价结果是否仍处于「尚未为本候选计算」的初始状态喵。
// PriceData 内含 map 字段因此不能直接用 == 比较，这里逐项检查可观测字段喵。
func candidatePriceDataIsZero(priceData hosttypes.PriceData) bool {
	// 免费模型标记与按次计费标记都说明定价已经算过了喵。
	if priceData.FreeModel || priceData.UsePrice {
		return false
	}
	// 固定价格与主要倍率非零说明沿用了上一个候选的定价喵。
	if priceData.ModelPrice != 0 || priceData.ModelRatio != 0 || priceData.CompletionRatio != 0 {
		return false
	}
	// 缓存、图像与音频倍率同样属于候选级定价结果喵。
	if priceData.CacheRatio != 0 || priceData.CacheCreationRatio != 0 || priceData.CacheCreation5mRatio != 0 || priceData.CacheCreation1hRatio != 0 {
		return false
	}
	if priceData.ImageRatio != 0 || priceData.AudioRatio != 0 || priceData.AudioCompletionRatio != 0 {
		return false
	}
	// 额度字段非零说明预扣或按次额度已经算过了喵。
	if priceData.Quota != 0 || priceData.QuotaToPreConsume != 0 {
		return false
	}
	// 分组倍率信息是候选分组的函数，必须为零值喵。
	if priceData.GroupRatioInfo != (hosttypes.GroupRatioInfo{}) {
		return false
	}
	// 其他倍率映射为空才算完全未定价喵。
	return len(priceData.OtherRatios()) == 0
}
