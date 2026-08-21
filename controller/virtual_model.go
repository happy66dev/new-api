package controller

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	virtualmodelservice "github.com/QuantumNous/new-api/service/virtualmodel"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// virtualModelInput 描述用户创建或更新虚拟模型时可以提交的字段喵。
type virtualModelInput struct {
	OwnerUserID         int    `json:"owner_user_id"`
	NormalizedName      string `json:"normalized_name"`
	DisplayName         string `json:"display_name"`
	Enabled             bool   `json:"enabled"`
	LoopEnabled         bool   `json:"loop_enabled"`
	TotalTimeoutSeconds int    `json:"total_timeout_seconds"`
	MaxLoopRounds       int    `json:"max_loop_rounds"`
	Version             int64  `json:"version"`
}

// virtualModelCandidateInput 描述候选链编辑需要的非敏感字段喵。
type virtualModelCandidateInput struct {
	ID             int                          `json:"id"`
	StableOrder    int                          `json:"stable_order"`
	SourceType     model.VirtualModelSourceType `json:"source_type"`
	Enabled        bool                         `json:"enabled"`
	MaxRetries     int                          `json:"max_retries"`
	TimeoutSeconds int                          `json:"timeout_seconds"`
	GroupName      string                       `json:"group_name"`
	RealModelName  string                       `json:"real_model_name"`
	BaseURL        string                       `json:"base_url"`
	APIKey         string                       `json:"api_key"`
	AuthStyle      model.VirtualModelAuthStyle  `json:"auth_style"`
}

// virtualModelBindingInput 描述当前用户 API Key 的授权关系喵。
type virtualModelBindingInput struct {
	TokenIDs []int `json:"token_ids"`
	Version  int64 `json:"version"`
}

// virtualModelNotFound 使用统一状态码和错误码阻止资源存在性泄露喵。
func virtualModelNotFound(c *gin.Context) {
	// 喵~防御：不存在、越权和未授权资源使用完全相同的响应，避免用户枚举资源喵。
	c.JSON(http.StatusNotFound, gin.H{"success": false, "code": "virtual_model_not_found", "message": "虚拟模型不存在"})
}

// parseVirtualModelID 解析并限制路径中的模型编号喵。
func parseVirtualModelID(c *gin.Context) (int, bool) {
	modelID, err := strconv.Atoi(c.Param("id"))
	if err != nil || modelID <= 0 {
		virtualModelNotFound(c)
		return 0, false
	}
	return modelID, true
}

// loadOwnedVirtualModel 只使用认证用户条件读取资源喵。
func loadOwnedVirtualModel(c *gin.Context, modelID int) (*model.VirtualModel, bool) {
	virtualModel := &model.VirtualModel{}
	if err := model.DB.Where("id = ? AND owner_user_id = ?", modelID, c.GetInt("id")).First(virtualModel).Error; err != nil {
		virtualModelNotFound(c)
		return nil, false
	}
	return virtualModel, true
}

// virtualModelResponse 返回脱敏的主模型配置和候选链喵。
type virtualModelResponse struct {
	*model.VirtualModel
	Candidates []virtualModelCandidateInput `json:"candidates"`
	Bindings   []int                        `json:"binding_token_ids"`
}

