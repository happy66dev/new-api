package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	virtualmodelservice "github.com/QuantumNous/new-api/service/virtualmodel"
	"github.com/gin-gonic/gin"
)

// userUpstreamModelDefaultTimeoutSeconds 独立上游调用的默认超时，单位：秒喵。
const userUpstreamModelDefaultTimeoutSeconds = 60

// isUserUpstreamModelRequest 判断请求模型是否进入用户上游模型独立命名空间喵。
func isUserUpstreamModelRequest(modelName string) bool {
	return strings.HasPrefix(strings.TrimSpace(modelName), "upstream/")
}

// handleUserUpstreamModelRequest 验证用户上游模型授权并执行透传，处理完成即终止后续 relay 链喵。
// 返回 false 表示请求已经被完全处理（成功或失败），调用方应停止继续分发喵。
func handleUserUpstreamModelRequest(c *gin.Context, modelRequest *ModelRequest) bool {
	// 喵~防御：空上下文或空请求对象直接终止，避免空指针喵。
	if c == nil || modelRequest == nil {
		return false
	}
	normalizedName, normalizeError := model.NormalizeUserUpstreamModelName(modelRequest.Model)
	// 喵~防御：无效名称不触发数据库查询，避免异常输入扩大资源占用或泄露校验细节喵。
	if normalizeError != nil {
		abortWithOpenAiMessage(c, http.StatusBadRequest, "invalid user upstream model request", types.ErrorCode("upstream_model_invalid_request"))
		return false
	}
	// 只允许当前登录用户访问自己名下的上游模型，隐藏资源存在性喵。
	ownerUserID := c.GetInt("id")
	upstreamModel, queryError := model.GetEnabledUserUpstreamModelByOwnerName(ownerUserID, normalizedName)
	if queryError != nil {
		abortWithOpenAiMessage(c, http.StatusNotFound, "user upstream model not found", types.ErrorCode("upstream_model_not_found"))
		return false
	}
	// 这里在 P2 补充余额与使用上限硬检查喵。
	baseURL, decryptBaseURLError := virtualmodelservice.DecryptCredential(upstreamModel.EncryptedBaseURL, upstreamModel.CredentialVersion)
	apiKey, decryptAPIKeyError := virtualmodelservice.DecryptCredential(upstreamModel.EncryptedAPIKey, upstreamModel.CredentialVersion)
	// 喵~防御：凭据密文篡改、主密钥缺失或解密失败均只返回受控不可用错误，不泄露秘密或密文状态喵。
	if decryptBaseURLError != nil || decryptAPIKeyError != nil {
		abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "user upstream model is not available", types.ErrorCode("upstream_model_unavailable"))
		return false
	}
	executionError := virtualmodelservice.ExecuteCustomCandidate(c, virtualmodelservice.CustomCandidateExecutionInput{
		BaseURL:        baseURL,
		APIKey:         apiKey,
		RealModelName:  upstreamModel.RealModelName,
		AuthStyle:      model.VirtualModelAuthStyle(upstreamModel.AuthStyle),
		TimeoutSeconds: userUpstreamModelDefaultTimeoutSeconds,
	})
	if executionError != nil {
		customFailure := &virtualmodelservice.CustomCandidateExecutionFailure{}
		// 喵~防御：非结构化异常不能透传；若响应已提交则只中止，避免重复错误响应喵。
		if !errors.As(executionError, &customFailure) {
			if c.Writer != nil && c.Writer.Written() {
				c.Abort()
				return false
			}
			abortWithOpenAiMessage(c, http.StatusBadGateway, "user upstream model is unavailable", types.ErrorCode("upstream_model_unavailable"))
			return false
		}
		// 上游返回非 2xx：把受限错误响应原样回传客户端，保持上游错误可读喵。
		if customFailure.ResponseBody != nil {
			virtualmodelservice.CopyCustomPassthroughResponse(c.Writer, customFailure.ResponseHeaders, customFailure.Failure.HTTPStatus, customFailure.ResponseBody)
			c.Abort()
			return false
		}
		abortWithOpenAiMessage(c, http.StatusBadGateway, "user upstream model is unavailable", types.ErrorCode("upstream_model_unavailable"))
		return false
	}
	// 成功响应已由透传器直接写出，中止后续 controller relay 喵。
	c.Abort()
	return false
}
