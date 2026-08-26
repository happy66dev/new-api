package model

import (
	"errors"
	"regexp"
	"strings"

	"gorm.io/gorm"
)

// UserUpstreamModelNamePattern 只允许 ASCII 字母、数字、短横线和下划线喵。
var UserUpstreamModelNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// UserUpstreamModel 表示用户私有的一个上游模型（一模型一上游）喵。
// 金额字段一律以"分"存储（RMB），前端展示转元，避免浮点误差喵。
type UserUpstreamModel struct {
	ID               int64  `json:"id" gorm:"primaryKey"`
	OwnerUserID      int    `json:"owner_user_id" gorm:"index"`
	NormalizedName   string `json:"normalized_name" gorm:"type:varchar(96);uniqueIndex:idx_upstream_owner_name"`
	DisplayName      string `json:"display_name" gorm:"type:varchar(128)"`
	Enabled          bool   `json:"enabled"`
	// 上游连接：凭据一律加密存储，绝不落明文喵。
	EncryptedBaseURL   string `json:"-" gorm:"type:text"`
	EncryptedAPIKey    string `json:"-" gorm:"type:text"`
	BaseURLFingerprint string `json:"-" gorm:"type:varchar(64)"`
	APIKeyFingerprint  string `json:"-" gorm:"type:varchar(64)"`
	CredentialVersion  int    `json:"-"`
	RealModelName      string `json:"real_model_name" gorm:"type:varchar(128)"`
	AuthStyle          string `json:"auth_style" gorm:"type:varchar(32)"`
	// 计费：价格机制参考 new-api 的 ModelRatio 体系，倍率为 decimal 字符串喵。
	ModelRatio           string `json:"model_ratio" gorm:"type:varchar(32)"`
	CompletionRatio      string `json:"completion_ratio" gorm:"type:varchar(32)"`
	CacheRatio           string `json:"cache_ratio" gorm:"type:varchar(32)"`
	CacheCreationRatio   string `json:"cache_creation_ratio" gorm:"type:varchar(32)"`
	CacheCreation5mRatio string `json:"cache_creation_5m_ratio" gorm:"type:varchar(32)"`
	CacheCreation1hRatio string `json:"cache_creation_1h_ratio" gorm:"type:varchar(32)"`
	ImageRatio           string `json:"image_ratio" gorm:"type:varchar(32)"`
	AudioRatio           string `json:"audio_ratio" gorm:"type:varchar(32)"`
	AudioCompletionRatio string `json:"audio_completion_ratio" gorm:"type:varchar(32)"`
	// 余额与额度控制（分）喵。
	BalanceCents           int64  `json:"balance_cents"`
	SpendLimitCents        int64  `json:"spend_limit_cents"`
	TotalSpentCents        int64  `json:"total_spent_cents"`
	UpstreamRemainingCents int64  `json:"upstream_remaining_cents"`
	UpstreamRemainingAt    int64  `json:"upstream_remaining_at"`
	BalanceCheckEnabled    bool   `json:"balance_check_enabled"`
	BalanceCheckPath       string `json:"balance_check_path" gorm:"type:varchar(256)"`
	// 共享配置喵。
	ShareEnabled       bool  `json:"share_enabled"`
	ShareLimitCents    int64 `json:"share_limit_cents"`
	ShareSpentCents    int64 `json:"share_spent_cents"`
	ShowBalanceEnabled bool  `json:"show_balance_enabled"`
	// 版本与时间喵。
	Version     int   `json:"version"`
	CreatedTime int64 `json:"created_time"`
	UpdatedTime int64 `json:"updated_time"`
}

