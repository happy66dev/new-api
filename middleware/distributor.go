package middleware

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	virtualmodelservice "github.com/QuantumNous/new-api/service/virtualmodel"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type ModelRequest struct {
	Model string `json:"model"`
	Group string `json:"group,omitempty"`
}

func Distribute() func(c *gin.Context) {
	return func(c *gin.Context) {
		var channel *model.Channel
		selectedModel := ""
		channelId, ok := common.GetContextKey(c, constant.ContextKeyTokenSpecificChannelId)
		modelRequest, shouldSelectChannel, err := getModelRequest(c)
		if err != nil {
			abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": err.Error()}))
			return
		}
		if shouldSelectChannel && isVirtualModelRequest(modelRequest.Model) {
			handled := handleVirtualModelRequest(c, modelRequest)
			// 活跃请求退出：请求链完成（含 custom 候选同步结束）后统一清理注册表喵。
			if executionState, foundState := getVirtualModelExecutionState(c); foundState && executionState.inflightRequestID != "" {
				defer ExitVirtualModelInflight(int64(executionState.virtualModelID), executionState.inflightRequestID)
			}
			if !handled {
				return
			}
			// 虚拟模型 user/xxx 内部候选：上游失败按失败规则推进候选链时循环重分发，
			// 直到新候选不再是 user/xxx（交给下方普通 channel 选择）或请求已终结喵。
			for shouldSelectChannel && isUserUpstreamModelRequest(modelRequest.Model) {
				if !handleUserUpstreamModelRequest(c, modelRequest) {
					return
				}
			}
		}
		// 用户上游模型直接调用：透传后终止，不进入普通 channel 选择喵。
		if shouldSelectChannel && isUserUpstreamModelRequest(modelRequest.Model) {
			if !handleUserUpstreamModelRequest(c, modelRequest) {
				return
			}
		}
		if ok {
			id, err := strconv.Atoi(channelId.(string))
			if err != nil {
				abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgDistributorInvalidChannelId))
				return
			}
			channel, err = model.GetChannelById(id, true)
			if err != nil {
				abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgDistributorInvalidChannelId))
				return
			}
			if channel.Status != common.ChannelStatusEnabled {
				abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgDistributorChannelDisabled))
				return
			}
		} else {
			// Select a channel for the user
			// check token model mapping
			modelLimitEnable := common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled)
			if modelLimitEnable {
				s, ok := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
				if !ok {
					// token model limit is empty, all models are not allowed
					abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgDistributorTokenNoModelAccess))
					return
				}
				var tokenModelLimit map[string]bool
				tokenModelLimit, ok = s.(map[string]bool)
				if !ok {
					tokenModelLimit = map[string]bool{}
				}
				matchName := ratio_setting.FormatMatchingModelName(modelRequest.Model) // match gpts & thinking-*
				if _, ok := tokenModelLimit[matchName]; !ok {
					abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgDistributorTokenModelForbidden, map[string]any{"Model": modelRequest.Model}))
					return
				}
			}

			selectedModel = modelRequest.Model
			if shouldSelectChannel {
				if modelRequest.Model == "" {
					abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgDistributorModelNameRequired))
					return
				}
				var selectGroup string
				usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
				if strings.HasPrefix(c.Request.URL.Path, "/pg/") && modelRequest.Group != "" {
					if !service.GetRequestUserGroupAccess(c).Allows(modelRequest.Group) && modelRequest.Group != usingGroup {
						abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgDistributorGroupAccessDenied))
						return
					}
					usingGroup = modelRequest.Group
					common.SetContextKey(c, constant.ContextKeyUsingGroup, usingGroup)
				}

				if preferredChannelID, found := service.GetPreferredChannelByAffinity(c, modelRequest.Model, usingGroup); found {
					affinityUsable := false
					affinityModel := modelRequest.Model
					if usingGroup == "auto" {
						if route, ok := service.GetRequestAutoRoute(c, modelRequest.Model); ok && len(route) > 0 {
							affinityModel = route[0]
							selectedModel = affinityModel
						}
					}
					preferred, err := model.CacheGetChannel(preferredChannelID)
					if err == nil && preferred != nil && preferred.Status == common.ChannelStatusEnabled &&
						channelSupportsRequestPath(preferred, canonicalRelayPath(c.Request.URL.Path), affinityModel) {
						if usingGroup == "auto" {
							userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
							autoGroups := service.GetRequestAutoGroups(c, userGroup)
							for _, g := range autoGroups {
								if model.IsChannelEnabledForGroupModel(g, affinityModel, preferred.Id) && service.ChannelSupportsVirtualModel(preferred, modelRequest.Model) {
									selectGroup = g
									common.SetContextKey(c, constant.ContextKeyAutoGroup, g)
									channel = preferred
									affinityUsable = true
									service.MarkChannelAffinityUsed(c, g, preferred.Id)
									break
								}
							}
						} else if model.IsChannelEnabledForGroupModel(usingGroup, modelRequest.Model, preferred.Id) {
							channel = preferred
							selectGroup = usingGroup
							affinityUsable = true
							service.MarkChannelAffinityUsed(c, usingGroup, preferred.Id)
						}
					}
					if !affinityUsable && !service.ShouldKeepChannelAffinityOnChannelDisabled() {
						service.ClearCurrentChannelAffinityCache(c)
					}
				}

				if channel == nil {
					retryParam := &service.RetryParam{
						Ctx:         c,
						ModelName:   modelRequest.Model,
						TokenGroup:  usingGroup,
						RequestPath: canonicalRelayPath(c.Request.URL.Path),
						Retry:       common.GetPointer(0),
					}
					channel, selectGroup, err = service.CacheGetRandomSatisfiedChannel(retryParam)
					if retryParam.SelectedModel != "" {
						selectedModel = retryParam.SelectedModel
					}
					if err != nil {
						showGroup := usingGroup
						if usingGroup == "auto" {
							showGroup = fmt.Sprintf("auto(%s)", selectGroup)
						}
						message := i18n.T(c, i18n.MsgDistributorGetChannelFailed, map[string]any{"Group": showGroup, "Model": modelRequest.Model, "Error": err.Error()})
						// 如果错误，但是渠道不为空，说明是数据库一致性问题
						//if channel != nil {
						//	common.SysError(fmt.Sprintf("渠道不存在：%d", channel.Id))
						//	message = "数据库一致性已被破坏，请联系管理员"
						//}
						abortWithOpenAiMessage(c, http.StatusServiceUnavailable, message, types.ErrorCodeModelNotFound)
						return
					}
					if channel == nil {
						abortWithOpenAiMessage(c, http.StatusServiceUnavailable, i18n.T(c, i18n.MsgDistributorNoAvailableChannel, map[string]any{"Group": usingGroup, "Model": modelRequest.Model}), types.ErrorCodeModelNotFound)
						return
					}
				}
				common.SetContextKey(c, constant.ContextKeySelectedModel, selectedModel)
			}
		}
		common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())
		// 内部模型活跃请求计数：virtual/ 与 user/ 命名空间已单独计数，这里只统计普通内部模型喵。
		// 虚拟模型内部候选会把 modelRequest.Model 改写为真实模型名，因此同样计入对应内部模型的活跃请求喵。
		relayModelName := strings.TrimSpace(modelRequest.Model)
		if shouldSelectChannel && relayModelName != "" && !isVirtualModelRequest(relayModelName) && !isUserUpstreamModelRequest(relayModelName) {
			EnterInternalModelInflight(relayModelName)
			defer ExitInternalModelInflight(relayModelName)
		}
		SetupContextForSelectedChannel(c, channel, modelRequest.Model)
		if shouldSelectChannel {
			common.SetContextKey(c, constant.ContextKeySelectedModel, selectedModel)
		}
		c.Next()
		if channel != nil && c.Writer != nil && c.Writer.Status() < http.StatusBadRequest {
			service.RecordChannelAffinity(c, channel.Id)
		}
	}
}

// isVirtualModelRequest 判断请求模型是否进入独立虚拟模型命名空间喵。
func isVirtualModelRequest(modelName string) bool {
	return strings.HasPrefix(strings.TrimSpace(modelName), "virtual/")
}

// virtualModelExecutionState 保存单个请求不可变的候选、规则、冻结和原始 JSON 请求体喵。
// 主人注意：虚拟模型会保留原始请求体以便跨内部候选重写 model；请求体上限仍受全局 BodyStorage 限制喵。
type virtualModelExecutionState struct {
	virtualModelName                string
	virtualModelID                  int
	ownerUserID                     int
	executionSnapshot               *model.VirtualModelExecutionSnapshot
	manualFrozenCandidateIDs        map[int]bool
	automaticFreezeStatesByIdentity map[string]model.VirtualModelCustomFreezeState
	// internalFreezeStatesByCandidate 保存请求启动时观察到的内部候选自动冻结状态，成功清除时用其版本防并发喵。
	internalFreezeStatesByCandidate map[int]model.VirtualModelInternalFreezeState
	// ruleRetryCounts 记录失败规则对内部候选的重试次数，防止规则 retry 无限重放候选喵。
	ruleRetryCounts           map[int]int
	originalRequestBody       []byte
	modelRequest              *ModelRequest
	requestDeadline           time.Time
	loopEnabled               bool
	loopRoundsCompleted       int
	maximumLoopRounds         int
	currentCandidateIndex     int
	skippedCandidateIDs       map[int]bool
	candidateAttemptSequence  int    // 请求内已分配的候选尝试序号，单调递增，单位：次喵。
	currentCandidateAttemptID string // 当前已激活候选尝试的请求内唯一标识，未激活时为空喵。
	// startTime 虚拟模型请求的整体开始时间，用于整体状态检测延迟（含失败候选尝试）喵。
	startTime time.Time
	// currentCandidateStartedAt 当前激活候选的开始时间，用于候选级状态检测延迟喵。
	currentCandidateStartedAt time.Time
	// overallProbeRecorded 防止整体状态样本重复记录喵。
	overallProbeRecorded bool
	// successExtras 最近一次成功调用的吞吐/TTFT 样本，供整体与候选探测记录填充 token 喵。
	successExtras perfmetrics.EntityProbeExtras
	// inflightRequestID 活跃请求注册表里的请求唯一标识，首个候选激活时登记喵。
	inflightRequestID string
	// fakeStreamEnabled 流转伪流开关：开启后上游流式全量缓存到 [DONE] 再一次性伪流发出喵。
	fakeStreamEnabled bool
	// streamCutAction 流转伪流断流时的处理措施，空表示跟随失败规则喵。
	streamCutAction model.VirtualModelFailureAction
	// streamCutRetries 流转伪流断流时对当前候选的重试次数喵。
	streamCutRetries int
	// streamCutRetryCounts 记录断流处理措施对候选的重试次数，防止断流 retry 无限重放喵。
	streamCutRetryCounts map[int]int
}

