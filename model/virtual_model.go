package model

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// VirtualModelSourceType 标记候选来自 new-api 内部模型还是用户自定义上游喵。
type VirtualModelSourceType string

const (
	// VirtualModelSourceInternal 表示候选复用 new-api 的分组和模型喵。
	VirtualModelSourceInternal VirtualModelSourceType = "internal"
	// VirtualModelSourceCustom 表示候选使用用户自己的上游凭据喵。
	VirtualModelSourceCustom VirtualModelSourceType = "custom"
)

// VirtualModelFailureAction 描述候选失败后的编排动作喵。
type VirtualModelFailureAction string

const (
	// VirtualModelActionRetry 表示在预算内重试当前自定义候选喵。
	VirtualModelActionRetry VirtualModelFailureAction = "retry"
	// VirtualModelActionNext 表示跳过当前候选并进入下一候选喵。
	VirtualModelActionNext VirtualModelFailureAction = "next"
	// VirtualModelActionFreeze 表示冻结当前自定义候选后进入下一候选喵。
	VirtualModelActionFreeze VirtualModelFailureAction = "freeze"
	// VirtualModelActionPassthrough 表示立即把当前错误返回客户端喵。
	VirtualModelActionPassthrough VirtualModelFailureAction = "passthrough"
)

// VirtualModelAuthStyle 标记自定义上游采用的认证头语义喵。
type VirtualModelAuthStyle string

const (
	// VirtualModelAuthBearer 表示使用 Authorization Bearer 认证喵。
	VirtualModelAuthBearer VirtualModelAuthStyle = "bearer"
	// VirtualModelAuthAPIKey 表示使用 x-api-key 认证喵。
	VirtualModelAuthAPIKey VirtualModelAuthStyle = "x-api-key"
	// VirtualModelAuthAnthropic 表示使用 Anthropic x-api-key 认证喵。
	VirtualModelAuthAnthropic VirtualModelAuthStyle = "anthropic-x-api-key"
)

// VirtualModel 保存用户私有虚拟模型的主配置喵。
type VirtualModel struct {
	ID                  int            `json:"id" gorm:"primaryKey"`
	OwnerUserID         int            `json:"owner_user_id" gorm:"index;not null;uniqueIndex:idx_virtual_model_owner_name,priority:1"`
	NormalizedName      string         `json:"normalized_name" gorm:"type:varchar(128);not null;uniqueIndex:idx_virtual_model_owner_name,priority:2"`
	DisplayName         string         `json:"display_name" gorm:"type:varchar(128);not null"`
	Enabled             bool           `json:"enabled" gorm:"default:true"`
	LoopEnabled         bool           `json:"loop_enabled"`
	TotalTimeoutSeconds int            `json:"total_timeout_seconds" gorm:"default:120"`
	MaxLoopRounds       int            `json:"max_loop_rounds" gorm:"default:1"`
	Version             int64          `json:"version" gorm:"default:1"`
	CreatedTime         int64          `json:"created_time" gorm:"bigint"`
	UpdatedTime         int64          `json:"updated_time" gorm:"bigint"`
	DeletedAt           gorm.DeletedAt `json:"-" gorm:"index"`
}

// VirtualModelCandidate 保存候选顺序和通用运行参数喵。
type VirtualModelCandidate struct {
	ID             int                    `json:"id" gorm:"primaryKey"`
	VirtualModelID int                    `json:"virtual_model_id" gorm:"index;not null;uniqueIndex:idx_virtual_model_candidate_order,priority:1"`
	StableOrder    int                    `json:"stable_order" gorm:"not null;uniqueIndex:idx_virtual_model_candidate_order,priority:2"`
	SourceType     VirtualModelSourceType `json:"source_type" gorm:"type:varchar(16);not null"`
	Enabled        bool                   `json:"enabled" gorm:"default:true"`
	MaxRetries     int                    `json:"max_retries" gorm:"default:0"`
	TimeoutSeconds int                    `json:"timeout_seconds" gorm:"default:60"`
	Version        int64                  `json:"version" gorm:"default:1"`
	CreatedTime    int64                  `json:"created_time" gorm:"bigint"`
	UpdatedTime    int64                  `json:"updated_time" gorm:"bigint"`
	DeletedAt      gorm.DeletedAt         `json:"-" gorm:"index"`
}

