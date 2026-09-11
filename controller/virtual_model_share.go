package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	virtualmodelservice "github.com/QuantumNous/new-api/service/virtualmodel"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 分享码导入预检的跳过原因码喵。
// 喵~防御：这些是稳定契约，前端按它们查 i18n 文案，因此不得随意改名喵。
const (
	// virtualModelShareSkipGroupNotConfigured 表示候选没有配置分组，永远无法执行喵。
	virtualModelShareSkipGroupNotConfigured = "group_not_configured"
	// virtualModelShareSkipAutoGroup 表示候选用了 auto 分组，虚拟模型不支持自动分组喵。
	virtualModelShareSkipAutoGroup = "auto_group_not_supported"
	// virtualModelShareSkipGroupNotAccessible 表示导入方账号没有该分组的访问权限喵。
	virtualModelShareSkipGroupNotAccessible = "group_not_accessible"
	// virtualModelShareSkipModelNotInGroup 表示该分组下当前没有这个模型的可用渠道喵。
	virtualModelShareSkipModelNotInGroup = "model_not_available_in_group"
	// virtualModelShareSkipBaseURLRejected 表示候选的上游地址未通过本实例的地址策略喵。
	virtualModelShareSkipBaseURLRejected = "base_url_rejected"
)

// 分享码导入结果的提示码喵，同样由前端负责翻译成用户可见文案喵。
const (
	// virtualModelShareWarningNoCallableCandidate 表示本次导入没有任何内部候选可被直接调用喵。
	virtualModelShareWarningNoCallableCandidate = "no_directly_callable_candidate"
	// virtualModelShareWarningAllCandidatesSkipped 表示全部候选都被跳过，方案只剩空壳喵。
	virtualModelShareWarningAllCandidatesSkipped = "all_candidates_skipped"
)

// 分享码快照的硬上限喵。
// 候选数量与候选链编辑器保持一致；失败规则条数是防御性上限，避免恶意快照拖垮导入喵。
const (
	// maxVirtualModelCandidateCount 是候选链允许的最大候选数量喵。
	// 主人注意：该值与 ReplaceVirtualModelCandidates 里的字面量 32 必须保持一致；
	// 之所以不直接复用那边的字面量，是因为该文件当前有他人未提交的改动，
	// 跨文件共享常量会把两方的改动缠在同一个 commit 里，等对方提交落地后再合并成一个常量喵。
	maxVirtualModelCandidateCount = 32
	// virtualModelShareMaxFailureRulesPerCandidate 是单个候选允许导入的最大失败规则条数喵。
	virtualModelShareMaxFailureRulesPerCandidate = 64
	// virtualModelShareMaxGlobalFailureRules 是模型级允许导入的最大全局兜底规则条数喵。
	virtualModelShareMaxGlobalFailureRules = 64
)

// virtualModelShareCodeCreateInput 描述生成分享码的请求喵。
type virtualModelShareCodeCreateInput struct {
	// VirtualModelID 是要分享的虚拟模型编号，必须属于当前登录用户喵。
	VirtualModelID int `json:"virtual_model_id"`
	// ExpiresAt 是可选到期时间（Unix 秒），零表示永不过期喵。
	ExpiresAt int64 `json:"expires_at"`
	// MaxImports 是可选最大导入次数，零表示不限喵。
	MaxImports int `json:"max_imports"`
}

// virtualModelShareCodeImportInput 描述导入分享码的请求喵。
type virtualModelShareCodeImportInput struct {
	// Code 是用户粘贴的分享码本体喵。
	Code string `json:"code"`
	// NormalizedName 是导入方自己填写的模型名（virtual/ 后面那段），必填喵。
	// 喵~防御：不再自动推导或追加序号，撞名直接报错让用户换一个，避免导入出"名字跟想的不一样"的模型喵。
	NormalizedName string `json:"normalized_name"`
	// DisplayName 是导入方自己填写的显示名，必填喵。
	DisplayName string `json:"display_name"`
}

// virtualModelShareCandidateSkip 描述一个被跳过的候选及其原因喵。
type virtualModelShareCandidateSkip struct {
	// Order 是该候选在分享快照里的原始序号（1 起），与预检清单一一对应喵。
	Order int `json:"order"`
	// Source 是该候选的来源类型（internal / custom）喵。
	Source string `json:"source"`
	// Group 与 Model 是该候选的路由目标，供导入方定位是哪一个候选喵。
	Group string `json:"group,omitempty"`
	Model string `json:"model,omitempty"`
	// Reason 是稳定的机器可读原因码，前端据此选择 i18n 文案喵。
	Reason string `json:"reason"`
	// Message 是中文兜底说明，当前端缺少对应翻译时直接展示喵。
	Message string `json:"message"`
}

// virtualModelShareImportPlan 描述一次导入预检的裁定结果喵。
type virtualModelShareImportPlan struct {
	// KeptCandidates 是通过全部检查、将被真正写入的候选（顺序已重新压紧）喵。
	KeptCandidates []model.VirtualModelShareCandidate
	// SkippedCandidates 是被剔除的候选及原因喵。
	SkippedCandidates []virtualModelShareCandidateSkip
	// Warnings 是不阻断导入但需要提示导入方的提示码喵。
	Warnings []string
}

// virtualModelShareInvalidRequest 用统一 400 响应表示分享码请求体不合法喵。
func virtualModelShareInvalidRequest(c *gin.Context, message string) {
	c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "virtual_model_invalid_request", "message": message})
}

// virtualModelShareUnavailable 用统一 503 响应表示分享码服务暂时不可用喵。
func virtualModelShareUnavailable(c *gin.Context, reason error) {
	// 喵~防御：服务端细节只写日志，不把内部错误原文回给客户端，避免泄露存储结构喵。
	common.SysError("virtual model share code unavailable: " + reason.Error())
	c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "code": "virtual_model_unavailable", "message": "虚拟模型服务暂时不可用"})
}

