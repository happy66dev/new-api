package virtualmodel

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaykitypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// CustomCandidateExecutionInput 描述一次自定义候选透传所需的脱敏配置喵。
type CustomCandidateExecutionInput struct {
	CandidateID    int
	BaseURL        string
	APIKey         string
	RealModelName  string
	AuthStyle      model.VirtualModelAuthStyle
	TimeoutSeconds int
	// StallTimeoutSeconds 静默多久判定流式卡流，单位：秒；零使用默认 60 喵。
	StallTimeoutSeconds int
	// MinContentChars 探测放流前需累积的内容字符门槛，零使用默认 10 喵。
	MinContentChars int
	// ProbeTotalTimeoutSeconds 探测阶段总预算，单位：秒；零使用默认 300 喵。
	ProbeTotalTimeoutSeconds int
	// CustomHeaders 结构化自定义请求头 JSON（如 {"*": true, "User-Agent": "..."}），空表示不追加喵。
	CustomHeaders string
	// FieldReplacements 字段值映射表 JSON（如 {"reasoning_effort": {"max": "xhigh"}}），空表示不替换喵。
	FieldReplacements string
	// FakeStreamEnabled 流转伪流开关：开启后流式响应全量缓存到 [DONE] 再一次性伪流回放，中途中断判定断流喵。
	FakeStreamEnabled bool
}

// CustomCandidateExecutionFailure 表示自定义候选在响应提交前可安全编排的失败结果喵。
type CustomCandidateExecutionFailure struct {
	Failure         CandidateFailure // 标准化失败信息，用于 retry、next、freeze 或 passthrough 规则决策喵。
	ResponseHeaders http.Header      // 上游错误响应头副本，仅在 passthrough 时经二次过滤后使用喵。
	ResponseBody    []byte           // 上游错误响应正文的受限缓冲，仅在 passthrough 时原样返回喵。
	Cause           error            // 原始内部错误，仅供服务器日志或调用链包装，禁止直接回显给客户端喵。
}

// Error 实现 error 接口并只返回受控错误分类，避免泄露网络或凭据细节喵。
func (executionFailure *CustomCandidateExecutionFailure) Error() string {
	// 喵~防御：空失败对象返回统一错误，避免错误处理路径出现空指针喵。
	if executionFailure == nil {
		return "custom candidate execution failed"
	}
	return "custom candidate execution failed: " + executionFailure.Failure.ErrorClass
}

// UpstreamStreamError 描述上游在流式阶段报告的错误事件喵。
// SSEBytes 为探测阶段已缓冲的上游 SSE 错误事件字节，供直调透传或失败规则 passthrough 时原样回放喵。
type UpstreamStreamError struct {
	SSEBytes []byte // 已缓冲的上游 SSE 错误事件字节（data: {"error":...}），空表示没有可回放内容喵。
	Cause    error  // 原始错误原因，仅供服务器日志包装，禁止直接回显给客户端喵。
}

// Error 实现 error 接口并返回受控分类文案喵。
func (streamError *UpstreamStreamError) Error() string {
	// 喵~防御：空错误对象或缺失底层错误时返回统一文案喵。
	if streamError == nil || streamError.Cause == nil {
		return "custom upstream stream reported an error"
	}
	return streamError.Cause.Error()
}

// Unwrap 暴露底层错误供 errors.Is/errors.As 穿透分类喵。
func (streamError *UpstreamStreamError) Unwrap() error {
	// 喵~防御：空错误对象返回 nil，避免空指针喵。
	if streamError == nil {
		return nil
	}
	return streamError.Cause
}

// customCandidatePrecommitBufferLimit 限制响应提交前的探测缓冲，避免异常上游耗尽服务内存喵。
const customCandidatePrecommitBufferLimit = 2 * 1024 * 1024

// customCandidatePrecommitFailure 将任何尚未提交的候选执行错误统一转成可编排结果喵。
func customCandidatePrecommitFailure(cause error) *CustomCandidateExecutionFailure {
	// 喵~防御：错误分类只使用规范化结果，避免将连接细节或凭据写回客户端喵。
	return &CustomCandidateExecutionFailure{
		Failure: NormalizeCandidateFailure(0, nil, nil, cause),
		Cause:   cause,
	}
}

// isStreamingCustomRequest 从可复用 JSON 请求确认客户端是否请求 SSE 流式输出喵。
func isStreamingCustomRequest(c *gin.Context) bool {
	// 喵~防御：缺少 Gin 上下文或请求时按非流式处理，避免空指针导致错误提交喵。
	if c == nil || c.Request == nil {
		return false
	}
	bodyStorage, storageError := common.GetBodyStorage(c)
	if storageError != nil {
		return false
	}
	requestBody, bodyError := bodyStorage.Bytes()
	if bodyError != nil || !gjson.ValidBytes(requestBody) {
		return false
	}
	return gjson.GetBytes(requestBody, "stream").Type == gjson.True
}

