package service

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	glebarezsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// autoCheckinPoolRefreshInterval 池额度刷新周期：余额每天签到才变一次，1 分钟足够新鲜又几乎零开销喵~
const autoCheckinPoolRefreshInterval = 1 * time.Minute

// autoCheckinTimeLayout AutoCheckin 写库用的时间格式：datetime.now().isoformat(sep=' ') 得到 "2026-10-10 14:30:05" 喵~
const autoCheckinTimeLayout = "2006-01-02 15:04:05"

// autoCheckinSiteFold 同站折叠表：这些变体站点键登录的是同一个站点，统计时折回主站喵~
// 口径与 AutoCheckin 的 SITE_DISPLAY_FOLD / proxy keysource 保持一致。
var autoCheckinSiteFold = map[string]string{
	"agentrouter-linuxdo": "agentrouter", // linuxdo 变体登录的是 agentrouter.org 同一站点
	"budsai-email":        "budsai",      // budsai-email 与 budsai 是同一站点
}

// autoCheckinBalanceCleanupRe 清洗余额文本：只保留数字、负号与小数点，其余（$、逗号、空白、字母）全部剥掉喵~
var autoCheckinBalanceCleanupRe = regexp.MustCompile(`[^0-9.\-]`)

// autoCheckinPoolState 保存一份池额度快照 + 读取状态，供 /status 与救济金只读取用喵~
type autoCheckinPoolState struct {
	mu        sync.RWMutex       // 读写锁：刷新时加写锁、查询时加读锁
	bySite    map[string]float64 // 折叠后站点 -> 余额合计（美元），例如 {"agentrouter": 3456.78}
	updatedAt int64              // 上次成功刷新的 unix 秒；0 表示从未成功刷新过
	lastError string             // 上次刷新失败原因，正常为空字符串
	db        *gorm.DB           // 复用的只读连接（懒加载）
	dbPath    string             // 当前连接对应的 data.db 路径，路径变了要重开
}

// autoCheckinPool 全局唯一的池额度快照实例喵~
var autoCheckinPool = &autoCheckinPoolState{bySite: map[string]float64{}}

// autoCheckinPoolTaskOnce 保证后台刷新任务只启动一次喵~
var autoCheckinPoolTaskOnce sync.Once

// foldAutoCheckinSite 把存储层站点键折叠成统计用的主站键喵~
func foldAutoCheckinSite(site string) string {
	// 去掉首尾空白，空值兜底当 agentrouter（与 AutoCheckin 旧库口径一致）
	actual := strings.TrimSpace(site)
	// 空站点名默认视为 agentrouter
	if actual == "" {
		return "agentrouter"
	}
	// 命中折叠表就折回主站，否则原样返回
	if folded, ok := autoCheckinSiteFold[actual]; ok {
		return folded
	}
	return actual
}

