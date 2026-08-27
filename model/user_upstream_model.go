package model

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
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
	// 余额 = 这个模型理论上还能用那么多（手动预存，递减账户）喵。
	BalanceCents int64 `json:"balance_cents"`
	// 可用额度 = 这个模型用户能接受用那么多（递减账户，自用/共享调用都扣）喵。
	AvailableCents int64 `json:"available_cents"`
	// 以下字段为旧版余额体系遗留，保留列以兼容旧数据但不再参与任何扣费与展示喵。
	SpendLimitCents        int64  `json:"spend_limit_cents"`
	TotalSpentCents        int64  `json:"total_spent_cents"`
	UpstreamRemainingCents int64  `json:"upstream_remaining_cents"`
	UpstreamRemainingAt    int64  `json:"upstream_remaining_at"`
	BalanceCheckEnabled    bool   `json:"balance_check_enabled"`
	BalanceCheckPath       string `json:"balance_check_path" gorm:"type:varchar(256)"`
	// 共享配置：共享额度是递减账户，共享调用扣「余额+可用+共享」，耗尽后自动停止共享喵。
	ShareEnabled       bool  `json:"share_enabled"`
	ShareLimitCents    int64 `json:"share_limit_cents"`
	ShareSpentCents    int64 `json:"share_spent_cents"`
	ShowBalanceEnabled bool  `json:"show_balance_enabled"`
	// 共享白名单/黑名单：逗号分隔的用户 id，白名单非空时仅白名单用户可见可调用；黑名单用户一律被挡喵。
	ShareWhitelist string `json:"share_whitelist" gorm:"type:text"`
	ShareBlacklist string `json:"share_blacklist" gorm:"type:text"`
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

// GetUserUpstreamModelByOwnerName 返回某用户名下的指定名称上游模型（不限制启用状态）喵。
// 实体状态检测用：模型被停用时仍需定位实体以更新「最近一次调用」喵。
func GetUserUpstreamModelByOwnerName(ownerUserID int, normalizedName string) (*UserUpstreamModel, error) {
	// 喵~防御：无效属主或空名称直接返回记录不存在，避免空值进入 SQL 查询喵。
	if ownerUserID <= 0 || strings.TrimSpace(normalizedName) == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var upstreamModel UserUpstreamModel
	if err := DB.Where("owner_user_id = ? AND normalized_name = ?", ownerUserID, normalizedName).First(&upstreamModel).Error; err != nil {
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
	// 实体状态检测：模型删除后联动清理自用与共享维度的状态行，避免残留孤儿数据喵。
	if err := DB.Where("scope IN ? AND entity_id = ?",
		[]string{EntityProbeScopeUpstream, EntityProbeScopeUpstreamShared}, upstreamModelID).
		Delete(&EntityProbeState{}).Error; err != nil {
		// 喵~防御：清理失败只记录日志，不阻止删除已提交的结果喵。
		common.SysError("failed to delete upstream model entity probe states: " + err.Error())
	}
	return nil
}

// UpdateUserUpstreamModelBalanceResult 持久化嗅探到的剩余额度与时间戳喵。
// 嗅探结果是只读参考提示（上游真实剩余），不参与扣费喵。
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

// SyncUserUpstreamModelBalance 一键把嗅探到的剩余额度设为「余额」喵。
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

// SyncUserUpstreamModelAvailable 一键把嗅探到的剩余额度设为「可用额度」喵。
// 可用额度表示用户能接受用那么多，嗅探到的上游剩余是合理参考值喵。
func SyncUserUpstreamModelAvailable(upstreamModelID int64, ownerUserID int) error {
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
		// 把嗅探结果覆盖可用额度，同步操作显式接受该结果喵。
		upstreamModel.AvailableCents = upstreamModel.UpstreamRemainingCents
		upstreamModel.UpdatedTime = time.Now().Unix()
		// 只更新可用额度字段，避免覆盖控制面并发修改的其他配置喵。
		return tx.Model(&upstreamModel).Select("available_cents", "updated_time").Updates(upstreamModel).Error
	})
}

// SharedUserUpstreamModelView 描述共享模型中可对其他用户展示的公开信息喵。
type SharedUserUpstreamModelView struct {
	ID                 int64
	OwnerUserID        int
	NormalizedName     string
	DisplayName        string
	Description        string
	RealModelName      string
	ModelRatio         string
	CompletionRatio    string
	ShareLimitCents    int64
	ShowBalanceEnabled bool
	BalanceCents       int64
	AvailableCents     int64
	// 共享白名单/黑名单：逗号分隔的用户 id，供可见性过滤使用喵。
	ShareWhitelist string
	ShareBlacklist string
}

