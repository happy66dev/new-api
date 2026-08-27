package virtualmodel

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// UserUpstreamModelExecutionResult 描述透传结果与解析出的 usage 喵。
// Err 为 nil 表示成功且响应已向客户端提交；失败时 Err 可能是 *CustomCandidateExecutionFailure 喵。
type UserUpstreamModelExecutionResult struct {
	Usage *dto.Usage
	Err   error
	// TtftMs 从发起上游请求到收到响应头的时间（毫秒），响应头到达≈首字节喵。
	TtftMs int64
}

// userUpstreamNonStreamingBodyLimit 限制非流式响应正文的最大缓冲，防御异常上游耗尽内存喵。
const userUpstreamNonStreamingBodyLimit = 8 * 1024 * 1024

// userUpstreamStreamLineLimit 限制单条流式事件行的最大长度，防御超长行拖垮内存喵。
const userUpstreamStreamLineLimit = 1024 * 1024

// ExecuteUserUpstreamModel 执行用户上游模型透传并解析响应 usage 喵。
// 成功时返回解析出的 usage（可能为 nil 表示上游未提供计费信息），响应已原样提交喵。
func ExecuteUserUpstreamModel(c *gin.Context, input CustomCandidateExecutionInput) *UserUpstreamModelExecutionResult {
	// 喵~防御：输入字段缺失或格式非法时返回可编排失败，避免外发未认证请求喵。
	if c == nil || c.Request == nil || strings.TrimSpace(input.BaseURL) == "" || strings.TrimSpace(input.APIKey) == "" || strings.TrimSpace(input.RealModelName) == "" {
		return &UserUpstreamModelExecutionResult{Err: customCandidatePrecommitFailure(errors.New("user upstream model execution input is invalid"))}
	}
	parsedBaseURL, validateURLError := ValidateCustomBaseURL(input.BaseURL)
	if validateURLError != nil {
		return &UserUpstreamModelExecutionResult{Err: customCandidatePrecommitFailure(validateURLError)}
	}
	requestBody, bodyError := rewrittenCustomRequestBody(c, input.RealModelName)
	if bodyError != nil {
		return &UserUpstreamModelExecutionResult{Err: customCandidatePrecommitFailure(bodyError)}
	}
	upstreamURL, targetURLError := buildCustomUpstreamURL(parsedBaseURL, c.Request.URL)
	if targetURLError != nil {
		return &UserUpstreamModelExecutionResult{Err: customCandidatePrecommitFailure(targetURLError)}
	}
	requestContext := c.Request.Context()
	candidateTimeout := time.Duration(input.TimeoutSeconds) * time.Second
	// 喵~防御：超时必须落在固定安全范围，防止错误配置占用连接或立即取消请求喵。
	if candidateTimeout < time.Second || candidateTimeout > 10*time.Minute {
		candidateTimeout = 60 * time.Second
	}
	requestContext, cancelRequest := context.WithTimeout(requestContext, candidateTimeout)
	defer cancelRequest()
	upstreamRequest, requestError := http.NewRequestWithContext(requestContext, c.Request.Method, upstreamURL.String(), strings.NewReader(string(requestBody)))
	if requestError != nil {
		return &UserUpstreamModelExecutionResult{Err: customCandidatePrecommitFailure(fmt.Errorf("create user upstream request: %w", requestError))}
	}
	copyCustomUpstreamHeaders(upstreamRequest.Header, c.Request.Header)
	if authError := applyCustomCandidateAuth(upstreamRequest.Header, input.AuthStyle, input.APIKey); authError != nil {
		return &UserUpstreamModelExecutionResult{Err: customCandidatePrecommitFailure(authError)}
	}
	upstreamRequest.ContentLength = int64(len(requestBody))
	upstreamRequest.Header.Set("Content-Length", strconv.FormatInt(upstreamRequest.ContentLength, 10))
	// 发起上游请求前打点，用于测量首字节（TTFT）喵。
	execStart := time.Now()
	response, responseError := strictCustomHTTPClient(candidateTimeout).Do(upstreamRequest)
	// 响应头到达即首字节，流式与非流式都成立喵。
	ttftMs := int64(0)
	if responseError == nil {
		ttftMs = time.Since(execStart).Milliseconds()
	}
	if responseError != nil {
		return &UserUpstreamModelExecutionResult{Err: &CustomCandidateExecutionFailure{Failure: NormalizeCandidateFailure(0, nil, nil, responseError), Cause: responseError}, TtftMs: ttftMs}
	}
	defer response.Body.Close()
	// 喵~防御：仅 2xx 状态可提交为成功，其余状态进入受控错误透传喵。
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		responseBody, readError := io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
		if readError != nil {
			return &UserUpstreamModelExecutionResult{Err: &CustomCandidateExecutionFailure{Failure: NormalizeCandidateFailure(response.StatusCode, response.Header, nil, readError), Cause: readError}, TtftMs: ttftMs}
		}
		if len(responseBody) > 64*1024 {
			responseBody = responseBody[:64*1024]
		}
		return &UserUpstreamModelExecutionResult{Err: &CustomCandidateExecutionFailure{Failure: NormalizeCandidateFailure(response.StatusCode, response.Header, responseBody, nil), ResponseHeaders: response.Header.Clone(), ResponseBody: responseBody, Cause: errors.New("user upstream returned an error status")}, TtftMs: ttftMs}
	}
	responseReader := bufio.NewReader(response.Body)
	// 流式请求逐事件转发并累积最后的 usage 事件喵。
	if isStreamingCustomRequest(c) {
		precommitBuffer, precommitError := probeCustomStreamingResponse(responseReader)
		if precommitError != nil {
			return &UserUpstreamModelExecutionResult{Err: customCandidatePrecommitFailure(precommitError), TtftMs: ttftMs}
		}
		copyCustomResponseHeaders(c.Writer.Header(), response.Header)
		c.Status(response.StatusCode)
		usage := &dto.Usage{}
		// 探测缓冲里可能已包含带 usage 的事件，先提取再回放喵。
		extractUsageFromSSEBytes(precommitBuffer, usage)
		if _, writeError := c.Writer.Write(precommitBuffer); writeError != nil {
			return &UserUpstreamModelExecutionResult{Err: fmt.Errorf("write committed user upstream response: %w", writeError), TtftMs: ttftMs}
		}
		// 逐行转发剩余 SSE 事件，同时从 data 载荷提取 usage 喵。
		for {
			lineBytes, readError := readLimitedSSELine(responseReader)
			if len(lineBytes) > 0 {
				extractUsageFromSSELine(lineBytes, usage)
				if _, writeError := c.Writer.Write(lineBytes); writeError != nil {
					return &UserUpstreamModelExecutionResult{Err: fmt.Errorf("write committed user upstream response: %w", writeError), TtftMs: ttftMs}
				}
			}
			if readError != nil {
				if errors.Is(readError, io.EOF) {
					break
				}
				return &UserUpstreamModelExecutionResult{Err: fmt.Errorf("read committed user upstream stream: %w", readError), TtftMs: ttftMs}
			}
		}
		// 只有真正解析到 token 计数才返回 usage，避免空 usage 对象混入日志喵。
		if usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0 {
			return &UserUpstreamModelExecutionResult{Usage: nil, TtftMs: ttftMs}
		}
		return &UserUpstreamModelExecutionResult{Usage: usage, TtftMs: ttftMs}
	}
	// 非流式：读完整正文并解析顶层 usage 后原样转发喵。
	responseBody, readBodyError := io.ReadAll(io.LimitReader(response.Body, userUpstreamNonStreamingBodyLimit+1))
	if readBodyError != nil {
		return &UserUpstreamModelExecutionResult{Err: customCandidatePrecommitFailure(readBodyError), TtftMs: ttftMs}
	}
	// 喵~防御：正文超过缓冲上限视为异常上游，返回可编排失败喵。
	if len(responseBody) > userUpstreamNonStreamingBodyLimit {
		return &UserUpstreamModelExecutionResult{Err: customCandidatePrecommitFailure(errors.New("user upstream non-streaming response is too large")), TtftMs: ttftMs}
	}
	// 喵~防御：空正文成功响应视为异常，不提交空业务结果喵。
	if len(responseBody) == 0 {
		return &UserUpstreamModelExecutionResult{Err: customCandidatePrecommitFailure(errors.New("user upstream returned an empty success response")), TtftMs: ttftMs}
	}
	usage := extractUsageFromOpenAIBody(responseBody)
	copyCustomResponseHeaders(c.Writer.Header(), response.Header)
	c.Status(response.StatusCode)
	if _, writeError := c.Writer.Write(responseBody); writeError != nil {
		return &UserUpstreamModelExecutionResult{Err: fmt.Errorf("write committed user upstream response: %w", writeError), TtftMs: ttftMs}
	}
	return &UserUpstreamModelExecutionResult{Usage: usage, TtftMs: ttftMs}
}

