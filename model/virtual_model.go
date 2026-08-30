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

// VirtualModelCandidateAttemptRecord 描述虚拟模型一次候选尝试的可审计摘要喵。
// 只保存受控信息，绝不携带上游凭据、完整 URL 或请求正文喵。
type VirtualModelCandidateAttemptRecord struct {
	Seq          int    `json:"seq"`           // 候选链序号（1 起）喵。
	CandidateID  int    `json:"candidate_id"`  // 候选编号喵。
	Source       string `json:"source"`        // 候选来源：internal / custom 喵。
	Label        string `json:"label"`         // 候选标识（internal: 真实模型名；custom: 模型名/显示名）喵。
	Success      bool   `json:"success"`       // 本次尝试是否成功喵。
	StatusCode   int    `json:"status_code"`   // 上游 HTTP 状态码，网络错误时为零喵。
	ErrorClass   string `json:"error_class"`   // 稳定错误分类，供失败规则匹配喵。
	ErrorMessage string `json:"error_message"` // 受控错误信息（受限于安全词表，不含密钥/URL/正文）喵。
	// TtftMs 本次尝试的首字耗时（毫秒），成功流式尝试才有，零表示未测到喵。
	TtftMs int64 `json:"ttft_ms"`
	// ElapsedMs 本次尝试的总耗时，单位：毫秒喵。
	ElapsedMs int64 `json:"elapsed_ms"`
	// ErrorBody 本次尝试的错误返回体（受限摘要），custom 候选填上游真实响应体，internal 填错误消息喵。
	ErrorBody  string `json:"error_body,omitempty"`
	RetryCount int    `json:"retry_count"` // 失败规则对该候选的重试次数喵。
}

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

// VirtualModelFreezeUnit 标记响应体字段冻结时间的解析单位喵。
type VirtualModelFreezeUnit string

const (
	// VirtualModelFreezeUnitSeconds 表示字段值直接是秒数喵。
	VirtualModelFreezeUnitSeconds VirtualModelFreezeUnit = "seconds"
	// VirtualModelFreezeUnitMinutes 表示字段值是分钟数，换算为秒时乘以 60 喵。
	VirtualModelFreezeUnitMinutes VirtualModelFreezeUnit = "minutes"
	// VirtualModelFreezeUnitMixed 表示字段值是分钟+秒格式（如 1m30s）喵。
	VirtualModelFreezeUnitMixed VirtualModelFreezeUnit = "mixed"
	// VirtualModelFreezeUnitAuto 表示自动在响应体中扫描自然语言时间（如 "in 22 minutes"）喵。
	VirtualModelFreezeUnitAuto VirtualModelFreezeUnit = "auto"
)

// VirtualModelAuthStyle 标记自定义上游采用的认证头语义喵。
type VirtualModelAuthStyle string

const (
	// VirtualModelAuthBearer 表示使用 Authorization Bearer 认证喵。
	VirtualModelAuthBearer VirtualModelAuthStyle = "bearer"
	// VirtualModelAuthAPIKey 表示使用通用 x-api-key 认证喵。
	VirtualModelAuthAPIKey VirtualModelAuthStyle = "api_key"
	// VirtualModelAuthAnthropic 表示使用 Anthropic x-api-key 认证喵。
	VirtualModelAuthAnthropic VirtualModelAuthStyle = "anthropic"
	// virtualModelAuthLegacyAPIKey 保留已写入数据库的旧通用 API Key 枚举兼容性喵。
	virtualModelAuthLegacyAPIKey VirtualModelAuthStyle = "x-api-key"
	// virtualModelAuthLegacyAnthropic 保留已写入数据库的旧 Anthropic 枚举兼容性喵。
	virtualModelAuthLegacyAnthropic VirtualModelAuthStyle = "anthropic-x-api-key"
)

// VirtualModel 保存用户私有虚拟模型的主配置喵。
type VirtualModel struct {
	ID             int    `json:"id" gorm:"primaryKey"`
	OwnerUserID    int    `json:"owner_user_id" gorm:"index;not null;uniqueIndex:idx_virtual_model_owner_name,priority:1"`
	NormalizedName string `json:"normalized_name" gorm:"type:varchar(128);not null;uniqueIndex:idx_virtual_model_owner_name,priority:2"`
	DisplayName    string `json:"display_name" gorm:"type:varchar(128);not null"`
	// 喵~防御：Enabled 不能带 default 标签，否则 GORM 在 Create 时会跳过 false 值并回落到数据库默认 true，
	// 导致用户「创建即停用」的模型被静默写成启用状态并可被 API Key 调用喵。
	Enabled             bool `json:"enabled"`
	LoopEnabled         bool `json:"loop_enabled"`
	TotalTimeoutSeconds int  `json:"total_timeout_seconds" gorm:"default:120"`
	MaxLoopRounds       int  `json:"max_loop_rounds" gorm:"default:1"`
	// FakeStreamEnabled 流转伪流开关：开启后上游流式响应全量缓存到 [DONE] 再一次性伪流发给客户端，抵抗网络波动断流喵。
	FakeStreamEnabled bool `json:"fake_stream_enabled"`
	// StreamCutAction 流转伪流断流（上游未完整返回就中断）时的处理措施，与失败规则动作一致，空表示跟随失败规则喵。
	StreamCutAction VirtualModelFailureAction `json:"stream_cut_action" gorm:"type:varchar(16)"`
	// StreamCutRetries 流转伪流断流时对当前候选的重试次数，单位：次，零表示不额外重试喵。
	StreamCutRetries int            `json:"stream_cut_retries"`
	Version          int64          `json:"version" gorm:"default:1"`
	CreatedTime      int64          `json:"created_time" gorm:"bigint"`
	UpdatedTime      int64          `json:"updated_time" gorm:"bigint"`
	DeletedAt        gorm.DeletedAt `json:"-" gorm:"index"`
}

