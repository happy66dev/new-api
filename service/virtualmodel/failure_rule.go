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

// candidateFailureRuleMatches 判断单条规则是否同时满足所有已配置条件喵。
func candidateFailureRuleMatches(rule model.VirtualModelFailureRule, failure CandidateFailure) bool {
	// HTTP 状态已配置时必须精确匹配喵。
	if rule.HTTPStatus != 0 && rule.HTTPStatus != failure.HTTPStatus {
		return false
	}
	// 错误分类已配置时必须精确匹配喵。
	if strings.TrimSpace(rule.ErrorClass) != "" && strings.TrimSpace(rule.ErrorClass) != failure.ErrorClass {
		return false
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

// ValidateCandidateFailureRule 校验控制面写入的失败规则边界喵。
func ValidateCandidateFailureRule(rule *model.VirtualModelFailureRule) error {
	// 喵~防御：空规则、非法候选、无效操作和超长冻结配置必须拒绝持久化喵。
	if rule == nil || rule.CandidateID <= 0 || rule.RuleOrder < 0 || rule.HTTPStatus < 0 || rule.HTTPStatus > 599 || rule.FreezeSeconds < 0 || rule.FreezeSeconds > 24*60*60 {
		return errors.New("virtual model failure rule is invalid")
	}
	if rule.Action != model.VirtualModelActionRetry && rule.Action != model.VirtualModelActionNext && rule.Action != model.VirtualModelActionFreeze && rule.Action != model.VirtualModelActionPassthrough {
		return errors.New("virtual model failure rule action is invalid")
	}
	if strings.TrimSpace(rule.ErrorClass) != "" && len(rule.ErrorClass) > 64 {
		return errors.New("virtual model failure rule error class is invalid")
	}
	if len(rule.BodyRegex) > 2048 {
		return errors.New("virtual model failure rule body regex is too long")
	}
	if strings.TrimSpace(rule.BodyRegex) != "" {
		if _, compileError := regexp.Compile(rule.BodyRegex); compileError != nil {
			return errors.New("virtual model failure rule body regex is invalid")
		}
	}
	return nil
}
