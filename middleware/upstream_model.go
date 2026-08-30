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

// userUpstreamModelRelayContext 保存自定义上游 relay 执行的结算上下文喵。
type userUpstreamModelRelayContext struct {
	upstreamModel    *model.UserUpstreamModel
	ownerUserID      int
	isShared         bool
	group            string
	preConsumedCents int64
	startTime        time.Time
	// settled 标记本次请求是否已完成差额结算，未结算时由 Distribute 兜底 defer 退还预扣喵。
	settled bool
}

// getUserUpstreamModelRelayContext 读取自定义上游 relay 的结算上下文，不存在时返回 nil 喵。
func getUserUpstreamModelRelayContext(c *gin.Context) *userUpstreamModelRelayContext {
	value, found := common.GetContextKey(c, constant.ContextKeyUpstreamModelRelayContext)
	if !found {
		return nil
	}
	relayCtx, ok := value.(*userUpstreamModelRelayContext)
	if !ok {
		return nil
	}
	return relayCtx
}

// IsUpstreamModelRelayRequest 判断请求是否已注入自定义上游临时渠道、应走原生 relay 中转链喵。
func IsUpstreamModelRelayRequest(c *gin.Context) bool {
	// 喵~防御：空上下文按非 relay 处理，避免空指针喵。
	if c == nil {
		return false
	}
	return common.GetContextKeyBool(c, constant.ContextKeyUpstreamModelRelay)
}

// userUpstreamAPITypeToChannelType 把上游 API 类型映射为临时渠道类型，未知类型按 OpenAI 兼容兜底喵。
func userUpstreamAPITypeToChannelType(apiType string) int {
	switch strings.ToLower(strings.TrimSpace(apiType)) {
	case model.UserUpstreamAPITypeAnthropic:
		return constant.ChannelTypeAnthropic
	default:
		return constant.ChannelTypeOpenAI
	}
}

// setupUserUpstreamModelRelay 构造临时渠道注入 context，让请求走原生 relay 中转链喵。
// 渠道信息（类型/base_url/key/model）写入 context，relay 的 getChannel 与 InitChannelMeta 直接读取喵。
func setupUserUpstreamModelRelay(c *gin.Context, upstreamModel *model.UserUpstreamModel, baseURL string, apiKey string, isShared bool, group string, preConsumedCents int64, startTime time.Time) bool {
	// 喵~防御：空上下文或空模型对象直接失败，避免空指针喵。
	if c == nil || upstreamModel == nil {
		return false
	}
	// 按上游 API 类型映射临时渠道类型，OpenAI 兼容为默认喵。
	channelType := userUpstreamAPITypeToChannelType(upstreamModel.APIType)
	baseURLValue := baseURL
	tempChannel := &model.Channel{
		Type:    channelType,
		BaseURL: &baseURLValue,
		Key:     apiKey,
		Name:    "user/" + upstreamModel.NormalizedName,
	}
	// 解析并注入自定义请求头：与虚拟模型直连路径同源校验，剥除 "*" 语义标记后写入渠道头覆盖喵。
	// 这样 SetupContextForSelectedChannel 会把它灌入 ContextKeyChannelHeaderOverride，relay 中转链据此覆盖上游请求头喵。
	if customHeadersJSON := strings.TrimSpace(upstreamModel.CustomHeaders); customHeadersJSON != "" {
		headerOverrides, parseErr := virtualmodelservice.ParseCustomUpstreamHeaderOverrides(customHeadersJSON)
		if parseErr != nil {
			// 喵~防御：非法自定义头属于用户配置错误，以 400 拒绝且不改变健康成功率喵。
			abortWithOpenAiMessage(c, http.StatusBadRequest, parseErr.Error(), types.ErrorCode("upstream_model_invalid_custom_headers"))
			return false
		}
		if len(headerOverrides) > 0 {
			headerOverrideJSON, marshalErr := common.Marshal(headerOverrides)
			if marshalErr != nil {
				// 喵~防御：序列化失败按配置错误 400 拒绝，避免静默丢失请求头喵。
				abortWithOpenAiMessage(c, http.StatusBadRequest, "invalid custom headers", types.ErrorCode("upstream_model_invalid_custom_headers"))
				return false
			}
			// 临时渠道头覆盖字段：JSON 字符串形式，GetHeaderOverride 解析后供 relay 读取喵。
			headerOverrideText := string(headerOverrideJSON)
			tempChannel.HeaderOverride = &headerOverrideText
		}
	}
	// 注入渠道上下文：original/selected model 与 base_url/key/channel_type 等，供 relay 读取喵。
	if setupError := SetupContextForSelectedChannel(c, tempChannel, upstreamModel.RealModelName); setupError != nil {
		// 实体状态检测：渠道注入失败按配置态处理，不改变成功率喵。
		recordUpstreamModelProbeState(upstreamModel, isShared, false, false, "", startTime, upstreamProbeExtras{})
		abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "user upstream model is not available", types.ErrorCode("upstream_model_unavailable"))
		return false
	}
	// 保存结算上下文并标记 relay 执行，Distribute 据此跳过普通渠道选择喵。
	common.SetContextKey(c, constant.ContextKeyUpstreamModelRelay, true)
	common.SetContextKey(c, constant.ContextKeyUpstreamModelRelayContext, &userUpstreamModelRelayContext{
		upstreamModel:    upstreamModel,
		ownerUserID:      upstreamModel.OwnerUserID,
		isShared:         isShared,
		group:            group,
		preConsumedCents: preConsumedCents,
		startTime:        startTime,
	})
	return true
}