// probeCustomStreamingResponse 在同一响应 reader 上缓冲至内容字符达到门槛喵。
// 静默超过 stall 秒数判定卡流、探测总预算耗尽判定超时，均不向客户端写任何字节喵。
func probeCustomStreamingResponse(responseReader *bufio.Reader, params ProbeParameters) ([]byte, error) {
	// 喵~防御：空 reader 不能安全探测，直接返回结构化候选失败喵。
	if responseReader == nil {
		return nil, errors.New("custom upstream streaming response is unavailable")
	}
	// 参数非法或为零时回退内置默认值，保证探测永远有界喵。
	stallTimeout := time.Duration(DefaultProbeStallTimeoutSeconds) * time.Second
	if params.StallTimeoutSeconds > 0 {
		stallTimeout = time.Duration(params.StallTimeoutSeconds) * time.Second
	}
	probeTotalTimeout := time.Duration(DefaultProbeTotalTimeoutSeconds) * time.Second
	if params.ProbeTotalTimeoutSeconds > 0 {
		probeTotalTimeout = time.Duration(params.ProbeTotalTimeoutSeconds) * time.Second
	}
	minContentChars := DefaultProbeMinContentChars
	if params.MinContentChars > 0 {
		minContentChars = params.MinContentChars
	}
	probeStartTime := time.Now()
	bufferedBytes := make([]byte, 0, 4096)
	// 已累积的内容字符数，达到门槛才判定上游健康喵。
	bufferedContentChars := 0
	for len(bufferedBytes) < customCandidatePrecommitBufferLimit {
		// 喵~防御：探测总预算耗尽时终止，避免上游一直发无内容心跳拖死候选链喵。
		if time.Since(probeStartTime) >= probeTotalTimeout {
			return nil, fmt.Errorf("%w: probe phase exceeded total budget", relaykitypes.ErrStalledStream)
		}
		lineBytes, readError := readProbeLineWithTimeout(responseReader, stallTimeout)
		if len(lineBytes) > 0 {
			if len(bufferedBytes)+len(lineBytes) > customCandidatePrecommitBufferLimit {
				return nil, errors.New("custom upstream stream precommit buffer limit exceeded")
			}
			bufferedBytes = append(bufferedBytes, lineBytes...)
			trimmedLine := strings.TrimSpace(string(lineBytes))
			if strings.HasPrefix(trimmedLine, "data:") {
				dataPayload := strings.TrimSpace(strings.TrimPrefix(trimmedLine, "data:"))
				// 喵~防御：空事件与 [DONE] 不构成业务内容，继续等待喵。
				if dataPayload == "" || strings.EqualFold(dataPayload, "[DONE]") {
					continue
				}
				// 喵~防御：上游明确 error 事件在提交前转为携带错误事件字节的候选失败，供直调透传或失败规则 passthrough 原样回放喵。
				if strings.Contains(strings.ToLower(dataPayload), "\"error\"") || strings.Contains(strings.ToLower(dataPayload), "\"type\":\"error\"") {
					return nil, &UpstreamStreamError{SSEBytes: bufferedBytes, Cause: errors.New("custom upstream stream reported an error before business content")}
				}
				// 喵~防御：仅显式心跳不构成业务内容，继续等待有效 data 事件喵。
				if strings.EqualFold(dataPayload, "ping") || strings.EqualFold(dataPayload, "pong") {
					continue
				}
				// 累积内容字符，达到门槛才判定健康并放流喵。
				bufferedContentChars += common.StreamProbeContentChars(dataPayload)
				if bufferedContentChars >= minContentChars {
					return bufferedBytes, nil
				}
			}
		}
		if readError != nil {
			if errors.Is(readError, io.EOF) {
				// 喵~防御：流在达到内容门槛前结束视为空流，不提交给客户端喵。
				return nil, errors.New("custom upstream returned an empty streaming response")
			}
			return nil, readError
		}
	}
	return nil, errors.New("custom upstream stream precommit buffer limit exceeded")
}

// readProbeLineWithTimeout 读取一行 SSE 数据，超过静默秒数未读到新行则返回卡流哨兵喵。
func readProbeLineWithTimeout(reader *bufio.Reader, stallTimeout time.Duration) ([]byte, error) {
	// 喵~防御：非法超时或空 reader 时直接按卡流处理，避免无限阻塞喵。
	if reader == nil || stallTimeout <= 0 {
		return nil, relaykitypes.ErrStalledStream
	}
	type lineReadResult struct {
		lineBytes []byte
		readError error
	}
	// 每次读行启动一个 goroutine，配合 select 实现“距上次读到字节”的静默计时喵。
	resultChannel := make(chan lineReadResult, 1)
	go func() {
		lineBytes, readError := reader.ReadBytes('\n')
		resultChannel <- lineReadResult{lineBytes: lineBytes, readError: readError}
	}()
	select {
	case result := <-resultChannel:
		return result.lineBytes, result.readError
	case <-time.After(stallTimeout):
		// 超时后调用方关闭响应体会解除阻塞读，goroutine 写入有缓冲的 channel 后自然退出，不泄漏喵。
		return nil, fmt.Errorf("%w: upstream stream silent for %s", relaykitypes.ErrStalledStream, stallTimeout)
	}
}