// handleVirtualModelRequest 验证虚拟模型授权、构造请求级快照并激活首个可执行候选喵。
func handleVirtualModelRequest(c *gin.Context, modelRequest *ModelRequest) bool {
	// 喵~防御：空 Gin 上下文无法安全写出 API 错误，直接终止以避免空指针喵。
	if c == nil {
		return false
	}
	// 喵~防御：空请求对象或功能关闭时立即拒绝，避免控制面数据意外进入既有 Channel 分发链喵。
	if modelRequest == nil {
		abortWithOpenAiMessage(c, http.StatusBadRequest, "invalid virtual model request", types.ErrorCode("virtual_model_invalid_request"))
		return false
	}
	if !model.VirtualModelFunctionEnabled() {
		abortWithOpenAiMessage(c, http.StatusNotFound, "virtual model execution is disabled", types.ErrorCode("virtual_model_disabled"))
		return false
	}
	normalizedName, normalizeError := model.NormalizeVirtualModelName(modelRequest.Model)
	// 喵~防御：无效名称不触发数据库查询，避免异常输入扩大资源占用或泄露校验细节喵。
	if normalizeError != nil {
		abortWithOpenAiMessage(c, http.StatusBadRequest, "invalid virtual model request", types.ErrorCode("virtual_model_invalid_request"))
		return false
	}
	ownerUserID := c.GetInt("id")
	tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	// API Key 请求按 token 绑定授权；会话态请求（如游乐场 /pg 路径）没有 token_id，按 owner 授权用户自己的虚拟模型喵。
	var virtualModel *model.VirtualModel
	var queryError error
	if tokenID > 0 {
		virtualModel, queryError = model.GetEnabledVirtualModelByOwnerTokenName(ownerUserID, tokenID, normalizedName)
	} else {
		virtualModel, queryError = model.GetEnabledVirtualModelByOwnerName(ownerUserID, normalizedName)
	}
	// 喵~防御：未绑定、停用、跨用户和查询失败统一隐藏资源存在性，避免模型枚举喵。
	if queryError != nil || virtualModel == nil {
		abortWithOpenAiMessage(c, http.StatusNotFound, "virtual model not found", types.ErrorCode("virtual_model_not_found"))
		return false
	}
	executionSnapshot, snapshotError := model.GetVirtualModelExecutionSnapshot(virtualModel.ID)
	// 喵~防御：无法构造候选和规则的一致快照时安全拒绝，避免执行混合版本配置喵。
	if snapshotError != nil || executionSnapshot == nil || len(executionSnapshot.Candidates) == 0 {
		abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "virtual model execution is not available", types.ErrorCode("virtual_model_unavailable"))
		return false
	}
	bodyStorage, bodyError := common.GetBodyStorage(c)
	// 喵~防御：无法读取原始 JSON 请求体时不允许候选切换，避免把已改写请求发给后备候选喵。
	if bodyError != nil {
		abortWithOpenAiMessage(c, http.StatusBadRequest, "invalid virtual model request", types.ErrorCode("virtual_model_invalid_request"))
		return false
	}
	originalRequestBody, bodyError := bodyStorage.Bytes()
	// 喵~防御：虚拟模型只支持可安全重放的有效 JSON 请求，防止重写 multipart 或损坏的请求体喵。
	if bodyError != nil || !gjson.ValidBytes(originalRequestBody) {
		abortWithOpenAiMessage(c, http.StatusBadRequest, "invalid virtual model request", types.ErrorCode("virtual_model_invalid_request"))
		return false
	}
	candidateIDs := make([]int, 0, len(executionSnapshot.Candidates))
	customIdentityDigests := make([]string, 0, len(executionSnapshot.Candidates))
	for _, candidateSnapshot := range executionSnapshot.Candidates {
		candidateIDs = append(candidateIDs, candidateSnapshot.CandidateID)
		if candidateSnapshot.SourceType == model.VirtualModelSourceCustom {
			customIdentityDigests = append(customIdentityDigests, virtualmodelservice.CustomCandidateIdentityDigest(candidateSnapshot))
		}
	}
	manualFrozenCandidateIDs, manualFreezeError := model.GetActiveVirtualModelManualFreezeCandidateIDs(candidateIDs, common.GetTimestamp())
	// 喵~防御：无法确认冻结状态时保守拒绝，避免冻结候选因数据库故障被执行喵。
	if manualFreezeError != nil {
		abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "virtual model execution is not available", types.ErrorCode("virtual_model_unavailable"))
		return false
	}
	automaticFreezeStatesByIdentity, automaticFreezeError := model.GetVirtualModelCustomFreezeStates(ownerUserID, customIdentityDigests, common.GetTimestamp())
	// 喵~防御：无法确认自动冻结状态时保守拒绝，避免冻结候选因数据库故障被执行喵。
	if automaticFreezeError != nil {
		abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "virtual model execution is not available", types.ErrorCode("virtual_model_unavailable"))
		return false
	}
	internalFreezeStatesByCandidate, internalFreezeError := model.GetActiveVirtualModelInternalFreezeStates(ownerUserID, candidateIDs, common.GetTimestamp())
	// 喵~防御：无法确认内部候选冻结状态时保守拒绝，避免冻结候选因数据库故障被执行喵。
	if internalFreezeError != nil {
		abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "virtual model execution is not available", types.ErrorCode("virtual_model_unavailable"))
		return false
	}
	// 喵~防御：内存候选快照必须与原始请求体完全隔离，避免调用方意外复用或修改底层缓存喵。
	originalRequestBodySnapshot := append([]byte(nil), originalRequestBody...)
	executionState := &virtualModelExecutionState{
		virtualModelName:                virtualModel.VirtualModelName(),
		virtualModelID:                  virtualModel.ID,
		ownerUserID:                     ownerUserID,
		executionSnapshot:               executionSnapshot,
		manualFrozenCandidateIDs:        manualFrozenCandidateIDs,
		automaticFreezeStatesByIdentity: automaticFreezeStatesByIdentity,
		internalFreezeStatesByCandidate: internalFreezeStatesByCandidate,
		ruleRetryCounts:                 make(map[int]int),
		originalRequestBody:             originalRequestBodySnapshot,
		modelRequest:                    modelRequest,
		requestDeadline:                 time.Now().Add(time.Duration(virtualModel.TotalTimeoutSeconds) * time.Second),
		loopEnabled:                     virtualModel.LoopEnabled,
		maximumLoopRounds:               virtualModel.MaxLoopRounds,
		currentCandidateIndex:           -1,
		skippedCandidateIDs:             make(map[int]bool),
		fakeStreamEnabled:               virtualModel.FakeStreamEnabled,
		streamCutAction:                 virtualModel.StreamCutAction,
		streamCutRetries:                virtualModel.StreamCutRetries,
		streamCutRetryCounts:            make(map[int]int),
		// 整体与候选延迟都从请求进入虚拟层开始计时喵。
		startTime:                 time.Now(),
		currentCandidateStartedAt: time.Now(),
	}
	common.SetContextKey(c, constant.ContextKeyVirtualModelExecutionState, executionState)
	// 虚拟模型请求进入虚拟层的时刻写入 context，供日志/候选序列耗时改请求级口径（所有候选时间总和）喵。
	common.SetContextKey(c, constant.ContextKeyVirtualModelStartTime, executionState.startTime)
	// 虚拟模型请求日志统一归入「虚拟模型」类型（internal 候选走消费日志时覆盖 type 为 9）喵。
	common.SetContextKey(c, constant.ContextKeyVirtualLogType, model.LogTypeVirtualModel)
	// 初始化候选尝试记录切片，供请求生命周期内各候选追加，最终随日志落库喵。
	candidateAttempts := make([]model.VirtualModelCandidateAttemptRecord, 0, len(executionSnapshot.Candidates))
	common.SetContextKey(c, constant.ContextKeyVirtualCandidateAttempts, &candidateAttempts)
	if activateNextVirtualModelCandidate(c, executionState) {
		return true
	}
	// 喵~防御：自定义候选已提交响应或错误时不得继续原生 Channel 分发，避免重复写响应喵。
	if c.Writer != nil && c.Writer.Written() {
		return false
	}
	// 实体状态检测：首个候选激活即失败且无响应写出，记录虚拟模型整体失败喵。
	RecordVirtualModelOverallProbe(c, false, "virtual_model_unavailable")
	abortWithOpenAiMessage(c, http.StatusBadGateway, "virtual model execution is not available", types.ErrorCode("virtual_model_unavailable"))
	return false
}

// VirtualModelNativeFailureDecision 描述内部候选原生失败后应采取的编排动作喵。
type VirtualModelNativeFailureDecision struct {
	// RetryCurrentCandidate 表示失败规则要求重试当前内部候选，调用方应重新走原生分发喵。
	RetryCurrentCandidate bool
	// NextCandidateActivated 表示已激活下一个候选，调用方应切换独立 RelayInfo 喵。
	NextCandidateActivated bool
	// CustomCandidateCommitted 表示已有自定义候选提交最终响应，调用方不得再追加错误正文喵。
	CustomCandidateCommitted bool
}