// VirtualModelCandidate 保存候选顺序和通用运行参数喵。
type VirtualModelCandidate struct {
	ID             int                    `json:"id" gorm:"primaryKey"`
	VirtualModelID int                    `json:"virtual_model_id" gorm:"index;not null;uniqueIndex:idx_virtual_model_candidate_order,priority:1"`
	StableOrder    int                    `json:"stable_order" gorm:"not null;uniqueIndex:idx_virtual_model_candidate_order,priority:2"`
	SourceType     VirtualModelSourceType `json:"source_type" gorm:"type:varchar(16);not null"`
	// 喵~防御：同上，候选的 Enabled 也不能带 default 标签，否则新建停用候选会被写成启用并参与调用喵。
	Enabled        bool `json:"enabled"`
	MaxRetries     int  `json:"max_retries" gorm:"default:0"`
	TimeoutSeconds int  `json:"timeout_seconds" gorm:"default:60"`
	// HedgeThreshold 连续失败自动避险阈值，达到该次数才冻结退避；零表示关闭自动避险、维持失效规则既有语义喵。
	HedgeThreshold int `json:"hedge_threshold"`
	// HedgeFreezeSeconds 达到连续失败阈值后的退避冻结秒数；阈值非零时必填且必须为正数喵。
	HedgeFreezeSeconds int            `json:"hedge_freeze_seconds"`
	Version            int64          `json:"version" gorm:"default:1"`
	CreatedTime        int64          `json:"created_time" gorm:"bigint"`
	UpdatedTime        int64          `json:"updated_time" gorm:"bigint"`
	DeletedAt          gorm.DeletedAt `json:"-" gorm:"index"`
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
	// UpstreamModelID 引用用户上游模型条目；非空时凭据与真实模型名以该条目为准，直填凭据仅作兼容喵。
	UpstreamModelID *int64 `json:"upstream_model_id,omitempty" gorm:"index"`
}

// VirtualModelFailureRule 保存候选级有序失败规则喵。
type VirtualModelFailureRule struct {
	ID          int `json:"id" gorm:"primaryKey"`
	CandidateID int `json:"candidate_id" gorm:"index;not null;uniqueIndex:idx_virtual_model_failure_rule_order,priority:1"`
	RuleOrder   int `json:"rule_order" gorm:"not null;uniqueIndex:idx_virtual_model_failure_rule_order,priority:2"`
	HTTPStatus  int `json:"http_status"`
	// HTTPStatusMax 是状态码范围匹配的上界，零表示仅匹配 HTTPStatus 单值喵。
	HTTPStatusMax int                       `json:"http_status_max"`
	ErrorClass    string                    `json:"error_class" gorm:"type:varchar(64)"`
	BodyRegex     string                    `json:"body_regex" gorm:"type:text"`
	Action        VirtualModelFailureAction `json:"action" gorm:"type:varchar(16);not null"`
	FreezeSeconds int                       `json:"freeze_seconds"`
	// FreezeField 是响应体中的冻结时间字段名，非空时启用从响应体解析冻结时间喵。
	FreezeField string `json:"freeze_field" gorm:"type:varchar(64)"`
	// FreezeUnit 标记响应体字段冻结时间的单位，仅在 FreezeField 非空时生效喵。
	FreezeUnit VirtualModelFreezeUnit `json:"freeze_unit" gorm:"type:varchar(16)"`
	// StallTimeoutSeconds 静默多久判定流式卡流，单位：秒；零表示使用运行时默认 60 喵。
	StallTimeoutSeconds int `json:"stall_timeout_seconds"`
	// MinContentChars 探测放流前需要累积的内容字符数门槛，零表示默认 10 喵。
	MinContentChars int `json:"min_content_chars"`
	// ProbeTotalTimeoutSeconds 探测阶段总预算，单位：秒；零表示默认 300 喵。
	ProbeTotalTimeoutSeconds int `json:"probe_total_timeout_seconds"`
	// TimeoutSeconds 超时条件判定阈值，单位：秒；零表示沿用候选级执行超时喵。
	TimeoutSeconds int `json:"timeout_seconds"`
	// RetryCount 失败规则重试当前候选的最大重试次数，零表示未配置时沿用候选 MaxRetries 喵。
	RetryCount int   `json:"retry_count"`
	Version    int64 `json:"version" gorm:"default:1"`
}

