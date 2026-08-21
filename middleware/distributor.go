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
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
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
			if !handleVirtualModelRequest(c, modelRequest) {
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
	ownerUserID                     int
	executionSnapshot               *model.VirtualModelExecutionSnapshot
	manualFrozenCandidateIDs        map[int]bool
	automaticFreezeStatesByIdentity map[string]model.VirtualModelCustomFreezeState
	originalRequestBody             []byte
	modelRequest                    *ModelRequest
	currentCandidateIndex           int
	skippedCandidateIDs             map[int]bool
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
	virtualModel, queryError := model.GetEnabledVirtualModelByOwnerTokenName(ownerUserID, tokenID, normalizedName)
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
	// 喵~防御：内存候选快照必须与原始请求体完全隔离，避免调用方意外复用或修改底层缓存喵。
	originalRequestBodySnapshot := append([]byte(nil), originalRequestBody...)
	executionState := &virtualModelExecutionState{
		virtualModelName:                virtualModel.VirtualModelName(),
		ownerUserID:                     ownerUserID,
		executionSnapshot:               executionSnapshot,
		manualFrozenCandidateIDs:        manualFrozenCandidateIDs,
		automaticFreezeStatesByIdentity: automaticFreezeStatesByIdentity,
		originalRequestBody:             originalRequestBodySnapshot,
		modelRequest:                    modelRequest,
		currentCandidateIndex:           -1,
		skippedCandidateIDs:             make(map[int]bool),
	}
	common.SetContextKey(c, constant.ContextKeyVirtualModelExecutionState, executionState)
	if activateNextVirtualModelCandidate(c, executionState) {
		return true
	}
	// 喵~防御：自定义候选已提交响应或错误时不得继续原生 Channel 分发，避免重复写响应喵。
	if c.Writer != nil && c.Writer.Written() {
		return false
	}
	abortWithOpenAiMessage(c, http.StatusBadGateway, "virtual model execution is not available", types.ErrorCode("virtual_model_unavailable"))
	return false
}

// AdvanceVirtualModelAfterNativeFailure 在一个内部候选完成全部原生 Channel 重试后决定是否切换喵。
// 返回值依次表示是否已激活下一个内部候选、是否已有自定义候选提交了最终响应喵。
func AdvanceVirtualModelAfterNativeFailure(c *gin.Context, nativeError *types.NewAPIError) (bool, bool) {
	// 喵~防御：响应已提交、错误为空或不存在虚拟执行状态时绝不能重新选择候选喵。
	if c == nil || nativeError == nil || c.Request == nil || (c.Writer != nil && c.Writer.Written()) {
		return false, false
	}
	executionState, foundState := getVirtualModelExecutionState(c)
	if !foundState || executionState.currentCandidateIndex < 0 || executionState.currentCandidateIndex >= len(executionState.executionSnapshot.Candidates) {
		return false, false
	}
	currentCandidate := executionState.executionSnapshot.Candidates[executionState.currentCandidateIndex]
	// 喵~防御：只允许 JSON body 路径进行内部候选重写，避免 WebSocket、路径模型或表单请求在失败后被错误重放喵。
	if !strings.HasPrefix(strings.ToLower(c.Request.Header.Get("Content-Type")), "application/json") {
		return false, false
	}
	// 喵~防御：仅当前内部候选才可从原生 relay 失败回切候选链，防止错误状态使 custom 候选被重复执行喵。
	if currentCandidate.SourceType != model.VirtualModelSourceInternal {
		return false, false
	}
	// 内部候选没有可安全共享的自定义冻结身份；retry 在原生 Channel 重试耗尽后等价于切换下一候选喵。
	nativeFailure := virtualmodelservice.NormalizeCandidateFailure(nativeError.StatusCode, nil, nil, nil)
	action, _ := virtualmodelservice.DecideCandidateFailureAction(executionState.executionSnapshot.FailureRulesByCandidateID[currentCandidate.CandidateID], nativeFailure)
	if action == model.VirtualModelActionPassthrough {
		return false, false
	}
	// internal 的 retry 与 freeze 都不会重放已耗尽的原生 Channel；两者安全地继续后备候选喵。
	if activateNextVirtualModelCandidate(c, executionState) {
		return true, false
	}
	if c.Writer != nil && c.Writer.Written() {
		return false, true
	}
	return false, false
}