// VirtualModelInternalCandidate 保存 new-api 内部候选的分组和真实模型喵。
type VirtualModelInternalCandidate struct {
	ID            int    `json:"id" gorm:"primaryKey"`
	CandidateID   int    `json:"candidate_id" gorm:"uniqueIndex;not null"`
	GroupName     string `json:"group_name" gorm:"type:varchar(128);not null"`
	RealModelName string `json:"real_model_name" gorm:"type:varchar(256);not null"`
}

// VirtualModelCustomCandidate 保存加密后的自定义上游配置和脱敏摘要喵。
type VirtualModelCustomCandidate struct {
	ID                 int                   `json:"id" gorm:"primaryKey"`
	CandidateID        int                   `json:"candidate_id" gorm:"uniqueIndex;not null"`
	EncryptedBaseURL   string                `json:"-" gorm:"type:text;not null"`
	EncryptedAPIKey    string                `json:"-" gorm:"type:text;not null"`
	CredentialVersion  int                   `json:"credential_version" gorm:"default:1"`
	BaseURLSummary     string                `json:"base_url_summary" gorm:"type:varchar(255)"`
	BaseURLFingerprint string                `json:"-" gorm:"type:varchar(128);index"`
	APIKeyFingerprint  string                `json:"-" gorm:"type:varchar(128);index"`
	RealModelName      string                `json:"real_model_name" gorm:"type:varchar(256);not null"`
	AuthStyle          VirtualModelAuthStyle `json:"auth_style" gorm:"type:varchar(32);not null"`
}

// VirtualModelFailureRule 保存候选级有序失败规则喵。
type VirtualModelFailureRule struct {
	ID            int                       `json:"id" gorm:"primaryKey"`
	CandidateID   int                       `json:"candidate_id" gorm:"index;not null;uniqueIndex:idx_virtual_model_failure_rule_order,priority:1"`
	RuleOrder     int                       `json:"rule_order" gorm:"not null;uniqueIndex:idx_virtual_model_failure_rule_order,priority:2"`
	HTTPStatus    int                       `json:"http_status"`
	ErrorClass    string                    `json:"error_class" gorm:"type:varchar(64)"`
	BodyRegex     string                    `json:"body_regex" gorm:"type:text"`
	Action        VirtualModelFailureAction `json:"action" gorm:"type:varchar(16);not null"`
	FreezeSeconds int                       `json:"freeze_seconds"`
	Version       int64                     `json:"version" gorm:"default:1"`
}

// VirtualModelTokenBinding 保存模型和用户 API Key 的显式授权关系喵。
type VirtualModelTokenBinding struct {
	ID             int   `json:"id" gorm:"primaryKey"`
	VirtualModelID int   `json:"virtual_model_id" gorm:"not null;uniqueIndex:idx_virtual_model_token_binding,priority:1"`
	TokenID        int   `json:"token_id" gorm:"not null;uniqueIndex:idx_virtual_model_token_binding,priority:2"`
	OwnerUserID    int   `json:"owner_user_id" gorm:"index;not null"`
	CreatedTime    int64 `json:"created_time" gorm:"bigint"`
}

// VirtualModelManualFreeze 保存用户手动冻结候选的时间范围喵。
type VirtualModelManualFreeze struct {
	ID          int   `json:"id" gorm:"primaryKey"`
	CandidateID int   `json:"candidate_id" gorm:"index;not null"`
	OperatorID  int   `json:"operator_id" gorm:"index;not null"`
	StartedAt   int64 `json:"started_at" gorm:"bigint"`
	ExpiresAt   int64 `json:"expires_at" gorm:"bigint"`
}

// VirtualModelCustomFreezeState 保存用户范围内共享的自动冻结状态喵。
type VirtualModelCustomFreezeState struct {
	ID               int    `json:"id" gorm:"primaryKey"`
	OwnerUserID      int    `json:"owner_user_id" gorm:"index;not null;uniqueIndex:idx_virtual_custom_freeze_identity,priority:1"`
	IdentityDigest   string `json:"identity_digest" gorm:"type:varchar(128);not null;uniqueIndex:idx_virtual_custom_freeze_identity,priority:2"`
	FrozenUntil      int64  `json:"frozen_until" gorm:"bigint"`
	ConsecutiveFails int    `json:"consecutive_fails"`
	LastFailureClass string `json:"last_failure_class" gorm:"type:varchar(64)"`
	UpdatedTime      int64  `json:"updated_time" gorm:"bigint"`
}

