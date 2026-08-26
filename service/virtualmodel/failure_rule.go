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

// candidateFreezeSeconds 选择上游 Retry-After 与规则冻结时长中的安全有效值喵。
func candidateFreezeSeconds(rule model.VirtualModelFailureRule, failure CandidateFailure) int {
	freezeSeconds := rule.FreezeSeconds
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
	return validateFailureRuleFields(rule.RuleOrder, rule.HTTPStatus, rule.HTTPStatusMax, rule.FreezeSeconds, rule.ErrorClass, rule.BodyRegex, rule.Action)
}

// ValidateGlobalFailureRule 校验控制面写入的模型级全局兜底失败规则边界喵。
func ValidateGlobalFailureRule(rule *model.VirtualModelGlobalFailureRule) error {
	// 喵~防御：空规则或非法模型编号必须拒绝持久化，防止无主规则污染全局兜底喵。
	if rule == nil || rule.VirtualModelID <= 0 {
		return errors.New("virtual model failure rule is invalid")
	}
	// 模型级与候选级规则的字段约束一致，直接复用共享校验喵。
	return validateFailureRuleFields(rule.RuleOrder, rule.HTTPStatus, rule.HTTPStatusMax, rule.FreezeSeconds, rule.ErrorClass, rule.BodyRegex, rule.Action)
}

// validateFailureRuleFields 校验失败规则字段的通用边界喵。
func validateFailureRuleFields(ruleOrder int, httpStatus int, httpStatusMax int, freezeSeconds int, errorClass string, bodyRegex string, action model.VirtualModelFailureAction) error {
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
	return nil
}