// getUpstreamModelRelayUsage 读取自定义上游 relay 执行成功后的 usage 喵。
func getUpstreamModelRelayUsage(c *gin.Context) *dto.Usage {
	value, found := common.GetContextKey(c, constant.ContextKeyUpstreamModelUsage)
	if !found {
		return nil
	}
	usage, ok := value.(*dto.Usage)
	if !ok {
		return nil
	}
	return usage
}

// SettleUpstreamModelRelaySuccess 在自定义上游 relay 成功后完成独立 RMB 结算与状态探测喵。
// 由 controller.Relay 成功分支调用；ttftMs 为响应头到达耗时（毫秒），零表示未测到喵。
func SettleUpstreamModelRelaySuccess(c *gin.Context, ttftMs int64) {
	relayCtx := getUserUpstreamModelRelayContext(c)
	// 喵~防御：非自定义上游或缺少结算上下文时跳过，普通请求不产生任何写入喵。
	if c == nil || relayCtx == nil || relayCtx.upstreamModel == nil {
		return
	}
	usage := getUpstreamModelRelayUsage(c)
	// 耗时口径：总耗时取请求入口到当前，首字优先取请求级打点，其次上游 TTFT 兜底喵。
	requestElapsedMs := time.Since(relayCtx.startTime).Milliseconds()
	requestFirstByteMs := ttftMs
	if firstByteMs := virtualModelFirstByteMs(c); firstByteMs > 0 {
		requestFirstByteMs = firstByteMs
	}
	// 成功结算：上游模型探测成功 + 独立 RMB 结算 + 日志喵。
	result := &virtualmodelservice.UserUpstreamModelExecutionResult{Usage: usage, TtftMs: ttftMs}
	recordUpstreamModelProbeState(relayCtx.upstreamModel, relayCtx.isShared, true, true, "", relayCtx.startTime, buildUpstreamProbeExtras(result))
	settleUserUpstreamModelCharge(c, relayCtx.ownerUserID, relayCtx.upstreamModel, usage, relayCtx.group, isUpstreamModelRequestStreaming(c), int(requestElapsedMs/1000), relayCtx.isShared, requestFirstByteMs, relayCtx.preConsumedCents)
	relayCtx.settled = true
	// 虚拟模型上下文：记录候选成功尝试摘要与候选/整体成功样本喵。
	if executionState, foundState := getVirtualModelExecutionState(c); foundState && executionState != nil && executionState.currentCandidateIndex >= 0 && executionState.currentCandidateIndex < len(executionState.executionSnapshot.Candidates) {
		currentCandidate := executionState.executionSnapshot.Candidates[executionState.currentCandidateIndex]
		appendVirtualModelCandidateAttempt(c, model.VirtualModelCandidateAttemptRecord{
			Seq:         currentVirtualModelCandidateSeq(c),
			CandidateID: currentCandidate.CandidateID,
			Source:      "internal",
			Label:       buildVirtualModelAttemptLabel(&currentCandidate, currentCandidate.RealModelName),
			Success:     true,
			StatusCode:  http.StatusOK,
			TtftMs:      ttftMs,
			ElapsedMs:   time.Since(relayCtx.startTime).Milliseconds(),
		})
		recordVirtualModelProbeSuccess(c, executionState, currentCandidate.CandidateID, usage, requestFirstByteMs)
	}
}

