/**
 * 上游模型余额/费用的固定计费单位换算喵。
 *
 * 后端三账户（balance/available/share_limit）与费用以 10^-5 元单位存储，
 * 即每元拆成 100000 个单位，支持 5 位小数费率的小额调用不被舍成 0 喵。
 * 前端展示统一走本模块换算成元，避免在各页面重复魔法数字喵。
 */

// 每元对应的单位数：10^-5 元/单位喵。
const UPSTREAM_MODEL_UNITS_PER_YUAN = 100000

/**
 * 把 10^-5 元单位换算成元字符串展示，精确到 5 位小数并去掉末尾冗余零喵。
 * 例如 123456 单位 → "1.23456"，250000 单位 → "2.5" 喵。
 */
export function unitsToYuan(units: number): string {
  // 喵~防御：非有限数值回退为 0 元，避免 NaN 或无穷大污染展示喵。
  if (!Number.isFinite(units)) return '0'
  // 先固定 5 位小数再裁剪末尾零，兼顾精度与可读性喵。
  return (units / UPSTREAM_MODEL_UNITS_PER_YUAN)
    .toFixed(5)
    .replace(/(\.[0-9]*?)0+$/, '$1')
    .replace(/\.$/, '')
}

/**
 * 把元字符串换算成 10^-5 元单位，供保存到后端使用喵。
 * 例如 "2.5" → 250000，"0.0001" → 10 喵。
 */
export function yuanToUnits(yuan: string): number {
  // 喵~防御：非法或空输入回退为 0，避免 NaN 写入后端喵。
  const parsed = Number.parseFloat(yuan)
  return Number.isFinite(parsed) && parsed >= 0
    ? Math.round(parsed * UPSTREAM_MODEL_UNITS_PER_YUAN)
    : 0
}
