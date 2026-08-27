package virtualmodel

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/model"
	relaykitypes "github.com/QuantumNous/new-api/relaykit/types"
)

// CandidateFailure 描述一次尚未向客户端提交响应的候选失败结果喵。
type CandidateFailure struct {
	HTTPStatus  int    // 上游 HTTP 状态码，网络错误时为零喵。
	ErrorClass  string // 稳定错误分类，用于失败规则匹配喵。
	BodyPreview string // 受限响应体摘要，仅用于规则匹配，不得写入客户端或日志喵。
	RetryAfter  int    // 上游建议冻结秒数，单位：秒，未知时为零喵。
}

// NormalizeCandidateFailure 将候选失败规范为可安全匹配规则的受限结构喵。
func NormalizeCandidateFailure(statusCode int, responseHeaders http.Header, responseBody []byte, executionError error) CandidateFailure {
	// 创建默认失败结果，避免空指针或零值状态被遗漏喵。
	failure := CandidateFailure{HTTPStatus: statusCode, ErrorClass: "upstream_error"}
	// 喵~防御：网络或 TLS 失败没有可信 HTTP 状态，必须与 HTTP 响应故障区分喵。
	if executionError != nil {
		// 流转伪流断流哨兵优先识别：上游未完整返回即中断，独立分类供 stream_cut 处理措施匹配喵。
		if errors.Is(executionError, relaykitypes.ErrStreamCut) {
			failure.ErrorClass = "stream_cut"
			return failure
		}
		// 卡流哨兵优先识别：静默超时没有 HTTP 状态码可依赖，必须独立分类供 stalled_stream 规则匹配喵。
		if errors.Is(executionError, relaykitypes.ErrStalledStream) {
			failure.ErrorClass = "stalled_stream"
			return failure
		}
		// 区分超时：context deadline 超时归为 timeout 错误分类，其余网络错误归 network_error 喵。
		if errors.Is(executionError, context.DeadlineExceeded) {
			failure.ErrorClass = "timeout"
		} else {
			failure.ErrorClass = "network_error"
		}
		return failure
	}
	// 根据常见 HTTP 状态建立稳定分类，规则仍可通过精确状态码覆盖喵。
	switch {
	case statusCode == http.StatusTooManyRequests:
		failure.ErrorClass = "rate_limited"
	case statusCode == http.StatusRequestTimeout || statusCode == http.StatusGatewayTimeout:
		failure.ErrorClass = "timeout"
	case statusCode >= http.StatusInternalServerError:
		failure.ErrorClass = "upstream_server_error"
	case statusCode >= http.StatusBadRequest:
		failure.ErrorClass = "upstream_client_error"
	case statusCode > 0:
		failure.ErrorClass = "upstream_error"
	default:
		failure.ErrorClass = "network_error"
	}
	// 喵~防御：规则匹配只保留最多 64 KiB 响应文本，避免异常上游消耗无界内存喵。
	const maxFailureBodyPreviewBytes = 64 * 1024
	if len(responseBody) > maxFailureBodyPreviewBytes {
		responseBody = responseBody[:maxFailureBodyPreviewBytes]
	}
	failure.BodyPreview = string(responseBody)
	failure.RetryAfter = ParseRetryAfterSeconds(responseHeaders)
	return failure
}

// ParseRetryAfterSeconds 解析秒数格式的 Retry-After 响应头喵。
func ParseRetryAfterSeconds(responseHeaders http.Header) int {
	// 喵~防御：空响应头或非正数重试时间都不可信，必须回退为零喵。
	if responseHeaders == nil {
		return 0
	}
	retryAfterText := strings.TrimSpace(responseHeaders.Get("Retry-After"))
	retryAfterSeconds, parseError := strconv.Atoi(retryAfterText)
	if parseError != nil || retryAfterSeconds <= 0 {
		return 0
	}
	// 喵~防御：冻结时间不能超过一天，避免恶意上游通过响应头造成长期拒绝服务喵。
	const maximumFreezeSeconds = 24 * 60 * 60
	if retryAfterSeconds > maximumFreezeSeconds {
		return maximumFreezeSeconds
	}
	return retryAfterSeconds
}

