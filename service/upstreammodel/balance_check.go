package upstreammodel

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
	virtualmodelservice "github.com/QuantumNous/new-api/service/virtualmodel"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/tidwall/gjson"
)

// balanceCheckTimeoutSeconds 嗅探请求超时，单位：秒喵。
const balanceCheckTimeoutSeconds = 30

// balanceCheckBodyLimit 限制嗅探响应体大小，防御异常上游拖垮内存喵。
const balanceCheckBodyLimit = 256 * 1024

// openAIBillingSubscriptionPath 默认 OpenAI 标准订阅路径喵。
const openAIBillingSubscriptionPath = "/v1/dashboard/billing/subscription"

// openAIBillingUsagePath 默认 OpenAI 标准用量路径喵。
const openAIBillingUsagePath = "/v1/dashboard/billing/usage"

// openAISubscription 描述 OpenAI 标准订阅响应中用到的字段喵。
type openAISubscription struct {
	HasPaymentMethod bool
	HardLimitUSD     float64
}

// openAIUsage 描述 OpenAI 标准用量响应中用到的字段喵。
type openAIUsage struct {
	TotalUsage float64 // 单位：0.01 美元喵
}

// CheckUserUpstreamModelBalanceCents 嗅探上游 key 的剩余额度，返回 RMB 分喵。
// 默认走 OpenAI 标准 billing 接口；BalanceCheckPath 非空时走自定义路径喵。
func CheckUserUpstreamModelBalanceCents(upstreamModel *model.UserUpstreamModel) (int64, error) {
	// 喵~防御：空对象直接返回错误喵。
	if upstreamModel == nil {
		return 0, errors.New("用户上游模型不存在")
	}
	baseURL, decryptBaseURLError := virtualmodelservice.DecryptCredential(upstreamModel.EncryptedBaseURL, upstreamModel.CredentialVersion)
	apiKey, decryptAPIKeyError := virtualmodelservice.DecryptCredential(upstreamModel.EncryptedAPIKey, upstreamModel.CredentialVersion)
	// 喵~防御：凭据解密失败返回统一错误，不向外暴露密文状态喵。
	if decryptBaseURLError != nil || decryptAPIKeyError != nil {
		return 0, errors.New("用户上游模型凭据不可用")
	}
	parsedBaseURL, urlError := virtualmodelservice.ValidateCustomBaseURL(baseURL)
	if urlError != nil {
		return 0, urlError
	}
	balanceUSD, fetchError := fetchUserUpstreamBalanceUSD(parsedBaseURL.String(), apiKey, upstreamModel.BalanceCheckPath)
	if fetchError != nil {
		return 0, fetchError
	}
	// 美元余额按配置汇率转 RMB 分；异常值由转换函数钳制为零喵。
	return usdToCnyCents(balanceUSD), nil
}

// fetchUserUpstreamBalanceUSD 请求上游 billing 接口并解析剩余美元余额喵。
func fetchUserUpstreamBalanceUSD(baseURL, apiKey, customPath string) (float64, error) {
	// 喵~防御：空 base URL 拒绝请求喵。
	trimmedBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmedBase == "" {
		return 0, errors.New("custom upstream URL is invalid")
	}
	client := virtualmodelservice.StrictCustomHTTPClient(balanceCheckTimeoutSeconds * time.Second)
	// 自定义路径：单次请求，响应内容直接作为余额（美元或余额数字）喵。
	if strings.TrimSpace(customPath) != "" {
		customEndpoint := trimmedBase + "/" + strings.TrimLeft(strings.TrimSpace(customPath), "/")
		responseBody, requestError := getBalanceResponseBody(client, customEndpoint, apiKey)
		if requestError != nil {
			return 0, requestError
		}
		balance, parseError := parseCustomBalanceResponse(responseBody)
		if parseError != nil {
			return 0, parseError
		}
		return balance, nil
	}
	// 默认 OpenAI 标准路径：订阅 + 用量两次请求喵。
	subscriptionBody, requestError := getBalanceResponseBody(client, trimmedBase+openAIBillingSubscriptionPath, apiKey)
	if requestError != nil {
		return 0, requestError
	}
	subscription, parseError := parseOpenAISubscription(subscriptionBody)
	if parseError != nil {
		return 0, parseError
	}
	// 用量统计口径与 OpenAI 后台一致：有支付方式按当月计，无支付方式回溯 100 天喵。
	now := time.Now()
	startDate := fmt.Sprintf("%s-01", now.Format("2006-01"))
	endDate := now.Format("2006-01-02")
	if !subscription.HasPaymentMethod {
		startDate = now.AddDate(0, 0, -100).Format("2006-01-02")
	}
	usageEndpoint := fmt.Sprintf("%s%s?start_date=%s&end_date=%s", trimmedBase, openAIBillingUsagePath, startDate, endDate)
	usageBody, requestError := getBalanceResponseBody(client, usageEndpoint, apiKey)
	if requestError != nil {
		return 0, requestError
	}
	usage, parseError := parseOpenAIUsage(usageBody)
	if parseError != nil {
		return 0, parseError
	}
	// balance = 硬上限 - 已用（TotalUsage 单位 0.01 美元）喵。
	return subscription.HardLimitUSD - usage.TotalUsage/100, nil
}

