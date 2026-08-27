package virtualmodel

import (
	"errors"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// allowInsecureCustomUpstream 是否允许不安全自定义上游（http/IP/非 443）喵。
// 仅当显式设置 VIRTUAL_MODEL_INSECURE_UPSTREAM=1 时开启，供本地开发与冒烟测试使用喵。
// 主人注意：生产环境务必保持关闭，否则明文 API Key 会在公网传输喵。
func allowInsecureCustomUpstream() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("VIRTUAL_MODEL_INSECURE_UPSTREAM"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// ValidateCustomBaseURL 严格校验用户自定义上游的公开地址喵。
// 支持 http/https 与任意合法端口，公网可达性（防内网探测）由拨号阶段公网 IP 校验兜底喵。
func ValidateCustomBaseURL(rawBaseURL string) (*url.URL, error) {
	// 喵~防御：空地址、控制字符和过长输入会造成日志混淆或资源滥用，因此在解析前拒绝喵。
	trimmedBaseURL := strings.TrimSpace(rawBaseURL)
	if trimmedBaseURL == "" || len(trimmedBaseURL) > 2048 || strings.ContainsAny(trimmedBaseURL, "\r\n\x00") {
		return nil, errors.New("custom upstream URL is invalid")
	}
	parsedURL, parseError := url.Parse(trimmedBaseURL)
	if parseError != nil || parsedURL == nil {
		return nil, errors.New("custom upstream URL is invalid")
	}
	// 喵~防御：userinfo、fragment 与空 host 在任何模式都拒绝，避免认证混淆或不可预期目标语义喵。
	if parsedURL.User != nil || parsedURL.Fragment != "" || parsedURL.Hostname() == "" {
		return nil, errors.New("custom upstream URL contains unsupported components")
	}
	// 喵~防御：只允许 http/https scheme，端口必须是合法范围（1-65535），拒绝其他协议与畸形端口喵。
	// 公网可达性（防内网探测）由拨号阶段公网 IP 校验兜底，因此这里不再强制 443 端口，支持 http 与非 443 端口喵。
	scheme := strings.ToLower(parsedURL.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, errors.New("custom upstream URL must use HTTP or HTTPS")
	}
	if portText := parsedURL.Port(); portText != "" {
		portValue, portError := strconv.Atoi(portText)
		// 喵~防御：非数字端口或越界端口拒绝，避免畸形地址进入拨号喵。
		if portError != nil || portValue <= 0 || portValue > 65535 {
			return nil, errors.New("custom upstream URL port is invalid")
		}
	}
	// 喵~防御：生产环境要求 hostname 是可验证的域名而非字面 IP，避免绕过域名语义；开发模式显式放宽供本地 mock 使用喵。
	if !allowInsecureCustomUpstream() {
		if net.ParseIP(parsedURL.Hostname()) != nil {
			return nil, errors.New("custom upstream URL must use a public hostname")
		}
	}
	return parsedURL, nil
}

// SummarizeCustomBaseURL 返回可安全展示的来源摘要，不返回 path、query 或用户信息喵。
func SummarizeCustomBaseURL(parsedURL *url.URL) string {
	// 喵~防御：空 URL 不生成误导性展示文本喵。
	if parsedURL == nil || parsedURL.Hostname() == "" {
		return ""
	}
	// 保留原始 scheme（http/https），与配置的真实协议一致喵。
	return strings.ToLower(parsedURL.Scheme) + "://" + strings.ToLower(parsedURL.Hostname())
}
