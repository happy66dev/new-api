package controller

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
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
	// 流转伪流：开启后上游流式全量缓存到 [DONE] 再一次性伪流发出，断流按处理措施决策喵。
	FakeStreamEnabled bool   `json:"fake_stream_enabled"`
	StreamCutAction   string `json:"stream_cut_action"`
	StreamCutRetries  int    `json:"stream_cut_retries"`
	Version           int64  `json:"version"`
}

// virtualModelCandidateInput 描述候选链编辑需要的非敏感字段喵。
type virtualModelCandidateInput struct {
	ID             int                          `json:"id"`
	StableOrder    int                          `json:"stable_order"`
	SourceType     model.VirtualModelSourceType `json:"source_type"`
	Enabled        bool                         `json:"enabled"`
	MaxRetries     int                          `json:"max_retries"`
	TimeoutSeconds int                          `json:"timeout_seconds"`
	// HedgeThreshold 连续失败自动避险阈值，达到该次数才冻结退避；零表示关闭自动避险喵。
	HedgeThreshold int `json:"hedge_threshold"`
	// HedgeFreezeSeconds 达到连续失败阈值后的退避冻结秒数；阈值非零时必填且必须为正数喵。
	HedgeFreezeSeconds int                         `json:"hedge_freeze_seconds"`
	GroupName          string                      `json:"group_name"`
	RealModelName      string                      `json:"real_model_name"`
	BaseURL            string                      `json:"base_url"`
	APIKey             string                      `json:"api_key"`
	AuthStyle          model.VirtualModelAuthStyle `json:"auth_style"`
	// UpstreamModelID 引用用户上游模型条目，非空时凭据与真实模型名以该条目为准喵。
	UpstreamModelID *int64 `json:"upstream_model_id,omitempty"`
	// FrozenUntil 当前手动冻结到期时间（Unix 秒），未冻结时为零，供调用链页面展示已冻结状态喵。
	FrozenUntil int64 `json:"frozen_until,omitempty"`
	// 喵~防御：FailureRules 必须使用真实模型类型而不是 DTO，否则 GORM 会按结构体名生成 virtual_model_failure_rule_inputs 表名导致查询报 no such table。
	FailureRules []model.VirtualModelFailureRule `json:"failure_rules,omitempty"`
}

// virtualModelCandidatesReplaceInput 描述带模型版本保护的候选链整体保存请求喵。
type virtualModelCandidatesReplaceInput struct {
	Version    int64                        `json:"version"`
	Candidates []virtualModelCandidateInput `json:"candidates"`
}

// virtualModelFailureRuleInput 描述一个失败规则的可编辑字段喵。
type virtualModelFailureRuleInput struct {
	ID         int `json:"id"`
	HTTPStatus int `json:"http_status"`
	// HTTPStatusMax 是状态码范围匹配的上界，零表示仅匹配 HTTPStatus 单值喵。
	HTTPStatusMax int                             `json:"http_status_max"`
	ErrorClass    string                          `json:"error_class"`
	BodyRegex     string                          `json:"body_regex"`
	Action        model.VirtualModelFailureAction `json:"action"`
	FreezeSeconds int                             `json:"freeze_seconds"`
	// FreezeField 是响应体中的冻结时间字段名，非空时启用从响应体解析冻结时间喵。
	FreezeField string `json:"freeze_field"`
	// FreezeUnit 标记响应体字段冻结时间的单位，仅在 FreezeField 非空时生效喵。
	FreezeUnit model.VirtualModelFreezeUnit `json:"freeze_unit"`
	// StallTimeoutSeconds 静默多久判定流式卡流，单位：秒；零表示运行时默认 60 喵。
	StallTimeoutSeconds int `json:"stall_timeout_seconds"`
	// MinContentChars 探测放流前需累积的内容字符门槛，零表示默认 10 喵。
	MinContentChars int `json:"min_content_chars"`
	// ProbeTotalTimeoutSeconds 探测阶段总预算，单位：秒；零表示默认 300 喵。
	ProbeTotalTimeoutSeconds int `json:"probe_total_timeout_seconds"`
	// TimeoutSeconds 超时条件判定阈值，单位：秒；零表示沿用候选级执行超时喵。
	TimeoutSeconds int `json:"timeout_seconds"`
	// RetryCount 规则重试当前候选的最大重试次数，零表示未配置沿用候选 MaxRetries 喵。
	RetryCount int `json:"retry_count"`
}

// virtualModelFailureRulesReplaceInput 描述候选失败规则的版本化整体替换请求喵。
// HedgeThreshold 与 HedgeFreezeSeconds 是候选级自动避险配置，随规则保存一并写入候选表喵。
type virtualModelFailureRulesReplaceInput struct {
	Version int64                          `json:"version"`
	Rules   []virtualModelFailureRuleInput `json:"rules"`
	// HedgeThreshold 连续失败自动避险阈值，达到该次数才冻结退避；零表示关闭自动避险喵。
	HedgeThreshold int `json:"hedge_threshold"`
	// HedgeFreezeSeconds 达到连续失败阈值后的退避冻结秒数；阈值非零时必填且必须为正数喵。
	HedgeFreezeSeconds int `json:"hedge_freeze_seconds"`
}

// virtualModelBindingInput 描述当前用户 API Key 的授权关系喵。
type virtualModelBindingInput struct {
	TokenIDs []int `json:"token_ids"`
	Version  int64 `json:"version"`
}

// virtualModelFreezeInput 描述带模型版本保护的手动冻结或解冻请求喵。
// FreezeSeconds 与 ExpiresAt 二选一：FreezeSeconds 为正时换算到期时间，否则使用 ExpiresAt 喵。
type virtualModelFreezeInput struct {
	ExpiresAt     int64 `json:"expires_at"`
	FreezeSeconds int   `json:"freeze_seconds"`
	Version       int64 `json:"version"`
}