// VirtualModelAuditLog 只保存不可还原的资源操作摘要喵。
type VirtualModelAuditLog struct {
	ID             int    `json:"id" gorm:"primaryKey"`
	VirtualModelID int    `json:"virtual_model_id" gorm:"index"`
	OwnerUserID    int    `json:"owner_user_id" gorm:"index;not null"`
	OperatorID     int    `json:"operator_id" gorm:"index;not null"`
	Action         string `json:"action" gorm:"type:varchar(64);not null"`
	SummaryDigest  string `json:"summary_digest" gorm:"type:varchar(128)"`
	CreatedTime    int64  `json:"created_time" gorm:"bigint"`
}

var virtualModelNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// NormalizeVirtualModelName 校验并规范化 virtual/ 后面的资源名称喵。
func NormalizeVirtualModelName(input string) (string, error) {
	// 喵~防御：拒绝空值、路径分隔符、非 ASCII 字符和超长名称，避免资源穿透与歧义喵。
	name := strings.TrimSpace(input)
	if strings.HasPrefix(name, "virtual/") {
		name = strings.TrimPrefix(name, "virtual/")
	}
	if name == "" || len(name) > 96 || !virtualModelNamePattern.MatchString(name) {
		return "", errors.New("虚拟模型名称只允许 ASCII 字母、数字、短横线和下划线")
	}
	return strings.ToLower(name), nil
}

// VirtualModelName 返回对外使用的 virtual/ 模型名称喵。
func (virtualModel *VirtualModel) VirtualModelName() string {
	if virtualModel == nil {
		return ""
	}
	return "virtual/" + virtualModel.NormalizedName
}

// VirtualModelFunctionEnabled 读取独立功能开关，默认关闭以保护既有请求链喵。
func VirtualModelFunctionEnabled() bool {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return common.OptionMap["VirtualModelEnabled"] == "true"
}

// ValidateVirtualModelConfiguration 校验保存前的模型配置和系统硬上限喵。
func ValidateVirtualModelConfiguration(virtualModel *VirtualModel) error {
	// 喵~防御：拒绝空对象和非法所有者，避免绕过用户隔离喵。
	if virtualModel == nil || virtualModel.OwnerUserID <= 0 {
		return errors.New("虚拟模型所有者无效")
	}
	if _, err := NormalizeVirtualModelName(virtualModel.NormalizedName); err != nil {
		return err
	}
	if strings.TrimSpace(virtualModel.DisplayName) == "" || len(virtualModel.DisplayName) > 128 {
		return errors.New("虚拟模型显示名称无效")
	}
	if virtualModel.TotalTimeoutSeconds < 1 || virtualModel.TotalTimeoutSeconds > 3600 {
		return errors.New("虚拟模型总超时必须介于 1 和 3600 秒之间")
	}
	if virtualModel.MaxLoopRounds < 1 || virtualModel.MaxLoopRounds > 100 {
		return errors.New("虚拟模型最大循环轮数必须介于 1 和 100 轮之间")
	}
	return nil
}

// ValidateVirtualModelCandidate 校验候选的来源、顺序和重试边界喵。
func ValidateVirtualModelCandidate(candidate *VirtualModelCandidate) error {
	// 喵~防御：拒绝空候选、非法来源、负数参数和过大的资源消耗上限喵。
	if candidate == nil || candidate.VirtualModelID <= 0 {
		return errors.New("虚拟模型候选无效")
	}
	if candidate.StableOrder < 0 || candidate.MaxRetries < 0 || candidate.MaxRetries > 20 || candidate.TimeoutSeconds < 1 || candidate.TimeoutSeconds > 600 {
		return errors.New("虚拟模型候选参数超出允许范围")
	}
	if candidate.SourceType != VirtualModelSourceInternal && candidate.SourceType != VirtualModelSourceCustom {
		return errors.New("虚拟模型候选来源无效")
	}
	return nil
}