// HandleUpstreamModelRelayFailure 在自定义上游 relay 最终失败时退还预扣并记录失败日志与探测喵。
// 错误正文由 controller.Relay 的收尾逻辑按客户端格式输出喵。
func HandleUpstreamModelRelayFailure(c *gin.Context, newAPIError *types.NewAPIError) {
	relayCtx := getUserUpstreamModelRelayContext(c)
	// 喵~防御：缺少结算上下文时按通用失败处理，普通请求不产生任何写入喵。
	if c == nil || relayCtx == nil || relayCtx.upstreamModel == nil {
		return
	}
	errorClass := "upstream_unavailable"
	statusCode := http.StatusBadGateway
	if newAPIError != nil {
		if newAPIError.StatusCode > 0 {
			statusCode = newAPIError.StatusCode
		}
		errorClass = string(newAPIError.GetErrorCode())
		// 喵~防御：错误码为空时回退通用分类喵。
		if errorClass == "" {
			errorClass = "upstream_unavailable"
		}
	}
	// 退还预扣：请求未结算时释放预扣金额，避免额度被永久锁定喵。
	if relayCtx.preConsumedCents > 0 && !relayCtx.settled {
		if refundError := model.AdjustUserUpstreamModelCharge(relayCtx.upstreamModel.ID, relayCtx.ownerUserID, -relayCtx.preConsumedCents, relayCtx.isShared); refundError != nil {
			common.SysError("user upstream model relay failure refund failed: " + refundError.Error())
		}
		relayCtx.settled = true
	}
	// 实体状态检测：上游模型失败计入失败喵。
	recordUpstreamModelProbeState(relayCtx.upstreamModel, relayCtx.isShared, true, false, errorClass, relayCtx.startTime, upstreamProbeExtras{})
	// 失败日志：非虚拟上下文直接写，虚拟上下文由候选链最终失败路径兜底写喵。
	if _, inVirtualModelContext := getVirtualModelExecutionState(c); !inVirtualModelContext {
		recordUserUpstreamModelFailureLog(c, relayCtx.upstreamModel, relayCtx.isShared, relayCtx.group, &virtualmodelservice.CustomCandidateExecutionFailure{
			Failure: virtualmodelservice.CandidateFailure{ErrorClass: errorClass, HTTPStatus: statusCode},
		}, relayCtx.startTime)
	}
}

// handleUserUpstreamModelRequest 验证用户上游模型授权、执行透传并完成独立 RMB 计费喵。
// 返回 false 表示请求已经被完全处理（成功或失败），调用方应停止继续分发喵。
// 虚拟模型 user/xxx 内部候选失败切换候选时返回 true，让 Distribute 循环重新分发新的 user/xxx 候选喵。
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
	// 活跃请求注册：进入上游模型活跃计数喵。
	EnterUpstreamModelInflight(upstreamModel.ID, upstreamModel.UserUpstreamModelName(), isShared)
	baseURL, decryptBaseURLError := virtualmodelservice.DecryptCredential(upstreamModel.EncryptedBaseURL, upstreamModel.CredentialVersion)
	apiKey, decryptAPIKeyError := virtualmodelservice.DecryptCredential(upstreamModel.EncryptedAPIKey, upstreamModel.CredentialVersion)
	// 喵~防御：凭据密文篡改、主密钥缺失或解密失败均只返回受控不可用错误，不泄露秘密或密文状态喵。
	if decryptBaseURLError != nil || decryptAPIKeyError != nil {
		// 凭据解密失败计入失败（反映配置问题），记录受控错误分类喵。
		recordUpstreamModelProbeState(upstreamModel, isShared, true, false, "credential_error", startTime, upstreamProbeExtras{})
		abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "user upstream model is not available", types.ErrorCode("upstream_model_unavailable"))
		return false
	}
	// 虚拟模型 user/xxx 候选：保持哑代理透传（候选链编排依赖同步执行），请求同步结束后退出活跃计数喵。
	if _, inVirtualModelContext := getVirtualModelExecutionState(c); inVirtualModelContext {
		defer ExitUpstreamModelInflight(upstreamModel.ID, isShared)
		return executeUserUpstreamModelPassthrough(c, upstreamModel, modelRequest, baseURL, apiKey, isShared, preConsumedCents, startTime)
	}
	// 非虚拟 user/xxx 直调：注入临时渠道走原生 relay 中转链（自动格式转换与流式处理）喵。
	requestGroup := modelRequest.Group
	if !setupUserUpstreamModelRelay(c, upstreamModel, baseURL, apiKey, isShared, requestGroup, preConsumedCents, startTime) {
		// 注入失败：请求已终结，退出活跃计数喵。
		ExitUpstreamModelInflight(upstreamModel.ID, isShared)
		return false
	}
	// 注入成功：返回 false，Distribute 检测到 relay 标记后继续走 controller.Relay；活跃计数由 Distribute relay 分支执行完毕后统一退出喵。
	return false
}