// virtualModelDeleteInput 描述带模型版本保护的删除请求喵。
type virtualModelDeleteInput struct {
	Version int64 `json:"version"`
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
	// GlobalFailureRules 是模型级全局兜底失败规则，候选未配置规则时运行时按其决策喵。
	GlobalFailureRules []model.VirtualModelGlobalFailureRule `json:"global_failure_rules,omitempty"`
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
			HedgeThreshold: candidate.HedgeThreshold, HedgeFreezeSeconds: candidate.HedgeFreezeSeconds,
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
			// 引用上游模型且直填真实模型名为空时，回退解析上游模型名，避免编辑界面名称空白喵。
			if candidateResponse.RealModelName == "" {
				candidateResponse.RealModelName = resolveVirtualCandidateUpstreamLabel(customCandidate.UpstreamModelID, virtualModel.OwnerUserID)
			}
			candidateResponse.BaseURL = customCandidate.BaseURLSummary
			// 用户上游模型引用回填，供前端候选链编辑器展示喵。
			candidateResponse.UpstreamModelID = customCandidate.UpstreamModelID
			// 喵~防御：历史认证枚举不得回显为旧内部值，控制面只输出稳定 wire value 喵。
			candidateResponse.AuthStyle = model.VirtualModelAuthStyleFromStorage(customCandidate.AuthStyle)
		}
		if err := model.DB.Where("candidate_id = ?", candidate.ID).Order("rule_order asc, id asc").Find(&candidateResponse.FailureRules).Error; err != nil {
			return nil, err
		}
		candidateResponses = append(candidateResponses, candidateResponse)
	}
	// 读取当前手动冻结状态，供调用链页面展示已冻结徽章与剩余时间喵。
	candidateIDs := make([]int, 0, len(candidateResponses))
	for _, candidateResponse := range candidateResponses {
		candidateIDs = append(candidateIDs, candidateResponse.ID)
	}
	frozenUntilByCandidate, freezeQueryError := model.GetActiveVirtualModelManualFreezes(candidateIDs, common.GetTimestamp())
	// 喵~防御：冻结状态查询失败不阻断模型读取，仅跳过冻结状态回填喵。
	if freezeQueryError == nil {
		for i := range candidateResponses {
			candidateResponses[i].FrozenUntil = frozenUntilByCandidate[candidateResponses[i].ID]
		}
	}
	var bindings []model.VirtualModelTokenBinding
	if err := model.DB.Where("virtual_model_id = ? AND owner_user_id = ?", virtualModel.ID, virtualModel.OwnerUserID).Find(&bindings).Error; err != nil {
		return nil, err
	}
	bindingTokenIDs := make([]int, 0, len(bindings))
	for _, binding := range bindings {
		bindingTokenIDs = append(bindingTokenIDs, binding.TokenID)
	}
	// 读取模型级全局兜底规则，与候选级规则一起返回给控制面编辑喵。
	var globalFailureRules []model.VirtualModelGlobalFailureRule
	if err := model.DB.Where("virtual_model_id = ?", virtualModel.ID).Order("rule_order asc, id asc").Find(&globalFailureRules).Error; err != nil {
		return nil, err
	}
	return &virtualModelResponse{VirtualModel: virtualModel, Candidates: candidateResponses, Bindings: bindingTokenIDs, GlobalFailureRules: globalFailureRules}, nil
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
	if existing != nil && input.Version != existing.Version {
		return fmt.Errorf("virtual_model_version_conflict")
	}
	existing.OwnerUserID = ownerUserID
	existing.NormalizedName = normalizedName
	existing.DisplayName = strings.TrimSpace(input.DisplayName)
	existing.Enabled = input.Enabled
	existing.LoopEnabled = input.LoopEnabled
	existing.TotalTimeoutSeconds = input.TotalTimeoutSeconds
	existing.MaxLoopRounds = input.MaxLoopRounds
	// 流转伪流配置：断流处理措施空值表示跟随失败规则，重试次数经共享校验把关喵。
	existing.FakeStreamEnabled = input.FakeStreamEnabled
	existing.StreamCutAction = model.VirtualModelFailureAction(input.StreamCutAction)
	existing.StreamCutRetries = input.StreamCutRetries
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
	// 喵~防御：所有更新都必须携带读取版本，避免缺失版本的旧客户端无意覆盖新配置喵。
	if input.Version <= 0 {
		common.ApiError(c, errors.New("虚拟模型版本无效"))
		return
	}
	if input.Version != existingVersion {
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
	updateResult := model.DB.Model(virtualModel).Where("id = ? AND owner_user_id = ? AND version = ?", modelID, c.GetInt("id"), existingVersion).Select("normalized_name", "display_name", "enabled", "loop_enabled", "total_timeout_seconds", "max_loop_rounds", "fake_stream_enabled", "stream_cut_action", "stream_cut_retries", "version", "updated_time").Updates(virtualModel)
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
	virtualModel, ok := loadOwnedVirtualModel(c, modelID)
	if !ok {
		return
	}
	var input virtualModelDeleteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		common.ApiError(c, err)
		return
	}
	// 喵~防御：删除也必须比对版本，避免一个过期页面撤销其他配置修改喵。
	if input.Version <= 0 {
		common.ApiError(c, errors.New("虚拟模型版本无效"))
		return
	}
	if input.Version != virtualModel.Version {
		c.JSON(http.StatusConflict, gin.H{"success": false, "code": "virtual_model_version_conflict", "message": "虚拟模型已被其他请求修改"})
		return
	}
	if err := model.DeleteVirtualModelByOwnerWithVersion(modelID, c.GetInt("id"), c.GetInt("id"), input.Version); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			virtualModelNotFound(c)
			return
		}
		if err.Error() == "virtual_model_version_conflict" {
			c.JSON(http.StatusConflict, gin.H{"success": false, "code": "virtual_model_version_conflict", "message": "虚拟模型已被其他请求修改"})
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
	var input virtualModelCandidatesReplaceInput
	if err := c.ShouldBindJSON(&input); err != nil {
		common.ApiError(c, err)
		return
	}
	if input.Version <= 0 {
		common.ApiError(c, errors.New("虚拟模型版本无效"))
		return
	}
	if len(input.Candidates) == 0 || len(input.Candidates) > 32 {
		common.ApiError(c, errors.New("候选链长度必须介于 1 和 32 之间"))
		return
	}
	if input.Version != virtualModel.Version {
		c.JSON(http.StatusConflict, gin.H{"success": false, "code": "virtual_model_version_conflict", "message": "虚拟模型已被其他请求修改"})
		return
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		// 喵~防御：使用客户端读取的版本执行条件更新，避免陈旧候选链覆盖新的保存结果喵。
		updateResult := tx.Model(&model.VirtualModel{}).Where("id = ? AND owner_user_id = ? AND version = ?", modelID, c.GetInt("id"), input.Version).Updates(map[string]any{"version": input.Version + 1, "updated_time": common.GetTimestamp()})
		if updateResult.Error != nil {
			return updateResult.Error
		}
		if updateResult.RowsAffected != 1 {
			return errors.New("virtual_model_version_conflict")
		}
		// 读取当前候选并建立编号索引，后续只允许更新属于当前模型的候选喵。
		currentCandidates := make([]model.VirtualModelCandidate, 0)
		if err := tx.Where("virtual_model_id = ?", modelID).Find(&currentCandidates).Error; err != nil {
			return err
		}
		// 喵~防御：先将旧顺序整体移出目标范围，避免交换或插入排序时触发唯一索引的中间冲突喵。
		if len(currentCandidates) > 0 {
			if err := tx.Model(&model.VirtualModelCandidate{}).Where("virtual_model_id = ?", modelID).Update("stable_order", gorm.Expr("stable_order + ?", 1000000)).Error; err != nil {
				return err
			}
			for candidateIndex := range currentCandidates {
				currentCandidates[candidateIndex].StableOrder += 1000000
			}
		}
		currentCandidatesByID := make(map[int]model.VirtualModelCandidate, len(currentCandidates))
		for _, currentCandidate := range currentCandidates {
			currentCandidatesByID[currentCandidate.ID] = currentCandidate
		}
		retainedCandidateIDs := make(map[int]struct{}, len(input.Candidates))
		for candidateIndex, candidateInput := range input.Candidates {
			// 喵~防御：同一候选不得在一次请求中出现两次，避免顺序和关联配置产生歧义喵。
			if candidateInput.ID > 0 {
				if _, duplicateCandidateID := retainedCandidateIDs[candidateInput.ID]; duplicateCandidateID {
					return errors.New("候选编号重复")
				}
				retainedCandidateIDs[candidateInput.ID] = struct{}{}
			}
			if candidateInput.ID == 0 {
				if err := createVirtualModelCandidateWithConfig(tx, modelID, candidateIndex, candidateInput); err != nil {
					return err
				}
				continue
			}
			currentCandidate, candidateExists := currentCandidatesByID[candidateInput.ID]
			// 喵~防御：候选 ID 必须归属当前模型，避免利用编号跨模型修改配置或凭据喵。
			if !candidateExists {
				return errors.New("虚拟模型候选不存在")
			}
			// 喵~防御：保留候选不可改变来源类型，避免规则与冻结在语义变化时错误继承喵。
			if currentCandidate.SourceType != candidateInput.SourceType {
				return errors.New("保留候选不能变更来源类型")
			}
			if err := updateVirtualModelCandidateWithConfig(tx, &currentCandidate, candidateIndex, candidateInput); err != nil {
				return err
			}
		}
		removedCandidateIDs := make([]int, 0)
		for _, currentCandidate := range currentCandidates {
			if _, retainedCandidate := retainedCandidateIDs[currentCandidate.ID]; !retainedCandidate {
				removedCandidateIDs = append(removedCandidateIDs, currentCandidate.ID)
			}
		}
		if err := deleteVirtualModelCandidatesWithAssociations(tx, removedCandidateIDs); err != nil {
			return err
		}
		// 喵~防御：默认 action 与摘要只含资源编号，审计中禁止写入 URL、API Key 或规则正文喵。
		if err := tx.Create(&model.VirtualModelAuditLog{VirtualModelID: modelID, OwnerUserID: c.GetInt("id"), OperatorID: c.GetInt("id"), Action: "candidate_chain_replace", SummaryDigest: fmt.Sprintf("candidate_count:%d", len(input.Candidates)), CreatedTime: common.GetTimestamp()}).Error; err != nil {
			return err
		}
		virtualModel.Version = input.Version + 1
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

// createVirtualModelCandidateWithConfig 创建新候选及其来源专属配置，确保整个操作受调用方事务保护喵。
func createVirtualModelCandidateWithConfig(tx *gorm.DB, virtualModelID int, stableOrder int, candidateInput virtualModelCandidateInput) error {
	// 喵~防御：事务连接、模型编号和顺序无效时拒绝写入，避免脱离原子更新边界喵。
	if tx == nil || virtualModelID <= 0 || stableOrder < 0 {
		return errors.New("虚拟模型候选无效")
	}
	candidate := &model.VirtualModelCandidate{VirtualModelID: virtualModelID, StableOrder: stableOrder, SourceType: candidateInput.SourceType, Enabled: candidateInput.Enabled, MaxRetries: candidateInput.MaxRetries, TimeoutSeconds: candidateInput.TimeoutSeconds, HedgeThreshold: candidateInput.HedgeThreshold, HedgeFreezeSeconds: candidateInput.HedgeFreezeSeconds, Version: 1, CreatedTime: common.GetTimestamp(), UpdatedTime: common.GetTimestamp()}
	// 喵~防御：在创建主候选行前校验来源专属字段，避免校验失败时留下孤立候选喵。
	if err := validateVirtualModelCandidateSourceInput(candidate.SourceType, candidateInput, true); err != nil {
		return err
	}
	if err := model.ValidateVirtualModelCandidate(candidate); err != nil {
		return err
	}
	if err := tx.Create(candidate).Error; err != nil {
		return err
	}
	return saveVirtualModelCandidateSourceConfig(tx, candidate, candidateInput, true)
}

// updateVirtualModelCandidateWithConfig 更新保留候选并保留不受本次修改影响的关联资源喵。
func updateVirtualModelCandidateWithConfig(tx *gorm.DB, candidate *model.VirtualModelCandidate, stableOrder int, candidateInput virtualModelCandidateInput) error {
	// 喵~防御：候选必须存在且归属有效模型，避免跨模型或零值更新喵。
	if tx == nil || candidate == nil || candidate.ID <= 0 || candidate.VirtualModelID <= 0 || stableOrder < 0 {
		return errors.New("虚拟模型候选无效")
	}
	// 喵~防御：来源专属字段先通过校验，避免更新通用候选后才发现来源配置非法喵。
	if err := validateVirtualModelCandidateSourceInput(candidate.SourceType, candidateInput, false); err != nil {
		return err
	}
	candidate.StableOrder = stableOrder
	candidate.Enabled = candidateInput.Enabled
	candidate.MaxRetries = candidateInput.MaxRetries
	candidate.TimeoutSeconds = candidateInput.TimeoutSeconds
	candidate.HedgeThreshold = candidateInput.HedgeThreshold
	candidate.HedgeFreezeSeconds = candidateInput.HedgeFreezeSeconds
	candidate.Version++
	candidate.UpdatedTime = common.GetTimestamp()
	if err := model.ValidateVirtualModelCandidate(candidate); err != nil {
		return err
	}
	if err := tx.Model(&model.VirtualModelCandidate{}).Where("id = ? AND virtual_model_id = ?", candidate.ID, candidate.VirtualModelID).Select("stable_order", "enabled", "max_retries", "timeout_seconds", "hedge_threshold", "hedge_freeze_seconds", "version", "updated_time").Updates(candidate).Error; err != nil {
		return err
	}
	return saveVirtualModelCandidateSourceConfig(tx, candidate, candidateInput, false)
}

// validateVirtualModelCandidateSourceInput 验证候选来源专属字段，避免写入主候选后才发现配置无法执行喵。
func validateVirtualModelCandidateSourceInput(sourceType model.VirtualModelSourceType, candidateInput virtualModelCandidateInput, isNewCandidate bool) error {
	// 喵~防御：内部候选必须有明确分组和真实模型，避免请求意外落入默认分组或空模型喵。
	if sourceType == model.VirtualModelSourceInternal {
		if strings.TrimSpace(candidateInput.GroupName) == "" || strings.TrimSpace(candidateInput.RealModelName) == "" {
			return errors.New("内部候选必须提供分组和真实模型")
		}
		return nil
	}
	// 喵~防御：未知来源不接受任何配置，避免未经实现的执行分支进入数据面喵。
	if sourceType != model.VirtualModelSourceCustom {
		return errors.New("虚拟模型候选来源无效")
	}
	// 引用用户上游模型时，真实模型名与凭据以该条目为准，不要求直填字段喵。
	if candidateInput.UpstreamModelID != nil && *candidateInput.UpstreamModelID > 0 {
		return nil
	}
	if strings.TrimSpace(candidateInput.RealModelName) == "" {
		return errors.New("自定义候选必须提供真实模型")
	}
	baseURL := strings.TrimSpace(candidateInput.BaseURL)
	// 喵~防御：已有候选可省略完整 URL 以保留服务端密文，避免脱敏摘要覆盖带路径的真实上游地址喵。
	if baseURL == "" && !isNewCandidate {
		if _, normalizeAuthError := model.NormalizeVirtualModelAuthStyle(candidateInput.AuthStyle); normalizeAuthError != nil {
			return normalizeAuthError
		}
		return nil
	}
	if baseURL == "" {
		return errors.New("自定义候选必须提供地址和真实模型")
	}
	if _, validateURLError := virtualmodelservice.ValidateCustomBaseURL(baseURL); validateURLError != nil {
		return validateURLError
	}
	if _, normalizeAuthError := model.NormalizeVirtualModelAuthStyle(candidateInput.AuthStyle); normalizeAuthError != nil {
		return normalizeAuthError
	}
	// 喵~防御：新自定义候选没有旧密文可保留，因此必须提交一次非空凭据喵。
	if isNewCandidate && strings.TrimSpace(candidateInput.APIKey) == "" {
		return errors.New("自定义候选必须提供地址、凭据和真实模型")
	}
	return nil
}

// saveVirtualModelCandidateSourceConfig 保存内部或自定义来源配置，新建自定义候选必须带凭据，保留候选可省略凭据喵。
func saveVirtualModelCandidateSourceConfig(tx *gorm.DB, candidate *model.VirtualModelCandidate, candidateInput virtualModelCandidateInput, isNewCandidate bool) error {
	// 喵~防御：候选来源和事务边界必须完整，避免创建孤立配置或以未知来源发送外部请求喵。
	if tx == nil || candidate == nil || candidate.ID <= 0 {
		return errors.New("虚拟模型候选无效")
	}
	if candidate.SourceType == model.VirtualModelSourceInternal {
		if strings.TrimSpace(candidateInput.GroupName) == "" || strings.TrimSpace(candidateInput.RealModelName) == "" {
			return errors.New("内部候选必须提供分组和真实模型")
		}
		// 喵~防御：保留候选保存也必须验证来源字段，防止直接调用本 helper 时绕过创建或更新入口喵。
		if err := validateVirtualModelCandidateSourceInput(candidate.SourceType, candidateInput, isNewCandidate); err != nil {
			return err
		}
		internalCandidate := &model.VirtualModelInternalCandidate{CandidateID: candidate.ID, GroupName: strings.TrimSpace(candidateInput.GroupName), RealModelName: strings.TrimSpace(candidateInput.RealModelName)}
		return tx.Where("candidate_id = ?", candidate.ID).Assign(internalCandidate).FirstOrCreate(&model.VirtualModelInternalCandidate{}).Error
	}
	if candidate.SourceType != model.VirtualModelSourceCustom {
		return errors.New("虚拟模型候选来源无效")
	}
	// 喵~防御：保存层再次验证自定义来源字段，避免未来调用方绕过入口校验写入不可执行凭据喵。
	if err := validateVirtualModelCandidateSourceInput(candidate.SourceType, candidateInput, isNewCandidate); err != nil {
		return err
	}
	customCandidate := &model.VirtualModelCustomCandidate{}
	customCandidateQueryError := tx.Where("candidate_id = ?", candidate.ID).First(customCandidate).Error
	if customCandidateQueryError != nil && !errors.Is(customCandidateQueryError, gorm.ErrRecordNotFound) {
		return customCandidateQueryError
	}
	// 引用用户上游模型条目：凭据与真实模型名以条目为准，直填字段仅作展示兼容喵。
	if candidateInput.UpstreamModelID != nil && *candidateInput.UpstreamModelID > 0 {
		customCandidate.UpstreamModelID = candidateInput.UpstreamModelID
		customCandidate.CandidateID = candidate.ID
		return tx.Where("candidate_id = ?", candidate.ID).Assign(customCandidate).FirstOrCreate(&model.VirtualModelCustomCandidate{}).Error
	}
	// 无引用：清空引用标记并走原有直填凭据逻辑喵。
	customCandidate.UpstreamModelID = nil
	apiKey := strings.TrimSpace(candidateInput.APIKey)
	baseURL := strings.TrimSpace(candidateInput.BaseURL)
	// 喵~防御：保留候选省略地址时仅保留既有加密 URL，不用脱敏摘要重建或覆盖真正目标喵。
	if baseURL != "" {
		parsedURL, validateURLError := virtualmodelservice.ValidateCustomBaseURL(baseURL)
		if validateURLError != nil {
			return validateURLError
		}
		encryptedBaseURL, credentialVersion, encryptBaseURLError := virtualmodelservice.EncryptCredential(parsedURL.String())
		if encryptBaseURLError != nil {
			return encryptBaseURLError
		}
		customCandidate.EncryptedBaseURL = encryptedBaseURL
		customCandidate.CredentialVersion = credentialVersion
		customCandidate.BaseURLSummary = virtualmodelservice.SummarizeCustomBaseURL(parsedURL)
		customCandidate.BaseURLFingerprint = virtualmodelservice.CredentialFingerprint(parsedURL.String())
	}
	normalizedAuthStyle, normalizeAuthError := model.NormalizeVirtualModelAuthStyle(candidateInput.AuthStyle)
	if normalizeAuthError != nil {
		return normalizeAuthError
	}
	customCandidate.CandidateID = candidate.ID
	customCandidate.RealModelName = strings.TrimSpace(candidateInput.RealModelName)
	customCandidate.AuthStyle = normalizedAuthStyle
	if apiKey != "" {
		encryptedAPIKey, _, encryptAPIKeyError := virtualmodelservice.EncryptCredential(apiKey)
		if encryptAPIKeyError != nil {
			return encryptAPIKeyError
		}
		customCandidate.EncryptedAPIKey = encryptedAPIKey
		customCandidate.APIKeyFingerprint = virtualmodelservice.CredentialFingerprint(apiKey)
	}
	// 喵~防御：保留候选省略凭据时只有已存在密文才可保存，防止空凭据候选进入运行时喵。
	if strings.TrimSpace(customCandidate.EncryptedAPIKey) == "" {
		return errors.New("自定义候选必须提供地址、凭据和真实模型")
	}
	return tx.Where("candidate_id = ?", candidate.ID).Assign(customCandidate).FirstOrCreate(&model.VirtualModelCustomCandidate{}).Error
}

// deleteVirtualModelCandidatesWithAssociations 删除明确移除的候选及其规则、冻结和加密配置喵。
func deleteVirtualModelCandidatesWithAssociations(tx *gorm.DB, candidateIDs []int) error {
	// 喵~防御：空删除集合安全跳过，避免生成无条件关联删除喵。
	if len(candidateIDs) == 0 {
		return nil
	}
	if tx == nil {
		return errors.New("虚拟模型数据库不可用")
	}
	if err := tx.Unscoped().Where("candidate_id IN ?", candidateIDs).Delete(&model.VirtualModelFailureRule{}).Error; err != nil {
		return err
	}
	if err := tx.Unscoped().Where("candidate_id IN ?", candidateIDs).Delete(&model.VirtualModelInternalCandidate{}).Error; err != nil {
		return err
	}
	if err := tx.Unscoped().Where("candidate_id IN ?", candidateIDs).Delete(&model.VirtualModelCustomCandidate{}).Error; err != nil {
		return err
	}
	if err := tx.Unscoped().Where("candidate_id IN ?", candidateIDs).Delete(&model.VirtualModelManualFreeze{}).Error; err != nil {
		return err
	}
	return tx.Unscoped().Where("id IN ?", candidateIDs).Delete(&model.VirtualModelCandidate{}).Error
}

// ReplaceVirtualModelCandidateFailureRules 原子替换一个候选的排序失败规则，并通过父模型版本避免并发覆盖喵。
func ReplaceVirtualModelCandidateFailureRules(c *gin.Context) {
	// 喵~防御：路径模型与候选编号非法时统一返回资源不存在，避免越权枚举喵。
	modelID, validModelID := parseVirtualModelID(c)
	if !validModelID {
		return
	}
	candidateID, candidateIDError := strconv.Atoi(c.Param("candidateId"))
	if candidateIDError != nil || candidateID <= 0 {
		virtualModelNotFound(c)
		return
	}
	var input virtualModelFailureRulesReplaceInput
	// 喵~防御：请求体格式错误时拒绝写入，避免零值规则覆盖现有故障策略喵。
	if bindError := c.ShouldBindJSON(&input); bindError != nil || input.Version <= 0 || len(input.Rules) > 32 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "virtual_model_invalid_request", "message": "虚拟模型失败规则请求无效"})
		return
	}
	virtualModel, foundModel := loadOwnedVirtualModel(c, modelID)
	if !foundModel {
		return
	}
	if input.Version != virtualModel.Version {
		c.JSON(http.StatusConflict, gin.H{"success": false, "code": "virtual_model_version_conflict", "message": "虚拟模型已被其他请求修改"})
		return
	}
	transactionError := model.DB.Transaction(func(transactionDatabase *gorm.DB) error {
		candidate := &model.VirtualModelCandidate{}
		// 喵~防御：候选查询同时限制当前模型归属，防止用户借路径参数覆盖其他模型候选规则喵。
		if candidateError := transactionDatabase.Where("id = ? AND virtual_model_id = ?", candidateID, modelID).First(candidate).Error; candidateError != nil {
			return gorm.ErrRecordNotFound
		}
		// 校验候选级自动避险配置，非法值必须整体回滚，避免持久化不可执行的退避策略喵。
		candidate.HedgeThreshold = input.HedgeThreshold
		candidate.HedgeFreezeSeconds = input.HedgeFreezeSeconds
		if validateError := model.ValidateVirtualModelCandidate(candidate); validateError != nil {
			return validateError
		}
		// 喵~防御：确认候选存在后才推进父模型版本，避免无效候选请求无谓制造版本冲突喵。
		updateResult := transactionDatabase.Model(&model.VirtualModel{}).Where("id = ? AND owner_user_id = ? AND version = ?", modelID, c.GetInt("id"), input.Version).Updates(map[string]any{"version": input.Version + 1, "updated_time": common.GetTimestamp()})
		if updateResult.Error != nil {
			return updateResult.Error
		}
		if updateResult.RowsAffected != 1 {
			return errors.New("virtual_model_version_conflict")
		}
		// 更新候选的自动避险配置，与规则替换保持同一事务边界喵。
		if hedgeUpdateError := transactionDatabase.Model(candidate).Select("hedge_threshold", "hedge_freeze_seconds", "updated_time").Updates(map[string]any{"hedge_threshold": candidate.HedgeThreshold, "hedge_freeze_seconds": candidate.HedgeFreezeSeconds, "updated_time": common.GetTimestamp()}).Error; hedgeUpdateError != nil {
			return hedgeUpdateError
		}
		// 喵~防御：先硬删除旧规则再按请求顺序创建，避免中间排序唯一约束冲突或遗留失效规则喵。
		if deleteError := transactionDatabase.Unscoped().Where("candidate_id = ?", candidateID).Delete(&model.VirtualModelFailureRule{}).Error; deleteError != nil {
			return deleteError
		}
		for ruleOrder, ruleInput := range input.Rules {
			failureRule := &model.VirtualModelFailureRule{CandidateID: candidateID, RuleOrder: ruleOrder, HTTPStatus: ruleInput.HTTPStatus, HTTPStatusMax: ruleInput.HTTPStatusMax, ErrorClass: strings.TrimSpace(ruleInput.ErrorClass), BodyRegex: strings.TrimSpace(ruleInput.BodyRegex), Action: ruleInput.Action, FreezeSeconds: ruleInput.FreezeSeconds, FreezeField: strings.TrimSpace(ruleInput.FreezeField), FreezeUnit: ruleInput.FreezeUnit, StallTimeoutSeconds: ruleInput.StallTimeoutSeconds, MinContentChars: ruleInput.MinContentChars, ProbeTotalTimeoutSeconds: ruleInput.ProbeTotalTimeoutSeconds, TimeoutSeconds: ruleInput.TimeoutSeconds, RetryCount: ruleInput.RetryCount}
			if validateError := virtualmodelservice.ValidateCandidateFailureRule(failureRule); validateError != nil {
				return validateError
			}
			if createError := transactionDatabase.Create(failureRule).Error; createError != nil {
				return createError
			}
		}
		return transactionDatabase.Create(&model.VirtualModelAuditLog{VirtualModelID: modelID, OwnerUserID: c.GetInt("id"), OperatorID: c.GetInt("id"), Action: "failure_rule_replace", SummaryDigest: fmt.Sprintf("candidate:%d;rules:%d", candidateID, len(input.Rules)), CreatedTime: common.GetTimestamp()}).Error
	})
	if transactionError != nil {
		if transactionError.Error() == "virtual_model_version_conflict" {
			c.JSON(http.StatusConflict, gin.H{"success": false, "code": "virtual_model_version_conflict", "message": "虚拟模型已被其他请求修改"})
			return
		}
		if errors.Is(transactionError, gorm.ErrRecordNotFound) {
			virtualModelNotFound(c)
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "virtual_model_invalid_request", "message": transactionError.Error()})
		return
	}
	virtualModel.Version = input.Version + 1
	response, responseError := buildVirtualModelResponse(virtualModel)
	if responseError != nil {
		common.ApiError(c, responseError)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": response})
}

