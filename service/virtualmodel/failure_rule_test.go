package virtualmodel

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaykitypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

// TestDecideCandidateFailureAction 验证规则顺序、默认 next 与冻结时长边界喵。
func TestDecideCandidateFailureAction(t *testing.T) {
	// 定义第一条命中、后续规则和无规则命中的测试场景喵。
	testCases := []struct {
		name           string
		rules          []model.VirtualModelFailureRule
		failure        CandidateFailure
		expectedAction model.VirtualModelFailureAction
		expectedFreeze int
	}{
		{
			name: "first matching rule wins",
			rules: []model.VirtualModelFailureRule{
				{HTTPStatus: http.StatusTooManyRequests, Action: model.VirtualModelActionFreeze, FreezeSeconds: 30},
				{HTTPStatus: http.StatusTooManyRequests, Action: model.VirtualModelActionPassthrough},
			},
			failure:        CandidateFailure{HTTPStatus: http.StatusTooManyRequests, ErrorClass: "rate_limited", RetryAfter: 60},
			expectedAction: model.VirtualModelActionFreeze,
			expectedFreeze: 60,
		},
		{
			name:           "unmatched rule defaults next",
			rules:          []model.VirtualModelFailureRule{{HTTPStatus: http.StatusBadRequest, Action: model.VirtualModelActionPassthrough}},
			failure:        CandidateFailure{HTTPStatus: http.StatusBadGateway, ErrorClass: "upstream_server_error"},
			expectedAction: model.VirtualModelActionNext,
			expectedFreeze: 0,
		},
		{
			name: "response body regex must match",
			rules: []model.VirtualModelFailureRule{
				{BodyRegex: "capacity", Action: model.VirtualModelActionRetry},
			},
			failure:        CandidateFailure{HTTPStatus: http.StatusServiceUnavailable, ErrorClass: "upstream_server_error", BodyPreview: "temporary capacity exhaustion"},
			expectedAction: model.VirtualModelActionRetry,
			expectedFreeze: 0,
		},
		{
			name: "status code range matches inside interval",
			rules: []model.VirtualModelFailureRule{
				{HTTPStatus: http.StatusInternalServerError, HTTPStatusMax: 524, Action: model.VirtualModelActionPassthrough},
			},
			failure:        CandidateFailure{HTTPStatus: http.StatusBadGateway},
			expectedAction: model.VirtualModelActionPassthrough,
			expectedFreeze: 0,
		},
		{
			name: "status code range rejects outside interval",
			rules: []model.VirtualModelFailureRule{
				{HTTPStatus: http.StatusInternalServerError, HTTPStatusMax: 524, Action: model.VirtualModelActionPassthrough},
			},
			failure:        CandidateFailure{HTTPStatus: 525},
			expectedAction: model.VirtualModelActionNext,
			expectedFreeze: 0,
		},
		{
			name: "exact status code still matches when max is unset",
			rules: []model.VirtualModelFailureRule{
				{HTTPStatus: http.StatusTooManyRequests, Action: model.VirtualModelActionFreeze},
			},
			failure:        CandidateFailure{HTTPStatus: http.StatusTooManyRequests},
			expectedAction: model.VirtualModelActionFreeze,
			expectedFreeze: 0,
		},
		{
			name: "matching ignores error class entirely",
			rules: []model.VirtualModelFailureRule{
				{HTTPStatus: http.StatusServiceUnavailable, Action: model.VirtualModelActionRetry},
			},
			failure:        CandidateFailure{HTTPStatus: http.StatusServiceUnavailable, ErrorClass: "unrelated_class"},
			expectedAction: model.VirtualModelActionRetry,
			expectedFreeze: 0,
		},
	}
	// 逐项确认失败规则不会因后续规则或不匹配条件改变决策喵。
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			action, freezeSeconds, ruleRetryCount := DecideCandidateFailureAction(testCase.rules, testCase.failure)
			if action != testCase.expectedAction || freezeSeconds != testCase.expectedFreeze {
				t.Fatalf("DecideCandidateFailureAction() = (%q, %d), want (%q, %d)", action, freezeSeconds, testCase.expectedAction, testCase.expectedFreeze)
			}
			// 规则未配置重试次数时返回零，表示沿用候选 MaxRetries 喵。
			if ruleRetryCount != 0 {
				t.Fatalf("DecideCandidateFailureAction() retry count = %d, want 0", ruleRetryCount)
			}
		})
	}
}