// CustomCandidateExecutionResult 描述自定义候选透传结果与解析出的 usage/TTFT 喵。
type CustomCandidateExecutionResult struct {
	// Usage 上游返回的 usage（可能为 nil 表示未提供计费信息）喵。
	Usage *dto.Usage
	// TtftMs 从发起请求到收到响应头的毫秒数，零表示未测到喵。
	TtftMs int64
	// Err 透传失败（响应提交前），成功时为空喵。
	Err error
}

// ExecuteCustomCandidate 尝试当前自定义候选并仅在成功响应写入时向客户端提交内容喵。
// 返回结构携带解析出的 usage 与 TTFT，供虚拟模型日志与状态探测使用喵。
func ExecuteCustomCandidate(c *gin.Context, input CustomCandidateExecutionInput) *CustomCandidateExecutionResult {
	// 喵~防御：Gin 上下文、请求和候选必要字段缺失时拒绝执行，避免产生未认证外发请求喵。
	// CandidateID 为 0 时表示用户上游模型独立直接调用（无候选链身份），同样允许执行喵。
	if c == nil || c.Request == nil || strings.TrimSpace(input.BaseURL) == "" || strings.TrimSpace(input.APIKey) == "" || strings.TrimSpace(input.RealModelName) == "" {
		return &CustomCandidateExecutionResult{Err: customCandidatePrecommitFailure(errors.New("custom candidate execution input is invalid"))}
	}
	parsedBaseURL, validateURLError := ValidateCustomBaseURL(input.BaseURL)
	if validateURLError != nil {
		return &CustomCandidateExecutionResult{Err: customCandidatePrecommitFailure(validateURLError)}
	}
	requestBody, bodyError := rewrittenCustomRequestBody(c, input.RealModelName, input.FieldReplacements)
	if bodyError != nil {
		return &CustomCandidateExecutionResult{Err: customCandidatePrecommitFailure(bodyError)}
	}
	upstreamURL, targetURLError := buildCustomUpstreamURL(parsedBaseURL, c.Request.URL)
	if targetURLError != nil {
		return &CustomCandidateExecutionResult{Err: customCandidatePrecommitFailure(targetURLError)}
	}
	requestContext := c.Request.Context()
	candidateTimeout := time.Duration(input.TimeoutSeconds) * time.Second
	// 喵~防御：候选超时必须落在固定安全范围，防止错误配置占用连接或立即取消请求喵。
	if candidateTimeout < time.Second || candidateTimeout > 10*time.Minute {
		candidateTimeout = 60 * time.Second
	}
	requestContext, cancelRequest := context.WithTimeout(requestContext, candidateTimeout)
	defer cancelRequest()
	upstreamRequest, requestError := http.NewRequestWithContext(requestContext, c.Request.Method, upstreamURL.String(), strings.NewReader(string(requestBody)))
	if requestError != nil {
		return &CustomCandidateExecutionResult{Err: customCandidatePrecommitFailure(fmt.Errorf("create custom upstream request: %w", requestError))}
	}
	copyCustomUpstreamHeaders(upstreamRequest.Header, c.Request.Header)
	if authError := applyCustomCandidateAuth(upstreamRequest.Header, input.AuthStyle, input.APIKey); authError != nil {
		return &CustomCandidateExecutionResult{Err: customCandidatePrecommitFailure(authError)}
	}
	// 自定义请求头在复制客户端头与认证头之后应用，认证头仍由 auth_style 独占，其余键覆盖同名客户端头喵。
	if headersError := applyCustomUpstreamHeaders(upstreamRequest.Header, input.CustomHeaders); headersError != nil {
		return &CustomCandidateExecutionResult{Err: customCandidatePrecommitFailure(headersError)}
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
		return &CustomCandidateExecutionResult{Err: &CustomCandidateExecutionFailure{Failure: NormalizeCandidateFailure(0, nil, nil, responseError), Cause: responseError}, TtftMs: ttftMs}
	}
	defer response.Body.Close()
	// 喵~防御：仅 2xx 状态可提交为成功；重定向和其他协议状态必须进入候选规则处理喵。
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		responseBody, readError := io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
		if readError != nil {
			return &CustomCandidateExecutionResult{Err: &CustomCandidateExecutionFailure{Failure: NormalizeCandidateFailure(response.StatusCode, response.Header, nil, readError), Cause: readError}, TtftMs: ttftMs}
		}
		if len(responseBody) > 64*1024 {
			responseBody = responseBody[:64*1024]
		}
		return &CustomCandidateExecutionResult{Err: &CustomCandidateExecutionFailure{Failure: NormalizeCandidateFailure(response.StatusCode, response.Header, responseBody, nil), ResponseHeaders: response.Header.Clone(), ResponseBody: responseBody, Cause: errors.New("custom upstream returned an error status")}, TtftMs: ttftMs}
	}
	// 喵~防御：2xx 响应在确认存在有效业务内容前不得提交，避免空流或 SSE 错误阻断候选故障转移喵。
	responseReader := bufio.NewReader(response.Body)
	if isStreamingCustomRequest(c) {
		// 流转伪流：全量缓存到 [DONE] 后一次性流式回放，抵抗上游网络波动断流喵。
		if input.FakeStreamEnabled {
			usage, fakeStreamError := fakeStreamCommitResponse(c, responseReader, response.Header, response.StatusCode, input)
			if fakeStreamError != nil {
				return &CustomCandidateExecutionResult{Err: customCandidatePrecommitFailure(fakeStreamError), TtftMs: ttftMs}
			}
			return &CustomCandidateExecutionResult{Usage: usage, TtftMs: ttftMs}
		}
		precommitBuffer, precommitError := probeCustomStreamingResponse(responseReader, ProbeParameters{
			StallTimeoutSeconds:      input.StallTimeoutSeconds,
			MinContentChars:          input.MinContentChars,
			ProbeTotalTimeoutSeconds: input.ProbeTotalTimeoutSeconds,
		})
		if precommitError != nil {
			// 上游流式阶段报告 SSE 错误事件：把已缓冲的错误事件字节作为可透传响应体返回，供直调透传或失败规则 passthrough 使用喵。
			if streamError, isStreamError := precommitError.(*UpstreamStreamError); isStreamError && len(streamError.SSEBytes) > 0 {
				return &CustomCandidateExecutionResult{Err: &CustomCandidateExecutionFailure{
					Failure:         NormalizeCandidateFailure(response.StatusCode, response.Header, streamError.SSEBytes, nil),
					ResponseHeaders: response.Header.Clone(),
					ResponseBody:    streamError.SSEBytes,
					Cause:           streamError.Cause,
				}, TtftMs: ttftMs}
			}
			return &CustomCandidateExecutionResult{Err: customCandidatePrecommitFailure(precommitError), TtftMs: ttftMs}
		}
		copyCustomResponseHeaders(c.Writer.Header(), response.Header)
		c.Status(response.StatusCode)
		usage := &dto.Usage{}
		// 探测缓冲里可能已包含带 usage 的事件，先提取再回放喵。
		extractUsageFromSSEBytes(precommitBuffer, usage)
		// 首次有内容的写响应才打点，供请求级首字统计喵。
		if len(precommitBuffer) > 0 {
			markVirtualModelFirstWrite(c)
		}
		if _, writeError := c.Writer.Write(precommitBuffer); writeError != nil {
			return &CustomCandidateExecutionResult{Err: fmt.Errorf("write committed custom upstream response: %w", writeError), TtftMs: ttftMs}
		}
		// 逐行转发剩余 SSE 事件，同时从 data 载荷提取 usage 喵。
		for {
			lineBytes, readError := readLimitedSSELine(responseReader)
			if len(lineBytes) > 0 {
				extractUsageFromSSELine(lineBytes, usage)
				// 幂等打点：探测缓冲为空时首个有内容行才是真正首字节喵。
				markVirtualModelFirstWrite(c)
				if _, writeError := c.Writer.Write(lineBytes); writeError != nil {
					return &CustomCandidateExecutionResult{Err: fmt.Errorf("write committed custom upstream response: %w", writeError), TtftMs: ttftMs}
				}
			}
			if readError != nil {
				if errors.Is(readError, io.EOF) {
					break
				}
				return &CustomCandidateExecutionResult{Err: fmt.Errorf("read committed custom upstream stream: %w", readError), TtftMs: ttftMs}
			}
		}
		usage = normalizeUpstreamModelUsage(usage)
		// 只有真正解析到 token 计数才返回 usage，避免空 usage 对象混入日志喵。
		if !usageHasTokens(usage) {
			return &CustomCandidateExecutionResult{TtftMs: ttftMs}
		}
		return &CustomCandidateExecutionResult{Usage: usage, TtftMs: ttftMs}
	}
	// 非流式：先尝试缓冲读取并解析顶层 usage，超过上限时退回流式原样转发喵。
	responseBody, readBodyError := io.ReadAll(io.LimitReader(responseReader, userUpstreamNonStreamingBodyLimit+1))
	if readBodyError != nil {
		return &CustomCandidateExecutionResult{Err: customCandidatePrecommitFailure(readBodyError), TtftMs: ttftMs}
	}
	if len(responseBody) > userUpstreamNonStreamingBodyLimit {
		// 超限大响应：不解析 usage，把已读内容与剩余流合并原样转发喵。
		copyCustomResponseHeaders(c.Writer.Header(), response.Header)
		c.Status(response.StatusCode)
		// 超限大响应首次写即首字，供请求级首字统计喵。
		markVirtualModelFirstWrite(c)
		if _, copyError := io.Copy(c.Writer, io.MultiReader(bytes.NewReader(responseBody), responseReader)); copyError != nil {
			return &CustomCandidateExecutionResult{Err: fmt.Errorf("copy committed custom upstream response: %w", copyError), TtftMs: ttftMs}
		}
		return &CustomCandidateExecutionResult{TtftMs: ttftMs}
	}
	// 喵~防御：空正文成功响应视为异常，不提交空业务结果喵。
	if len(responseBody) == 0 {
		return &CustomCandidateExecutionResult{Err: customCandidatePrecommitFailure(errors.New("custom upstream returned an empty success response")), TtftMs: ttftMs}
	}
	usage := normalizeUpstreamModelUsage(extractUsageFromOpenAIBody(responseBody))
	copyCustomResponseHeaders(c.Writer.Header(), response.Header)
	c.Status(response.StatusCode)
	// 非流式首次写响应即首字，供请求级首字统计喵。
	markVirtualModelFirstWrite(c)
	if _, writeError := c.Writer.Write(responseBody); writeError != nil {
		return &CustomCandidateExecutionResult{Err: fmt.Errorf("write committed custom upstream response: %w", writeError), TtftMs: ttftMs}
	}
	return &CustomCandidateExecutionResult{Usage: usage, TtftMs: ttftMs}
}

