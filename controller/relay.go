package controller

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func relayHandler(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	var err *types.NewAPIError
	switch info.RelayMode {
	case relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeImagesEdits:
		err = relay.ImageHelper(c, info)
	case relayconstant.RelayModeAudioSpeech:
		fallthrough
	case relayconstant.RelayModeAudioTranslation:
		fallthrough
	case relayconstant.RelayModeAudioTranscription:
		err = relay.AudioHelper(c, info)
	case relayconstant.RelayModeRerank:
		err = relay.RerankHelper(c, info)
	case relayconstant.RelayModeEmbeddings:
		err = relay.EmbeddingHelper(c, info)
	case relayconstant.RelayModeResponses, relayconstant.RelayModeResponsesCompact:
		err = relay.ResponsesHelper(c, info)
	case relayconstant.RelayModeAlphaSearch:
		err = relay.AlphaSearchHelper(c, info)
	default:
		err = relay.TextHelper(c, info)
	}
	return err
}

// relayUpstreamModel 执行自定义上游的 relay 中转链，自动做请求/响应格式转换喵。
// 渠道已由 middleware 注入 context（临时渠道），relay 的 getChannel 直接使用；
// 成功时由 middleware 完成独立 RMB 结算，失败时退还预扣并由调用方按客户端格式输出错误喵。
func relayUpstreamModel(c *gin.Context, relayInfo *relaycommon.RelayInfo, relayFormat types.RelayFormat) *types.NewAPIError {
	var newAPIError *types.NewAPIError
	// 外层候选循环恒在首轮 break 退化为单次执行，这里直接展开，避免冗余 label 与循环喵。
	// 重试只发生在内层 retryParam 循环中，无外层候选级出口依赖喵。
	retryParam := &service.RetryParam{
		Ctx:         c,
		TokenGroup:  relayInfo.TokenGroup,
		ModelName:   relayInfo.OriginModelName,
		RequestPath: c.Request.URL.Path,
		Retry:       common.GetPointer(0),
	}
	relayInfo.RetryIndex = 0
	relayInfo.LastError = nil

	for ; ; retryParam.IncreaseRetry() {
		relayInfo.RetryIndex = retryParam.GetRetry()
		channel, channelErr := getChannel(c, relayInfo, retryParam)
		if channelErr != nil {
			logger.LogError(c, channelErr.Error())
			newAPIError = channelErr
			break
		}
		addUsedChannel(c, channel.Id)
		bodyStorage, bodyErr := common.GetBodyStorage(c)
		if bodyErr != nil {
			// 请求体不可复用按 400 拒绝，与普通 relay 路径一致喵。
			newAPIError = types.NewErrorWithStatusCode(bodyErr, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			break
		}
		c.Request.Body = io.NopCloser(bodyStorage)

		switch relayFormat {
		case types.RelayFormatClaude:
			newAPIError = relay.ClaudeHelper(c, relayInfo)
		case types.RelayFormatGemini:
			newAPIError = geminiRelayHandler(c, relayInfo)
		default:
			newAPIError = relayHandler(c, relayInfo)
		}

		if newAPIError == nil {
			relayInfo.LastError = nil
			// 自定义上游成功：TTFT 取响应头到达减请求入口，交 middleware 完成独立 RMB 结算与日志喵。
			internalTtftMs := int64(0)
			if !relayInfo.FirstResponseTime.IsZero() {
				internalTtftMs = relayInfo.FirstResponseTime.Sub(relayInfo.StartTime).Milliseconds()
			}
			middleware.SettleUpstreamModelRelaySuccess(c, internalTtftMs)
			return nil
		}

		newAPIError = service.NormalizeViolationFeeError(newAPIError)
		relayInfo.LastError = newAPIError
		processChannelError(c, *types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, common.GetContextKeyString(c, constant.ContextKeyChannelKey), channel.GetAutoBan()), newAPIError)

		retryTimes := getRetryTimesForCurrentGroup(c, relayInfo.TokenGroup)
		if !shouldRetry(c, newAPIError, retryTimes-retryParam.GetRetry()) {
			break
		}
	}

	if newAPIError != nil {
		// 自定义上游 relay 失败：退款与失败日志/探测由 middleware 收尾，虚拟候选编排在虚拟上下文生效喵。
		middleware.HandleUpstreamModelRelayFailure(c, newAPIError)
	}
	return newAPIError
}

func geminiRelayHandler(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	var err *types.NewAPIError
	if strings.Contains(c.Request.URL.Path, "embed") {
		err = relay.GeminiEmbeddingHandler(c, info)
	} else {
		err = relay.GeminiHelper(c, info)
	}
	return err
}

