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
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	relaykitypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
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

// markVirtualModelFirstWrite 记录虚拟模型 custom/user-xxx 候选首次向客户端写响应的时刻喵。
// 幂等：只有首次写（context 中尚无时间）才打点，供调用方计算请求级首字耗时喵。
func markVirtualModelFirstWrite(c *gin.Context) {
	// 喵~防御：空上下文直接返回喵。
	if c == nil {
		return
	}
	// 已记录过首次写则不再更新，保证首字取第一次写响应的时刻喵。
	if !common.GetContextKeyTime(c, constant.ContextKeyVirtualModelFirstWriteAt).IsZero() {
		return
	}
	common.SetContextKey(c, constant.ContextKeyVirtualModelFirstWriteAt, time.Now())
}

// userUpstreamNonStreamingBodyLimit 限制非流式响应正文的最大缓冲，防御异常上游耗尽内存喵。
const userUpstreamNonStreamingBodyLimit = 8 * 1024 * 1024

// userUpstreamStreamLineLimit 限制单条流式事件行的最大长度，防御超长行拖垮内存喵。
const userUpstreamStreamLineLimit = 1024 * 1024

// fakeStreamBufferLimit 流转伪流全量缓存的上限，防御异常上游无限流拖垮内存喵。
// 伪流模式要求完整缓存到 [DONE] 才回放，超大流在此模式下按断流处理回切候选喵。
const fakeStreamBufferLimit = 16 * 1024 * 1024

// fakeStreamCommitResponse 伪流模式：全量缓存到 [DONE] 后一次性流式回放喵。
// 返回解析出的 usage（无 token 时按请求/响应文本估计，估计不出时为 nil）；失败返回断流/写入错误供调用方编排喵。
func fakeStreamCommitResponse(c *gin.Context, responseReader *bufio.Reader, responseHeaders http.Header, statusCode int, input CustomCandidateExecutionInput, requestBody []byte) (*dto.Usage, error) {
	stallTimeout := time.Duration(DefaultProbeStallTimeoutSeconds) * time.Second
	if input.StallTimeoutSeconds > 0 {
		stallTimeout = time.Duration(input.StallTimeoutSeconds) * time.Second
	}
	probeTotalTimeout := time.Duration(DefaultProbeTotalTimeoutSeconds) * time.Second
	if input.ProbeTotalTimeoutSeconds > 0 {
		probeTotalTimeout = time.Duration(input.ProbeTotalTimeoutSeconds) * time.Second
	}
	// 全量缓存到 [DONE]，中途中断按断流分类处理喵。
	fakeStreamBuffer, fakeStreamError := bufferCustomStreamToDone(responseReader, stallTimeout, probeTotalTimeout)
	if fakeStreamError != nil {
		return nil, fakeStreamError
	}
	copyCustomResponseHeaders(c.Writer.Header(), responseHeaders)
	c.Status(statusCode)
	usage := &dto.Usage{}
	// 回放前先从全量缓存提取 usage 事件喵。
	extractUsageFromSSEBytes(fakeStreamBuffer, usage)
	// 伪流一次性回放开始即首次写响应，供请求级首字统计喵。
	markVirtualModelFirstWrite(c)
	if _, writeError := c.Writer.Write(fakeStreamBuffer); writeError != nil {
		return nil, fmt.Errorf("write committed fake stream response: %w", writeError)
	}
	usage = normalizeUpstreamModelUsage(usage)
	// 无 token 时用全量缓存里的响应文本估计，供计费与日志展示喵。
	var responseTextBuilder strings.Builder
	if !usageHasTokens(usage) {
		appendResponseContentFromSSEBytes(&responseTextBuilder, fakeStreamBuffer)
		return service.EstimateUsageFromTexts(input.RealModelName, requestBody, responseTextBuilder.String()), nil
	}
	// 上游只给了部分 token：用全量缓存响应文本估算缺失侧，避免输出 token 为 0 喵。
	appendResponseContentFromSSEBytes(&responseTextBuilder, fakeStreamBuffer)
	usage = fillEstimatedUsageIfMissing(input.RealModelName, usage, requestBody, responseTextBuilder.String())
	return usage, nil
}