// ReplaceVirtualModelGlobalFailureRules 原子替换模型级全局兜底失败规则，并通过父模型版本避免并发覆盖喵。
func ReplaceVirtualModelGlobalFailureRules(c *gin.Context) {
	// 喵~防御：路径模型编号非法时统一返回资源不存在，避免越权枚举喵。
	modelID, validModelID := parseVirtualModelID(c)
	if !validModelID {
		return
	}
	var input virtualModelFailureRulesReplaceInput
	// 喵~防御：请求体格式错误或规则数量越界时拒绝写入，避免零值规则覆盖现有兜底策略喵。
	if bindError := c.ShouldBindJSON(&input); bindError != nil || input.Version <= 0 || len(input.Rules) > 32 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "virtual_model_invalid_request", "message": "虚拟模型失败规则请求无效"})
		return
	}
	virtualModel, foundModel := loadOwnedVirtualModel(c, modelID)
	if !foundModel {
		return
	}
	if input.Version != virtualModel.Version {
		c.JSON(http.StatusConflict, gin.H{"success": false, "code": "virtual_model_version_conflict", "message": "虚拟模型已被其他请求修改"})
		return
	}
	transactionError := model.DB.Transaction(func(transactionDatabase *gorm.DB) error {
		// 喵~防御：先条件推进父模型版本，避免过期请求覆盖较新的全局兜底配置喵。
		updateResult := transactionDatabase.Model(&model.VirtualModel{}).Where("id = ? AND owner_user_id = ? AND version = ?", modelID, c.GetInt("id"), input.Version).Updates(map[string]any{"version": input.Version + 1, "updated_time": common.GetTimestamp()})
		if updateResult.Error != nil {
			return updateResult.Error
		}
		if updateResult.RowsAffected != 1 {
			return errors.New("virtual_model_version_conflict")
		}
		// 喵~防御：先硬删除旧全局规则再按请求顺序创建，避免中间排序唯一约束冲突或遗留失效规则喵。
		if deleteError := transactionDatabase.Unscoped().Where("virtual_model_id = ?", modelID).Delete(&model.VirtualModelGlobalFailureRule{}).Error; deleteError != nil {
			return deleteError
		}
		for ruleOrder, ruleInput := range input.Rules {
			globalFailureRule := &model.VirtualModelGlobalFailureRule{VirtualModelID: modelID, RuleOrder: ruleOrder, HTTPStatus: ruleInput.HTTPStatus, HTTPStatusMax: ruleInput.HTTPStatusMax, ErrorClass: strings.TrimSpace(ruleInput.ErrorClass), BodyRegex: strings.TrimSpace(ruleInput.BodyRegex), Action: ruleInput.Action, FreezeSeconds: ruleInput.FreezeSeconds, FreezeField: strings.TrimSpace(ruleInput.FreezeField), FreezeUnit: ruleInput.FreezeUnit, StallTimeoutSeconds: ruleInput.StallTimeoutSeconds, MinContentChars: ruleInput.MinContentChars, ProbeTotalTimeoutSeconds: ruleInput.ProbeTotalTimeoutSeconds, TimeoutSeconds: ruleInput.TimeoutSeconds, RetryCount: ruleInput.RetryCount}
			if validateError := virtualmodelservice.ValidateGlobalFailureRule(globalFailureRule); validateError != nil {
				return validateError
			}
			if createError := transactionDatabase.Create(globalFailureRule).Error; createError != nil {
				return createError
			}
		}
		return transactionDatabase.Create(&model.VirtualModelAuditLog{VirtualModelID: modelID, OwnerUserID: c.GetInt("id"), OperatorID: c.GetInt("id"), Action: "global_failure_rule_replace", SummaryDigest: fmt.Sprintf("model:%d;rules:%d", modelID, len(input.Rules)), CreatedTime: common.GetTimestamp()}).Error
	})
	if transactionError != nil {
		if transactionError.Error() == "virtual_model_version_conflict" {
			c.JSON(http.StatusConflict, gin.H{"success": false, "code": "virtual_model_version_conflict", "message": "虚拟模型已被其他请求修改"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "virtual_model_invalid_request", "message": transactionError.Error()})
		return
	}
	virtualModel.Version = input.Version + 1
	response, responseError := buildVirtualModelResponse(virtualModel)
	if responseError != nil {
		common.ApiError(c, responseError)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": response})
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
	if input.Version <= 0 {
		common.ApiError(c, errors.New("虚拟模型版本无效"))
		return
	}
	seenTokenIDs := make(map[int]struct{}, len(input.TokenIDs))
	existingVersion := virtualModel.Version
	// 喵~防御：请求版本不匹配时在删除旧绑定前拒绝，避免过期请求破坏较新授权关系喵。
	if input.Version != existingVersion {
		c.JSON(http.StatusConflict, gin.H{"success": false, "code": "virtual_model_version_conflict", "message": "虚拟模型已被其他请求修改"})
		return
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		// 喵~防御：先条件推进版本再变更绑定，保证并发过期写入会整体回滚喵。
		updateResult := tx.Model(&model.VirtualModel{}).Where("id = ? AND owner_user_id = ? AND version = ?", modelID, c.GetInt("id"), existingVersion).Updates(map[string]any{"version": existingVersion + 1, "updated_time": common.GetTimestamp()})
		if updateResult.Error != nil {
			return updateResult.Error
		}
		if updateResult.RowsAffected != 1 {
			return errors.New("virtual_model_version_conflict")
		}
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
		// 同步内存对象版本供响应 DTO 使用；数据库版本已由事务起始的条件更新原子推进喵。
		virtualModel.Version = existingVersion + 1
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

// FreezeVirtualModelCandidate 手动冻结当前用户拥有模型中的候选喵。
func FreezeVirtualModelCandidate(c *gin.Context) {
	modelID, ok := parseVirtualModelID(c)
	if !ok {
		return
	}
	virtualModel, ok := loadOwnedVirtualModel(c, modelID)
	if !ok {
		return
	}
	candidateID, parseError := strconv.Atoi(c.Param("candidateId"))
	if parseError != nil || candidateID <= 0 {
		virtualModelNotFound(c)
		return
	}
	var input virtualModelFreezeInput
	if err := c.ShouldBindJSON(&input); err != nil {
		common.ApiError(c, err)
		return
	}
	currentTimestamp := common.GetTimestamp()
	// 喵~防御：冻结必须携带当前版本，避免陈旧覆盖喵。
	if input.Version <= 0 {
		common.ApiError(c, errors.New("虚拟模型版本无效"))
		return
	}
	// 到期时间解析：FreezeSeconds 为正时按秒数换算到期时间戳，否则要求 ExpiresAt 落在未来一天时间窗内喵。
	expiresAt := input.ExpiresAt
	if input.FreezeSeconds > 0 {
		// 喵~防御：自定义秒数不得超过一天，与自动冻结 freeze 的上限保持一致喵。
		if input.FreezeSeconds > 86400 {
			common.ApiError(c, errors.New("冻结秒数必须在未来 86400 秒内"))
			return
		}
		expiresAt = currentTimestamp + int64(input.FreezeSeconds)
	} else if input.ExpiresAt <= currentTimestamp || input.ExpiresAt-currentTimestamp > 86400 {
		common.ApiError(c, errors.New("冻结到期时间必须在未来 86400 秒内"))
		return
	}
	if input.Version != virtualModel.Version {
		c.JSON(http.StatusConflict, gin.H{"success": false, "code": "virtual_model_version_conflict", "message": "虚拟模型已被其他请求修改"})
		return
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		var candidate model.VirtualModelCandidate
		// 喵~防御：候选查询同时限制模型归属，避免跨模型候选被用户冻结喵。
		if err := tx.Where("id = ? AND virtual_model_id = ?", candidateID, modelID).First(&candidate).Error; err != nil {
			return gorm.ErrRecordNotFound
		}
		updateResult := tx.Model(&model.VirtualModel{}).Where("id = ? AND owner_user_id = ? AND version = ?", modelID, c.GetInt("id"), input.Version).Updates(map[string]any{"version": input.Version + 1, "updated_time": currentTimestamp})
		if updateResult.Error != nil {
			return updateResult.Error
		}
		if updateResult.RowsAffected != 1 {
			return errors.New("virtual_model_version_conflict")
		}
		freeze := &model.VirtualModelManualFreeze{}
		freezeQueryError := tx.Where("candidate_id = ?", candidate.ID).First(freeze).Error
		if freezeQueryError != nil && !errors.Is(freezeQueryError, gorm.ErrRecordNotFound) {
			return freezeQueryError
		}
		freeze.CandidateID = candidate.ID
		freeze.OperatorID = c.GetInt("id")
		freeze.StartedAt = currentTimestamp
		freeze.ExpiresAt = expiresAt
		if errors.Is(freezeQueryError, gorm.ErrRecordNotFound) {
			if err := tx.Create(freeze).Error; err != nil {
				return err
			}
		} else if err := tx.Save(freeze).Error; err != nil {
			return err
		}
		return tx.Create(&model.VirtualModelAuditLog{VirtualModelID: modelID, OwnerUserID: c.GetInt("id"), OperatorID: c.GetInt("id"), Action: "manual_freeze", SummaryDigest: fmt.Sprintf("candidate:%d", candidateID), CreatedTime: currentTimestamp}).Error
	}); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			virtualModelNotFound(c)
			return
		}
		if err.Error() == "virtual_model_version_conflict" {
			c.JSON(http.StatusConflict, gin.H{"success": false, "code": "virtual_model_version_conflict", "message": "虚拟模型已被其他请求修改"})
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"candidate_id": candidateID, "expires_at": expiresAt, "operator_id": c.GetInt("id"), "version": input.Version + 1})
}

