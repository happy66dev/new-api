package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	virtualmodelservice "github.com/QuantumNous/new-api/service/virtualmodel"
	"github.com/gin-gonic/gin"
)

// upstreamModelInput 描述用户创建或更新上游模型时可提交的字段喵。
type upstreamModelInput struct {
	NormalizedName       string `json:"normalized_name"`
	DisplayName          string `json:"display_name"`
	Enabled              bool   `json:"enabled"`
	BaseURL              string `json:"base_url"`
	APIKey               string `json:"api_key"`
	RealModelName        string `json:"real_model_name"`
	AuthStyle            string `json:"auth_style"`
	ModelRatio           string `json:"model_ratio"`
	CompletionRatio      string `json:"completion_ratio"`
	CacheRatio           string `json:"cache_ratio"`
	CacheCreationRatio   string `json:"cache_creation_ratio"`
	CacheCreation5mRatio string `json:"cache_creation_5m_ratio"`
	CacheCreation1hRatio string `json:"cache_creation_1h_ratio"`
	ImageRatio           string `json:"image_ratio"`
	AudioRatio           string `json:"audio_ratio"`
	AudioCompletionRatio string `json:"audio_completion_ratio"`
	// 以下金额字段单位都是"分"（RMB）喵。
	BalanceCents           int64  `json:"balance_cents"`
	SpendLimitCents        int64  `json:"spend_limit_cents"`
	UpstreamRemainingCents int64  `json:"upstream_remaining_cents"`
	BalanceCheckEnabled    bool   `json:"balance_check_enabled"`
	BalanceCheckPath       string `json:"balance_check_path"`
	ShareEnabled           bool   `json:"share_enabled"`
	ShareLimitCents        int64  `json:"share_limit_cents"`
	ShowBalanceEnabled     bool   `json:"show_balance_enabled"`
	Version                int    `json:"version"`
}

// upstreamModelResponse 是控制面读取时可安全展示的脱敏响应喵。
type upstreamModelResponse struct {
	model.UserUpstreamModel
	// APIKeySet 标记该模型是否已配置上游密钥，绝不在响应中回显密钥喵。
	APIKeySet bool `json:"api_key_set"`
	// BaseURL 返回明文 base_url（非密钥）便于前端编辑展示喵。
	BaseURL string `json:"base_url"`
}

// upstreamModelNotFound 使用统一状态码和错误码阻止资源存在性泄露喵。
func upstreamModelNotFound(c *gin.Context) {
	// 喵~防御：不存在、越权和未授权资源使用完全相同的响应，避免用户枚举资源喵。
	c.JSON(http.StatusNotFound, gin.H{"success": false, "code": "upstream_model_not_found", "message": "用户上游模型不存在"})
}

// parseUpstreamModelID 解析并限制路径中的上游模型编号喵。
func parseUpstreamModelID(c *gin.Context) (int64, bool) {
	modelID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	// 喵~防御：非正数或解析失败统一按资源不存在处理，避免路径注入喵。
	if err != nil || modelID <= 0 {
		upstreamModelNotFound(c)
		return 0, false
	}
	return modelID, true
}

// loadOwnedUpstreamModel 加载并校验当前用户对资源的所有权喵。
func loadOwnedUpstreamModel(c *gin.Context, upstreamModelID int64) (*model.UserUpstreamModel, bool) {
	upstreamModel, err := model.GetUserUpstreamModelByOwnerID(upstreamModelID, c.GetInt("id"))
	// 喵~防御：不存在或越权统一按资源不存在处理，隐藏资源存在性喵。
	if err != nil {
		upstreamModelNotFound(c)
		return nil, false
	}
	return upstreamModel, true
}