func Relay(c *gin.Context, relayFormat types.RelayFormat) {

	requestId := c.GetString(common.RequestIdKey)
	//group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	//originalModel := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)

	var (
		newAPIError *types.NewAPIError
		ws          *websocket.Conn
	)

	if relayFormat == types.RelayFormatOpenAIRealtime || relayFormat == types.RelayFormatUnrealSpeechWebSocket {
		var err error
		ws, err = upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			helper.WssError(c, ws, types.NewError(err, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry()).ToOpenAIError())
			return
		}
		defer ws.Close()
	}

	defer func() {
		if newAPIError != nil {
			// 实体状态检测：虚拟模型请求以错误收尾时确保整体失败样本已记录；
			// 内部函数按执行状态与已记录标记去重，普通请求与已成功记录均自动跳过喵。
			middleware.RecordVirtualModelOverallProbe(c, false, "virtual_model_unavailable")
			// 虚拟模型 relay 级失败统一补记 type=9 整体失败日志（此前内部候选链耗尽/透传仅 probe 无日志落库）喵。
			// 内部按防重标记去重，与 abortWithOpenAiMessage 钩子、recordUserUpstreamModelFailureLog 均不重复喵。
			middleware.RecordVirtualModelOverallFailure(c, string(newAPIError.GetErrorCode()), newAPIError.StatusCode)
			logger.LogError(c, fmt.Sprintf("relay error: %s", common.LocalLogPreview(newAPIError.Error())))
			// 虚拟模型候选可能已经把响应字节交给客户端，此时必须抑制最终错误正文喵。
			_, isVirtualCandidateRequest := middleware.GetActiveVirtualModelCandidateAttempt(c)
			if shouldSuppressFinalErrorBody(c, isVirtualCandidateRequest) {
				return
			}
			// Apply global message rewrites only after channel retries and
			// accounting have completed. This keeps retry/auto-ban decisions
			// and the diagnostic log based on the original upstream error while
			// changing only the final client-facing payload.
			modelName := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)
			operation_setting.ApplyErrorRewrite(newAPIError, modelName)
			newAPIError.SetMessage(common.MessageWithRequestId(newAPIError.Error(), requestId))
			switch relayFormat {
			case types.RelayFormatOpenAIRealtime, types.RelayFormatUnrealSpeechWebSocket:
				helper.WssError(c, ws, newAPIError.ToOpenAIError())
			case types.RelayFormatClaude:
				c.JSON(newAPIError.StatusCode, gin.H{
					"type":  "error",
					"error": newAPIError.ToClaudeError(),
				})
			default:
				c.JSON(newAPIError.StatusCode, gin.H{
					"error": newAPIError.ToOpenAIError(),
				})
			}
		}
	}()

	if err := service.RewriteMeshyImageProxyRequest(c); err != nil {
		newAPIError = types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
		return
	}

	request, err := helper.GetAndValidateRequest(c, relayFormat)
	if err != nil {
		// Map "request body too large" to 413 so clients can handle it correctly
		if common.IsRequestBodyTooLargeError(err) || errors.Is(err, common.ErrRequestBodyTooLarge) {
			newAPIError = types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusRequestEntityTooLarge, types.ErrOptionWithSkipRetry())
		} else {
			newAPIError = types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithStatusCode(http.StatusBadRequest), types.ErrOptionWithSkipRetry())
		}
		return
	}
	service.MarkMeshyImageProxyBase64Response(c, request)

	relayInfo, err := relaycommon.GenRelayInfo(c, relayFormat, request, ws)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeGenRelayInfoFailed)
		return
	}
	if !strings.HasPrefix(strings.ToLower(c.GetHeader("Content-Type")), "application/json") {
		requestInput, inputErr := helper.BuildBillingExprRequestInputFromRequest(request, relayInfo.RequestHeaders)
		if inputErr != nil {
			newAPIError = types.NewError(inputErr, types.ErrorCodeInvalidRequest)
			return
		}
		relayInfo.BillingRequestInput = &requestInput
	}

	// 自定义上游模式：已由 middleware 注入临时渠道并独立预扣，跳过配额计费与 token 预估，直接执行 relay 中转链喵。
	if middleware.IsUpstreamModelRelayRequest(c) {
		newAPIError = relayUpstreamModel(c, relayInfo, relayFormat)
		return
	}

	needSensitiveCheck := setting.ShouldCheckPromptSensitive()
	needCountToken := constant.CountToken
	// Avoid building huge CombineText (strings.Join) when token counting and sensitive check are both disabled.
	var meta *types.TokenCountMeta
	if needSensitiveCheck || needCountToken {
		meta = request.GetTokenCountMeta()
	} else {
		meta = fastTokenCountMetaForPricing(request)
	}

	if needSensitiveCheck && meta != nil {
		contains, words := service.CheckSensitiveText(meta.CombineText)
		if contains {
			logger.LogWarn(c, fmt.Sprintf("user sensitive words detected: %s", strings.Join(words, ", ")))
			newAPIError = types.NewError(err, types.ErrorCodeSensitiveWordsDetected)
			return
		}
	}

	tokens, err := service.EstimateRequestToken(c, meta, relayInfo)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeCountTokenFailed)
		return
	}

	relayInfo.SetEstimatePromptTokens(tokens)

	// candidateRelayBaseline 保存虚拟模型候选切换所需的请求级基线，仅虚拟请求才构造喵。
	// 普通模型请求和 Token AutoRoutes 请求不会进入该分支，单一 RelayInfo 生命周期保持不变喵。
	var candidateRelayBaseline *relaycommon.CandidateRelayBaseline
	if _, isVirtualCandidateRequest := middleware.GetActiveVirtualModelCandidateAttempt(c); isVirtualCandidateRequest {
		baseline, baselineError := relaycommon.NewCandidateRelayBaseline(relayInfo)
		// 喵~防御：无法抽取请求级基线时立即终止，避免后续候选切换用残缺基线建立新计费喵。
		if baselineError != nil {
			newAPIError = types.NewError(baselineError, types.ErrorCodeGenRelayInfoFailed, types.ErrOptionWithSkipRetry())
			return
		}
		candidateRelayBaseline = baseline
	}

	priceData, err := helper.ModelPriceHelper(c, relayInfo, tokens, meta)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusBadRequest))
		return
	}

	// common.SetContextKey(c, constant.ContextKeyTokenCountMeta, meta)

	if priceData.FreeModel {
		logger.LogInfo(c, fmt.Sprintf("模型 %s 免费，跳过预扣费", relayInfo.OriginModelName))
	} else {
		newAPIError = service.PreConsumeBilling(c, priceData.QuotaToPreConsume, relayInfo)
		if newAPIError != nil {
			return
		}
	}

	defer func() {
		// Only return quota if downstream failed and quota was actually pre-consumed
		if newAPIError != nil {
			newAPIError = service.NormalizeViolationFeeError(newAPIError)
			if relayInfo.Billing != nil {
				relayInfo.Billing.Refund(c)
			}
			service.ChargeViolationFeeIfNeeded(c, relayInfo, newAPIError)
		}
	}()

