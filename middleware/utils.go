package middleware

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

func abortWithOpenAiMessage(c *gin.Context, statusCode int, message string, code ...types.ErrorCode) {
	codeStr := ""
	if len(code) > 0 {
		codeStr = string(code[0])
	}
	userId := c.GetInt("id")
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"message": common.MessageWithRequestId(message, c.GetString(common.RequestIdKey)),
			"type":    "new_api_error",
			"code":    codeStr,
		},
	})
	c.Abort()
	logger.LogError(c.Request.Context(), fmt.Sprintf("user %d | %s", userId, message))
	// 虚拟模型执行级失败统一记录 type=9 整体失败日志（此前 virtual_model_unavailable 无任何日志落库）喵。
	// 钩子条件：错误码为 virtual_model_unavailable 且处于虚拟模型上下文，其余普通请求/客户端错误不产生日志喵。
	if codeStr == string(types.ErrorCode("virtual_model_unavailable")) && common.GetContextKeyString(c, constant.ContextKeyVirtualModelName) != "" {
		RecordVirtualModelOverallFailure(c, "virtual_model_unavailable", statusCode)
	}
}

func abortWithMidjourneyMessage(c *gin.Context, statusCode int, code int, description string) {
	c.JSON(statusCode, gin.H{
		"description": description,
		"type":        "new_api_error",
		"code":        code,
	})
	c.Abort()
	logger.LogError(c.Request.Context(), description)
}
