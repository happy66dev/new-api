package virtualmodel

import (
	"net/http"
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
			name: "error class and regex must match",
			rules: []model.VirtualModelFailureRule{
				{ErrorClass: "upstream_server_error", BodyRegex: "capacity", Action: model.VirtualModelActionRetry},
			},
			failure:        CandidateFailure{HTTPStatus: http.StatusServiceUnavailable, ErrorClass: "upstream_server_error", BodyPreview: "temporary capacity exhaustion"},
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