// AdvanceVirtualModelAfterNativeFailure 在一个内部候选完成全部原生 Channel 重试后按失败规则编排喵。
func AdvanceVirtualModelAfterNativeFailure(c *gin.Context, nativeError *types.NewAPIError) VirtualModelNativeFailureDecision {
	decision := VirtualModelNativeFailureDecision{}
	// 喵~防御：响应已提交、错误为空或不存在虚拟执行状态时绝不能重新选择候选喵。
	if c == nil || nativeError == nil || c.Request == nil || (c.Writer != nil && c.Writer.Written()) {
		return decision
	}
	executionState, foundState := getVirtualModelExecutionState(c)
	if !foundState || executionState.currentCandidateIndex < 0 || executionState.currentCandidateIndex >= len(executionState.executionSnapshot.Candidates) {
		return decision
	}
	// 喵~防御：总请求 deadline 到期后不得继续后备候选，避免每个候选 timeout 相加造成无界执行喵。
	if !executionState.requestDeadline.IsZero() && !time.Now().Before(executionState.requestDeadline) {
		return decision
	}
	currentCandidate := executionState.executionSnapshot.Candidates[executionState.currentCandidateIndex]
	// 喵~防御：只允许 JSON body 路径进行内部候选重写，避免 WebSocket、路径模型或表单请求在失败后被错误重放喵。
	if !strings.HasPrefix(strings.ToLower(c.Request.Header.Get("Content-Type")), "application/json") {
		return decision
	}
	// 喵~防御：仅当前内部候选才可从原生 relay 失败回切候选链，防止错误状态使 custom 候选被重复执行喵。
	if currentCandidate.SourceType != model.VirtualModelSourceInternal {
		return decision
	}
	// 仅识别到卡流或断流哨兵时才把执行错误传入分类，避免普通失败因 NewAPIError.Err 非空被误归 network_error 喵。
	var probeExecutionError error
	if errors.Is(nativeError, types.ErrStalledStream) || errors.Is(nativeError, types.ErrStreamCut) {
		probeExecutionError = nativeError
	}
	// 规范化失败规则可匹配的受限结果，候选未配置规则时自动回退模型级全局兜底规则喵。
	nativeFailure := virtualmodelservice.NormalizeCandidateFailure(nativeError.StatusCode, nil, nil, probeExecutionError)
	// 记录该 internal 候选的失败尝试摘要，供最终日志展示候选链故障转移过程喵。
	// 喵~防御：错误消息需受限截断，避免恶意超长消息撑大日志喵。
	internalErrorBody := nativeError.Error()
	const maxInternalErrorBodyBytes = 4096
	if len(internalErrorBody) > maxInternalErrorBodyBytes {
		internalErrorBody = internalErrorBody[:maxInternalErrorBodyBytes]
	}
	appendVirtualModelCandidateAttempt(c, model.VirtualModelCandidateAttemptRecord{
		Seq:          currentVirtualModelCandidateSeq(c),
		CandidateID:  currentCandidate.CandidateID,
		Source:       "internal",
		Label:        buildVirtualModelAttemptLabel(&currentCandidate, currentCandidate.RealModelName),
		Success:      false,
		StatusCode:   nativeFailure.HTTPStatus,
		ErrorClass:   nativeFailure.ErrorClass,
		ErrorMessage: nativeFailure.ErrorClass,
		// internal 候选错误返回体取错误对象消息（受限），原生 relay 不保留完整上游响应体喵。
		ErrorBody:  internalErrorBody,
		ElapsedMs:  time.Since(executionState.currentCandidateStartedAt).Milliseconds(),
		RetryCount: executionState.ruleRetryCounts[currentCandidate.CandidateID],
	})
	// 断流失败与普通失败统一走失败规则决策（候选规则优先，无则全局兜底），目标模式断流措施已由全局兜底规则代替喵。
	action, freezeSeconds, ruleRetryCount := virtualmodelservice.DecideVirtualModelFailureAction(executionState.executionSnapshot, currentCandidate.CandidateID, nativeFailure)
	if action == model.VirtualModelActionPassthrough {
		// 实体状态检测：内部候选失败按规则透传错误，记录候选失败与虚拟模型整体失败喵。
		RecordActiveVirtualModelCandidateProbe(c, false, nativeFailure.ErrorClass)
		RecordVirtualModelOverallProbe(c, false, nativeFailure.ErrorClass)
		return decision
	}
	// 失败规则 retry：规则级最大重试次数优先，未配置时回退候选 MaxRetries 重放当前内部候选喵。
	maxInternalRetries := currentCandidate.MaxRetries
	if ruleRetryCount > 0 {
		maxInternalRetries = ruleRetryCount
	}
	internalRetryCount := executionState.ruleRetryCounts[currentCandidate.CandidateID]
	if action == model.VirtualModelActionRetry && internalRetryCount < maxInternalRetries {
		executionState.ruleRetryCounts[currentCandidate.CandidateID]++
		// 恢复客户端原始请求体后重新改写为当前候选，供 relay 循环再次走原生分发喵。
		if restoreVirtualModelOriginalRequest(c, executionState.originalRequestBody) && applyInternalVirtualModelCandidate(c, executionState.modelRequest, executionState.virtualModelName, &currentCandidate) {
			decision.RetryCurrentCandidate = true
			return decision
		}
	}
	// 实体状态检测：内部候选完成全部原生重试且不再重试，记录该候选失败样本喵。
	RecordActiveVirtualModelCandidateProbe(c, false, nativeFailure.ErrorClass)
	// 失败规则 freeze：在 owner 范围内按候选编号冻结指定时长，随后跳过该候选喵。
	if action == model.VirtualModelActionFreeze && freezeSeconds > 0 {
		frozenUntil := common.GetTimestamp() + int64(freezeSeconds)
		freezeError := model.UpsertVirtualModelInternalFreezeState(executionState.ownerUserID, currentCandidate.CandidateID, frozenUntil, nativeFailure.ErrorClass, common.GetTimestamp())
		// 喵~防御：冻结状态写入失败时保守中止候选链，避免上游故障扩大为循环请求喵。
		if freezeError != nil {
			abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "virtual model execution is not available", types.ErrorCode("virtual_model_unavailable"))
			return decision
		}
		// 同步内存快照，使本次请求后续候选激活立即跳过刚冻结的内部候选喵。
		executionState.internalFreezeStatesByCandidate[currentCandidate.CandidateID] = model.VirtualModelInternalFreezeState{CandidateID: currentCandidate.CandidateID, FrozenUntil: frozenUntil}
	}
	// next 与不可继续 retry/freeze 都交由候选链尝试后续候选喵。
	if activateNextVirtualModelCandidate(c, executionState) {
		decision.NextCandidateActivated = true
		return decision
	}
	// 喵~防御：只有显式启用循环、尚未超出轮数且响应未提交时才允许回到链首，避免默认无限重试喵。
	if executionState.loopEnabled && executionState.loopRoundsCompleted+1 < executionState.maximumLoopRounds && (executionState.requestDeadline.IsZero() || time.Now().Before(executionState.requestDeadline)) {
		executionState.loopRoundsCompleted++
		executionState.currentCandidateIndex = -1
		executionState.skippedCandidateIDs = make(map[int]bool)
		if activateNextVirtualModelCandidate(c, executionState) {
			decision.NextCandidateActivated = true
			return decision
		}
	}
	if c.Writer != nil && c.Writer.Written() {
		decision.CustomCandidateCommitted = true
	}
	// 实体状态检测：候选链彻底耗尽且无后备候选接管时，记录虚拟模型整体失败喵。
	if !decision.RetryCurrentCandidate && !decision.NextCandidateActivated && !decision.CustomCandidateCommitted {
		RecordVirtualModelOverallProbe(c, false, nativeFailure.ErrorClass)
	}
	return decision
}

// ClearCurrentVirtualModelCandidateAutomaticFreeze 在内部候选成功后清除其请求启动时观察到的自动冻结状态喵。
// 成功清除失败只记录日志，不影响已提交的成功响应喵。
func ClearCurrentVirtualModelCandidateAutomaticFreeze(c *gin.Context) {
	// 喵~防御：缺少上下文时直接返回，普通请求无需任何处理喵。
	if c == nil {
		return
	}
	executionState, foundState := getVirtualModelExecutionState(c)
	// 喵~防御：候选索引越界说明候选尚未激活或已耗尽，不做任何清理喵。
	if !foundState || executionState.currentCandidateIndex < 0 || executionState.currentCandidateIndex >= len(executionState.executionSnapshot.Candidates) {
		return
	}
	currentCandidate := executionState.executionSnapshot.Candidates[executionState.currentCandidateIndex]
	// 只清除内部候选的自动冻结，自定义候选由透传执行成功路径自行清理喵。
	if currentCandidate.SourceType != model.VirtualModelSourceInternal {
		return
	}
	freezeState, wasFrozen := executionState.internalFreezeStatesByCandidate[currentCandidate.CandidateID]
	if !wasFrozen {
		return
	}
	// 喵~防御：只清除请求启动时观察到的冻结版本，避免成功请求清除并发失败刚写入的新冻结喵。
	if clearError := model.ClearVirtualModelInternalFreezeState(executionState.ownerUserID, currentCandidate.CandidateID, freezeState.UpdatedTime, common.GetTimestamp()); clearError != nil {
		common.SysError("failed to clear virtual model internal freeze state: " + clearError.Error())
	}
}

// VirtualModelCandidateAttempt 描述当前已激活候选尝试的身份与关联标识喵。
// 该结构只暴露可安全写入日志的字段，禁止携带上游凭据、完整 URL 或请求正文喵。
type VirtualModelCandidateAttempt struct {
	CandidateID         int                          // 候选编号，用于冻结、失败规则与审计关联喵。
	CandidateAttemptID  string                       // 请求内唯一的候选尝试标识喵。
	AttemptSequence     int                          // 请求内候选尝试序号，从 1 开始递增，单位：次喵。
	CandidateIndex      int                          // 候选在稳定顺序中的下标，从 0 开始喵。
	SourceType          model.VirtualModelSourceType // 候选来源类型，决定内部 relay 或自定义透传路径喵。
	RealModelName       string                       // 候选真实上游模型名称喵。
	GroupName           string                       // 候选固定分组名称，自定义候选为空喵。
	LoopRoundsCompleted int                          // 已完成的循环轮数，单位：轮喵。
}

// GetActiveVirtualModelCandidateAttempt 返回当前已激活候选尝试的身份摘要喵。
// 普通模型请求、Token AutoRoutes 请求以及候选尚未激活时都返回 false，
// 调用方必须在该情况下保留原有的请求级计费与日志语义喵。
func GetActiveVirtualModelCandidateAttempt(c *gin.Context) (VirtualModelCandidateAttempt, bool) {
	executionState, foundState := getVirtualModelExecutionState(c)
	// 喵~防御：缺少执行状态时说明这是普通请求，绝不返回候选身份喵。
	if !foundState {
		return VirtualModelCandidateAttempt{}, false
	}
	// 喵~防御：候选索引越界说明候选尚未激活或已耗尽，不返回可能过期的身份喵。
	if executionState.currentCandidateIndex < 0 || executionState.currentCandidateIndex >= len(executionState.executionSnapshot.Candidates) {
		return VirtualModelCandidateAttempt{}, false
	}
	// 喵~防御：尝试标识为空说明上一次候选切换未完成，不允许调用方据此建立计费喵。
	if executionState.currentCandidateAttemptID == "" {
		return VirtualModelCandidateAttempt{}, false
	}
	candidateSnapshot := executionState.executionSnapshot.Candidates[executionState.currentCandidateIndex]
	return VirtualModelCandidateAttempt{
		CandidateID:         candidateSnapshot.CandidateID,
		CandidateAttemptID:  executionState.currentCandidateAttemptID,
		AttemptSequence:     executionState.candidateAttemptSequence,
		CandidateIndex:      executionState.currentCandidateIndex,
		SourceType:          candidateSnapshot.SourceType,
		RealModelName:       candidateSnapshot.RealModelName,
		GroupName:           candidateSnapshot.GroupName,
		LoopRoundsCompleted: executionState.loopRoundsCompleted,
	}, true
}

// getVirtualModelExecutionState 从请求上下文安全读取私有执行状态喵。
func getVirtualModelExecutionState(c *gin.Context) (*virtualModelExecutionState, bool) {
	// 喵~防御：上下文或类型不匹配时不进行断言访问，避免中间件组合导致 panic 喵。
	if c == nil {
		return nil, false
	}
	value, found := common.GetContextKey(c, constant.ContextKeyVirtualModelExecutionState)
	if !found {
		return nil, false
	}
	executionState, validState := value.(*virtualModelExecutionState)
	return executionState, validState && executionState != nil && executionState.executionSnapshot != nil
}