// TestDecideVirtualModelFailureActionGlobalFallback 验证候选无规则时回退模型级全局兜底规则喵。
func TestDecideVirtualModelFailureActionGlobalFallback(t *testing.T) {
	// 构造同时含候选级规则与模型级全局规则的执行快照喵。
	executionSnapshot := &model.VirtualModelExecutionSnapshot{
		FailureRulesByCandidateID: map[int][]model.VirtualModelFailureRule{
			11: {
				{HTTPStatus: http.StatusTooManyRequests, Action: model.VirtualModelActionFreeze, FreezeSeconds: 30},
			},
		},
		GlobalFailureRules: []model.VirtualModelFailureRule{
			{HTTPStatus: http.StatusInternalServerError, Action: model.VirtualModelActionPassthrough},
		},
	}
	// 场景一：候选配置了规则时优先按候选规则决策，不受全局规则影响喵。
	action, _, _ := DecideVirtualModelFailureAction(executionSnapshot, 11, CandidateFailure{HTTPStatus: http.StatusTooManyRequests})
	require.Equal(t, model.VirtualModelActionFreeze, action)
	// 场景二：候选未配置规则时回退到模型级全局兜底规则喵。
	action, _, _ = DecideVirtualModelFailureAction(executionSnapshot, 22, CandidateFailure{HTTPStatus: http.StatusInternalServerError})
	require.Equal(t, model.VirtualModelActionPassthrough, action)
	// 场景三：全局规则也未命中时保持默认切换下一候选喵。
	action, _, _ = DecideVirtualModelFailureAction(executionSnapshot, 22, CandidateFailure{HTTPStatus: http.StatusBadGateway})
	require.Equal(t, model.VirtualModelActionNext, action)
	// 场景四：快照为空时保守回退默认切换下一候选喵。
	action, _, _ = DecideVirtualModelFailureAction(nil, 1, CandidateFailure{HTTPStatus: http.StatusInternalServerError})
	require.Equal(t, model.VirtualModelActionNext, action)
}

// TestNormalizeCandidateFailure 验证 HTTP、网络错误和 Retry-After 的稳定分类喵。
func TestNormalizeCandidateFailure(t *testing.T) {
	// 构造包含可解析 Retry-After 的限流响应头喵。
	responseHeaders := make(http.Header)
	responseHeaders.Set("Retry-After", "75")
	// HTTP 限流必须分类为 rate_limited 并保留受限冻结建议喵。
	rateLimitedFailure := NormalizeCandidateFailure(http.StatusTooManyRequests, responseHeaders, []byte("rate limited"), nil)
	if rateLimitedFailure.ErrorClass != "rate_limited" || rateLimitedFailure.RetryAfter != 75 || rateLimitedFailure.BodyPreview != "rate limited" {
		t.Fatalf("unexpected rate-limited failure: %#v", rateLimitedFailure)
	}
	// 网络失败不能伪造 HTTP 状态或采纳上游响应头喵。
	networkFailure := NormalizeCandidateFailure(0, responseHeaders, nil, http.ErrHandlerTimeout)
	if networkFailure.ErrorClass != "network_error" || networkFailure.RetryAfter != 0 {
		t.Fatalf("unexpected network failure: %#v", networkFailure)
	}
	// 非法 Retry-After 必须安全回退为零喵。
	invalidHeaders := make(http.Header)
	invalidHeaders.Set("Retry-After", "invalid")
	if retryAfterSeconds := ParseRetryAfterSeconds(invalidHeaders); retryAfterSeconds != 0 {
		t.Fatalf("ParseRetryAfterSeconds(invalid) = %d, want 0", retryAfterSeconds)
	}
}

// TestParseRetryAfterSeconds 验证 Retry-After 响应头的解析边界喵。
func TestParseRetryAfterSeconds(t *testing.T) {
	// 定义响应头文本、空头与预期秒数的表格测试喵。
	testCases := []struct {
		name            string
		responseHeaders http.Header
		expectedSeconds int
	}{
		{name: "nil headers fall back to zero", responseHeaders: nil, expectedSeconds: 0},
		{name: "empty header text falls back to zero", responseHeaders: func() http.Header { headers := make(http.Header); headers.Set("Retry-After", ""); return headers }(), expectedSeconds: 0},
		{name: "valid seconds header", responseHeaders: func() http.Header { headers := make(http.Header); headers.Set("Retry-After", "75"); return headers }(), expectedSeconds: 75},
		{name: "non numeric text falls back to zero", responseHeaders: func() http.Header { headers := make(http.Header); headers.Set("Retry-After", "later"); return headers }(), expectedSeconds: 0},
		{name: "zero value falls back to zero", responseHeaders: func() http.Header { headers := make(http.Header); headers.Set("Retry-After", "0"); return headers }(), expectedSeconds: 0},
		{name: "negative value falls back to zero", responseHeaders: func() http.Header { headers := make(http.Header); headers.Set("Retry-After", "-30"); return headers }(), expectedSeconds: 0},
		{name: "caps above one day", responseHeaders: func() http.Header { headers := make(http.Header); headers.Set("Retry-After", "90000"); return headers }(), expectedSeconds: 24 * 60 * 60},
		{name: "keeps exact one day bound", responseHeaders: func() http.Header { headers := make(http.Header); headers.Set("Retry-After", "86400"); return headers }(), expectedSeconds: 24 * 60 * 60},
	}
	// 逐项断言响应头不会泄漏负数、无界时长或解析错误喵。
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actualSeconds := ParseRetryAfterSeconds(testCase.responseHeaders); actualSeconds != testCase.expectedSeconds {
				t.Fatalf("ParseRetryAfterSeconds() = %d, want %d", actualSeconds, testCase.expectedSeconds)
			}
		})
	}
}

