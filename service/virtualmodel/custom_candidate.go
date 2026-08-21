package virtualmodel

import (
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

// ExecuteCustomCandidate 尝试当前自定义候选并仅在成功响应写入时向客户端提交内容喵。
func ExecuteCustomCandidate(c *gin.Context, input CustomCandidateExecutionInput) error {
	// 喵~防御：Gin 上下文、请求和候选必要字段缺失时拒绝执行，避免产生未认证外发请求喵。
	if c == nil || c.Request == nil || input.CandidateID <= 0 || strings.TrimSpace(input.BaseURL) == "" || strings.TrimSpace(input.APIKey) == "" || strings.TrimSpace(input.RealModelName) == "" {
		return errors.New("custom candidate execution input is invalid")
	}
	parsedBaseURL, validateURLError := ValidateCustomBaseURL(input.BaseURL)
	if validateURLError != nil {
		return validateURLError
	}
	requestBody, bodyError := rewrittenCustomRequestBody(c, input.RealModelName)
	if bodyError != nil {
		return bodyError
	}
	upstreamURL, targetURLError := buildCustomUpstreamURL(parsedBaseURL, c.Request.URL)
	if targetURLError != nil {
		return targetURLError
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
		return fmt.Errorf("create custom upstream request: %w", requestError)
	}
	copyCustomUpstreamHeaders(upstreamRequest.Header, c.Request.Header)
	if authError := applyCustomCandidateAuth(upstreamRequest.Header, input.AuthStyle, input.APIKey); authError != nil {
		return authError
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
	// 喵~防御：只在确定成功后复制响应头和状态，允许编排器在错误候选之间安全切换喵。
	copyCustomResponseHeaders(c.Writer.Header(), response.Header)
	c.Status(response.StatusCode)
	// 主人注意：成功流式响应通过 io.Copy 按块转发，不会把不可信上游响应整体读入内存喵。
	if _, copyError := io.Copy(c.Writer, response.Body); copyError != nil {
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
	upstreamURL := *baseURL
	basePath := strings.TrimSuffix(baseURL.Path, "/")
	baseEscapedPath := strings.TrimSuffix(baseURL.EscapedPath(), "/")
	if baseEscapedPath == "" && basePath != "" {
		baseEscapedPath = url.PathEscape(basePath)
	}
	if requestPath == "" {
		requestPath = "/"
	}
	requestEscapedPath := requestURL.EscapedPath()
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