// GetVirtualModelByOwnerName 只按所有者和规范化名称读取资源，防止跨用户枚举喵。
func GetVirtualModelByOwnerName(ownerUserID int, normalizedName string) (*VirtualModel, error) {
	// 喵~防御：查询前强制校验所有者和名称，拒绝空条件查询喵。
	if ownerUserID <= 0 || normalizedName == "" {
		return nil, gorm.ErrRecordNotFound
	}
	virtualModel := &VirtualModel{}
	err := DB.Where("owner_user_id = ? AND normalized_name = ?", ownerUserID, normalizedName).First(virtualModel).Error
	return virtualModel, err
}

// GetVirtualModelsByOwnerToken 查询当前用户通过指定 API Key 显式授权的启用模型喵。
func GetVirtualModelsByOwnerToken(ownerUserID int, tokenID int) ([]VirtualModel, error) {
	// 喵~防御：身份或 API Key 无效时拒绝执行无条件查询，避免越权读取喵。
	if ownerUserID <= 0 || tokenID <= 0 {
		return []VirtualModel{}, nil
	}
	virtualModels := make([]VirtualModel, 0)
	queryError := DB.Model(&VirtualModel{}).
		Joins("JOIN virtual_model_token_bindings ON virtual_model_token_bindings.virtual_model_id = virtual_models.id").
		Where("virtual_models.owner_user_id = ? AND virtual_model_token_bindings.owner_user_id = ? AND virtual_model_token_bindings.token_id = ? AND virtual_models.enabled = ?", ownerUserID, ownerUserID, tokenID, true).
		Order("virtual_models.normalized_name ASC").
		Find(&virtualModels).Error
	return virtualModels, queryError
}

// GetEnabledVirtualModelByOwnerTokenName 查询指定 API Key 可调用的单个启用虚拟模型喵。
func GetEnabledVirtualModelByOwnerTokenName(ownerUserID int, tokenID int, normalizedName string) (*VirtualModel, error) {
	// 喵~防御：拒绝无效身份、API Key 或模型名称，防止空条件命中资源喵。
	if ownerUserID <= 0 || tokenID <= 0 || normalizedName == "" {
		return nil, gorm.ErrRecordNotFound
	}
	virtualModel := &VirtualModel{}
	queryError := DB.Model(&VirtualModel{}).
		Joins("JOIN virtual_model_token_bindings ON virtual_model_token_bindings.virtual_model_id = virtual_models.id").
		Where("virtual_models.owner_user_id = ? AND virtual_model_token_bindings.owner_user_id = ? AND virtual_model_token_bindings.token_id = ? AND virtual_models.enabled = ? AND virtual_models.normalized_name = ?", ownerUserID, ownerUserID, tokenID, true, normalizedName).
		First(virtualModel).Error
	return virtualModel, queryError
}

// VirtualModelCandidateSnapshot 保存一次虚拟模型请求的候选不可变执行配置喵。
type VirtualModelCandidateSnapshot struct {
	CandidateID        int                    // 候选唯一编号，用于冻结、规则和审计关联喵。
	VirtualModelID     int                    // 虚拟模型唯一编号，用于校验候选归属喵。
	StableOrder        int                    // 候选稳定顺序，数值越小越优先喵。
	SourceType         VirtualModelSourceType // 候选来源类型，决定内部 relay 或自定义透传路径喵。
	Enabled            bool                   // 候选是否启用，禁用候选不会进入本请求快照喵。
	MaxRetries         int                    // 自定义候选的最大附加重试次数，单位：次喵。
	TimeoutSeconds     int                    // 单次候选执行超时，单位：秒喵。
	GroupName          string                 // 内部候选目标分组，自定义候选为空喵。
	RealModelName      string                 // 上游或内部实际请求模型名称喵。
	EncryptedBaseURL   string                 // 自定义候选加密后的上游基址，内部候选为空喵。
	BaseURLSummary     string                 // 自定义候选公开地址摘要，仅用于旧记录冻结身份兼容喵。
	BaseURLFingerprint string                 // 自定义候选规范化地址不可逆摘要，用于共享冻结身份喵。
	EncryptedAPIKey    string                 // 自定义候选加密后的认证凭据，内部候选为空喵。
	APIKeyFingerprint  string                 // 自定义候选 API Key 不可逆摘要，用于共享冻结身份喵。
	CredentialVersion  int                    // 自定义候选凭据加密版本喵。
	AuthStyle          VirtualModelAuthStyle  // 自定义候选认证头样式喵。
}