// UnfreezeVirtualModelCandidate 解除当前用户模型候选的所有有效手动冻结喵。
func UnfreezeVirtualModelCandidate(c *gin.Context) {
	modelID, ok := parseVirtualModelID(c)
	if !ok {
		return
	}
	virtualModel, ok := loadOwnedVirtualModel(c, modelID)
	if !ok {
		return
	}
	candidateID, parseError := strconv.Atoi(c.Param("candidateId"))
	if parseError != nil || candidateID <= 0 {
		virtualModelNotFound(c)
		return
	}
	var input virtualModelFreezeInput
	if err := c.ShouldBindJSON(&input); err != nil {
		common.ApiError(c, err)
		return
	}
	// 喵~防御：解冻必须携带当前版本，避免过期界面取消一个较新的冻结喵。
	if input.Version <= 0 {
		common.ApiError(c, errors.New("虚拟模型版本无效"))
		return
	}
	if input.Version != virtualModel.Version {
		c.JSON(http.StatusConflict, gin.H{"success": false, "code": "virtual_model_version_conflict", "message": "虚拟模型已被其他请求修改"})
		return
	}
	currentTimestamp := common.GetTimestamp()
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		var candidate model.VirtualModelCandidate
		// 喵~防御：候选查询同时限制模型归属，避免跨模型候选被用户解冻喵。
		if err := tx.Where("id = ? AND virtual_model_id = ?", candidateID, modelID).First(&candidate).Error; err != nil {
			return gorm.ErrRecordNotFound
		}
		updateResult := tx.Model(&model.VirtualModel{}).Where("id = ? AND owner_user_id = ? AND version = ?", modelID, c.GetInt("id"), input.Version).Updates(map[string]any{"version": input.Version + 1, "updated_time": currentTimestamp})
		if updateResult.Error != nil {
			return updateResult.Error
		}
		if updateResult.RowsAffected != 1 {
			return errors.New("virtual_model_version_conflict")
		}
		if err := tx.Unscoped().Where("candidate_id = ?", candidate.ID).Delete(&model.VirtualModelManualFreeze{}).Error; err != nil {
			return err
		}
		// 喵~防御：不存在冻结时仍视为幂等成功，使重复解冻不会泄漏状态或阻塞恢复流程喵。
		return tx.Create(&model.VirtualModelAuditLog{VirtualModelID: modelID, OwnerUserID: c.GetInt("id"), OperatorID: c.GetInt("id"), Action: "manual_unfreeze", SummaryDigest: fmt.Sprintf("candidate:%d", candidateID), CreatedTime: currentTimestamp}).Error
	}); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			virtualModelNotFound(c)
			return
		}
		if err.Error() == "virtual_model_version_conflict" {
			c.JSON(http.StatusConflict, gin.H{"success": false, "code": "virtual_model_version_conflict", "message": "虚拟模型已被其他请求修改"})
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"candidate_id": candidateID, "version": input.Version + 1})
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
	// 实体状态检测：读取启用候选快照作为节点摘要，同时保留旧字段保证 Overview 既有展示不破坏喵。
	snapshot, snapshotError := model.GetVirtualModelExecutionSnapshot(virtualModel.ID)
	// 喵~防御：快照读取失败不影响整体状态返回，仅候选摘要为空喵。
	var snapshotValue *model.VirtualModelExecutionSnapshot
	if snapshotError == nil && snapshot != nil {
		snapshotValue = snapshot
	}
	payload := buildVirtualModelStatusPayload(virtualModel, snapshotValue)
	payload.Enabled = virtualModel.Enabled
	payload.CandidateCount = len(candidates)
	payload.EnabledCandidates = countEnabledVirtualCandidates(candidates)
	common.ApiSuccess(c, payload)
}