// buildVirtualModelResponse 读取关联数据并只输出允许公开的字段喵。
func buildVirtualModelResponse(virtualModel *model.VirtualModel) (*virtualModelResponse, error) {
	if virtualModel == nil {
		return nil, errors.New("虚拟模型为空")
	}
	var candidates []model.VirtualModelCandidate
	if err := model.DB.Where("virtual_model_id = ?", virtualModel.ID).Order("stable_order asc").Find(&candidates).Error; err != nil {
		return nil, err
	}
	candidateResponses := make([]virtualModelCandidateInput, 0, len(candidates))
	for _, candidate := range candidates {
		candidateResponse := virtualModelCandidateInput{
			ID: candidate.ID, StableOrder: candidate.StableOrder, SourceType: candidate.SourceType,
			Enabled: candidate.Enabled, MaxRetries: candidate.MaxRetries, TimeoutSeconds: candidate.TimeoutSeconds,
		}
		if candidate.SourceType == model.VirtualModelSourceInternal {
			var internalCandidate model.VirtualModelInternalCandidate
			if err := model.DB.Where("candidate_id = ?", candidate.ID).First(&internalCandidate).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			}
			candidateResponse.GroupName = internalCandidate.GroupName
			candidateResponse.RealModelName = internalCandidate.RealModelName
		}
		if candidate.SourceType == model.VirtualModelSourceCustom {
			var customCandidate model.VirtualModelCustomCandidate
			if err := model.DB.Where("candidate_id = ?", candidate.ID).First(&customCandidate).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			}
			candidateResponse.RealModelName = customCandidate.RealModelName
			candidateResponse.BaseURL = customCandidate.BaseURLSummary
			candidateResponse.AuthStyle = customCandidate.AuthStyle
		}
		candidateResponses = append(candidateResponses, candidateResponse)
	}
	var bindings []model.VirtualModelTokenBinding
	if err := model.DB.Where("virtual_model_id = ? AND owner_user_id = ?", virtualModel.ID, virtualModel.OwnerUserID).Find(&bindings).Error; err != nil {
		return nil, err
	}
	bindingTokenIDs := make([]int, 0, len(bindings))
	for _, binding := range bindings {
		bindingTokenIDs = append(bindingTokenIDs, binding.TokenID)
	}
	return &virtualModelResponse{VirtualModel: virtualModel, Candidates: candidateResponses, Bindings: bindingTokenIDs}, nil
}

// GetVirtualModels 返回当前登录用户拥有的全部虚拟模型喵。
func GetVirtualModels(c *gin.Context) {
	var virtualModels []model.VirtualModel
	if err := model.DB.Where("owner_user_id = ?", c.GetInt("id")).Order("id desc").Find(&virtualModels).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	responses := make([]*virtualModelResponse, 0, len(virtualModels))
	for index := range virtualModels {
		response, err := buildVirtualModelResponse(&virtualModels[index])
		if err != nil {
			common.ApiError(c, err)
			return
		}
		responses = append(responses, response)
	}
	common.ApiSuccess(c, responses)
}

// GetVirtualModel 返回当前登录用户拥有的单个虚拟模型喵。
func GetVirtualModel(c *gin.Context) {
	modelID, ok := parseVirtualModelID(c)
	if !ok {
		return
	}
	virtualModel, ok := loadOwnedVirtualModel(c, modelID)
	if !ok {
		return
	}
	response, err := buildVirtualModelResponse(virtualModel)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, response)
}

// saveVirtualModelFields 将用户输入转换为受约束的主模型配置喵。
func saveVirtualModelFields(input virtualModelInput, ownerUserID int, existing *model.VirtualModel) error {
	// 喵~防御：客户端提交的 owner 字段永远被忽略，所有者只来自认证上下文喵。
	normalizedName, err := model.NormalizeVirtualModelName(input.NormalizedName)
	if err != nil {
		return err
	}
	if existing != nil && input.Version != 0 && input.Version != existing.Version {
		return fmt.Errorf("virtual_model_version_conflict")
	}
	existing.OwnerUserID = ownerUserID
	existing.NormalizedName = normalizedName
	existing.DisplayName = strings.TrimSpace(input.DisplayName)
	existing.Enabled = input.Enabled
	existing.LoopEnabled = input.LoopEnabled
	existing.TotalTimeoutSeconds = input.TotalTimeoutSeconds
	existing.MaxLoopRounds = input.MaxLoopRounds
	if existing.TotalTimeoutSeconds == 0 {
		existing.TotalTimeoutSeconds = 120
	}
	if existing.MaxLoopRounds == 0 {
		existing.MaxLoopRounds = 1
	}
	if existing.Version == 0 {
		existing.Version = 1
	}
	if existing.CreatedTime == 0 {
		existing.CreatedTime = common.GetTimestamp()
	}
	existing.UpdatedTime = common.GetTimestamp()
	// 喵~防御：更新必须推进版本号，使后续过期写入命中乐观锁冲突喵。
	if existing.ID > 0 {
		existing.Version++
	}
	return model.ValidateVirtualModelConfiguration(existing)
}

