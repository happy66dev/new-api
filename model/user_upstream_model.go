package model

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
)

// UserUpstreamModelNamePattern 只允许 ASCII 字母、数字、短横线和下划线喵。
var UserUpstreamModelNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// UserUpstreamModel 表示用户私有的一个上游模型（一模型一上游）喵。
// 金额字段一律以"分"存储（RMB），前端展示转元，避免浮点误差喵。
type UserUpstreamModel struct {
	ID             int64  `json:"id" gorm:"primaryKey"`
	OwnerUserID    int    `json:"owner_user_id" gorm:"index"`
	NormalizedName string `json:"normalized_name" gorm:"type:varchar(96);uniqueIndex:idx_upstream_owner_name"`
	DisplayName    string `json:"display_name" gorm:"type:varchar(128)"`
	// Description 是模型简介，展示在模型广场卡片上，独立于显示名喵。
	Description string `json:"description" gorm:"type:text"`
	Enabled     bool   `json:"enabled"`
	// 上游连接：凭据一律加密存储，绝不落明文喵。
	EncryptedBaseURL   string `json:"-" gorm:"type:text"`
	EncryptedAPIKey    string `json:"-" gorm:"type:text"`
	BaseURLFingerprint string `json:"-" gorm:"type:varchar(64)"`
	APIKeyFingerprint  string `json:"-" gorm:"type:varchar(64)"`
	CredentialVersion  int    `json:"-"`
	RealModelName      string `json:"real_model_name" gorm:"type:varchar(128)"`
	AuthStyle          string `json:"auth_style" gorm:"type:varchar(32)"`
	// 计费：每个字段是该 token 分类的独立价格（每百万 token 的 RMB 元），decimal 字符串喵。
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
	// 调用前缀统一为 user/，同时兼容旧前缀 upstream/ 的存量请求喵。
	for _, prefix := range []string{"user/", "upstream/"} {
		if strings.HasPrefix(name, prefix) {
			name = strings.TrimPrefix(name, prefix)
			break
		}
	}
	if name == "" || len(name) > 96 || !UserUpstreamModelNamePattern.MatchString(name) {
		return "", errors.New("用户上游模型名称只允许 ASCII 字母、数字、短横线和下划线")
	}
	return strings.ToLower(name), nil
}

// UserUpstreamModelName 返回对外使用的 user/ 模型名称喵。
func (upstreamModel *UserUpstreamModel) UserUpstreamModelName() string {
	// 喵~防御：空对象不生成名称，避免上游模型名悬空喵。
	if upstreamModel == nil {
		return ""
	}
	return "user/" + upstreamModel.NormalizedName
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

// UpdateUserUpstreamModelBalanceResult 持久化嗅探到的剩余额度与时间戳喵。
func UpdateUserUpstreamModelBalanceResult(upstreamModelID int64, ownerUserID int, remainingCents int64) error {
	// 喵~防御：无效参数直接返回记录不存在，避免空值进入更新喵。
	if upstreamModelID <= 0 || ownerUserID <= 0 {
		return gorm.ErrRecordNotFound
	}
	// 只更新展示字段，不动版本号，避免嗅探干扰并发配置编辑喵。
	return DB.Model(&UserUpstreamModel{}).Where("id = ? AND owner_user_id = ?", upstreamModelID, ownerUserID).Updates(map[string]interface{}{
		"upstream_remaining_cents": remainingCents,
		"upstream_remaining_at":    time.Now().Unix(),
	}).Error
}

// SyncUserUpstreamModelBalance 把嗅探到的剩余额度同步为可用余额喵。
// 事务加行锁读取最新剩余额度后覆盖余额，保证与嗅探结果一致喵。
func SyncUserUpstreamModelBalance(upstreamModelID int64, ownerUserID int) error {
	// 喵~防御：无效参数直接返回记录不存在喵。
	if upstreamModelID <= 0 || ownerUserID <= 0 {
		return gorm.ErrRecordNotFound
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var upstreamModel UserUpstreamModel
		// lockForUpdate 在 MySQL/PostgreSQL 加行锁，SQLite 跳过锁语法喵。
		if err := lockForUpdate(tx).Where("id = ? AND owner_user_id = ?", upstreamModelID, ownerUserID).First(&upstreamModel).Error; err != nil {
			return err
		}
		// 把嗅探结果覆盖余额，同步操作显式接受该结果喵。
		upstreamModel.BalanceCents = upstreamModel.UpstreamRemainingCents
		upstreamModel.UpdatedTime = time.Now().Unix()
		// 只更新余额字段，避免覆盖控制面并发修改的其他配置喵。
		return tx.Model(&upstreamModel).Select("balance_cents", "updated_time").Updates(upstreamModel).Error
	})
}