// executeUserUpstreamModelPassthrough 虚拟模型 user/xxx 候选的哑代理透传执行喵。
// 虚拟候选链依赖同步透传做失败规则编排（retry/next/freeze/passthrough），
// 在虚拟模型候选 relay 化完成前保持此过渡路径，避免破坏候选链行为喵。
func executeUserUpstreamModelPassthrough(c *gin.Context, upstreamModel *model.UserUpstreamModel, modelRequest *ModelRequest, baseURL string, apiKey string, isShared bool, preConsumedCents int64, startTime time.Time) bool {
	// 喵~防御：空上下文或空模型对象直接终止，避免空指针喵。
	if c == nil || upstreamModel == nil {
		return false
	}
	// 超时：模型配置了超时秒数时使用配置值，否则回退默认 60 秒喵。
	requestTimeoutSeconds := userUpstreamModelDefaultTimeoutSeconds
	if upstreamModel.TimeoutSeconds > 0 {
		requestTimeoutSeconds = upstreamModel.TimeoutSeconds
	}
	// 构造可复用的上游执行输入：失败规则 retry 重放同一请求时复用，避免重复解密与构造喵。
	upstreamExecutionInput := virtualmodelservice.CustomCandidateExecutionInput{
		BaseURL:        baseURL,
		APIKey:         apiKey,
		RealModelName:  upstreamModel.RealModelName,
		AuthStyle:      model.VirtualModelAuthStyle(upstreamModel.AuthStyle),
		TimeoutSeconds: requestTimeoutSeconds,
		// 请求定制：自定义请求头与字段替换随模型配置传入，供透传时应用喵。
		CustomHeaders:     upstreamModel.CustomHeaders,
		FieldReplacements: upstreamModel.FieldReplacements,
	}
	// 虚拟模型上下文标记：internal 候选 user/xxx 失败时需要按失败规则决策并推进候选链喵。
	_, inVirtualModelContext := getVirtualModelExecutionState(c)
	executionResult := virtualmodelservice.ExecuteUserUpstreamModel(c, upstreamExecutionInput)
	// 透传失败：不计费、不写日志，返回受控错误或透传上游错误喵。
	// 虚拟模型上下文时按失败规则决策；retry 在循环内退避重放当前上游请求喵。
	for executionResult.Err != nil {
		customFailure := &virtualmodelservice.CustomCandidateExecutionFailure{}
		// 喵~防御：非结构化异常不能参与规则匹配或重试；若响应已提交则只中止，避免重复错误响应喵。
		if !errors.As(executionResult.Err, &customFailure) {
			// 诊断日志：记录非结构化执行错误的底层原因（连接/TLS/DNS/端口等），供用户上游连接问题定位喵。
			common.SysError(fmt.Sprintf("user upstream model execution error: model=%s cause=%v", upstreamModel.UserUpstreamModelName(), executionResult.Err))
			// 实体状态检测：上游异常记录失败样本喵。
			recordUpstreamModelProbeState(upstreamModel, isShared, true, false, upstreamModelFailureErrorClass(executionResult.Err), startTime, upstreamProbeExtras{})
			if c.Writer != nil && c.Writer.Written() {
				c.Abort()
				return false
			}
			// 虚拟模型上下文：非结构化异常不能匹配失败规则，记录整体失败后终结喵。
			if inVirtualModelContext {
				RecordVirtualModelOverallProbe(c, false, "upstream_unavailable")
			}
			abortWithOpenAiMessage(c, http.StatusBadGateway, "user upstream model is unavailable", types.ErrorCode("upstream_model_unavailable"))
			return false
		}
		// 虚拟模型上下文：按失败规则决策动作并推进候选链喵。
		outcome := handleVirtualModelUpstreamFailure(c, upstreamModel, isShared, customFailure, startTime)
		if outcome != virtualModelUpstreamFailureRetry {
			// next=候选链已推进，返回 true 让 Distribute 循环重新分发；end=请求已终结喵。
			return outcome == virtualModelUpstreamFailureNext
		}
		// 失败规则 retry：退避后重放当前上游请求，退避须服从客户端取消与总 deadline 喵。
		retryDelay, canRetry := virtualModelUpstreamRetryDelay(c)
		if !canRetry {
			return false
		}
		select {
		case <-time.After(retryDelay):
			executionResult = virtualmodelservice.ExecuteUserUpstreamModel(c, upstreamExecutionInput)
			continue
		case <-c.Request.Context().Done():
			return false
		}
	}
	// 耗时口径：虚拟模型上下文取请求级（new-api 接收请求 → 首次写响应 / 请求完成）喵。
	requestElapsedMs := time.Since(startTime).Milliseconds()
	requestFirstByteMs := executionResult.TtftMs
	if executionState, foundState := getVirtualModelExecutionState(c); foundState && executionState != nil && !executionState.startTime.IsZero() {
		requestElapsedMs = time.Since(executionState.startTime).Milliseconds()
	}
	// 请求级首字：首次向客户端写响应时刻减请求入口时刻；未打点时回退上游 TTFT 兜底喵。
	if firstByteMs := virtualModelFirstByteMs(c); firstByteMs > 0 {
		requestFirstByteMs = firstByteMs
	}
	// 虚拟模型 user/xxx 内部候选成功：先追加成功尝试摘要，确保日志写入时候选序列已包含本次成功喵。
	if executionState, foundState := getVirtualModelExecutionState(c); foundState && executionState != nil && executionState.currentCandidateIndex >= 0 && executionState.currentCandidateIndex < len(executionState.executionSnapshot.Candidates) {
		currentCandidate := executionState.executionSnapshot.Candidates[executionState.currentCandidateIndex]
		appendVirtualModelCandidateAttempt(c, model.VirtualModelCandidateAttemptRecord{
			Seq:         currentVirtualModelCandidateSeq(c),
			CandidateID: currentCandidate.CandidateID,
			Source:      "internal",
			Label:       buildVirtualModelAttemptLabel(&currentCandidate, currentCandidate.RealModelName),
			Success:     true,
			StatusCode:  http.StatusOK,
			// 候选尝试序列展示该候选自己的耗时（模型级口径）：首字取上游响应头到达，总耗时取候选启动到当前喵。
			TtftMs:    executionResult.TtftMs,
			ElapsedMs: time.Since(startTime).Milliseconds(),
		})
	}
	// 成功透传计入成功，随后按独立 RMB 计费系统扣费与写日志；共享调用免费只累计共享消耗喵。
	recordUpstreamModelProbeState(upstreamModel, isShared, true, true, "", startTime, buildUpstreamProbeExtras(executionResult))
	settleUserUpstreamModelCharge(c, upstreamModel.OwnerUserID, upstreamModel, executionResult.Usage, modelRequest.Group, isUpstreamModelRequestStreaming(c), int(requestElapsedMs/1000), isShared, requestFirstByteMs, preConsumedCents)
	// 虚拟模型 user/xxx 内部候选成功：记录候选与整体成功样本，供虚拟模型状态监控展示喵。
	if executionState, foundState := getVirtualModelExecutionState(c); foundState && executionState != nil && executionState.currentCandidateIndex >= 0 && executionState.currentCandidateIndex < len(executionState.executionSnapshot.Candidates) {
		currentCandidate := executionState.executionSnapshot.Candidates[executionState.currentCandidateIndex]
		recordVirtualModelProbeSuccess(c, executionState, currentCandidate.CandidateID, executionResult.Usage, requestFirstByteMs)
	}
	c.Abort()
	return false
}