// activateNextVirtualModelCandidate 按请求快照顺序跳过冻结候选并激活下一个内部或自定义候选喵。
func activateNextVirtualModelCandidate(c *gin.Context, executionState *virtualModelExecutionState) bool {
	// 喵~防御：状态不完整时拒绝候选切换，避免使用控制面后续修改后的数据喵。
	if c == nil || executionState == nil || executionState.executionSnapshot == nil {
		return false
	}
	// 喵~防御：候选推进必须服从总 deadline 与客户端取消，避免失效请求继续访问上游喵。
	if (!executionState.requestDeadline.IsZero() && !time.Now().Before(executionState.requestDeadline)) || (c.Request != nil && c.Request.Context().Err() != nil) {
		return false
	}
	// 进入候选选择前先清空上一个候选的尝试标识，避免切换失败后仍留下错误关联喵。
	executionState.currentCandidateAttemptID = ""
	for candidateIndex := executionState.currentCandidateIndex + 1; candidateIndex < len(executionState.executionSnapshot.Candidates); candidateIndex++ {
		candidateSnapshot := &executionState.executionSnapshot.Candidates[candidateIndex]
		if executionState.skippedCandidateIDs[candidateSnapshot.CandidateID] || executionState.manualFrozenCandidateIDs[candidateSnapshot.CandidateID] {
			continue
		}
		// 内部候选按候选编号检查自动冻结状态，与手动冻结一样在激活前跳过喵。
		if _, internalFrozen := executionState.internalFreezeStatesByCandidate[candidateSnapshot.CandidateID]; internalFrozen {
			continue
		}
		if candidateSnapshot.SourceType == model.VirtualModelSourceCustom {
			identityDigest := virtualmodelservice.CustomCandidateIdentityDigest(*candidateSnapshot)
			if _, automaticallyFrozen := executionState.automaticFreezeStatesByIdentity[identityDigest]; automaticallyFrozen {
				continue
			}
		}
		executionState.currentCandidateIndex = candidateIndex
		// 为本次候选尝试分配请求内唯一序号，供候选级计费幂等键与结构化日志隔离使用喵。
		candidateAttemptSequence := executionState.candidateAttemptSequence + 1
		candidateAttemptID, attemptIDError := virtualmodelservice.FormatCandidateAttemptID(candidateSnapshot.CandidateID, candidateAttemptSequence)
		// 喵~防御：无法分配唯一尝试标识时立即停止候选链，避免两个候选共享同一个计费幂等键喵。
		if attemptIDError != nil {
			return false
		}
		executionState.candidateAttemptSequence = candidateAttemptSequence
		executionState.currentCandidateAttemptID = candidateAttemptID
		// 候选序号 = 链上位置（1 起），供日志渠道字段展示「候选n」喵。
		candidateSeq := candidateIndex + 1
		common.SetContextKey(c, constant.ContextKeyVirtualCandidateSeq, candidateSeq)
		// 活跃请求注册：首个候选激活时进入注册表，候选切换时更新当前调用链喵。
		executionState.inflightRequestID = TrackVirtualModelCandidateInflight(int64(executionState.virtualModelID), executionState.virtualModelName, executionState.inflightRequestID, candidateSeq, candidateSnapshot.RealModelName)
		if candidateSnapshot.SourceType == model.VirtualModelSourceInternal {
			if restoreVirtualModelOriginalRequest(c, executionState.originalRequestBody) && applyInternalVirtualModelCandidate(c, executionState.modelRequest, executionState.virtualModelName, candidateSnapshot) {
				// 实体状态检测：候选激活成功，标记该候选延迟起点喵。
				executionState.currentCandidateStartedAt = time.Now()
				return true
			}
			// 喵~防御：内部候选无法安全接入原生 relay 时立即终止本次选择，避免把鉴权或配置错误误伪装为候选故障喵。
			executionState.currentCandidateAttemptID = ""
			return false
		}
		if candidateSnapshot.SourceType == model.VirtualModelSourceCustom {
			// 传入完整执行快照，让自定义候选执行内部按需回退模型级全局兜底规则喵。
			executeCustomVirtualModelCandidate(c, candidateSnapshot, executionState.executionSnapshot)
			// 自定义候选已提交响应时保留其尝试标识，便于最终日志定位真正写出响应的候选喵。
			if c.Writer != nil && c.Writer.Written() {
				return false
			}
			executionState.skippedCandidateIDs[candidateSnapshot.CandidateID] = true
		}
	}
	// 候选链耗尽时清空当前尝试标识，避免外层误判仍存在活动候选喵。
	executionState.currentCandidateAttemptID = ""
	return false
}

// restoreVirtualModelOriginalRequest 在每次内部候选激活前恢复客户端原始 JSON，再由候选替换顶层 model 喵。
func restoreVirtualModelOriginalRequest(c *gin.Context, originalRequestBody []byte) bool {
	// 喵~防御：空请求体或非 JSON 内容不得恢复，防止用错误数据覆盖当前请求喵。
	if c == nil || c.Request == nil || len(originalRequestBody) == 0 || !gjson.ValidBytes(originalRequestBody) || !strings.HasPrefix(strings.ToLower(c.Request.Header.Get("Content-Type")), "application/json") {
		return false
	}
	return common.ReplaceRequestBody(c, originalRequestBody) == nil
}

// appendVirtualModelCandidateAttempt 把一次候选尝试的可审计摘要追加到请求级候选尝试切片喵。
func appendVirtualModelCandidateAttempt(c *gin.Context, attempt model.VirtualModelCandidateAttemptRecord) {
	// 喵~防御：空上下文时直接返回，避免空指针喵。
	if c == nil {
		return
	}
	attempts, found := common.GetContextKeyType[*[]model.VirtualModelCandidateAttemptRecord](c, constant.ContextKeyVirtualCandidateAttempts)
	// 喵~防御：切片未初始化时静默跳过，候选尝试记录是日志增强，不阻塞请求主流程喵。
	if !found || attempts == nil || *attempts == nil {
		return
	}
	*attempts = append(*attempts, attempt)
}

// buildVirtualModelAttemptLabel 构造候选尝试的可辨识标识（internal: 真实模型名；custom: 模型名）喵。
func buildVirtualModelAttemptLabel(candidate *model.VirtualModelInternalCandidateSnapshot, fallbackModelName string) string {
	// 喵~防御：空候选回退到模型名，避免空标识喵。
	if candidate == nil {
		return fallbackModelName
	}
	return candidate.RealModelName
}

// currentVirtualModelCandidateSeq 返回当前激活候选在链上的序号（1 起），未激活时返回 0 喵。
func currentVirtualModelCandidateSeq(c *gin.Context) int {
	executionState, foundState := getVirtualModelExecutionState(c)
	// 喵~防御：无执行状态或候选未激活时返回 0，避免越界访问喵。
	if !foundState || executionState.currentCandidateIndex < 0 {
		return 0
	}
	return executionState.currentCandidateIndex + 1
}

// recordVirtualModelCustomSuccess 纯直填 custom 候选成功时写虚拟模型日志喵。
// usage 为候选透传解析出的上游计费信息（可能为 nil），token 计真实值喵。
// requestFirstByteMs 为请求级首字耗时（毫秒），供日志 Timing 展示，未测到时为零喵。
func recordVirtualModelCustomSuccess(c *gin.Context, useTimeSeconds int, usage *dto.Usage, requestFirstByteMs int64) {
	// 喵~防御：缺少上下文时跳过日志，避免空指针喵。
	if c == nil {
		return
	}
	executionState, foundState := getVirtualModelExecutionState(c)
	// 喵~防御：缺少执行状态或模型名时跳过日志，避免脏数据喵。
	if !foundState || executionState == nil || executionState.virtualModelName == "" {
		return
	}
	group := ""
	if executionState.modelRequest != nil {
		group = executionState.modelRequest.Group
	}
	// 喵~防御：空 usage 按零 token 处理，避免空指针喵。
	promptTokens := 0
	completionTokens := 0
	if usage != nil {
		promptTokens = usage.PromptTokens
		completionTokens = usage.CompletionTokens
	}
	// 纯直填 custom 候选以解析出的 usage 填 token；候选尝试序列由日志 Other 承载喵。
	other := map[string]interface{}{
		"virtual_model": executionState.virtualModelName,
		"final_success": true,
	}
	// 请求级首字耗时写入日志 Timing 展示，纯直填 custom 候选此前未记录首字喵。
	if requestFirstByteMs > 0 {
		other["frt"] = requestFirstByteMs
	}
	model.RecordVirtualModelLog(c, c.GetInt("id"), model.RecordVirtualModelLogParams{
		ModelName:        executionState.virtualModelName,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		UseTimeSeconds:   useTimeSeconds,
		IsStream:         isUpstreamModelRequestStreaming(c),
		Group:            group,
		Other:            other,
	})
}

// RecordActiveVirtualModelCandidateProbe 记录当前激活候选的被动统计样本喵。
// 供内部候选原生 relay 成功/失败后由外部调用，候选延迟取请求级基准（请求入口到本次探测）喵。
func RecordActiveVirtualModelCandidateProbe(c *gin.Context, success bool, errorClass string) {
	executionState, foundState := getVirtualModelExecutionState(c)
	// 喵~防御：无执行状态或候选越界时跳过，普通请求不产生任何记录喵。
	if !foundState || executionState == nil || executionState.currentCandidateIndex < 0 || executionState.currentCandidateIndex >= len(executionState.executionSnapshot.Candidates) {
		return
	}
	candidate := executionState.executionSnapshot.Candidates[executionState.currentCandidateIndex]
	recordVirtualModelCandidateProbe(c, executionState, candidate.CandidateID, success, errorClass, time.Since(executionState.startTime).Milliseconds())
}

// recordVirtualModelCandidateProbe 记录单个候选节点的被动统计样本与最近一次状态喵。
func recordVirtualModelCandidateProbe(c *gin.Context, executionState *virtualModelExecutionState, candidateID int, success bool, errorClass string, latencyMs int64) {
	// 喵~防御：执行状态或模型名缺失时跳过，避免空指针喵。
	if c == nil || executionState == nil || executionState.virtualModelName == "" {
		return
	}
	// 候选节点的聚合键为 virtual/<name>/candidate/<id>，全部归入自用固定分组喵。
	probeModelName := fmt.Sprintf("%s/candidate/%d", executionState.virtualModelName, candidateID)
	// 成功样本携带最近一次成功调用的 token/TTFT，失败样本留空喵。
	extras := perfmetrics.EntityProbeExtras{}
	if success {
		extras = executionState.successExtras
	}
	perfmetrics.RecordEntityProbe(probeModelName, latencyMs, success, extras)
	now := time.Now().Unix()
	// 候选节点状态行的 EntityID 为候选 id、VirtualID 为所属虚拟模型 id，供按模型聚合查询喵。
	_ = model.RecordEntityProbeCounted(model.EntityProbeScopeVirtualCandidate, int64(candidateID), int64(executionState.virtualModelID), executionState.ownerUserID, now, success, latencyMs, errorClass)
}

// recordVirtualModelProbeSuccess 记录一次虚拟模型成功调用的候选与整体探测样本，并携带 usage/TTFT 喵。
// 上游 usage 可空（未提供计费信息），TTFT 为响应头到达毫秒数，零表示未测到喵。
func recordVirtualModelProbeSuccess(c *gin.Context, executionState *virtualModelExecutionState, candidateID int, usage *dto.Usage, ttftMs int64) {
	// 喵~防御：缺少执行状态或模型名时跳过，避免空指针喵。
	if c == nil || executionState == nil || executionState.virtualModelName == "" {
		return
	}
	extras := perfmetrics.EntityProbeExtras{TtftMs: ttftMs, HasTtft: ttftMs > 0}
	if usage != nil {
		extras.InputTokens = int64(usage.PromptTokens)
		extras.OutputTokens = int64(usage.CompletionTokens)
		extras.CachedTokens = int64(usage.PromptTokensDetails.CachedTokens)
	}
	executionState.successExtras = extras
	// 候选延迟取请求级基准（请求入口到本次探测），与整体延迟口径一致喵。
	latencyMs := time.Since(executionState.startTime).Milliseconds()
	recordVirtualModelCandidateProbe(c, executionState, candidateID, true, "", latencyMs)
	RecordVirtualModelOverallProbe(c, true, "")
}

// ApplyVirtualModelSuccessProbe 从 context 读取内部候选成功结算的 usage，并携带 TTFT 填充整体探测样本喵。
// 供 controller/relay.go 在内部候选原生成功结算后调用，普通请求无副作用喵。
func ApplyVirtualModelSuccessProbe(c *gin.Context, ttftMs int64) {
	executionState, foundState := getVirtualModelExecutionState(c)
	// 喵~防御：无执行状态时跳过，普通请求不产生任何写入喵。
	if !foundState || executionState == nil {
		return
	}
	extras := executionState.successExtras
	if usageValue, foundUsage := common.GetContextKey(c, constant.ContextKeyVirtualModelSuccessUsage); foundUsage {
		// 喵~防御：context 里类型不符时跳过，避免类型断言 panic 喵。
		if usage, valid := usageValue.(*dto.Usage); valid && usage != nil {
			extras.InputTokens = int64(usage.PromptTokens)
			extras.OutputTokens = int64(usage.CompletionTokens)
			extras.CachedTokens = int64(usage.PromptTokensDetails.CachedTokens)
		}
	}
	if ttftMs > 0 {
		extras.TtftMs = ttftMs
		extras.HasTtft = true
	}
	executionState.successExtras = extras
}