candidateRelayLoop:
	for {
		retryParam := &service.RetryParam{
			Ctx:         c,
			TokenGroup:  relayInfo.TokenGroup,
			ModelName:   relayInfo.OriginModelName,
			RequestPath: c.Request.URL.Path,
			Retry:       common.GetPointer(0),
		}
		relayInfo.RetryIndex = 0
		relayInfo.LastError = nil

		for ; ; retryParam.IncreaseRetry() {
			relayInfo.RetryIndex = retryParam.GetRetry()
			channel, channelErr := getChannel(c, relayInfo, retryParam)
			if channelErr != nil {
				logger.LogError(c, channelErr.Error())
				newAPIError = channelErr
				break
			}
			addUsedChannel(c, channel.Id)
			if billingErr := service.PrepareTieredBillingForSelectedGroup(c, relayInfo); billingErr != nil {
				newAPIError = billingErr
				break
			}

			bodyStorage, bodyErr := common.GetBodyStorage(c)
			if bodyErr != nil {
				// Ensure consistent 413 for oversized bodies even when error occurs later (e.g., retry path)
				if common.IsRequestBodyTooLargeError(bodyErr) || errors.Is(bodyErr, common.ErrRequestBodyTooLarge) {
					newAPIError = types.NewErrorWithStatusCode(bodyErr, types.ErrorCodeReadRequestBodyFailed, http.StatusRequestEntityTooLarge, types.ErrOptionWithSkipRetry())
				} else {
					newAPIError = types.NewErrorWithStatusCode(bodyErr, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
				}
				break
			}
			c.Request.Body = io.NopCloser(bodyStorage)

			switch relayFormat {
			case types.RelayFormatOpenAIRealtime, types.RelayFormatUnrealSpeechWebSocket:
				newAPIError = relay.WssHelper(c, relayInfo)
			case types.RelayFormatClaude:
				newAPIError = relay.ClaudeHelper(c, relayInfo)
			case types.RelayFormatGemini:
				newAPIError = geminiRelayHandler(c, relayInfo)
			default:
				newAPIError = relayHandler(c, relayInfo)
			}

			if newAPIError == nil {
				relayInfo.LastError = nil
				// 内部候选成功后清除其请求启动时观察到的自动冻结状态，失败只记录日志不影响成功响应喵。
				middleware.ClearCurrentVirtualModelCandidateAutomaticFreeze(c)
				// 内部候选成功：结算 usage 已在 service 写入 context，这里连同 TTFT 一并填充探测样本喵。
				internalTtftMs := int64(0)
				if !relayInfo.FirstResponseTime.IsZero() {
					internalTtftMs = relayInfo.FirstResponseTime.Sub(relayInfo.StartTime).Milliseconds()
				}
				middleware.ApplyVirtualModelSuccessProbe(c, internalTtftMs)
				// 实体状态检测：内部候选原生成功，记录候选成功与虚拟模型整体成功喵。
				middleware.RecordActiveVirtualModelCandidateProbe(c, true, "")
				middleware.RecordVirtualModelOverallProbe(c, true, "")
				return
			}

			newAPIError = service.NormalizeViolationFeeError(newAPIError)
			relayInfo.LastError = newAPIError

			// 多 key 故障转移规则（上游引入）：命中禁用规则时自动禁用当前 key，并允许同渠道多 key 自动重试喵。
			multiKeyRuleMatched := channel.MatchesMultiKeyDisableRule(
				newAPIError.StatusCode,
				newAPIError.Error(),
			)
			if multiKeyRuleMatched {
				usingKey := common.GetContextKeyString(c, constant.ContextKeyChannelKey)
				model.UpdateChannelStatus(channel.Id, usingKey, common.ChannelStatusAutoDisabled, newAPIError.ErrorWithStatusCode())
				if channel.ChannelInfo.MultiKeyStatusList == nil {
					channel.ChannelInfo.MultiKeyStatusList = make(map[int]int)
				}
				for index, key := range channel.GetKeys() {
					if key == usingKey {
						channel.ChannelInfo.MultiKeyStatusList[index] = common.ChannelStatusAutoDisabled
						break
					}
				}
			}

			processChannelError(c, *types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, common.GetContextKeyString(c, constant.ContextKeyChannelKey), channel.GetAutoBan()), newAPIError)

			retryTimes := getRetryTimesForCurrentGroup(c, relayInfo.TokenGroup)
			shouldRetryRequest := shouldRetry(c, newAPIError, retryTimes-retryParam.GetRetry())
			if multiKeyRuleMatched && channel.ChannelInfo.MultiKeyAutoRetry && !types.IsSkipRetryError(newAPIError) && channel.HasEnabledMultiKey() {
				shouldRetryRequest = true
				retryParam.PinnedChannelID = channel.Id
			}
			if !shouldRetryRequest {
				break
			}
		}

		if newAPIError != nil {
			// 喵~防御：只有内部候选完成全部原生 Channel 重试且尚未提交任何响应时，才允许虚拟层编排后备候选喵。
			nativeFailureDecision := middleware.AdvanceVirtualModelAfterNativeFailure(c, newAPIError)
			if nativeFailureDecision.RetryCurrentCandidate {
				// 失败规则要求重试当前内部候选：清除本次错误并回到候选循环顶部重新走原生分发，不切换候选喵。
				relayInfo.LastError = nil
				newAPIError = nil
				continue candidateRelayLoop
			}
			if nativeFailureDecision.NextCandidateActivated {
				// 旧候选进入终态并完成同步退款后，才为后备候选创建独立 relay、定价与计费会话喵。
				nextCandidateRelayInfo, candidateSwitchError := switchToNextVirtualModelCandidate(c, relayInfo, candidateRelayBaseline, relayFormat, meta)
				if candidateSwitchError != nil {
					newAPIError = candidateSwitchError
					break candidateRelayLoop
				}
				// 把当前候选替换为后备候选的独立 RelayInfo，外层 defer 之后只会收尾这一个候选喵。
				relayInfo = nextCandidateRelayInfo
				newAPIError = nil
				continue candidateRelayLoop
			}
			if nativeFailureDecision.CustomCandidateCommitted {
				// 自定义候选已把响应字节交给客户端，此后绝不允许再向同一个响应追加第二个错误正文喵。
				if relayInfo.Billing != nil {
					if refundError := relayInfo.Billing.RefundImmediately(c); refundError != nil {
						// 喵~防御：同步退款只完成了部分步骤时只能记录日志，改用异步退款补齐剩余步骤，
						// 绝不能把退款错误变成客户端可见的第二个响应正文，否则会破坏已提交的流式协议喵。
						logger.LogError(c, fmt.Sprintf("自定义候选已提交响应，失败内部候选同步退款未完成，转为异步补退：%s", refundError.Error()))
						relayInfo.Billing.Refund(c)
					}
				}
				newAPIError = nil
				return
			}
		}

		// 候选链重试渠道诊断日志：记录本候选链实际经过的渠道序列喵。
		useChannel := c.GetStringSlice("use_channel")
		if len(useChannel) > 1 {
			retryLogStr := fmt.Sprintf("重试：%s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
			logger.LogInfo(c, retryLogStr)
		}
		if newAPIError != nil {
			// 先把当前候选的 relay 信息固定到局部变量，避免异步采样读取后续可能被替换的候选状态喵。
			sampledRelayInfo := relayInfo
			gopool.Go(func() {
				perfmetrics.RecordRelaySample(sampledRelayInfo, false, 0, nil)
			})
		}
		break candidateRelayLoop
	}
}

var upgrader = websocket.Upgrader{
	Subprotocols: []string{"realtime"}, // WS 握手支持的协议，如果有使用 Sec-WebSocket-Protocol，则必须在此声明对应的 Protocol TODO add other protocol
	CheckOrigin: func(r *http.Request) bool {
		return true // 允许跨域
	},
}

func addUsedChannel(c *gin.Context, channelId int) {
	useChannel := c.GetStringSlice("use_channel")
	useChannel = append(useChannel, fmt.Sprintf("%d", channelId))
	c.Set("use_channel", useChannel)
}