// DecideCandidateFailureAction 按规则顺序决定候选失败后的编排动作喵。
func DecideCandidateFailureAction(rules []model.VirtualModelFailureRule, failure CandidateFailure) (model.VirtualModelFailureAction, int) {
	// 规则已由查询层稳定排序；第一条命中规则拥有唯一决策权喵。
	for _, rule := range rules {
		if !candidateFailureRuleMatches(rule, failure) {
			continue
		}
		return rule.Action, candidateFreezeSeconds(rule, failure)
	}
	// 喵~防御：不存在匹配规则时默认切换下一候选，避免无限重试不可预期故障喵。
	return model.VirtualModelActionNext, 0
}

// DecideVirtualModelFailureAction 决定一次候选失败后的编排动作，并在候选未配置规则时回退到模型级全局兜底规则喵。
// 候选配置了自己的失效规则时仍按候选规则决策；候选规则集为空时采用模型级全局兜底规则喵。
func DecideVirtualModelFailureAction(executionSnapshot *model.VirtualModelExecutionSnapshot, candidateID int, failure CandidateFailure) (model.VirtualModelFailureAction, int) {
	// 喵~防御：快照为空时按无规则处理，默认切换下一候选喵。
	if executionSnapshot == nil {
		return model.VirtualModelActionNext, 0
	}
	// 读取候选自己的规则，候选没有配置任何规则时为空喵。
	candidateRules := executionSnapshot.FailureRulesByCandidateID[candidateID]
	// 候选未配置规则时回退到模型级全局兜底规则，保证任何失败都有明确策略喵。
	if len(candidateRules) == 0 {
		candidateRules = executionSnapshot.GlobalFailureRules
	}
	return DecideCandidateFailureAction(candidateRules, failure)
}

// candidateFailureRuleMatches 判断单条规则是否命中喵。
// 失败条件二选一：配置了 ErrorClass 时按错误分类（超时/网络等）匹配，否则按 HTTP 状态码（单值或范围）匹配；
// 两种条件都与可选响应体正则做 AND 组合喵。
func candidateFailureRuleMatches(rule model.VirtualModelFailureRule, failure CandidateFailure) bool {
	errorClass := strings.TrimSpace(rule.ErrorClass)
	if errorClass != "" {
		// 错误分类条件：失败分类必须精确匹配，HTTP 状态码不参与喵。
		if failure.ErrorClass != errorClass {
			return false
		}
	} else {
		// HTTP 状态已配置时必须匹配；带范围上界时落在闭区间内喵。
		if rule.HTTPStatus != 0 {
			if rule.HTTPStatusMax > 0 {
				// 范围匹配：失败状态码必须落在 [HTTPStatus, HTTPStatusMax] 闭区间内喵。
				if failure.HTTPStatus < rule.HTTPStatus || failure.HTTPStatus > rule.HTTPStatusMax {
					return false
				}
			} else {
				// 单值匹配：失败状态码必须精确等于配置值喵。
				if failure.HTTPStatus != rule.HTTPStatus {
					return false
				}
			}
		}
	}
	bodyPattern := strings.TrimSpace(rule.BodyRegex)
	if bodyPattern == "" {
		return true
	}
	// 喵~防御：数据库中的正则可能因历史错误配置而失效，失效规则只能视为不匹配喵。
	compiledPattern, compileError := regexp.Compile(bodyPattern)
	if compileError != nil {
		return false
	}
	return compiledPattern.MatchString(failure.BodyPreview)
}