// buildVirtualModelSharePayload 把虚拟模型组装成脱敏快照喵。
//
// 脱敏规则（与产品约定一致）喵：
//  1. 内部候选零秘密，分组与真实模型名原样导出喵。
//  2. 直填型自定义候选导出上游地址明文、真实模型名与认证方式，但绝不导出 API Key 喵。
//  3. 引用型自定义候选（UpstreamModelID 非空）按属主加载被引用的用户上游条目，
//     把该条目的上游地址、真实模型名与认证方式映射成普通自定义候选导出；
//     接收方拿到的同样是"缺凭据"候选，导入后自行补填自己的 API Key 喵。
//  4. API Key 指纹与地址指纹一律不导出：指纹跨实例稳定，导出等于送给对方一个可离线校验器喵。
func buildVirtualModelSharePayload(virtualModel *model.VirtualModel) (*model.VirtualModelSharePayload, error) {
	// 喵~防御：没有模型就无法生成快照，避免产出空方案喵。
	if virtualModel == nil || virtualModel.ID <= 0 {
		return nil, errors.New("虚拟模型无效")
	}
	var candidates []model.VirtualModelCandidate
	if err := model.DB.Where("virtual_model_id = ?", virtualModel.ID).Order("stable_order asc, id asc").Find(&candidates).Error; err != nil {
		return nil, err
	}
	payload := &model.VirtualModelSharePayload{
		Version:             model.VirtualModelSharePayloadVersion,
		DisplayName:         virtualModel.DisplayName,
		LoopEnabled:         virtualModel.LoopEnabled,
		TotalTimeoutSeconds: virtualModel.TotalTimeoutSeconds,
		MaxLoopRounds:       virtualModel.MaxLoopRounds,
		FakeStreamEnabled:   virtualModel.FakeStreamEnabled,
		StreamCutAction:     string(virtualModel.StreamCutAction),
		StreamCutRetries:    virtualModel.StreamCutRetries,
		Candidates:          make([]model.VirtualModelShareCandidate, 0, len(candidates)),
	}
	for _, candidate := range candidates {
		shareCandidate := model.VirtualModelShareCandidate{
			StableOrder:        candidate.StableOrder,
			SourceType:         candidate.SourceType,
			Enabled:            candidate.Enabled,
			MaxRetries:         candidate.MaxRetries,
			TimeoutSeconds:     candidate.TimeoutSeconds,
			HedgeThreshold:     candidate.HedgeThreshold,
			HedgeFreezeSeconds: candidate.HedgeFreezeSeconds,
		}
		if candidate.SourceType == model.VirtualModelSourceInternal {
			var internalCandidate model.VirtualModelInternalCandidate
			queryError := model.DB.Where("candidate_id = ?", candidate.ID).First(&internalCandidate).Error
			// 喵~防御：来源配置缺失或读取失败时拒绝生成快照，避免导出无法执行的空候选喵。
			if queryError != nil {
				return nil, queryError
			}
			shareCandidate.GroupName = internalCandidate.GroupName
			shareCandidate.RealModelName = internalCandidate.RealModelName
		} else if candidate.SourceType == model.VirtualModelSourceCustom {
			var customCandidate model.VirtualModelCustomCandidate
			queryError := model.DB.Where("candidate_id = ?", candidate.ID).First(&customCandidate).Error
			// 喵~防御：同上，读不到来源配置就不能确定这个候选到底指向哪里，宁可整体失败喵。
			if queryError != nil {
				return nil, queryError
			}
			// 自定义候选的地址与真实模型名有两个来源：引用用户上游条目，或者直填在候选自己身上喵。
			// 先把直填字段当作默认值，命中引用时再整体换成被引用条目的字段喵。
			encryptedBaseURL := customCandidate.EncryptedBaseURL
			credentialVersion := customCandidate.CredentialVersion
			realModelName := customCandidate.RealModelName
			authStyle := customCandidate.AuthStyle
			if customCandidate.UpstreamModelID != nil && *customCandidate.UpstreamModelID > 0 {
				// 喵~防御：必须按快照属主加载被引用的上游条目，避免把其他用户的条目导出去喵。
				referencedUpstream, resolveError := model.GetUserUpstreamModelByOwnerID(*customCandidate.UpstreamModelID, virtualModel.OwnerUserID)
				if resolveError != nil {
					return nil, resolveError
				}
				encryptedBaseURL = referencedUpstream.EncryptedBaseURL
				credentialVersion = referencedUpstream.CredentialVersion
				realModelName = referencedUpstream.RealModelName
				authStyle = model.VirtualModelAuthStyle(referencedUpstream.AuthStyle)
				// 主人注意：这里刻意不解被引用条目的 API Key——分享码本来就不带 Key，
				// 为一个根本不导出的字段让整次分享失败并不划算喵。
			}
			// 喵~防御：地址密文必须解得开，解不开说明密钥轮换或数据损坏，
			// 此时不能把候选降级成"没有地址"的半残配置导出喵。
			plainBaseURL, decryptError := virtualmodelservice.DecryptCredential(encryptedBaseURL, credentialVersion)
			if decryptError != nil {
				return nil, decryptError
			}
			shareCandidate.BaseURL = plainBaseURL
			shareCandidate.RealModelName = realModelName
			// 认证枚举经历史值归一化后再导出，避免把内部旧枚举写进快照喵。
			shareCandidate.AuthStyle = string(model.VirtualModelAuthStyleFromStorage(authStyle))
			// 导入方拿到的是脱敏快照，凭据必须由他自行补填，因此恒标记为需要凭据喵。
			shareCandidate.RequiresCredential = true
		} else {
			return nil, errors.New("虚拟模型候选来源无效")
		}
		failureRules, rulesError := loadVirtualModelShareFailureRules(candidate.ID)
		if rulesError != nil {
			return nil, rulesError
		}
		shareCandidate.FailureRules = failureRules
		payload.Candidates = append(payload.Candidates, shareCandidate)
	}
	globalFailureRules, globalRulesError := loadVirtualModelShareGlobalFailureRules(virtualModel.ID)
	if globalRulesError != nil {
		return nil, globalRulesError
	}
	payload.GlobalFailureRules = globalFailureRules
	return payload, nil
}