// shouldSuppressFinalErrorBody 判断最终错误正文是否必须被抑制喵。
//
// 判定思路喵：一旦虚拟模型的某个候选已经把有效响应字节写给客户端，
// 后续任何收尾失败都不能再追加第二个 JSON 错误，否则会拼接在已发出的流式响应之后，
// 让客户端把两段互不兼容的正文当成同一个响应来解析喵。
// 普通模型请求保持上游既有行为不变，避免影响非虚拟路径的错误可见性喵。
//
// 输入：Gin 上下文；当前请求是否处于虚拟模型候选执行中喵。
// 输出：true 表示必须静默返回，只留日志；false 表示照常写出错误正文喵。
func shouldSuppressFinalErrorBody(c *gin.Context, isVirtualCandidateRequest bool) bool {
	// 喵~防御：缺少上下文或 ResponseWriter 时按不抑制处理，保证错误仍可被观测喵。
	if c == nil || c.Writer == nil {
		return false
	}
	// 尚未写出任何字节时可以安全写出完整错误正文喵。
	if !c.Writer.Written() {
		return false
	}
	// 只有虚拟模型候选请求才抑制；普通请求维持上游原有语义喵。
	return isVirtualCandidateRequest
}

// switchToNextVirtualModelCandidate 结束当前候选并为已激活的后备内部候选建立独立 relay 与计费喵。
//
// 整体思路喵：候选之间不共享任何可变状态，所以切换必须严格按下列顺序执行，
// 任一步失败都立刻返回错误，并阻止后备候选建立预扣或发起上游连接喵。
//  1. 校验确实存在已激活的内部候选，普通请求误入此路径时只返回内部错误；
//  2. 让旧候选的计费会话进入终态（同步退款），确认旧额度确实已经释放；
//  3. 按候选真实模型重新解析请求体，禁止复用上一候选改写过的请求对象；
//  4. 用候选工厂创建全新 RelayInfo，工厂内部会断言未继承任何候选级状态；
//  5. 按候选模型与固定分组重新定价，并为该候选单独预扣额度。
//
// 输入：Gin 上下文、当前候选 relay、请求级基线、协议格式、请求级 token 元数据喵。
// 输出：后备候选专属 RelayInfo；失败时返回可直接回传客户端的结构化错误喵。
func switchToNextVirtualModelCandidate(c *gin.Context, currentRelayInfo *relaycommon.RelayInfo, baseline *relaycommon.CandidateRelayBaseline, relayFormat types.RelayFormat, meta *types.TokenCountMeta) (*relaycommon.RelayInfo, *types.NewAPIError) {
	// 喵~防御：缺少上下文、旧候选 relay、请求级基线或 token 元数据时禁止切换候选喵。
	if c == nil || currentRelayInfo == nil || baseline == nil || meta == nil {
		return nil, types.NewError(errors.New("virtual model candidate switch context is unavailable"), types.ErrorCodeModelPriceError, types.ErrOptionWithSkipRetry())
	}
	candidateAttempt, foundCandidateAttempt := middleware.GetActiveVirtualModelCandidateAttempt(c)
	// 喵~防御：普通请求或候选未激活时绝不进入候选级计费路径，避免改动普通 billing 语义喵。
	if !foundCandidateAttempt || candidateAttempt.SourceType != model.VirtualModelSourceInternal {
		return nil, types.NewError(errors.New("virtual model candidate is unavailable"), types.ErrorCodeModelPriceError, types.ErrOptionWithSkipRetry())
	}
	// 第一步：旧候选必须先完成同步退款；额度未确认释放前不允许为新候选预扣喵。
	if currentRelayInfo.Billing != nil {
		if refundError := currentRelayInfo.Billing.RefundImmediately(c); refundError != nil {
			return nil, types.NewError(refundError, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
		}
	}
	// 第二步：重新解析请求体；中间件已在激活候选时恢复原始请求并改写顶层 model 喵。
	candidateRequest, parseError := helper.GetAndValidateRequest(c, relayFormat)
	// 喵~防御：候选请求无法解析时停止候选链，避免把上一候选的请求对象发给新上游喵。
	if parseError != nil {
		return nil, types.NewError(parseError, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	// 第三步：用候选工厂创建全新 RelayInfo，工厂会断言 Channel、计费、定价与重试状态均为初始值喵。
	candidateRelayInfo, candidateRelayInfoError := relaycommon.NewCandidateRelayInfo(c, baseline, relaycommon.CandidateRelayIdentity{
		CandidateID:        candidateAttempt.CandidateID,
		CandidateAttemptID: candidateAttempt.CandidateAttemptID,
		RealModelName:      candidateAttempt.RealModelName,
		GroupName:          candidateAttempt.GroupName,
	}, candidateRequest)
	// 喵~防御：候选隔离前提不满足时立即失败，绝不退化成继续复用旧候选的 RelayInfo 喵。
	if candidateRelayInfoError != nil {
		return nil, types.NewError(candidateRelayInfoError, types.ErrorCodeModelPriceError, types.ErrOptionWithSkipRetry())
	}
	// 记录候选切换事件；只输出候选编号、尝试标识、真实模型与分组，禁止写入凭据或请求正文喵。
	logger.LogInfo(c, fmt.Sprintf("虚拟模型切换候选：attempt=%s candidate=%d model=%s group=%s",
		candidateAttempt.CandidateAttemptID, candidateAttempt.CandidateID, candidateAttempt.RealModelName, candidateAttempt.GroupName))
	// 第四步：按候选自己的模型与固定分组重新定价，免费候选不建立预扣会话喵。
	priceData, priceError := helper.ModelPriceHelper(c, candidateRelayInfo, candidateRelayInfo.GetEstimatePromptTokens(), meta)
	if priceError != nil {
		return nil, types.NewError(priceError, types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusBadRequest))
	}
	if priceData.FreeModel {
		logger.LogInfo(c, fmt.Sprintf("模型 %s 免费，跳过预扣费", candidateRelayInfo.OriginModelName))
		return candidateRelayInfo, nil
	}
	// 第五步：为当前候选单独预扣额度；失败时不返回半成品候选 relay，避免外层继续调用上游喵。
	if preConsumeError := service.PreConsumeBilling(c, priceData.QuotaToPreConsume, candidateRelayInfo); preConsumeError != nil {
		return nil, preConsumeError
	}
	return candidateRelayInfo, nil
}

func fastTokenCountMetaForPricing(request dto.Request) *types.TokenCountMeta {
	if request == nil {
		return &types.TokenCountMeta{}
	}
	meta := &types.TokenCountMeta{
		TokenType: types.TokenTypeTokenizer,
	}
	switch r := request.(type) {
	case *dto.GeneralOpenAIRequest:
		maxCompletionTokens := lo.FromPtrOr(r.MaxCompletionTokens, uint(0))
		maxTokens := lo.FromPtrOr(r.MaxTokens, uint(0))
		if maxCompletionTokens > maxTokens {
			meta.MaxTokens = int(maxCompletionTokens)
		} else {
			meta.MaxTokens = int(maxTokens)
		}
	case *dto.OpenAIResponsesRequest:
		meta.MaxTokens = int(lo.FromPtrOr(r.MaxOutputTokens, uint(0)))
	case *dto.ClaudeRequest:
		meta.MaxTokens = int(lo.FromPtr(r.MaxTokens))
	case *dto.ImageRequest:
		// Pricing for image requests depends on ImagePriceRatio; safe to compute even when CountToken is disabled.
		return r.GetTokenCountMeta()
	default:
		// Best-effort: leave CombineText empty to avoid large allocations.
	}
	return meta
}

func getChannel(c *gin.Context, info *relaycommon.RelayInfo, retryParam *service.RetryParam) (*model.Channel, *types.NewAPIError) {
	// 渠道上下文已有可用渠道且本次尚未换候选：普通请求由 Distribute 预选、虚拟候选首个尝试
	// 由中间件预选，此时直接复用上下文渠道即可喵。
	if info.ChannelMeta == nil && c.GetInt("channel_id") != 0 {
		autoBan := c.GetBool("auto_ban")
		autoBanInt := 1
		if !autoBan {
			autoBanInt = 0
		}
		return &model.Channel{
			Id:      c.GetInt("channel_id"),
			Type:    c.GetInt("channel_type"),
			Name:    c.GetString("channel_name"),
			AutoBan: &autoBanInt,
		}, nil
	}
	// ChannelMeta 非空代表同候选内原生渠道重试；ChannelMeta 为空但上下文渠道已被候选激活清空
	// 代表虚拟模型刚切换到新分组候选。两种情况都必须按 retryParam 的分组+模型重新选择渠道，
	// 不能复用上一个候选留在上下文里的旧渠道喵。
	channel, selectGroup, err := service.CacheGetRandomSatisfiedChannel(retryParam)
	if err != nil {
		return nil, types.NewError(fmt.Errorf("获取分组 %s 下模型 %s 的可用渠道失败（retry）: %s", selectGroup, info.OriginModelName, err.Error()), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	if channel == nil {
		return nil, types.NewError(fmt.Errorf("分组 %s 下模型 %s 的可用渠道不存在（retry）", selectGroup, info.OriginModelName), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}

	// 分组可能因 auto 跨分组重试而变化：按最终分组整体刷新定价口径（计费方式、价格、倍率、
	// 阶梯表达式），否则会拿旧分组的价格去结算实际由新分组上游完成的请求喵。
	if repriceErr := helper.RefreshPriceDataForSelectedGroup(c, info); repriceErr != nil {
		return nil, types.NewError(repriceErr, types.ErrorCodeModelPriceError, types.ErrOptionWithSkipRetry())
	}

	selectedModel := retryParam.SelectedModel
	if selectedModel == "" {
		selectedModel = info.OriginModelName
	}
	newAPIError := middleware.SetupContextForSelectedChannel(c, channel, selectedModel)
	if newAPIError != nil {
		return nil, newAPIError
	}
	if selectedModel != info.OriginModelName {
		info.UpstreamModelName = selectedModel
		c.Set(string(constant.ContextKeyOriginalModel), info.OriginModelName)
	}
	return channel, nil
}

func shouldRetry(c *gin.Context, openaiErr *types.NewAPIError, retryTimes int) bool {
	if openaiErr == nil {
		return false
	}
	if service.ShouldSkipRetryAfterChannelAffinityFailure(c) {
		return false
	}
	if retryTimes <= 0 {
		return false
	}
	if types.IsChannelError(openaiErr) {
		return true
	}
	if types.IsSkipRetryError(openaiErr) {
		return false
	}
	if _, ok := c.Get("specific_channel_id"); ok || service.GetChannelConstraints(c).SuppressesRetry() {
		return false
	}
	code := openaiErr.StatusCode
	if code >= 200 && code < 300 {
		return false
	}
	if code < 100 || code > 599 {
		return true
	}
	if operation_setting.IsAlwaysSkipRetryCode(openaiErr.GetErrorCode()) {
		return false
	}
	return operation_setting.ShouldRetryByStatusCode(code)
}

func getRetryTimesForCurrentGroup(c *gin.Context, tokenGroup string) int {
	group := tokenGroup
	if usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup); usingGroup != "" {
		group = usingGroup
	}
	if autoGroup := common.GetContextKeyString(c, constant.ContextKeyAutoGroup); autoGroup != "" {
		group = autoGroup
	}
	retryTimes := ratio_setting.GetGroupRetryTimes(group, common.RetryTimes)
	if tokenGroup == "auto" {
		modelName := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)
		if route, ok := service.GetRequestAutoRoute(c, modelName); ok && len(route)-1 > retryTimes {
			retryTimes = len(route) - 1
		}
	}
	return retryTimes
}

func processChannelError(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError) {
	logger.LogError(c, fmt.Sprintf("channel error (channel #%d, status code: %d): %s", channelError.ChannelId, err.StatusCode, common.LocalLogPreview(err.Error())))
	// 不要使用context获取渠道信息，异步处理时可能会出现渠道信息不一致的情况
	// do not use context to get channel info, there may be inconsistent channel info when processing asynchronously
	if service.ShouldDisableChannel(err) && channelError.AutoBan {
		gopool.Go(func() {
			service.DisableChannel(channelError, err.ErrorWithStatusCode())
		})
	}

	if constant.ErrorLogEnabled && types.IsRecordErrorLog(err) {
		// 保存错误日志到mysql中
		userId := c.GetInt("id")
		tokenName := c.GetString("token_name")
		modelName := c.GetString("original_model")
		tokenId := c.GetInt("token_id")
		userGroup := c.GetString("group")
		channelId := c.GetInt("channel_id")
		other := make(map[string]interface{})
		if c.Request != nil && c.Request.URL != nil {
			other["request_path"] = c.Request.URL.Path
		}
		other["error_type"] = err.GetErrorType()
		other["error_code"] = err.GetErrorCode()
		other["status_code"] = err.StatusCode
		other["channel_id"] = channelId
		other["channel_name"] = c.GetString("channel_name")
		other["channel_type"] = c.GetInt("channel_type")
		adminInfo := make(map[string]interface{})
		adminInfo["use_channel"] = c.GetStringSlice("use_channel")
		isMultiKey := common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey)
		if isMultiKey {
			adminInfo["is_multi_key"] = true
			adminInfo["multi_key_index"] = common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex)
		}
		service.AppendChannelAffinityAdminInfo(c, adminInfo)
		other["admin_info"] = adminInfo
		service.AppendTaskPluginContextAuditInfo(c, other)
		startTime := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
		if startTime.IsZero() {
			startTime = time.Now()
		}
		useTimeSeconds := int(time.Since(startTime).Seconds())
		model.RecordErrorLog(c, userId, channelId, modelName, tokenName, err.MaskSensitiveErrorWithStatusCode(), tokenId, useTimeSeconds, common.GetContextKeyBool(c, constant.ContextKeyIsStream), userGroup, other)
	}

}

func RelayMidjourney(c *gin.Context) {
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatMjProxy, nil, nil)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"description": fmt.Sprintf("failed to generate relay info: %s", err.Error()),
			"type":        "upstream_error",
			"code":        4,
		})
		return
	}

	var mjErr *taskdto.MidjourneyResponse
	switch relayInfo.RelayMode {
	case relayconstant.RelayModeMidjourneyNotify:
		mjErr = relay.RelayMidjourneyNotify(c)
	case relayconstant.RelayModeMidjourneyTaskFetch, relayconstant.RelayModeMidjourneyTaskFetchByCondition:
		mjErr = relay.RelayMidjourneyTask(c, relayInfo.RelayMode)
	case relayconstant.RelayModeMidjourneyTaskImageSeed:
		mjErr = relay.RelayMidjourneyTaskImageSeed(c)
	case relayconstant.RelayModeSwapFace:
		mjErr = relay.RelaySwapFace(c, relayInfo)
	default:
		mjErr = relay.RelayMidjourneySubmit(c, relayInfo)
	}
	//err = relayMidjourneySubmit(c, relayMode)
	log.Println(mjErr)
	if mjErr != nil {
		statusCode := http.StatusBadRequest
		if mjErr.Code == 30 {
			mjErr.Result = "当前分组负载已饱和，请稍后再试，或升级账户以提升服务质量。"
			statusCode = http.StatusTooManyRequests
		}
		c.JSON(statusCode, gin.H{
			"description": fmt.Sprintf("%s %s", mjErr.Description, mjErr.Result),
			"type":        "upstream_error",
			"code":        mjErr.Code,
		})
		channelId := c.GetInt("channel_id")
		logger.LogError(c, fmt.Sprintf("relay error (channel #%d, status code %d): %s", channelId, statusCode, fmt.Sprintf("%s %s", mjErr.Description, mjErr.Result)))
	}
}