// VirtualModelGlobalFailureRule 保存模型级全局兜底失败规则喵。
// 当候选没有配置自己的失效规则时，运行时回退到这组全局规则做失败决策喵。
type VirtualModelGlobalFailureRule struct {
	ID             int `json:"id" gorm:"primaryKey"`
	VirtualModelID int `json:"virtual_model_id" gorm:"index;not null;uniqueIndex:idx_virtual_model_global_failure_rule_order,priority:1"`
	RuleOrder      int `json:"rule_order" gorm:"not null;uniqueIndex:idx_virtual_model_global_failure_rule_order,priority:2"`
	HTTPStatus     int `json:"http_status"`
	// HTTPStatusMax 是状态码范围匹配的上界，零表示仅匹配 HTTPStatus 单值喵。
	HTTPStatusMax int                       `json:"http_status_max"`
	ErrorClass    string                    `json:"error_class" gorm:"type:varchar(64)"`
	BodyRegex     string                    `json:"body_regex" gorm:"type:text"`
	Action        VirtualModelFailureAction `json:"action" gorm:"type:varchar(16);not null"`
	FreezeSeconds int                       `json:"freeze_seconds"`
	// FreezeField 是响应体中的冻结时间字段名，非空时启用从响应体解析冻结时间喵。
	FreezeField string `json:"freeze_field" gorm:"type:varchar(64)"`
	// FreezeUnit 标记响应体字段冻结时间的单位，仅在 FreezeField 非空时生效喵。
	FreezeUnit VirtualModelFreezeUnit `json:"freeze_unit" gorm:"type:varchar(16)"`
	// StallTimeoutSeconds 静默多久判定流式卡流，单位：秒；零表示使用运行时默认 60 喵。
	StallTimeoutSeconds int `json:"stall_timeout_seconds"`
	// MinContentChars 探测放流前需要累积的内容字符数门槛，零表示默认 10 喵。
	MinContentChars int `json:"min_content_chars"`
	// ProbeTotalTimeoutSeconds 探测阶段总预算，单位：秒；零表示默认 300 喵。
	ProbeTotalTimeoutSeconds int `json:"probe_total_timeout_seconds"`
	// TimeoutSeconds 超时条件判定阈值，单位：秒；零表示沿用候选级执行超时喵。
	TimeoutSeconds int `json:"timeout_seconds"`
	// RetryCount 失败规则重试当前候选的最大重试次数，零表示未配置时沿用候选 MaxRetries 喵。
	RetryCount int   `json:"retry_count"`
	Version    int64 `json:"version" gorm:"default:1"`
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

// VirtualModelInternalFreezeState 保存内部候选按候选编号共享的自动冻结状态喵。
type VirtualModelInternalFreezeState struct {
	ID               int    `json:"id" gorm:"primaryKey"`
	OwnerUserID      int    `json:"owner_user_id" gorm:"index;not null;uniqueIndex:idx_virtual_internal_freeze_candidate,priority:1"`
	CandidateID      int    `json:"candidate_id" gorm:"not null;uniqueIndex:idx_virtual_internal_freeze_candidate,priority:2"`
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

// NormalizeVirtualModelAuthStyle 将控制面稳定认证值与历史持久化值统一为当前安全枚举喵。
func NormalizeVirtualModelAuthStyle(authStyle VirtualModelAuthStyle) (VirtualModelAuthStyle, error) {
	// 喵~防御：未知认证方式直接拒绝，避免请求在无认证或错误认证状态下泄漏到上游喵。
	switch authStyle {
	case VirtualModelAuthBearer:
		return VirtualModelAuthBearer, nil
	case VirtualModelAuthAPIKey, virtualModelAuthLegacyAPIKey:
		return VirtualModelAuthAPIKey, nil
	case VirtualModelAuthAnthropic, virtualModelAuthLegacyAnthropic:
		return VirtualModelAuthAnthropic, nil
	default:
		return "", errors.New("自定义候选认证方式无效")
	}
}

// VirtualModelAuthStyleFromStorage 读取历史数据库值时返回可执行的稳定认证方式喵。
func VirtualModelAuthStyleFromStorage(authStyle VirtualModelAuthStyle) VirtualModelAuthStyle {
	normalizedAuthStyle, normalizeError := NormalizeVirtualModelAuthStyle(authStyle)
	// 喵~防御：异常的历史值保留原值，使执行层安全拒绝而不是错误降级认证方式喵。
	if normalizeError != nil {
		return authStyle
	}
	return normalizedAuthStyle
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
	// 流转伪流断流重试次数不能超过候选允许的最大重试上界，避免异常配置无限重放当前候选喵。
	if virtualModel.StreamCutRetries < 0 || virtualModel.StreamCutRetries > 20 {
		return errors.New("虚拟模型断流重试次数必须介于 0 和 20 次之间")
	}
	// 流转伪流断流处理措施必须是已知动作枚举，空值表示跟随失败规则喵。
	if virtualModel.StreamCutAction != "" &&
		virtualModel.StreamCutAction != VirtualModelActionRetry &&
		virtualModel.StreamCutAction != VirtualModelActionNext &&
		virtualModel.StreamCutAction != VirtualModelActionFreeze &&
		virtualModel.StreamCutAction != VirtualModelActionPassthrough {
		return errors.New("虚拟模型断流处理措施无效")
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
	// 喵~防御：连续失败自动避险阈值必须在零到一千之间，超过该上限时累计计数失去运维意义喵。
	if candidate.HedgeThreshold < 0 || candidate.HedgeThreshold > 1000 {
		return errors.New("虚拟模型候选自动避险阈值超出允许范围")
	}
	// 喵~防御：配置了自动避险阈值时退避秒数必须为正且不超过一天，否则冻结退避形同虚设喵。
	if candidate.HedgeThreshold > 0 && (candidate.HedgeFreezeSeconds < 1 || candidate.HedgeFreezeSeconds > 24*60*60) {
		return errors.New("虚拟模型候选自动避险退避秒数超出允许范围")
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

// GetEnabledVirtualModelByOwnerName 查询会话态用户自己拥有的单个启用虚拟模型喵。
// 会话态请求（如游乐场 /pg 路径）没有 API Key 绑定，只需确认模型属于该用户且已启用喵。
func GetEnabledVirtualModelByOwnerName(ownerUserID int, normalizedName string) (*VirtualModel, error) {
	// 喵~防御：拒绝无效身份或空模型名称，防止空条件命中资源喵。
	if ownerUserID <= 0 || normalizedName == "" {
		return nil, gorm.ErrRecordNotFound
	}
	virtualModel := &VirtualModel{}
	queryError := DB.Where("owner_user_id = ? AND normalized_name = ? AND enabled = ?", ownerUserID, normalizedName, true).First(virtualModel).Error
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
	HedgeThreshold     int                    // 连续失败自动避险阈值，达到该次数才冻结退避，零表示关闭喵。
	HedgeFreezeSeconds int                    // 达到连续失败阈值后的退避冻结秒数喵。
	GroupName          string                 // 内部候选目标分组，自定义候选为空喵。
	RealModelName      string                 // 上游或内部实际请求模型名称喵。
	EncryptedBaseURL   string                 // 自定义候选加密后的上游基址，内部候选为空喵。
	BaseURLSummary     string                 // 自定义候选公开地址摘要，仅用于旧记录冻结身份兼容喵。
	BaseURLFingerprint string                 // 自定义候选规范化地址不可逆摘要，用于共享冻结身份喵。
	EncryptedAPIKey    string                 // 自定义候选加密后的认证凭据，内部候选为空喵。
	APIKeyFingerprint  string                 // 自定义候选 API Key 不可逆摘要，用于共享冻结身份喵。
	CredentialVersion  int                    // 自定义候选凭据加密版本喵。
	AuthStyle          VirtualModelAuthStyle  // 自定义候选认证头样式喵。
	// UpstreamModelID 引用用户上游模型条目，自定义候选专用；非空时凭据以条目为准喵。
	UpstreamModelID *int64
}

// VirtualModelInternalCandidateSnapshot 保留旧名称兼容现有内部候选调用代码喵。
type VirtualModelInternalCandidateSnapshot = VirtualModelCandidateSnapshot

// VirtualModelExecutionSnapshot 保存一次请求所需的候选链和失败规则不可变读取结果喵。
type VirtualModelExecutionSnapshot struct {
	Candidates                []VirtualModelCandidateSnapshot   // 已按稳定顺序排列的启用候选快照喵。
	FailureRulesByCandidateID map[int][]VirtualModelFailureRule // 按候选编号归类且已排序的候选级失败规则快照喵。
	// GlobalFailureRules 保存模型级全局兜底失败规则快照，候选未配置规则时由其兜底喵。
	GlobalFailureRules []VirtualModelFailureRule
}

// GetVirtualModelExecutionSnapshot 在单个只读事务中构造候选和规则的不可变执行快照喵。
func GetVirtualModelExecutionSnapshot(virtualModelID int) (*VirtualModelExecutionSnapshot, error) {
	// 喵~防御：无效模型编号直接拒绝，避免开始无意义事务或查询全表喵。
	if virtualModelID <= 0 {
		return nil, gorm.ErrRecordNotFound
	}
	executionSnapshot := &VirtualModelExecutionSnapshot{Candidates: make([]VirtualModelCandidateSnapshot, 0), FailureRulesByCandidateID: make(map[int][]VirtualModelFailureRule), GlobalFailureRules: make([]VirtualModelFailureRule, 0)}
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
		// 候选级规则与模型级全局兜底规则在同一只读事务内读取，保证一次请求的快照一致性喵。
		globalFailureRules, globalRulesError := GetVirtualModelGlobalFailureRulesWithDB(transactionDatabase, virtualModelID)
		if globalRulesError != nil {
			return globalRulesError
		}
		executionSnapshot.Candidates = candidateSnapshots
		executionSnapshot.FailureRulesByCandidateID = failureRulesByCandidateID
		executionSnapshot.GlobalFailureRules = globalFailureRules
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
		Select("virtual_model_candidates.id AS candidate_id, virtual_model_candidates.virtual_model_id, virtual_model_candidates.stable_order, virtual_model_candidates.source_type, virtual_model_candidates.enabled, virtual_model_candidates.max_retries, virtual_model_candidates.timeout_seconds, virtual_model_candidates.hedge_threshold, virtual_model_candidates.hedge_freeze_seconds, virtual_model_internal_candidates.group_name, CASE WHEN virtual_model_candidates.source_type = ? THEN virtual_model_internal_candidates.real_model_name ELSE virtual_model_custom_candidates.real_model_name END AS real_model_name, virtual_model_custom_candidates.encrypted_base_url, virtual_model_custom_candidates.base_url_summary, virtual_model_custom_candidates.base_url_fingerprint, virtual_model_custom_candidates.encrypted_api_key, virtual_model_custom_candidates.api_key_fingerprint, virtual_model_custom_candidates.credential_version, virtual_model_custom_candidates.auth_style, virtual_model_custom_candidates.upstream_model_id", VirtualModelSourceInternal).
		Joins("LEFT JOIN virtual_model_internal_candidates ON virtual_model_internal_candidates.candidate_id = virtual_model_candidates.id AND virtual_model_candidates.source_type = ?", VirtualModelSourceInternal).
		Joins("LEFT JOIN virtual_model_custom_candidates ON virtual_model_custom_candidates.candidate_id = virtual_model_candidates.id AND virtual_model_candidates.source_type = ?", VirtualModelSourceCustom).
		Where("virtual_model_candidates.virtual_model_id = ? AND virtual_model_candidates.enabled = ?", virtualModelID, true).
		Order("virtual_model_candidates.stable_order ASC, virtual_model_candidates.id ASC").
		Find(&candidateSnapshots).Error
	// 喵~防御：历史数据库可能含旧认证枚举，读取时统一为稳定值以避免控制面回显内部实现细节喵。
	for candidateIndex := range candidateSnapshots {
		candidateSnapshots[candidateIndex].AuthStyle = VirtualModelAuthStyleFromStorage(candidateSnapshots[candidateIndex].AuthStyle)
	}
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

// GetVirtualModelGlobalFailureRulesWithDB 使用给定数据库连接按规则顺序读取模型级全局兜底规则喵。
// 返回时统一转换为候选规则结构，使规则决策函数无需区分两种来源喵。
func GetVirtualModelGlobalFailureRulesWithDB(database *gorm.DB, virtualModelID int) ([]VirtualModelFailureRule, error) {
	// 喵~防御：无效模型编号直接返回空列表，避免生成无界查询喵。
	if virtualModelID <= 0 {
		return []VirtualModelFailureRule{}, nil
	}
	// 喵~防御：数据库连接为空时拒绝查询，避免运行时空指针或绕过调用方事务边界喵。
	if database == nil {
		return nil, errors.New("virtual model database is unavailable")
	}
	storedGlobalRules := make([]VirtualModelGlobalFailureRule, 0)
	queryError := database.Where("virtual_model_id = ?", virtualModelID).Order("rule_order ASC, id ASC").Find(&storedGlobalRules).Error
	if queryError != nil {
		return nil, queryError
	}
	// 将模型级规则字段复制到候选规则结构，字段语义与候选规则完全一致，含规则级重试次数喵。
	globalFailureRules := make([]VirtualModelFailureRule, 0, len(storedGlobalRules))
	for _, storedRule := range storedGlobalRules {
		globalFailureRules = append(globalFailureRules, VirtualModelFailureRule{ID: storedRule.ID, RuleOrder: storedRule.RuleOrder, HTTPStatus: storedRule.HTTPStatus, HTTPStatusMax: storedRule.HTTPStatusMax, ErrorClass: storedRule.ErrorClass, BodyRegex: storedRule.BodyRegex, Action: storedRule.Action, FreezeSeconds: storedRule.FreezeSeconds, FreezeField: storedRule.FreezeField, FreezeUnit: storedRule.FreezeUnit, RetryCount: storedRule.RetryCount})
	}
	return globalFailureRules, nil
}

// GetVirtualModelGlobalFailureRules 按规则顺序读取模型级全局兜底规则喵。
func GetVirtualModelGlobalFailureRules(virtualModelID int) ([]VirtualModelFailureRule, error) {
	return GetVirtualModelGlobalFailureRulesWithDB(DB, virtualModelID)
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

// GetActiveVirtualModelManualFreezesWithDB 使用给定数据库连接读取当前仍处于手动冻结期的候选到期时间戳映射喵。
func GetActiveVirtualModelManualFreezesWithDB(database *gorm.DB, candidateIDs []int, currentTimestamp int64) (map[int]int64, error) {
	// 喵~防御：空候选集合或非法时间戳不执行数据库查询，避免无效条件扩大读取范围喵。
	if len(candidateIDs) == 0 || currentTimestamp <= 0 {
		return map[int]int64{}, nil
	}
	// 喵~防御：数据库连接为空时拒绝查询，避免运行时空指针或绕过调用方事务边界喵。
	if database == nil {
		return nil, errors.New("virtual model database is unavailable")
	}
	var freezeStates []VirtualModelManualFreeze
	queryError := database.Model(&VirtualModelManualFreeze{}).
		Where("candidate_id IN ? AND started_at <= ? AND expires_at > ?", candidateIDs, currentTimestamp, currentTimestamp).
		Find(&freezeStates).Error
	if queryError != nil {
		return nil, queryError
	}
	frozenUntilByCandidate := make(map[int]int64, len(freezeStates))
	for _, freezeState := range freezeStates {
		frozenUntilByCandidate[freezeState.CandidateID] = freezeState.ExpiresAt
	}
	return frozenUntilByCandidate, nil
}

// GetActiveVirtualModelManualFreezes 返回当前仍处于手动冻结期的候选到期时间戳映射喵。
func GetActiveVirtualModelManualFreezes(candidateIDs []int, currentTimestamp int64) (map[int]int64, error) {
	return GetActiveVirtualModelManualFreezesWithDB(DB, candidateIDs, currentTimestamp)
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

// RecordVirtualModelCustomFailure 在 owner 范围内原子累计一次自定义候选失败，达到连续失败阈值时设置冻结时间喵。
// 返回本次失败后是否达到阈值触发了冻结；阈值非正或参数非法时视为无操作并返回 false喵。
func RecordVirtualModelCustomFailure(ownerUserID int, identityDigest string, threshold int, freezeUntil int64, failureClass string, currentTimestamp int64) (bool, error) {
	return RecordVirtualModelCustomFailureWithDB(DB, ownerUserID, identityDigest, threshold, freezeUntil, failureClass, currentTimestamp)
}

// RecordVirtualModelCustomFailureWithDB 使用给定数据库连接累计自定义候选失败并条件冻结喵。
func RecordVirtualModelCustomFailureWithDB(database *gorm.DB, ownerUserID int, identityDigest string, threshold int, freezeUntil int64, failureClass string, currentTimestamp int64) (bool, error) {
	// 喵~防御：数据库连接为空时拒绝写入，避免运行时空指针或绕过调用方事务边界喵。
	if database == nil {
		return false, errors.New("virtual model database is unavailable")
	}
	// 喵~防御：缺少身份、所有者或非正阈值时按无操作处理，调用方按普通失败路径继续喵。
	if ownerUserID <= 0 || strings.TrimSpace(identityDigest) == "" || threshold <= 0 || currentTimestamp <= 0 {
		return false, nil
	}
	reachedThreshold := false
	transactionError := database.Transaction(func(tx *gorm.DB) error {
		var freezeState VirtualModelCustomFreezeState
		// lockForUpdate 在 MySQL/PostgreSQL 加行锁，SQLite 跳过锁语法（写事务天然串行）喵。
		queryError := lockForUpdate(tx).Where("owner_user_id = ? AND identity_digest = ?", ownerUserID, identityDigest).First(&freezeState).Error
		if errors.Is(queryError, gorm.ErrRecordNotFound) {
			// 首次失败：计数从 1 起，阈值 1 时直接视为触发冻结喵。
			freezeState = VirtualModelCustomFreezeState{OwnerUserID: ownerUserID, IdentityDigest: identityDigest, ConsecutiveFails: 1, LastFailureClass: strings.TrimSpace(failureClass), UpdatedTime: currentTimestamp}
			if 1 >= threshold {
				// 阈值 1 触发：设置冻结时间，计数已从 1 归零需要显式清零喵。
				freezeState.FrozenUntil = freezeUntil
				freezeState.ConsecutiveFails = 0
				reachedThreshold = true
			}
			return tx.Create(&freezeState).Error
		}
		if queryError != nil {
			return queryError
		}
		// 已有记录：连续失败计数加一，达到阈值时设置冻结时间并清零计数喵。
		freezeState.ConsecutiveFails++
		freezeState.LastFailureClass = strings.TrimSpace(failureClass)
		freezeState.UpdatedTime = currentTimestamp
		if freezeState.ConsecutiveFails >= threshold {
			// 达阈值触发冻结：设置冻结时间，计数清零供冻结到期后重新攒满一轮喵。
			freezeState.FrozenUntil = freezeUntil
			freezeState.ConsecutiveFails = 0
			reachedThreshold = true
		}
		return tx.Model(&freezeState).Select("frozen_until", "consecutive_fails", "last_failure_class", "updated_time").Updates(freezeState).Error
	})
	return reachedThreshold, transactionError
}

// ClearVirtualModelCustomFreezeState 使用给定数据库连接仅清除本次调用开始前已存在的自动冻结状态喵。
func ClearVirtualModelCustomFreezeStateWithDB(database *gorm.DB, ownerUserID int, identityDigest string, expectedUpdatedTime int64, currentTimestamp int64) error {
	// 喵~防御：无效输入无需触发写库，调用方成功路径可安全忽略该空操作喵。
	if ownerUserID <= 0 || strings.TrimSpace(identityDigest) == "" || expectedUpdatedTime <= 0 || currentTimestamp <= 0 {
		return nil
	}
	// 喵~防御：数据库连接为空时拒绝写入，避免运行时空指针或绕过调用方事务边界喵。
	if database == nil {
		return errors.New("virtual model database is unavailable")
	}
	// 喵~防御：仅匹配请求启动时观察到的版本，避免成功请求清除并发失败刚写入的新冻结喵。
	// 主人注意：这里必须显式绑定 Model，否则 GORM 无法从 map 推断表名并直接返回 "Table not set" 错误喵。
	return database.Model(&VirtualModelCustomFreezeState{}).Where("owner_user_id = ? AND identity_digest = ? AND updated_time = ?", ownerUserID, identityDigest, expectedUpdatedTime).Updates(map[string]any{"frozen_until": 0, "consecutive_fails": 0, "last_failure_class": "", "updated_time": currentTimestamp}).Error
}

// ClearVirtualModelCustomFreezeState 清除一次成功调用对应的自动冻结失败计数喵。
func ClearVirtualModelCustomFreezeState(ownerUserID int, identityDigest string, expectedUpdatedTime int64, currentTimestamp int64) error {
	return ClearVirtualModelCustomFreezeStateWithDB(DB, ownerUserID, identityDigest, expectedUpdatedTime, currentTimestamp)
}

// GetActiveVirtualModelInternalFreezeStatesWithDB 使用给定数据库连接查询当前用户可见内部候选的自动冻结状态喵。
func GetActiveVirtualModelInternalFreezeStatesWithDB(database *gorm.DB, ownerUserID int, candidateIDs []int, currentTimestamp int64) (map[int]VirtualModelInternalFreezeState, error) {
	// 喵~防御：无效 owner、空候选集合或非法时间不执行查询，避免跨用户或全表读取喵。
	if ownerUserID <= 0 || len(candidateIDs) == 0 || currentTimestamp <= 0 {
		return map[int]VirtualModelInternalFreezeState{}, nil
	}
	// 喵~防御：数据库连接为空时拒绝查询，避免运行时空指针或绕过调用方事务边界喵。
	if database == nil {
		return nil, errors.New("virtual model database is unavailable")
	}
	freezeStates := make([]VirtualModelInternalFreezeState, 0)
	queryError := database.Where("owner_user_id = ? AND candidate_id IN ? AND frozen_until > ?", ownerUserID, candidateIDs, currentTimestamp).Find(&freezeStates).Error
	if queryError != nil {
		return nil, queryError
	}
	freezeStatesByCandidate := make(map[int]VirtualModelInternalFreezeState, len(freezeStates))
	for _, freezeState := range freezeStates {
		freezeStatesByCandidate[freezeState.CandidateID] = freezeState
	}
	return freezeStatesByCandidate, nil
}

// GetActiveVirtualModelInternalFreezeStates 查询当前用户可见内部候选的自动冻结状态喵。
func GetActiveVirtualModelInternalFreezeStates(ownerUserID int, candidateIDs []int, currentTimestamp int64) (map[int]VirtualModelInternalFreezeState, error) {
	return GetActiveVirtualModelInternalFreezeStatesWithDB(DB, ownerUserID, candidateIDs, currentTimestamp)
}

// UpsertVirtualModelInternalFreezeStateWithDB 使用给定数据库连接在 owner 范围内更新内部候选的自动冻结状态喵。
func UpsertVirtualModelInternalFreezeStateWithDB(database *gorm.DB, ownerUserID int, candidateID int, frozenUntil int64, failureClass string, currentTimestamp int64) error {
	// 喵~防御：缺少所有者、候选编号或时间时拒绝写入，避免创建无法隔离或永不过期的冻结状态喵。
	if ownerUserID <= 0 || candidateID <= 0 || frozenUntil <= currentTimestamp || currentTimestamp <= 0 {
		return errors.New("virtual model internal freeze state is invalid")
	}
	// 喵~防御：数据库连接为空时拒绝写入，避免运行时空指针或绕过调用方事务边界喵。
	if database == nil {
		return errors.New("virtual model database is unavailable")
	}
	// 喵~防御：使用数据库原子 upsert，避免并发首次冻结时唯一键竞争导致请求被错误拒绝喵。
	return database.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "owner_user_id"}, {Name: "candidate_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			// 喵~防御：并发失败不得将更长的既有冻结缩短为较短的新冻结时间喵。
			"frozen_until":       gorm.Expr("CASE WHEN frozen_until > ? THEN frozen_until ELSE ? END", frozenUntil, frozenUntil),
			"consecutive_fails":  gorm.Expr("consecutive_fails + ?", 1),
			"last_failure_class": strings.TrimSpace(failureClass),
			"updated_time":       currentTimestamp,
		}),
	}).Create(&VirtualModelInternalFreezeState{OwnerUserID: ownerUserID, CandidateID: candidateID, FrozenUntil: frozenUntil, ConsecutiveFails: 1, LastFailureClass: strings.TrimSpace(failureClass), UpdatedTime: currentTimestamp}).Error
}

// UpsertVirtualModelInternalFreezeState 在 owner 范围内更新内部候选的自动冻结状态喵。
func UpsertVirtualModelInternalFreezeState(ownerUserID int, candidateID int, frozenUntil int64, failureClass string, currentTimestamp int64) error {
	return UpsertVirtualModelInternalFreezeStateWithDB(DB, ownerUserID, candidateID, frozenUntil, failureClass, currentTimestamp)
}

// RecordVirtualModelInternalFailure 在 owner 范围内原子累计一次内部候选失败，达到连续失败阈值时设置冻结时间喵。
// 返回本次失败后是否达到阈值触发了冻结；阈值非正或参数非法时视为无操作并返回 false，调用方按普通失败处理喵。
// 达到阈值时冻结时间置为 freezeUntil 且连续失败计数清零，触发后需重新攒满一轮（对齐 autoapi 语义）喵。
func RecordVirtualModelInternalFailure(ownerUserID int, candidateID int, threshold int, freezeUntil int64, failureClass string, currentTimestamp int64) (bool, error) {
	return RecordVirtualModelInternalFailureWithDB(DB, ownerUserID, candidateID, threshold, freezeUntil, failureClass, currentTimestamp)
}

// RecordVirtualModelInternalFailureWithDB 使用给定数据库连接累计内部候选失败并条件冻结喵。
func RecordVirtualModelInternalFailureWithDB(database *gorm.DB, ownerUserID int, candidateID int, threshold int, freezeUntil int64, failureClass string, currentTimestamp int64) (bool, error) {
	// 喵~防御：数据库连接为空时拒绝写入，避免运行时空指针或绕过调用方事务边界喵。
	if database == nil {
		return false, errors.New("virtual model database is unavailable")
	}
	// 喵~防御：非正阈值或非法参数按无操作处理，保持与调用方既有失败路径兼容喵。
	if ownerUserID <= 0 || candidateID <= 0 || threshold <= 0 || currentTimestamp <= 0 {
		return false, nil
	}
	reachedThreshold := false
	transactionError := database.Transaction(func(tx *gorm.DB) error {
		var freezeState VirtualModelInternalFreezeState
		// lockForUpdate 在 MySQL/PostgreSQL 加行锁，SQLite 跳过锁语法（写事务天然串行）喵。
		queryError := lockForUpdate(tx).Where("owner_user_id = ? AND candidate_id = ?", ownerUserID, candidateID).First(&freezeState).Error
		if errors.Is(queryError, gorm.ErrRecordNotFound) {
			// 首次失败：计数从 1 起，阈值 1 时直接视为触发冻结喵。
			freezeState = VirtualModelInternalFreezeState{OwnerUserID: ownerUserID, CandidateID: candidateID, ConsecutiveFails: 1, LastFailureClass: strings.TrimSpace(failureClass), UpdatedTime: currentTimestamp}
			if 1 >= threshold {
				// 阈值 1 触发：设置冻结时间，计数已从 1 归零需要显式清零喵。
				freezeState.FrozenUntil = freezeUntil
				freezeState.ConsecutiveFails = 0
				reachedThreshold = true
			}
			return tx.Create(&freezeState).Error
		}
		if queryError != nil {
			return queryError
		}
		// 已有记录：连续失败计数加一，达到阈值时设置冻结时间并清零计数喵。
		freezeState.ConsecutiveFails++
		freezeState.LastFailureClass = strings.TrimSpace(failureClass)
		freezeState.UpdatedTime = currentTimestamp
		if freezeState.ConsecutiveFails >= threshold {
			// 达阈值触发冻结：设置冻结时间，计数清零供冻结到期后重新攒满一轮喵。
			freezeState.FrozenUntil = freezeUntil
			freezeState.ConsecutiveFails = 0
			reachedThreshold = true
		}
		return tx.Model(&freezeState).Select("frozen_until", "consecutive_fails", "last_failure_class", "updated_time").Updates(freezeState).Error
	})
	return reachedThreshold, transactionError
}

// ClearVirtualModelInternalFreezeStateWithDB 使用给定数据库连接仅清除本次调用开始前已存在的内部候选自动冻结状态喵。
func ClearVirtualModelInternalFreezeStateWithDB(database *gorm.DB, ownerUserID int, candidateID int, expectedUpdatedTime int64, currentTimestamp int64) error {
	// 喵~防御：无效输入无需触发写库，调用方成功路径可安全忽略该空操作喵。
	if ownerUserID <= 0 || candidateID <= 0 || expectedUpdatedTime <= 0 || currentTimestamp <= 0 {
		return nil
	}
	// 喵~防御：数据库连接为空时拒绝写入，避免运行时空指针或绕过调用方事务边界喵。
	if database == nil {
		return errors.New("virtual model database is unavailable")
	}
	// 喵~防御：仅匹配请求启动时观察到的版本，避免成功请求清除并发失败刚写入的新冻结喵。
	return database.Model(&VirtualModelInternalFreezeState{}).Where("owner_user_id = ? AND candidate_id = ? AND updated_time = ?", ownerUserID, candidateID, expectedUpdatedTime).Updates(map[string]any{"frozen_until": 0, "consecutive_fails": 0, "last_failure_class": "", "updated_time": currentTimestamp}).Error
}

// ClearVirtualModelInternalFreezeState 清除一次成功调用对应的内部候选自动冻结失败计数喵。
func ClearVirtualModelInternalFreezeState(ownerUserID int, candidateID int, expectedUpdatedTime int64, currentTimestamp int64) error {
	return ClearVirtualModelInternalFreezeStateWithDB(DB, ownerUserID, candidateID, expectedUpdatedTime, currentTimestamp)
}

// DeleteVirtualModelByOwnerWithVersion 在版本匹配时事务删除所有关联数据并写入不可还原审计喵。
func DeleteVirtualModelByOwnerWithVersion(virtualModelID int, ownerUserID int, operatorID int, expectedVersion int64) error {
	// 喵~防御：无效身份、资源编号或版本拒绝执行，避免误删或陈旧页面删除新配置喵。
	if virtualModelID <= 0 || ownerUserID <= 0 || operatorID <= 0 || expectedVersion <= 0 {
		return gorm.ErrRecordNotFound
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		virtualModel := &VirtualModel{}
		if err := tx.Where("id = ? AND owner_user_id = ?", virtualModelID, ownerUserID).First(virtualModel).Error; err != nil {
			return gorm.ErrRecordNotFound
		}
		// 喵~防御：读取后再次比较事务内版本，确保删除不会覆盖在并发窗口内发生的配置更新喵。
		if virtualModel.Version != expectedVersion {
			return errors.New("virtual_model_version_conflict")
		}
		var candidateIDs []int
		if err := tx.Model(&VirtualModelCandidate{}).Where("virtual_model_id = ?", virtualModelID).Pluck("id", &candidateIDs).Error; err != nil {
			return err
		}
		if len(candidateIDs) > 0 {
			if err := tx.Unscoped().Where("candidate_id IN ?", candidateIDs).Delete(&VirtualModelFailureRule{}).Error; err != nil {
				return err
			}
			if err := tx.Unscoped().Where("candidate_id IN ?", candidateIDs).Delete(&VirtualModelInternalCandidate{}).Error; err != nil {
				return err
			}
			// 喵~防御：自定义候选含加密密文，模型删除时必须硬删除，避免残留可被未来代码误解密喵。
			if err := tx.Unscoped().Where("candidate_id IN ?", candidateIDs).Delete(&VirtualModelCustomCandidate{}).Error; err != nil {
				return err
			}
			if err := tx.Unscoped().Where("candidate_id IN ?", candidateIDs).Delete(&VirtualModelManualFreeze{}).Error; err != nil {
				return err
			}
			// 喵~防御：候选使用硬删除，避免软删除记录占用模型候选顺序唯一索引喵。
			if err := tx.Unscoped().Where("id IN ?", candidateIDs).Delete(&VirtualModelCandidate{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Unscoped().Where("virtual_model_id = ?", virtualModelID).Delete(&VirtualModelTokenBinding{}).Error; err != nil {
			return err
		}
		// 模型级全局兜底规则随模型一并硬删除，避免残留无主规则喵。
		if err := tx.Unscoped().Where("virtual_model_id = ?", virtualModelID).Delete(&VirtualModelGlobalFailureRule{}).Error; err != nil {
			return err
		}
		if err := tx.Create(&VirtualModelAuditLog{VirtualModelID: virtualModelID, OwnerUserID: ownerUserID, OperatorID: operatorID, Action: "delete", SummaryDigest: fmt.Sprintf("model:%d", virtualModelID), CreatedTime: time.Now().Unix()}).Error; err != nil {
			return err
		}
		// 喵~防御：主模型必须通过版本条件实际删除，否则整个事务回滚，避免留下伪造的删除审计喵。
		deleteResult := tx.Unscoped().Where("id = ? AND owner_user_id = ? AND version = ?", virtualModelID, ownerUserID, expectedVersion).Delete(&VirtualModel{})
		if deleteResult.Error != nil {
			return deleteResult.Error
		}
		if deleteResult.RowsAffected != 1 {
			return errors.New("virtual_model_version_conflict")
		}
		// 实体状态检测：模型删除后联动清理整体与候选节点的状态行，避免残留孤儿数据喵。
		if err := tx.Where("(scope = ? AND entity_id = ?) OR (scope = ? AND virtual_id = ?)",
			EntityProbeScopeVirtual, virtualModelID, EntityProbeScopeVirtualCandidate, virtualModelID).
			Delete(&EntityProbeState{}).Error; err != nil {
			return err
		}
		return nil
	})
}

// DeleteVirtualModelByOwner 在事务内删除所有关联数据并写入不可还原审计喵。
func DeleteVirtualModelByOwner(virtualModelID int, ownerUserID int, operatorID int) error {
	// 喵~防御：兼容旧调用方时先按当前版本读取再执行版本化删除，避免复制一套删除逻辑喵。
	if virtualModelID <= 0 || ownerUserID <= 0 || operatorID <= 0 {
		return gorm.ErrRecordNotFound
	}
	virtualModel := &VirtualModel{}
	if err := DB.Where("id = ? AND owner_user_id = ?", virtualModelID, ownerUserID).First(virtualModel).Error; err != nil {
		return gorm.ErrRecordNotFound
	}
	return DeleteVirtualModelByOwnerWithVersion(virtualModelID, ownerUserID, operatorID, virtualModel.Version)
}

// DeleteOrphanVirtualModelInternalCandidatesByModel 清理已无任何启用渠道支撑的内部候选喵。
// 当某 (分组, 真实模型) 在 abilities 表中不再有启用能力时，删除所有引用它的候选及其关联数据喵。
func DeleteOrphanVirtualModelInternalCandidatesByModel(modelName string) (int, error) {
	// 喵~防御：空模型名直接跳过，避免生成无条件关联删除喵。
	if strings.TrimSpace(modelName) == "" {
		return 0, nil
	}
	// 读取所有引用该模型的内部候选快照喵。
	var internalCandidates []VirtualModelInternalCandidate
	if err := DB.Where("real_model_name = ?", modelName).Find(&internalCandidates).Error; err != nil {
		return 0, err
	}
	if len(internalCandidates) == 0 {
		return 0, nil
	}
	// 收集已经没有任何启用渠道能力支撑的孤儿候选编号喵。
	orphanCandidateIDs := make([]int, 0, len(internalCandidates))
	for _, candidate := range internalCandidates {
		// 喵~防御：未指定分组的候选无法判定可用性，保守跳过不误删喵。
		if strings.TrimSpace(candidate.GroupName) == "" {
			continue
		}
		var enabledAbilityCount int64
		if err := DB.Model(&Ability{}).Where(commonGroupCol+" = ? AND model = ? AND enabled = ?", candidate.GroupName, candidate.RealModelName, true).Count(&enabledAbilityCount).Error; err != nil {
			return 0, err
		}
		if enabledAbilityCount == 0 {
			orphanCandidateIDs = append(orphanCandidateIDs, candidate.CandidateID)
		}
	}
	if len(orphanCandidateIDs) == 0 {
		return 0, nil
	}
	// 事务内级联删除孤儿候选及其规则、冻结和加密配置，保证一致性喵。
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Unscoped().Where("candidate_id IN ?", orphanCandidateIDs).Delete(&VirtualModelFailureRule{}).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().Where("candidate_id IN ?", orphanCandidateIDs).Delete(&VirtualModelInternalCandidate{}).Error; err != nil {
			return err
		}
		// 喵~防御：自定义候选含加密密文，候选删除时必须硬删除，避免残留可被未来代码误解密喵。
		if err := tx.Unscoped().Where("candidate_id IN ?", orphanCandidateIDs).Delete(&VirtualModelCustomCandidate{}).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().Where("candidate_id IN ?", orphanCandidateIDs).Delete(&VirtualModelManualFreeze{}).Error; err != nil {
			return err
		}
		// 喵~防御：候选使用硬删除，避免软删除记录占用模型候选顺序唯一索引喵。
		return tx.Unscoped().Where("id IN ?", orphanCandidateIDs).Delete(&VirtualModelCandidate{}).Error
	})
	if err != nil {
		return 0, err
	}
	return len(orphanCandidateIDs), nil
}