// VirtualModelInternalCandidateSnapshot 保留旧名称兼容现有内部候选调用代码喵。
type VirtualModelInternalCandidateSnapshot = VirtualModelCandidateSnapshot

// VirtualModelExecutionSnapshot 保存一次请求所需的候选链和失败规则不可变读取结果喵。
type VirtualModelExecutionSnapshot struct {
	Candidates                []VirtualModelCandidateSnapshot   // 已按稳定顺序排列的启用候选快照喵。
	FailureRulesByCandidateID map[int][]VirtualModelFailureRule // 按候选编号归类且已排序的失败规则快照喵。
}

// GetVirtualModelExecutionSnapshot 在单个只读事务中构造候选和规则的不可变执行快照喵。
func GetVirtualModelExecutionSnapshot(virtualModelID int) (*VirtualModelExecutionSnapshot, error) {
	// 喵~防御：无效模型编号直接拒绝，避免开始无意义事务或查询全表喵。
	if virtualModelID <= 0 {
		return nil, gorm.ErrRecordNotFound
	}
	executionSnapshot := &VirtualModelExecutionSnapshot{Candidates: make([]VirtualModelCandidateSnapshot, 0), FailureRulesByCandidateID: make(map[int][]VirtualModelFailureRule)}
	transactionError := DB.Transaction(func(transactionDatabase *gorm.DB) error {
		candidateSnapshots, candidateError := GetEnabledVirtualModelCandidateSnapshotsWithDB(transactionDatabase, virtualModelID)
		if candidateError != nil {
			return candidateError
		}
		candidateIDs := make([]int, 0, len(candidateSnapshots))
		for _, candidateSnapshot := range candidateSnapshots {
			candidateIDs = append(candidateIDs, candidateSnapshot.CandidateID)
		}
		failureRulesByCandidateID, rulesError := GetVirtualModelFailureRulesByCandidateIDsWithDB(transactionDatabase, candidateIDs)
		if rulesError != nil {
			return rulesError
		}
		executionSnapshot.Candidates = candidateSnapshots
		executionSnapshot.FailureRulesByCandidateID = failureRulesByCandidateID
		return nil
	})
	if transactionError != nil {
		return nil, transactionError
	}
	return executionSnapshot, nil
}

// GetEnabledVirtualModelCandidateSnapshots 使用给定数据库连接按稳定顺序读取本次请求的所有启用候选快照喵。
func GetEnabledVirtualModelCandidateSnapshotsWithDB(database *gorm.DB, virtualModelID int) ([]VirtualModelCandidateSnapshot, error) {
	// 喵~防御：拒绝无效模型编号，避免候选查询退化为全表扫描喵。
	if virtualModelID <= 0 {
		return []VirtualModelCandidateSnapshot{}, gorm.ErrRecordNotFound
	}
	// 喵~防御：数据库连接为空时拒绝查询，避免运行时空指针或绕过调用方事务边界喵。
	if database == nil {
		return nil, errors.New("virtual model database is unavailable")
	}
	candidateSnapshots := make([]VirtualModelCandidateSnapshot, 0)
	queryError := database.Model(&VirtualModelCandidate{}).
		Select("virtual_model_candidates.id AS candidate_id, virtual_model_candidates.virtual_model_id, virtual_model_candidates.stable_order, virtual_model_candidates.source_type, virtual_model_candidates.enabled, virtual_model_candidates.max_retries, virtual_model_candidates.timeout_seconds, virtual_model_internal_candidates.group_name, CASE WHEN virtual_model_candidates.source_type = ? THEN virtual_model_internal_candidates.real_model_name ELSE virtual_model_custom_candidates.real_model_name END AS real_model_name, virtual_model_custom_candidates.encrypted_base_url, virtual_model_custom_candidates.base_url_summary, virtual_model_custom_candidates.base_url_fingerprint, virtual_model_custom_candidates.encrypted_api_key, virtual_model_custom_candidates.api_key_fingerprint, virtual_model_custom_candidates.credential_version, virtual_model_custom_candidates.auth_style", VirtualModelSourceInternal).
		Joins("LEFT JOIN virtual_model_internal_candidates ON virtual_model_internal_candidates.candidate_id = virtual_model_candidates.id AND virtual_model_candidates.source_type = ?", VirtualModelSourceInternal).
		Joins("LEFT JOIN virtual_model_custom_candidates ON virtual_model_custom_candidates.candidate_id = virtual_model_candidates.id AND virtual_model_candidates.source_type = ?", VirtualModelSourceCustom).
		Where("virtual_model_candidates.virtual_model_id = ? AND virtual_model_candidates.enabled = ?", virtualModelID, true).
		Order("virtual_model_candidates.stable_order ASC, virtual_model_candidates.id ASC").
		Find(&candidateSnapshots).Error
	return candidateSnapshots, queryError
}