// TestRetryBackoffSeconds 验证指数退避、负值处理与最大等待上限喵。
func TestRetryBackoffSeconds(t *testing.T) {
	// 定义首次、后续、负数和过大重试序号的预期退避时间喵。
	testCases := []struct {
		retryIndex      int
		expectedSeconds int
	}{
		{retryIndex: -1, expectedSeconds: 1},
		{retryIndex: 0, expectedSeconds: 1},
		{retryIndex: 1, expectedSeconds: 2},
		{retryIndex: 4, expectedSeconds: 16},
		{retryIndex: 5, expectedSeconds: 30},
		{retryIndex: 99, expectedSeconds: 30},
	}
	// 逐项断言退避不会负数、溢出或超过系统上限喵。
	for _, testCase := range testCases {
		if actualSeconds := RetryBackoffSeconds(testCase.retryIndex); actualSeconds != testCase.expectedSeconds {
			t.Fatalf("RetryBackoffSeconds(%d) = %d, want %d", testCase.retryIndex, actualSeconds, testCase.expectedSeconds)
		}
	}
}

// TestValidateCandidateFailureRule 验证失败规则拒绝非法动作、正则和冻结配置喵。
func TestValidateCandidateFailureRule(t *testing.T) {
	// 合法规则必须允许保存喵。
	validRule := &model.VirtualModelFailureRule{CandidateID: 1, RuleOrder: 0, HTTPStatus: http.StatusBadGateway, BodyRegex: "unavailable", Action: model.VirtualModelActionNext}
	if validationError := ValidateCandidateFailureRule(validRule); validationError != nil {
		t.Fatalf("valid rule rejected: %v", validationError)
	}
	// 非法动作不能落库，避免运行时产生未定义编排语义喵。
	invalidActionRule := *validRule
	invalidActionRule.Action = "invalid"
	if validationError := ValidateCandidateFailureRule(&invalidActionRule); validationError == nil {
		t.Fatal("invalid action should be rejected")
	}
	// 非法正则不能落库，避免请求执行时重复发生编译错误喵。
	invalidRegexRule := *validRule
	invalidRegexRule.BodyRegex = "["
	if validationError := ValidateCandidateFailureRule(&invalidRegexRule); validationError == nil {
		t.Fatal("invalid regex should be rejected")
	}
}

// TestParseBodyFreezeSeconds 验证从响应体字段解析冻结时间的三种单位与换算边界喵。
func TestParseBodyFreezeSeconds(t *testing.T) {
	// 定义字段名、单位、响应体与预期冻结秒数的表格测试喵。
	testCases := []struct {
		name            string
		field           string
		unit            model.VirtualModelFreezeUnit
		body            string
		expectedSeconds int
	}{
		{name: "seconds from json number", field: "retry_after", unit: model.VirtualModelFreezeUnitSeconds, body: `{"error":"busy","retry_after":120}`, expectedSeconds: 120},
		{name: "seconds from quoted string", field: "retry_after", unit: model.VirtualModelFreezeUnitSeconds, body: `{"retry_after":"45"}`, expectedSeconds: 45},
		{name: "minutes multiply by sixty", field: "wait_minutes", unit: model.VirtualModelFreezeUnitMinutes, body: `{"wait_minutes":2}`, expectedSeconds: 120},
		{name: "mixed one minute thirty seconds", field: "retry", unit: model.VirtualModelFreezeUnitMixed, body: `{"retry":"1m30s"}`, expectedSeconds: 90},
		{name: "mixed minutes only", field: "retry", unit: model.VirtualModelFreezeUnitMixed, body: `{"retry":"2m"}`, expectedSeconds: 120},
		{name: "mixed seconds only", field: "retry", unit: model.VirtualModelFreezeUnitMixed, body: `{"retry":"40s"}`, expectedSeconds: 40},
		{name: "equals separated query style", field: "cooldown", unit: model.VirtualModelFreezeUnitSeconds, body: `cooldown=30&reason=busy`, expectedSeconds: 30},
		{name: "caps at one day", field: "retry_after", unit: model.VirtualModelFreezeUnitSeconds, body: `{"retry_after":99999999}`, expectedSeconds: 24 * 60 * 60},
	}
	// 逐项断言解析结果与换算单位完全符合预期喵。
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actualSeconds := parseBodyFreezeSeconds(testCase.field, testCase.unit, testCase.body); actualSeconds != testCase.expectedSeconds {
				t.Fatalf("parseBodyFreezeSeconds() = %d, want %d", actualSeconds, testCase.expectedSeconds)
			}
		})
	}
}

