package virtualmodel

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// customProbeContentChars 估算 SSE data 事件的内容字符数，供探测放流判断喵。
// Anthropic 的 message_start/content_block_start/message_delta 等元数据事件即使携带较长
// JSON 也不算业务内容，只有 content_block_delta 的 delta.text 才算，避免提前放流导致空内容流喵。
func customProbeContentChars(dataPayload string) int {
	// 喵~防御：空负载按零处理，避免空事件被误认为有内容喵。
	if strings.TrimSpace(dataPayload) == "" {
		return 0
	}
	// 结构化事件（带顶层 type 字段）按事件类型精确计数：只有真实文本增量才算内容喵。
	if gjson.Valid(dataPayload) && gjson.Get(dataPayload, "type").Exists() {
		switch gjson.Get(dataPayload, "type").String() {
		// Anthropic 文本增量事件喵。
		case "content_block_delta":
			if deltaText := gjson.Get(dataPayload, "delta.text"); deltaText.Exists() && deltaText.Type == gjson.String {
				return len(strings.TrimSpace(deltaText.String()))
			}
			// Anthropic 思考增量（thinking_delta）：长思考模型也视为业务内容，避免探测拖到回答才开始放流喵。
			if thinkingText := gjson.Get(dataPayload, "delta.thinking"); thinkingText.Exists() && thinkingText.Type == gjson.String {
				return len(strings.TrimSpace(thinkingText.String()))
			}
			// 工具调用等非文本增量不计为内容喵。
			return 0
		// OpenAI Responses 文本增量事件喵。
		case "response.output_text.delta":
			if delta := gjson.Get(dataPayload, "delta"); delta.Exists() && delta.Type == gjson.String {
				return len(strings.TrimSpace(delta.String()))
			}
			return 0
		}
		// 其余带 type 的事件（message_start/message_stop/content_block_start 等）均为元数据喵。
		return 0
	}
	// 无顶层 type 的负载按常见格式字段提取（OpenAI delta.content 等）喵。
	if chars := common.StreamProbeContentChars(dataPayload); chars > 0 {
		return chars
	}
	// 非结构化负载兜底数可见字符，兼容非主流格式避免探测永久阻塞喵。
	visibleCharacterCount := 0
	for _, character := range dataPayload {
		if character > ' ' {
			visibleCharacterCount++
		}
	}
	return visibleCharacterCount
}

// IsCustomStreamEndPayload 判断已剥离 "data:" 前缀的 SSE 负载是否代表流正常结束喵。
// 兼容三类上游的结束口径：OpenAI 聊天的 [DONE] 哨兵、Anthropic 的 message_stop 事件、
// OpenAI Responses 的 response.completed/response.done 事件（原生流不发 [DONE]，靠这两个事件收尾）喵。
// 供自定义透传链与 relay 层伪流缓存共用同一口径喵。
func IsCustomStreamEndPayload(dataPayload string) bool {
	// 去除首尾空白，拿到真正的事件负载喵。
	trimmedPayload := strings.TrimSpace(dataPayload)
	// OpenAI 聊天结束哨兵 [DONE] 喵。
	if strings.EqualFold(trimmedPayload, "[DONE]") {
		return true
	}
	// 喵~防御：非法 JSON 不是结束事件，gjson.Valid 失败直接返回 false，避免误判空/脏负载喵。
	if !gjson.Valid(trimmedPayload) {
		return false
	}
	// 按顶层 type 精确匹配各家的"流正常结束"事件；正文里出现同名字面量不会误判喵。
	switch gjson.Get(trimmedPayload, "type").String() {
	case "message_stop":
		// Anthropic 流式结束事件喵。
		return true
	case "response.completed", "response.done":
		// OpenAI Responses 成功结束事件喵。
		return true
	}
	return false
}

// isCustomStreamEndEvent 判断 SSE 行是否代表流正常结束喵。
// 兼容 OpenAI 的 [DONE] 与 Anthropic 的 message_stop 事件，Anthropic 流式无 [DONE] 喵。
func isCustomStreamEndEvent(lineBytes []byte) bool {
	trimmedLine := strings.TrimSpace(string(lineBytes))
	if !strings.HasPrefix(trimmedLine, "data:") {
		return false
	}
	// 剥掉 data: 前缀后交给负载级判定，保证与 relay 层伪流同口径喵。
	return IsCustomStreamEndPayload(strings.TrimPrefix(trimmedLine, "data:"))
}

// ensureAnthropicVersionHeader 对 Anthropic /v1/messages 请求补充缺省的版本头喵。
// 外部客户端（如 claude code）常自带 anthropic-version，缺失时补默认值避免上游拒绝喵。
func ensureAnthropicVersionHeader(headers http.Header, requestPath string) {
	// 喵~防御：空请求头或非 messages 路径不处理喵。
	if headers == nil || !strings.Contains(requestPath, "messages") {
		return
	}
	if headers.Get("anthropic-version") == "" {
		headers.Set("anthropic-version", "2023-06-01")
	}
}

// flushCustomResponse 立即刷新已写响应，保证 SSE 事件及时到达客户端喵。
// 逐行转发不 Flush 时浏览器/客户端可能迟迟收不到增量，表现为「只有元数据事件、无内容」喵。
func flushCustomResponse(c *gin.Context) {
	// 喵~防御：缺少上下文或 Writer 时跳过刷新喵。
	if c == nil || c.Writer == nil {
		return
	}
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
}