// loadVirtualModelShareFailureRules 读取候选级失败规则并转换成快照结构喵。
func loadVirtualModelShareFailureRules(candidateID int) ([]model.VirtualModelShareFailureRule, error) {
	var storedRules []model.VirtualModelFailureRule
	if err := model.DB.Where("candidate_id = ?", candidateID).Order("rule_order asc, id asc").Find(&storedRules).Error; err != nil {
		return nil, err
	}
	shareRules := make([]model.VirtualModelShareFailureRule, 0, len(storedRules))
	for _, storedRule := range storedRules {
		shareRules = append(shareRules, model.VirtualModelShareFailureRule{
			RuleOrder:                storedRule.RuleOrder,
			HTTPStatus:               storedRule.HTTPStatus,
			HTTPStatusMax:            storedRule.HTTPStatusMax,
			ErrorClass:               storedRule.ErrorClass,
			BodyRegex:                storedRule.BodyRegex,
			Action:                   storedRule.Action,
			FreezeSeconds:            storedRule.FreezeSeconds,
			FreezeField:              storedRule.FreezeField,
			FreezeUnit:               storedRule.FreezeUnit,
			StallTimeoutSeconds:      storedRule.StallTimeoutSeconds,
			MinContentChars:          storedRule.MinContentChars,
			ProbeTotalTimeoutSeconds: storedRule.ProbeTotalTimeoutSeconds,
			TimeoutSeconds:           storedRule.TimeoutSeconds,
			RetryCount:               storedRule.RetryCount,
		})
	}
	return shareRules, nil
}

// loadVirtualModelShareGlobalFailureRules 读取模型级全局兜底失败规则并转换成快照结构喵。
func loadVirtualModelShareGlobalFailureRules(virtualModelID int) ([]model.VirtualModelShareFailureRule, error) {
	var storedRules []model.VirtualModelGlobalFailureRule
	if err := model.DB.Where("virtual_model_id = ?", virtualModelID).Order("rule_order asc, id asc").Find(&storedRules).Error; err != nil {
		return nil, err
	}
	shareRules := make([]model.VirtualModelShareFailureRule, 0, len(storedRules))
	for _, storedRule := range storedRules {
		shareRules = append(shareRules, model.VirtualModelShareFailureRule{
			RuleOrder:                storedRule.RuleOrder,
			HTTPStatus:               storedRule.HTTPStatus,
			HTTPStatusMax:            storedRule.HTTPStatusMax,
			ErrorClass:               storedRule.ErrorClass,
			BodyRegex:                storedRule.BodyRegex,
			Action:                   storedRule.Action,
			FreezeSeconds:            storedRule.FreezeSeconds,
			FreezeField:              storedRule.FreezeField,
			FreezeUnit:               storedRule.FreezeUnit,
			StallTimeoutSeconds:      storedRule.StallTimeoutSeconds,
			MinContentChars:          storedRule.MinContentChars,
			ProbeTotalTimeoutSeconds: storedRule.ProbeTotalTimeoutSeconds,
			TimeoutSeconds:           storedRule.TimeoutSeconds,
			RetryCount:               storedRule.RetryCount,
		})
	}
	return shareRules, nil
}

// CreateVirtualModelShareCode 为当前用户自己的虚拟模型生成一枚分享码喵。
func CreateVirtualModelShareCode(c *gin.Context) {
	var input virtualModelShareCodeCreateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		virtualModelShareInvalidRequest(c, "虚拟模型分享码请求无效")
		return
	}
	// 喵~防御：只允许分享自己的模型，loadOwnedVirtualModel 已把越权请求统一转成 404 喵。
	virtualModel, ok := loadOwnedVirtualModel(c, input.VirtualModelID)
	if !ok {
		return
	}
	payload, buildError := buildVirtualModelSharePayload(virtualModel)
	if buildError != nil {
		virtualModelShareUnavailable(c, buildError)
		return
	}
	// 喵~防御：没有任何候选的方案分享出去对接收方毫无意义，直接拒绝生成分享码喵。
	// 主人注意：内部候选与两类自定义候选现在都会进快照，所以这里为 0 只可能是模型本身没有候选喵。
	if len(payload.Candidates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "virtual_model_share_no_candidate", "message": "该方案没有可分享的候选，无法生成分享码"})
		return
	}
	encodedPayload, marshalError := common.Marshal(payload)
	if marshalError != nil {
		virtualModelShareUnavailable(c, marshalError)
		return
	}
	// 喵~防御：分享码取负或超出次数上限的配置没有意义，直接拒绝而不是写入不可用记录喵。
	if input.MaxImports < 0 || input.ExpiresAt < 0 {
		virtualModelShareInvalidRequest(c, "虚拟模型分享码请求无效")
		return
	}
	codeValue, codeError := model.GenerateVirtualModelShareCodeValue()
	if codeError != nil {
		virtualModelShareUnavailable(c, codeError)
		return
	}
	shareCode := &model.VirtualModelShareCode{
		Code:                 codeValue,
		OwnerUserID:          c.GetInt("id"),
		SourceVirtualModelID: virtualModel.ID,
		DisplayName:          virtualModel.DisplayName,
		Payload:              string(encodedPayload),
		PayloadDigest:        model.VirtualModelSharePayloadDigest(string(encodedPayload)),
		Visibility:           model.VirtualModelShareVisibilityPrivate,
		MaxImports:           input.MaxImports,
		ExpiresAt:            input.ExpiresAt,
	}
	if createError := model.CreateVirtualModelShareCode(shareCode); createError != nil {
		virtualModelShareUnavailable(c, createError)
		return
	}
	internalCandidateCount := 0
	customCandidateCount := 0
	for _, shareCandidate := range payload.Candidates {
		if shareCandidate.SourceType == model.VirtualModelSourceInternal {
			internalCandidateCount++
		}
		if shareCandidate.SourceType == model.VirtualModelSourceCustom {
			customCandidateCount++
		}
	}
	// 生成成功即写入审计摘要，方便事后追溯"谁在什么时候分享了哪个方案"喵。
	if auditError := model.DB.Create(&model.VirtualModelAuditLog{
		VirtualModelID: virtualModel.ID,
		OwnerUserID:    c.GetInt("id"),
		OperatorID:     c.GetInt("id"),
		Action:         "share_code_create",
		SummaryDigest:  fmt.Sprintf("share_code:%d;candidates:%d", shareCode.ID, len(payload.Candidates)),
		CreatedTime:    common.GetTimestamp(),
	}).Error; auditError != nil {
		// 喵~防御：审计失败不影响分享码本身已生效的事实，只记日志不向用户报错喵。
		common.SysError("virtual model share code audit failed: " + auditError.Error())
	}
	common.ApiSuccess(c, gin.H{
		"id":                       shareCode.ID,
		"code":                     shareCode.Code,
		"display_name":             shareCode.DisplayName,
		"candidate_count":          len(payload.Candidates),
		"internal_candidate_count": internalCandidateCount,
		"custom_candidate_count":   customCandidateCount,
		"expires_at":               shareCode.ExpiresAt,
		"max_imports":              shareCode.MaxImports,
		"created_time":             shareCode.CreatedTime,
	})
}