// TestParseBodyFreezeSecondsDefensive 验证空字段、缺失字段与畸形值的零值安全回退喵。
func TestParseBodyFreezeSecondsDefensive(t *testing.T) {
	// 字段名为空时无法定位值，必须回退为零喵。
	if seconds := parseBodyFreezeSeconds("", model.VirtualModelFreezeUnitSeconds, `{"retry_after":1}`); seconds != 0 {
		t.Fatalf("empty field parsed to %d, want 0", seconds)
	}
	// 响应体不存在该字段时回退为零喵。
	if seconds := parseBodyFreezeSeconds("missing", model.VirtualModelFreezeUnitSeconds, `{"retry_after":1}`); seconds != 0 {
		t.Fatalf("missing field parsed to %d, want 0", seconds)
	}
	// 非数字值不能安全换算，必须回退为零喵。
	if seconds := parseBodyFreezeSeconds("retry_after", model.VirtualModelFreezeUnitSeconds, `{"retry_after":"later"}`); seconds != 0 {
		t.Fatalf("non-numeric value parsed to %d, want 0", seconds)
	}
	// mixed 单位遇到纯数字时按秒兜底，不会误读为分钟喵。
	if seconds := parseBodyFreezeSeconds("retry_after", model.VirtualModelFreezeUnitMixed, `{"retry_after":5}`); seconds != 5 {
		t.Fatalf("mixed unit with plain number parsed to %d, want 5", seconds)
	}
	// 负值文本不得解析为负冻结时长喵。
	if seconds := parseBodyFreezeSeconds("retry_after", model.VirtualModelFreezeUnitSeconds, `{"retry_after":-30}`); seconds != 0 {
		t.Fatalf("negative value parsed to %d, want 0", seconds)
	}
}

// TestCandidateFreezeSecondsBodyField 验证响应体字段冻结与固定时长、Retry-After 的取值优先级喵。
func TestCandidateFreezeSecondsBodyField(t *testing.T) {
	// 响应体字段换算值大于固定时长时采用响应体值喵。
	rule := model.VirtualModelFailureRule{Action: model.VirtualModelActionFreeze, FreezeSeconds: 10, FreezeField: "retry_after", FreezeUnit: model.VirtualModelFreezeUnitMinutes}
	if seconds := candidateFreezeSeconds(rule, CandidateFailure{BodyPreview: `{"retry_after":3}`}); seconds != 180 {
		t.Fatalf("body field freeze = %d, want 180", seconds)
	}
	// 固定时长大于响应体解析值时保留固定时长喵。
	if seconds := candidateFreezeSeconds(rule, CandidateFailure{BodyPreview: `{"retry_after":1}`}); seconds != 60 {
		t.Fatalf("body field freeze = %d, want 60", seconds)
	}
	// Retry-After 响应头仍然参与比较，三者取最大值喵。
	if seconds := candidateFreezeSeconds(rule, CandidateFailure{BodyPreview: `{"retry_after":2}`, RetryAfter: 300}); seconds != 300 {
		t.Fatalf("retry-after head = %d, want 300", seconds)
	}
	// FreezeField 为空时退化为纯固定时长逻辑，不触碰响应体喵。
	if seconds := candidateFreezeSeconds(model.VirtualModelFailureRule{FreezeSeconds: 5}, CandidateFailure{BodyPreview: `{"retry_after":999}`}); seconds != 5 {
		t.Fatalf("no field freeze = %d, want 5", seconds)
	}
}

// TestValidateFailureRuleFreezeField 验证响应体冻结字段名与单位的控制面校验喵。
func TestValidateFailureRuleFreezeField(t *testing.T) {
	// 合法字段名与合法单位必须允许保存喵。
	validRule := &model.VirtualModelFailureRule{CandidateID: 1, RuleOrder: 0, FreezeField: "retry_after", FreezeUnit: model.VirtualModelFreezeUnitSeconds, Action: model.VirtualModelActionFreeze}
	if validationError := ValidateCandidateFailureRule(validRule); validationError != nil {
		t.Fatalf("valid freeze field rejected: %v", validationError)
	}
	// 未知单位必须拒绝，避免运行时无法换算冻结秒数喵。
	invalidUnitRule := *validRule
	invalidUnitRule.FreezeUnit = "fortnights"
	if validationError := ValidateCandidateFailureRule(&invalidUnitRule); validationError == nil {
		t.Fatal("unknown freeze unit should be rejected")
	}
	// 超长字段名必须拒绝，避免数据库列截断产生歧义喵。
	longFieldRule := *validRule
	longFieldRule.FreezeField = strings.Repeat("x", 65)
	if validationError := ValidateCandidateFailureRule(&longFieldRule); validationError == nil {
		t.Fatal("overlong freeze field should be rejected")
	}
}

