/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { EXCLUDED_GROUPS, FILTER_ALL, QUOTA_TYPE_VALUES } from '../constants'
import type { PricingModel, PriceType } from '../types'

// ----------------------------------------------------------------------------
// Model Helper Utilities
// ----------------------------------------------------------------------------

/**
 * Get available groups for a model
 */
export function getAvailableGroups(
  model: PricingModel,
  usableGroup: Record<string, { desc: string; ratio: number }>,
  visibleGroups: readonly string[] = []
): string[] {
  const modelEnableGroups = Array.isArray(model.enable_groups)
    ? model.enable_groups
    : []
  const groups = new Set([...Object.keys(usableGroup), ...visibleGroups])

  return [...groups]
    .filter((g) => !EXCLUDED_GROUPS.includes(g))
    .filter((g) => modelEnableGroups.includes(g))
}

/**
 * Read a configured group ratio while preserving valid zero ratios.
 */
export function getConfiguredGroupRatio(
  groupRatio: Record<string, number>,
  group: string
): number {
  const ratio = groupRatio[group]
  return typeof ratio === 'number' && Number.isFinite(ratio) ? ratio : 1
}

/**
 * Resolve the group ratio used by model square summary prices.
 *
 * When no specific group is selected, the model square shows the best price
 * available to the viewer. When a group filter is active, it shows that
 * group's price instead.
 */
export function getDisplayGroupRatio(
  model: PricingModel,
  selectedGroup?: string
): number {
  const modelEnableGroups = Array.isArray(model.enable_groups)
    ? model.enable_groups
    : []
  const groupRatio = model.group_ratio || {}

  if (
    selectedGroup &&
    selectedGroup !== FILTER_ALL &&
    modelEnableGroups.includes(selectedGroup)
  ) {
    return getConfiguredGroupRatio(groupRatio, selectedGroup)
  }

  const enabledAllGroups = modelEnableGroups.includes('all')
  const availableRatios = Object.entries(groupRatio)
    .filter(([group]) => enabledAllGroups || modelEnableGroups.includes(group))
    .map(([, ratio]) => ratio)
    .filter((ratio) => typeof ratio === 'number' && Number.isFinite(ratio))

  return availableRatios.length > 0 ? Math.min(...availableRatios) : 1
}

/**
 * Replace model placeholder in endpoint path
 */
export function replaceModelInPath(path: string, modelName: string): string {
  return path.replaceAll('{model}', modelName)
}

/**
 * Check if model is token-based pricing
 */
export function isTokenBasedModel(model: PricingModel): boolean {
  return model.quota_type === QUOTA_TYPE_VALUES.TOKEN
}

/**
 * 把模型投影成「某个分组视角下的模型」，让所有既有价格函数直接算出分组定制价喵。
 *
 * 为什么要投影而不是给每个价格函数都加个 group 参数喵：
 *
 *   分组定制定价会改掉计费方式、按次价、各类倍率一整套字段。与其在十几个格式化函数里
 *   到处传分组、到处判空，不如先把「这个分组看到的模型长什么样」算出来，
 *   后面的展示逻辑一行都不用改，回归风险最小喵。
 *
 * 边界与约定喵：
 * - 没传分组、选了「全部分组」、或该分组没有定制时，原样返回入参对象（同一引用），
 *   既保证行为与改造前完全一致，也避免多余的对象分配触发无谓重渲染喵。
 * - 返回的仍是未乘分组倍率的基础价，倍率由调用方按 group_ratio 另乘喵。
 * - entry.billing_mode 为空即代表该分组不是阶梯计费，所以这里直接用 entry 的值覆盖
 *   顶层字段，不做「回落到全局 billing_mode」的兜底，否则会把已改成按量的分组
 *   又错误地显示成阶梯计费喵。
 */
export function resolveGroupPricingModel(
  model: PricingModel,
  group?: string
): PricingModel {
  // 喵~防御：没有分组上下文或选的是「全部」时不做投影，直接用全局价展示喵。
  if (!group || group === FILTER_ALL) return model
  const entry = model.group_pricing?.[group]
  // 喵~防御：该分组没配定制（绝大多数情况）时原样返回，保持引用稳定喵。
  if (!entry) return model
  return {
    ...model,
    quota_type: entry.quota_type,
    model_ratio: entry.model_ratio,
    model_price: entry.model_price,
    completion_ratio: entry.completion_ratio,
    cache_ratio: entry.cache_ratio ?? null,
    create_cache_ratio: entry.create_cache_ratio ?? null,
    image_ratio: entry.image_ratio ?? null,
    audio_ratio: entry.audio_ratio ?? null,
    audio_completion_ratio: entry.audio_completion_ratio ?? null,
    billing_mode: entry.billing_mode,
    billing_expr: entry.billing_expr,
  }
}

/**
 * 判断某个分组下这个模型是按量还是按次计费喵。
 * 没有分组定制时回落到模型的全局 quota_type，与改造前的展示一致喵。
 */
export function getGroupQuotaType(model: PricingModel, group: string): number {
  const entry = model.group_pricing?.[group]
  // 喵~防御：quota_type 必须是有限数字才可信，否则回落全局值避免展示错位喵。
  if (entry && Number.isFinite(entry.quota_type)) return entry.quota_type
  return model.quota_type
}

/**
 * 判断这个模型是否存在任何分组定制定价，用来决定要不要走「按计费方式分卡片」的展示喵。
 */
export function hasGroupPricingOverrides(model: PricingModel): boolean {
  return Object.keys(model.group_pricing ?? {}).length > 0
}

/**
 * 判断某个价格类型在这个模型上到底有没有配价，没配就不该渲染对应的列或行喵。
 *
 * 为什么需要它喵：
 *
 *   分组定制定价允许「A 组有缓存优惠、B 组没有缓存」，所以缓存/图片/音频这些列
 *   是按分组逐个判断的。列头取所有分组的并集，某个分组缺这项价时单元格要显示 '-'，
 *   否则会把 NaN 直接渲染给用户看喵。
 *
 * 输入价与输出价是按量计费的必备项，恒为 true 喵。
 */
export function hasPriceTypeRatio(
  model: PricingModel,
  type: PriceType
): boolean {
  switch (type) {
    case 'input':
    case 'output':
      return true
    case 'cache':
      return isUsableRatio(model.cache_ratio)
    case 'create_cache':
      return isUsableRatio(model.create_cache_ratio)
    case 'image':
      return isUsableRatio(model.image_ratio)
    case 'audio_input':
      return isUsableRatio(model.audio_ratio)
    // 音频输出价需要音频输入倍率与音频输出倍率同时存在才算得出来喵。
    case 'audio_output':
      return (
        isUsableRatio(model.audio_ratio) &&
        isUsableRatio(model.audio_completion_ratio)
      )
    default:
      return false
  }
}

/** 倍率必须是非空的有限数字才算配过，null/undefined/NaN 一律当作没配喵。 */
function isUsableRatio(value: number | null | undefined): boolean {
  return value != null && Number.isFinite(Number(value))
}