// GetEnabledVirtualModelCandidateSnapshots 按稳定顺序读取本次请求的所有启用候选快照喵。
func GetEnabledVirtualModelCandidateSnapshots(virtualModelID int) ([]VirtualModelCandidateSnapshot, error) {
	return GetEnabledVirtualModelCandidateSnapshotsWithDB(DB, virtualModelID)
}

// GetVirtualModelFailureRulesByCandidateIDs 使用给定数据库连接按候选和规则顺序读取失败规则快照喵。
func GetVirtualModelFailureRulesByCandidateIDsWithDB(database *gorm.DB, candidateIDs []int) (map[int][]VirtualModelFailureRule, error) {
	// 喵~防御：空候选集合直接返回空映射，避免生成 IN (NULL) 或无条件查询喵。
	if len(candidateIDs) == 0 {
		return map[int][]VirtualModelFailureRule{}, nil
	}
	// 喵~防御：数据库连接为空时拒绝查询，避免运行时空指针或绕过调用方事务边界喵。
	if database == nil {
		return nil, errors.New("virtual model database is unavailable")
	}
	failureRules := make([]VirtualModelFailureRule, 0)
	queryError := database.Where("candidate_id IN ?", candidateIDs).Order("candidate_id ASC, rule_order ASC, id ASC").Find(&failureRules).Error
	if queryError != nil {
		return nil, queryError
	}
	failureRulesByCandidateID := make(map[int][]VirtualModelFailureRule, len(candidateIDs))
	for _, failureRule := range failureRules {
		failureRulesByCandidateID[failureRule.CandidateID] = append(failureRulesByCandidateID[failureRule.CandidateID], failureRule)
	}
	return failureRulesByCandidateID, nil
}

// GetVirtualModelFailureRulesByCandidateIDs 按候选和规则顺序批量读取失败规则快照喵。
func GetVirtualModelFailureRulesByCandidateIDs(candidateIDs []int) (map[int][]VirtualModelFailureRule, error) {
	return GetVirtualModelFailureRulesByCandidateIDsWithDB(DB, candidateIDs)
}

// GetActiveVirtualModelManualFreezeCandidateIDs 使用给定数据库连接读取当前仍处于手动冻结期的候选编号集合喵。
func GetActiveVirtualModelManualFreezeCandidateIDsWithDB(database *gorm.DB, candidateIDs []int, currentTimestamp int64) (map[int]bool, error) {
	// 喵~防御：空候选集合或非法时间戳不执行数据库查询，避免无效条件扩大读取范围喵。
	if len(candidateIDs) == 0 || currentTimestamp <= 0 {
		return map[int]bool{}, nil
	}
	// 喵~防御：数据库连接为空时拒绝查询，避免运行时空指针或绕过调用方事务边界喵。
	if database == nil {
		return nil, errors.New("virtual model database is unavailable")
	}
	frozenCandidateIDs := make([]int, 0)
	queryError := database.Model(&VirtualModelManualFreeze{}).Where("candidate_id IN ? AND started_at <= ? AND expires_at > ?", candidateIDs, currentTimestamp, currentTimestamp).Distinct().Pluck("candidate_id", &frozenCandidateIDs).Error
	if queryError != nil {
		return nil, queryError
	}
	frozenCandidateIDSet := make(map[int]bool, len(frozenCandidateIDs))
	for _, candidateID := range frozenCandidateIDs {
		frozenCandidateIDSet[candidateID] = true
	}
	return frozenCandidateIDSet, nil
}

// GetActiveVirtualModelManualFreezeCandidateIDs 返回当前仍处于手动冻结期的候选编号集合喵。
func GetActiveVirtualModelManualFreezeCandidateIDs(candidateIDs []int, currentTimestamp int64) (map[int]bool, error) {
	return GetActiveVirtualModelManualFreezeCandidateIDsWithDB(DB, candidateIDs, currentTimestamp)
}