// GetVirtualModelShareCodes 返回当前用户生成的分享码列表喵。
func GetVirtualModelShareCodes(c *gin.Context) {
	shareCodes, queryError := model.GetVirtualModelShareCodesByOwner(c.GetInt("id"), 100)
	if queryError != nil {
		virtualModelShareUnavailable(c, queryError)
		return
	}
	responses := make([]gin.H, 0, len(shareCodes))
	for _, shareCode := range shareCodes {
		responses = append(responses, gin.H{
			"id":           shareCode.ID,
			"code":         shareCode.Code,
			"display_name": shareCode.DisplayName,
			"import_count": shareCode.ImportCount,
			"max_imports":  shareCode.MaxImports,
			"expires_at":   shareCode.ExpiresAt,
			"created_time": shareCode.CreatedTime,
		})
	}
	common.ApiSuccess(c, gin.H{"share_codes": responses, "total": len(responses)})
}

// DeleteVirtualModelShareCode 删除当前用户自己的一枚分享码喵。
// 删除是硬删除：删完这一行就从库里消失，别人再拿这枚码导入会得到"分享码不存在"喵。
func DeleteVirtualModelShareCode(c *gin.Context) {
	shareCodeID, convertError := strconv.Atoi(c.Param("codeId"))
	// 喵~防御：非数字或非正数一律按不存在处理，避免非法输入进入查询条件喵。
	if convertError != nil || shareCodeID <= 0 {
		virtualModelShareCodeNotFound(c)
		return
	}
	if deleteError := model.DeleteVirtualModelShareCodeByOwner(c.GetInt("id"), shareCodeID); deleteError != nil {
		// 喵~防御：他人分享码与不存在的分享码统一按同一个 404 处理，避免泄露他人分享码是否存在喵。
		if errors.Is(deleteError, gorm.ErrRecordNotFound) {
			virtualModelShareCodeNotFound(c)
			return
		}
		virtualModelShareUnavailable(c, deleteError)
		return
	}
	common.ApiSuccess(c, gin.H{"id": shareCodeID})
}

// virtualModelShareCodeNotFound 用统一 404 响应表示分享码不存在或已不可用喵。
func virtualModelShareCodeNotFound(c *gin.Context) {
	c.JSON(http.StatusNotFound, gin.H{"success": false, "code": "virtual_model_share_code_not_found", "message": "分享码不存在或已被删除"})
}

// loadVirtualModelSharePayload 读取并校验分享码，返回记录与已解析的快照喵。
// 校验失败时已直接写出响应，调用方只需要按 ok 返回喵。
func loadVirtualModelSharePayload(c *gin.Context, rawCode string) (*model.VirtualModelShareCode, *model.VirtualModelSharePayload, bool) {
	shareCode, queryError := model.GetVirtualModelShareCodeByValue(rawCode)
	if queryError != nil {
		// 喵~防御：不存在与数据库故障区分处理，前者 404 后者 503，避免把故障伪装成"码不对"喵。
		if errors.Is(queryError, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "code": "virtual_model_share_code_not_found", "message": "分享码不存在或已被删除"})
			return nil, nil, false
		}
		virtualModelShareUnavailable(c, queryError)
		return nil, nil, false
	}
	// 喵~防御：过期与超出导入次数都必须显式拒绝，不能用"查不到"含糊带过，
	// 否则分享者无法判断是码写错了还是这枚码已经到期/用尽喵。
	// 撤销走的路径不同：撤销是硬删除，被撤销的分享码在下一次查询时就已经"不存在"了喵。
	if shareCode.ExpiresAt != 0 && shareCode.ExpiresAt <= common.GetTimestamp() {
		c.JSON(http.StatusGone, gin.H{"success": false, "code": "virtual_model_share_code_expired", "message": "分享码已过期"})
		return nil, nil, false
	}
	if shareCode.MaxImports > 0 && shareCode.ImportCount >= shareCode.MaxImports {
		c.JSON(http.StatusGone, gin.H{"success": false, "code": "virtual_model_share_code_exhausted", "message": "分享码导入次数已用尽"})
		return nil, nil, false
	}
	payload := &model.VirtualModelSharePayload{}
	if unmarshalError := common.UnmarshalJsonStr(shareCode.Payload, payload); unmarshalError != nil {
		// 喵~防御：快照是服务端自己写入的，解析失败属于数据损坏，报 503 而不是 400 甩锅给用户喵。
		virtualModelShareUnavailable(c, unmarshalError)
		return nil, nil, false
	}
	if validationError := validateVirtualModelSharePayload(payload); validationError != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "virtual_model_share_payload_invalid", "message": validationError.Error()})
		return nil, nil, false
	}
	return shareCode, payload, true
}