// candidateFreezeSeconds 综合规则固定时长、响应体字段解析与上游 Retry-After 三者取安全有效值喵。
func candidateFreezeSeconds(rule model.VirtualModelFailureRule, failure CandidateFailure) int {
	freezeSeconds := rule.FreezeSeconds
	// FreezeField 非空时从响应体指定字段解析冻结时间，与固定时长取较大值避免过度放宽冷却喵。
	if strings.TrimSpace(rule.FreezeField) != "" {
		bodyFreezeSeconds := parseBodyFreezeSeconds(rule.FreezeField, rule.FreezeUnit, failure.BodyPreview)
		if bodyFreezeSeconds > freezeSeconds {
			freezeSeconds = bodyFreezeSeconds
		}
	}
	// 上游 Retry-After 响应头同样参与决策，取其中最大冻结时长喵。
	if failure.RetryAfter > freezeSeconds {
		freezeSeconds = failure.RetryAfter
	}
	// 喵~防御：负数和超长冻结配置不可信，统一夹紧至零到一天喵。
	if freezeSeconds < 0 {
		return 0
	}
	const maximumFreezeSeconds = 24 * 60 * 60
	if freezeSeconds > maximumFreezeSeconds {
		return maximumFreezeSeconds
	}
	return freezeSeconds
}

// naturalLanguagePattern 识别响应体中的自然语言时间，如 "in 22 minutes"、"after 5 seconds" 喵。
// 必须带 in/after/within/wait 触发词，避免误匹配 "1h usage" 这类窗口描述喵。
var naturalLanguagePattern = regexp.MustCompile(`(?i)\b(?:in|after|within|wait)\s+(\d+(?:\.\d+)?)\s*(hours?|hrs?|h|minutes?|mins?|m|seconds?|secs?|s)\b`)

// valueNaturalUnitPattern 识别值文本开头的自然语言单位，如 "22 minutes" 喵。
var valueNaturalUnitPattern = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*(hours?|hrs?|h|minutes?|mins?|m|seconds?|secs?|s)\b`)

// valueMixedPattern 识别 1m30s、2m、45s 复合格式喵。
var valueMixedPattern = regexp.MustCompile(`^(\d+)\s*(m(?:\s*(\d+)\s*s)?|s)`)

// valueNumericPattern 识别纯数字开头的值喵。
var valueNumericPattern = regexp.MustCompile(`^(\d+)`)

// parseBodyFreezeSeconds 从响应体文本查找冻结时间，并按单位换算冻结秒数喵。
// field 是响应体中的字段名；unit 决定解读方式：seconds 直接按秒、minutes 按分钟乘以 60、
// mixed 支持 "1m30s" 复合格式、auto 自动在响应体全文扫描自然语言时间喵。
func parseBodyFreezeSeconds(field string, unit model.VirtualModelFreezeUnit, body string) int {
	// 喵~防御：响应体为空时无法解析，直接返回零喵。
	if body == "" {
		return 0
	}
	// auto 单位自动全文扫描自然语言时间（如 "refreshes in 22 minutes"），无需用户指定字段名喵。
	if unit == model.VirtualModelFreezeUnitAuto {
		return scanNaturalLanguageFreezeSeconds(body)
	}
	// 喵~防御：非 auto 单位但字段名为空时无法定位值，直接返回零喵。
	if strings.TrimSpace(field) == "" {
		return 0
	}
	// 转义字段名中的正则元字符，避免用户输入破坏匹配结构喵。
	escapedField := regexp.QuoteMeta(strings.TrimSpace(field))
	// 同时支持 JSON 冒号分隔与 URL 风格等号分隔两种形态，并兼容带引号与裸字段名喵。
	fieldPattern := regexp.MustCompile(`(?i)"` + escapedField + `"\s*[:=]|\b` + escapedField + `\b\s*[:=]`)
	// 找到字段在响应体中的位置，不存在则无法解析喵。
	fieldIndexes := fieldPattern.FindStringIndex(body)
	if fieldIndexes == nil {
		return 0
	}
	// 从字段值起点截取有限长度文本，避免恶意超长值消耗内存喵。
	valueStart := fieldIndexes[1]
	const maxFieldValueBytes = 64
	valueEnd := valueStart + maxFieldValueBytes
	if valueEnd > len(body) {
		valueEnd = len(body)
	}
	// 交给按单位换算的纯函数处理原始值文本喵。
	return parseFreezeValue(body[valueStart:valueEnd], unit)
}

// scanNaturalLanguageFreezeSeconds 在响应体全文查找第一处自然语言时间并换算秒数喵。
func scanNaturalLanguageFreezeSeconds(body string) int {
	const maximumFreezeSeconds = 24 * 60 * 60
	// 找到第一处触发词引导的时间（如 "in 22 minutes"），避免重复扫描误匹配喵。
	match := naturalLanguagePattern.FindStringSubmatch(body)
	if match == nil {
		return 0
	}
	// 换算数字与单位到秒，并对超界值做饱和夹紧喵。
	return convertDurationMatch(match[1], match[2], maximumFreezeSeconds)
}

// parseFreezeValue 按单位解析字段原始值文本并返回夹紧后的冻结秒数喵。
func parseFreezeValue(rawValue string, unit model.VirtualModelFreezeUnit) int {
	const maximumFreezeSeconds = 24 * 60 * 60
	// 去除值文本首尾空白，并剥离 JSON 字符串值可能带的一层引号喵。
	rawValue = strings.TrimPrefix(strings.TrimSpace(rawValue), `"`)
	// mixed 单位优先识别复合格式，避免 1m30s 被自然语言模式截断成 1m 喵。
	if unit == model.VirtualModelFreezeUnitMixed {
		if mixedSeconds := parseMixedValue(rawValue, maximumFreezeSeconds); mixedSeconds > 0 {
			return mixedSeconds
		}
	}
	// 值文本自带自然语言单位（如 "22 minutes"、"1 hour"）时按文本单位换算喵。
	if naturalSeconds := parseValueWithNaturalUnit(rawValue, maximumFreezeSeconds); naturalSeconds > 0 {
		return naturalSeconds
	}
	// 纯数字按用户选择的单位换算：秒单位乘 1、分钟单位乘 60、auto 视为秒喵。
	multiplier := 1
	if unit == model.VirtualModelFreezeUnitMinutes {
		multiplier = 60
	}
	return parseNumericValue(rawValue, multiplier, maximumFreezeSeconds)
}