// GetActiveVirtualModelCandidateID 返回当前请求已激活内部候选的编号，用于隔离计费尝试标识喵。
func GetActiveVirtualModelCandidateID(c *gin.Context) int {
	// 喵~防御：缺少执行状态或候选索引异常时返回零，调用方必须保留原请求标识喵。
	executionState, foundState := getVirtualModelExecutionState(c)
	if !foundState || executionState.currentCandidateIndex < 0 || executionState.currentCandidateIndex >= len(executionState.executionSnapshot.Candidates) {
		return 0
	}
	return executionState.executionSnapshot.Candidates[executionState.currentCandidateIndex].CandidateID
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
	for candidateIndex := executionState.currentCandidateIndex + 1; candidateIndex < len(executionState.executionSnapshot.Candidates); candidateIndex++ {
		candidateSnapshot := &executionState.executionSnapshot.Candidates[candidateIndex]
		if executionState.skippedCandidateIDs[candidateSnapshot.CandidateID] || executionState.manualFrozenCandidateIDs[candidateSnapshot.CandidateID] {
			continue
		}
		if candidateSnapshot.SourceType == model.VirtualModelSourceCustom {
			identityDigest := virtualmodelservice.CustomCandidateIdentityDigest(*candidateSnapshot)
			if _, automaticallyFrozen := executionState.automaticFreezeStatesByIdentity[identityDigest]; automaticallyFrozen {
				continue
			}
		}
		executionState.currentCandidateIndex = candidateIndex
		if candidateSnapshot.SourceType == model.VirtualModelSourceInternal {
			if restoreVirtualModelOriginalRequest(c, executionState.originalRequestBody) && applyInternalVirtualModelCandidate(c, executionState.modelRequest, executionState.virtualModelName, candidateSnapshot) {
				return true
			}
			// 喵~防御：内部候选无法安全接入原生 relay 时立即终止本次选择，避免把鉴权或配置错误误伪装为候选故障喵。
			return false
		}
		if candidateSnapshot.SourceType == model.VirtualModelSourceCustom {
			executeCustomVirtualModelCandidate(c, candidateSnapshot, executionState.executionSnapshot.FailureRulesByCandidateID[candidateSnapshot.CandidateID])
			if c.Writer != nil && c.Writer.Written() {
				return false
			}
			executionState.skippedCandidateIDs[candidateSnapshot.CandidateID] = true
		}
	}
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

// executeCustomVirtualModelCandidate 在当前 middleware 生命周期内安全完成单次自定义候选透传喵。
func executeCustomVirtualModelCandidate(c *gin.Context, candidate *model.VirtualModelInternalCandidateSnapshot, failureRules []model.VirtualModelFailureRule) bool {
	// 喵~防御：自定义候选必须带有完整加密凭据和模型配置，缺失时绝不尝试外发请求喵。
	if c == nil || candidate == nil || strings.TrimSpace(candidate.EncryptedBaseURL) == "" || strings.TrimSpace(candidate.EncryptedAPIKey) == "" || strings.TrimSpace(candidate.RealModelName) == "" {
		abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "virtual model execution is not available", types.ErrorCode("virtual_model_unavailable"))
		return false
	}
	baseURL, decryptBaseURLError := virtualmodelservice.DecryptCredential(candidate.EncryptedBaseURL, candidate.CredentialVersion)
	apiKey, decryptAPIKeyError := virtualmodelservice.DecryptCredential(candidate.EncryptedAPIKey, candidate.CredentialVersion)
	// 喵~防御：凭据密文篡改、主密钥缺失或解密失败均只返回受控不可用错误，不泄露秘密或密文状态喵。
	if decryptBaseURLError != nil || decryptAPIKeyError != nil {
		abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "virtual model execution is not available", types.ErrorCode("virtual_model_unavailable"))
		return false
	}
	// 失败规则随请求快照传入，禁止在候选执行期间二次读取已可能被控制面替换的规则喵。
	maximumRetries := candidate.MaxRetries
	// 喵~防御：自定义候选重试次数最多三次，避免错误配置耗尽总请求预算喵。
	if maximumRetries < 0 {
		maximumRetries = 0
	}
	if maximumRetries > 3 {
		maximumRetries = 3
	}
	for retryIndex := 0; retryIndex <= maximumRetries; retryIndex++ {
		executionError := virtualmodelservice.ExecuteCustomCandidate(c, virtualmodelservice.CustomCandidateExecutionInput{
			CandidateID:    candidate.CandidateID,
			BaseURL:        baseURL,
			APIKey:         apiKey,
			RealModelName:  candidate.RealModelName,
			AuthStyle:      candidate.AuthStyle,
			TimeoutSeconds: candidate.TimeoutSeconds,
		})
		// 成功响应已由透传器直接写出，此时清除历史冻结状态并中止后续 controller relay 喵。
		if executionError == nil {
			identityDigest := virtualmodelservice.CustomCandidateIdentityDigest(*candidate)
			// 喵~防御：清除冻结状态失败不影响已提交成功响应，仅保留后续请求可能继续跳过该候选的保守状态喵。
			_ = model.ClearVirtualModelCustomFreezeState(c.GetInt("id"), identityDigest, common.GetTimestamp())
			c.Abort()
			return false
		}
		customFailure := &virtualmodelservice.CustomCandidateExecutionFailure{}
		// 喵~防御：非结构化异常不能参与规则匹配或重试；若响应已提交则只中止，避免重复错误响应喵。
		if !errors.As(executionError, &customFailure) {
			if c.Writer != nil && c.Writer.Written() {
				c.Abort()
				return false
			}
			abortWithOpenAiMessage(c, http.StatusBadGateway, "virtual model custom upstream is unavailable", types.ErrorCode("virtual_model_unavailable"))
			return false
		}
		action, freezeSeconds := virtualmodelservice.DecideCandidateFailureAction(failureRules, customFailure.Failure)
		if action == model.VirtualModelActionRetry && retryIndex < maximumRetries {
			retryDelay := time.Duration(virtualmodelservice.RetryBackoffSeconds(retryIndex)) * time.Second
			// 喵~防御：退避等待必须响应客户端取消，避免断开请求仍占用 goroutine 和连接喵。
			select {
			case <-time.After(retryDelay):
				continue
			case <-c.Request.Context().Done():
				return false
			}
		}
		if action == model.VirtualModelActionFreeze && freezeSeconds > 0 {
			ownerUserID := c.GetInt("id")
			identityDigest := virtualmodelservice.CustomCandidateIdentityDigest(*candidate)
			freezeError := model.UpsertVirtualModelCustomFreezeState(ownerUserID, identityDigest, common.GetTimestamp()+int64(freezeSeconds), customFailure.Failure.ErrorClass, common.GetTimestamp())
			// 喵~防御：冻结状态写入失败不会降级为继续重试，避免上游故障扩大为循环请求喵。
			if freezeError != nil {
				abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "virtual model execution is not available", types.ErrorCode("virtual_model_unavailable"))
				return false
			}
		}
		if action == model.VirtualModelActionPassthrough {
			// 喵~防御：仅回传已受限缓冲的上游错误正文和过滤后的响应头，禁止写入 hop-by-hop 字段喵。
			virtualmodelservice.CopyCustomPassthroughResponse(c.Writer, customFailure.ResponseHeaders, customFailure.Failure.HTTPStatus, customFailure.ResponseBody)
			c.Abort()
			return false
		}
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