// virtualModelFirstByteMs 计算虚拟模型请求级首字耗时（new-api 接收请求 → 首次向客户端写响应），单位：毫秒喵。
// 执行函数未打点（非虚拟上下文或响应未写）时返回零，由调用方回退其他口径喵。
func virtualModelFirstByteMs(c *gin.Context) int64 {
	// 喵~防御：空上下文直接返回零喵。
	if c == nil {
		return 0
	}
	firstWriteAt := common.GetContextKeyTime(c, constant.ContextKeyVirtualModelFirstWriteAt)
	// 未打点或尚未写响应时不提供首字喵。
	if firstWriteAt.IsZero() {
		return 0
	}
	executionState, foundState := getVirtualModelExecutionState(c)
	// 喵~防御：缺少执行状态时无法获得请求入口时刻，返回零喵。
	if !foundState || executionState == nil {
		return 0
	}
	firstByteMs := firstWriteAt.Sub(executionState.startTime).Milliseconds()
	// 喵~防御：时钟异常导致的负值按零处理喵。
	if firstByteMs < 0 {
		return 0
	}
	return firstByteMs
}

// RecordVirtualModelOverallProbe 记录虚拟模型整体的被动统计样本喵。
// 任一候选 2xx 成功即整体成功；候选链耗尽或透传失败即整体失败；整体延迟取整次请求耗时喵。
func RecordVirtualModelOverallProbe(c *gin.Context, success bool, errorClass string) {
	executionState, foundState := getVirtualModelExecutionState(c)
	// 喵~防御：无执行状态或已记录过整体结果时跳过，避免重复样本喵。
	if !foundState || executionState == nil || executionState.overallProbeRecorded {
		return
	}
	// 喵~防御：空模型名不产生无意义记录喵。
	if executionState.virtualModelName == "" {
		return
	}
	executionState.overallProbeRecorded = true
	latencyMs := time.Since(executionState.startTime).Milliseconds()
	// 成功样本携带最近一次成功调用的 token/TTFT，失败样本留空喵。
	extras := perfmetrics.EntityProbeExtras{}
	if success {
		extras = executionState.successExtras
	}
	perfmetrics.RecordEntityProbe(executionState.virtualModelName, latencyMs, success, extras)
	now := time.Now().Unix()
	_ = model.RecordEntityProbeCounted(model.EntityProbeScopeVirtual, int64(executionState.virtualModelID), 0, executionState.ownerUserID, now, success, latencyMs, errorClass)
}

// recordCustomCandidateFailureProbe 记录自定义候选失败及其引用上游模型的失败样本喵。
// hasResponse 表示候选已写出最终响应（请求终结），此时一并记录虚拟模型整体失败喵。
func recordCustomCandidateFailureProbe(c *gin.Context, candidate *model.VirtualModelInternalCandidateSnapshot, hasUpstreamReference bool, referencedUpstreamModel *model.UserUpstreamModel, errorClass string, startTime time.Time, hasResponse bool) {
	executionState, _ := getVirtualModelExecutionState(c)
	// 候选失败延迟取请求级基准（请求入口到本次失败探测），与整体延迟口径一致喵。
	// 喵~防御：缺少执行状态时回退候选级 startTime，保证任何情况下都能记录喵。
	latencyMs := time.Since(startTime).Milliseconds()
	if executionState != nil {
		latencyMs = time.Since(executionState.startTime).Milliseconds()
	}
	recordVirtualModelCandidateProbe(c, executionState, candidate.CandidateID, false, errorClass, latencyMs)
	if hasUpstreamReference && referencedUpstreamModel != nil {
		recordUpstreamModelProbeState(referencedUpstreamModel, false, true, false, errorClass, startTime, upstreamProbeExtras{})
	}
	if hasResponse {
		RecordVirtualModelOverallProbe(c, false, errorClass)
	}
}