// rewrittenCustomRequestBody 读取可复用 JSON 请求并改写 model 字段与配置的请求字段替换喵。
// fieldReplacements 是字段路径 → 旧值→新值映射表 JSON；路径以 messages 开头的对话内容一律不替换喵。
func rewrittenCustomRequestBody(c *gin.Context, realModelName string, fieldReplacements string) ([]byte, error) {
	// 喵~防御：当前自定义候选仅接收 JSON，以避免在 multipart 或表单中错误重写用户上传内容喵。
	if !strings.HasPrefix(strings.ToLower(c.Request.Header.Get("Content-Type")), "application/json") {
		return nil, errors.New("custom candidate only supports JSON requests")
	}
	bodyStorage, storageError := common.GetBodyStorage(c)
	if storageError != nil {
		return nil, storageError
	}
	requestBody, bodyError := bodyStorage.Bytes()
	if bodyError != nil || !gjson.ValidBytes(requestBody) {
		return nil, errors.New("custom candidate request body is invalid")
	}
	modelValue := gjson.GetBytes(requestBody, "model")
	if !modelValue.Exists() || modelValue.Type != gjson.String {
		return nil, errors.New("custom candidate request model must be a string")
	}
	rewrittenBody, rewriteError := sjson.SetBytes(requestBody, "model", realModelName)
	if rewriteError != nil {
		return nil, rewriteError
	}
	// 字段值映射表：只替换请求参数标量，绝不触碰 messages 对话内容喵。
	replacementMap, parseError := parseFieldReplacements(fieldReplacements)
	if parseError != nil {
		return nil, parseError
	}
	for fieldPath, valueMap := range replacementMap {
		// 喵~防御：路径以 messages 开头（对话内容）一律跳过，防止改写用户对话喵。
		if strings.HasPrefix(strings.TrimSpace(fieldPath), "messages") {
			continue
		}
		currentValue := gjson.GetBytes(rewrittenBody, fieldPath)
		// 喵~防御：字段不存在或非字符串标量时不替换，保持请求原样喵。
		if !currentValue.Exists() || currentValue.Type != gjson.String {
			continue
		}
		// 命中映射旧值才替换为新值，未命中的值原样保留喵。
		if replacementValue, hit := valueMap[currentValue.String()]; hit {
			rewrittenBody, rewriteError = sjson.SetBytes(rewrittenBody, fieldPath, replacementValue)
			if rewriteError != nil {
				return nil, rewriteError
			}
		}
	}
	return rewrittenBody, nil
}

