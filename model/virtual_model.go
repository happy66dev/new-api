package model

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
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
	VirtualModelID int                    `json:"virtual_model_id" gorm:"index;not null"`
	StableOrder    int                    `json:"stable_order" gorm:"not null"`
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
	ID                int                   `json:"id" gorm:"primaryKey"`
	CandidateID       int                   `json:"candidate_id" gorm:"uniqueIndex;not null"`
	EncryptedBaseURL  string                `json:"-" gorm:"type:text;not null"`
	EncryptedAPIKey   string                `json:"-" gorm:"type:text;not null"`
	CredentialVersion int                   `json:"credential_version" gorm:"default:1"`
	BaseURLSummary    string                `json:"base_url_summary" gorm:"type:varchar(255)"`
	APIKeyFingerprint string                `json:"-" gorm:"type:varchar(128);index"`
	RealModelName     string                `json:"real_model_name" gorm:"type:varchar(256);not null"`
	AuthStyle         VirtualModelAuthStyle `json:"auth_style" gorm:"type:varchar(32);not null"`
}

// VirtualModelFailureRule 保存候选级有序失败规则喵。
type VirtualModelFailureRule struct {
	ID            int                       `json:"id" gorm:"primaryKey"`
	CandidateID   int                       `json:"candidate_id" gorm:"index;not null"`
	RuleOrder     int                       `json:"rule_order" gorm:"not null"`
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