// parseValueWithNaturalUnit 解析值文本开头的自然语言单位，如 "22 minutes"、"1.5 hours" 喵。
func parseValueWithNaturalUnit(rawValue string, maximumFreezeSeconds int) int {
	match := valueNaturalUnitPattern.FindStringSubmatch(rawValue)
	if match == nil {
		return 0
	}
	return convertDurationMatch(match[1], match[2], maximumFreezeSeconds)
}

// parseMixedValue 解析 1m30s、2m、45s 复合格式的冻结时间喵。
func parseMixedValue(rawValue string, maximumFreezeSeconds int) int {
	mixedMatch := valueMixedPattern.FindStringSubmatch(rawValue)
	// 喵~防御：无法匹配复合格式时返回零，拒绝错误解读分钟喵。
	if mixedMatch == nil {
		return 0
	}
	firstValue, _ := strconv.Atoi(mixedMatch[1])
	// 纯秒格式直接作为秒数，带分钟标记时换算并累加可选秒段喵。
	if mixedMatch[2] == "s" {
		return clampFreezeSeconds(firstValue, maximumFreezeSeconds)
	}
	// 喵~防御：分钟数先夹紧再换算，避免超大分钟数乘法溢出喵。
	if firstValue > maximumFreezeSeconds/60 {
		return maximumFreezeSeconds
	}
	totalSeconds := firstValue * 60
	if mixedMatch[3] != "" {
		secondsValue, _ := strconv.Atoi(mixedMatch[3])
		totalSeconds += secondsValue
	}
	return clampFreezeSeconds(totalSeconds, maximumFreezeSeconds)
}

// parseNumericValue 解析纯数字开头的值，并按倍率换算为秒喵。
func parseNumericValue(rawValue string, multiplier int, maximumFreezeSeconds int) int {
	numberMatch := valueNumericPattern.FindStringSubmatch(rawValue)
	// 喵~防御：找不到数字时无法换算，返回零喵。
	if numberMatch == nil {
		return 0
	}
	numericValue, _ := strconv.Atoi(numberMatch[1])
	// 喵~防御：带倍率的数字先夹紧再换算，避免乘法溢出喵。
	if multiplier > 1 && numericValue > maximumFreezeSeconds/multiplier {
		return maximumFreezeSeconds
	}
	return clampFreezeSeconds(numericValue*multiplier, maximumFreezeSeconds)
}