// virtualModelUpstreamFailureOutcome 描述虚拟模型 user/xxx 内部候选上游失败后的编排结果喵。
type virtualModelUpstreamFailureOutcome int

const (
	// virtualModelUpstreamFailureEnd 表示请求已终结（透传错误、候选链耗尽或响应已提交）喵。
	virtualModelUpstreamFailureEnd virtualModelUpstreamFailureOutcome = iota
	// virtualModelUpstreamFailureRetry 表示失败规则要求重试当前候选，调用方应退避后重放上游请求喵。
	virtualModelUpstreamFailureRetry
	// virtualModelUpstreamFailureNext 表示候选链已推进到新的 user/xxx 候选，调用方应重新分发喵。
	virtualModelUpstreamFailureNext
)

// handleVirtualModelUpstreamFailure 在虚拟模型 user/xxx 内部候选上游失败时按失败规则决策并推进候选链喵。
// 候选未配置规则时自动回退模型级全局兜底规则；返回 retry/next/end 由调用方编排执行喵。
func handleVirtualModelUpstreamFailure(c *gin.Context, upstreamModel *model.UserUpstreamModel, isShared bool, customFailure *virtualmodelservice.CustomCandidateExecutionFailure, startTime time.Time) virtualModelUpstreamFailureOutcome {
	executionState, foundState := getVirtualModelExecutionState(c)
	// 喵~防御：缺少执行状态或候选越界时按终结处理，由调用方回退透传喵。
	if !foundState || executionState == nil || executionState.currentCandidateIndex < 0 || executionState.currentCandidateIndex >= len(executionState.executionSnapshot.Candidates) {
		return virtualModelUpstreamFailureEnd
	}
	currentCandidate := executionState.executionSnapshot.Candidates[executionState.currentCandidateIndex]
	candidateID := currentCandidate.CandidateID
	errorClass := customFailure.Failure.ErrorClass
	// 记录该 user/xxx 内部候选的失败尝试摘要，供最终日志展示候选链故障转移过程喵。
	appendVirtualModelCandidateAttempt(c, model.VirtualModelCandidateAttemptRecord{
		Seq:          currentVirtualModelCandidateSeq(c),
		CandidateID:  candidateID,
		Source:       "internal",
		Label:        buildVirtualModelAttemptLabel(&currentCandidate, currentCandidate.RealModelName),
		Success:      false,
		StatusCode:   customFailure.Failure.HTTPStatus,
		ErrorClass:   errorClass,
		ErrorMessage: errorClass,
		// 模型级错误返回体取上游响应体受限摘要（最多 64 KiB），供详情点击复制喵。
		ErrorBody:  customFailure.Failure.BodyPreview,
		ElapsedMs:  time.Since(startTime).Milliseconds(),
		RetryCount: executionState.ruleRetryCounts[candidateID],
	})
	// 自动避险独立于失效规则：候选配置了连续失败阈值时，无论失效规则决策为何都累计连续失败，达阈值即冻结退避喵。
	hedgeFroze, hedgeError := virtualmodelservice.RecordCandidateAutoHedge(executionState.ownerUserID, currentCandidate, customFailure.Failure, common.GetTimestamp())
	// 喵~防御：连续失败计数写入失败时保守终结候选链，避免上游故障扩大为循环请求喵。
	if hedgeError != nil {
		// 实体状态检测：计数写入失败终结请求，记录候选、上游模型与整体失败喵。
		recordVirtualModelUpstreamFailureProbe(c, executionState, candidateID, upstreamModel, isShared, errorClass, startTime, true)
		abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "virtual model execution is not available", types.ErrorCode("virtual_model_unavailable"))
		return virtualModelUpstreamFailureEnd
	}
	// 同步内存快照：自动避险达阈值冻结后，本次请求后续候选激活立即跳过刚冻结的内部候选喵。
	if hedgeFroze {
		executionState.internalFreezeStatesByCandidate[candidateID] = model.VirtualModelInternalFreezeState{CandidateID: candidateID, FrozenUntil: common.GetTimestamp() + int64(currentCandidate.HedgeFreezeSeconds)}
	}
	// 按失败规则决策动作；候选未配置规则时回退模型级全局兜底规则喵。
	action, freezeSeconds, ruleRetryCount := virtualmodelservice.DecideVirtualModelFailureAction(executionState.executionSnapshot, candidateID, customFailure.Failure)
	// 失败规则 retry：规则级最大重试次数优先，未配置时回退候选 MaxRetries，在其内重放当前上游请求喵。
	// 喵~防御：自动避险刚达阈值冻结该候选时不得再重试，直接进入候选切换退避喵。
	maxUpstreamRetries := currentCandidate.MaxRetries
	if ruleRetryCount > 0 {
		maxUpstreamRetries = ruleRetryCount
	}
	if action == model.VirtualModelActionRetry && executionState.ruleRetryCounts[candidateID] < maxUpstreamRetries && !hedgeFroze {
		executionState.ruleRetryCounts[candidateID]++
		return virtualModelUpstreamFailureRetry
	}
	// 失败规则 freeze：命中规则时单次失败立即在 owner 范围内冻结指定时长，随后跳过该候选喵。
	if action == model.VirtualModelActionFreeze && freezeSeconds > 0 {
		frozenUntil := common.GetTimestamp() + int64(freezeSeconds)
		freezeError := model.UpsertVirtualModelInternalFreezeState(executionState.ownerUserID, candidateID, frozenUntil, errorClass, common.GetTimestamp())
		// 喵~防御：冻结状态写入失败时保守终结候选链，避免上游故障扩大为循环请求喵。
		if freezeError != nil {
			// 实体状态检测：冻结失败终结请求，记录候选、上游模型与整体失败喵。
			recordVirtualModelUpstreamFailureProbe(c, executionState, candidateID, upstreamModel, isShared, errorClass, startTime, true)
			abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "virtual model execution is not available", types.ErrorCode("virtual_model_unavailable"))
			return virtualModelUpstreamFailureEnd
		}
		// 同步内存快照，使本次请求后续候选激活立即跳过刚冻结的内部候选喵。
		executionState.internalFreezeStatesByCandidate[candidateID] = model.VirtualModelInternalFreezeState{CandidateID: candidateID, FrozenUntil: frozenUntil}
	}
	// 失败规则 passthrough：把受限上游错误正文原样回传客户端喵。
	if action == model.VirtualModelActionPassthrough {
		// 实体状态检测：透传失败终结请求，记录候选、上游模型与整体失败喵。
		recordVirtualModelUpstreamFailureProbe(c, executionState, candidateID, upstreamModel, isShared, errorClass, startTime, true)
		// 失败日志：记录本次透传错误，候选尝试序列由日志落库时注入喵。
		requestGroup := ""
		if executionState.modelRequest != nil {
			requestGroup = executionState.modelRequest.Group
		}
		recordUserUpstreamModelFailureLog(c, upstreamModel, isShared, requestGroup, customFailure, startTime)
		// 喵~防御：仅回传已受限缓冲的上游错误正文和过滤后的响应头，禁止写入 hop-by-hop 字段喵。
		// 2xx 内嵌的 SSE error 事件也携带错误正文原样透传，仅网络类失败才回退通用错误喵。
		if len(customFailure.ResponseBody) > 0 || customFailure.Failure.HTTPStatus >= http.StatusBadRequest {
			virtualmodelservice.CopyCustomPassthroughResponse(c.Writer, customFailure.ResponseHeaders, customFailure.Failure.HTTPStatus, customFailure.ResponseBody)
		} else {
			abortWithOpenAiMessage(c, http.StatusBadGateway, "user upstream model is unavailable", types.ErrorCode("upstream_model_unavailable"))
		}
		c.Abort()
		return virtualModelUpstreamFailureEnd
	}
	// next 与不可继续 retry/freeze：尝试激活候选链后续候选喵。
	if activateNextVirtualModelCandidate(c, executionState) {
		// 实体状态检测：候选链已推进，记录该候选失败样本（整体结果由后续候选决定）喵。
		recordVirtualModelUpstreamFailureProbe(c, executionState, candidateID, upstreamModel, isShared, errorClass, startTime, false)
		return virtualModelUpstreamFailureNext
	}
	// 喵~防御：custom 候选可能已同步提交响应，此时不得二次写响应喵。
	if c.Writer != nil && c.Writer.Written() {
		recordVirtualModelUpstreamFailureProbe(c, executionState, candidateID, upstreamModel, isShared, errorClass, startTime, true)
		c.Abort()
		return virtualModelUpstreamFailureEnd
	}
	// 实体状态检测：候选链彻底耗尽且无后备候选接管，记录候选、上游模型与整体失败喵。
	recordVirtualModelUpstreamFailureProbe(c, executionState, candidateID, upstreamModel, isShared, errorClass, startTime, true)
	// 失败日志：候选链耗尽最终失败，记录错误信息与候选尝试序列喵。
	requestGroup := ""
	if executionState.modelRequest != nil {
		requestGroup = executionState.modelRequest.Group
	}
	recordUserUpstreamModelFailureLog(c, upstreamModel, isShared, requestGroup, customFailure, startTime)
	// 上游返回了 HTTP 错误时按状态码透传；2xx 内嵌的 SSE error 事件也携带错误正文原样透传，仅网络类失败才回退通用错误喵。
	if len(customFailure.ResponseBody) > 0 || customFailure.Failure.HTTPStatus >= http.StatusBadRequest {
		virtualmodelservice.CopyCustomPassthroughResponse(c.Writer, customFailure.ResponseHeaders, customFailure.Failure.HTTPStatus, customFailure.ResponseBody)
	} else {
		abortWithOpenAiMessage(c, http.StatusBadGateway, "user upstream model is unavailable", types.ErrorCode("upstream_model_unavailable"))
	}
	c.Abort()
	return virtualModelUpstreamFailureEnd
}

