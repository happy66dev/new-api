package upstreammodel

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUSDToCnyCents 验证美元余额转 RMB 分与异常钳制喵。
func TestUSDToCnyCents(t *testing.T) {
	// 固定汇率 7.0 以便精确断言，结束后恢复喵。
	oldRate := operation_setting.USDExchangeRate
	operation_setting.USDExchangeRate = 7.0
	defer func() { operation_setting.USDExchangeRate = oldRate }()

	// 1 美元 = 7 元 = 700 分喵。
	assert.Equal(t, int64(700), usdToCnyCents(1.0))
	// 0.5 美元 = 3.5 元 = 350 分喵。
	assert.Equal(t, int64(350), usdToCnyCents(0.5))
	// 负数、NaN、正负无穷一律钳制为零，绝不污染展示或余额喵。
	assert.Equal(t, int64(0), usdToCnyCents(-1.0))
	assert.Equal(t, int64(0), usdToCnyCents(math.NaN()))
	assert.Equal(t, int64(0), usdToCnyCents(math.Inf(1)))
	assert.Equal(t, int64(0), usdToCnyCents(math.Inf(-1)))
}

// TestParseCustomBalanceResponse 验证自定义嗅探路径的余额解析喵。
func TestParseCustomBalanceResponse(t *testing.T) {
	// OpenAI 兼容结构：hard_limit_usd - total_usage/100 喵。
	openAICompatible, _ := json.Marshal(map[string]any{"hard_limit_usd": 100.0, "total_usage": 500.0})
	balance, err := parseCustomBalanceResponse(openAICompatible)
	require.NoError(t, err)
	// total_usage 单位 0.01 美元，500 对应 5 美元喵。
	assert.InDelta(t, 95.0, balance, 0.001)

	// 常见余额字段（字符串形式）喵。
	remainingField, _ := json.Marshal(map[string]any{"remaining": "88.5"})
	balance, err = parseCustomBalanceResponse(remainingField)
	require.NoError(t, err)
	assert.InDelta(t, 88.5, balance, 0.001)

	// 纯数字文本响应喵。
	balance, err = parseCustomBalanceResponse([]byte("123.45"))
	require.NoError(t, err)
	assert.InDelta(t, 123.45, balance, 0.001)

	// 空响应体、非法结构与无余额字段均拒绝喵。
	_, err = parseCustomBalanceResponse([]byte(""))
	require.Error(t, err)
	_, err = parseCustomBalanceResponse([]byte("not-a-number"))
	require.Error(t, err)
	_, err = parseCustomBalanceResponse([]byte(`{"foo":"bar"}`))
	require.Error(t, err)
}

// TestParseOpenAIResponses 验证 OpenAI 标准订阅与用量解析的防御喵。
func TestParseOpenAIResponses(t *testing.T) {
	// 合法订阅响应解析硬上限与支付方式喵。
	subscription, err := parseOpenAISubscription([]byte(`{"has_payment_method":true,"hard_limit_usd":120.0}`))
	require.NoError(t, err)
	assert.Equal(t, 120.0, subscription.HardLimitUSD)
	assert.True(t, subscription.HasPaymentMethod)

	// 非法 JSON 或缺失字段拒绝喵。
	_, err = parseOpenAISubscription([]byte(`not-json`))
	require.Error(t, err)
	_, err = parseOpenAISubscription([]byte(`{"has_payment_method":true}`))
	require.Error(t, err)

	// 合法用量响应解析 total_usage（0.01 美元单位）喵。
	usage, err := parseOpenAIUsage([]byte(`{"total_usage":3000.0}`))
	require.NoError(t, err)
	assert.Equal(t, 3000.0, usage.TotalUsage)

	// 缺失或非法用量字段拒绝喵。
	_, err = parseOpenAIUsage([]byte(`{"object":"list"}`))
	require.Error(t, err)
	_, err = parseOpenAIUsage([]byte(`oops`))
	require.Error(t, err)
}

// TestFetchUserUpstreamBalanceUSD 验证默认 OpenAI 路径与自定义路径嗅探喵。
func TestFetchUserUpstreamBalanceUSD(t *testing.T) {
	// 开发模式开关允许 http://127.0.0.1 嗅探，仅限测试环境喵。
	t.Setenv("VIRTUAL_MODEL_INSECURE_UPSTREAM", "1")

	// mock 服务器同时响应订阅、用量与自定义路径喵。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/dashboard/billing/subscription":
			w.Write([]byte(`{"has_payment_method":true,"hard_limit_usd":120.0}`))
		case "/v1/dashboard/billing/usage":
			w.Write([]byte(`{"total_usage":3000.0}`)) // 3000 × 0.01 = 30 美元已用喵
		case "/custom/balance":
			w.Write([]byte(`{"remaining":50.0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// 默认路径：120 - 30 = 90 美元喵。
	balance, err := fetchUserUpstreamBalanceUSD(server.URL, "sk-test", "")
	require.NoError(t, err)
	assert.InDelta(t, 90.0, balance, 0.001)

	// 自定义路径：50 美元喵。
	balance, err = fetchUserUpstreamBalanceUSD(server.URL, "sk-test", "/custom/balance")
	require.NoError(t, err)
	assert.InDelta(t, 50.0, balance, 0.001)

	// 非 2xx 响应返回错误喵。
	_, err = fetchUserUpstreamBalanceUSD(server.URL, "sk-test", "/missing/path")
	require.Error(t, err)
}