// convertDurationMatch 将数字文本与单位词换算为夹紧后的冻结秒数喵。
func convertDurationMatch(valueText string, unitText string, maximumFreezeSeconds int) int {
	// 解析浮点数值，解析失败时安全回退为零喵。
	value, parseError := strconv.ParseFloat(valueText, 64)
	if parseError != nil {
		return 0
	}
	factor := naturalUnitFactor(unitText)
	if factor <= 0 {
		return 0
	}
	// 喵~防御：数值超出单日上界时饱和到最大值，避免浮点乘积溢出喵。
	if value > float64(maximumFreezeSeconds/factor) {
		return maximumFreezeSeconds
	}
	return clampFreezeSeconds(int(value*float64(factor)), maximumFreezeSeconds)
}

// naturalUnitFactor 返回自然语言单位词的秒数换算系数，未知单位返回零喵。
func naturalUnitFactor(unit string) int {
	switch unit {
	case "s", "sec", "secs", "second", "seconds":
		return 1
	case "m", "min", "mins", "minute", "minutes":
		return 60
	case "h", "hr", "hrs", "hour", "hours":
		return 3600
	}
	return 0
}

// clampFreezeSeconds 将冻结秒数夹紧到零到一天的安全区间喵。
func clampFreezeSeconds(freezeSeconds int, maximumFreezeSeconds int) int {
	if freezeSeconds <= 0 {
		return 0
	}
	if freezeSeconds > maximumFreezeSeconds {
		return maximumFreezeSeconds
	}
	return freezeSeconds
}

// RetryBackoffSeconds 计算候选重试前的指数退避秒数喵。
func RetryBackoffSeconds(retryIndex int) int {
	// 喵~防御：负数重试序号回退到首次重试，避免位移或指数计算异常喵。
	if retryIndex < 0 {
		retryIndex = 0
	}
	// 主人注意：指数退避会快速增长，因此固定上限 30 秒避免单候选长期占用请求预算喵。
	const maximumRetryBackoffSeconds = 30
	if retryIndex >= 5 {
		return maximumRetryBackoffSeconds
	}
	backoffSeconds := 1 << retryIndex
	if backoffSeconds > maximumRetryBackoffSeconds {
		return maximumRetryBackoffSeconds
	}
	return backoffSeconds
}

// validCandidateErrorClass 判断错误分类是否在稳定白名单内喵。
// 与 NormalizeCandidateFailure 产出的分类一一对应，保证规则能命中真实失败喵。
func validCandidateErrorClass(errorClass string) bool {
	switch errorClass {
	case "timeout", "network_error", "rate_limited", "upstream_server_error", "upstream_client_error", "upstream_error", "stalled_stream", "stream_cut":
		return true
	}
	return false
}

// 流式探测参数默认值，对齐 autoapi 的探测语义喵。
const (
	// DefaultProbeStallTimeoutSeconds 静默多久判定流式卡流，收到字节后计时重置喵。
	DefaultProbeStallTimeoutSeconds = 60
	// DefaultProbeMinContentChars 放流前需累积的内容字符数门槛喵。
	DefaultProbeMinContentChars = 10
	// DefaultProbeTotalTimeoutSeconds 探测阶段总预算，只管放流前的健康确认喵。
	DefaultProbeTotalTimeoutSeconds = 300
)

// ProbeParameters 描述流式候选放流前的健康探测参数喵。
type ProbeParameters struct {
	StallTimeoutSeconds      int // 静默超时，单位：秒；零表示使用默认值喵。
	MinContentChars          int // 放流前需累积的内容字符门槛，零表示使用默认值喵。
	ProbeTotalTimeoutSeconds int // 探测阶段总预算，单位：秒；零表示使用默认值喵。
}