// readLimitedSSELine 读取一行 SSE 数据并限制最大长度，超长时截断返回喵。
func readLimitedSSELine(reader *bufio.Reader) ([]byte, error) {
	// 喵~防御：空 reader 直接返回 EOF，避免空指针喵。
	if reader == nil {
		return nil, io.EOF
	}
	lineBytes, readError := reader.ReadBytes('\n')
	// 喵~防御：单行超长时截断到安全上限，避免超大行占满内存喵。
	if len(lineBytes) > userUpstreamStreamLineLimit {
		lineBytes = lineBytes[:userUpstreamStreamLineLimit]
	}
	return lineBytes, readError
}

// extractUsageFromOpenAIBody 从非流式 OpenAI 兼容响应体提取顶层 usage 喵。
func extractUsageFromOpenAIBody(body []byte) *dto.Usage {
	// 喵~防御：非法或缺失 usage 的正文返回 nil，表示无可计费信息喵。
	if !gjson.ValidBytes(body) || !gjson.GetBytes(body, "usage").Exists() {
		return nil
	}
	var payload struct {
		Usage dto.Usage `json:"usage"`
	}
	// 喵~防御：解析失败不影响透传结果，只丢失计费信息喵。
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil
	}
	// 只有真正包含 token 计数才返回 usage 喵。
	if payload.Usage.PromptTokens == 0 && payload.Usage.CompletionTokens == 0 && payload.Usage.TotalTokens == 0 {
		return nil
	}
	return &payload.Usage
}