// GetFirstEnabledVirtualModelCandidate 读取候选链首个启用候选，保持配置顺序的不可变选择语义喵。
func GetFirstEnabledVirtualModelCandidate(virtualModelID int) (*VirtualModelInternalCandidateSnapshot, error) {
	candidateSnapshots, queryError := GetEnabledVirtualModelCandidateSnapshots(virtualModelID)
	if queryError != nil {
		return nil, queryError
	}
	// 喵~防御：候选链为空时返回统一的记录不存在错误，避免调用方误把零值候选当作有效配置喵。
	if len(candidateSnapshots) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &candidateSnapshots[0], nil
}

// GetVirtualModelCustomFreezeStates 使用给定数据库连接查询当前用户可见自定义候选身份的自动冻结状态喵。
func GetVirtualModelCustomFreezeStatesWithDB(database *gorm.DB, ownerUserID int, identityDigests []string, currentTimestamp int64) (map[string]VirtualModelCustomFreezeState, error) {
	// 喵~防御：无效 owner、空身份集合或非法时间不执行查询，避免跨用户或全表读取喵。
	if ownerUserID <= 0 || len(identityDigests) == 0 || currentTimestamp <= 0 {
		return map[string]VirtualModelCustomFreezeState{}, nil
	}
	// 喵~防御：数据库连接为空时拒绝查询，避免运行时空指针或绕过调用方事务边界喵。
	if database == nil {
		return nil, errors.New("virtual model database is unavailable")
	}
	freezeStates := make([]VirtualModelCustomFreezeState, 0)
	queryError := database.Where("owner_user_id = ? AND identity_digest IN ? AND frozen_until > ?", ownerUserID, identityDigests, currentTimestamp).Find(&freezeStates).Error
	if queryError != nil {
		return nil, queryError
	}
	freezeStatesByIdentity := make(map[string]VirtualModelCustomFreezeState, len(freezeStates))
	for _, freezeState := range freezeStates {
		freezeStatesByIdentity[freezeState.IdentityDigest] = freezeState
	}
	return freezeStatesByIdentity, nil
}

// GetVirtualModelCustomFreezeStates 查询当前用户可见自定义候选身份的自动冻结状态喵。
func GetVirtualModelCustomFreezeStates(ownerUserID int, identityDigests []string, currentTimestamp int64) (map[string]VirtualModelCustomFreezeState, error) {
	return GetVirtualModelCustomFreezeStatesWithDB(DB, ownerUserID, identityDigests, currentTimestamp)
}

// UpsertVirtualModelCustomFreezeState 使用给定数据库连接在 owner 范围内更新自定义上游自动冻结状态喵。
func UpsertVirtualModelCustomFreezeStateWithDB(database *gorm.DB, ownerUserID int, identityDigest string, frozenUntil int64, failureClass string, currentTimestamp int64) error {
	// 喵~防御：缺少身份、所有者或时间时拒绝写入，避免创建无法隔离或永不过期的冻结状态喵。
	if ownerUserID <= 0 || strings.TrimSpace(identityDigest) == "" || frozenUntil <= currentTimestamp || currentTimestamp <= 0 {
		return errors.New("virtual model custom freeze state is invalid")
	}
	// 喵~防御：数据库连接为空时拒绝写入，避免运行时空指针或绕过调用方事务边界喵。
	if database == nil {
		return errors.New("virtual model database is unavailable")
	}
	// 喵~防御：使用数据库原子 upsert，避免并发首次冻结时唯一键竞争导致请求被错误拒绝喵。
	return database.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "owner_user_id"}, {Name: "identity_digest"}},
		DoUpdates: clause.Assignments(map[string]any{
			// 喵~防御：并发失败不得将更长的既有冻结缩短为较短的新冻结时间喵。
			"frozen_until":       gorm.Expr("CASE WHEN frozen_until > ? THEN frozen_until ELSE ? END", frozenUntil, frozenUntil),
			"consecutive_fails":  gorm.Expr("consecutive_fails + ?", 1),
			"last_failure_class": strings.TrimSpace(failureClass),
			"updated_time":       currentTimestamp,
		}),
	}).Create(&VirtualModelCustomFreezeState{OwnerUserID: ownerUserID, IdentityDigest: identityDigest, FrozenUntil: frozenUntil, ConsecutiveFails: 1, LastFailureClass: strings.TrimSpace(failureClass), UpdatedTime: currentTimestamp}).Error
}