func RelayNotImplemented(c *gin.Context) {
	err := types.OpenAIError{
		Message: "API not implemented",
		Type:    "new_api_error",
		Param:   "",
		Code:    "api_not_implemented",
	}
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": err,
	})
}

func RelayNotFound(c *gin.Context) {
	err := types.OpenAIError{
		Message: fmt.Sprintf("Invalid URL (%s %s)", c.Request.Method, c.Request.URL.Path),
		Type:    "invalid_request_error",
		Param:   "",
		Code:    "",
	}
	c.JSON(http.StatusNotFound, gin.H{
		"error": err,
	})
}

// RelayTaskPluginEndpoint keeps unclaimed shared-endpoint traffic on its
// existing handler while claimed requests enter the generation-pinned
// host-owned protocol bridge.
func RelayTaskPluginEndpoint(c *gin.Context, fallback gin.HandlerFunc) {
	pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedEndpoint)
	if !exists {
		fallback(c)
		return
	}
	pinned, ok := pinnedValue.(pluginruntime.PinnedEndpoint)
	if !ok || pinned.Plugin == nil || pinned.Generation == nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": "Task protocol request failed",
				"type":    "new_api_error",
				"code":    "task_protocol_error",
			},
		})
		return
	}
	if pinned.Protocol != "openai_responses" {
		fallback(c)
		return
	}
	serveTaskPluginProtocol(c, pinned, defaultPluginProtocolBridgeDeps())
}