// GetVirtualModelCandidateStatus 返回单个候选节点的状态统计喵。
func GetVirtualModelCandidateStatus(c *gin.Context) {
	modelID, ok := parseVirtualModelID(c)
	if !ok {
		return
	}
	virtualModel, ok := loadOwnedVirtualModel(c, modelID)
	if !ok {
		return
	}
	candidateID, ok := parseVirtualModelCandidateID(c)
	if !ok {
		return
	}
	// 校验候选确实属于该虚拟模型，防止越权读取其他模型的节点状态喵。
	var candidate model.VirtualModelCandidate
	if err := model.DB.Where("id = ? AND virtual_model_id = ?", candidateID, virtualModel.ID).First(&candidate).Error; err != nil {
		virtualModelNotFound(c)
		return
	}
	label := ""
	snapshot, snapshotError := model.GetVirtualModelExecutionSnapshot(virtualModel.ID)
	// 喵~防御：快照可用时取真实模型名作为节点标签，引用上游且为空时回退到上游显示名喵。
	if snapshotError == nil && snapshot != nil {
		for _, snapshotCandidate := range snapshot.Candidates {
			if snapshotCandidate.CandidateID == candidateID {
				label = snapshotCandidate.RealModelName
				if label == "" {
					label = resolveVirtualCandidateUpstreamLabel(snapshotCandidate.UpstreamModelID, virtualModel.OwnerUserID)
				}
				break
			}
		}
	}
	payload := buildVirtualModelCandidateStatusPayload(virtualModel, candidateID, label)
	common.ApiSuccess(c, payload)
}