// parseAutoCheckinBalance 从余额文本抠出数值（移植自 AutoCheckin 的 parse_balance_number）喵~
// 输入：形如 "$9.99" / "1,234.56" / "45.60" 的余额原文
// 输出：数值与是否解析成功；空文本/非法形态都返回 (0, false)
func parseAutoCheckinBalance(text string) (float64, bool) {
	// 喵~防御：空文本没有数值可抠，直接判失败
	if strings.TrimSpace(text) == "" {
		return 0, false
	}
	// 剥掉货币符号、千分位逗号、空白与字母，只留数字/负号/小数点
	cleaned := autoCheckinBalanceCleanupRe.ReplaceAllString(text, "")
	// 喵~防御：剥完只剩孤零零的点/负号等非法形态，视为无余额
	if cleaned == "" || cleaned == "." || cleaned == "-" || cleaned == "-." {
		return 0, false
	}
	// 转成浮点数；极端格式转换失败也按无余额处理，不拖垮统计
	value, err := strconv.ParseFloat(cleaned, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

// GetAutoCheckinPoolUSD 返回某站点（会先折叠）当前的池余额合计，单位美元；没有数据返回 0 喵~
func GetAutoCheckinPoolUSD(site string) float64 {
	// 查询也先折叠一次，调用方传 agentrouter-linuxdo 也能命中
	key := foldAutoCheckinSite(site)
	// 加读锁快照读取，避免与刷新写入竞争
	autoCheckinPool.mu.RLock()
	defer autoCheckinPool.mu.RUnlock()
	// map 读不到返回零值 0，正好表示"无数据"
	return autoCheckinPool.bySite[key]
}

// AutoCheckinPoolSnapshot 返回池额度快照的拷贝 + 上次刷新时间 + 错误原因，供 /status 展示喵~
func AutoCheckinPoolSnapshot() (map[string]float64, int64, string) {
	// 加读锁，拷一份出去，绝不把内部 map 直接暴露给调用方（防外部改写）
	autoCheckinPool.mu.RLock()
	defer autoCheckinPool.mu.RUnlock()
	// 复制一份，长度很小（站点数个），开销可忽略
	cloned := make(map[string]float64, len(autoCheckinPool.bySite))
	for site, amount := range autoCheckinPool.bySite {
		cloned[site] = amount
	}
	return cloned, autoCheckinPool.updatedAt, autoCheckinPool.lastError
}

// StartAutoCheckinPoolRefreshTask 启动后台定时刷新任务，让 /status 永远读缓存、不同步打开 sqlite 喵~
func StartAutoCheckinPoolRefreshTask() {
	// 用 sync.Once 保证整个进程只起一个刷新循环
	autoCheckinPoolTaskOnce.Do(func() {
		// 用项目统一的 gopool 起协程（与订阅重置任务一致）
		gopool.Go(func() {
			// 建一个定时器，按固定周期触发
			ticker := time.NewTicker(autoCheckinPoolRefreshInterval)
			defer ticker.Stop()
			// 启动即先刷一次，避免首个 /status 请求拿到空数据
			refreshAutoCheckinPoolOnce()
			// 之后每到周期刷一次
			for range ticker.C {
				refreshAutoCheckinPoolOnce()
			}
		})
	})
}

// openAutoCheckinDB 懒加载 / 按需重开 data.db 的只读连接喵~
// 输入：data.db 绝对路径
// 输出：可用的 *gorm.DB 或错误
// 边界条件：文件不存在直接报错（绝不创建新库，避免在错误路径写出空文件）
func openAutoCheckinDB(path string) (*gorm.DB, error) {
	// 喵~防御：路径为空说明还没配置，直接报错让上层记录原因
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("AutoCheckin data.db 路径未配置")
	}
	// 已有连接且路径没变，直接复用
	if autoCheckinPool.db != nil && autoCheckinPool.dbPath == path {
		return autoCheckinPool.db, nil
	}
	// 喵~防御：文件不存在就报错，不走 open（SQLite open 可能会凭空建一个空库）
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("data.db 不存在或不可访问: %v", err)
	}
	// 构造只读 DSN：file: URI + mode=ro（只读）+ busy_timeout（等 webui 写锁最多 5 秒）
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)", filepath.ToSlash(path))
	// 用静默日志器打开，避免把每条 SQL 打进主日志
	db, err := gorm.Open(glebarezsqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("打开 data.db 失败: %v", err)
	}
	// 再上一道保险：query_only 禁止任何写操作，彻底保证不碰 AutoCheckin 的库
	_ = db.Exec("PRAGMA query_only = 1").Error
	// 换连接前，先关掉旧连接，避免句柄泄漏
	if autoCheckinPool.db != nil {
		if sqlDB, e := autoCheckinPool.db.DB(); e == nil {
			_ = sqlDB.Close()
		}
	}
	// 记住新连接与其路径，供下次复用
	autoCheckinPool.db = db
	autoCheckinPool.dbPath = path
	return db, nil
}

// autoCheckinAccountRow 承接每个账号"最近一条带余额记录"的查询结果喵~
type autoCheckinAccountRow struct {
	Site      string `gorm:"column:site"`       // 账号所属站点（可能是变体键，稍后折叠）
	Balance   string `gorm:"column:balance"`    // 最近一次带余额记录的余额原文
	StartedAt string `gorm:"column:started_at"` // 该记录的开始时间文本，用于判定是否在时效期内
}