// UpsertVirtualModelCustomFreezeState 在 owner 范围内更新自定义上游自动冻结状态喵。
func UpsertVirtualModelCustomFreezeState(ownerUserID int, identityDigest string, frozenUntil int64, failureClass string, currentTimestamp int64) error {
	return UpsertVirtualModelCustomFreezeStateWithDB(DB, ownerUserID, identityDigest, frozenUntil, failureClass, currentTimestamp)
}

// ClearVirtualModelCustomFreezeState 使用给定数据库连接清除一次成功调用对应的自动冻结失败计数喵。
func ClearVirtualModelCustomFreezeStateWithDB(database *gorm.DB, ownerUserID int, identityDigest string, currentTimestamp int64) error {
	// 喵~防御：无效输入无需触发写库，调用方成功路径可安全忽略该空操作喵。
	if ownerUserID <= 0 || strings.TrimSpace(identityDigest) == "" || currentTimestamp <= 0 {
		return nil
	}
	// 喵~防御：数据库连接为空时拒绝写入，避免运行时空指针或绕过调用方事务边界喵。
	if database == nil {
		return errors.New("virtual model database is unavailable")
	}
	return database.Where("owner_user_id = ? AND identity_digest = ?", ownerUserID, identityDigest).Updates(map[string]any{"frozen_until": 0, "consecutive_fails": 0, "last_failure_class": "", "updated_time": currentTimestamp}).Error
}

// ClearVirtualModelCustomFreezeState 清除一次成功调用对应的自动冻结失败计数喵。
func ClearVirtualModelCustomFreezeState(ownerUserID int, identityDigest string, currentTimestamp int64) error {
	return ClearVirtualModelCustomFreezeStateWithDB(DB, ownerUserID, identityDigest, currentTimestamp)
}

// DeleteVirtualModelByOwner 在事务内删除所有关联数据并写入不可还原审计喵。
func DeleteVirtualModelByOwner(virtualModelID int, ownerUserID int, operatorID int) error {
	// 喵~防御：拒绝无效身份和资源编号，避免误删或全表操作喵。
	if virtualModelID <= 0 || ownerUserID <= 0 || operatorID <= 0 {
		return gorm.ErrRecordNotFound
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		virtualModel := &VirtualModel{}
		if err := tx.Where("id = ? AND owner_user_id = ?", virtualModelID, ownerUserID).First(virtualModel).Error; err != nil {
			return gorm.ErrRecordNotFound
		}
		var candidateIDs []int
		if err := tx.Model(&VirtualModelCandidate{}).Where("virtual_model_id = ?", virtualModelID).Pluck("id", &candidateIDs).Error; err != nil {
			return err
		}
		if len(candidateIDs) > 0 {
			if err := tx.Where("candidate_id IN ?", candidateIDs).Delete(&VirtualModelFailureRule{}).Error; err != nil {
				return err
			}
			if err := tx.Where("candidate_id IN ?", candidateIDs).Delete(&VirtualModelInternalCandidate{}).Error; err != nil {
				return err
			}
			if err := tx.Where("candidate_id IN ?", candidateIDs).Delete(&VirtualModelCustomCandidate{}).Error; err != nil {
				return err
			}
			if err := tx.Where("candidate_id IN ?", candidateIDs).Delete(&VirtualModelManualFreeze{}).Error; err != nil {
				return err
			}
			if err := tx.Where("candidate_id IN ?", candidateIDs).Delete(&VirtualModelCandidate{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("virtual_model_id = ?", virtualModelID).Delete(&VirtualModelTokenBinding{}).Error; err != nil {
			return err
		}
		if err := tx.Create(&VirtualModelAuditLog{VirtualModelID: virtualModelID, OwnerUserID: ownerUserID, OperatorID: operatorID, Action: "delete", SummaryDigest: fmt.Sprintf("model:%d", virtualModelID), CreatedTime: time.Now().Unix()}).Error; err != nil {
			return err
		}
		return tx.Delete(virtualModel).Error
	})
}