// parseVirtualModelCandidateID 解析并限制路径中的候选编号喵。
func parseVirtualModelCandidateID(c *gin.Context) (int, bool) {
	candidateID, err := strconv.Atoi(c.Param("candidateId"))
	if err != nil || candidateID <= 0 {
		virtualModelNotFound(c)
		return 0, false
	}
	return candidateID, true
}

// virtualModelStatusPayload 虚拟模型整体状态响应喵。
// 保留 enabled/candidate_count/enabled_candidates 旧字段，兼容 Overview 既有展示喵。
type virtualModelStatusPayload struct {
	Enabled           bool                            `json:"enabled"`
	CandidateCount    int                             `json:"candidate_count"`
	EnabledCandidates int                             `json:"enabled_candidates"`
	Availability      float64                         `json:"availability"`
	AvgLatencyMs      int64                           `json:"avg_latency_ms"`
	AvgTtftMs         int64                           `json:"avg_ttft_ms"`
	CacheHitRate      float64                         `json:"cache_hit_rate"`
	TotalTokens       int64                           `json:"total_tokens"`
	RequestCount      int64                           `json:"request_count"`
	Availability24    []float64                       `json:"availability_24h"`
	Series            []perfmetrics.EntityProbeBucket `json:"series"`
	LastAt            int64                           `json:"last_at"`
	LastSuccess       bool                            `json:"last_success"`
	LastLatencyMs     int64                           `json:"last_latency_ms"`
	LastError         string                          `json:"last_error"`
	// LastFailureAt 最近一次失败调用时间戳，即使最近一次调用成功也保留喵。
	LastFailureAt int64 `json:"last_failure_at"`
	// LastFailureError 最近一次失败调用错误分类，即使最近一次调用成功也保留喵。
	LastFailureError string                               `json:"last_failure_error"`
	Candidates       []virtualModelCandidateStatusPayload `json:"candidates"`
	// CurrentRequests 当前处理中的客户端请求数喵。
	CurrentRequests int64 `json:"current_requests"`
	// ActiveRequests 活跃请求详情列表，展示当前调用链喵。
	ActiveRequests []middleware.ActiveRequestInfo `json:"active_requests"`
}