// CreateVirtualModel 创建默认没有 API Key 授权的虚拟模型喵。
func CreateVirtualModel(c *gin.Context) {
	var input virtualModelInput
	if err := c.ShouldBindJSON(&input); err != nil {
		common.ApiError(c, err)
		return
	}
	virtualModel := &model.VirtualModel{}
	if err := saveVirtualModelFields(input, c.GetInt("id"), virtualModel); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DB.Create(virtualModel).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	response, err := buildVirtualModelResponse(virtualModel)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, response)
}

// UpdateVirtualModel 更新主模型配置并保留原有候选与授权关系喵。
func UpdateVirtualModel(c *gin.Context) {
	modelID, ok := parseVirtualModelID(c)
	if !ok {
		return
	}
	virtualModel, ok := loadOwnedVirtualModel(c, modelID)
	if !ok {
		return
	}
	var input virtualModelInput
	if err := c.ShouldBindJSON(&input); err != nil {
		common.ApiError(c, err)
		return
	}
	// 喵~防御：在覆盖输入字段前保存数据库版本，避免乐观锁条件被请求体篡改喵。
	existingVersion := virtualModel.Version
	if input.Version != 0 && input.Version != existingVersion {
		c.JSON(http.StatusConflict, gin.H{"success": false, "code": "virtual_model_version_conflict", "message": "虚拟模型已被其他请求修改"})
		return
	}
	if err := saveVirtualModelFields(input, c.GetInt("id"), virtualModel); err != nil {
		if err.Error() == "virtual_model_version_conflict" {
			c.JSON(http.StatusConflict, gin.H{"success": false, "code": "virtual_model_version_conflict", "message": "虚拟模型已被其他请求修改"})
			return
		}
		common.ApiError(c, err)
		return
	}
	updateResult := model.DB.Model(virtualModel).Where("id = ? AND owner_user_id = ? AND version = ?", modelID, c.GetInt("id"), existingVersion).Select("normalized_name", "display_name", "enabled", "loop_enabled", "total_timeout_seconds", "max_loop_rounds", "version", "updated_time").Updates(virtualModel)
	if updateResult.Error != nil {
		common.ApiError(c, updateResult.Error)
		return
	}
	// 喵~防御：零行更新意味着并发请求已改变版本，不能把过期写入伪装为成功喵。
	if updateResult.RowsAffected != 1 {
		c.JSON(http.StatusConflict, gin.H{"success": false, "code": "virtual_model_version_conflict", "message": "虚拟模型已被其他请求修改"})
		return
	}
	response, err := buildVirtualModelResponse(virtualModel)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, response)
}

// DeleteVirtualModel 删除资源及其关联配置并保留最小审计摘要喵。
func DeleteVirtualModel(c *gin.Context) {
	modelID, ok := parseVirtualModelID(c)
	if !ok {
		return
	}
	if _, ok := loadOwnedVirtualModel(c, modelID); !ok {
		return
	}
	if err := model.DeleteVirtualModelByOwner(modelID, c.GetInt("id"), c.GetInt("id")); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			virtualModelNotFound(c)
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"id": modelID})
}