// ValidateFieldReplacementsJSON 校验字段替换映射表 JSON 配置喵。
// 路径非空且不得以 messages 开头（对话内容绝不替换），供保存时拒绝非法配置喵。
func ValidateFieldReplacementsJSON(fieldReplacements string) error {
	replacementMap, err := parseFieldReplacements(fieldReplacements)
	if err != nil {
		return err
	}
	for fieldPath := range replacementMap {
		// 喵~防御：以 messages 开头的路径会改写对话内容，一律拒绝保存喵。
		if strings.HasPrefix(strings.TrimSpace(fieldPath), "messages") {
			return errors.New("field replacement path must not start with messages")
		}
	}
	return nil
}

// parseFieldReplacements 解析并校验字段替换映射表 JSON，返回字段路径 → 旧值→新值映射喵。
func parseFieldReplacements(fieldReplacements string) (map[string]map[string]string, error) {
	// 喵~防御：空配置直接跳过，避免无意义的解析喵。
	if strings.TrimSpace(fieldReplacements) == "" {
		return nil, nil
	}
	var rawMap map[string]map[string]string
	if err := common.UnmarshalJsonStr(fieldReplacements, &rawMap); err != nil {
		return nil, errors.New("field replacements JSON is invalid")
	}
	for fieldPath, valueMap := range rawMap {
		// 喵~防御：空路径与空新旧值一律拒绝，防止生成无意义的替换规则喵。
		if strings.TrimSpace(fieldPath) == "" {
			return nil, errors.New("field replacement path is empty")
		}
		for oldValue, newValue := range valueMap {
			if strings.TrimSpace(oldValue) == "" || strings.TrimSpace(newValue) == "" {
				return nil, errors.New("field replacement value is empty")
			}
		}
	}
	return rawMap, nil
}

