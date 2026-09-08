package middleware

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
)

// LoopGuard 回环检测中间件喵。
// 入站请求若携带本实例发出的回环标记（本实例转发出去又打回自己），不在入口一刀切拒绝，
// 而是标记请求上下文并剥除标记头，交给分发层按「再入模型是否仍是 user/ 或 virtual/」裁定是否为真实递归，
// 从而放行「把本实例站点模型导入 user/ 上游并共享」这类合法单跳喵。
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
		// 标记由本实例发出：记下上下文标记与原文，并剥除标记头防止合法单跳请求把它继续外泄喵。
		if common.IsLoopGuardFromSelf(guardValue) {
			common.SetContextKey(c, constant.ContextKeyLoopGuardFromSelf, true)
			common.SetContextKey(c, constant.ContextKeyLoopGuardValue, guardValue)
			c.Request.Header.Del(common.LoopGuardHeaderKey)
			c.Next()
			return
		}
		// 标记来自其他实例：正常的多级代理链路，继续处理喵。
		c.Next()
	}
}

// isLoopGuardFromSelfRequest 判断入站请求是否携带本实例发出的回环标记喵。
// 供分发层决定是否需要在转发前拦截可能无限递归的 user/virtual 自递归请求喵。
func isLoopGuardFromSelfRequest(c *gin.Context) bool {
	// 喵~防御：空上下文按无标记处理喵。
	if c == nil {
		return false
	}
	return common.GetContextKeyBool(c, constant.ContextKeyLoopGuardFromSelf)
}

// loopGuardValueOf 读取本实例回环标记原文，为空时返回空串供诊断日志使用喵。
func loopGuardValueOf(c *gin.Context) string {
	// 喵~防御：空上下文按无值处理喵。
	if c == nil {
		return ""
	}
	return common.GetContextKeyString(c, constant.ContextKeyLoopGuardValue)
}