// applyUpstreamModelCredentialDefaults 为空白倍率填充安全默认值，避免 P2 计费解析空字符串喵。
func applyUpstreamModelCredentialDefaults(upstreamModel *model.UserUpstreamModel) {
	// 各倍率字段为空时填充默认值：输出与各输入分类默认按 1 倍计价，ModelRatio 默认 0（未定价）喵。
	if upstreamModel.ModelRatio == "" {
		upstreamModel.ModelRatio = "0"
	}
	if upstreamModel.CompletionRatio == "" {
		upstreamModel.CompletionRatio = "1"
	}
	if upstreamModel.CacheRatio == "" {
		upstreamModel.CacheRatio = "1"
	}
	if upstreamModel.CacheCreationRatio == "" {
		upstreamModel.CacheCreationRatio = "1"
	}
	if upstreamModel.CacheCreation5mRatio == "" {
		upstreamModel.CacheCreation5mRatio = "1"
	}
	if upstreamModel.CacheCreation1hRatio == "" {
		upstreamModel.CacheCreation1hRatio = "1"
	}
	if upstreamModel.ImageRatio == "" {
		upstreamModel.ImageRatio = "1"
	}
	if upstreamModel.AudioRatio == "" {
		upstreamModel.AudioRatio = "1"
	}
	if upstreamModel.AudioCompletionRatio == "" {
		upstreamModel.AudioCompletionRatio = "1"
	}
}

// saveUpstreamModelFields 将用户输入转换为受约束的上游模型配置喵。
func saveUpstreamModelFields(input upstreamModelInput, ownerUserID int, existing *model.UserUpstreamModel) error {
	// 喵~防御：客户端提交的 owner 字段永远被忽略，所有者只来自认证上下文喵。
	normalizedName, err := model.NormalizeUserUpstreamModelName(input.NormalizedName)
	if err != nil {
		return err
	}
	// 喵~防御：更新必须比对版本，避免过期客户端覆盖新配置喵。
	if existing.ID > 0 && input.Version != existing.Version {
		return errors.New("upstream_model_version_conflict")
	}
	// 创建时新模型版本从 1 开始，更新时递增喵。
	if existing.ID == 0 {
		existing.Version = 1
	} else {
		existing.Version++
	}
	existing.OwnerUserID = ownerUserID
	existing.NormalizedName = normalizedName
	existing.DisplayName = input.DisplayName
	existing.Enabled = input.Enabled
	existing.RealModelName = input.RealModelName
	// 喵~防御：真实模型名必填，避免无模型名的上游请求喵。
	if existing.RealModelName == "" {
		return errors.New("真实模型名不能为空")
	}
	// 认证方式规范化，未知方式直接拒绝喵。
	authStyle, authError := model.NormalizeVirtualModelAuthStyle(model.VirtualModelAuthStyle(input.AuthStyle))
	if authError != nil {
		return authError
	}
	existing.AuthStyle = string(authStyle)
	// base_url 非空时加密更新；编辑留空表示保留原有配置喵。
	if input.BaseURL != "" {
		parsedURL, urlError := virtualmodelservice.ValidateCustomBaseURL(input.BaseURL)
		if urlError != nil {
			return urlError
		}
		encryptedBaseURL, credentialVersion, encryptError := virtualmodelservice.EncryptCredential(parsedURL.String())
		if encryptError != nil {
			return encryptError
		}
		existing.EncryptedBaseURL = encryptedBaseURL
		existing.CredentialVersion = credentialVersion
		existing.BaseURLFingerprint = virtualmodelservice.CredentialFingerprint(parsedURL.String())
	}
	// api_key 非空时加密更新；编辑留空表示保留原有密钥喵。
	if input.APIKey != "" {
		encryptedAPIKey, credentialVersion, encryptError := virtualmodelservice.EncryptCredential(input.APIKey)
		if encryptError != nil {
			return encryptError
		}
		existing.EncryptedAPIKey = encryptedAPIKey
		existing.CredentialVersion = credentialVersion
		existing.APIKeyFingerprint = virtualmodelservice.CredentialFingerprint(input.APIKey)
	}
	// 喵~防御：创建时必须同时具备 base_url 与 api_key，否则请求无法到达上游喵。
	if existing.ID == 0 && (existing.EncryptedBaseURL == "" || existing.EncryptedAPIKey == "") {
		return errors.New("上游地址与密钥不能为空")
	}
	existing.ModelRatio = input.ModelRatio
	existing.CompletionRatio = input.CompletionRatio
	existing.CacheRatio = input.CacheRatio
	existing.CacheCreationRatio = input.CacheCreationRatio
	existing.CacheCreation5mRatio = input.CacheCreation5mRatio
	existing.CacheCreation1hRatio = input.CacheCreation1hRatio
	existing.ImageRatio = input.ImageRatio
	existing.AudioRatio = input.AudioRatio
	existing.AudioCompletionRatio = input.AudioCompletionRatio
	applyUpstreamModelCredentialDefaults(existing)
	existing.BalanceCents = input.BalanceCents
	existing.SpendLimitCents = input.SpendLimitCents
	existing.UpstreamRemainingCents = input.UpstreamRemainingCents
	existing.BalanceCheckEnabled = input.BalanceCheckEnabled
	existing.BalanceCheckPath = input.BalanceCheckPath
	existing.ShareEnabled = input.ShareEnabled
	existing.ShareLimitCents = input.ShareLimitCents
	existing.ShowBalanceEnabled = input.ShowBalanceEnabled
	if existing.CreatedTime == 0 {
		existing.CreatedTime = common.GetTimestamp()
	}
	existing.UpdatedTime = common.GetTimestamp()
	return nil
}