// isUserAllowedShared 判断 viewerID 是否被共享白名单/黑名单允许喵。
// 黑名单优先：黑名单含 viewerID → 禁止；白名单非空且不含 viewerID → 禁止；否则允许喵。
// viewerID <= 0（未登录）视为"非白名单用户"，白名单非空即被挡喵。
func isUserAllowedShared(viewerID int, whitelist string, blacklist string) bool {
	// 黑名单命中即禁止喵。
	if containsSharedUserID(blacklist, viewerID) {
		return false
	}
	// 白名单为空表示不限制；非空时必须包含 viewerID 喵。
	if strings.TrimSpace(whitelist) == "" {
		return true
	}
	return containsSharedUserID(whitelist, viewerID)
}

// containsSharedUserID 判断逗号分隔的用户 id 串是否包含目标 id 喵。
func containsSharedUserID(ids string, targetID int) bool {
	// 目标 id 非正（未登录）时不可能在白名单/黑名单中喵。
	if targetID <= 0 || strings.TrimSpace(ids) == "" {
		return false
	}
	for _, part := range strings.Split(ids, ",") {
		parsed, parseError := strconv.Atoi(strings.TrimSpace(part))
		if parseError == nil && parsed == targetID {
			return true
		}
	}
	return false
}

// GetSharedUserUpstreamModels 返回当前共享中（余额>0、可用>0、共享额度>0）的全部上游模型喵。
// viewerID 用于白名单/黑名单过滤：被挡用户看不到对应模型喵。
func GetSharedUserUpstreamModels(viewerID int) ([]SharedUserUpstreamModelView, error) {
	var views []SharedUserUpstreamModelView
	// 三账户都是递减账户，任一耗尽即自动停止共享（从共享列表消失）喵。
	if err := DB.Model(&UserUpstreamModel{}).
		Select("id", "owner_user_id", "normalized_name", "display_name", "description", "real_model_name", "model_ratio", "completion_ratio", "share_limit_cents", "show_balance_enabled", "balance_cents", "available_cents", "share_whitelist", "share_blacklist").
		Where("share_enabled = ? AND balance_cents > 0 AND available_cents > 0 AND share_limit_cents > 0", true).
		Find(&views).Error; err != nil {
		return nil, err
	}
	// 白名单/黑名单在 Go 侧过滤（逗号分隔的 id 串不适合跨库 SQL 表达）喵。
	filtered := make([]SharedUserUpstreamModelView, 0, len(views))
	for _, view := range views {
		if isUserAllowedShared(viewerID, view.ShareWhitelist, view.ShareBlacklist) {
			filtered = append(filtered, view)
		}
	}
	return filtered, nil
}