// validateVirtualModelSharePayload 校验快照的版本与结构边界喵。
func validateVirtualModelSharePayload(payload *model.VirtualModelSharePayload) error {
	// 喵~防御：空快照与未知版本都不做兼容尝试，避免把未来格式误读成当前语义喵。
	if payload == nil {
		return errors.New("分享码内容为空")
	}
	if payload.Version != model.VirtualModelSharePayloadVersion {
		return errors.New("分享码格式版本不受支持")
	}
	if len(payload.Candidates) == 0 || len(payload.Candidates) > maxVirtualModelCandidateCount {
		return errors.New("分享码候选数量超出允许范围")
	}
	if len(payload.GlobalFailureRules) > virtualModelShareMaxGlobalFailureRules {
		return errors.New("分享码全局失败规则数量超出允许范围")
	}
	for _, shareCandidate := range payload.Candidates {
		if shareCandidate.SourceType != model.VirtualModelSourceInternal && shareCandidate.SourceType != model.VirtualModelSourceCustom {
			return errors.New("分享码候选来源无效")
		}
		if len(shareCandidate.FailureRules) > virtualModelShareMaxFailureRulesPerCandidate {
			return errors.New("分享码失败规则数量超出允许范围")
		}
		// 喵~防御：内部候选必须带分组与真实模型，否则导入后是一条永远无法执行的空候选喵。
		if shareCandidate.SourceType == model.VirtualModelSourceInternal &&
			(strings.TrimSpace(shareCandidate.GroupName) == "" || strings.TrimSpace(shareCandidate.RealModelName) == "") {
			return errors.New("分享码内部候选缺少分组或真实模型")
		}
	}
	return nil
}

// resolveVirtualModelShareImportPlan 按导入方权限裁定每个候选的去留喵。
// 判定顺序与运行时 middleware/distributor.go 的"失去分组的内部候选从调用链上摘除"保持同源语义，
// 差别只在于：这里额外检查该分组下是否真的存在这个模型（运行时把这步交给原生渠道选择兜底）喵。
func resolveVirtualModelShareImportPlan(c *gin.Context, payload *model.VirtualModelSharePayload) (*virtualModelShareImportPlan, error) {
	// 喵~防御：导入方身份与所属分组缺失时无法做权限裁定，拒绝而不是放行全部候选喵。
	ownerUserID := c.GetInt("id")
	if ownerUserID <= 0 {
		return nil, errors.New("导入方身份无效")
	}
	userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	// 使用 ForUser 版本：只有它才叠加 console_setting 的分组访问规则过滤，旧版本会漏判喵。
	usableGroups := service.GetUserUsableGroupsForUser(ownerUserID, userGroup)
	// 主人注意：这里先把全部内部候选的分组去重，每个不同分组只查一次 abilities 表，
	// 把 N 个候选的 N 次查询压到「不同分组数」次；分组数上限就是候选数量上限 32 喵。
	enabledModelsByGroup := make(map[string]map[string]bool)
	// 喵~防御：abilities 查询函数不返回 error，查询失败时会得到空列表，
	// 于是该分组下所有模型都判为不可用、候选被跳过——这是保守方向（宁可少导入也不导入必然失败的候选）喵。
	for _, shareCandidate := range payload.Candidates {
		groupName := strings.TrimSpace(shareCandidate.GroupName)
		if shareCandidate.SourceType != model.VirtualModelSourceInternal || groupName == "" || groupName == "auto" {
			continue
		}
		if _, alreadyLoaded := enabledModelsByGroup[groupName]; alreadyLoaded {
			continue
		}
		modelSet := make(map[string]bool)
		for _, enabledModelName := range model.GetGroupEnabledModels(groupName) {
			modelSet[enabledModelName] = true
		}
		enabledModelsByGroup[groupName] = modelSet
	}
	plan := &virtualModelShareImportPlan{
		KeptCandidates:    make([]model.VirtualModelShareCandidate, 0, len(payload.Candidates)),
		SkippedCandidates: make([]virtualModelShareCandidateSkip, 0),
		Warnings:          make([]string, 0),
	}
	for candidateIndex, shareCandidate := range payload.Candidates {
		// Order 用 1 起序号，方便前端直接展示"第 N 个候选"喵。
		candidateOrder := candidateIndex + 1
		skipReason, skipMessage := judgeVirtualModelShareCandidateImport(shareCandidate, usableGroups, enabledModelsByGroup)
		if skipReason != "" {
			plan.SkippedCandidates = append(plan.SkippedCandidates, virtualModelShareCandidateSkip{
				Order:   candidateOrder,
				Source:  string(shareCandidate.SourceType),
				Group:   strings.TrimSpace(shareCandidate.GroupName),
				Model:   strings.TrimSpace(shareCandidate.RealModelName),
				Reason:  skipReason,
				Message: skipMessage,
			})
			continue
		}
		// 保留的候选按新顺序重新压紧，避免导入后候选链里留下空洞喵。
		keptCandidate := shareCandidate
		keptCandidate.StableOrder = len(plan.KeptCandidates)
		plan.KeptCandidates = append(plan.KeptCandidates, keptCandidate)
	}
	// 统计保留下来的内部候选数量，用于判断本次导入是否产出"可直接调用"的候选喵。
	callableCandidateCount := 0
	for _, keptCandidate := range plan.KeptCandidates {
		if keptCandidate.SourceType == model.VirtualModelSourceInternal {
			callableCandidateCount++
		}
	}
	if len(plan.KeptCandidates) == 0 {
		plan.Warnings = append(plan.Warnings, virtualModelShareWarningAllCandidatesSkipped)
	} else if callableCandidateCount == 0 {
		plan.Warnings = append(plan.Warnings, virtualModelShareWarningNoCallableCandidate)
	}
	return plan, nil
}