// buildCustomUpstreamURL 保留原始 escaped path 与 query，并在 Base URL 下安全拼接路径喵。
func buildCustomUpstreamURL(baseURL *url.URL, requestURL *url.URL) (*url.URL, error) {
	// 喵~防御：缺少 URL 时拒绝请求，避免将流量意外发往 Base URL 根路径喵。
	if baseURL == nil || requestURL == nil {
		return nil, errors.New("custom upstream URL is invalid")
	}
	requestPath := requestURL.Path
	// 喵~防御：拒绝点路径段及其编码等价形式，防止客户端越过用户配置的 Base URL 路径前缀喵。
	for _, pathSegment := range strings.Split(requestPath, "/") {
		if pathSegment == "." || pathSegment == ".." {
			return nil, errors.New("custom upstream request path is invalid")
		}
	}
	requestEscapedPath := requestURL.EscapedPath()
	// 游乐场经 /pg/chat/completions 入口透传时，必须把 /pg/ 归一化为 /v1/，
	// 否则上游（常见为另一 new-api 网关）会把请求打到其走 UserAuth 认证的 /pg 路径，
	// 对 API Key 校验失败返回 AUTH_UNAUTHORIZED；归一化后与原生 channel relay 的上游路径语义一致喵。
	if strings.HasPrefix(requestPath, "/pg/") {
		requestPath = "/v1/" + strings.TrimPrefix(requestPath, "/pg/")
		if strings.HasPrefix(requestEscapedPath, "/pg/") {
			requestEscapedPath = "/v1/" + strings.TrimPrefix(requestEscapedPath, "/pg/")
		}
	}
	upstreamURL := *baseURL
	basePath := strings.TrimSuffix(baseURL.Path, "/")
	baseEscapedPath := strings.TrimSuffix(baseURL.EscapedPath(), "/")
	if baseEscapedPath == "" && basePath != "" {
		baseEscapedPath = url.PathEscape(basePath)
	}
	if requestPath == "" {
		requestPath = "/"
	}
	if requestEscapedPath == "" {
		requestEscapedPath = "/"
	}
	// 喵~防御：Path 保存解码路径而 RawPath 保存可验证转义路径，避免 %2F 被二次编码为 %252F 喵。
	upstreamURL.Path = basePath + requestPath
	upstreamURL.RawPath = baseEscapedPath + requestEscapedPath
	upstreamURL.RawQuery = requestURL.RawQuery
	return &upstreamURL, nil
}

// blockedCustomUpstreamHeaderNames 返回客户端不可设置的请求头名（认证与 hop-by-hop 语义头）喵。
// 认证头由 applyCustomCandidateAuth 独占注入，hop-by-hop 头会破坏代理连接语义喵。
func blockedCustomUpstreamHeaderNames() map[string]struct{} {
	return map[string]struct{}{
		"authorization": {}, "x-api-key": {}, "proxy-authorization": {}, "cookie": {}, "connection": {}, "keep-alive": {}, "proxy-connection": {}, "te": {}, "trailer": {}, "transfer-encoding": {}, "upgrade": {}, "host": {}, "content-length": {}, "forwarded": {}, "x-forwarded-for": {}, "x-forwarded-host": {}, "x-forwarded-proto": {}, "x-real-ip": {},
	}
}

// isBlockedCustomUpstreamHeader 判断请求头名是否属于危险头（大小写不敏感）喵。
func isBlockedCustomUpstreamHeader(headerName string) bool {
	_, blocked := blockedCustomUpstreamHeaderNames()[strings.ToLower(strings.TrimSpace(headerName))]
	return blocked
}