// ReplaceVirtualModelCandidates 原子替换候选链，目前只接受无敏感信息的内部候选喵。
func ReplaceVirtualModelCandidates(c *gin.Context) {
	modelID, ok := parseVirtualModelID(c)
	if !ok {
		return
	}
	virtualModel, ok := loadOwnedVirtualModel(c, modelID)
	if !ok {
		return
	}
	var inputs []virtualModelCandidateInput
	if err := c.ShouldBindJSON(&inputs); err != nil {
		common.ApiError(c, err)
		return
	}
	if len(inputs) == 0 || len(inputs) > 32 {
		common.ApiError(c, errors.New("候选链长度必须介于 1 和 32 之间"))
		return
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		// 喵~防御：先通过版本条件锁定主模型，防止并发替换先删除旧链再发现版本冲突喵。
		updateResult := tx.Model(&model.VirtualModel{}).Where("id = ? AND owner_user_id = ? AND version = ?", modelID, c.GetInt("id"), virtualModel.Version).Updates(map[string]any{"version": virtualModel.Version + 1, "updated_time": common.GetTimestamp()})
		if updateResult.Error != nil {
			return updateResult.Error
		}
		if updateResult.RowsAffected != 1 {
			return errors.New("virtual_model_version_conflict")
		}
		var oldCandidateIDs []int
		if err := tx.Model(&model.VirtualModelCandidate{}).Where("virtual_model_id = ?", modelID).Pluck("id", &oldCandidateIDs).Error; err != nil {
			return err
		}
		if len(oldCandidateIDs) > 0 {
			if err := tx.Where("candidate_id IN ?", oldCandidateIDs).Delete(&model.VirtualModelFailureRule{}).Error; err != nil {
				return err
			}
			if err := tx.Where("candidate_id IN ?", oldCandidateIDs).Delete(&model.VirtualModelInternalCandidate{}).Error; err != nil {
				return err
			}
			if err := tx.Where("candidate_id IN ?", oldCandidateIDs).Delete(&model.VirtualModelCustomCandidate{}).Error; err != nil {
				return err
			}
			if err := tx.Where("candidate_id IN ?", oldCandidateIDs).Delete(&model.VirtualModelManualFreeze{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("virtual_model_id = ?", modelID).Delete(&model.VirtualModelCandidate{}).Error; err != nil {
			return err
		}
		for index, input := range inputs {
			candidate := &model.VirtualModelCandidate{VirtualModelID: modelID, StableOrder: index, SourceType: input.SourceType, Enabled: input.Enabled, MaxRetries: input.MaxRetries, TimeoutSeconds: input.TimeoutSeconds, Version: 1, CreatedTime: common.GetTimestamp(), UpdatedTime: common.GetTimestamp()}
			if err := model.ValidateVirtualModelCandidate(candidate); err != nil {
				return err
			}
			if input.SourceType == model.VirtualModelSourceInternal && (strings.TrimSpace(input.GroupName) == "" || strings.TrimSpace(input.RealModelName) == "") {
				return errors.New("内部候选必须提供分组和真实模型")
			}
			if input.SourceType == model.VirtualModelSourceCustom && (strings.TrimSpace(input.BaseURL) == "" || strings.TrimSpace(input.APIKey) == "" || strings.TrimSpace(input.RealModelName) == "") {
				return errors.New("自定义候选必须提供地址、凭据和真实模型")
			}
			if err := tx.Create(candidate).Error; err != nil {
				return err
			}
			if candidate.SourceType == model.VirtualModelSourceInternal {
				internalCandidate := &model.VirtualModelInternalCandidate{CandidateID: candidate.ID, GroupName: strings.TrimSpace(input.GroupName), RealModelName: strings.TrimSpace(input.RealModelName)}
				if err := tx.Create(internalCandidate).Error; err != nil {
					return err
				}
			}
			if candidate.SourceType == model.VirtualModelSourceCustom {
				parsedURL, validateURLError := virtualmodelservice.ValidateCustomBaseURL(input.BaseURL)
				if validateURLError != nil {
					return validateURLError
				}
				encryptedBaseURL, credentialVersion, encryptBaseURLError := virtualmodelservice.EncryptCredential(parsedURL.String())
				if encryptBaseURLError != nil {
					return encryptBaseURLError
				}
				encryptedAPIKey, _, encryptAPIKeyError := virtualmodelservice.EncryptCredential(strings.TrimSpace(input.APIKey))
				if encryptAPIKeyError != nil {
					return encryptAPIKeyError
				}
				if input.AuthStyle != model.VirtualModelAuthBearer && input.AuthStyle != model.VirtualModelAuthAPIKey && input.AuthStyle != model.VirtualModelAuthAnthropic {
					return errors.New("自定义候选认证方式无效")
				}
				customCandidate := &model.VirtualModelCustomCandidate{CandidateID: candidate.ID, EncryptedBaseURL: encryptedBaseURL, EncryptedAPIKey: encryptedAPIKey, CredentialVersion: credentialVersion, BaseURLSummary: virtualmodelservice.SummarizeCustomBaseURL(parsedURL), APIKeyFingerprint: virtualmodelservice.CredentialFingerprint(strings.TrimSpace(input.APIKey)), RealModelName: strings.TrimSpace(input.RealModelName), AuthStyle: input.AuthStyle}
				if err := tx.Create(customCandidate).Error; err != nil {
					return err
				}
			}
		}
		virtualModel.Version++
		virtualModel.UpdatedTime = common.GetTimestamp()
		return nil
	}); err != nil {
		if err.Error() == "virtual_model_version_conflict" {
			c.JSON(http.StatusConflict, gin.H{"success": false, "code": "virtual_model_version_conflict", "message": "虚拟模型已被其他请求修改"})
			return
		}
		common.ApiError(c, err)
		return
	}
	response, err := buildVirtualModelResponse(virtualModel)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, response)
}

// ReplaceVirtualModelBindings 原子替换当前用户 API Key 授权关系喵。
func ReplaceVirtualModelBindings(c *gin.Context) {
	modelID, ok := parseVirtualModelID(c)
	if !ok {
		return
	}
	virtualModel, ok := loadOwnedVirtualModel(c, modelID)
	if !ok {
		return
	}
	var input virtualModelBindingInput
	if err := c.ShouldBindJSON(&input); err != nil {
		common.ApiError(c, err)
		return
	}
	seenTokenIDs := make(map[int]struct{}, len(input.TokenIDs))
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("virtual_model_id = ? AND owner_user_id = ?", modelID, c.GetInt("id")).Delete(&model.VirtualModelTokenBinding{}).Error; err != nil {
			return err
		}
		for _, tokenID := range input.TokenIDs {
			if tokenID <= 0 {
				return errors.New("API Key ID 无效")
			}
			if _, exists := seenTokenIDs[tokenID]; exists {
				return errors.New("API Key 授权关系重复")
			}
			seenTokenIDs[tokenID] = struct{}{}
			var token model.Token
			if err := tx.Where("id = ? AND user_id = ?", tokenID, c.GetInt("id")).First(&token).Error; err != nil {
				return errors.New("API Key 不存在")
			}
			if err := tx.Create(&model.VirtualModelTokenBinding{VirtualModelID: modelID, TokenID: tokenID, OwnerUserID: c.GetInt("id"), CreatedTime: common.GetTimestamp()}).Error; err != nil {
				return err
			}
		}
		virtualModel.Version++
		virtualModel.UpdatedTime = common.GetTimestamp()
		return tx.Model(virtualModel).Where("id = ? AND owner_user_id = ? AND version = ?", modelID, c.GetInt("id"), virtualModel.Version-1).Select("version", "updated_time").Updates(virtualModel).Error
	}); err != nil {
		common.ApiError(c, err)
		return
	}
	response, err := buildVirtualModelResponse(virtualModel)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, response)
}

