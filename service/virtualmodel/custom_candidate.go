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

// probeCustomStreamingResponse 在同一响应 reader 上缓冲至首个有效 SSE 业务事件喵。
func probeCustomStreamingResponse(responseReader *bufio.Reader) ([]byte, error) {
	// 喵~防御：空 reader 不能安全探测，直接返回结构化候选失败喵。
	if responseReader == nil {
		return nil, errors.New("custom upstream streaming response is unavailable")
	}
	bufferedBytes := make([]byte, 0, 4096)
	for len(bufferedBytes) < customCandidatePrecommitBufferLimit {
		lineBytes, readError := responseReader.ReadBytes('\n')
		if len(lineBytes) > 0 {
			if len(bufferedBytes)+len(lineBytes) > customCandidatePrecommitBufferLimit {
				return nil, errors.New("custom upstream stream precommit buffer limit exceeded")
			}
			bufferedBytes = append(bufferedBytes, lineBytes...)
			trimmedLine := strings.TrimSpace(string(lineBytes))
			if strings.HasPrefix(trimmedLine, "data:") {
				dataPayload := strings.TrimSpace(strings.TrimPrefix(trimmedLine, "data:"))
				// 喵~防御：上游明确 error 事件在提交前转为候选失败，允许后备候选接管喵。
				if dataPayload == "" || strings.EqualFold(dataPayload, "[DONE]") {
					continue
				}
				if strings.Contains(strings.ToLower(dataPayload), "\"error\"") || strings.Contains(strings.ToLower(dataPayload), "\"type\":\"error\"") {
					return nil, errors.New("custom upstream stream reported an error before business content")
				}
				// 喵~防御：仅显式心跳不构成业务内容，继续等待有效 data 事件喵。
				if strings.EqualFold(dataPayload, "ping") || strings.EqualFold(dataPayload, "pong") {
					continue
				}
				return bufferedBytes, nil
			}
		}
		if readError != nil {
			if errors.Is(readError, io.EOF) {
				return nil, errors.New("custom upstream returned an empty streaming response")
			}
			return nil, readError
		}
	}
	return nil, errors.New("custom upstream stream precommit buffer limit exceeded")
}