// GetSharedUserUpstreamModelNames 返回共享中且对 viewerID 可见的上游模型对外调用名称列表喵。
func GetSharedUserUpstreamModelNames(viewerID int) []string {
	views, err := GetSharedUserUpstreamModels(viewerID)
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

// GetSharedUserUpstreamModelByNormalizedName 返回共享池中指定名称的模型（任意属主），供全局唯一命名池校验用喵。
func GetSharedUserUpstreamModelByNormalizedName(normalizedName string) (*UserUpstreamModel, error) {
	// 喵~防御：空名称直接返回记录不存在喵。
	if strings.TrimSpace(normalizedName) == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var upstreamModel UserUpstreamModel
	// 只查共享开启的模型，不限制属主；额度耗尽与否都占用名字（恢复额度后仍会回到共享池）喵。
	if err := DB.Where("normalized_name = ? AND share_enabled = ?", normalizedName, true).First(&upstreamModel).Error; err != nil {
		return nil, err
	}
	return &upstreamModel, nil
}

// GetEnabledSharedUserUpstreamModelByName 返回指定名称的共享启用上游模型，供共享调用授权用喵。
// 不限定属主：任何用户都可调用共享中的模型；任一账户耗尽或被白名单/黑名单挡时按记录不存在处理喵。
func GetEnabledSharedUserUpstreamModelByName(normalizedName string, viewerID int) (*UserUpstreamModel, error) {
	// 喵~防御：空名称直接返回记录不存在喵。
	if strings.TrimSpace(normalizedName) == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var upstreamModel UserUpstreamModel
	// 启用 + 共享开启 + 余额/可用/共享三账户都未耗尽才算"共享中"喵。
	// Order("id asc") 保证存量重复命名数据（命名池上线前的历史遗留）下有确定选择，不依赖数据库返回顺序喵。
	if err := DB.Where("normalized_name = ? AND enabled = ? AND share_enabled = ? AND balance_cents > 0 AND available_cents > 0 AND share_limit_cents > 0", normalizedName, true, true).Order("id asc").First(&upstreamModel).Error; err != nil {
		return nil, err
	}
	// 白名单/黑名单过滤：被挡用户按记录不存在处理，避免泄露模型存在性喵。
	if !isUserAllowedShared(viewerID, upstreamModel.ShareWhitelist, upstreamModel.ShareBlacklist) {
		return nil, gorm.ErrRecordNotFound
	}
	return &upstreamModel, nil
}

// SharedModelUserUsage 描述某个共享上游模型按用户聚合的使用量喵。
type SharedModelUserUsage struct {
	UserID           int    `json:"user_id"`
	Username         string `json:"username"`
	RequestCount     int64  `json:"request_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	LastAt           int64  `json:"last_at"`
}

// sharedModelUserUsageLimit 限制共享模型使用情况返回的最大用户数，防止大表拖慢接口喵。
const sharedModelUserUsageLimit = 100

// GetSharedModelUserUsage 聚合共享调用日志（type=8 且 group=user-shared）按用户统计喵。
// 属主校验由控制器负责，这里按模型编号与属主归属查询喵。
func GetSharedModelUserUsage(upstreamModelID int64, ownerUserID int) ([]SharedModelUserUsage, error) {
	// 喵~防御：无效参数返回空结果喵。
	if upstreamModelID <= 0 || ownerUserID <= 0 {
		return []SharedModelUserUsage{}, nil
	}
	// 先定位属主名下模型，防止越权聚合他人模型的日志喵。
	upstreamModel, err := GetUserUpstreamModelByOwnerID(upstreamModelID, ownerUserID)
	if err != nil {
		return []SharedModelUserUsage{}, err
	}
	modelName := upstreamModel.UserUpstreamModelName()
	rows := make([]SharedModelUserUsage, 0, sharedModelUserUsageLimit)
	// 共享调用日志固定归入 user-shared 分组且 type=8，按 user_id 聚合喵。
	queryError := LOG_DB.Table("logs").
		Select("user_id, count(*) as request_count, sum(prompt_tokens) as prompt_tokens, sum(completion_tokens) as completion_tokens, max(created_at) as last_at").
		Where("model_name = ? AND "+commonGroupCol+" = ? AND type = ? AND user_id > 0", modelName, constant.GroupUserShared, LogTypeCustomUpstream).
		Group("user_id").
		Order("last_at DESC").
		Limit(sharedModelUserUsageLimit).
		Find(&rows).Error
	if queryError != nil {
		return nil, queryError
	}
	// 从主库批量补齐用户名，避免跨库 JOIN（日志库可能是独立实例）喵。
	fillSharedModelUsernames(rows)
	return rows, nil
}

// fillSharedModelUsernames 批量从主库用户表补齐使用情况的用户名喵。
func fillSharedModelUsernames(rows []SharedModelUserUsage) {
	// 喵~防御：空结果直接返回喵。
	if len(rows) == 0 {
		return
	}
	userIDs := make([]int, 0, len(rows))
	for _, row := range rows {
		userIDs = append(userIDs, row.UserID)
	}
	var users []User
	// 喵~防御：用户查询失败按空名处理，不阻塞使用情况返回喵。
	if err := DB.Where("id IN ?", userIDs).Find(&users).Error; err != nil {
		return
	}
	usernameByID := make(map[int]string, len(users))
	for _, user := range users {
		usernameByID[user.Id] = user.Username
	}
	for i := range rows {
		rows[i].Username = usernameByID[rows[i].UserID]
	}
}

// DeductUserUpstreamModelCharge 请求后按实际费用扣减三个递减账户，事务加行锁防止并发超扣喵。
// isShared 为 true 时扣「余额+可用+共享」，为 false 时扣「余额+可用」喵。
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
		// 递减账户减法统一钳制到 0，绝不产生负余额；余额/可用/共享任一归零后由请求前硬检查拦截喵。
		upstreamModel.BalanceCents = clampUpstreamModelCents(upstreamModel.BalanceCents, costCents)
		upstreamModel.AvailableCents = clampUpstreamModelCents(upstreamModel.AvailableCents, costCents)
		if isShared {
			// 共享调用额外扣减共享额度，反映共享专属余额的消耗喵。
			upstreamModel.ShareLimitCents = clampUpstreamModelCents(upstreamModel.ShareLimitCents, costCents)
		}
		upstreamModel.UpdatedTime = time.Now().Unix()
		// 只更新金额相关字段，避免覆盖控制面并发修改的其他配置喵。
		return tx.Model(&upstreamModel).Select("balance_cents", "available_cents", "share_limit_cents", "updated_time").Updates(upstreamModel).Error
	})
}

// clampUpstreamModelCents 递减减法钳制到非负，防止任何计费路径产生负账户喵。
func clampUpstreamModelCents(current int64, costCents int64) int64 {
	// 喵~防御：费用非负（调用方已校验），余额不足时置 0 即可喵。
	if current <= costCents {
		return 0
	}
	return current - costCents
}