// FreezeVirtualModelCandidate 手动冻结当前用户拥有模型中的候选喵。
func FreezeVirtualModelCandidate(c *gin.Context) {
	modelID, ok := parseVirtualModelID(c)
	if !ok {
		return
	}
	if _, ok := loadOwnedVirtualModel(c, modelID); !ok {
		return
	}
	candidateID, parseError := strconv.Atoi(c.Param("candidateId"))
	if parseError != nil || candidateID <= 0 {
		virtualModelNotFound(c)
		return
	}
	var candidate model.VirtualModelCandidate
	if err := model.DB.Where("id = ? AND virtual_model_id = ?", candidateID, modelID).First(&candidate).Error; err != nil {
		virtualModelNotFound(c)
		return
	}
	var input struct {
		ExpiresAt int64 `json:"expires_at"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		common.ApiError(c, err)
		return
	}
	if input.ExpiresAt <= common.GetTimestamp() {
		common.ApiError(c, errors.New("冻结到期时间必须晚于当前时间"))
		return
	}
	freeze := &model.VirtualModelManualFreeze{CandidateID: candidate.ID, OperatorID: c.GetInt("id"), StartedAt: common.GetTimestamp(), ExpiresAt: input.ExpiresAt}
	if err := model.DB.Create(freeze).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"candidate_id": candidateID, "expires_at": input.ExpiresAt})
}

// UnfreezeVirtualModelCandidate 解除当前用户模型候选的所有有效手动冻结喵。
func UnfreezeVirtualModelCandidate(c *gin.Context) {
	modelID, ok := parseVirtualModelID(c)
	if !ok {
		return
	}
	if _, ok := loadOwnedVirtualModel(c, modelID); !ok {
		return
	}
	candidateID, parseError := strconv.Atoi(c.Param("candidateId"))
	if parseError != nil || candidateID <= 0 {
		virtualModelNotFound(c)
		return
	}
	var candidate model.VirtualModelCandidate
	if err := model.DB.Where("id = ? AND virtual_model_id = ?", candidateID, modelID).First(&candidate).Error; err != nil {
		virtualModelNotFound(c)
		return
	}
	if err := model.DB.Where("candidate_id = ?", candidate.ID).Delete(&model.VirtualModelManualFreeze{}).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"candidate_id": candidateID})
}

// GetVirtualModelAuditLog 返回当前用户模型的脱敏操作摘要喵。
func GetVirtualModelAuditLog(c *gin.Context) {
	modelID, ok := parseVirtualModelID(c)
	if !ok {
		return
	}
	if _, ok := loadOwnedVirtualModel(c, modelID); !ok {
		return
	}
	var auditLogs []model.VirtualModelAuditLog
	if err := model.DB.Where("virtual_model_id = ? AND owner_user_id = ?", modelID, c.GetInt("id")).Order("created_time desc").Limit(100).Find(&auditLogs).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, auditLogs)
}

func GetVirtualModelStatus(c *gin.Context) {
	modelID, ok := parseVirtualModelID(c)
	if !ok {
		return
	}
	virtualModel, ok := loadOwnedVirtualModel(c, modelID)
	if !ok {
		return
	}
	var candidates []model.VirtualModelCandidate
	if err := model.DB.Where("virtual_model_id = ?", virtualModel.ID).Order("stable_order asc").Find(&candidates).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"model": virtualModel.VirtualModelName(), "enabled": virtualModel.Enabled, "candidate_count": len(candidates), "available_candidates": countEnabledVirtualCandidates(candidates)})
}

// countEnabledVirtualCandidates 统计当前配置中启用的候选数量喵。
func countEnabledVirtualCandidates(candidates []model.VirtualModelCandidate) int {
	count := 0
	for _, candidate := range candidates {
		if candidate.Enabled {
			count++
		}
	}
	return count
}

// sortVirtualModelCandidates 保证后续状态和响应的候选顺序稳定喵。
func sortVirtualModelCandidates(candidates []model.VirtualModelCandidate) {
	sort.SliceStable(candidates, func(leftIndex, rightIndex int) bool {
		return candidates[leftIndex].StableOrder < candidates[rightIndex].StableOrder
	})
}