// virtualModelCandidateStatusPayload 虚拟模型候选节点状态摘要喵。
type virtualModelCandidateStatusPayload struct {
	CandidateID  int                             `json:"candidate_id"`
	Label        string                          `json:"label"`
	Availability float64                         `json:"availability"`
	AvgLatencyMs int64                           `json:"avg_latency_ms"`
	AvgTtftMs    int64                           `json:"avg_ttft_ms"`
	CacheHitRate float64                         `json:"cache_hit_rate"`
	TotalTokens  int64                           `json:"total_tokens"`
	RequestCount int64                           `json:"request_count"`
	Series       []perfmetrics.EntityProbeBucket `json:"series"`
	LastAt       int64                           `json:"last_at"`
	LastSuccess  bool                            `json:"last_success"`
	LastError    string                          `json:"last_error"`
	// LastFailureAt 最近一次失败调用时间戳，即使最近一次调用成功也保留喵。
	LastFailureAt int64 `json:"last_failure_at"`
	// LastFailureError 最近一次失败调用错误分类，即使最近一次调用成功也保留喵。
	LastFailureError string `json:"last_failure_error"`
}

// resolveVirtualCandidateUpstreamLabel 候选引用用户上游模型且直填真实模型名为空时，回退解析上游模型显示名喵。
func resolveVirtualCandidateUpstreamLabel(upstreamModelID *int64, ownerUserID int) string {
	// 喵~防御：无引用或非法编号直接返回空喵。
	if upstreamModelID == nil || *upstreamModelID <= 0 {
		return ""
	}
	upstreamModel, queryError := model.GetUserUpstreamModelByOwnerID(*upstreamModelID, ownerUserID)
	// 喵~防御：上游模型缺失或越权时不回填，避免把私有名称泄露给状态视图喵。
	if queryError != nil || upstreamModel == nil {
		return ""
	}
	return upstreamModel.UserUpstreamModelName()
}