// ResolveProbeParameters 从候选级与模型级全局失败规则中解析流式探测参数喵。
// 候选规则优先于全局规则，每个字段取第一条非零配置，全部未配置时回退内置默认值喵。
func ResolveProbeParameters(candidateRules []model.VirtualModelFailureRule, globalRules []model.VirtualModelFailureRule) ProbeParameters {
	// 逐字段按规则顺序取第一个非零值，保证用户在不同规则上分散配置也能生效喵。
	return ProbeParameters{
		StallTimeoutSeconds:      resolveProbeParameter(candidateRules, globalRules, func(rule model.VirtualModelFailureRule) int { return rule.StallTimeoutSeconds }, DefaultProbeStallTimeoutSeconds),
		MinContentChars:          resolveProbeParameter(candidateRules, globalRules, func(rule model.VirtualModelFailureRule) int { return rule.MinContentChars }, DefaultProbeMinContentChars),
		ProbeTotalTimeoutSeconds: resolveProbeParameter(candidateRules, globalRules, func(rule model.VirtualModelFailureRule) int { return rule.ProbeTotalTimeoutSeconds }, DefaultProbeTotalTimeoutSeconds),
	}
}

// resolveProbeParameter 按候选再到全局的顺序返回第一个非零参数值，全部为零时回退默认喵。
func resolveProbeParameter(candidateRules []model.VirtualModelFailureRule, globalRules []model.VirtualModelFailureRule, pick func(rule model.VirtualModelFailureRule) int, fallback int) int {
	// 候选级规则优先，用户可按候选差异配置探测行为喵。
	for _, rule := range candidateRules {
		if value := pick(rule); value > 0 {
			return value
		}
	}
	// 候选未配置时回退模型级全局兜底规则喵。
	for _, rule := range globalRules {
		if value := pick(rule); value > 0 {
			return value
		}
	}
	return fallback
}

// ResolveFailureTimeoutSeconds 从候选级与模型级全局失败规则中解析超时条件判定阈值喵。
// 候选规则优先于全局规则，取第一条非零值；全部未配置时回退到调用方传入的候选级执行超时喵。
func ResolveFailureTimeoutSeconds(candidateRules []model.VirtualModelFailureRule, globalRules []model.VirtualModelFailureRule, fallback int) int {
	// 候选级超时阈值优先，用户可按候选差异配置判定阈值喵。
	for _, rule := range candidateRules {
		if rule.TimeoutSeconds > 0 {
			return rule.TimeoutSeconds
		}
	}
	// 候选未配置时回退模型级全局兜底规则喵。
	for _, rule := range globalRules {
		if rule.TimeoutSeconds > 0 {
			return rule.TimeoutSeconds
		}
	}
	return fallback
}

// ValidateCandidateFailureRule 校验控制面写入的候选级失败规则边界喵。
func ValidateCandidateFailureRule(rule *model.VirtualModelFailureRule) error {
	// 喵~防御：空规则或非法候选编号必须拒绝持久化喵。
	if rule == nil || rule.CandidateID <= 0 {
		return errors.New("virtual model failure rule is invalid")
	}
	// 规则字段边界由共享校验函数统一把关喵。
	return validateFailureRuleFields(rule.RuleOrder, rule.HTTPStatus, rule.HTTPStatusMax, rule.FreezeSeconds, rule.ErrorClass, rule.BodyRegex, rule.Action, rule.FreezeField, rule.FreezeUnit, rule.StallTimeoutSeconds, rule.MinContentChars, rule.ProbeTotalTimeoutSeconds, rule.TimeoutSeconds)
}

// ValidateGlobalFailureRule 校验控制面写入的模型级全局兜底失败规则边界喵。
func ValidateGlobalFailureRule(rule *model.VirtualModelGlobalFailureRule) error {
	// 喵~防御：空规则或非法模型编号必须拒绝持久化，防止无主规则污染全局兜底喵。
	if rule == nil || rule.VirtualModelID <= 0 {
		return errors.New("virtual model failure rule is invalid")
	}
	// 模型级与候选级规则的字段约束一致，直接复用共享校验喵。
	return validateFailureRuleFields(rule.RuleOrder, rule.HTTPStatus, rule.HTTPStatusMax, rule.FreezeSeconds, rule.ErrorClass, rule.BodyRegex, rule.Action, rule.FreezeField, rule.FreezeUnit, rule.StallTimeoutSeconds, rule.MinContentChars, rule.ProbeTotalTimeoutSeconds, rule.TimeoutSeconds)
}