// copyCustomUpstreamHeaders 复制安全请求头并移除客户端可控的认证和 hop-by-hop 字段喵。
func copyCustomUpstreamHeaders(targetHeaders http.Header, sourceHeaders http.Header) {
	// 定义固定危险头集合，防止客户端影响代理连接语义或伪造上游认证喵。
	blockedHeaders := blockedCustomUpstreamHeaderNames()
	// 喵~防御：Connection 头可声明额外 hop-by-hop 字段，必须一并阻断以防客户端控制上游连接语义喵。
	for _, connectionValue := range sourceHeaders.Values("Connection") {
		for _, connectionToken := range strings.Split(connectionValue, ",") {
			if normalizedToken := strings.ToLower(strings.TrimSpace(connectionToken)); normalizedToken != "" {
				blockedHeaders[normalizedToken] = struct{}{}
			}
		}
	}
	for headerName, headerValues := range sourceHeaders {
		if _, blocked := blockedHeaders[strings.ToLower(headerName)]; blocked {
			continue
		}
		for _, headerValue := range headerValues {
			targetHeaders.Add(headerName, headerValue)
		}
	}
}

// ValidateCustomUpstreamHeadersJSON 校验自定义请求头 JSON 配置是否可安全应用喵。
// 供保存时拒绝非法配置；执行时 applyCustomUpstreamHeaders 内部也会再次防御喵。
func ValidateCustomUpstreamHeadersJSON(customHeadersJSON string) error {
	return applyCustomUpstreamHeaders(nil, customHeadersJSON)
}

// applyCustomUpstreamHeaders 解析结构化自定义请求头 JSON 并覆盖到目标请求头喵。
// "*" 为语义标记表示对全部请求生效，本身不是请求头；其余键覆盖同名客户端头喵。
func applyCustomUpstreamHeaders(headers http.Header, customHeadersJSON string) error {
	// 喵~防御：空配置直接跳过，避免无意义的解析喵。
	if strings.TrimSpace(customHeadersJSON) == "" {
		return nil
	}
	var customHeaders map[string]any
	if err := common.UnmarshalJsonStr(customHeadersJSON, &customHeaders); err != nil {
		return errors.New("custom upstream headers JSON is invalid")
	}
	for headerName, headerValue := range customHeaders {
		// "*" 标记跳过，仅表示对所有请求生效喵。
		if headerName == "*" {
			continue
		}
		// 喵~防御：空头名与危险头一律拒绝，防止客户端覆盖认证或破坏代理语义喵。
		if strings.TrimSpace(headerName) == "" {
			return errors.New("custom upstream header name is empty")
		}
		if isBlockedCustomUpstreamHeader(headerName) {
			return errors.New("custom upstream header is not allowed: " + headerName)
		}
		// 喵~防御：头值必须是字符串，拒绝布尔/数字等不可序列化的值喵。
		valueText, ok := headerValue.(string)
		if !ok {
			return errors.New("custom upstream header value must be a string: " + headerName)
		}
		if headers != nil {
			headers.Set(headerName, valueText)
		}
	}
	return nil
}

// applyCustomCandidateAuth 按候选认证方式注入唯一的上游凭据喵。
func applyCustomCandidateAuth(headers http.Header, authStyle model.VirtualModelAuthStyle, apiKey string) error {
	// 喵~防御：清除所有认证头后再注入，保证客户端无法追加竞争认证信息喵。
	headers.Del("Authorization")
	headers.Del("x-api-key")
	switch authStyle {
	case model.VirtualModelAuthBearer:
		headers.Set("Authorization", "Bearer "+apiKey)
	case model.VirtualModelAuthAPIKey, model.VirtualModelAuthAnthropic:
		headers.Set("x-api-key", apiKey)
	default:
		return errors.New("custom candidate authentication style is invalid")
	}
	return nil
}

// CopyCustomPassthroughResponse 过滤错误响应头后将受限上游失败安全回传给客户端喵。
// statusCode 允许 2xx-5xx：上游流式阶段在 2xx 响应内报告 SSE error 事件时，同样原样透传错误正文喵。
func CopyCustomPassthroughResponse(writer http.ResponseWriter, responseHeaders http.Header, statusCode int, responseBody []byte) {
	// 喵~防御：缺少 writer 或非法状态（1xx/6xx 或以下）时不写响应，避免空指针或伪造协议状态喵。
	if writer == nil || statusCode < http.StatusOK || statusCode > 599 {
		return
	}
	copyCustomResponseHeaders(writer.Header(), responseHeaders)
	writer.WriteHeader(statusCode)
	// 喵~防御：正文来自受限缓冲（SSE 探测缓冲或 64 KiB 错误正文）；写入失败无法安全恢复，因此仅结束当前响应喵。
	_, _ = writer.Write(responseBody)
}