// judgeVirtualModelShareCandidateImport 判定单个候选能否导入，返回空原因码表示可以导入喵。
func judgeVirtualModelShareCandidateImport(
	shareCandidate model.VirtualModelShareCandidate,
	usableGroups map[string]string,
	enabledModelsByGroup map[string]map[string]bool,
) (string, string) {
	if shareCandidate.SourceType == model.VirtualModelSourceCustom {
		// 喵~防御：不带上游地址的自定义候选导入后无法执行，直接剔除而不是留一个空壳喵。
		trimmedBaseURL := strings.TrimSpace(shareCandidate.BaseURL)
		if trimmedBaseURL == "" {
			return virtualModelShareSkipBaseURLRejected, "该候选没有携带上游地址，无法导入"
		}
		// 喵~防御：地址来自他人快照，导入时就按本实例策略校验一遍，
		// 不能等到真正调用时才由拨号阶段的公网 IP 校验兜底喵。
		if _, validateError := virtualmodelservice.ValidateCustomBaseURL(trimmedBaseURL); validateError != nil {
			return virtualModelShareSkipBaseURLRejected, "该候选的上游地址未通过本实例的地址安全策略"
		}
		return "", ""
	}
	groupName := strings.TrimSpace(shareCandidate.GroupName)
	// 喵~防御：空分组与 auto 分组是配置错误，运行时也会拒绝执行，因此导入时直接剔除喵。
	if groupName == "" {
		return virtualModelShareSkipGroupNotConfigured, "该候选没有配置分组"
	}
	if groupName == "auto" {
		return virtualModelShareSkipAutoGroup, "该候选使用了自动分组，虚拟模型不支持自动分组"
	}
	// 与运行时同一套判定：导入方可用分组集合里没有这个分组就摘除该候选喵。
	if _, accessible := usableGroups[groupName]; !accessible {
		return virtualModelShareSkipGroupNotAccessible, "你的账号没有分组「" + groupName + "」的访问权限"
	}
	// 该分组下当前没有这个模型的可用渠道，导入后必然调用失败，因此一并剔除喵。
	if !enabledModelsByGroup[groupName][strings.TrimSpace(shareCandidate.RealModelName)] {
		return virtualModelShareSkipModelNotInGroup, "分组「" + groupName + "」下当前没有模型「" + strings.TrimSpace(shareCandidate.RealModelName) + "」的可用渠道"
	}
	return "", ""
}

// buildVirtualModelShareImportPreview 组装导入预检的响应体喵。
func buildVirtualModelShareImportPreview(shareCode *model.VirtualModelShareCode, payload *model.VirtualModelSharePayload, plan *virtualModelShareImportPlan) gin.H {
	return gin.H{
		"share_code_id": shareCode.ID,
		"display_name":  payload.DisplayName,
		"plan": gin.H{
			"loop_enabled":          payload.LoopEnabled,
			"total_timeout_seconds": payload.TotalTimeoutSeconds,
			"max_loop_rounds":       payload.MaxLoopRounds,
			"fake_stream_enabled":   payload.FakeStreamEnabled,
			"stream_cut_action":     payload.StreamCutAction,
			"stream_cut_retries":    payload.StreamCutRetries,
		},
		"candidate_count":            len(payload.Candidates),
		"importable_candidate_count": len(plan.KeptCandidates),
		"skipped_candidates":         plan.SkippedCandidates,
		"warnings":                   plan.Warnings,
	}
}

// PrecheckVirtualModelShareCode 解析分享码并按导入方权限预演一次导入，不写入任何数据喵。
func PrecheckVirtualModelShareCode(c *gin.Context) {
	var input virtualModelShareCodeImportInput
	if err := c.ShouldBindJSON(&input); err != nil {
		virtualModelShareInvalidRequest(c, "分享码请求无效")
		return
	}
	shareCode, payload, ok := loadVirtualModelSharePayload(c, input.Code)
	if !ok {
		return
	}
	plan, planError := resolveVirtualModelShareImportPlan(c, payload)
	if planError != nil {
		virtualModelShareUnavailable(c, planError)
		return
	}
	common.ApiSuccess(c, buildVirtualModelShareImportPreview(shareCode, payload, plan))
}