// virtualModelUpstreamRetryDelay 计算 user/xxx 候选失败规则 retry 的退避时长喵。
// 返回 (时长, 是否可继续)；总 deadline 已耗尽或状态缺失时返回 (0, false) 喵。
func virtualModelUpstreamRetryDelay(c *gin.Context) (time.Duration, bool) {
	executionState, foundState := getVirtualModelExecutionState(c)
	// 喵~防御：状态缺失或候选越界时不允许重试，避免无界重放喵。
	if !foundState || executionState == nil || executionState.currentCandidateIndex < 0 || executionState.currentCandidateIndex >= len(executionState.executionSnapshot.Candidates) {
		return 0, false
	}
	candidateID := executionState.executionSnapshot.Candidates[executionState.currentCandidateIndex].CandidateID
	// 退避按已记录的重试次数增长；helper 返回 retry 前已递增计数，故首轮等待 2 秒喵。
	retryDelay := time.Duration(virtualmodelservice.RetryBackoffSeconds(executionState.ruleRetryCounts[candidateID])) * time.Second
	// 喵~防御：退避不得越过总 deadline，剩余时间不足时截断到剩余时间喵。
	if !executionState.requestDeadline.IsZero() {
		remainingDuration := time.Until(executionState.requestDeadline)
		if remainingDuration <= 0 {
			return 0, false
		}
		if retryDelay > remainingDuration {
			retryDelay = remainingDuration
		}
	}
	return retryDelay, true
}