// TestParseBodyFreezeSecondsAuto 验证 auto 单位在响应体全文扫描自然语言时间的语义喵。
func TestParseBodyFreezeSecondsAuto(t *testing.T) {
	// 定义响应体、字段名与预期冻结秒数的表格测试，auto 应忽略字段名喵。
	testCases := []struct {
		name            string
		field           string
		body            string
		expectedSeconds int
	}{
		{name: "user quota sample", field: "ignored", body: `status_code=429, key sk-a2b*** has reached its rolling 1h usage quota; refreshes in 22 minutes`, expectedSeconds: 22 * 60},
		{name: "try again in minutes", field: "", body: `Try again in 5 minutes.`, expectedSeconds: 5 * 60},
		{name: "retry after seconds", field: "", body: `Rate limit exceeded. Retry after 30 seconds.`, expectedSeconds: 30},
		{name: "please wait minutes", field: "", body: `Please wait 2 minutes before trying again.`, expectedSeconds: 2 * 60},
		{name: "hours unit", field: "", body: `Try again in 1 hour.`, expectedSeconds: 3600},
		{name: "no trigger word ignores window description", field: "", body: `rolling 1h usage quota exceeded`, expectedSeconds: 0},
		{name: "status code alone is not a duration", field: "", body: `status_code=429, try again later`, expectedSeconds: 0},
	}
	// 逐项断言 auto 全文扫描不会漏掉自然语言或误匹配窗口描述喵。
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actualSeconds := parseBodyFreezeSeconds(testCase.field, model.VirtualModelFreezeUnitAuto, testCase.body); actualSeconds != testCase.expectedSeconds {
				t.Fatalf("parseBodyFreezeSeconds(auto) = %d, want %d", actualSeconds, testCase.expectedSeconds)
			}
		})
	}
}

// TestParseFreezeValueNaturalLanguage 验证值文本自带自然语言单位的换算喵。
func TestParseFreezeValueNaturalLanguage(t *testing.T) {
	// 定义值文本、单位与预期秒数的表格测试喵。
	testCases := []struct {
		name            string
		rawValue        string
		unit            model.VirtualModelFreezeUnit
		expectedSeconds int
	}{
		{name: "minutes word with seconds unit", rawValue: `"22 minutes"`, unit: model.VirtualModelFreezeUnitSeconds, expectedSeconds: 22 * 60},
		{name: "hour word", rawValue: `1 hour`, unit: model.VirtualModelFreezeUnitSeconds, expectedSeconds: 3600},
		{name: "fractional hour", rawValue: `1.5 hours`, unit: model.VirtualModelFreezeUnitSeconds, expectedSeconds: 5400},
		{name: "minutes word with minutes unit", rawValue: `22 minutes`, unit: model.VirtualModelFreezeUnitMinutes, expectedSeconds: 22 * 60},
		{name: "mixed compound not truncated", rawValue: `1m30s`, unit: model.VirtualModelFreezeUnitMixed, expectedSeconds: 90},
		{name: "mixed seconds only", rawValue: `45s`, unit: model.VirtualModelFreezeUnitMixed, expectedSeconds: 45},
		{name: "plain number keeps unit meaning", rawValue: `2`, unit: model.VirtualModelFreezeUnitMinutes, expectedSeconds: 120},
		{name: "plain number seconds", rawValue: `120`, unit: model.VirtualModelFreezeUnitSeconds, expectedSeconds: 120},
	}
	// 逐项断言自然语言单位优先且不会破坏复合格式喵。
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actualSeconds := parseFreezeValue(testCase.rawValue, testCase.unit); actualSeconds != testCase.expectedSeconds {
				t.Fatalf("parseFreezeValue(%q, %q) = %d, want %d", testCase.rawValue, testCase.unit, actualSeconds, testCase.expectedSeconds)
			}
		})
	}
}

// TestValidateFailureRuleAutoUnit 验证 auto 单位在字段名可空场景下的校验语义喵。
func TestValidateFailureRuleAutoUnit(t *testing.T) {
	// auto 单位允许字段名为空，用于全文扫描自然语言时间喵。
	autoRule := &model.VirtualModelFailureRule{CandidateID: 1, RuleOrder: 0, FreezeUnit: model.VirtualModelFreezeUnitAuto, Action: model.VirtualModelActionFreeze}
	if validationError := ValidateCandidateFailureRule(autoRule); validationError != nil {
		t.Fatalf("auto unit with empty field rejected: %v", validationError)
	}
	// 带字段名的 auto 单位同样合法喵。
	autoFieldRule := &model.VirtualModelFailureRule{CandidateID: 1, RuleOrder: 0, FreezeField: "retry_after", FreezeUnit: model.VirtualModelFreezeUnitAuto, Action: model.VirtualModelActionFreeze}
	if validationError := ValidateCandidateFailureRule(autoFieldRule); validationError != nil {
		t.Fatalf("auto unit with field rejected: %v", validationError)
	}
}