func RelayTaskFetch(c *gin.Context) {
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, &taskdto.TaskError{
			Code:       "gen_relay_info_failed",
			Message:    err.Error(),
			StatusCode: http.StatusInternalServerError,
		})
		return
	}
	if taskErr := relay.RelayTaskFetch(c, relayInfo.RelayMode); taskErr != nil {
		respondTaskError(c, taskErr)
	}
}

type taskSubmissionOutcome struct {
	Result    *relay.TaskSubmitResult
	Task      *model.Task
	RelayInfo *relaycommon.RelayInfo
}

func RelayTask(c *gin.Context) {
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		respondTaskSubmissionError(c, &taskdto.TaskError{
			Code:       "gen_relay_info_failed",
			Message:    err.Error(),
			StatusCode: http.StatusInternalServerError,
		})
		return
	}
	if action := c.GetString("task_action"); action != "" {
		relayInfo.Action = action
	}

	if taskErr := relay.ResolveOriginTask(c, relayInfo); taskErr != nil {
		respondTaskSubmissionError(c, taskErr)
		return
	}
	if taskErr := relay.ApplyOriginTaskAffinity(c, relayInfo); taskErr != nil {
		respondTaskSubmissionError(c, taskErr)
		return
	}

	outcome, taskErr := executeTaskSubmission(c, relayInfo)
	if taskErr != nil {
		respondTaskSubmissionError(c, taskErr)
		return
	}
	presentTaskSubmission(c, outcome)
}

// executeTaskSubmission owns the retry, billing, and persistence lifecycle.
// It deliberately performs no client response writes so JSON and protocol
// presenters share the same durable task barrier. Its cancellation semantics
// come from c.Request.Context: native task endpoints use the client context,
// while the Responses bridge supplies an independently bounded context.
func executeTaskSubmission(c *gin.Context, relayInfo *relaycommon.RelayInfo) (*taskSubmissionOutcome, *taskdto.TaskError) {
	return executeTaskSubmissionWith(c, relayInfo, relay.RelayTaskSubmit)
}

type taskSubmitAttempt func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *taskdto.TaskError)

