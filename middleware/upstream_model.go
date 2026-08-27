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
	// 请求前预扣：按请求体估算费用并原子预扣三账户，避免单次调用超出用户设置限额喵。
	preConsumedCents, preConsumeError := preConsumeUserUpstreamModelCharge(c, upstreamModel, isShared)
	// 喵~防御：预扣失败（三账户任一不足）直接 403 拒绝，与普通模型预扣语义一致喵。
	if preConsumeError != nil {
		// 配置态：预扣不足不计入成功率，只更新最近调用时间喵。
		recordUpstreamModelProbeState(upstreamModel, isShared, false, false, "", startTime, upstreamProbeExtras{})
		abortUpstreamModelQuotaExhausted(c)
		return false
	}
	// 活跃请求注册：进入上游模型活跃计数，函数返回（含错误路径）时统一退出喵。
	EnterUpstreamModelInflight(upstreamModel.ID, upstreamModel.UserUpstreamModelName(), isShared)
	defer ExitUpstreamModelInflight(upstreamModel.ID, isShared)
	// settled 标记本次请求是否已完成差额结算；未结算时由 defer 在函数退出前退还预扣喵。
	settled := false
	defer func() {
		// 喵~防御：请求结束仍未结算（透传失败）时退还预扣金额，避免额度被永久锁定喵。
		if preConsumedCents > 0 && !settled {
			if refundError := model.AdjustUserUpstreamModelCharge(upstreamModel.ID, upstreamModel.OwnerUserID, -preConsumedCents, isShared); refundError != nil {
				common.SysError("user upstream model pre-consume refund failed: " + refundError.Error())
			}
		}
	}()
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
		// 请求定制：自定义请求头与字段替换随模型配置传入，供透传时应用喵。
		CustomHeaders:     upstreamModel.CustomHeaders,
		FieldReplacements: upstreamModel.FieldReplacements,
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
	settleUserUpstreamModelCharge(c, upstreamModel.OwnerUserID, upstreamModel, executionResult.Usage, modelRequest.Group, isUpstreamModelRequestStreaming(c), int(time.Since(startTime).Seconds()), isShared, executionResult.TtftMs, preConsumedCents)
	// 已按差额结算完毕，defer 不再退还预扣喵。
	settled = true
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
	// cacheCreation5mTokens 缓存写入 5 分钟分类 token 数（Claude 语义），无则为零喵。
	cacheCreation5mTokens int64
	// cacheCreation1hTokens 缓存写入 1 小时分类 token 数（Claude 语义），无则为零喵。
	cacheCreation1hTokens int64
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
		// 缓存写入分类 token 只取非负值（Claude 语义），有则记录喵。
		extras.cacheCreation5mTokens = int64(clampNonNegative(result.Usage.ClaudeCacheCreation5mTokens))
		extras.cacheCreation1hTokens = int64(clampNonNegative(result.Usage.ClaudeCacheCreation1hTokens))
	}
	return extras
}

