package virtualmodel

import (
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
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
			action, freezeSeconds := DecideCandidateFailureAction(testCase.rules, testCase.failure)
			if action != testCase.expectedAction || freezeSeconds != testCase.expectedFreeze {
				t.Fatalf("DecideCandidateFailureAction() = (%q, %d), want (%q, %d)", action, freezeSeconds, testCase.expectedAction, testCase.expectedFreeze)
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
	action, _ := DecideVirtualModelFailureAction(executionSnapshot, 11, CandidateFailure{HTTPStatus: http.StatusTooManyRequests})
	require.Equal(t, model.VirtualModelActionFreeze, action)
	// 场景二：候选未配置规则时回退到模型级全局兜底规则喵。
	action, _ = DecideVirtualModelFailureAction(executionSnapshot, 22, CandidateFailure{HTTPStatus: http.StatusInternalServerError})
	require.Equal(t, model.VirtualModelActionPassthrough, action)
	// 场景三：全局规则也未命中时保持默认切换下一候选喵。
	action, _ = DecideVirtualModelFailureAction(executionSnapshot, 22, CandidateFailure{HTTPStatus: http.StatusBadGateway})
	require.Equal(t, model.VirtualModelActionNext, action)
	// 场景四：快照为空时保守回退默认切换下一候选喵。
	action, _ = DecideVirtualModelFailureAction(nil, 1, CandidateFailure{HTTPStatus: http.StatusInternalServerError})
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
	// mixed 单位遇到纯数字应拒绝而不是误读为分钟喵。
	if seconds := parseBodyFreezeSeconds("retry_after", model.VirtualModelFreezeUnitMixed, `{"retry_after":5}`); seconds != 0 {
		t.Fatalf("mixed unit with plain number parsed to %d, want 0", seconds)
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