// ImportVirtualModelShareCode 把分享码里的方案复制一份到当前用户名下喵。
// 喵~防御：导入是写操作，这里会重新跑一遍预检而不是采信客户端携带的预检结果，
// 因为预检与导入之间分组权限、渠道可用性都可能已经变化（典型的 TOCTOU 场景）喵。
func ImportVirtualModelShareCode(c *gin.Context) {
	var input virtualModelShareCodeImportInput
	if err := c.ShouldBindJSON(&input); err != nil {
		virtualModelShareInvalidRequest(c, "分享码请求无效")
		return
	}
	shareCode, payload, ok := loadVirtualModelSharePayload(c, input.Code)
	if !ok {
		return
	}
	plan, planError := resolveVirtualModelShareImportPlan(c, payload)
	if planError != nil {
		virtualModelShareUnavailable(c, planError)
		return
	}
	// 喵~防御：一个候选都不剩时拒绝导入，避免给用户留下一个永远无内容可执行的空模型喵。
	if len(plan.KeptCandidates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "virtual_model_share_nothing_importable", "message": "该分享码的候选在当前账号下均不可用，无法导入"})
		return
	}
	normalizedName, displayName, nameError := resolveVirtualModelShareImportName(c, input)
	if nameError != nil {
		switch {
		// 名字撞车：409 让前端提示用户换一个名字喵。
		case errors.Is(nameError, errVirtualModelShareNameConflict):
			c.JSON(http.StatusConflict, gin.H{"success": false, "code": "virtual_model_name_conflict", "message": "该模型名已被占用，请更换一个名称"})
		// 两个名字都是必填项，缺哪个就明确告诉用户缺哪个喵。
		case errors.Is(nameError, errVirtualModelShareNameRequired):
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "virtual_model_share_name_required", "message": "请填写模型标识"})
		case errors.Is(nameError, errVirtualModelShareDisplayNameRequired):
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "virtual_model_share_display_name_required", "message": "请填写模型显示名"})
		default:
			virtualModelShareInvalidRequest(c, nameError.Error())
		}
		return
	}
	createdModelID := 0
	transactionError := model.DB.Transaction(func(tx *gorm.DB) error {
		virtualModel := &model.VirtualModel{
			OwnerUserID:    c.GetInt("id"),
			NormalizedName: normalizedName,
			DisplayName:    displayName,
			// 喵~防御：导入的模型一律先停用，由用户核对凭据后再自行启用，避免"贴码即打上游"喵。
			Enabled:             false,
			LoopEnabled:         payload.LoopEnabled,
			TotalTimeoutSeconds: payload.TotalTimeoutSeconds,
			MaxLoopRounds:       payload.MaxLoopRounds,
			FakeStreamEnabled:   payload.FakeStreamEnabled,
			StreamCutAction:     model.VirtualModelFailureAction(payload.StreamCutAction),
			StreamCutRetries:    payload.StreamCutRetries,
			CreatedTime:         common.GetTimestamp(),
			UpdatedTime:         common.GetTimestamp(),
		}
		if validationError := model.ValidateVirtualModelConfiguration(virtualModel); validationError != nil {
			return validationError
		}
		if createError := tx.Create(virtualModel).Error; createError != nil {
			return createError
		}
		for _, keptCandidate := range plan.KeptCandidates {
			if candidateError := createImportedVirtualModelCandidate(tx, virtualModel.ID, keptCandidate); candidateError != nil {
				return candidateError
			}
		}
		for _, globalRule := range payload.GlobalFailureRules {
			globalFailureRule := &model.VirtualModelGlobalFailureRule{
				VirtualModelID:           virtualModel.ID,
				RuleOrder:                globalRule.RuleOrder,
				HTTPStatus:               globalRule.HTTPStatus,
				HTTPStatusMax:            globalRule.HTTPStatusMax,
				ErrorClass:               globalRule.ErrorClass,
				BodyRegex:                globalRule.BodyRegex,
				Action:                   globalRule.Action,
				FreezeSeconds:            globalRule.FreezeSeconds,
				FreezeField:              globalRule.FreezeField,
				FreezeUnit:               globalRule.FreezeUnit,
				StallTimeoutSeconds:      globalRule.StallTimeoutSeconds,
				MinContentChars:          globalRule.MinContentChars,
				ProbeTotalTimeoutSeconds: globalRule.ProbeTotalTimeoutSeconds,
				TimeoutSeconds:           globalRule.TimeoutSeconds,
				RetryCount:               globalRule.RetryCount,
			}
			if createError := tx.Create(globalFailureRule).Error; createError != nil {
				return createError
			}
		}
		// 审计只记录"谁导入了几号的分享码、落了几条候选"，不记录快照内容，符合审计表不可还原的设计喵。
		if auditError := tx.Create(&model.VirtualModelAuditLog{
			VirtualModelID: virtualModel.ID,
			OwnerUserID:    c.GetInt("id"),
			OperatorID:     c.GetInt("id"),
			Action:         "share_code_import",
			SummaryDigest:  fmt.Sprintf("share_code:%d;candidates:%d", shareCode.ID, len(plan.KeptCandidates)),
			CreatedTime:    common.GetTimestamp(),
		}).Error; auditError != nil {
			return auditError
		}
		// 喵~防御：导入计数与导入结果同事务提交，避免出现"方案已导入但计数没涨"的不一致喵。
		if incrementError := tx.Model(&model.VirtualModelShareCode{}).
			Where("id = ?", shareCode.ID).
			Update("import_count", gorm.Expr("import_count + 1")).Error; incrementError != nil {
			return incrementError
		}
		createdModelID = virtualModel.ID
		return nil
	})
	if transactionError != nil {
		virtualModelShareUnavailable(c, transactionError)
		return
	}
	common.ApiSuccess(c, gin.H{
		"id":                       createdModelID,
		"normalized_name":          normalizedName,
		"display_name":             displayName,
		"imported_candidate_count": len(plan.KeptCandidates),
		"skipped_candidates":       plan.SkippedCandidates,
		"warnings":                 plan.Warnings,
		// 喵~防御：导入的模型默认停用，响应里明确告诉前端"还需要用户手动启用"喵。
		"enabled": false,
	})
}

