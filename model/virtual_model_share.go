package model

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// VirtualModelSharePayloadVersion 标识分享码快照的格式版本喵。
// 导入端只接受等于本值的版本，未来格式升级时旧版本会被明确拒绝而不是被误读喵。
const VirtualModelSharePayloadVersion = 1

// VirtualModelShareCodeByteLength 是分享码随机部分的字节数，20 字节即 160 位熵喵。
// 喵~防御：分享码是"知道即可导入"的凭据，熵必须足够高，避免被穷举喵。
const VirtualModelShareCodeByteLength = 20

// virtualModelShareCodeEncoding 使用无填充 base32，字符集只含 A-Z 与 2-7 喵。
// 这样分享码里不会出现 0/O、1/I/l 这类肉眼难分的字符，主人手工抄写也不容易错喵。
var virtualModelShareCodeEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// VirtualModelShareCode 保存一份脱敏虚拟模型方案的分享码喵。
// 只存"脱敏快照"，不存指向源模型的引用，因此源模型被改名或删除都不影响已发出的分享码喵。
// 主人注意：撤销分享码即硬删除整行，因此本表不存在"已撤销"这种中间状态喵。
type VirtualModelShareCode struct {
	ID int `json:"id" gorm:"primaryKey"`
	// Code 是分享码本体，全局唯一；导入方凭它读取快照喵。
	Code string `json:"code" gorm:"type:varchar(64);not null;uniqueIndex"`
	// OwnerUserID 是分享码的生成者，撤销与列表都以它为隔离边界喵。
	OwnerUserID int `json:"owner_user_id" gorm:"index;not null"`
	// SourceVirtualModelID 记录快照来自哪个虚拟模型，仅用于分享者自查与审计，导入时不回读该模型喵。
	SourceVirtualModelID int `json:"source_virtual_model_id" gorm:"index;not null"`
	// DisplayName 冗余保存分享时的方案显示名，便于列表展示而不必解析快照喵。
	DisplayName string `json:"display_name" gorm:"type:varchar(128)"`
	// Payload 是脱敏后的方案快照（JSON），导入只依赖它喵。
	Payload string `json:"payload" gorm:"type:text;not null"`
	// PayloadDigest 是快照的不可逆摘要，供审计与排查使用而无需读取快照内容喵。
	PayloadDigest string `json:"payload_digest" gorm:"type:varchar(128)"`
	// Visibility 预留市场可见性（private/unlisted/public）；当前一律 private，仅本人可用喵。
	Visibility string `json:"visibility" gorm:"type:varchar(16);not null"`
	// ModerationStatus 预留市场审核状态；当前留空，方向2 引入市场审核后写入喵。
	ModerationStatus string `json:"moderation_status" gorm:"type:varchar(16)"`
	// OriginShareCodeID 预留二次扩散溯源：本次导入若来源本身也是分享码，记录其编号便于追踪喵。
	OriginShareCodeID *int `json:"origin_share_code_id,omitempty" gorm:"index"`
	// ImportCount 是累计成功导入次数，供分享者自查扩散范围喵。
	ImportCount int `json:"import_count"`
	// MaxImports 是允许的最大导入次数，零表示不限喵。
	MaxImports int `json:"max_imports"`
	// ExpiresAt 是分享码到期时间（Unix 秒），零表示永不过期喵。
	ExpiresAt int64 `json:"expires_at" gorm:"bigint"`
	CreatedTime int64 `json:"created_time" gorm:"bigint"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

// VirtualModelSharePayload 描述分享码承载的脱敏虚拟模型方案快照喵。
// 快照只包含"可分享的配置"，绝不包含任何凭据喵。
type VirtualModelSharePayload struct {
	Version             int    `json:"version"`
	DisplayName         string `json:"display_name"`
	LoopEnabled         bool   `json:"loop_enabled"`
	TotalTimeoutSeconds int    `json:"total_timeout_seconds"`
	MaxLoopRounds       int    `json:"max_loop_rounds"`
	FakeStreamEnabled   bool   `json:"fake_stream_enabled"`
	StreamCutAction     string `json:"stream_cut_action,omitempty"`
	StreamCutRetries    int    `json:"stream_cut_retries"`
	// Candidates 是分享的候选链，顺序即为导入后的稳定顺序喵。
	Candidates []VirtualModelShareCandidate `json:"candidates"`
	// GlobalFailureRules 是模型级全局兜底失败规则喵。
	GlobalFailureRules []VirtualModelShareFailureRule `json:"global_failure_rules,omitempty"`
}

// VirtualModelShareCandidate 描述快照里的一个候选喵。
// 喵~防御：本结构刻意不声明 api_key / 任何指纹字段，从类型层面杜绝凭据被序列化进分享码喵。
type VirtualModelShareCandidate struct {
	StableOrder        int                    `json:"stable_order"`
	SourceType         VirtualModelSourceType `json:"source_type"`
	Enabled            bool                   `json:"enabled"`
	MaxRetries         int                    `json:"max_retries"`
	TimeoutSeconds     int                    `json:"timeout_seconds"`
	HedgeThreshold     int                    `json:"hedge_threshold"`
	HedgeFreezeSeconds int                    `json:"hedge_freeze_seconds"`
	// GroupName 与 RealModelName 是内部候选的路由目标（分组 + 真实模型）喵。
	GroupName     string `json:"group_name,omitempty"`
	RealModelName string `json:"real_model_name,omitempty"`
	// BaseURL 是自定义候选的上游地址明文；分享码永不携带 API Key 喵。
	BaseURL string `json:"base_url,omitempty"`
	// AuthStyle 是自定义候选的认证头样式，本身不含秘密喵。
	AuthStyle string `json:"auth_style,omitempty"`
	// RequiresCredential 为真表示导入方必须自行补填 API Key 后该候选才可用喵。
	RequiresCredential bool `json:"requires_credential,omitempty"`
	// FailureRules 是候选级失败规则，字段全部为非敏感配置喵。
	FailureRules []VirtualModelShareFailureRule `json:"failure_rules,omitempty"`
}

// VirtualModelShareFailureRule 描述快照里的一个失败规则喵。
// 与 VirtualModelFailureRule 字段一一对应，但不带主键、候选编号和版本等本地运行态字段喵。
type VirtualModelShareFailureRule struct {
	RuleOrder     int   `json:"rule_order"`
	HTTPStatus    int   `json:"http_status"`
	HTTPStatusMax int   `json:"http_status_max"`
	ErrorClass    string `json:"error_class,omitempty"`
	BodyRegex     string `json:"body_regex,omitempty"`
	Action        VirtualModelFailureAction `json:"action"`
	FreezeSeconds int   `json:"freeze_seconds"`
	FreezeField   string `json:"freeze_field,omitempty"`
	FreezeUnit    VirtualModelFreezeUnit `json:"freeze_unit,omitempty"`
	// StallTimeoutSeconds 静默多久判定流式卡流，单位：秒；零表示运行时默认 60 喵。
	StallTimeoutSeconds int `json:"stall_timeout_seconds"`
	// MinContentChars 探测放流前需累积的内容字符门槛，零表示默认 10 喵。
	MinContentChars int `json:"min_content_chars"`
	// ProbeTotalTimeoutSeconds 探测阶段总预算，单位：秒；零表示默认 300 喵。
	ProbeTotalTimeoutSeconds int `json:"probe_total_timeout_seconds"`
	// TimeoutSeconds 超时条件判定阈值，单位：秒；零表示沿用候选级执行超时喵。
	TimeoutSeconds int `json:"timeout_seconds"`
	// RetryCount 规则重试当前候选的最大重试次数，零表示沿用候选 MaxRetries 喵。
	RetryCount int `json:"retry_count"`
}

// GenerateVirtualModelShareCodeValue 生成一个新的高熵分享码字符串喵。
func GenerateVirtualModelShareCodeValue() (string, error) {
	randomBytes := make([]byte, VirtualModelShareCodeByteLength)
	// 喵~防御：随机源读取失败时绝不退化成可预测的弱码，直接向上抛错喵。
	if _, readError := rand.Read(randomBytes); readError != nil {
		return "", readError
	}
	return virtualModelShareCodeEncoding.EncodeToString(randomBytes), nil
}

// NormalizeVirtualModelShareCode 规范化用户粘贴的分享码喵。
// 大小写与空白差异不该导致"码是对的却导入失败"，因此统一去空白并转大写喵。
func NormalizeVirtualModelShareCode(inputCode string) string {
	return strings.ToUpper(strings.TrimSpace(inputCode))
}

// VirtualModelSharePayloadDigest 返回快照内容的不可逆摘要，用于审计记录喵。
func VirtualModelSharePayloadDigest(payload string) string {
	// 喵~防御：空快照返回空摘要，避免把"没有快照"与"快照摘要"混同喵。
	if payload == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(digest[:])
}

// CreateVirtualModelShareCode 落库一条分享码记录喵。
func CreateVirtualModelShareCode(shareCode *VirtualModelShareCode) error {
	// 喵~防御：分享码、所有者与快照缺一不可，避免写入无法使用的记录喵。
	if shareCode == nil || strings.TrimSpace(shareCode.Code) == "" || shareCode.OwnerUserID <= 0 {
		return errors.New("虚拟模型分享码无效")
	}
	if strings.TrimSpace(shareCode.Payload) == "" {
		return errors.New("虚拟模型分享码快照不能为空")
	}
	if shareCode.Visibility == "" {
		shareCode.Visibility = VirtualModelShareVisibilityPrivate
	}
	if shareCode.CreatedTime == 0 {
		shareCode.CreatedTime = common.GetTimestamp()
	}
	return DB.Create(shareCode).Error
}

// VirtualModelShareVisibilityPrivate 是当前唯一的分享码可见性：仅凭码可用，不进入任何公开列表喵。
const VirtualModelShareVisibilityPrivate = "private"

// GetVirtualModelShareCodeByValue 按分享码字符串读取记录（含已撤销与已过期记录）喵。
// 是否可用由调用方判定，以便分别给出"已撤销""已过期"等明确提示喵。
func GetVirtualModelShareCodeByValue(codeValue string) (*VirtualModelShareCode, error) {
	normalizedCode := NormalizeVirtualModelShareCode(codeValue)
	// 喵~防御：空码直接按不存在处理，避免无条件查询命中最旧的一条记录喵。
	if normalizedCode == "" {
		return nil, gorm.ErrRecordNotFound
	}
	shareCode := &VirtualModelShareCode{}
	queryError := DB.Where("code = ?", normalizedCode).First(shareCode).Error
	return shareCode, queryError
}

// GetVirtualModelShareCodesByOwner 按创建时间倒序返回某用户生成的分享码列表喵。
func GetVirtualModelShareCodesByOwner(ownerUserID int, limit int) ([]VirtualModelShareCode, error) {
	// 喵~防御：无效所有者返回空列表，并给分页大小一个硬上限防止一次拉全表喵。
	if ownerUserID <= 0 {
		return []VirtualModelShareCode{}, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	shareCodes := make([]VirtualModelShareCode, 0)
	queryError := DB.Where("owner_user_id = ?", ownerUserID).Order("id desc").Limit(limit).Find(&shareCodes).Error
	return shareCodes, queryError
}

// DeleteVirtualModelShareCodeByOwner 删除某用户自己的分享码，删除后立即不可再导入喵。
// 主人注意：这里是硬删除（Unscoped），既不保留"已撤销"的中间状态，
// 也不给 code 唯一索引留下"查不到却占着键"的幽灵行喵。
func DeleteVirtualModelShareCodeByOwner(ownerUserID int, shareCodeID int) error {
	// 喵~防御：删除必须带所有者条件，避免越权删除他人分享码喵。
	if ownerUserID <= 0 || shareCodeID <= 0 {
		return gorm.ErrRecordNotFound
	}
	// 用 Unscoped 绕开软删除，让这一行真正从表里消失喵。
	result := DB.Unscoped().
		Where("id = ? AND owner_user_id = ?", shareCodeID, ownerUserID).
		Delete(&VirtualModelShareCode{})
	if result.Error != nil {
		return result.Error
	}
	// 喵~防御：零行删除说明分享码不存在或不属于本人，统一按未找到返回避免存在性泄露喵。
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// IncrementVirtualModelShareCodeImportCount 在导入成功后累加分享码的导入次数喵。
// 使用 SQL 表达式自增而不是"读出再写回"，避免并发导入时互相覆盖计数喵。
func IncrementVirtualModelShareCodeImportCount(shareCodeID int) error {
	if shareCodeID <= 0 {
		return errors.New("虚拟模型分享码编号无效")
	}
	return DB.Model(&VirtualModelShareCode{}).
		Where("id = ?", shareCodeID).
		Update("import_count", gorm.Expr("import_count + 1")).Error
}