// bufferCustomStreamToDone 在伪流模式下读取整个 SSE 流直到 [DONE]，返回完整行字节缓冲喵。
// 中途 EOF、静默超时或总预算耗尽且未见 [DONE] 时返回断流哨兵错误，供断流处理措施决策喵。
func bufferCustomStreamToDone(responseReader *bufio.Reader, stallTimeout time.Duration, probeTotalTimeout time.Duration) ([]byte, error) {
	// 喵~防御：空 reader 无法缓存，直接按断流处理喵。
	if responseReader == nil {
		return nil, fmt.Errorf("%w: custom upstream stream is unavailable", relaykitypes.ErrStreamCut)
	}
	bufferedBytes := make([]byte, 0, 4096)
	probeStartTime := time.Now()
	// 伪流阶段的总预算截止时刻，传入读行函数作为硬上限，防止 stall > total 时预算被单次读行绕过喵。
	probeDeadline := time.Time{}
	if probeTotalTimeout > 0 {
		probeDeadline = probeStartTime.Add(probeTotalTimeout)
	}
	for {
		// 喵~防御：总预算耗尽仍未到 [DONE]，判定断流喵。
		if probeTotalTimeout > 0 && time.Since(probeStartTime) >= probeTotalTimeout {
			return nil, fmt.Errorf("%w: stream cut before [DONE], total budget exceeded", relaykitypes.ErrStreamCut)
		}
		lineBytes, readError := readProbeLineWithTimeout(responseReader, stallTimeout, probeDeadline)
		if len(lineBytes) > 0 {
			// 喵~防御：全量缓存超限判定断流，避免异常上游耗尽服务内存喵。
			if len(bufferedBytes)+len(lineBytes) > fakeStreamBufferLimit {
				return nil, fmt.Errorf("%w: stream cut before [DONE], buffer limit exceeded", relaykitypes.ErrStreamCut)
			}
			bufferedBytes = append(bufferedBytes, lineBytes...)
			// 到达 [DONE] 或 Anthropic message_stop 即流完整，返回全量缓存供一次性回放喵。
			if isCustomStreamEndEvent(lineBytes) {
				return bufferedBytes, nil
			}
		}
		if readError != nil {
			// 总预算硬上限触发：即使单行未到也判定断流，保证伪流阶段受总预算约束喵。
			if errors.Is(readError, errProbeTotalBudgetExceeded) {
				return nil, fmt.Errorf("%w: stream cut before [DONE], total budget exceeded", relaykitypes.ErrStreamCut)
			}
			// 喵~防御：EOF 与静默超时都视为未完整返回，统一归入断流分类喵。
			if errors.Is(readError, io.EOF) {
				return nil, fmt.Errorf("%w: upstream stream cut before [DONE]", relaykitypes.ErrStreamCut)
			}
			return nil, fmt.Errorf("%w: %v", relaykitypes.ErrStreamCut, readError)
		}
	}
}

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
	requestBody, bodyError := rewrittenCustomRequestBody(c, input.RealModelName, input.FieldReplacements)
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
	// 自定义请求头在复制客户端头与认证头之后应用，认证头仍由 auth_style 独占，其余键覆盖同名客户端头喵。
	if headersError := applyCustomUpstreamHeaders(upstreamRequest.Header, input.CustomHeaders); headersError != nil {
		return &UserUpstreamModelExecutionResult{Err: customCandidatePrecommitFailure(headersError)}
	}
	// 透传 Anthropic /v1/messages 请求时补缺省版本头，避免上游拒绝未带版本号的请求喵。
	ensureAnthropicVersionHeader(upstreamRequest.Header, upstreamURL.Path)
	upstreamRequest.ContentLength = int64(len(requestBody))
	upstreamRequest.Header.Set("Content-Length", strconv.FormatInt(upstreamRequest.ContentLength, 10))
	// 注入回环检测标记：若 baseURL 指向本实例，入口 LoopGuard 中间件可拦截请求风暴循环喵。
	// 放在所有请求头改写之后，保证客户端或自定义头无法覆盖该标记喵。
	upstreamRequest.Header.Set(common.LoopGuardHeaderKey, common.BuildLoopGuardValue(c.GetString(common.RequestIdKey)))
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
		// 流转伪流：全量缓存到 [DONE] 后一次性流式回放，抵抗上游网络波动断流喵。
		if input.FakeStreamEnabled {
			usage, fakeStreamError := fakeStreamCommitResponse(c, responseReader, response.Header, response.StatusCode, input, requestBody)
			if fakeStreamError != nil {
				return &UserUpstreamModelExecutionResult{Err: customCandidatePrecommitFailure(fakeStreamError), TtftMs: ttftMs}
			}
			return &UserUpstreamModelExecutionResult{Usage: usage, TtftMs: ttftMs}
		}
		precommitBuffer, precommitError := probeCustomStreamingResponse(responseReader, ProbeParameters{
			StallTimeoutSeconds:      input.StallTimeoutSeconds,
			MinContentChars:          input.MinContentChars,
			ProbeTotalTimeoutSeconds: input.ProbeTotalTimeoutSeconds,
		})
		if precommitError != nil {
			// 上游流式阶段报告 SSE 错误事件：把已缓冲的错误事件字节作为可透传响应体返回，供直调透传或失败规则 passthrough 使用喵。
			if streamError, isStreamError := precommitError.(*UpstreamStreamError); isStreamError && len(streamError.SSEBytes) > 0 {
				return &UserUpstreamModelExecutionResult{Err: &CustomCandidateExecutionFailure{
					Failure:         NormalizeCandidateFailure(response.StatusCode, response.Header, streamError.SSEBytes, nil),
					ResponseHeaders: response.Header.Clone(),
					ResponseBody:    streamError.SSEBytes,
					Cause:           streamError.Cause,
				}, TtftMs: ttftMs}
			}
			return &UserUpstreamModelExecutionResult{Err: customCandidatePrecommitFailure(precommitError), TtftMs: ttftMs}
		}
		copyCustomResponseHeaders(c.Writer.Header(), response.Header)
		c.Status(response.StatusCode)
		usage := &dto.Usage{}
		// 累积响应文本用于 token 估计（上游无 usage 时的兜底计费素材）喵。
		var responseTextBuilder strings.Builder
		// 探测缓冲里可能已包含带 usage 的事件，先提取再回放喵。
		extractUsageFromSSEBytes(precommitBuffer, usage)
		appendResponseContentFromSSEBytes(&responseTextBuilder, precommitBuffer)
		// 首次有内容的写响应才打点，供请求级首字统计喵。
		if len(precommitBuffer) > 0 {
			markVirtualModelFirstWrite(c)
		}
		if _, writeError := c.Writer.Write(precommitBuffer); writeError != nil {
			return &UserUpstreamModelExecutionResult{Err: fmt.Errorf("write committed user upstream response: %w", writeError), TtftMs: ttftMs}
		}
		// 探测缓冲写出后立即刷新，保证首段 SSE 及时到达客户端喵。
		flushCustomResponse(c)
		// 逐行转发剩余 SSE 事件，同时从 data 载荷提取 usage 与响应文本喵。
		for {
			lineBytes, readError := readLimitedSSELine(responseReader)
			if len(lineBytes) > 0 {
				extractUsageFromSSELine(lineBytes, usage)
				appendResponseContentFromLine(&responseTextBuilder, lineBytes)
				// 幂等打点：探测缓冲为空时首个有内容行才是真正首字节喵。
				markVirtualModelFirstWrite(c)
				if _, writeError := c.Writer.Write(lineBytes); writeError != nil {
					return &UserUpstreamModelExecutionResult{Err: fmt.Errorf("write committed user upstream response: %w", writeError), TtftMs: ttftMs}
				}
				// 逐行刷新，SSE 事件及时推送避免客户端空等喵。
				flushCustomResponse(c)
			}
			if readError != nil {
				if errors.Is(readError, io.EOF) {
					break
				}
				return &UserUpstreamModelExecutionResult{Err: fmt.Errorf("read committed user upstream stream: %w", readError), TtftMs: ttftMs}
			}
		}
		// 只有真正解析到 token 计数才返回 usage，否则按请求/响应文本估计参与计费喵。
		usage = normalizeUpstreamModelUsage(usage)
		if !usageHasTokens(usage) {
			usage = service.EstimateUsageFromTexts(input.RealModelName, requestBody, responseTextBuilder.String())
		} else {
			// 上游只给了部分 token（如只有 prompt 无 completion）：用响应文本估算缺失侧，避免输出 token 为 0 喵。
			usage = fillEstimatedUsageIfMissing(input.RealModelName, usage, requestBody, responseTextBuilder.String())
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
	usage := normalizeUpstreamModelUsage(extractUsageFromOpenAIBody(responseBody))
	// 上游未提供 token 时按响应文本估计 completion，配合请求体估计 prompt 参与计费喵。
	if !usageHasTokens(usage) {
		usage = service.EstimateUsageFromTexts(input.RealModelName, requestBody, responseContentFromBody(responseBody))
	} else {
		// 上游只给了部分 token：用文本估算缺失侧，避免输出 token 为 0 喵。
		usage = fillEstimatedUsageIfMissing(input.RealModelName, usage, requestBody, responseContentFromBody(responseBody))
	}
	copyCustomResponseHeaders(c.Writer.Header(), response.Header)
	c.Status(response.StatusCode)
	// 非流式首次写响应即首字，供请求级首字统计喵。
	markVirtualModelFirstWrite(c)
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
	if !usageHasTokens(&payload.Usage) {
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

// extractUsageFromSSELine 从单条 SSE data 行提取 usage 事件，逐侧合并进目标喵。
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
	// 顶层 usage：OpenAI 流式末尾 + Anthropic message_delta 的用法喵。
	if usageRaw := gjson.GetBytes(dataPayload, "usage"); usageRaw.Exists() {
		var usage dto.Usage
		// 喵~防御：解析失败只丢弃该事件，不影响后续事件喵。
		if err := common.Unmarshal([]byte(usageRaw.Raw), &usage); err == nil {
			mergeUpstreamModelUsage(target, &usage)
		}
	}
	// Anthropic message_start 的 input_tokens 嵌在 message.usage 里，单独解析合并，避免输入被漏掉喵。
	if nestedUsageRaw := gjson.GetBytes(dataPayload, "message.usage"); nestedUsageRaw.Exists() {
		var usage dto.Usage
		if err := common.Unmarshal([]byte(nestedUsageRaw.Raw), &usage); err == nil {
			mergeUpstreamModelUsage(target, &usage)
		}
	}
}

// mergeUpstreamModelUsage 把候选 usage 合并进目标：候选非零字段覆盖目标，全零候选不改动任何字段喵。
// 覆盖语义：Anthropic 流式里 message_start 的 output_tokens 是占位 1，message_delta 才是最终累计值，
// 所以后到的非零值应覆盖；而 message_start 的 input_tokens 与 message_delta 的 output_tokens 是互补事件，
// 逐字段合并保证 input/output 都能拿到。全零 usage（上游每块都带的占位对象）绝不清空已有计数喵。
func mergeUpstreamModelUsage(target *dto.Usage, candidate *dto.Usage) {
	// 喵~防御：空指针直接返回喵。
	if target == nil || candidate == nil {
		return
	}
	if candidate.PromptTokens > 0 {
		target.PromptTokens = candidate.PromptTokens
	}
	if candidate.CompletionTokens > 0 {
		target.CompletionTokens = candidate.CompletionTokens
	}
	if candidate.TotalTokens > 0 {
		target.TotalTokens = candidate.TotalTokens
	}
	if candidate.InputTokens > 0 {
		target.InputTokens = candidate.InputTokens
	}
	if candidate.OutputTokens > 0 {
		target.OutputTokens = candidate.OutputTokens
	}
	if candidate.PromptCacheHitTokens > 0 {
		target.PromptCacheHitTokens = candidate.PromptCacheHitTokens
	}
	// 缓存/推理细节字段：候选非零同样覆盖，保证后到的完整值生效喵。
	if candidate.PromptTokensDetails.CachedTokens > 0 {
		target.PromptTokensDetails.CachedTokens = candidate.PromptTokensDetails.CachedTokens
	}
	if candidate.PromptTokensDetails.CacheCreationInputTokens > 0 {
		target.PromptTokensDetails.CacheCreationInputTokens = candidate.PromptTokensDetails.CacheCreationInputTokens
	}
	if candidate.PromptTokensDetails.CacheReadInputTokens > 0 {
		target.PromptTokensDetails.CacheReadInputTokens = candidate.PromptTokensDetails.CacheReadInputTokens
	}
	if candidate.CompletionTokenDetails.ReasoningTokens > 0 {
		target.CompletionTokenDetails.ReasoningTokens = candidate.CompletionTokenDetails.ReasoningTokens
	}
	if candidate.ClaudeCacheCreation5mTokens > 0 {
		target.ClaudeCacheCreation5mTokens = candidate.ClaudeCacheCreation5mTokens
	}
	if candidate.ClaudeCacheCreation1hTokens > 0 {
		target.ClaudeCacheCreation1hTokens = candidate.ClaudeCacheCreation1hTokens
	}
}

// usageHasTokens 判断 usage 是否携带任何 token 计数（含 input/output 风格字段与推理 token）喵。
func usageHasTokens(usage *dto.Usage) bool {
	// 喵~防御：空 usage 视为无 token 喵。
	if usage == nil {
		return false
	}
	return usage.PromptTokens != 0 || usage.CompletionTokens != 0 || usage.TotalTokens != 0 ||
		usage.InputTokens != 0 || usage.OutputTokens != 0 ||
		// 推理 token 单独上报时也算有计费信息，避免整条 usage 被当成无 token 重新估算喵。
		usage.CompletionTokenDetails.ReasoningTokens != 0
}

// fillEstimatedUsageIfMissing 在上游 usage 缺失某部分 token 时，用请求/响应文本估算补全喵。
// 语义：usage 有真实 prompt 或 completion 时保留真实值，只对为零的那一侧按文本估算，
// 并把 usage 标记为估计来源（BillingUsage.Estimated=true），供日志「?」与计费识别喵。
// 适用场景：上游流式只返回 prompt 未返回 completion（或反之），导致输出 token 显示/计费为 0 喵。
func fillEstimatedUsageIfMissing(modelName string, usage *dto.Usage, requestBody []byte, responseText string) *dto.Usage {
	// 喵~防御：空 usage 直接返回，由调用方决定是否整体估算喵。
	if usage == nil {
		return nil
	}
	promptMissing := usage.PromptTokens <= 0
	completionMissing := usage.CompletionTokens <= 0
	// 两侧都有真实 token 时无需补全喵。
	if !promptMissing && !completionMissing {
		return usage
	}
	// 用文本估算完整 usage 作为素材，只取缺失侧的估算值，不覆盖真实侧喵。
	estimated := service.EstimateUsageFromTexts(modelName, requestBody, responseText)
	if estimated == nil {
		return usage
	}
	filledSomething := false
	if promptMissing && estimated.PromptTokens > 0 {
		usage.PromptTokens = estimated.PromptTokens
		filledSomething = true
	}
	if completionMissing && estimated.CompletionTokens > 0 {
		usage.CompletionTokens = estimated.CompletionTokens
		filledSomething = true
	}
	if !filledSomething {
		return usage
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	// 补全了估算侧：标记 usage 为估计来源，供日志「?」与计费识别喵。
	if usage.BillingUsage == nil {
		usage.BillingUsage = &dto.BillingUsage{Estimated: true}
	}
	return usage
}

// normalizeUpstreamModelUsage 把各厂商 usage 字段统一到 new-api 标准口径喵。
// 缓存命中 token 可能出现在 prompt_tokens_details.cached_tokens、prompt_cache_hit_tokens
// 或 input_tokens_details.cached_tokens 之一，统一回填到 prompt_tokens_details.cached_tokens，
// 使计费（缓存价）与缓存命中率统计都拿到正确的缓存量喵。
func normalizeUpstreamModelUsage(usage *dto.Usage) *dto.Usage {
	// 喵~防御：空 usage 直接返回，避免空指针喵。
	if usage == nil {
		return nil
	}
	// Anthropic/Responses 风格的 input_tokens/output_tokens 回填到 prompt/completion 标准字段喵。
	if usage.PromptTokens == 0 && usage.InputTokens > 0 {
		usage.PromptTokens = usage.InputTokens
	}
	if usage.CompletionTokens == 0 && usage.OutputTokens > 0 {
		usage.CompletionTokens = usage.OutputTokens
	}
	// 推理 token 计入输出：部分上游（DeepSeek/xAI）把推理单独放 completion_tokens_details.reasoning_tokens，
	// 而 completion_tokens 为 0（或只算正文），此时把推理并入输出，保证输出 token 包含思考量喵。
	if usage.CompletionTokens == 0 && usage.CompletionTokenDetails.ReasoningTokens > 0 {
		usage.CompletionTokens = usage.CompletionTokenDetails.ReasoningTokens
	}
	// 缓存命中回填：标准字段优先，其次 DeepSeek 的 prompt_cache_hit_tokens，再 Anthropic 的 input_tokens_details.cached_tokens 喵。
	// 回填后计费按 prompt_tokens - cached_tokens 计算基础输入价，等价于把缓存命中从输入扣除喵。
	if usage.PromptTokensDetails.CachedTokens == 0 {
		if usage.PromptCacheHitTokens > 0 {
			usage.PromptTokensDetails.CachedTokens = usage.PromptCacheHitTokens
		} else if usage.InputTokensDetails != nil && usage.InputTokensDetails.CachedTokens > 0 {
			usage.PromptTokensDetails.CachedTokens = usage.InputTokensDetails.CachedTokens
		}
	}
	// 防御钳制：异常负值统一归零，防止上游异常把计费与命中率拉偏喵。
	if usage.PromptTokens < 0 {
		usage.PromptTokens = 0
	}
	if usage.CompletionTokens < 0 {
		usage.CompletionTokens = 0
	}
	if usage.PromptTokensDetails.CachedTokens < 0 {
		usage.PromptTokensDetails.CachedTokens = 0
	}
	return usage
}