// executeCustomVirtualModelCandidate 在当前 middleware 生命周期内安全完成单次自定义候选透传喵。
// executionSnapshot 提供候选级与模型级全局兜底失败规则，候选未配置规则时自动回退全局规则喵。
func executeCustomVirtualModelCandidate(c *gin.Context, candidate *model.VirtualModelInternalCandidateSnapshot, executionSnapshot *model.VirtualModelExecutionSnapshot) bool {
	// 喵~防御：自定义候选必须存在，缺失时绝不尝试外发请求喵。
	if c == nil || candidate == nil {
		abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "virtual model execution is not available", types.ErrorCode("virtual_model_unavailable"))
		return false
	}
	// 记录请求起始时间供结算耗时使用喵。
	startTime := time.Now()
	// 从候选级与模型级全局失败规则解析流式探测参数，供候选透传的放流前探测使用喵。
	probeParameters := virtualmodelservice.ResolveProbeParameters(executionSnapshot.FailureRulesByCandidateID[candidate.CandidateID], executionSnapshot.GlobalFailureRules)
	// 从失败规则解析超时条件判定阈值，非零时覆盖候选级执行超时，让「超过 N 秒判定超时」真正生效喵。
	ruleTimeoutSeconds := virtualmodelservice.ResolveFailureTimeoutSeconds(executionSnapshot.FailureRulesByCandidateID[candidate.CandidateID], executionSnapshot.GlobalFailureRules, candidate.TimeoutSeconds)
	// 解析候选执行来源：引用用户上游模型条目或直填凭据喵。
	hasUpstreamReference := candidate.UpstreamModelID != nil && *candidate.UpstreamModelID > 0
	var referencedUpstreamModel *model.UserUpstreamModel
	var baseURL string
	var apiKey string
	candidateRealModelName := candidate.RealModelName
	candidateAuthStyle := candidate.AuthStyle
	// 喵~防御：引用用户上游模型时以条目为准实时加载，缺失或越权按候选不可用处理喵。
	if hasUpstreamReference {
		loadedModel, loadError := model.GetUserUpstreamModelByOwnerID(*candidate.UpstreamModelID, c.GetInt("id"))
		if loadError != nil {
			abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "virtual model execution is not available", types.ErrorCode("virtual_model_unavailable"))
			return false
		}
		referencedUpstreamModel = loadedModel
		// 请求前硬检查：引用上游模型同样受余额与可用额度约束喵。
		if referencedUpstreamModel.BalanceCents <= 0 {
			abortUpstreamModelQuotaExhausted(c)
			return false
		}
		if referencedUpstreamModel.AvailableCents <= 0 {
			abortUpstreamModelQuotaExhausted(c)
			return false
		}
		decryptedBaseURL, decryptBaseURLError := virtualmodelservice.DecryptCredential(referencedUpstreamModel.EncryptedBaseURL, referencedUpstreamModel.CredentialVersion)
		decryptedAPIKey, decryptAPIKeyError := virtualmodelservice.DecryptCredential(referencedUpstreamModel.EncryptedAPIKey, referencedUpstreamModel.CredentialVersion)
		// 喵~防御：条目凭据不可用时返回受控不可用错误喵。
		if decryptBaseURLError != nil || decryptAPIKeyError != nil {
			abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "virtual model execution is not available", types.ErrorCode("virtual_model_unavailable"))
			return false
		}
		baseURL = decryptedBaseURL
		apiKey = decryptedAPIKey
		candidateRealModelName = referencedUpstreamModel.RealModelName
		candidateAuthStyle = model.VirtualModelAuthStyle(referencedUpstreamModel.AuthStyle)
	} else {
		// 喵~防御：直填候选必须带有完整加密凭据和模型配置，缺失时绝不尝试外发请求喵。
		if strings.TrimSpace(candidate.EncryptedBaseURL) == "" || strings.TrimSpace(candidate.EncryptedAPIKey) == "" || strings.TrimSpace(candidate.RealModelName) == "" {
			abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "virtual model execution is not available", types.ErrorCode("virtual_model_unavailable"))
			return false
		}
		decryptedBaseURL, decryptBaseURLError := virtualmodelservice.DecryptCredential(candidate.EncryptedBaseURL, candidate.CredentialVersion)
		decryptedAPIKey, decryptAPIKeyError := virtualmodelservice.DecryptCredential(candidate.EncryptedAPIKey, candidate.CredentialVersion)
		// 喵~防御：凭据密文篡改、主密钥缺失或解密失败均只返回受控不可用错误，不泄露秘密或密文状态喵。
		if decryptBaseURLError != nil || decryptAPIKeyError != nil {
			abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "virtual model execution is not available", types.ErrorCode("virtual_model_unavailable"))
			return false
		}
		baseURL = decryptedBaseURL
		apiKey = decryptedAPIKey
	}
	// 候选级与模型级全局兜底规则均随请求快照传入，禁止在候选执行期间二次读取已可能被控制面替换的规则喵。
	maximumRetries := candidate.MaxRetries
	// 喵~防御：自定义候选重试次数与控制面上限一致，并由总请求 deadline 进一步约束喵。
	if maximumRetries < 0 {
		maximumRetries = 0
	}
	if maximumRetries > 20 {
		maximumRetries = 20
	}
	// 请求前预扣：引用上游模型时按请求体估算费用并原子预扣三账户，避免单次调用超出用户设置限额喵。
	preConsumedCents := int64(0)
	var preConsumeError error
	settled := false
	if hasUpstreamReference && referencedUpstreamModel != nil {
		preConsumedCents, preConsumeError = preConsumeUserUpstreamModelCharge(c, referencedUpstreamModel, false)
		// 喵~防御：预扣失败（余额/可用任一不足）直接 403 拒绝，与自用上游模型预扣语义一致喵。
		if preConsumeError != nil {
			abortUpstreamModelQuotaExhausted(c)
			return false
		}
		// 请求未完成差额结算前持有的预扣，由成功结算或失败退出退款二选一释放喵。
		defer func() {
			// 喵~防御：候选失败退出（切换/终结/透传）时退还预扣金额，避免虚拟模型候选链反复预扣锁定额度喵。
			if preConsumedCents > 0 && !settled {
				if refundError := model.AdjustUserUpstreamModelCharge(referencedUpstreamModel.ID, referencedUpstreamModel.OwnerUserID, -preConsumedCents, false); refundError != nil {
					common.SysError("virtual model custom pre-consume refund failed: " + refundError.Error())
				}
			}
		}()
	}
	for retryIndex := 0; retryIndex <= maximumRetries; retryIndex++ {
		// 喵~防御：候选重试前检查全局 deadline 与取消状态，防止单个 custom 候选耗尽请求预算后继续外发喵。
		if executionState, foundState := getVirtualModelExecutionState(c); foundState && ((!executionState.requestDeadline.IsZero() && !time.Now().Before(executionState.requestDeadline)) || c.Request == nil || c.Request.Context().Err() != nil) {
			return false
		}
		var executionError error
		var executionUsage *dto.Usage
		// 引用上游模型分支的透传结果提升到外层作用域，供成功路径提取 TTFT/usage 喵。
		var executionResult *virtualmodelservice.UserUpstreamModelExecutionResult
		if hasUpstreamReference {
			// 流转伪流开关随执行状态读取，引用上游候选与直填候选一致生效喵。
			fakeStreamEnabled := false
			if executionState, foundState := getVirtualModelExecutionState(c); foundState {
				fakeStreamEnabled = executionState.fakeStreamEnabled
			}
			// 引用用户上游模型：走带 usage 解析的独立透传，返回解析结果供结算喵。
			executionResult = virtualmodelservice.ExecuteUserUpstreamModel(c, virtualmodelservice.CustomCandidateExecutionInput{
				CandidateID:    candidate.CandidateID,
				BaseURL:        baseURL,
				APIKey:         apiKey,
				RealModelName:  candidateRealModelName,
				AuthStyle:      candidateAuthStyle,
				TimeoutSeconds: ruleTimeoutSeconds,
				// 流式探测参数随候选执行传入，供放流前健康探测使用喵。
				StallTimeoutSeconds:      probeParameters.StallTimeoutSeconds,
				MinContentChars:          probeParameters.MinContentChars,
				ProbeTotalTimeoutSeconds: probeParameters.ProbeTotalTimeoutSeconds,
				// 引用上游模型的请求定制：自定义请求头与字段替换随条目配置传入喵。
				CustomHeaders:     referencedUpstreamModel.CustomHeaders,
				FieldReplacements: referencedUpstreamModel.FieldReplacements,
				FakeStreamEnabled: fakeStreamEnabled,
			})
			executionError = executionResult.Err
			executionUsage = executionResult.Usage
		} else {
			// 流转伪流开关随执行状态读取，引用上游候选与直填候选一致生效喵。
			fakeStreamEnabled := false
			if executionState, foundState := getVirtualModelExecutionState(c); foundState {
				fakeStreamEnabled = executionState.fakeStreamEnabled
			}
			// 纯直填 custom 候选：透传并解析 usage/TTFT 返回，供结算与状态探测使用喵。
			customExecutionResult := virtualmodelservice.ExecuteCustomCandidate(c, virtualmodelservice.CustomCandidateExecutionInput{
				CandidateID:    candidate.CandidateID,
				BaseURL:        baseURL,
				APIKey:         apiKey,
				RealModelName:  candidateRealModelName,
				AuthStyle:      candidateAuthStyle,
				TimeoutSeconds: ruleTimeoutSeconds,
				// 流式探测参数随候选执行传入，供放流前健康探测使用喵。
				StallTimeoutSeconds:      probeParameters.StallTimeoutSeconds,
				MinContentChars:          probeParameters.MinContentChars,
				ProbeTotalTimeoutSeconds: probeParameters.ProbeTotalTimeoutSeconds,
				FakeStreamEnabled:        fakeStreamEnabled,
			})
			executionError = customExecutionResult.Err
			executionUsage = customExecutionResult.Usage
			executionResult = &virtualmodelservice.UserUpstreamModelExecutionResult{Usage: customExecutionResult.Usage, TtftMs: customExecutionResult.TtftMs}
		}
		// 成功响应已由透传器直接写出，此时仅清除请求开始前已观察到的历史冻结并中止后续 controller relay 喵。
		if executionError == nil {
			// 请求级耗时基准：总耗时取请求入口到当前，首字取首次写响应时刻减请求入口喵。
			// 喵~防御：缺少执行状态或未打点时回退候选级计时，保证任何情况下都能写日志喵。
			requestExecutionState, foundRequestState := getVirtualModelExecutionState(c)
			requestElapsedMs := time.Since(startTime).Milliseconds()
			if foundRequestState && requestExecutionState != nil && !requestExecutionState.startTime.IsZero() {
				requestElapsedMs = time.Since(requestExecutionState.startTime).Milliseconds()
			}
			requestFirstByteMs := virtualModelFirstByteMs(c)
			// 喵~防御：未打点（非虚拟上下文或尚未写响应）时回退总耗时近似首字喵。
			if requestFirstByteMs <= 0 {
				requestFirstByteMs = requestElapsedMs
			}
			// 先记录该 custom 候选成功尝试摘要，确保后续日志写入时候选序列已包含本次成功（否则首个候选直接成功时详情会为空）喵。
			appendVirtualModelCandidateAttempt(c, model.VirtualModelCandidateAttemptRecord{
				Seq:         currentVirtualModelCandidateSeq(c),
				CandidateID: candidate.CandidateID,
				Source:      "custom",
				Label:       buildVirtualModelAttemptLabel(candidate, candidateRealModelName),
				Success:     true,
				StatusCode:  http.StatusOK,
				// 请求级首字与总耗时，与虚拟模型日志口径一致喵。
				TtftMs:     requestFirstByteMs,
				ElapsedMs:  requestElapsedMs,
				RetryCount: retryIndex,
			})
			// 引用上游模型成功时结算独立 RMB 计费并写虚拟模型日志（上下文已把类型覆盖为 9）喵。
			if hasUpstreamReference && referencedUpstreamModel != nil {
				requestGroup := ""
				if executionState, foundState := getVirtualModelExecutionState(c); foundState && executionState.modelRequest != nil {
					requestGroup = executionState.modelRequest.Group
				}
				// 结算总耗时取请求入口到当前，首字取请求级首次写响应，与虚拟模型日志口径一致喵。
				settleUserUpstreamModelCharge(c, referencedUpstreamModel.OwnerUserID, referencedUpstreamModel, executionUsage, requestGroup, isUpstreamModelRequestStreaming(c), int(requestElapsedMs/1000), false, requestFirstByteMs, preConsumedCents)
				// 已按差额结算完毕，defer 不再退还预扣喵。
				settled = true
				// 实体状态检测：引用上游模型成功，同时记录上游模型自用维度成功喵。
				recordUpstreamModelProbeState(referencedUpstreamModel, false, true, true, "", startTime, buildUpstreamProbeExtras(executionResult))
			} else {
				// 纯直填 custom 候选成功：写虚拟模型日志（携带解析出的 usage 与请求级首字，token 计真实值）喵。
				recordVirtualModelCustomSuccess(c, int(requestElapsedMs/1000), executionUsage, requestFirstByteMs)
			}
			// 实体状态检测：自定义候选成功，记录候选成功与虚拟模型整体成功（携带 usage/请求级首字）喵。
			executionState, _ := getVirtualModelExecutionState(c)
			recordVirtualModelProbeSuccess(c, executionState, candidate.CandidateID, executionUsage, requestFirstByteMs)
			identityDigest := virtualmodelservice.CustomCandidateIdentityDigest(*candidate)
			// 喵~防御：只清除请求开始时观察到的历史冻结，避免并发失败请求写入的新冻结被成功响应误删喵。
			expectedUpdatedTime := int64(0)
			if executionState, foundState := getVirtualModelExecutionState(c); foundState {
				if freezeState, foundFreezeState := executionState.automaticFreezeStatesByIdentity[identityDigest]; foundFreezeState {
					expectedUpdatedTime = freezeState.UpdatedTime
				}
			}
			if expectedUpdatedTime > 0 {
				// 喵~防御：冻结清理失败不影响已提交成功响应，仅保留后续请求可能继续跳过该候选的保守状态喵。
				_ = model.ClearVirtualModelCustomFreezeState(c.GetInt("id"), identityDigest, expectedUpdatedTime, common.GetTimestamp())
			}
			c.Abort()
			return false
		}
		customFailure := &virtualmodelservice.CustomCandidateExecutionFailure{}
		// 喵~防御：非结构化异常不能参与规则匹配或重试；若响应已提交则只中止，避免重复错误响应喵。
		if !errors.As(executionError, &customFailure) {
			if c.Writer != nil && c.Writer.Written() {
				// 实体状态检测：响应已部分提交但仍失败，记录候选失败与整体失败喵。
				recordCustomCandidateFailureProbe(c, candidate, hasUpstreamReference, referencedUpstreamModel, "upstream_unavailable", startTime, true)
				c.Abort()
				return false
			}
			// 实体状态检测：非结构化异常记录候选失败与虚拟模型整体失败喵。
			recordCustomCandidateFailureProbe(c, candidate, hasUpstreamReference, referencedUpstreamModel, "upstream_unavailable", startTime, true)
			abortWithOpenAiMessage(c, http.StatusBadGateway, "virtual model custom upstream is unavailable", types.ErrorCode("virtual_model_unavailable"))
			return false
		}
		// 候选失败后按规则决策动作；断流与普通失败统一走失败规则（候选规则优先，无则全局兜底），目标模式断流措施已由全局兜底规则代替喵。
		action, freezeSeconds, ruleRetryCount := virtualmodelservice.DecideVirtualModelFailureAction(executionSnapshot, candidate.CandidateID, customFailure.Failure)
		// 记录该 custom 候选的失败尝试摘要，供最终日志展示候选链故障转移过程喵。
		appendVirtualModelCandidateAttempt(c, model.VirtualModelCandidateAttemptRecord{
			Seq:          currentVirtualModelCandidateSeq(c),
			CandidateID:  candidate.CandidateID,
			Source:       "custom",
			Label:        buildVirtualModelAttemptLabel(candidate, candidateRealModelName),
			Success:      false,
			StatusCode:   customFailure.Failure.HTTPStatus,
			ErrorClass:   customFailure.Failure.ErrorClass,
			ErrorMessage: customFailure.Failure.ErrorClass,
			// 模型级错误返回体取上游响应体受限摘要（最多 64 KiB），供详情点击复制喵。
			ErrorBody:  customFailure.Failure.BodyPreview,
			ElapsedMs:  time.Since(startTime).Milliseconds(),
			RetryCount: retryIndex,
		})
		// 断流 retry 用规则级最大重试次数优先，未配置时回退候选 MaxRetries 喵。
		maxCustomRetries := maximumRetries
		if ruleRetryCount > 0 {
			maxCustomRetries = ruleRetryCount
		}
		if action == model.VirtualModelActionRetry && retryIndex < maxCustomRetries {
			retryDelay := time.Duration(virtualmodelservice.RetryBackoffSeconds(retryIndex)) * time.Second
			// 喵~防御：退避等待必须响应客户端取消和总 deadline，避免断开请求仍占用 goroutine 和连接喵。
			if executionState, foundState := getVirtualModelExecutionState(c); foundState && !executionState.requestDeadline.IsZero() {
				remainingDuration := time.Until(executionState.requestDeadline)
				if remainingDuration <= 0 {
					// 实体状态检测：总 deadline 耗尽导致候选无法继续，记录候选失败喵。
					recordCustomCandidateFailureProbe(c, candidate, hasUpstreamReference, referencedUpstreamModel, customFailure.Failure.ErrorClass, startTime, false)
					return false
				}
				if retryDelay > remainingDuration {
					retryDelay = remainingDuration
				}
			}
			select {
			case <-time.After(retryDelay):
				continue
			case <-c.Request.Context().Done():
				// 实体状态检测：客户端取消导致候选无法继续，记录候选失败喵。
				recordCustomCandidateFailureProbe(c, candidate, hasUpstreamReference, referencedUpstreamModel, customFailure.Failure.ErrorClass, startTime, false)
				return false
			}
		}
		if action == model.VirtualModelActionFreeze && freezeSeconds > 0 {
			ownerUserID := c.GetInt("id")
			identityDigest := virtualmodelservice.CustomCandidateIdentityDigest(*candidate)
			freezeError := model.UpsertVirtualModelCustomFreezeState(ownerUserID, identityDigest, common.GetTimestamp()+int64(freezeSeconds), customFailure.Failure.ErrorClass, common.GetTimestamp())
			// 喵~防御：冻结状态写入失败不会降级为继续重试，避免上游故障扩大为循环请求喵。
			if freezeError != nil {
				// 实体状态检测：冻结写入失败终结候选链，记录候选失败与整体失败喵。
				recordCustomCandidateFailureProbe(c, candidate, hasUpstreamReference, referencedUpstreamModel, customFailure.Failure.ErrorClass, startTime, true)
				abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "virtual model execution is not available", types.ErrorCode("virtual_model_unavailable"))
				return false
			}
		}
		if action == model.VirtualModelActionPassthrough {
			// 实体状态检测：透传失败终结请求，记录候选失败与整体失败喵。
			recordCustomCandidateFailureProbe(c, candidate, hasUpstreamReference, referencedUpstreamModel, customFailure.Failure.ErrorClass, startTime, true)
			// 喵~防御：仅回传已受限缓冲的上游错误正文和过滤后的响应头，禁止写入 hop-by-hop 字段喵。
			virtualmodelservice.CopyCustomPassthroughResponse(c.Writer, customFailure.ResponseHeaders, customFailure.Failure.HTTPStatus, customFailure.ResponseBody)
			c.Abort()
			return false
		}
		// 实体状态检测：next、freeze 与不可继续 retry 后候选终态失败，记录候选失败（整体结果由候选链终局决定）喵。
		recordCustomCandidateFailureProbe(c, candidate, hasUpstreamReference, referencedUpstreamModel, customFailure.Failure.ErrorClass, startTime, false)
		// next、freeze 和不可继续 retry 交由候选链尝试后续候选喵。
		return false
	}
	return false
}