// buildVirtualModelStatusPayload 组装虚拟模型整体与候选节点摘要的状态载荷喵。
func buildVirtualModelStatusPayload(virtualModel *model.VirtualModel, snapshot *model.VirtualModelExecutionSnapshot) virtualModelStatusPayload {
	payload := virtualModelStatusPayload{Candidates: []virtualModelCandidateStatusPayload{}}
	// 喵~防御：空模型对象直接返回空载荷喵。
	if virtualModel == nil {
		return payload
	}
	// 实时活跃请求：从内存注册表读取当前处理请求数与当前调用链详情喵。
	payload.CurrentRequests, payload.ActiveRequests = middleware.GetVirtualModelActiveRequests(int64(virtualModel.ID))
	status, queryError := perfmetrics.QueryEntityProbeStatus(virtualModel.VirtualModelName(), perfmetrics.EntityProbeGroupSelf, entityProbeWindowHours)
	// 喵~防御：聚合查询失败按空数据返回，不阻塞状态展示喵。
	if queryError != nil {
		common.SysError("query virtual model entity probe status failed: " + queryError.Error())
	}
	payload.Availability = status.Availability
	payload.AvgLatencyMs = status.AvgLatencyMs
	payload.RequestCount = status.RequestCount
	payload.Availability24 = status.Availability24
	// 富系列：TTFT、缓存命中率与 token 消耗逐桶明细，供 Overview 图表喵。
	detailed, detailedError := perfmetrics.QueryEntityProbeStatusDetailed(virtualModel.VirtualModelName(), perfmetrics.EntityProbeGroupSelf, entityProbeWindowHours)
	// 喵~防御：富系列查询失败按空数据返回，不阻塞状态展示喵。
	if detailedError != nil {
		common.SysError("query virtual model entity probe detailed failed: " + detailedError.Error())
	} else {
		payload.AvgTtftMs = detailed.AvgTtftMs
		payload.CacheHitRate = detailed.CacheHitRate
		payload.TotalTokens = detailed.TotalTokens
		payload.Series = detailed.Series
	}
	if state, stateError := model.GetEntityProbeState(model.EntityProbeScopeVirtual, int64(virtualModel.ID)); stateError == nil && state != nil {
		payload.LastAt = state.LastAt
		payload.LastSuccess = state.LastSuccess
		payload.LastLatencyMs = state.LastLatencyMs
		payload.LastError = state.LastError
		// 最近一次失败独立保留，即使最近一次调用成功也能看到失败历史喵。
		payload.LastFailureAt = state.LastFailureAt
		payload.LastFailureError = state.LastFailureError
	}
	// 启用候选快照逐个生成节点摘要喵。
	if snapshot != nil {
		for _, candidate := range snapshot.Candidates {
			// 候选直填真实模型名为空时回退到引用上游模型的显示名，避免 Candidates 标签空白喵。
			label := candidate.RealModelName
			if label == "" {
				label = resolveVirtualCandidateUpstreamLabel(candidate.UpstreamModelID, virtualModel.OwnerUserID)
			}
			payload.Candidates = append(payload.Candidates, buildVirtualModelCandidateStatusPayload(virtualModel, candidate.CandidateID, label))
		}
	}
	return payload
}

// buildVirtualModelCandidateStatusPayload 组装单个候选节点的状态摘要喵。
func buildVirtualModelCandidateStatusPayload(virtualModel *model.VirtualModel, candidateID int, label string) virtualModelCandidateStatusPayload {
	candidatePayload := virtualModelCandidateStatusPayload{CandidateID: candidateID, Label: label}
	// 喵~防御：空模型对象直接返回空载荷喵。
	if virtualModel == nil {
		return candidatePayload
	}
	probeModelName := fmt.Sprintf("%s/candidate/%d", virtualModel.VirtualModelName(), candidateID)
	status, queryError := perfmetrics.QueryEntityProbeStatus(probeModelName, perfmetrics.EntityProbeGroupSelf, entityProbeWindowHours)
	// 喵~防御：聚合查询失败按空数据返回，不阻塞状态展示喵。
	if queryError != nil {
		common.SysError("query virtual model candidate entity probe status failed: " + queryError.Error())
	}
	candidatePayload.Availability = status.Availability
	candidatePayload.AvgLatencyMs = status.AvgLatencyMs
	candidatePayload.RequestCount = status.RequestCount
	// 富系列：候选节点的 TTFT、缓存命中率与 token 消耗逐桶明细，供性能抽屉图表喵。
	detailed, detailedError := perfmetrics.QueryEntityProbeStatusDetailed(probeModelName, perfmetrics.EntityProbeGroupSelf, entityProbeWindowHours)
	// 喵~防御：富系列查询失败按空数据返回，不阻塞状态展示喵。
	if detailedError != nil {
		common.SysError("query virtual model candidate entity probe detailed failed: " + detailedError.Error())
	} else {
		candidatePayload.AvgTtftMs = detailed.AvgTtftMs
		candidatePayload.CacheHitRate = detailed.CacheHitRate
		candidatePayload.TotalTokens = detailed.TotalTokens
		candidatePayload.Series = detailed.Series
	}
	if state, stateError := model.GetEntityProbeState(model.EntityProbeScopeVirtualCandidate, int64(candidateID)); stateError == nil && state != nil {
		candidatePayload.LastAt = state.LastAt
		candidatePayload.LastSuccess = state.LastSuccess
		candidatePayload.LastError = state.LastError
		// 最近一次失败独立保留，即使最近一次调用成功也能看到失败历史喵。
		candidatePayload.LastFailureAt = state.LastFailureAt
		candidatePayload.LastFailureError = state.LastFailureError
	}
	return candidatePayload
}

// countEnabledVirtualCandidates 统计当前配置中启用而非实时健康的候选数量喵。
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