func executeTaskSubmissionWith(
	c *gin.Context,
	relayInfo *relaycommon.RelayInfo,
	submit taskSubmitAttempt,
) (*taskSubmissionOutcome, *taskdto.TaskError) {
	diagnostics := newTaskPluginSubmitDiagnostics(c)
	diagnostics.start(relayInfo)
	var result *relay.TaskSubmitResult
	var taskErr *taskdto.TaskError
	durable := false
	stage := "start"
	defer func() {
		if !durable && relayInfo.Billing != nil {
			diagnostics.refund(stage)
			relayInfo.Billing.Refund(c)
		}
	}()
	stage = "before_attempt"
	if requestErr := c.Request.Context().Err(); requestErr != nil {
		diagnostics.cancelled("before_attempt", 0)
		return nil, service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
	}

	retryParam := &service.RetryParam{
		Ctx:         c,
		TokenGroup:  relayInfo.TokenGroup,
		ModelName:   relayInfo.OriginModelName,
		RequestPath: c.Request.URL.Path,
		Retry:       common.GetPointer(0),
	}

	for ; retryParam.GetRetry() <= getRetryTimesForCurrentGroup(c, relayInfo.TokenGroup); retryParam.IncreaseRetry() {
		stage = "select_channel"
		if requestErr := c.Request.Context().Err(); requestErr != nil {
			diagnostics.cancelled("before_attempt", retryParam.GetRetry()+1)
			taskErr = service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
			break
		}
		var channel *model.Channel

		if lockedCh, ok := relayInfo.LockedChannel.(*model.Channel); ok && lockedCh != nil {
			channel = lockedCh
			if retryParam.GetRetry() > 0 || common.GetContextKeyInt(c, constant.ContextKeyChannelId) != channel.Id {
				if setupErr := middleware.SetupContextForSelectedChannel(c, channel, relayInfo.OriginModelName); setupErr != nil {
					taskErr = service.TaskErrorWrapperLocal(setupErr.Err, "setup_locked_channel_failed", http.StatusInternalServerError)
					break
				}
			}
		} else {
			var channelErr *types.NewAPIError
			channel, channelErr = getChannel(c, relayInfo, retryParam)
			if channelErr != nil {
				logger.LogError(c, channelErr.Error())
				taskErr = service.TaskErrorWrapperLocal(channelErr.Err, "get_channel_failed", http.StatusInternalServerError)
				break
			}
		}
		diagnostics.attempt(retryParam.GetRetry()+1, channel, relayInfo.LockedChannel != nil)

		addUsedChannel(c, channel.Id)
		bodyStorage, bodyErr := common.GetBodyStorage(c)
		if bodyErr != nil {
			stage = "read_body"
			if common.IsRequestBodyTooLargeError(bodyErr) || errors.Is(bodyErr, common.ErrRequestBodyTooLarge) {
				taskErr = service.TaskErrorWrapperLocal(bodyErr, "read_request_body_failed", http.StatusRequestEntityTooLarge)
			} else {
				taskErr = service.TaskErrorWrapperLocal(bodyErr, "read_request_body_failed", http.StatusBadRequest)
			}
			break
		}
		c.Request.Body = io.NopCloser(bodyStorage)

		stage = "submit"
		result, taskErr = submit(c, relayInfo)
		if requestErr := c.Request.Context().Err(); requestErr != nil {
			diagnostics.cancelled("after_submit", retryParam.GetRetry()+1)
			taskErr = service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
			break
		}
		if taskErr == nil {
			diagnostics.attemptSucceeded(retryParam.GetRetry()+1, result)
			break
		}

		if !taskErr.LocalError {
			processChannelError(c,
				*types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey,
					common.GetContextKeyString(c, constant.ContextKeyChannelKey), channel.GetAutoBan()),
				types.NewOpenAIError(taskErr.Error, types.ErrorCodeBadResponseStatusCode, taskErr.StatusCode))
		}

		retryTimes := getRetryTimesForCurrentGroup(c, relayInfo.TokenGroup)
		willRetry := shouldRetryTaskRelay(c, channel.Id, taskErr, retryTimes-retryParam.GetRetry())
		diagnostics.attemptFailed(retryParam.GetRetry()+1, channel, taskErr, willRetry)
		if !willRetry {
			break
		}
	}

	useChannel := c.GetStringSlice("use_channel")
	if len(useChannel) > 1 {
		retryLogStr := fmt.Sprintf("重试：%s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
		logger.LogInfo(c, retryLogStr)
	}

	if taskErr != nil {
		diagnostics.failed(stage, "task_error", taskErr, false)
		return nil, taskErr
	}
	if result == nil {
		taskErr = service.TaskErrorWrapperLocal(errors.New("task submission returned no result"), "task_submit_failed", http.StatusInternalServerError)
		diagnostics.failed("submit", "missing_result", taskErr, false)
		return nil, taskErr
	}
	if requestErr := c.Request.Context().Err(); requestErr != nil {
		diagnostics.cancelled("before_reserve", retryParam.GetRetry()+1)
		return nil, service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
	}

	// Reserve any submit-time upward billing adjustment before persistence.
	// This keeps insertion failures fully refundable while ensuring settlement
	// after the barrier normally has a zero positive delta.
	if relayInfo.Billing != nil {
		stage = "reserve"
		diagnostics.reserve("reserve_start", result.Quota)
		if reserveErr := relayInfo.Billing.Reserve(result.Quota); reserveErr != nil {
			common.SysError("reserve adjusted task billing error: " + reserveErr.Error())
			taskErr = service.TaskErrorWrapperLocal(errors.New("insufficient quota for adjusted task cost"), string(types.ErrorCodeInsufficientUserQuota), http.StatusForbidden)
			diagnostics.failed("reserve", "insufficient_quota", taskErr, false)
			return nil, taskErr
		}
		diagnostics.reserve("reserve_complete", result.Quota)
	}
	if requestErr := c.Request.Context().Err(); requestErr != nil {
		diagnostics.cancelled("before_insert", retryParam.GetRetry()+1)
		return nil, service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
	}

	stage = "insert"
	task := model.InitTask(result.Platform, relayInfo)
	task.PrivateData.Execution = service.TaskExecutionSnapshotFromContext(c)
	task.PrivateData.UpstreamTaskID = result.UpstreamTaskID
	task.PrivateData.BillingSource = relayInfo.BillingSource
	task.PrivateData.SubscriptionId = relayInfo.SubscriptionId
	task.PrivateData.TokenId = relayInfo.TokenId
	task.PrivateData.NodeName = common.NodeName
	task.PrivateData.BillingContext = &model.TaskBillingContext{
		ModelPrice:      relayInfo.PriceData.ModelPrice,
		GroupRatio:      relayInfo.PriceData.GroupRatioInfo.GroupRatio,
		ModelRatio:      relayInfo.PriceData.ModelRatio,
		OtherRatios:     relayInfo.PriceData.OtherRatios(),
		OriginModelName: relayInfo.OriginModelName,
		PerCallBilling:  common.StringsContains(constant.TaskPricePatches, relayInfo.OriginModelName) || relayInfo.PriceData.UsePrice,
		TieredSnapshot:  relayInfo.TieredBillingSnapshot,
	}
	task.Quota = result.Quota
	task.Data = result.TaskData
	task.Action = relayInfo.Action
	if immediate := result.Immediate; immediate != nil {
		task.Status = model.TaskStatus(immediate.Status)
		task.Progress = immediate.Progress
		if immediate.Status == model.TaskStatusSuccess || immediate.Status == model.TaskStatusFailure {
			task.FinishTime = time.Now().Unix()
		}
		if immediate.Status == model.TaskStatusFailure {
			task.FailReason = immediate.Reason
		}
		if immediate.Url != "" {
			task.PrivateData.ResultURL = immediate.Url
		} else if immediate.Status == model.TaskStatusSuccess {
			task.PrivateData.ResultURL = taskcommon.BuildProxyURL(task.TaskID)
		}
	}
	diagnostics.insertStart(task)
	if insertErr := task.InsertWithContext(c.Request.Context()); insertErr != nil {
		common.SysError("insert task error: " + insertErr.Error())
		taskErr = service.TaskErrorWrapperLocal(errors.New("failed to persist task"), "task_insert_failed", http.StatusInternalServerError)
		diagnostics.failed("insert", "database_error", taskErr, false)
		return nil, taskErr
	}
	durable = true
	stage = "settle"
	diagnostics.durable(task)
	diagnostics.settleStart(task, result.Quota)

	if settleErr := service.SettleBilling(c, relayInfo, result.Quota); settleErr != nil {
		common.SysError("settle task billing error: " + settleErr.Error())
		taskErr = service.TaskErrorWrapperLocal(errors.New("failed to settle task billing"), "task_billing_settlement_failed", http.StatusInternalServerError)
		diagnostics.failed("settle", "billing_error", taskErr, true)
		return nil, taskErr
	}
	service.LogTaskConsumption(c, relayInfo, task)
	diagnostics.complete(task, result.Quota)

	return &taskSubmissionOutcome{Result: result, Task: task, RelayInfo: relayInfo}, nil
}