// getBalanceResponseBody 发送带 Bearer 认证的 GET 并读取受限响应体喵。
func getBalanceResponseBody(client *http.Client, endpoint, apiKey string) ([]byte, error) {
	// 喵~防御：缺参直接拒绝喵。
	if client == nil || strings.TrimSpace(endpoint) == "" {
		return nil, errors.New("balance check request is invalid")
	}
	request, requestError := http.NewRequest(http.MethodGet, endpoint, nil)
	if requestError != nil {
		return nil, requestError
	}
	// 嗅探一律使用 Bearer 认证，覆盖 OpenAI 兼容 billing 接口喵。
	if strings.TrimSpace(apiKey) != "" {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	}
	response, requestError := client.Do(request)
	if requestError != nil {
		return nil, requestError
	}
	defer response.Body.Close()
	// 喵~防御：非 2xx 状态视为嗅探失败喵。
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("balance check returned HTTP %d", response.StatusCode)
	}
	body, readError := io.ReadAll(io.LimitReader(response.Body, balanceCheckBodyLimit+1))
	if readError != nil {
		return nil, readError
	}
	// 喵~防御：响应体超限或为空视为异常喵。
	if len(body) > balanceCheckBodyLimit || len(body) == 0 {
		return nil, errors.New("balance check response is invalid")
	}
	return body, nil
}

// parseOpenAISubscription 解析 OpenAI 标准订阅响应喵。
func parseOpenAISubscription(body []byte) (openAISubscription, error) {
	// 喵~防御：非法 JSON 或无 hard_limit_usd 字段视为失败喵。
	if !gjson.ValidBytes(body) || !gjson.GetBytes(body, "hard_limit_usd").Exists() {
		return openAISubscription{}, errors.New("balance check subscription response is invalid")
	}
	hardLimit := gjson.GetBytes(body, "hard_limit_usd").Float()
	// 喵~防御：非有限值硬上限按零处理，避免负数余额污染展示喵。
	if math.IsNaN(hardLimit) || math.IsInf(hardLimit, 0) || hardLimit < 0 {
		hardLimit = 0
	}
	return openAISubscription{
		HasPaymentMethod: gjson.GetBytes(body, "has_payment_method").Bool(),
		HardLimitUSD:     hardLimit,
	}, nil
}

// parseOpenAIUsage 解析 OpenAI 标准用量响应喵。
func parseOpenAIUsage(body []byte) (openAIUsage, error) {
	// 喵~防御：非法 JSON 或无 total_usage 字段视为失败喵。
	if !gjson.ValidBytes(body) || !gjson.GetBytes(body, "total_usage").Exists() {
		return openAIUsage{}, errors.New("balance check usage response is invalid")
	}
	totalUsage := gjson.GetBytes(body, "total_usage").Float()
	// 喵~防御：非有限值按零处理喵。
	if math.IsNaN(totalUsage) || math.IsInf(totalUsage, 0) || totalUsage < 0 {
		totalUsage = 0
	}
	return openAIUsage{TotalUsage: totalUsage}, nil
}

// parseCustomBalanceResponse 解析自定义嗅探路径返回的余额，支持纯数字、常见余额字段与 OpenAI 兼容结构喵。
func parseCustomBalanceResponse(body []byte) (float64, error) {
	// 喵~防御：空响应体视为失败喵。
	if len(bytes.TrimSpace(body)) == 0 {
		return 0, errors.New("balance check response is invalid")
	}
	if !gjson.ValidBytes(body) {
		// 非 JSON 文本（如 "not-a-number"）或裸数字，按十进制数字解析喵。
		parsed, parseError := strconv.ParseFloat(string(bytes.TrimSpace(body)), 64)
		if parseError != nil {
			return 0, errors.New("balance check response is invalid")
		}
		return parsed, nil
	}
	rootValue := gjson.GetBytes(body, "@this")
	// 顶层为数字的合法 JSON（如 "123.45"）直接作为余额喵。
	if rootValue.Type == gjson.Number {
		return rootValue.Float(), nil
	}
	// 喵~防御：顶层必须为对象才能探测余额字段喵。
	if rootValue.Type != gjson.JSON {
		return 0, errors.New("balance check response is invalid")
	}
	// OpenAI 兼容：hard_limit_usd - total_usage/100 喵。
	if gjson.GetBytes(body, "hard_limit_usd").Exists() {
		hardLimit := gjson.GetBytes(body, "hard_limit_usd").Float()
		totalUsage := gjson.GetBytes(body, "total_usage").Float()
		return hardLimit - totalUsage/100, nil
	}
	// 常见余额字段名依次探测，支持数字或可解析字符串喵。
	for _, fieldName := range []string{"remaining", "balance", "remaining_balance", "available", "credit", "amount"} {
		fieldValue := gjson.GetBytes(body, fieldName)
		if fieldValue.Exists() && fieldValue.Type != gjson.Null {
			if fieldValue.Type == gjson.String {
				parsed, parseError := strconv.ParseFloat(fieldValue.String(), 64)
				if parseError == nil {
					return parsed, nil
				}
				continue
			}
			return fieldValue.Float(), nil
		}
	}
	return 0, errors.New("balance check response is invalid")
}

// usdToCnyCents 把美元余额按配置汇率转成 RMB 分，异常值钳制为零喵。
func usdToCnyCents(usd float64) int64 {
	// 喵~防御：非有限值或负数按零处理，避免污染展示或余额喵。
	if math.IsNaN(usd) || math.IsInf(usd, 0) || usd < 0 {
		return 0
	}
	ratio := operation_setting.USDExchangeRate
	// 喵~防御：汇率配置异常时按零处理喵。
	if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio <= 0 {
		return 0
	}
	cents := math.Round(usd * ratio * 100)
	// 喵~防御：超大结果钳制到 int64 安全范围喵。
	if cents >= float64(math.MaxInt64) {
		return math.MaxInt64
	}
	return int64(cents)
}