// buildUpstreamModelResponse 构造脱敏响应，解密 base_url 但不回显 api_key 喵。
func buildUpstreamModelResponse(upstreamModel *model.UserUpstreamModel) (*upstreamModelResponse, error) {
	// 喵~防御：空对象不构造响应喵。
	if upstreamModel == nil {
		return nil, errors.New("用户上游模型不存在")
	}
	response := &upstreamModelResponse{UserUpstreamModel: *upstreamModel}
	// 喵~防御：凭据解密失败不影响列表展示，base_url 降级为空字符串喵。
	if response.EncryptedBaseURL != "" {
		if decryptedBaseURL, decryptError := virtualmodelservice.DecryptCredential(response.EncryptedBaseURL, response.CredentialVersion); decryptError == nil {
			response.BaseURL = decryptedBaseURL
		}
	}
	response.APIKeySet = response.EncryptedAPIKey != ""
	return response, nil
}

// GetUserUpstreamModels 返回当前登录用户拥有的全部上游模型喵。
func GetUserUpstreamModels(c *gin.Context) {
	upstreamModels, err := model.GetUserUpstreamModelsByOwner(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	responses := make([]*upstreamModelResponse, 0, len(upstreamModels))
	for index := range upstreamModels {
		response, buildError := buildUpstreamModelResponse(&upstreamModels[index])
		if buildError != nil {
			common.ApiError(c, buildError)
			return
		}
		responses = append(responses, response)
	}
	common.ApiSuccess(c, responses)
}

// CreateUserUpstreamModel 创建新的用户上游模型喵。
func CreateUserUpstreamModel(c *gin.Context) {
	var input upstreamModelInput
	if err := c.ShouldBindJSON(&input); err != nil {
		common.ApiError(c, err)
		return
	}
	upstreamModel := &model.UserUpstreamModel{}
	if err := saveUpstreamModelFields(input, c.GetInt("id"), upstreamModel); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DB.Create(upstreamModel).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	response, err := buildUpstreamModelResponse(upstreamModel)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, response)
}

// GetUserUpstreamModel 返回单个用户上游模型喵。
func GetUserUpstreamModel(c *gin.Context) {
	upstreamModelID, ok := parseUpstreamModelID(c)
	if !ok {
		return
	}
	upstreamModel, ok := loadOwnedUpstreamModel(c, upstreamModelID)
	if !ok {
		return
	}
	response, err := buildUpstreamModelResponse(upstreamModel)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, response)
}