// createImportedVirtualModelCandidate 在事务内写入一个导入候选及其来源配置和失败规则喵。
func createImportedVirtualModelCandidate(tx *gorm.DB, virtualModelID int, shareCandidate model.VirtualModelShareCandidate) error {
	// 喵~防御：事务与归属必须完整，避免写入孤儿候选喵。
	if tx == nil || virtualModelID <= 0 {
		return errors.New("虚拟模型候选导入失败")
	}
	candidate := &model.VirtualModelCandidate{
		VirtualModelID:     virtualModelID,
		StableOrder:        shareCandidate.StableOrder,
		SourceType:         shareCandidate.SourceType,
		Enabled:            shareCandidate.Enabled,
		MaxRetries:         shareCandidate.MaxRetries,
		TimeoutSeconds:     shareCandidate.TimeoutSeconds,
		HedgeThreshold:     shareCandidate.HedgeThreshold,
		HedgeFreezeSeconds: shareCandidate.HedgeFreezeSeconds,
		CreatedTime:        common.GetTimestamp(),
		UpdatedTime:        common.GetTimestamp(),
	}
	if validationError := model.ValidateVirtualModelCandidate(candidate); validationError != nil {
		return validationError
	}
	if createError := tx.Create(candidate).Error; createError != nil {
		return createError
	}
	if shareCandidate.SourceType == model.VirtualModelSourceInternal {
		internalCandidate := &model.VirtualModelInternalCandidate{
			CandidateID:   candidate.ID,
			GroupName:     strings.TrimSpace(shareCandidate.GroupName),
			RealModelName: strings.TrimSpace(shareCandidate.RealModelName),
		}
		if createError := tx.Create(internalCandidate).Error; createError != nil {
			return createError
		}
	} else {
		// 喵~防御：导入方必须自行补填 API Key，因此导入的自定义候选一律停用，
		// 这样即使快照内容被篡改也不会在本实例上自动发起上游调用喵。
		candidate.Enabled = false
		if updateError := tx.Model(&model.VirtualModelCandidate{}).
			Where("id = ?", candidate.ID).
			Update("enabled", false).Error; updateError != nil {
			return updateError
		}
		parsedBaseURL, validateError := virtualmodelservice.ValidateCustomBaseURL(strings.TrimSpace(shareCandidate.BaseURL))
		if validateError != nil {
			return validateError
		}
		// 用本实例自己的主密钥重新加密一遍地址：快照里是明文，来源实例的密文对本实例本来就不可解喵。
		encryptedBaseURL, credentialVersion, encryptError := virtualmodelservice.EncryptCredential(parsedBaseURL.String())
		if encryptError != nil {
			return encryptError
		}
		normalizedAuthStyle, normalizeError := model.NormalizeVirtualModelAuthStyle(model.VirtualModelAuthStyle(shareCandidate.AuthStyle))
		if normalizeError != nil {
			return normalizeError
		}
		customCandidate := &model.VirtualModelCustomCandidate{
			CandidateID:        candidate.ID,
			EncryptedBaseURL:   encryptedBaseURL,
			CredentialVersion:  credentialVersion,
			BaseURLSummary:     virtualmodelservice.SummarizeCustomBaseURL(parsedBaseURL),
			BaseURLFingerprint: virtualmodelservice.CredentialFingerprint(parsedBaseURL.String()),
			RealModelName:      strings.TrimSpace(shareCandidate.RealModelName),
			AuthStyle:          normalizedAuthStyle,
			// 喵~防御：EncryptedAPIKey 与 APIKeyFingerprint 刻意留空，分享码里本来就没有凭据喵。
			EncryptedAPIKey:   "",
			APIKeyFingerprint: "",
			UpstreamModelID:   nil,
		}
		if createError := tx.Create(customCandidate).Error; createError != nil {
			return createError
		}
	}
	for _, shareRule := range shareCandidate.FailureRules {
		failureRule := &model.VirtualModelFailureRule{
			CandidateID:              candidate.ID,
			RuleOrder:                shareRule.RuleOrder,
			HTTPStatus:               shareRule.HTTPStatus,
			HTTPStatusMax:            shareRule.HTTPStatusMax,
			ErrorClass:               shareRule.ErrorClass,
			BodyRegex:                shareRule.BodyRegex,
			Action:                   shareRule.Action,
			FreezeSeconds:            shareRule.FreezeSeconds,
			FreezeField:              shareRule.FreezeField,
			FreezeUnit:               shareRule.FreezeUnit,
			StallTimeoutSeconds:      shareRule.StallTimeoutSeconds,
			MinContentChars:          shareRule.MinContentChars,
			ProbeTotalTimeoutSeconds: shareRule.ProbeTotalTimeoutSeconds,
			TimeoutSeconds:           shareRule.TimeoutSeconds,
			RetryCount:               shareRule.RetryCount,
		}
		if createError := tx.Create(failureRule).Error; createError != nil {
			return createError
		}
	}
	return nil
}

// errVirtualModelShareNameConflict 表示用户显式指定的模型名已被占用喵。
var errVirtualModelShareNameConflict = errors.New("virtual_model_share_name_conflict")

// errVirtualModelShareNameRequired 表示导入方没有填写模型名喵。
var errVirtualModelShareNameRequired = errors.New("virtual_model_share_name_required")

// errVirtualModelShareDisplayNameRequired 表示导入方没有填写显示名喵。
var errVirtualModelShareDisplayNameRequired = errors.New("virtual_model_share_display_name_required")

// resolveVirtualModelShareImportName 校验导入方自己填写的模型名与显示名喵。
// 两个名字都由导入方决定：模型名撞车直接报错让他换，不再自动追加序号喵。
// 校验规则与手工创建虚拟模型完全一致（同一个 NormalizeVirtualModelName 与同一条唯一索引）喵。
func resolveVirtualModelShareImportName(c *gin.Context, input virtualModelShareCodeImportInput) (string, string, error) {
	// 喵~防御：模型名必须由导入方填写，缺失时不能替他瞎猜一个名字喵。
	requestedName := strings.TrimSpace(input.NormalizedName)
	if requestedName == "" {
		return "", "", errVirtualModelShareNameRequired
	}
	// 喵~防御：显示名同样必填，避免导入出一批全部叫"导入方案"分不清谁是谁的模型喵。
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		return "", "", errVirtualModelShareDisplayNameRequired
	}
	normalizedName, normalizeError := model.NormalizeVirtualModelName(requestedName)
	if normalizeError != nil {
		return "", "", normalizeError
	}
	available, availabilityError := isVirtualModelNameAvailable(c.GetInt("id"), normalizedName)
	if availabilityError != nil {
		return "", "", availabilityError
	}
	if !available {
		return "", "", errVirtualModelShareNameConflict
	}
	return normalizedName, truncateVirtualModelShareDisplayName(displayName), nil
}

// isVirtualModelNameAvailable 判断某用户下该规范化名是否空闲喵。
func isVirtualModelNameAvailable(ownerUserID int, normalizedName string) (bool, error) {
	// 喵~防御：必须用 Unscoped 查询，因为软删除的行仍然占用 (owner, name) 唯一索引，
	// 只看未删除行会误判为可用，然后在插入时撞唯一索引报出难以理解的数据库错误喵。
	var existingCount int64
	countError := model.DB.Unscoped().Model(&model.VirtualModel{}).
		Where("owner_user_id = ? AND normalized_name = ?", ownerUserID, normalizedName).
		Count(&existingCount).Error
	if countError != nil {
		return false, countError
	}
	return existingCount == 0, nil
}

// truncateVirtualModelShareDisplayName 把显示名裁剪到模型允许的长度上限内喵。
func truncateVirtualModelShareDisplayName(displayName string) string {
	trimmedName := strings.TrimSpace(displayName)
	// 喵~防御：显示名不能为空，否则保存时会被校验拒绝喵。
	if trimmedName == "" {
		return "imported-plan"
	}
	// 主人注意：必须按字符（rune）截断而不是按字节，否则中文显示名会在第 128 字节被从中间切断，
	// 落库后就是一段非法 UTF-8，前端渲染出来是乱码问号喵。
	displayNameRunes := []rune(trimmedName)
	if len(displayNameRunes) > 128 {
		return string(displayNameRunes[:128])
	}
	return trimmedName
}