// ExecuteCustomCandidate 尝试当前自定义候选并仅在成功响应写入时向客户端提交内容喵。
func ExecuteCustomCandidate(c *gin.Context, input CustomCandidateExecutionInput) error {
	// 喵~防御：Gin 上下文、请求和候选必要字段缺失时拒绝执行，避免产生未认证外发请求喵。
	// CandidateID 为 0 时表示用户上游模型独立直接调用（无候选链身份），同样允许执行喵。
	if c == nil || c.Request == nil || strings.TrimSpace(input.BaseURL) == "" || strings.TrimSpace(input.APIKey) == "" || strings.TrimSpace(input.RealModelName) == "" {
		return customCandidatePrecommitFailure(errors.New("custom candidate execution input is invalid"))
	}
	parsedBaseURL, validateURLError := ValidateCustomBaseURL(input.BaseURL)
	if validateURLError != nil {
		return customCandidatePrecommitFailure(validateURLError)
	}
	requestBody, bodyError := rewrittenCustomRequestBody(c, input.RealModelName)
	if bodyError != nil {
		return customCandidatePrecommitFailure(bodyError)
	}
	upstreamURL, targetURLError := buildCustomUpstreamURL(parsedBaseURL, c.Request.URL)
	if targetURLError != nil {
		return customCandidatePrecommitFailure(targetURLError)
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
		return customCandidatePrecommitFailure(fmt.Errorf("create custom upstream request: %w", requestError))
	}
	copyCustomUpstreamHeaders(upstreamRequest.Header, c.Request.Header)
	if authError := applyCustomCandidateAuth(upstreamRequest.Header, input.AuthStyle, input.APIKey); authError != nil {
		return customCandidatePrecommitFailure(authError)
	}
	upstreamRequest.ContentLength = int64(len(requestBody))
	upstreamRequest.Header.Set("Content-Length", strconv.FormatInt(upstreamRequest.ContentLength, 10))
	response, responseError := strictCustomHTTPClient(candidateTimeout).Do(upstreamRequest)
	if responseError != nil {
		return &CustomCandidateExecutionFailure{Failure: NormalizeCandidateFailure(0, nil, nil, responseError), Cause: responseError}
	}
	defer response.Body.Close()
	// 喵~防御：仅 2xx 状态可提交为成功；重定向和其他协议状态必须进入候选规则处理喵。
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		responseBody, readError := io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
		if readError != nil {
			return &CustomCandidateExecutionFailure{Failure: NormalizeCandidateFailure(response.StatusCode, response.Header, nil, readError), Cause: readError}
		}
		if len(responseBody) > 64*1024 {
			responseBody = responseBody[:64*1024]
		}
		return &CustomCandidateExecutionFailure{Failure: NormalizeCandidateFailure(response.StatusCode, response.Header, responseBody, nil), ResponseHeaders: response.Header.Clone(), ResponseBody: responseBody, Cause: errors.New("custom upstream returned an error status")}
	}
	// 喵~防御：2xx 响应在确认存在有效业务内容前不得提交，避免空流或 SSE 错误阻断候选故障转移喵。
	responseReader := bufio.NewReader(response.Body)
	if isStreamingCustomRequest(c) {
		precommitBuffer, precommitError := probeCustomStreamingResponse(responseReader)
		if precommitError != nil {
			return customCandidatePrecommitFailure(precommitError)
		}
		copyCustomResponseHeaders(c.Writer.Header(), response.Header)
		c.Status(response.StatusCode)
		// 喵~防御：预提交缓冲先回放，再继续读取同一个上游 iterator，避免重发请求或重复事件喵。
		if _, copyError := io.Copy(c.Writer, io.MultiReader(bytes.NewReader(precommitBuffer), responseReader)); copyError != nil {
			return fmt.Errorf("copy committed custom upstream response: %w", copyError)
		}
		return nil
	}
	// 喵~防御：非流式成功响应同样至少读取一个字节后才提交，避免把空 2xx 误当作成功喵。
	firstByte, firstByteError := responseReader.ReadByte()
	if firstByteError != nil {
		if errors.Is(firstByteError, io.EOF) {
			return customCandidatePrecommitFailure(errors.New("custom upstream returned an empty success response"))
		}
		return customCandidatePrecommitFailure(firstByteError)
	}
	copyCustomResponseHeaders(c.Writer.Header(), response.Header)
	c.Status(response.StatusCode)
	if _, copyError := io.Copy(c.Writer, io.MultiReader(bytes.NewReader([]byte{firstByte}), responseReader)); copyError != nil {
		// 喵~防御：一旦成功响应开始写入，禁止将错误反馈为可切换候选，避免重复或混合协议响应喵。
		return fmt.Errorf("copy committed custom upstream response: %w", copyError)
	}
	return nil
}

// rewrittenCustomRequestBody 读取可复用 JSON 请求并仅修改顶层 model 字段喵。
func rewrittenCustomRequestBody(c *gin.Context, realModelName string) ([]byte, error) {
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
	return rewrittenBody, nil
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

// copyCustomUpstreamHeaders 复制安全请求头并移除客户端可控的认证和 hop-by-hop 字段喵。
func copyCustomUpstreamHeaders(targetHeaders http.Header, sourceHeaders http.Header) {
	// 定义固定危险头集合，防止客户端影响代理连接语义或伪造上游认证喵。
	blockedHeaders := map[string]struct{}{
		"authorization": {}, "x-api-key": {}, "proxy-authorization": {}, "cookie": {}, "connection": {}, "keep-alive": {}, "proxy-connection": {}, "te": {}, "trailer": {}, "transfer-encoding": {}, "upgrade": {}, "host": {}, "content-length": {}, "forwarded": {}, "x-forwarded-for": {}, "x-forwarded-host": {}, "x-forwarded-proto": {}, "x-real-ip": {},
	}
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
func CopyCustomPassthroughResponse(writer http.ResponseWriter, responseHeaders http.Header, statusCode int, responseBody []byte) {
	// 喵~防御：缺少 writer 或非法失败状态时不写响应，避免空指针或伪造成功状态喵。
	if writer == nil || statusCode < http.StatusBadRequest || statusCode > 599 {
		return
	}
	copyCustomResponseHeaders(writer.Header(), responseHeaders)
	writer.WriteHeader(statusCode)
	// 喵~防御：正文来自 64 KiB 受限缓冲；写入失败无法安全恢复，因此仅结束当前响应喵。
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
	if portError != nil || port != 443 {
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