// validateFailureRuleFields 校验失败规则字段的通用边界喵。
func validateFailureRuleFields(ruleOrder int, httpStatus int, httpStatusMax int, freezeSeconds int, errorClass string, bodyRegex string, action model.VirtualModelFailureAction, freezeField string, freezeUnit model.VirtualModelFreezeUnit, stallTimeoutSeconds int, minContentChars int, probeTotalTimeoutSeconds int, timeoutSeconds int) error {
	// 喵~防御：非法序号、越界状态码、范围上界越界和超长冻结配置必须拒绝持久化喵。
	if ruleOrder < 0 || httpStatus < 0 || httpStatus > 599 || httpStatusMax < 0 || httpStatusMax > 599 || freezeSeconds < 0 || freezeSeconds > 24*60*60 {
		return errors.New("virtual model failure rule is invalid")
	}
	// 喵~防御：流式探测参数必须在安全范围内，零表示未配置使用默认值喵。
	if stallTimeoutSeconds < 0 || stallTimeoutSeconds > 600 || minContentChars < 0 || minContentChars > 1024 || probeTotalTimeoutSeconds < 0 || probeTotalTimeoutSeconds > 3600 {
		return errors.New("virtual model failure rule probe parameters are invalid")
	}
	// 喵~防御：超时条件判定阈值必须在候选超时安全范围内，零表示沿用候选级执行超时喵。
	if timeoutSeconds < 0 || timeoutSeconds > 600 {
		return errors.New("virtual model failure rule timeout is invalid")
	}
	// 喵~防御：范围上界非零时不得小于下界，否则产生永远无法命中的空范围喵。
	if httpStatusMax > 0 && httpStatusMax < httpStatus {
		return errors.New("virtual model failure rule is invalid")
	}
	// 喵~防御：只接受四种稳定动作枚举，拒绝未知动作破坏编排状态机喵。
	if action != model.VirtualModelActionRetry && action != model.VirtualModelActionNext && action != model.VirtualModelActionFreeze && action != model.VirtualModelActionPassthrough {
		return errors.New("virtual model failure rule action is invalid")
	}
	// 喵~防御：错误分类不得超过可检索长度，避免恶意长文本占用规则存储喵。
	errorClass = strings.TrimSpace(errorClass)
	if errorClass != "" && len(errorClass) > 64 {
		return errors.New("virtual model failure rule error class is invalid")
	}
	// 喵~防御：HTTP 状态码与错误分类必须二选一，同时配置会让匹配语义歧义喵。
	if httpStatus != 0 && errorClass != "" {
		return errors.New("virtual model failure rule is invalid")
	}
	// 喵~防御：错误分类只接受稳定白名单，拒绝拼写错误导致规则永不命中喵。
	if errorClass != "" && !validCandidateErrorClass(errorClass) {
		return errors.New("virtual model failure rule error class is invalid")
	}
	// 喵~防御：响应体正则必须能在运行时编译，超长正则直接拒绝喵。
	if len(bodyRegex) > 2048 {
		return errors.New("virtual model failure rule body regex is too long")
	}
	if strings.TrimSpace(bodyRegex) != "" {
		if _, compileError := regexp.Compile(bodyRegex); compileError != nil {
			return errors.New("virtual model failure rule body regex is invalid")
		}
	}
	// 喵~防御：响应体冻结字段名不得超过数据库列宽，避免截断造成歧义喵。
	if len(freezeField) > 64 {
		return errors.New("virtual model failure rule freeze field is too long")
	}
	// 喵~防御：配置了单位时必须是合法枚举，未知单位无法安全换算冻结秒数喵。
	// auto 单位允许字段名为空，用于全文扫描自然语言时间喵。
	if freezeUnit != "" &&
		freezeUnit != model.VirtualModelFreezeUnitSeconds &&
		freezeUnit != model.VirtualModelFreezeUnitMinutes &&
		freezeUnit != model.VirtualModelFreezeUnitMixed &&
		freezeUnit != model.VirtualModelFreezeUnitAuto {
		return errors.New("virtual model failure rule freeze unit is invalid")
	}
	return nil
}
