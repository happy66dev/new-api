package operation_setting

// 引入全局配置管理器，负责把本结构体注册、持久化到 options 表喵~
import "github.com/QuantumNous/new-api/setting/config"

// AutoCheckinSetting 对接 AutoCheckin（每日签到）项目的配置，用于读取各站点的"池额度"喵~
// 这里只放"读 AutoCheckin 的 data.db 并在 /status 展示池额度"这一类非敏感开关与参数。
type AutoCheckinSetting struct {
	Enabled       bool   `json:"enabled"`        // 是否启用：开启后 /status 展示 AgentRouter/justwoker 池额度，并允许救济金读取池余额
	DataDbPath    string `json:"data_db_path"`   // AutoCheckin webui 的 data.db 绝对路径（只读打开，绝不写入）
	FreshnessDays int    `json:"freshness_days"` // 池额度时效：只统计最近这么多天内签到过的账号余额，单位：天
}

// autoCheckinSetting 默认配置：默认关闭，路径指向生产机上 AutoCheckin 的默认位置喵~
var autoCheckinSetting = AutoCheckinSetting{
	Enabled:       false,                        // 默认关闭，管理员在设置页手动开启
	DataDbPath:    `C:\AutoCheck\webui\data.db`, // 生产机默认路径，设置页可改
	FreshnessDays: 2,                            // 默认只认前 2 天内的余额数据（主人口径：数据为前 2 天内时效期）
}

// init 把本配置注册进全局配置管理器，注册后读写、持久化、热更新都自动完成喵~
func init() {
	// 注册名 "autocheckin_setting"，对应前端/DB 里的 autocheckin_setting.* 键
	config.GlobalConfig.Register("autocheckin_setting", &autoCheckinSetting)
}

// GetAutoCheckinSetting 返回当前 AutoCheckin 对接配置（返回指针，调用方只读不要改字段）喵~
func GetAutoCheckinSetting() *AutoCheckinSetting {
	// 直接返回全局单例的地址，内存值由配置管理器在热更新时维护
	return &autoCheckinSetting
}