// applyInternalVirtualModelCandidate 将内部候选的真实模型和分组写入原生分发语义喵。
func applyInternalVirtualModelCandidate(c *gin.Context, modelRequest *ModelRequest, virtualModelName string, candidate *model.VirtualModelInternalCandidateSnapshot) bool {
	// 喵~防御：候选字段必须完整、分组不能使用 auto，避免虚拟模型绕过分组边界或递归路由喵。
	if modelRequest == nil || candidate == nil || strings.TrimSpace(candidate.GroupName) == "" || candidate.GroupName == "auto" || strings.TrimSpace(candidate.RealModelName) == "" {
		abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "virtual model execution is not available", types.ErrorCode("virtual_model_unavailable"))
		return false
	}
	// 喵~防御：指定 Channel 与候选分组语义互斥，防止固定 Channel 绕过候选的安全选择边界喵。
	if _, specificChannelRequested := common.GetContextKey(c, constant.ContextKeyTokenSpecificChannelId); specificChannelRequested {
		abortWithOpenAiMessage(c, http.StatusBadRequest, "virtual models do not support a specific channel", types.ErrorCode("virtual_model_invalid_request"))
		return false
	}
	// 喵~防御：候选模型仍需通过当前 Token 模型白名单，显式 API Key binding 不得绕过该既有限制喵。
	if common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled) {
		allowedModels, foundLimit := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
		modelLimit, validLimit := allowedModels.(map[string]bool)
		matchingModelName := ratio_setting.FormatMatchingModelName(candidate.RealModelName)
		if !foundLimit || !validLimit || (!modelLimit[candidate.RealModelName] && !modelLimit[matchingModelName]) {
			abortWithOpenAiMessage(c, http.StatusForbidden, "token does not have access to the virtual model candidate", types.ErrorCode("virtual_model_not_found"))
			return false
		}
	}
	// 喵~防御：候选分组必须属于当前 API Key 的可访问范围，避免私有模型升级调用方权限喵。
	if !service.GetRequestUserGroupAccess(c).Allows(candidate.GroupName) {
		abortWithOpenAiMessage(c, http.StatusForbidden, "token does not have access to the virtual model candidate group", types.ErrorCode("virtual_model_not_found"))
		return false
	}
	// 保存公开虚拟名称供后续审计使用，同时将计费和原生 relay 基准改为真实模型喵。
	common.SetContextKey(c, constant.ContextKeyVirtualModelName, virtualModelName)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, candidate.RealModelName)
	common.SetContextKey(c, constant.ContextKeyTokenGroup, candidate.GroupName)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, candidate.GroupName)
	common.SetContextKey(c, constant.ContextKeyInternalCandidateApplied, true)
	// 从候选级与模型级全局失败规则解析流式探测参数并写入 context，供 relay 层放流前探测读取喵。
	if executionState, foundState := getVirtualModelExecutionState(c); foundState && executionState.executionSnapshot != nil {
		probeParameters := virtualmodelservice.ResolveProbeParameters(executionState.executionSnapshot.FailureRulesByCandidateID[candidate.CandidateID], executionState.executionSnapshot.GlobalFailureRules)
		common.SetContextKey(c, constant.ContextKeyVirtualModelProbeParameters, probeParameters)
		// 流转伪流开关随候选激活写入 context，供 relay 层决定是否全量缓存到 [DONE] 再一次性回放喵。
		if executionState.fakeStreamEnabled {
			common.SetContextKey(c, constant.ContextKeyVirtualModelFakeStream, true)
		}
	}
	if !replaceTopLevelRequestModel(c, candidate.RealModelName) {
		abortWithOpenAiMessage(c, http.StatusBadRequest, "invalid virtual model request", types.ErrorCode("virtual_model_invalid_request"))
		return false
	}
	modelRequest.Model = candidate.RealModelName
	modelRequest.Group = candidate.GroupName
	return true
}

// replaceTopLevelRequestModel 将 JSON 请求体的顶层 model 替换为内部候选真实模型喵。
func replaceTopLevelRequestModel(c *gin.Context, realModelName string) bool {
	// 喵~防御：仅接受 JSON 请求和非空真实模型，避免破坏 multipart、表单或空请求体喵。
	if c == nil || c.Request == nil || !strings.HasPrefix(strings.ToLower(c.Request.Header.Get("Content-Type")), "application/json") || strings.TrimSpace(realModelName) == "" {
		return false
	}
	bodyStorage, bodyError := common.GetBodyStorage(c)
	if bodyError != nil {
		return false
	}
	requestBody, bodyError := bodyStorage.Bytes()
	if bodyError != nil || !gjson.ValidBytes(requestBody) {
		return false
	}
	rewrittenBody, rewriteError := sjson.SetBytes(requestBody, "model", realModelName)
	if rewriteError != nil {
		return false
	}
	return common.ReplaceRequestBody(c, rewrittenBody) == nil
}

// channelSupportsRequestPath reports whether a channel can serve the request path.
// Only Advanced Custom (type 58) channels are path-checked; all other channel types
// always pass. A type-58 channel is usable only when one of its routes matches.
func channelSupportsRequestPath(channel *model.Channel, requestPath string, requestModel string) bool {
	if channel == nil {
		return false
	}
	if channel.Type != constant.ChannelTypeAdvancedCustom {
		return true
	}
	config := channel.GetOtherSettings().AdvancedCustom
	return config != nil && config.SupportsPathForModel(requestPath, requestModel)
}

func canonicalRelayPath(path string) string {
	if strings.HasPrefix(path, "/pg/") {
		return "/v1/" + strings.TrimPrefix(path, "/pg/")
	}
	return path
}

// getModelFromRequest 从请求中读取模型信息
// 根据 Content-Type 自动处理：
// - application/json
// - application/x-www-form-urlencoded
// - multipart/form-data
func getModelFromRequest(c *gin.Context) (*ModelRequest, error) {
	if strings.HasPrefix(c.Request.Header.Get("Content-Type"), "application/json") {
		modelRequest, err := getModelFromJSONBody(c)
		if err != nil {
			return nil, errors.New(i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": err.Error()}))
		}
		return modelRequest, nil
	}

	var modelRequest ModelRequest
	err := common.UnmarshalBodyReusable(c, &modelRequest)
	if err != nil {
		return nil, errors.New(i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": err.Error()}))
	}
	return &modelRequest, nil
}

func getModelFromJSONBody(c *gin.Context) (*ModelRequest, error) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, err
	}
	requestBody, err := storage.Bytes()
	if err != nil {
		return nil, err
	}
	if !gjson.ValidBytes(requestBody) {
		return nil, errors.New("invalid JSON request body")
	}

	values := gjson.GetManyBytes(requestBody, "model", "group")
	model, err := getJSONStringValue(values[0], "model")
	if err != nil {
		return nil, err
	}
	group, err := getJSONStringValue(values[1], "group")
	if err != nil {
		return nil, err
	}

	if _, seekErr := storage.Seek(0, io.SeekStart); seekErr != nil {
		return nil, seekErr
	}
	c.Request.Body = io.NopCloser(storage)

	return &ModelRequest{
		Model: model,
		Group: group,
	}, nil
}

func getJSONStringValue(result gjson.Result, field string) (string, error) {
	if !result.Exists() || result.Type == gjson.Null {
		return "", nil
	}
	if result.Type != gjson.String {
		return "", fmt.Errorf("field %s must be a string", field)
	}
	return result.String(), nil
}