// copyCustomResponseHeaders 过滤上游 hop-by-hop 响应头后复制其余字段喵。
func copyCustomResponseHeaders(targetHeaders http.Header, sourceHeaders http.Header) {
	// 喵~防御：上游不得为 new-api 域写入 Cookie、跨域、安全策略或连接控制头喵。
	blockedHeaders := map[string]struct{}{
		"connection": {}, "keep-alive": {}, "proxy-authenticate": {}, "proxy-authorization": {}, "te": {}, "trailer": {}, "transfer-encoding": {}, "upgrade": {}, "set-cookie": {}, "access-control-allow-origin": {}, "access-control-allow-credentials": {}, "access-control-expose-headers": {}, "content-security-policy": {}, "strict-transport-security": {}, "x-frame-options": {}, "x-content-type-options": {}, "permissions-policy": {},
	}
	for headerName, headerValues := range sourceHeaders {
		if _, blocked := blockedHeaders[strings.ToLower(headerName)]; blocked {
			continue
		}
		for _, headerValue := range headerValues {
			targetHeaders.Add(headerName, headerValue)
		}
	}
}

// StrictCustomHTTPClient 导出严格拨号策略客户端，供同库嗅探余额等场景复用喵。
func StrictCustomHTTPClient(timeout time.Duration) *http.Client {
	return strictCustomHTTPClient(timeout)
}

// strictCustomHTTPClient 创建不信任环境代理、禁止重定向且固定 DNS 校验拨号的专用客户端喵。
func strictCustomHTTPClient(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           strictCustomDialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: timeout,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		// 喵~防御：不自动跟随重定向，防止已校验公开站点跳转到内部网络喵。
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// strictCustomDialContext 在实际拨号前解析并验证全部 DNS 结果，再固定使用已验证 IP 喵。
func strictCustomDialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	host, portText, splitError := net.SplitHostPort(address)
	if splitError != nil {
		return nil, errors.New("custom upstream dial address is invalid")
	}
	port, portError := strconv.Atoi(portText)
	// 开发模式（VIRTUAL_MODEL_INSECURE_UPSTREAM=1）直接按原地址拨号，跳过端口与公网 IP 校验喵。
	if allowInsecureCustomUpstream() {
		return (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, address)
	}
	// 喵~防御：端口必须落在合法范围（1-65535），允许 http（80）与非 443 端口，公网可达性由下方 IP 校验兜底喵。
	if portError != nil || port <= 0 || port > 65535 {
		return nil, errors.New("custom upstream port is invalid")
	}
	// 喵~防御：主机直接为 IP 时同样执行公开地址检查，防止绕过 DNS 路径喵。
	if literalIP := net.ParseIP(host); literalIP != nil {
		if !isPublicCustomUpstreamIP(literalIP) {
			return nil, errors.New("custom upstream resolves to a non-public address")
		}
		return (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, net.JoinHostPort(literalIP.String(), portText))
	}
	resolvedAddresses, resolveError := net.DefaultResolver.LookupIPAddr(ctx, host)
	if resolveError != nil || len(resolvedAddresses) == 0 {
		return nil, errors.New("custom upstream DNS resolution failed")
	}
	for _, resolvedAddress := range resolvedAddresses {
		if !isPublicCustomUpstreamIP(resolvedAddress.IP) {
			return nil, errors.New("custom upstream resolves to a non-public address")
		}
	}
	var lastDialError error
	for _, resolvedAddress := range resolvedAddresses {
		connection, dialError := (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, net.JoinHostPort(resolvedAddress.IP.String(), portText))
		if dialError == nil {
			return connection, nil
		}
		lastDialError = dialError
	}
	if lastDialError != nil {
		return nil, lastDialError
	}
	return nil, errors.New("custom upstream has no usable IP address")
}

// isPublicCustomUpstreamIP 拒绝回环、私网、链路本地、多播、文档及保留网络喵。
func isPublicCustomUpstreamIP(ip net.IP) bool {
	// 喵~防御：nil、未指定、回环、私网和链路本地地址一律不可作为用户上游目标喵。
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	if ipv4 := ip.To4(); ipv4 != nil {
		firstOctet := ipv4[0]
		secondOctet := ipv4[1]
		// 拒绝 this-network、CGNAT、基准测试、文档、保留和广播范围喵。
		if firstOctet == 0 || firstOctet >= 224 || (firstOctet == 100 && secondOctet >= 64 && secondOctet <= 127) || (firstOctet == 198 && (secondOctet == 18 || secondOctet == 19)) || (firstOctet == 192 && secondOctet == 0) || (firstOctet == 198 && secondOctet == 51) || (firstOctet == 203 && secondOctet == 0) {
			return false
		}
	}
	return true
}