// refreshAutoCheckinPoolOnce 刷新一次池额度快照：只读 data.db、按站点汇总时效期内的余额喵~
func refreshAutoCheckinPoolOnce() {
	// 读一次配置（结构体读取，无需加 OptionMap 锁）
	setting := operation_setting.GetAutoCheckinSetting()
	// 功能没开：清空快照并直接返回，连文件都不碰
	if !setting.Enabled {
		autoCheckinPool.mu.Lock()
		autoCheckinPool.bySite = map[string]float64{}
		autoCheckinPool.lastError = ""
		autoCheckinPool.mu.Unlock()
		return
	}
	// 时效天数兜底：配置非法（<=0）时回落到 2 天
	freshnessDays := setting.FreshnessDays
	if freshnessDays <= 0 {
		freshnessDays = 2
	}
	// 打开（或复用）只读连接
	db, err := openAutoCheckinDB(setting.DataDbPath)
	if err != nil {
		// 喵~防御：打不开就记错误并保留上次成功的快照（不清空），避免 /status 突然变空
		autoCheckinPool.mu.Lock()
		autoCheckinPool.lastError = err.Error()
		autoCheckinPool.mu.Unlock()
		common.SysError("刷新 AutoCheckin 池额度失败: " + err.Error())
		return
	}
	// 取每个启用账号"最近一条带余额记录"的余额与时间（相关子查询，账号量级很小开销可忽略）
	// 主人注意：account 行通常只有几十条，子查询成本可忽略；若未来账号数破千可考虑改窗口函数喵~
	const poolQuerySQL = `
SELECT a.site AS site,
       (SELECT h.balance FROM history h WHERE h.account_id = a.id AND h.balance != '' ORDER BY h.id DESC LIMIT 1) AS balance,
       (SELECT h.started_at FROM history h WHERE h.account_id = a.id AND h.balance != '' ORDER BY h.id DESC LIMIT 1) AS started_at
FROM accounts a
WHERE a.enabled = 1`
	// 执行查询，扫描进行切片
	var rows []autoCheckinAccountRow
	if err := db.Raw(poolQuerySQL).Scan(&rows).Error; err != nil {
		// 喵~防御：查询失败同样保留旧快照并记错误
		autoCheckinPool.mu.Lock()
		autoCheckinPool.lastError = err.Error()
		autoCheckinPool.mu.Unlock()
		common.SysError("查询 AutoCheckin 池额度失败: " + err.Error())
		return
	}
	// 时效期下界：早于这个时刻的余额记录一律不计入
	now := time.Now()
	freshnessCutoff := now.Add(-time.Duration(freshnessDays) * 24 * time.Hour)
	// 按折叠后站点累加余额
	summed := map[string]float64{}
	for _, row := range rows {
		// 余额解析不出（空记录/乱码）就跳过该账号
		value, ok := parseAutoCheckinBalance(row.Balance)
		if !ok {
			continue
		}
		// 解析该记录时间；按本地时区解析（AutoCheckin 写库用的是本地时间）
		recordedAt, parseErr := time.ParseInLocation(autoCheckinTimeLayout, strings.TrimSpace(row.StartedAt), time.Local)
		// 喵~防御：时间解析失败无法判定时效，保守剔除（宁可少算也不展示过期数据）
		if parseErr != nil {
			continue
		}
		// 不在时效期内（太旧）就跳过
		if recordedAt.Before(freshnessCutoff) {
			continue
		}
		// 累加到折叠后的主站上
		summed[foldAutoCheckinSite(row.Site)] += value
	}
	// 写回快照：加写锁整体替换，并清空错误、记录成功时间
	autoCheckinPool.mu.Lock()
	autoCheckinPool.bySite = summed
	autoCheckinPool.updatedAt = now.Unix()
	autoCheckinPool.lastError = ""
	autoCheckinPool.mu.Unlock()
}
