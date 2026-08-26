package virtualmodel

import (
	"errors"
	"net"
	"net/url"
	"os"
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

// ValidateCustomBaseURL 严格校验用户自定义上游的公开 HTTPS 地址喵。
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
	// 喵~防御：自定义上游默认仅允许 HTTPS 443 与可验证公网域名，禁止访问内部服务喵。
	// 开发模式（VIRTUAL_MODEL_INSECURE_UPSTREAM=1）显式放宽 scheme/端口/IP 限制，仅限本地测试喵。
	if !allowInsecureCustomUpstream() {
		if strings.ToLower(parsedURL.Scheme) != "https" {
			return nil, errors.New("custom upstream URL must use HTTPS")
		}
		if parsedURL.Port() != "" && parsedURL.Port() != "443" {
			return nil, errors.New("custom upstream URL must use port 443")
		}
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
	return "https://" + strings.ToLower(parsedURL.Hostname())
}