func presentTaskSubmission(c *gin.Context, outcome *taskSubmissionOutcome) {
	diagnostics := newTaskPluginSubmitDiagnostics(c)
	otherRatios := outcome.RelayInfo.PriceData.OtherRatios()
	if otherRatios == nil {
		otherRatios = map[string]float64{}
	}
	if ratiosJSON, err := common.Marshal(otherRatios); err == nil {
		c.Header("X-New-Api-Other-Ratios", string(ratiosJSON))
	}
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedRoute); exists {
		if pinned, ok := pinnedValue.(pluginruntime.PinnedRoute); ok && pinned.Plugin != nil && pinned.Route.Render != "" {
			view, err := service.BuildTaskPluginView(outcome.Task)
			requestValue, _ := c.Get(pluginruntime.ContextKeyRouteRequest)
			requestContext, _ := requestValue.(pluginruntime.RouteRequestContext)
			if err == nil {
				viewValue, valueErr := taskPluginProtocolJSONValue(view)
				if valueErr == nil {
					if body, callErr := pinned.Plugin.Engine.CallPath(c.Request.Context(), "native", []string{pinned.Route.Render}, requestContext.JSValue(), viewValue); callErr == nil {
						diagnostics.present(outcome.Task, "native_presenter")
						c.JSON(http.StatusOK, body)
						return
					} else {
						logger.LogError(c, "task plugin native submit presenter failed: "+callErr.Error())
					}
				} else {
					logger.LogError(c, "encode task plugin native submit view failed: "+valueErr.Error())
				}
			} else {
				logger.LogError(c, "build task plugin native submit view failed: "+err.Error())
			}
		}
	}
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedEndpoint); exists {
		if pinned, ok := pinnedValue.(pluginruntime.PinnedEndpoint); ok && pinned.Protocol == "openai_video" && pinned.Operation.Name == "create" {
			diagnostics.present(outcome.Task, "openai_video_create")
			c.JSON(http.StatusOK, outcome.Task.ToOpenAIVideo())
			return
		}
	}
	createdAt := outcome.Task.CreatedAt
	if createdAt == 0 {
		createdAt = outcome.Task.SubmitTime
	}
	diagnostics.present(outcome.Task, "host_fallback")
	c.JSON(http.StatusOK, map[string]any{
		"id":         outcome.Task.TaskID,
		"task_id":    outcome.Task.TaskID,
		"status":     "queued",
		"model":      outcome.RelayInfo.OriginModelName,
		"created_at": createdAt,
	})
}

func respondTaskSubmissionError(c *gin.Context, taskErr *taskdto.TaskError) {
	newTaskPluginSubmitDiagnostics(c).presentError(taskErr)
	if middleware.RespondTaskPluginError(c, taskErr) {
		return
	}
	respondTaskError(c, taskErr)
}

// respondTaskError 统一输出 Task 错误响应（含 429 限流提示改写）
func respondTaskError(c *gin.Context, taskErr *taskdto.TaskError) {
	modelName := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)
	if !operation_setting.ApplyTaskErrorRewrite(taskErr, modelName) && taskErr.StatusCode == http.StatusTooManyRequests {
		taskErr.Message = "当前分组上游负载已饱和，请稍后再试"
	}
	c.JSON(taskErr.StatusCode, taskErr)
}

func shouldRetryTaskRelay(c *gin.Context, channelId int, taskErr *taskdto.TaskError, retryTimes int) bool {
	if taskErr == nil {
		return false
	}
	if service.ShouldSkipRetryAfterChannelAffinityFailure(c) {
		return false
	}
	if retryTimes <= 0 {
		return false
	}
	if service.GetChannelConstraints(c).SuppressesRetry() {
		return false
	}
	if taskErr.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if taskErr.StatusCode == 307 {
		return true
	}
	if taskErr.StatusCode/100 == 5 {
		// 超时不重试
		if operation_setting.IsAlwaysSkipRetryStatusCode(taskErr.StatusCode) {
			return false
		}
		return true
	}
	if taskErr.StatusCode == http.StatusBadRequest {
		return false
	}
	if taskErr.StatusCode == 408 {
		// azure处理超时不重试
		return false
	}
	if taskErr.LocalError {
		return false
	}
	if taskErr.StatusCode/100 == 2 {
		return false
	}
	return true
}