// TestDecideCandidateFailureActionErrorClass 验证失败规则按错误分类匹配的二选一语义喵。
// 配置了 ErrorClass 时只按分类匹配并忽略 HTTP 状态码，未命中时保持默认切换下一候选喵。
func TestDecideCandidateFailureActionErrorClass(t *testing.T) {
	// 定义错误分类规则、失败载荷与预期动作的测试场景，动作用非默认值标记命中喵。
	testCases := []struct {
		name           string
		rule           model.VirtualModelFailureRule
		failure        CandidateFailure
		expectedAction model.VirtualModelFailureAction
	}{
		{
			name:           "timeout class matches a timeout failure",
			rule:           model.VirtualModelFailureRule{ErrorClass: "timeout", Action: model.VirtualModelActionFreeze},
			failure:        CandidateFailure{HTTPStatus: 0, ErrorClass: "timeout"},
			expectedAction: model.VirtualModelActionFreeze,
		},
		{
			name:           "timeout class ignores http status when class differs",
			rule:           model.VirtualModelFailureRule{ErrorClass: "timeout", Action: model.VirtualModelActionFreeze},
			failure:        CandidateFailure{HTTPStatus: 504, ErrorClass: "upstream_server_error"},
			expectedAction: model.VirtualModelActionNext,
		},
		{
			name:           "network class matches a network failure",
			rule:           model.VirtualModelFailureRule{ErrorClass: "network_error", Action: model.VirtualModelActionRetry},
			failure:        CandidateFailure{HTTPStatus: 0, ErrorClass: "network_error"},
			expectedAction: model.VirtualModelActionRetry,
		},
		{
			name:           "rate limited class matches an http 429 failure",
			rule:           model.VirtualModelFailureRule{ErrorClass: "rate_limited", Action: model.VirtualModelActionFreeze},
			failure:        CandidateFailure{HTTPStatus: 429, ErrorClass: "rate_limited"},
			expectedAction: model.VirtualModelActionFreeze,
		},
		{
			name:           "error class is trimmed before comparison",
			rule:           model.VirtualModelFailureRule{ErrorClass: " timeout ", Action: model.VirtualModelActionFreeze},
			failure:        CandidateFailure{HTTPStatus: 0, ErrorClass: "timeout"},
			expectedAction: model.VirtualModelActionFreeze,
		},
	}
	// 逐项断言错误分类规则不会误命中其它分类或退化为任意状态匹配喵。
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			action, _, _ := DecideCandidateFailureAction([]model.VirtualModelFailureRule{testCase.rule}, testCase.failure)
			require.Equal(t, testCase.expectedAction, action)
		})
	}
}

// TestDecideCandidateFailureActionErrorClassBodyRegex 验证错误分类条件仍与响应体正则 AND 组合喵。
func TestDecideCandidateFailureActionErrorClassBodyRegex(t *testing.T) {
	// 超时规则同时限定响应体包含 deadline 字样喵。
	rule := model.VirtualModelFailureRule{ErrorClass: "timeout", BodyRegex: "deadline", Action: model.VirtualModelActionRetry}
	// 分类命中且响应体正则命中时按规则动作执行喵。
	action, _, _ := DecideCandidateFailureAction([]model.VirtualModelFailureRule{rule}, CandidateFailure{HTTPStatus: 0, ErrorClass: "timeout", BodyPreview: "request deadline exceeded"})
	require.Equal(t, model.VirtualModelActionRetry, action)
	// 响应体正则不命中时整条规则视为不匹配，回退默认切换下一候选喵。
	action, _, _ = DecideCandidateFailureAction([]model.VirtualModelFailureRule{rule}, CandidateFailure{HTTPStatus: 0, ErrorClass: "timeout", BodyPreview: "slow but alive"})
	require.Equal(t, model.VirtualModelActionNext, action)
}

// TestDecideCandidateFailureActionRetryCount 验证规则级最大重试次数随决策返回喵。
func TestDecideCandidateFailureActionRetryCount(t *testing.T) {
	// 命中 retry 规则时返回规则级重试次数，供调用方覆盖候选 MaxRetries 喵。
	rule := model.VirtualModelFailureRule{HTTPStatus: http.StatusTooManyRequests, Action: model.VirtualModelActionRetry, RetryCount: 3}
	action, _, retryCount := DecideCandidateFailureAction([]model.VirtualModelFailureRule{rule}, CandidateFailure{HTTPStatus: http.StatusTooManyRequests})
	require.Equal(t, model.VirtualModelActionRetry, action)
	require.Equal(t, 3, retryCount)
	// 未命中时回退默认切换下一候选，并返回零重试次数表示沿用候选 MaxRetries 喵。
	action, _, retryCount = DecideCandidateFailureAction([]model.VirtualModelFailureRule{rule}, CandidateFailure{HTTPStatus: http.StatusBadGateway})
	require.Equal(t, model.VirtualModelActionNext, action)
	require.Equal(t, 0, retryCount)
}

// TestNormalizeCandidateFailureTimeout 验证 context 超时被独立分类为 timeout 而非 network_error 喵。
func TestNormalizeCandidateFailureTimeout(t *testing.T) {
	// 空响应头保证超时路径不采纳任何上游冻结建议喵。
	timeoutFailure := NormalizeCandidateFailure(0, nil, nil, context.DeadlineExceeded)
	require.Equal(t, "timeout", timeoutFailure.ErrorClass)
	require.Equal(t, 0, timeoutFailure.HTTPStatus)
	// HTTP 408 与 504 仍归为 timeout，兼容旧的上游超时信号喵。
	require.Equal(t, "timeout", NormalizeCandidateFailure(http.StatusRequestTimeout, nil, nil, nil).ErrorClass)
	require.Equal(t, "timeout", NormalizeCandidateFailure(http.StatusGatewayTimeout, nil, nil, nil).ErrorClass)
	// 非超时的执行错误仍归为网络错误，不与 timeout 混淆喵。
	require.Equal(t, "network_error", NormalizeCandidateFailure(0, nil, nil, http.ErrHandlerTimeout).ErrorClass)
}

