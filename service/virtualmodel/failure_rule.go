package virtualmodel

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/model"
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
		failure.ErrorClass = "network_error"
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

// candidateFailureRuleMatches 判断单条规则是否同时满足所有已配置条件喵。
// 规则只按 HTTP 状态码（单值或范围）与响应体正则匹配，不依赖可能让用户困惑的错误分类抽象喵。
func candidateFailureRuleMatches(rule model.VirtualModelFailureRule, failure CandidateFailure) bool {
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

// parseBodyFreezeSeconds 从响应体文本查找指定字段后的值，并按单位换算冻结秒数喵。
// field 是响应体中的字段名；unit 决定字段值解读方式：seconds 直接按秒、minutes 按分钟乘以 60、
// mixed 支持 "1m30s" 之类的复合格式喵。
func parseBodyFreezeSeconds(field string, unit model.VirtualModelFreezeUnit, body string) int {
	// 喵~防御：字段名或响应体为空时无法解析，直接返回零喵。
	if strings.TrimSpace(field) == "" || body == "" {
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

// parseFreezeValue 按单位解析字段原始值文本并返回夹紧后的冻结秒数喵。
func parseFreezeValue(rawValue string, unit model.VirtualModelFreezeUnit) int {
	const maximumFreezeSeconds = 24 * 60 * 60
	// 去除值文本首尾空白，并剥离 JSON 字符串值可能带的一层引号喵。
	rawValue = strings.TrimPrefix(strings.TrimSpace(rawValue), `"`)
	// mixed 单位按 XmYs、Xm 或 Xs 格式解析，分钟段与秒段可同时存在喵。
	if unit == model.VirtualModelFreezeUnitMixed {
		// 不锚定结尾以容忍值后残留的引号或括号等响应体字符喵。
		mixedPattern := regexp.MustCompile(`^(\d+)\s*(m(?:\s*(\d+)\s*s)?|s)`)
		mixedMatch := mixedPattern.FindStringSubmatch(rawValue)
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
	// 秒与分钟单位都先取值文本开头的连续数字，兼容 `"2"` 与 `"2 minutes"` 形态喵。
	numberPattern := regexp.MustCompile(`^(\d+)`)
	numberMatch := numberPattern.FindStringSubmatch(rawValue)
	// 喵~防御：找不到数字时无法换算，返回零喵。
	if numberMatch == nil {
		return 0
	}
	numericValue, _ := strconv.Atoi(numberMatch[1])
	// 分钟单位在夹紧前换算成秒，避免大分钟数与 60 相乘溢出喵。
	if unit == model.VirtualModelFreezeUnitMinutes {
		if numericValue > maximumFreezeSeconds/60 {
			return maximumFreezeSeconds
		}
		numericValue *= 60
	}
	return clampFreezeSeconds(numericValue, maximumFreezeSeconds)
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

// ValidateCandidateFailureRule 校验控制面写入的候选级失败规则边界喵。
func ValidateCandidateFailureRule(rule *model.VirtualModelFailureRule) error {
	// 喵~防御：空规则或非法候选编号必须拒绝持久化喵。
	if rule == nil || rule.CandidateID <= 0 {
		return errors.New("virtual model failure rule is invalid")
	}
	// 规则字段边界由共享校验函数统一把关喵。
	return validateFailureRuleFields(rule.RuleOrder, rule.HTTPStatus, rule.HTTPStatusMax, rule.FreezeSeconds, rule.ErrorClass, rule.BodyRegex, rule.Action, rule.FreezeField, rule.FreezeUnit)
}

// ValidateGlobalFailureRule 校验控制面写入的模型级全局兜底失败规则边界喵。
func ValidateGlobalFailureRule(rule *model.VirtualModelGlobalFailureRule) error {
	// 喵~防御：空规则或非法模型编号必须拒绝持久化，防止无主规则污染全局兜底喵。
	if rule == nil || rule.VirtualModelID <= 0 {
		return errors.New("virtual model failure rule is invalid")
	}
	// 模型级与候选级规则的字段约束一致，直接复用共享校验喵。
	return validateFailureRuleFields(rule.RuleOrder, rule.HTTPStatus, rule.HTTPStatusMax, rule.FreezeSeconds, rule.ErrorClass, rule.BodyRegex, rule.Action, rule.FreezeField, rule.FreezeUnit)
}

// validateFailureRuleFields 校验失败规则字段的通用边界喵。
func validateFailureRuleFields(ruleOrder int, httpStatus int, httpStatusMax int, freezeSeconds int, errorClass string, bodyRegex string, action model.VirtualModelFailureAction, freezeField string, freezeUnit model.VirtualModelFreezeUnit) error {
	// 喵~防御：非法序号、越界状态码、范围上界越界和超长冻结配置必须拒绝持久化喵。
	if ruleOrder < 0 || httpStatus < 0 || httpStatus > 599 || httpStatusMax < 0 || httpStatusMax > 599 || freezeSeconds < 0 || freezeSeconds > 24*60*60 {
		return errors.New("virtual model failure rule is invalid")
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
	if strings.TrimSpace(errorClass) != "" && len(errorClass) > 64 {
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
	// 喵~防御：配置了响应体字段时必须使用合法单位，未知单位无法安全换算冻结秒数喵。
	if strings.TrimSpace(freezeField) != "" &&
		freezeUnit != model.VirtualModelFreezeUnitSeconds &&
		freezeUnit != model.VirtualModelFreezeUnitMinutes &&
		freezeUnit != model.VirtualModelFreezeUnitMixed {
		return errors.New("virtual model failure rule freeze unit is invalid")
	}
	return nil
}