func getModelRequest(c *gin.Context) (*ModelRequest, bool, error) {
	var modelRequest ModelRequest
	shouldSelectChannel := true
	var err error
	relayPath := canonicalRelayPath(c.Request.URL.Path)
	if relayPath == "/v1/audio/speech/websocket" {
		modelRequest.Model = strings.TrimSpace(c.Query("model"))
		c.Set("relay_mode", relayconstant.RelayModeAudioSpeechWebSocket)
	} else if relayPath == "/v1/audio/speech/tasks" || strings.HasPrefix(relayPath, "/v1/audio/speech/tasks/") {
		if c.Request.Method == http.MethodPost {
			req, requestErr := getModelFromRequest(c)
			if requestErr != nil {
				return nil, false, requestErr
			}
			modelRequest = *req
			c.Set("relay_mode", relayconstant.RelayModeAudioSpeechTaskSubmit)
		} else {
			shouldSelectChannel = false
			modelRequest.Model = getTaskOriginModelName(c)
			c.Set("relay_mode", relayconstant.RelayModeAudioSpeechTaskFetchByID)
		}
	} else if strings.Contains(c.Request.URL.Path, "/mj/") {
		relayMode := relayconstant.Path2RelayModeMidjourney(c.Request.URL.Path)
		if relayMode == relayconstant.RelayModeMidjourneyTaskFetch ||
			relayMode == relayconstant.RelayModeMidjourneyTaskFetchByCondition ||
			relayMode == relayconstant.RelayModeMidjourneyNotify ||
			relayMode == relayconstant.RelayModeMidjourneyTaskImageSeed {
			shouldSelectChannel = false
		} else {
			midjourneyRequest := taskdto.MidjourneyRequest{}
			err = common.UnmarshalBodyReusable(c, &midjourneyRequest)
			if err != nil {
				return nil, false, errors.New(i18n.T(c, i18n.MsgDistributorInvalidMidjourney, map[string]any{"Error": err.Error()}))
			}
			midjourneyModel, mjErr, success := service.GetMjRequestModel(relayMode, &midjourneyRequest)
			if mjErr != nil {
				return nil, false, fmt.Errorf("%s", mjErr.Description)
			}
			if midjourneyModel == "" {
				if !success {
					return nil, false, fmt.Errorf("%s", i18n.T(c, i18n.MsgDistributorInvalidParseModel))
				} else {
					// task fetch, task fetch by condition, notify
					shouldSelectChannel = false
				}
			}
			modelRequest.Model = midjourneyModel
		}
		c.Set("relay_mode", relayMode)
	} else if strings.Contains(c.Request.URL.Path, "/suno/") {
		relayMode := relayconstant.Path2RelaySuno(c.Request.Method, c.Request.URL.Path)
		if relayMode == relayconstant.RelayModeSunoFetch ||
			relayMode == relayconstant.RelayModeSunoFetchByID {
			shouldSelectChannel = false
		} else {
			modelName := service.CoverTaskActionToModelName(constant.TaskPlatformSuno, c.Param("action"))
			modelRequest.Model = modelName
		}
		c.Set("platform", string(constant.TaskPlatformSuno))
		c.Set("relay_mode", relayMode)
	} else if strings.Contains(c.Request.URL.Path, "/v1/videos/") && strings.HasSuffix(c.Request.URL.Path, "/remix") {
		relayMode := relayconstant.RelayModeVideoSubmit
		c.Set("relay_mode", relayMode)
		shouldSelectChannel = false
	} else if relayPath == "/v1/3d" || strings.HasPrefix(relayPath, "/v1/3d/") {
		relayMode := relayconstant.RelayModeThreeDSubmit
		if c.Request.Method == http.MethodPost {
			req, err := getModelFromRequest(c)
			if err != nil {
				return nil, false, err
			}
			modelRequest = *req
		} else {
			relayMode = relayconstant.RelayModeThreeDFetchByID
			shouldSelectChannel = false
			modelRequest.Model = getTaskOriginModelName(c)
		}
		c.Set("relay_mode", relayMode)
	} else if strings.Contains(c.Request.URL.Path, "/v1/videos") {
		//curl https://api.openai.com/v1/videos \
		//  -H "Authorization: Bearer $OPENAI_API_KEY" \
		//  -F "model=sora-2" \
		//  -F "prompt=A calico cat playing a piano on stage"
		//	-F input_reference="@image.jpg"
		relayMode := relayconstant.RelayModeUnknown
		if c.Request.Method == http.MethodPost {
			relayMode = relayconstant.RelayModeVideoSubmit
			req, err := getModelFromRequest(c)
			if err != nil {
				return nil, false, err
			}
			if req != nil {
				modelRequest.Model = req.Model
			}
		} else if c.Request.Method == http.MethodGet {
			relayMode = relayconstant.RelayModeVideoFetchByID
			shouldSelectChannel = false
			modelRequest.Model = getTaskOriginModelName(c)
		}
		c.Set("relay_mode", relayMode)
	} else if strings.Contains(c.Request.URL.Path, "/v1/video/generations") {
		relayMode := relayconstant.RelayModeUnknown
		if c.Request.Method == http.MethodPost {
			req, err := getModelFromRequest(c)
			if err != nil {
				return nil, false, err
			}
			modelRequest.Model = req.Model
			relayMode = relayconstant.RelayModeVideoSubmit
		} else if c.Request.Method == http.MethodGet {
			relayMode = relayconstant.RelayModeVideoFetchByID
			shouldSelectChannel = false
			modelRequest.Model = getTaskOriginModelName(c)
		}
		if _, ok := c.Get("relay_mode"); !ok {
			c.Set("relay_mode", relayMode)
		}
	} else if strings.HasPrefix(c.Request.URL.Path, "/v1beta/models/") || strings.HasPrefix(c.Request.URL.Path, "/v1/models/") {
		// Gemini API 路径处理: /v1beta/models/gemini-2.0-flash:generateContent
		relayMode := relayconstant.RelayModeGemini
		modelName := extractModelNameFromGeminiPath(c.Request.URL.Path)
		if modelName != "" {
			modelRequest.Model = modelName
		}
		c.Set("relay_mode", relayMode)
	} else if !strings.HasPrefix(relayPath, "/v1/audio/transcriptions") && !strings.Contains(c.Request.Header.Get("Content-Type"), "multipart/form-data") {
		req, err := getModelFromRequest(c)
		if err != nil {
			return nil, false, err
		}
		modelRequest = *req
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/realtime") {
		//wss://api.openai.com/v1/realtime?model=gpt-4o-realtime-preview-2024-10-01
		modelRequest.Model = c.Query("model")
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/moderations") {
		if modelRequest.Model == "" {
			modelRequest.Model = "text-moderation-stable"
		}
	}
	if strings.HasSuffix(c.Request.URL.Path, "embeddings") {
		if modelRequest.Model == "" {
			modelRequest.Model = c.Param("model")
		}
	}
	if strings.HasPrefix(relayPath, "/v1/images/generations") {
		modelRequest.Model = common.GetStringIfEmpty(modelRequest.Model, "dall-e")
	} else if strings.HasPrefix(relayPath, "/v1/images/edits") {
		//modelRequest.Model = common.GetStringIfEmpty(c.PostForm("model"), "gpt-image-1")
		contentType := c.ContentType()
		if slices.Contains([]string{gin.MIMEPOSTForm, gin.MIMEMultipartPOSTForm}, contentType) {
			req, err := getModelFromRequest(c)
			if err == nil && req.Model != "" {
				modelRequest = *req
			}
		}
	}
	if strings.HasPrefix(relayPath, "/v1/audio") &&
		relayPath != "/v1/audio/speech/websocket" &&
		relayPath != "/v1/audio/speech/tasks" &&
		!strings.HasPrefix(relayPath, "/v1/audio/speech/tasks/") {
		relayMode := relayconstant.RelayModeAudioSpeech
		if strings.HasPrefix(relayPath, "/v1/audio/speech") {

			modelRequest.Model = common.GetStringIfEmpty(modelRequest.Model, "tts-1")
		} else if strings.HasPrefix(relayPath, "/v1/audio/translations") {
			// 先尝试从请求读取
			if req, err := getModelFromRequest(c); err == nil && req.Model != "" {
				modelRequest.Model = req.Model
			}
			modelRequest.Model = common.GetStringIfEmpty(modelRequest.Model, "whisper-1")
			relayMode = relayconstant.RelayModeAudioTranslation
		} else if strings.HasPrefix(relayPath, "/v1/audio/transcriptions") {
			// 先尝试从请求读取
			if req, err := getModelFromRequest(c); err == nil && req.Model != "" {
				modelRequest.Model = req.Model
			}
			modelRequest.Model = common.GetStringIfEmpty(modelRequest.Model, "whisper-1")
			relayMode = relayconstant.RelayModeAudioTranscription
		}
		c.Set("relay_mode", relayMode)
	}
	if strings.HasPrefix(c.Request.URL.Path, "/pg/") {
		common.SetContextKey(c, constant.ContextKeyTokenGroup, modelRequest.Group)
	}

	return &modelRequest, shouldSelectChannel, nil
}

// 修复 #4834: GET /v1/video/generations/:task_id && /v1/video/:task_id 此前不解析 model，
// 当 token 启用「可用模型限制」时，下游 modelLimitEnable 校验会因
// modelRequest.Model 为空而误报 "This token has no access to model"。
// 从已存储的任务记录中回填 OriginModelName 即可让校验走在正确的模型上。
func getTaskOriginModelName(c *gin.Context) string {
	if !common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled) {
		return ""
	}

	taskId := c.Param("task_id")
	if taskId == "" {
		// jimeng adapter
		taskId = c.GetString("task_id")
	}
	if taskId == "" {
		return ""
	}

	userId := c.GetInt("id")
	if task, exist, err := model.GetByTaskId(userId, taskId); err == nil && exist && task != nil {
		return task.Properties.OriginModelName
	}
	return ""
}

func SetupContextForSelectedChannel(c *gin.Context, channel *model.Channel, modelName string) *types.NewAPIError {
	if _, exists := c.Get(string(constant.ContextKeyOriginalModel)); !exists {
		c.Set(string(constant.ContextKeyOriginalModel), modelName) // for retry and billing
	}
	common.SetContextKey(c, constant.ContextKeySelectedModel, modelName)
	if channel == nil {
		return types.NewError(errors.New("channel is nil"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	common.SetContextKey(c, constant.ContextKeyChannelId, channel.Id)
	common.SetContextKey(c, constant.ContextKeyChannelName, channel.Name)
	common.SetContextKey(c, constant.ContextKeyChannelType, channel.Type)
	common.SetContextKey(c, constant.ContextKeyChannelCreateTime, channel.CreatedTime)
	common.SetContextKey(c, constant.ContextKeyChannelSetting, channel.GetSetting())
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, channel.GetOtherSettings())
	paramOverride := channel.GetParamOverride()
	headerOverride := channel.GetHeaderOverride()
	if mergedParam, applied := service.ApplyChannelAffinityOverrideTemplate(c, paramOverride); applied {
		paramOverride = mergedParam
	}
	common.SetContextKey(c, constant.ContextKeyChannelParamOverride, paramOverride)
	common.SetContextKey(c, constant.ContextKeyChannelHeaderOverride, headerOverride)
	if nil != channel.OpenAIOrganization && *channel.OpenAIOrganization != "" {
		common.SetContextKey(c, constant.ContextKeyChannelOrganization, *channel.OpenAIOrganization)
	}
	common.SetContextKey(c, constant.ContextKeyChannelAutoBan, channel.GetAutoBan())
	common.SetContextKey(c, constant.ContextKeyChannelModelMapping, channel.GetModelMapping())
	common.SetContextKey(c, constant.ContextKeyChannelStatusCodeMapping, channel.GetStatusCodeMapping())

	key, index, newAPIError := channel.GetNextEnabledKey()
	if newAPIError != nil {
		return newAPIError
	}
	if channel.ChannelInfo.IsMultiKey {
		common.SetContextKey(c, constant.ContextKeyChannelIsMultiKey, true)
		common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, index)
	} else {
		// 必须设置为 false，否则在重试到单个 key 的时候会导致日志显示错误
		common.SetContextKey(c, constant.ContextKeyChannelIsMultiKey, false)
	}
	// c.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", key))
	common.SetContextKey(c, constant.ContextKeyChannelKey, key)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, channel.GetBaseURL())

	common.SetContextKey(c, constant.ContextKeySystemPromptOverride, false)

	// TODO: api_version统一
	switch channel.Type {
	case constant.ChannelTypeAzure:
		c.Set("api_version", channel.Other)
	case constant.ChannelTypeVertexAi:
		c.Set("region", channel.Other)
	case constant.ChannelTypeXunfei:
		c.Set("api_version", channel.Other)
	case constant.ChannelTypeGemini:
		c.Set("api_version", channel.Other)
	case constant.ChannelTypeAli:
		c.Set("plugin", channel.Other)
	case constant.ChannelCloudflare:
		c.Set("api_version", channel.Other)
	case constant.ChannelTypeMokaAI:
		c.Set("api_version", channel.Other)
	case constant.ChannelTypeCoze:
		c.Set("bot_id", channel.Other)
	}
	return nil
}

// extractModelNameFromGeminiPath 从 Gemini API URL 路径中提取模型名
// 输入格式: /v1beta/models/gemini-2.0-flash:generateContent
// 输出: gemini-2.0-flash
func extractModelNameFromGeminiPath(path string) string {
	// 查找 "/models/" 的位置
	modelsPrefix := "/models/"
	modelsIndex := strings.Index(path, modelsPrefix)
	if modelsIndex == -1 {
		return ""
	}

	// 从 "/models/" 之后开始提取
	startIndex := modelsIndex + len(modelsPrefix)
	if startIndex >= len(path) {
		return ""
	}

	// 查找 ":" 的位置，模型名在 ":" 之前
	colonIndex := strings.Index(path[startIndex:], ":")
	if colonIndex == -1 {
		// 如果没有找到 ":"，返回从 "/models/" 到路径结尾的部分
		return path[startIndex:]
	}

	// 返回模型名部分
	return path[startIndex : startIndex+colonIndex]
}
