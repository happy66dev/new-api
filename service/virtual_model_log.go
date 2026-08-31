package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// maxVirtualModelRequestContentPreview 请求内容预览的最大字符数，防止超长请求体撑大日志喵。
// 原始请求体 JSON 截断到该长度，既保留 model/参数/messages 全貌，又不至于让日志行过长喵。
const maxVirtualModelRequestContentPreview = 2048

// VirtualModelRequestContentPreview 读取当前请求的原始请求体 JSON 并截断为日志可展示的预览喵。
// 供虚拟模型成功/失败日志的 Content 字段使用：日志页「Content」区块直接展示，无需前端新增字段喵。
// 喵~防御：请求体不可读或为空时返回空字符串，不阻断主流程喵。
func VirtualModelRequestContentPreview(c *gin.Context) string {
	// 喵~防御：空上下文直接返回空串，避免空指针喵。
	if c == nil {
		return ""
	}
	bodyStorage, bodyError := common.GetBodyStorage(c)
	// 喵~防御：无法读取请求体存储时返回空串，日志照常落库喵。
	if bodyError != nil {
		return ""
	}
	requestBody, readError := bodyStorage.Bytes()
	// 喵~防御：读取失败或请求体为空时返回空串，避免写入无意义内容喵。
	if readError != nil || len(requestBody) == 0 {
		return ""
	}
	return limitVirtualModelRequestContentPreview(string(requestBody))
}

// limitVirtualModelRequestContentPreview 截断请求内容预览到安全上限喵。
// 喵~防御：仅在超过上限时截断，避免对短请求体做无谓拷贝喵。
func limitVirtualModelRequestContentPreview(text string) string {
	if len(text) > maxVirtualModelRequestContentPreview {
		return text[:maxVirtualModelRequestContentPreview]
	}
	return text
}
