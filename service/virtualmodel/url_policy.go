package virtualmodel

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

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
	// 喵~防御：自定义上游仅允许 HTTPS，禁止明文传输 API Key 或请求内容喵。
	if strings.ToLower(parsedURL.Scheme) != "https" {
		return nil, errors.New("custom upstream URL must use HTTPS")
	}
	// 喵~防御：禁止 userinfo 和 fragment，避免认证混淆、秘密落入 URL 或不可预期的目标语义喵。
	if parsedURL.User != nil || parsedURL.Fragment != "" || parsedURL.Hostname() == "" {
		return nil, errors.New("custom upstream URL contains unsupported components")
	}
	// 喵~防御：仅允许默认 HTTPS 443 端口，避免访问内部管理服务和危险非标准端口喵。
	if parsedURL.Port() != "" && parsedURL.Port() != "443" {
		return nil, errors.New("custom upstream URL must use port 443")
	}
	// 喵~防御：直接填写 IP 会绕过执行期 DNS 固定拨号策略，因此仅允许可验证域名喵。
	if net.ParseIP(parsedURL.Hostname()) != nil {
		return nil, errors.New("custom upstream URL must use a public hostname")
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