// TestValidateFailureRuleErrorClass 验证错误分类与 HTTP 状态码二选一及白名单校验喵。
func TestValidateFailureRuleErrorClass(t *testing.T) {
	// 合法分类、无状态码的规则必须允许保存喵。
	validRule := &model.VirtualModelFailureRule{CandidateID: 1, RuleOrder: 0, ErrorClass: "timeout", Action: model.VirtualModelActionNext}
	require.NoError(t, ValidateCandidateFailureRule(validRule))
	// 喵~防御：HTTP 状态码与错误分类同时配置会让匹配语义歧义，必须拒绝喵。
	bothRule := *validRule
	bothRule.HTTPStatus = http.StatusBadGateway
	require.Error(t, ValidateCandidateFailureRule(&bothRule))
	// 喵~防御：白名单之外的分类会因拼写错误而永不命中，必须拒绝喵。
	unknownClassRule := *validRule
	unknownClassRule.ErrorClass = "upstream_custom_gateway"
	require.Error(t, ValidateCandidateFailureRule(&unknownClassRule))
	// 喵~防御：超长错误分类文本必须拒绝，避免无界存储喵。
	longClassRule := *validRule
	longClassRule.ErrorClass = strings.Repeat("x", 65)
	require.Error(t, ValidateCandidateFailureRule(&longClassRule))
	// 模型级全局规则同样遵守二选一与白名单语义喵。
	globalRule := &model.VirtualModelGlobalFailureRule{VirtualModelID: 1, RuleOrder: 0, ErrorClass: "network_error", Action: model.VirtualModelActionRetry}
	require.NoError(t, ValidateGlobalFailureRule(globalRule))
}

// TestNormalizeCandidateFailureStalledStream 验证卡流哨兵被独立分类为 stalled_stream 喵。
func TestNormalizeCandidateFailureStalledStream(t *testing.T) {
	// 包装哨兵的错误必须识别为 stalled_stream，与 timeout/network_error 区分喵。
	stalledFailure := NormalizeCandidateFailure(0, nil, nil, fmt.Errorf("%w: upstream silent", relaykitypes.ErrStalledStream))
	require.Equal(t, "stalled_stream", stalledFailure.ErrorClass)
	// 直接传入哨兵同样命中，HTTP 状态码不参与卡流分类喵。
	require.Equal(t, "stalled_stream", NormalizeCandidateFailure(http.StatusBadGateway, nil, nil, relaykitypes.ErrStalledStream).ErrorClass)
	// 普通网络错误不得被误归为卡流喵。
	require.Equal(t, "network_error", NormalizeCandidateFailure(0, nil, nil, http.ErrHandlerTimeout).ErrorClass)
}

// TestNormalizeCandidateFailureStreamCut 验证流转伪流断流哨兵被独立分类为 stream_cut 喵。
func TestNormalizeCandidateFailureStreamCut(t *testing.T) {
	// 包装哨兵的错误必须识别为 stream_cut，与 stalled_stream/timeout 区分喵。
	cutFailure := NormalizeCandidateFailure(0, nil, nil, fmt.Errorf("%w: stream cut before [DONE]", relaykitypes.ErrStreamCut))
	require.Equal(t, "stream_cut", cutFailure.ErrorClass)
	// 直接传入哨兵同样命中，HTTP 状态码不参与断流分类喵。
	require.Equal(t, "stream_cut", NormalizeCandidateFailure(http.StatusBadGateway, nil, nil, relaykitypes.ErrStreamCut).ErrorClass)
	// 断流分类必须在稳定白名单内，供失败规则保存与匹配喵。
	require.True(t, validCandidateErrorClass("stream_cut"))
	// 卡流与断流互不误归喵。
	require.NotEqual(t, "stalled_stream", NormalizeCandidateFailure(0, nil, nil, relaykitypes.ErrStreamCut).ErrorClass)
	require.NotEqual(t, "stream_cut", NormalizeCandidateFailure(0, nil, nil, relaykitypes.ErrStalledStream).ErrorClass)
}

// TestValidateFailureRuleProbeParameters 验证流式探测参数的范围边界喵。
func TestValidateFailureRuleProbeParameters(t *testing.T) {
	baseRule := &model.VirtualModelFailureRule{CandidateID: 1, RuleOrder: 0, ErrorClass: "stalled_stream", Action: model.VirtualModelActionNext}
	// 零值表示未配置，必须允许保存喵。
	require.NoError(t, ValidateCandidateFailureRule(baseRule))
	// 合法范围内的探测参数允许保存喵。
	configuredRule := *baseRule
	configuredRule.StallTimeoutSeconds = 45
	configuredRule.MinContentChars = 20
	configuredRule.ProbeTotalTimeoutSeconds = 240
	require.NoError(t, ValidateCandidateFailureRule(&configuredRule))
	// 喵~防御：超过上界的静默秒数必须拒绝喵。
	oversizeStallRule := *baseRule
	oversizeStallRule.StallTimeoutSeconds = 601
	require.Error(t, ValidateCandidateFailureRule(&oversizeStallRule))
	// 喵~防御：负数内容门槛必须拒绝喵。
	negativeContentRule := *baseRule
	negativeContentRule.MinContentChars = -1
	require.Error(t, ValidateCandidateFailureRule(&negativeContentRule))
	// 模型级全局规则同样校验探测参数喵。
	globalRule := &model.VirtualModelGlobalFailureRule{VirtualModelID: 1, RuleOrder: 0, ErrorClass: "stalled_stream", Action: model.VirtualModelActionRetry, StallTimeoutSeconds: 600}
	require.NoError(t, ValidateGlobalFailureRule(globalRule))
}