// clampNonNegative 把负的 token 计数钳制为零，避免负数进入探测统计喵。
func clampNonNegative(tokenCount int) int {
	if tokenCount < 0 {
		return 0
	}
	return tokenCount
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
		CacheCreation5mTokens: extras.cacheCreation5mTokens,
		CacheCreation1hTokens: extras.cacheCreation1hTokens,
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

// settleUserUpstreamModelCharge 计算费用、结算预扣差额并写入自定上游日志喵。
// preConsumedCents 为请求前预扣金额：大于零时按差额补扣或退还，等于零时保持直接扣费兼容喵。
// isShared 为 true 时表示共享调用：免费、只累计共享消耗、日志归入 user-shared 分组喵。
func settleUserUpstreamModelCharge(c *gin.Context, ownerUserID int, upstreamModel *model.UserUpstreamModel, usage *dto.Usage, group string, isStream bool, useTimeSeconds int, isShared bool, ttftMs int64, preConsumedCents int64) {
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
	// 扣减：有预扣时按实际费用结算差额（多退少补），无预扣时直接按实际费用扣减（兼容未启用预扣的调用点）喵。
	if preConsumedCents > 0 {
		deltaCents := costCents - preConsumedCents
		if adjustError := model.AdjustUserUpstreamModelCharge(upstreamModel.ID, ownerUserID, deltaCents, isShared); adjustError != nil {
			common.SysError("user upstream model charge settlement failed: " + adjustError.Error())
		}
	} else {
		if deductError := model.DeductUserUpstreamModelCharge(upstreamModel.ID, ownerUserID, costCents, isShared); deductError != nil {
			common.SysError("user upstream model charge deduction failed: " + deductError.Error())
		}
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
		"pre_consumed_cents":       preConsumedCents,
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
	// 虚拟模型上下文下补充模型标识与最终成功标记，保证日志详情始终有可展示内容喵。
	if virtualModelName := common.GetContextKeyString(c, constant.ContextKeyVirtualModelName); virtualModelName != "" {
		other["virtual_model"] = virtualModelName
		other["final_success"] = true
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

// upstreamModelPreConsumeMinPromptTokens 预估输入 token 的兜底下限，与普通模型最小预扣额度对齐喵。
const upstreamModelPreConsumeMinPromptTokens = 500

// estimateUserUpstreamModelCostCents 请求前按请求体估算一次调用的费用（分），供预扣使用喵。
// 输入 token 按请求体字节数/4 保守估算并钳制到最小兜底值；输出 token 取请求的 max_tokens 系列上限喵。
func estimateUserUpstreamModelCostCents(upstreamModel *model.UserUpstreamModel, requestBody []byte) int64 {
	// 喵~防御：空模型对象按零费用预估，避免空指针喵。
	if upstreamModel == nil {
		return 0
	}
	// 输入 token 估算：body 字节数/4（英文约 4 字符 1 token 的保守口径），并钳制到最小兜底值喵。
	estimatedPromptTokens := len(requestBody) / 4
	if estimatedPromptTokens < upstreamModelPreConsumeMinPromptTokens {
		estimatedPromptTokens = upstreamModelPreConsumeMinPromptTokens
	}
	// 输出 token 上限：max_completion_tokens / max_tokens / max_output_tokens 任一存在即取最大值，缺失按零（不预扣输出）喵。
	estimatedCompletionTokens := 0
	if len(requestBody) > 0 && gjson.ValidBytes(requestBody) {
		estimatedCompletionTokens = max(
			int(gjson.GetBytes(requestBody, "max_completion_tokens").Int()),
			int(gjson.GetBytes(requestBody, "max_tokens").Int()),
			int(gjson.GetBytes(requestBody, "max_output_tokens").Int()),
		)
	}
	// 构造预估 usage 复用统一算费函数，缓存/图片等分类预估时按零处理，保证预扣口径与结算一致喵。
	estimatedCents, costError := upstreammodelservice.CalculateUpstreamModelCostCents(upstreamModel, &dto.Usage{
		PromptTokens:     estimatedPromptTokens,
		CompletionTokens: estimatedCompletionTokens,
	})
	// 喵~防御：预估计算异常按零费用兜底，绝不因预估失败阻断请求喵。
	if costError != nil {
		common.SysError("user upstream model cost estimation failed: " + costError.Error())
		return 0
	}
	return estimatedCents
}

// preConsumeUserUpstreamModelCharge 按请求体预估费用并原子预扣三账户，返回预扣金额（分）喵。
// 模型未定价或预估费用为零时预扣金额为零且视为成功；账户不足时返回 ErrUserUpstreamModelInsufficientQuota 喵。
func preConsumeUserUpstreamModelCharge(c *gin.Context, upstreamModel *model.UserUpstreamModel, isShared bool) (int64, error) {
	// 喵~防御：空上下文或空模型对象按零预扣处理，避免空指针喵。
	if c == nil || c.Request == nil || upstreamModel == nil {
		return 0, nil
	}
	bodyStorage, storageError := common.GetBodyStorage(c)
	// 喵~防御：无法读取请求体时按零费用预扣，不因预扣读取失败阻断请求喵。
	if storageError != nil {
		return 0, nil
	}
	requestBody, bytesError := bodyStorage.Bytes()
	// 喵~防御：请求体读取异常时按零费用预扣喵。
	if bytesError != nil {
		return 0, nil
	}
	estimatedCents := estimateUserUpstreamModelCostCents(upstreamModel, requestBody)
	// 预估费用为零（未定价等）视为预扣成功，零金额不产生数据库写入喵。
	if estimatedCents <= 0 {
		return 0, nil
	}
	// 原子预扣三账户，任一不足即返回错误由调用方 403 拒绝喵。
	if preConsumeError := model.PreConsumeUserUpstreamModelCharge(upstreamModel.ID, upstreamModel.OwnerUserID, estimatedCents, isShared); preConsumeError != nil {
		return 0, preConsumeError
	}
	return estimatedCents, nil
}