// UpdateUserUpstreamModel 更新用户上游模型配置喵。
func UpdateUserUpstreamModel(c *gin.Context) {
	upstreamModelID, ok := parseUpstreamModelID(c)
	if !ok {
		return
	}
	upstreamModel, ok := loadOwnedUpstreamModel(c, upstreamModelID)
	if !ok {
		return
	}
	var input upstreamModelInput
	if err := c.ShouldBindJSON(&input); err != nil {
		common.ApiError(c, err)
		return
	}
	// 喵~防御：更新必须携带读取版本，缺失版本视为无效请求喵。
	if input.Version <= 0 {
		common.ApiError(c, errors.New("用户上游模型版本无效"))
		return
	}
	// 在覆盖输入字段前保存数据库版本，作为乐观锁 WHERE 条件的基准喵。
	existingVersion := upstreamModel.Version
	if err := saveUpstreamModelFields(input, c.GetInt("id"), upstreamModel); err != nil {
		if err.Error() == "upstream_model_version_conflict" {
			c.JSON(http.StatusConflict, gin.H{"success": false, "code": "upstream_model_version_conflict", "message": "用户上游模型已被其他请求修改"})
			return
		}
		common.ApiError(c, err)
		return
	}
	// 更新条件绑定旧版本号，保证并发写只有一个请求成功，其余命中零行更新喵。
	updateResult := model.DB.Model(upstreamModel).Where("id = ? AND owner_user_id = ? AND version = ?", upstreamModelID, c.GetInt("id"), existingVersion).Select("normalized_name", "display_name", "enabled", "encrypted_base_url", "encrypted_api_key", "base_url_fingerprint", "api_key_fingerprint", "credential_version", "real_model_name", "auth_style", "model_ratio", "completion_ratio", "cache_ratio", "cache_creation_ratio", "cache_creation_5m_ratio", "cache_creation_1h_ratio", "image_ratio", "audio_ratio", "audio_completion_ratio", "balance_cents", "spend_limit_cents", "total_spent_cents", "upstream_remaining_cents", "upstream_remaining_at", "balance_check_enabled", "balance_check_path", "share_enabled", "share_limit_cents", "share_spent_cents", "show_balance_enabled", "version", "updated_time").Updates(upstreamModel)
	if updateResult.Error != nil {
		common.ApiError(c, updateResult.Error)
		return
	}
	// 喵~防御：零行更新意味着并发请求已改变版本，不能把过期写入伪装为成功喵。
	if updateResult.RowsAffected != 1 {
		c.JSON(http.StatusConflict, gin.H{"success": false, "code": "upstream_model_version_conflict", "message": "用户上游模型已被其他请求修改"})
		return
	}
	response, err := buildUpstreamModelResponse(upstreamModel)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, response)
}

// DeleteUserUpstreamModel 删除用户上游模型喵。
func DeleteUserUpstreamModel(c *gin.Context) {
	upstreamModelID, ok := parseUpstreamModelID(c)
	if !ok {
		return
	}
	upstreamModel, ok := loadOwnedUpstreamModel(c, upstreamModelID)
	if !ok {
		return
	}
	var input upstreamModelDeleteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		common.ApiError(c, err)
		return
	}
	// 喵~防御：删除也必须比对版本，避免一个过期页面撤销其他配置修改喵。
	if input.Version <= 0 {
		common.ApiError(c, errors.New("用户上游模型版本无效"))
		return
	}
	if input.Version != upstreamModel.Version {
		c.JSON(http.StatusConflict, gin.H{"success": false, "code": "upstream_model_version_conflict", "message": "用户上游模型已被其他请求修改"})
		return
	}
	if err := model.DeleteUserUpstreamModelByOwnerWithVersion(upstreamModelID, c.GetInt("id"), input.Version); err != nil {
		upstreamModelNotFound(c)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"id": upstreamModelID}})
}

// upstreamModelDeleteInput 描述带版本保护的删除请求喵。
type upstreamModelDeleteInput struct {
	Version int `json:"version"`
}
