package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	upstreammodelservice "github.com/QuantumNous/new-api/service/upstreammodel"
	virtualmodelservice "github.com/QuantumNous/new-api/service/virtualmodel"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// userUpstreamModelDefaultTimeoutSeconds 独立上游调用的默认超时，单位：秒喵。
const userUpstreamModelDefaultTimeoutSeconds = 60

// defaultUserUpstreamGroupName 自用日志的兜底分组，保证日志可按分组筛选喵。
const defaultUserUpstreamGroupName = "default"

// isUserUpstreamModelRequest 判断请求模型是否进入用户上游模型独立命名空间喵。
func isUserUpstreamModelRequest(modelName string) bool {
	return strings.HasPrefix(strings.TrimSpace(modelName), "user/")
}

// abortUpstreamModelQuotaExhausted 统一返回额度不足的受控错误喵。
func abortUpstreamModelQuotaExhausted(c *gin.Context) {
	abortWithOpenAiMessage(c, http.StatusConflict, "user upstream model quota exhausted", types.ErrorCode("upstream_model_quota_exhausted"))
}

// handleUserUpstreamModelRequest 验证用户上游模型授权、执行透传并完成独立 RMB 计费喵。
// 返回 false 表示请求已经被完全处理（成功或失败），调用方应停止继续分发喵。
func handleUserUpstreamModelRequest(c *gin.Context, modelRequest *ModelRequest) bool {
	// 喵~防御：空上下文或空请求对象直接终止，避免空指针喵。
	if c == nil || modelRequest == nil {
		return false
	}
	startTime := time.Now()
	normalizedName, normalizeError := model.NormalizeUserUpstreamModelName(modelRequest.Model)
	// 喵~防御：无效名称不触发数据库查询，避免异常输入扩大资源占用或泄露校验细节喵。
	if normalizeError != nil {
		abortWithOpenAiMessage(c, http.StatusBadRequest, "invalid user upstream model request", types.ErrorCode("upstream_model_invalid_request"))
		return false
	}
	// 授权解析：先查当前用户自己名下的上游模型，再回退查询共享中的同名模型喵。
	ownerUserID := c.GetInt("id")
	upstreamModel, queryError := model.GetEnabledUserUpstreamModelByOwnerName(ownerUserID, normalizedName)
	isShared := false
	if queryError != nil {
		// 属主名下没有该模型时，查询共享中的同名模型（按调用者白名单/黑名单过滤），供共享调用喵。
		upstreamModel, queryError = model.GetEnabledSharedUserUpstreamModelByName(normalizedName, ownerUserID)
		if queryError != nil {
			// 喵~防御：模型不存在或停用时只更新其最近调用时间，不改变成功率喵。
			touchUpstreamModelConfigState(c, ownerUserID, normalizedName, startTime)
			abortWithOpenAiMessage(c, http.StatusNotFound, "user upstream model not found", types.ErrorCode("upstream_model_not_found"))
			return false
		}
		isShared = true
	}
	// 请求前硬检查：自用调用需余额与可用额度都大于 0；共享调用由查询条件保证三账户未耗尽喵。
	if !isShared {
		if upstreamModel.BalanceCents <= 0 {
			// 配置态：余额耗尽不计入成功率，只更新最近调用时间喵。
			recordUpstreamModelProbeState(upstreamModel, false, false, false, "", startTime, upstreamProbeExtras{})
			abortUpstreamModelQuotaExhausted(c)
			return false
		}
		if upstreamModel.AvailableCents <= 0 {
			recordUpstreamModelProbeState(upstreamModel, false, false, false, "", startTime, upstreamProbeExtras{})
			abortUpstreamModelQuotaExhausted(c)
			return false
		}
	}
	baseURL, decryptBaseURLError := virtualmodelservice.DecryptCredential(upstreamModel.EncryptedBaseURL, upstreamModel.CredentialVersion)
	apiKey, decryptAPIKeyError := virtualmodelservice.DecryptCredential(upstreamModel.EncryptedAPIKey, upstreamModel.CredentialVersion)
	// 喵~防御：凭据密文篡改、主密钥缺失或解密失败均只返回受控不可用错误，不泄露秘密或密文状态喵。
	if decryptBaseURLError != nil || decryptAPIKeyError != nil {
		// 凭据解密失败计入失败（反映配置问题），记录受控错误分类喵。
		recordUpstreamModelProbeState(upstreamModel, isShared, true, false, "credential_error", startTime, upstreamProbeExtras{})
		abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "user upstream model is not available", types.ErrorCode("upstream_model_unavailable"))
		return false
	}
	executionResult := virtualmodelservice.ExecuteUserUpstreamModel(c, virtualmodelservice.CustomCandidateExecutionInput{
		BaseURL:        baseURL,
		APIKey:         apiKey,
		RealModelName:  upstreamModel.RealModelName,
		AuthStyle:      model.VirtualModelAuthStyle(upstreamModel.AuthStyle),
		TimeoutSeconds: userUpstreamModelDefaultTimeoutSeconds,
	})
	// 透传失败：不计费、不写日志，返回受控错误或透传上游错误喵。
	if executionResult.Err != nil {
		// 实体状态检测：上游 4xx/5xx、超时、连接失败等透传失败计入失败喵。
		recordUpstreamModelProbeState(upstreamModel, isShared, true, false, upstreamModelFailureErrorClass(executionResult.Err), startTime, upstreamProbeExtras{})
		customFailure := &virtualmodelservice.CustomCandidateExecutionFailure{}
		// 喵~防御：非结构化异常不能透传；若响应已提交则只中止，避免重复错误响应喵。
		if !errors.As(executionResult.Err, &customFailure) {
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
	// 成功透传计入成功，随后按独立 RMB 计费系统扣费与写日志；共享调用免费只累计共享消耗喵。
	recordUpstreamModelProbeState(upstreamModel, isShared, true, true, "", startTime, buildUpstreamProbeExtras(executionResult))
	settleUserUpstreamModelCharge(c, upstreamModel.OwnerUserID, upstreamModel, executionResult.Usage, modelRequest.Group, isUpstreamModelRequestStreaming(c), int(time.Since(startTime).Seconds()), isShared, executionResult.TtftMs)
	c.Abort()
	return false
}

// upstreamProbeExtras 携带实体探测需要补记的吞吐、首字节与缓存 token 明细喵。
type upstreamProbeExtras struct {
	// ttftMs 从发起上游请求到收到响应头的时间，单位：毫秒喵。
	ttftMs int64
	// outputTokens 本次调用的完成 token 数，用于吞吐计算喵。
	outputTokens int64
	// inputTokens 本次调用的输入 token 数（含缓存命中），用于缓存命中率统计喵。
	inputTokens int64
	// cachedTokens 本次调用的缓存命中 token 数喵。
	cachedTokens int64
	// cacheHit 标记本次调用是否为缓存命中样本喵。
	cacheHit bool
	// cacheSample 标记本次调用是否携带 usage 喵。
	cacheSample bool
}

// buildUpstreamProbeExtras 从透传执行结果提取 TTFT、完成 token 数与缓存 token 喵。
// 空 usage 时相关计数为零，缓存命中率与吞吐自然跳过喵。
func buildUpstreamProbeExtras(result *virtualmodelservice.UserUpstreamModelExecutionResult) upstreamProbeExtras {
	// 喵~防御：空结果对象直接返回空 extras，避免空指针喵。
	if result == nil {
		return upstreamProbeExtras{}
	}
	extras := upstreamProbeExtras{ttftMs: result.TtftMs}
	if result.Usage != nil {
		extras.outputTokens = int64(result.Usage.CompletionTokens)
		// 缓存 token 提取复用内部模型同款多厂商 fallback，保证命中率口径一致喵。
		extras.cachedTokens, extras.inputTokens = perfmetrics.CacheTokenUsage(result.Usage)
		extras.cacheHit = perfmetrics.HasCacheHit(result.Usage)
		extras.cacheSample = true
	}
	return extras
}

// recordUpstreamModelProbeState 记录一次上游模型真实调用的被动统计与最近一次状态喵。
// counted 为 true 时计入成功率（成功/失败都累计请求数），为 false 时只更新最近一次时间喵。
func recordUpstreamModelProbeState(upstreamModel *model.UserUpstreamModel, isShared bool, counted bool, success bool, errorClass string, startTime time.Time, extras upstreamProbeExtras) {
	// 喵~防御：空模型对象直接返回，避免空指针喵。
	if upstreamModel == nil {
		return
	}
	latencyMs := time.Since(startTime).Milliseconds()
	// 吞吐口径：有 TTFT 时生成时长近似为 latency - ttft，否则取总延迟，与内部模型语义对齐喵。
	generationMs := latencyMs
	if extras.ttftMs > 0 && latencyMs > extras.ttftMs {
		generationMs = latencyMs - extras.ttftMs
	}
	probeModelName := upstreamModel.UserUpstreamModelName()
	// 自用与共享维度使用不同的作用域与固定隔离分组，互不混用喵。
	scope := model.EntityProbeScopeUpstream
	if isShared {
		scope = model.EntityProbeScopeUpstreamShared
	}
	now := time.Now().Unix()
	if !counted {
		// 配置态请求：只更新最近一次时间，不改动成功率喵。
		_ = model.TouchEntityProbeLastAt(scope, upstreamModel.ID, 0, upstreamModel.OwnerUserID, now)
		return
	}
	probeExtras := perfmetrics.EntityProbeExtras{
		TtftMs:       extras.ttftMs,
		HasTtft:      extras.ttftMs > 0,
		OutputTokens: extras.outputTokens,
		GenerationMs: generationMs,
		InputTokens:  extras.inputTokens,
		CachedTokens: extras.cachedTokens,
		CacheHit:     extras.cacheHit,
		CacheSample:  extras.cacheSample,
	}
	if isShared {
		perfmetrics.RecordEntityProbeShared(probeModelName, latencyMs, success, probeExtras)
	} else {
		perfmetrics.RecordEntityProbe(probeModelName, latencyMs, success, probeExtras)
	}
	_ = model.RecordEntityProbeCounted(scope, upstreamModel.ID, 0, upstreamModel.OwnerUserID, now, success, latencyMs, errorClass)
}

// touchUpstreamModelConfigState 模型不存在或停用时尝试更新其最近调用时间（不改变成功率）喵。
func touchUpstreamModelConfigState(c *gin.Context, ownerUserID int, normalizedName string, startTime time.Time) {
	// 喵~防御：空请求上下文直接返回喵。
	if c == nil {
		return
	}
	now := time.Now().Unix()
	// 先查自己名下任意状态的模型，命中说明是被停用或额度耗尽喵。
	if upstreamModel, queryError := model.GetUserUpstreamModelByOwnerName(ownerUserID, normalizedName); queryError == nil && upstreamModel != nil {
		_ = model.TouchEntityProbeLastAt(model.EntityProbeScopeUpstream, upstreamModel.ID, 0, upstreamModel.OwnerUserID, now)
		return
	}
	// 自己名下没有时查共享池任意状态的模型，命中说明共享额度耗尽或共享被关闭喵。
	if sharedModel, queryError := model.GetSharedUserUpstreamModelByNormalizedName(normalizedName); queryError == nil && sharedModel != nil {
		_ = model.TouchEntityProbeLastAt(model.EntityProbeScopeUpstreamShared, sharedModel.ID, 0, sharedModel.OwnerUserID, now)
	}
}

// upstreamModelFailureErrorClass 从透传失败结果提取受控错误分类，非结构化异常回退通用文案喵。
func upstreamModelFailureErrorClass(executionError error) string {
	// 喵~防御：空错误回退通用分类，避免空指针喵。
	if executionError == nil {
		return "upstream_unavailable"
	}
	customFailure := &virtualmodelservice.CustomCandidateExecutionFailure{}
	if errors.As(executionError, &customFailure) {
		return customFailure.Failure.ErrorClass
	}
	return "upstream_unavailable"
}

// settleUserUpstreamModelCharge 计算费用、扣减余额与累计消耗，并写入自定上游日志喵。
// isShared 为 true 时表示共享调用：免费、只累计共享消耗、日志归入 user-shared 分组喵。
func settleUserUpstreamModelCharge(c *gin.Context, ownerUserID int, upstreamModel *model.UserUpstreamModel, usage *dto.Usage, group string, isStream bool, useTimeSeconds int, isShared bool, ttftMs int64) {
	// 喵~防御：空上下文或空模型对象直接返回，避免空指针喵。
	if c == nil || upstreamModel == nil {
		return
	}
	// 费用计算：无 usage 时费用为零（不计费但照常写日志）喵。
	costCents, costError := upstreammodelservice.CalculateUpstreamModelCostCents(upstreamModel, usage)
	// 喵~防御：计费计算异常按零费用兜底，绝不因此吞掉用户请求结果喵。
	if costError != nil {
		common.SysError("user upstream model cost calculation failed: " + costError.Error())
		costCents = 0
	}
	// 扣减：自用扣余额并累计自用消耗，共享调用免费只累计共享消耗喵。
	if deductError := model.DeductUserUpstreamModelCharge(upstreamModel.ID, ownerUserID, costCents, isShared); deductError != nil {
		common.SysError("user upstream model charge deduction failed: " + deductError.Error())
	}
	// 分组：共享调用固定归入 user-shared，自用沿用请求分组并兜底默认值喵。
	effectiveGroup := strings.TrimSpace(group)
	if isShared {
		effectiveGroup = constant.GroupUserShared
	} else if effectiveGroup == "" {
		effectiveGroup = defaultUserUpstreamGroupName
	}
	// 日志归属：自用归模型属主，共享调用归当前调用者喵。
	logUserID := ownerUserID
	if isShared {
		logUserID = c.GetInt("id")
	}
	promptTokens, completionTokens, cachedTokens, cacheCreationTokens, imageTokens, audioTokens, cacheCreation5mTokens, cacheCreation1hTokens := splitUpstreamModelUsage(usage)
	// Other 记录独立 RMB 计费明细，供日志详情与筛选展示喵。
	other := map[string]interface{}{
		"custom_cost_rmb":          fmt.Sprintf("%.4f", float64(costCents)/100.0),
		"model_ratio":              upstreamModel.ModelRatio,
		"completion_ratio":         upstreamModel.CompletionRatio,
		"cache_ratio":              upstreamModel.CacheRatio,
		"cache_creation_ratio":     upstreamModel.CacheCreationRatio,
		"cache_creation_5m_ratio":  upstreamModel.CacheCreation5mRatio,
		"cache_creation_1h_ratio":  upstreamModel.CacheCreation1hRatio,
		"image_ratio":              upstreamModel.ImageRatio,
		"audio_ratio":              upstreamModel.AudioRatio,
		"audio_completion_ratio":   upstreamModel.AudioCompletionRatio,
		"prompt_tokens":            promptTokens,
		"completion_tokens":        completionTokens,
		"cached_tokens":            cachedTokens,
		"cache_creation_tokens":    cacheCreationTokens,
		"cache_creation_5m_tokens": cacheCreation5mTokens,
		"cache_creation_1h_tokens": cacheCreation1hTokens,
		"image_tokens":             imageTokens,
		"audio_tokens":             audioTokens,
		"usage_available":          usage != nil,
		"is_shared_call":           isShared,
	}
	// 首字延迟写入 other["frt"]（毫秒），与内部日志字段一致，供日志 Timing 展示首字耗时喵。
	if ttftMs > 0 {
		other["frt"] = ttftMs
	}
	model.RecordUserUpstreamModelLog(c, logUserID, model.RecordUserUpstreamModelLogParams{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		ModelName:        upstreamModel.UserUpstreamModelName(),
		Group:            effectiveGroup,
		UseTimeSeconds:   useTimeSeconds,
		IsStream:         isStream,
		Other:            other,
	})
}

// splitUpstreamModelUsage 从 usage 提取各 token 分类数，空 usage 全部回退为零喵。
func splitUpstreamModelUsage(usage *dto.Usage) (promptTokens int, completionTokens int, cachedTokens int, cacheCreationTokens int, imageTokens int, audioTokens int, cacheCreation5mTokens int, cacheCreation1hTokens int) {
	// 喵~防御：无 usage 时返回全零，避免空指针喵。
	if usage == nil {
		return 0, 0, 0, 0, 0, 0, 0, 0
	}
	promptTokens = usage.PromptTokens
	completionTokens = usage.CompletionTokens
	cachedTokens = usage.PromptTokensDetails.CachedTokens
	cacheCreationTokens = usage.PromptTokensDetails.CacheCreationTokensTotal()
	imageTokens = usage.PromptTokensDetails.ImageTokens
	audioTokens = usage.PromptTokensDetails.AudioTokens
	cacheCreation5mTokens = usage.ClaudeCacheCreation5mTokens
	cacheCreation1hTokens = usage.ClaudeCacheCreation1hTokens
	return promptTokens, completionTokens, cachedTokens, cacheCreationTokens, imageTokens, audioTokens, cacheCreation5mTokens, cacheCreation1hTokens
}

// isUpstreamModelRequestStreaming 从可复用 JSON 请求确认客户端是否请求 SSE 流式输出喵。
func isUpstreamModelRequestStreaming(c *gin.Context) bool {
	// 喵~防御：缺少 Gin 上下文或请求时按非流式处理，避免空指针喵。
	if c == nil || c.Request == nil {
		return false
	}
	bodyStorage, storageError := common.GetBodyStorage(c)
	if storageError != nil {
		return false
	}
	requestBody, bodyError := bodyStorage.Bytes()
	// 喵~防御：非法 JSON 或缺失 stream 字段按非流式处理喵。
	if bodyError != nil || !gjson.ValidBytes(requestBody) {
		return false
	}
	return gjson.GetBytes(requestBody, "stream").Type == gjson.True
}