// SharedUserUpstreamModelView 描述共享模型中可对其他用户展示的公开信息喵。
type SharedUserUpstreamModelView struct {
	ID                     int64
	OwnerUserID            int
	NormalizedName         string
	DisplayName            string
	Description            string
	RealModelName          string
	ModelRatio             string
	CompletionRatio        string
	ShareLimitCents        int64
	ShareSpentCents        int64
	ShowBalanceEnabled     bool
	BalanceCents           int64
	SpendLimitCents        int64
	UpstreamRemainingCents int64
}

// GetSharedUserUpstreamModels 返回当前共享中（共享开启且共享额度未耗尽）的全部上游模型喵。
func GetSharedUserUpstreamModels() ([]SharedUserUpstreamModelView, error) {
	var views []SharedUserUpstreamModelView
	// 共享额度为 0 表示不限；达到额度即自动停止共享（从列表消失）喵。
	if err := DB.Model(&UserUpstreamModel{}).
		Select("id", "owner_user_id", "normalized_name", "display_name", "description", "real_model_name", "model_ratio", "completion_ratio", "share_limit_cents", "share_spent_cents", "show_balance_enabled", "balance_cents", "spend_limit_cents", "upstream_remaining_cents").
		Where("share_enabled = ? AND (share_limit_cents = 0 OR share_spent_cents < share_limit_cents)", true).
		Find(&views).Error; err != nil {
		return nil, err
	}
	return views, nil
}

// GetSharedUserUpstreamModelNames 返回共享中上游模型的对外调用名称列表喵。
func GetSharedUserUpstreamModelNames() []string {
	views, err := GetSharedUserUpstreamModels()
	// 喵~防御：查询失败按空列表处理，避免把错误泄漏到模型列表接口喵。
	if err != nil {
		return []string{}
	}
	names := make([]string, 0, len(views))
	for _, view := range views {
		names = append(names, "user/"+view.NormalizedName)
	}
	return names
}

// GetEnabledSharedUserUpstreamModelByName 返回指定名称的共享启用上游模型，供共享调用授权用喵。
// 不限定属主：任何用户都可调用共享中的模型；额度耗尽时按记录不存在处理喵。
func GetEnabledSharedUserUpstreamModelByName(normalizedName string) (*UserUpstreamModel, error) {
	// 喵~防御：空名称直接返回记录不存在喵。
	if strings.TrimSpace(normalizedName) == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var upstreamModel UserUpstreamModel
	// 启用 + 共享开启 + 共享额度未耗尽才算"共享中"喵。
	if err := DB.Where("normalized_name = ? AND enabled = ? AND share_enabled = ? AND (share_limit_cents = 0 OR share_spent_cents < share_limit_cents)", normalizedName, true, true).First(&upstreamModel).Error; err != nil {
		return nil, err
	}
	return &upstreamModel, nil
}

// DeductUserUpstreamModelCharge 请求后按实际费用扣减余额与累计消耗，事务加行锁防止并发超扣喵。
// isShared 为 true 时只累加共享消耗（共享调用免费，不扣所有者余额）喵。
func DeductUserUpstreamModelCharge(upstreamModelID int64, ownerUserID int, costCents int64, isShared bool) error {
	// 喵~防御：费用必须非负，负数费用是计费缺陷，直接拒绝避免余额被错误增加喵。
	if costCents < 0 {
		return errors.New("upstream model charge must not be negative")
	}
	// 喵~防御：无效参数直接返回记录不存在，避免空值进入事务喵。
	if upstreamModelID <= 0 || ownerUserID <= 0 {
		return gorm.ErrRecordNotFound
	}
	// 事务内加行锁读取最新余额，防止并发请求对同一模型重复扣费喵。
	return DB.Transaction(func(tx *gorm.DB) error {
		var upstreamModel UserUpstreamModel
		// lockForUpdate 在 MySQL/PostgreSQL 加 FOR UPDATE 行锁，SQLite 跳过锁语法喵。
		if err := lockForUpdate(tx).Where("id = ? AND owner_user_id = ?", upstreamModelID, ownerUserID).First(&upstreamModel).Error; err != nil {
			return err
		}
		// 费用为零时不产生任何数据库写入，避免空事务喵。
		if costCents == 0 {
			return nil
		}
		if isShared {
			// 共享调用只累计共享消耗，达到共享额度后由请求前硬检查拦截喵。
			upstreamModel.ShareSpentCents += costCents
		} else {
			// 自用调用扣减余额，余额不足时置 0（下次请求被请求前硬检查拦截），绝不产生负余额喵。
			if upstreamModel.BalanceCents <= costCents {
				upstreamModel.BalanceCents = 0
			} else {
				upstreamModel.BalanceCents -= costCents
			}
			// 自用累计消耗单调递增，供使用上限判断喵。
			upstreamModel.TotalSpentCents += costCents
		}
		upstreamModel.UpdatedTime = time.Now().Unix()
		// 只更新金额相关字段，避免覆盖控制面并发修改的其他配置喵。
		return tx.Model(&upstreamModel).Select("balance_cents", "total_spent_cents", "share_spent_cents", "updated_time").Updates(upstreamModel).Error
	})
}