// recordVirtualModelUpstreamFailureProbe 记录 user/xxx 内部候选失败及其引用上游模型的失败样本喵。
// hasResponse 表示候选已写出最终响应（请求终结），此时一并记录虚拟模型整体失败喵。
func recordVirtualModelUpstreamFailureProbe(c *gin.Context, executionState *virtualModelExecutionState, candidateID int, upstreamModel *model.UserUpstreamModel, isShared bool, errorClass string, startTime time.Time, hasResponse bool) {
	// 候选级延迟取该候选激活起的耗时，与内部候选原生失败口径一致喵。
	recordVirtualModelCandidateProbe(c, executionState, candidateID, false, errorClass, time.Since(startTime).Milliseconds())
	if upstreamModel != nil {
		recordUpstreamModelProbeState(upstreamModel, isShared, true, false, errorClass, startTime, upstreamProbeExtras{})
	}
	if hasResponse {
		RecordVirtualModelOverallProbe(c, false, errorClass)
	}
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
		TtftMs:                extras.ttftMs,
		HasTtft:               extras.ttftMs > 0,
		OutputTokens:          extras.outputTokens,
		GenerationMs:          generationMs,
		InputTokens:           extras.inputTokens,
		CachedTokens:          extras.cachedTokens,
		CacheHit:              extras.cacheHit,
		CacheSample:           extras.cacheSample,
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
	promptTokens, completionTokens, cachedTokens, cacheCreationTokens, imageTokens, audioTokens, videoTokens, videoOutputTokens, cacheCreation5mTokens, cacheCreation1hTokens := splitUpstreamModelUsage(usage)
	// 估计标记：usage 由文本估算而来（上游未返回 token 计数），供日志与前端展示「?」喵。
	estimatedTokens := usage != nil && usage.BillingUsage != nil && usage.BillingUsage.Estimated
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
		"video_tokens":             videoTokens,
		"video_output_tokens":      videoOutputTokens,
		"usage_available":          usage != nil,
		"estimated_tokens":         estimatedTokens,
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

// recordUserUpstreamModelFailureLog 在 user/xxx 候选最终失败（透传错误或候选链耗尽）时记录失败日志喵。
// 与成功结算口径一致：非虚拟上下文写 type=8，虚拟上下文写 type=9 并注入候选尝试序列喵。
// 失败也计入成功率与最近失败状态由 recordUpstreamModelProbeState / RecordEntityProbeCounted 负责喵。
func recordUserUpstreamModelFailureLog(c *gin.Context, upstreamModel *model.UserUpstreamModel, isShared bool, group string, customFailure *virtualmodelservice.CustomCandidateExecutionFailure, startTime time.Time) {
	// 喵~防御：空模型或空失败对象不写日志，避免脏数据喵。
	if c == nil || upstreamModel == nil || customFailure == nil {
		return
	}
	// 共享调用日志归属当前调用者，自用归模型属主喵。
	logUserID := upstreamModel.OwnerUserID
	if isShared {
		logUserID = c.GetInt("id")
	}
	// 分组：共享固定归入 user-shared，自用沿用请求分组并兜底默认值喵。
	effectiveGroup := strings.TrimSpace(group)
	if isShared {
		effectiveGroup = constant.GroupUserShared
	} else if effectiveGroup == "" {
		effectiveGroup = defaultUserUpstreamGroupName
	}
	// 错误内容摘要：优先取上游错误正文受限摘要，否则回退稳定错误分类喵。
	contentText := customFailure.Failure.ErrorClass
	if bodyPreview := strings.TrimSpace(customFailure.Failure.BodyPreview); bodyPreview != "" {
		contentText = bodyPreview
		// 喵~防御：正文摘要过长时截断到日志列宽内，避免拖慢日志存储喵。
		if len(contentText) > 512 {
			contentText = contentText[:512]
		}
	}
	other := map[string]interface{}{
		"custom_cost_rmb": "0.0000",
		"is_shared_call":  isShared,
		"error_class":     customFailure.Failure.ErrorClass,
		"http_status":     customFailure.Failure.HTTPStatus,
		"final_success":   false,
	}
	if virtualModelName := common.GetContextKeyString(c, constant.ContextKeyVirtualModelName); virtualModelName != "" {
		other["virtual_model"] = virtualModelName
	}
	// 总耗时取请求级（虚拟上下文请求入口）或候选级基准，与成功日志口径一致喵。
	useTimeSeconds := int(time.Since(startTime).Seconds())
	if executionState, foundState := getVirtualModelExecutionState(c); foundState && executionState != nil && !executionState.startTime.IsZero() {
		useTimeSeconds = int(time.Since(executionState.startTime).Seconds())
	}
	model.RecordUserUpstreamModelLog(c, logUserID, model.RecordUserUpstreamModelLogParams{
		ModelName:      upstreamModel.UserUpstreamModelName(),
		Content:        contentText,
		UseTimeSeconds: useTimeSeconds,
		IsStream:       isUpstreamModelRequestStreaming(c),
		Group:          effectiveGroup,
		Other:          other,
	})
}

// splitUpstreamModelUsage 从 usage 提取各 token 分类数，空 usage 全部回退为零喵。
func splitUpstreamModelUsage(usage *dto.Usage) (promptTokens int, completionTokens int, cachedTokens int, cacheCreationTokens int, imageTokens int, audioTokens int, videoTokens int, videoOutputTokens int, cacheCreation5mTokens int, cacheCreation1hTokens int) {
	// 喵~防御：无 usage 时返回全零，避免空指针喵。
	if usage == nil {
		return 0, 0, 0, 0, 0, 0, 0, 0, 0, 0
	}
	promptTokens = usage.PromptTokens
	completionTokens = usage.CompletionTokens
	cachedTokens = usage.PromptTokensDetails.CachedTokens
	cacheCreationTokens = usage.PromptTokensDetails.CacheCreationTokensTotal()
	imageTokens = usage.PromptTokensDetails.ImageTokens
	audioTokens = usage.PromptTokensDetails.AudioTokens
	// 视频分类：输入与输出分离提取，供独立 RMB 日志展示喵。
	videoTokens = usage.PromptTokensDetails.VideoTokens
	videoOutputTokens = usage.CompletionTokenDetails.VideoTokens
	cacheCreation5mTokens = usage.ClaudeCacheCreation5mTokens
	cacheCreation1hTokens = usage.ClaudeCacheCreation1hTokens
	return promptTokens, completionTokens, cachedTokens, cacheCreationTokens, imageTokens, audioTokens, videoTokens, videoOutputTokens, cacheCreation5mTokens, cacheCreation1hTokens
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