// TestResolveProbeParameters 验证探测参数按候选再到全局取首个非零并回退默认喵。
func TestResolveProbeParameters(t *testing.T) {
	// 空规则回退内置默认值喵。
	defaults := ResolveProbeParameters(nil, nil)
	require.Equal(t, DefaultProbeStallTimeoutSeconds, defaults.StallTimeoutSeconds)
	require.Equal(t, DefaultProbeMinContentChars, defaults.MinContentChars)
	require.Equal(t, DefaultProbeTotalTimeoutSeconds, defaults.ProbeTotalTimeoutSeconds)
	// 候选规则优先于全局规则，缺省字段从全局补齐喵。
	candidateRules := []model.VirtualModelFailureRule{{StallTimeoutSeconds: 30}}
	globalRules := []model.VirtualModelFailureRule{{StallTimeoutSeconds: 90, MinContentChars: 25}}
	resolved := ResolveProbeParameters(candidateRules, globalRules)
	require.Equal(t, 30, resolved.StallTimeoutSeconds)
	require.Equal(t, 25, resolved.MinContentChars)
	require.Equal(t, DefaultProbeTotalTimeoutSeconds, resolved.ProbeTotalTimeoutSeconds)
	// 逐字段独立：第一条非零字段只影响该字段，不影响其他字段喵。
	secondCandidateRules := []model.VirtualModelFailureRule{{MinContentChars: 8}, {StallTimeoutSeconds: 5}}
	secondResolved := ResolveProbeParameters(secondCandidateRules, globalRules)
	require.Equal(t, 5, secondResolved.StallTimeoutSeconds)
	require.Equal(t, 8, secondResolved.MinContentChars)
}

// TestResolveFailureTimeoutSeconds 验证超时判定阈值按候选再到全局取首个非零并回退调用方超时喵。
func TestResolveFailureTimeoutSeconds(t *testing.T) {
	// 无规则配置时回退候选级执行超时喵。
	require.Equal(t, 30, ResolveFailureTimeoutSeconds(nil, nil, 30))
	// 候选规则优先于全局规则喵。
	candidateRules := []model.VirtualModelFailureRule{{ErrorClass: "timeout", TimeoutSeconds: 120}}
	globalRules := []model.VirtualModelFailureRule{{TimeoutSeconds: 300}}
	require.Equal(t, 120, ResolveFailureTimeoutSeconds(candidateRules, globalRules, 60))
	// 候选未配置时回退全局规则喵。
	require.Equal(t, 300, ResolveFailureTimeoutSeconds(nil, globalRules, 60))
	// 零值表示未配置，跳过后继续找非零值喵。
	zeroFirstRules := []model.VirtualModelFailureRule{{TimeoutSeconds: 0}, {TimeoutSeconds: 90}}
	require.Equal(t, 90, ResolveFailureTimeoutSeconds(zeroFirstRules, nil, 60))
}

// TestValidateFailureRuleTimeoutSeconds 验证超时判定阈值的范围边界喵。
func TestValidateFailureRuleTimeoutSeconds(t *testing.T) {
	baseRule := &model.VirtualModelFailureRule{CandidateID: 1, RuleOrder: 0, ErrorClass: "timeout", Action: model.VirtualModelActionNext}
	// 零值表示沿用候选级执行超时，必须允许保存喵。
	require.NoError(t, ValidateCandidateFailureRule(baseRule))
	// 合法上界内的判定阈值允许保存喵。
	configuredRule := *baseRule
	configuredRule.TimeoutSeconds = 600
	require.NoError(t, ValidateCandidateFailureRule(&configuredRule))
	// 喵~防御：超过候选超时上界的阈值必须拒绝，避免永不触发的判定喵。
	oversizeRule := *baseRule
	oversizeRule.TimeoutSeconds = 601
	require.Error(t, ValidateCandidateFailureRule(&oversizeRule))
	// 喵~防御：负数阈值必须拒绝喵。
	negativeRule := *baseRule
	negativeRule.TimeoutSeconds = -1
	require.Error(t, ValidateCandidateFailureRule(&negativeRule))
	// 模型级全局规则同样校验超时阈值喵。
	globalRule := &model.VirtualModelGlobalFailureRule{VirtualModelID: 1, RuleOrder: 0, ErrorClass: "timeout", Action: model.VirtualModelActionRetry, TimeoutSeconds: 120}
	require.NoError(t, ValidateGlobalFailureRule(globalRule))
}