// NormalizeUserUpstreamModelName 规范化用户上游模型名并拒绝危险输入喵。
func NormalizeUserUpstreamModelName(input string) (string, error) {
	// 喵~防御：拒绝空值、路径分隔符、非 ASCII 字符和超长名称，避免资源穿透与歧义喵。
	name := strings.TrimSpace(input)
	if strings.HasPrefix(name, "upstream/") {
		name = strings.TrimPrefix(name, "upstream/")
	}
	if name == "" || len(name) > 96 || !UserUpstreamModelNamePattern.MatchString(name) {
		return "", errors.New("用户上游模型名称只允许 ASCII 字母、数字、短横线和下划线")
	}
	return strings.ToLower(name), nil
}

// UserUpstreamModelName 返回对外使用的 upstream/ 模型名称喵。
func (upstreamModel *UserUpstreamModel) UserUpstreamModelName() string {
	// 喵~防御：空对象不生成名称，避免上游模型名悬空喵。
	if upstreamModel == nil {
		return ""
	}
	return "upstream/" + upstreamModel.NormalizedName
}

// GetUserUpstreamModelsByOwner 返回某用户拥有的全部上游模型喵。
func GetUserUpstreamModelsByOwner(ownerUserID int) ([]UserUpstreamModel, error) {
	// 喵~防御：无效属主直接返回空结果，避免枚举非法身份喵。
	if ownerUserID <= 0 {
		return []UserUpstreamModel{}, nil
	}
	var upstreamModels []UserUpstreamModel
	// 按创建时间倒序返回，便于前端展示最近创建的模型喵。
	if err := DB.Where("owner_user_id = ?", ownerUserID).Order("id desc").Find(&upstreamModels).Error; err != nil {
		return nil, err
	}
	return upstreamModels, nil
}

// GetUserUpstreamModelByOwnerID 返回某用户拥有的指定上游模型喵。
func GetUserUpstreamModelByOwnerID(upstreamModelID int64, ownerUserID int) (*UserUpstreamModel, error) {
	// 喵~防御：无效参数直接返回记录不存在，避免空值进入 SQL 查询喵。
	if upstreamModelID <= 0 || ownerUserID <= 0 {
		return nil, gorm.ErrRecordNotFound
	}
	var upstreamModel UserUpstreamModel
	if err := DB.Where("id = ? AND owner_user_id = ?", upstreamModelID, ownerUserID).First(&upstreamModel).Error; err != nil {
		return nil, err
	}
	return &upstreamModel, nil
}

// GetEnabledUserUpstreamModelByOwnerName 返回某用户启用的指定名称上游模型喵。
func GetEnabledUserUpstreamModelByOwnerName(ownerUserID int, normalizedName string) (*UserUpstreamModel, error) {
	// 喵~防御：无效属主或空名称直接返回记录不存在，避免隐藏资源存在性喵。
	if ownerUserID <= 0 || strings.TrimSpace(normalizedName) == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var upstreamModel UserUpstreamModel
	// 只查询启用状态，保证停用模型不可被调用喵。
	if err := DB.Where("owner_user_id = ? AND normalized_name = ? AND enabled = ?", ownerUserID, normalizedName, true).First(&upstreamModel).Error; err != nil {
		return nil, err
	}
	return &upstreamModel, nil
}

// DeleteUserUpstreamModelByOwnerWithVersion 版本保护的删除，防止过期页面撤销他人修改喵。
func DeleteUserUpstreamModelByOwnerWithVersion(upstreamModelID int64, ownerUserID int, expectedVersion int) error {
	// 喵~防御：无效参数直接返回记录不存在喵。
	if upstreamModelID <= 0 || ownerUserID <= 0 {
		return gorm.ErrRecordNotFound
	}
	// 只删除匹配属主与版本的记录，零行影响意味着版本冲突或资源不存在喵。
	result := DB.Where("id = ? AND owner_user_id = ? AND version = ?", upstreamModelID, ownerUserID, expectedVersion).Delete(&UserUpstreamModel{})
	if result.Error != nil {
		return result.Error
	}
	// 喵~防御：零行删除既可能是并发版本冲突，也可能是资源不存在，统一返回记录不存在喵。
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
