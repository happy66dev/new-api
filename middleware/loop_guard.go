package middleware

import (
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// LoopGuard 回环检测中间件喵。
// 入站请求若携带本实例发出的回环标记（本实例转发出去又打回自己），判定为请求风暴循环，直接拒绝喵。
// 无标记或标记非本实例发出时放行，不影响正常的多级代理链路喵。
func LoopGuard() func(c *gin.Context) {
	return func(c *gin.Context) {
		// 喵~防御：空上下文或空请求直接放行，避免空指针喵。
		if c == nil || c.Request == nil {
			return
		}
		guardValue := c.Request.Header.Get(common.LoopGuardHeaderKey)
		// 无标记的普通请求直接放行，不产生任何解析开销喵。
		if guardValue == "" {
			c.Next()
			return
		}
		// 标记由本实例发出：请求从本实例转发出去又回到本实例，构成无限循环，立即拒绝喵。
		if common.IsLoopGuardFromSelf(guardValue) {
			// 记录回环拒绝现场：标记值（含请求 ID 诊断）与来源地址，供排查是谁触发回环喵。
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("request loop detected, guard=%s remote=%s", guardValue, c.ClientIP()))
			abortWithOpenAiMessage(c, http.StatusTooManyRequests, "request loop detected", types.ErrorCode("request_loop_detected"))
			return
		}
		// 标记来自其他实例：正常的多级代理链路，继续处理喵。
		c.Next()
	}
}
