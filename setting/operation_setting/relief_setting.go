package operation_setting

// 引入全局配置管理器，负责把本结构体注册、持久化到 options 表喵~
import "github.com/QuantumNous/new-api/setting/config"

// ReliefSetting 救济金功能配置：余额快花光、且订阅额度也用完的用户，可领一笔限时救济额度喵~
// 救济金本质是"给用户发一张 1 天期的订阅"，到期未用完的部分自动返还救济池（见 model/relief.go）。
type ReliefSetting struct {
	Enabled             bool    `json:"enabled"`               // 是否启用救济金功能（钱包页救济金卡片的总开关）
	PlanId              int     `json:"plan_id"`               // 发放救济金所用的订阅套餐 id；为 0 时启动会自动建一个隐藏套餐并回写
	ReserveAmount       float64 `json:"reserve_amount"`        // 救济池储备金，单位：美元；池可用额度 = AgentRouter池余额 - 储备金 - 已发放救济金
	BalanceThresholdUsd float64 `json:"balance_threshold_usd"` // 领取门槛：用户钱包余额低于此值（美元）才可领取
	CooldownHours       float64 `json:"cooldown_hours"`        // 两次领取之间的冷却时长，单位：小时
	GrantAmountUsd      float64 `json:"grant_amount_usd"`      // 单次救济金额度，单位：美元（仅用于自动建套餐时的默认总额）
}

// reliefSetting 默认配置：默认关闭，数值与主人拍板口径一致喵~
var reliefSetting = ReliefSetting{
	Enabled:             false, // 默认关闭，管理员在设置页手动开启
	PlanId:              0,     // 0 表示尚未配置，启动时自动建隐藏套餐并回写此值
	ReserveAmount:       1200,  // 储备金 1200 美元（主人口径：AgentRouter余额 - 1200 - x）
	BalanceThresholdUsd: 10,    // 钱包余额低于 10 美元才可领取
	CooldownHours:       2,     // 领取后冷却 2 小时
	GrantAmountUsd:      200,   // 单次发放 200 美元额度（1 天有效）
}

// init 把本配置注册进全局配置管理器，注册后读写、持久化、热更新都自动完成喵~
func init() {
	// 注册名 "relief_setting"，对应前端/DB 里的 relief_setting.* 键
	config.GlobalConfig.Register("relief_setting", &reliefSetting)
}

// GetReliefSetting 返回当前救济金配置（返回指针，调用方只读不要改字段）喵~
func GetReliefSetting() *ReliefSetting {
	// 直接返回全局单例的地址，内存值由配置管理器在热更新时维护
	return &reliefSetting
}