// extractUsageFromSSEBytes 从已缓冲的 SSE 文本中提取 usage 事件喵。
func extractUsageFromSSEBytes(buffered []byte, target *dto.Usage) {
	// 喵~防御：空缓冲或空目标直接返回喵。
	if len(buffered) == 0 || target == nil {
		return
	}
	scanner := bufio.NewScanner(bytes.NewReader(buffered))
	// 探测缓冲较小，使用固定较大缓冲容纳长事件行喵。
	scanner.Buffer(make([]byte, 4096), userUpstreamStreamLineLimit)
	for scanner.Scan() {
		extractUsageFromSSELine(scanner.Bytes(), target)
	}
}

// extractUsageFromSSELine 从单条 SSE data 行提取 usage 事件，非空时覆盖目标喵。
func extractUsageFromSSELine(lineBytes []byte, target *dto.Usage) {
	// 喵~防御：空目标或非 data 行直接返回喵。
	if target == nil {
		return
	}
	trimmedLine := bytes.TrimSpace(lineBytes)
	if !bytes.HasPrefix(trimmedLine, []byte("data:")) {
		return
	}
	dataPayload := bytes.TrimSpace(trimmedLine[len("data:"):])
	// 喵~防御：空载荷或 [DONE] 事件不含 usage 喵。
	if len(dataPayload) == 0 || bytes.Equal(dataPayload, []byte("[DONE]")) {
		return
	}
	if !gjson.ValidBytes(dataPayload) {
		return
	}
	usageRaw := gjson.GetBytes(dataPayload, "usage")
	// 喵~防御：无 usage 字段的事件直接跳过喵。
	if !usageRaw.Exists() {
		return
	}
	var usage dto.Usage
	// 喵~防御：解析失败只丢弃该事件，不影响后续事件喵。
	if err := common.Unmarshal([]byte(usageRaw.Raw), &usage); err != nil {
		return
	}
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0 {
		return
	}
	// 流式事件里的 usage 取最后一次出现的非空值，符合 OpenAI 流式末尾 usage 语义喵。
	*target = usage
}
