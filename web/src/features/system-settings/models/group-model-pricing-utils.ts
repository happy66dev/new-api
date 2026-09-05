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
import { safeJsonParse } from '../utils/json-parser'

/**
 * 分组定制定价的前端数据结构与「草稿 ↔ 配置」互转工具喵。
 *
 * 这里的字段名必须和后端 setting/ratio_setting/group_model_pricing.go 里
 * GroupModelPriceOverride 的 json tag 完全一致，否则保存后后端读不到喵。
 *
 * 最重要的一条约定喵：**字段缺失（undefined）表示「继承全局配置」**，
 * 显式填 0 表示「这个分组真的免费」。所以草稿里的空字符串必须转成「不写这个键」，
 * 绝不能转成 0，不然会把继承悄悄改成免费喵。
 */

/** 分组不指定计费方式，沿用全局判定（全局配了按次价就按次，否则按量）喵。 */
export const GROUP_BILLING_MODE_INHERIT = ''
/** 该分组强制按量计费，用倍率乘 token 数结算喵。 */
export const GROUP_BILLING_MODE_PER_TOKEN = 'per_token'
/** 该分组强制按次计费，每次请求收固定美元价喵。 */
export const GROUP_BILLING_MODE_PER_CALL = 'per_call'
/** 该分组走阶梯计费表达式，价格完全由表达式决定，不再看任何倍率喵。 */
export const GROUP_BILLING_MODE_TIERED = 'tiered_expr'

/** 单条「分组 × 模型」定价覆盖项，字段与后端 json tag 一一对应喵。 */
export type GroupPricingOverride = {
  /** 计费方式，取值见 GROUP_BILLING_MODE_* 常量；缺失表示继承全局喵。 */
  billing_mode?: string
  /** 按次计费单价，单位美元/次喵。 */
  model_price?: number
  /** 按量计费的输入倍率，输入价 = 倍率 × 2 美元/百万 token 喵。 */
  model_ratio?: number
  /** 输出倍率，输出价 = 输入价 × 该倍率喵。 */
  completion_ratio?: number
  /** 缓存命中（读取）倍率；上游没有缓存优惠时填 1 喵。 */
  cache_ratio?: number
  /** 缓存写入倍率，未配置时全局默认 1.25 喵。 */
  create_cache_ratio?: number
  /** 图片输入倍率喵。 */
  image_ratio?: number
  /** 音频输入倍率喵。 */
  audio_ratio?: number
  /** 音频输出倍率喵。 */
  audio_completion_ratio?: number
}

/** 「分组名 -> 模型名 -> 定价覆盖项」两层映射，就是 GroupModelPricing 那份 JSON 喵。 */
export type GroupModelPricingMap = Record<
  string,
  Record<string, GroupPricingOverride>
>

/**
 * 覆盖项里纯数值字段的键名集合喵。
 * 把 billing_mode（字符串字段）排除掉，编辑面板遍历数值输入框时才能安全地按 key 赋数字喵。
 */
export type GroupPricingNumericKey = Exclude<
  keyof GroupPricingOverride,
  'billing_mode'
>

/** 「分组名 -> 模型名 -> 字符串值」两层映射，分组计费方式与分组表达式都用它喵。 */
export type GroupBillingTextMap = Record<string, Record<string, string>>

/** 分组定制定价编辑器的表单值：三份配置各自一个 JSON 字符串喵。 */
export type GroupModelPricingFormValues = {
  /** 对应 API key `GroupModelPricing` 喵。 */
  GroupModelPricing: string
  /** 对应 API key `billing_setting.group_billing_mode` 喵。 */
  GroupBillingMode: string
  /** 对应 API key `billing_setting.group_billing_expr` 喵。 */
  GroupBillingExpr: string
}

/** 编辑面板里的草稿：所有数值都用字符串存，空串代表「留空 = 继承全局」喵。 */
export type GroupPricingDraft = {
  /** 模型名，例如 deepseek-chat 喵。 */
  modelName: string
  /** 当前选中的计费方式，取值见 GROUP_BILLING_MODE_* 常量喵。 */
  billingMode: string
  /** 按次价格输入框的原始文本喵。 */
  modelPrice: string
  /** 模型倍率输入框的原始文本喵。 */
  modelRatio: string
  /** 补全倍率输入框的原始文本喵。 */
  completionRatio: string
  /** 缓存读取倍率输入框的原始文本喵。 */
  cacheRatio: string
  /** 缓存写入倍率输入框的原始文本喵。 */
  createCacheRatio: string
  /** 图片倍率输入框的原始文本喵。 */
  imageRatio: string
  /** 音频倍率输入框的原始文本喵。 */
  audioRatio: string
  /** 音频补全倍率输入框的原始文本喵。 */
  audioCompletionRatio: string
  /** 阶梯计费表达式的原始文本，仅计费方式为 tiered_expr 时有意义喵。 */
  billingExpr: string
}

/** 草稿字段 ↔ 覆盖项字段 ↔ 界面标签 的对应关系，编辑面板直接遍历它渲染输入框喵。 */
export const GROUP_PRICING_NUMERIC_FIELDS: ReadonlyArray<{
  /** 草稿里的字段名喵。 */
  field: keyof GroupPricingDraft
  /** 覆盖项 JSON 里的字段名，只可能是数值字段喵。 */
  jsonKey: GroupPricingNumericKey
  /** i18n 文案 key 喵。 */
  labelKey: string
}> = [
  {
    field: 'modelPrice',
    jsonKey: 'model_price',
    labelKey: 'Per-request price (USD)',
  },
  { field: 'modelRatio', jsonKey: 'model_ratio', labelKey: 'Model ratio' },
  {
    field: 'completionRatio',
    jsonKey: 'completion_ratio',
    labelKey: 'Completion ratio',
  },
  { field: 'cacheRatio', jsonKey: 'cache_ratio', labelKey: 'Cache ratio' },
  {
    field: 'createCacheRatio',
    jsonKey: 'create_cache_ratio',
    labelKey: 'Create cache ratio',
  },
  { field: 'imageRatio', jsonKey: 'image_ratio', labelKey: 'Image ratio' },
  { field: 'audioRatio', jsonKey: 'audio_ratio', labelKey: 'Audio ratio' },
  {
    field: 'audioCompletionRatio',
    jsonKey: 'audio_completion_ratio',
    labelKey: 'Audio completion ratio',
  },
]

/** 把「分组 -> 模型 -> 字符串」的 JSON 文本解析成对象，坏 JSON 一律回落空对象喵。 */
export function parseGroupBillingText(
  text: string | undefined
): GroupBillingTextMap {
  // 喵~防御：文本为空或不是合法 JSON 时返回空对象，绝不让表格因为解析失败而崩掉喵。
  return safeJsonParse<GroupBillingTextMap>(text, {
    fallback: {},
    silent: true,
  })
}

/** 把配置对象序列化回 JSON 文本；空对象写成 `{}` 而不是空串，后端校验才认喵。 */
export function stringifyGroupPricing(value: unknown): string {
  // 缩进 2 空格是为了切到 JSON 模式时人还能读，和其它设置项的写法保持一致喵。
  return JSON.stringify(value ?? {}, null, 2)
}

/** 数值字段转文本：undefined/null 转空串（表示继承），数字原样转字符串喵。 */
function numberToText(value: number | undefined | null): string {
  // 喵~防御：未配置的字段必须显示成空，不能显示 0，否则用户会误以为这个分组免费喵。
  if (value === undefined || value === null) {
    return ''
  }
  // 喵~防御：NaN 与 Infinity 无法编辑，按未配置处理，避免把脏值带回配置喵。
  if (!Number.isFinite(value)) {
    return ''
  }
  return String(value)
}

/**
 * 把一条已有配置还原成编辑面板的草稿喵。
 *
 * 输入：模型名，以及该模型在三份配置里各自的值（都可能是 undefined，表示那份没配）。
 * 输出：填好的草稿。
 * 边界：分组级阶梯计费优先——只要分组计费方式是 tiered_expr，面板就切到表达式模式，
 * 因为此时定价覆盖里的倍率对这个分组已经不生效了喵。
 */
export function buildDraftFromOverride(
  modelName: string,
  override: GroupPricingOverride | undefined,
  groupBillingMode: string | undefined,
  groupBillingExpr: string | undefined
): GroupPricingDraft {
  // 分组级声明了阶梯计费时直接进表达式模式，数值框留空避免误导喵。
  if (groupBillingMode === GROUP_BILLING_MODE_TIERED) {
    return {
      modelName,
      billingMode: GROUP_BILLING_MODE_TIERED,
      modelPrice: '',
      modelRatio: '',
      completionRatio: '',
      cacheRatio: '',
      createCacheRatio: '',
      imageRatio: '',
      audioRatio: '',
      audioCompletionRatio: '',
      billingExpr: groupBillingExpr ?? '',
    }
  }
  return {
    modelName,
    // 喵~防御：覆盖项缺失或没写 billing_mode 时按「继承全局」显示喵。
    billingMode: override?.billing_mode ?? GROUP_BILLING_MODE_INHERIT,
    modelPrice: numberToText(override?.model_price),
    modelRatio: numberToText(override?.model_ratio),
    completionRatio: numberToText(override?.completion_ratio),
    cacheRatio: numberToText(override?.cache_ratio),
    createCacheRatio: numberToText(override?.create_cache_ratio),
    imageRatio: numberToText(override?.image_ratio),
    audioRatio: numberToText(override?.audio_ratio),
    audioCompletionRatio: numberToText(override?.audio_completion_ratio),
    billingExpr: groupBillingExpr ?? '',
  }
}

/** draftToOverride 的返回值：要么带着可写入的覆盖项，要么带着一条待翻译的错误文案喵。 */
export type DraftConversionResult =
  | { ok: true; override: GroupPricingOverride }
  | { ok: false; messageKey: string }

/**
 * 把编辑面板的草稿转成可写入配置的覆盖项喵。
 *
 * 整体思路喵：
 *  1. 阶梯计费模式只需要表达式，数值字段一律不写，先单独校验表达式非空；
 *  2. 其余模式逐个解析数值框：空串跳过（继承全局），非空则必须是有限非负数；
 *  3. 声明按次计费却没填按次价时拦下来——后端也会拦，但前端先提示体验更好喵。
 *
 * 输入：草稿。输出：成功时是覆盖项，失败时是错误文案 key（调用方负责 t() 翻译）。
 * 边界：负数、NaN、Infinity、非数字文本全部判为非法，绝不写进计费配置喵。
 */
export function draftToOverride(
  draft: GroupPricingDraft
): DraftConversionResult {
  const isTiered = draft.billingMode === GROUP_BILLING_MODE_TIERED
  if (isTiered) {
    // 喵~防御：表达式为空的阶梯计费会让这个分组算不出价钱，必须拦住喵。
    if (draft.billingExpr.trim() === '') {
      return { ok: false, messageKey: 'Billing expression is required' }
    }
    return { ok: true, override: { billing_mode: GROUP_BILLING_MODE_TIERED } }
  }

  const override: GroupPricingOverride = {}
  // 计费方式为「继承全局」时不写这个键，保持 JSON 干净，语义也更明确喵。
  if (draft.billingMode !== GROUP_BILLING_MODE_INHERIT) {
    override.billing_mode = draft.billingMode
  }

  for (const numericField of GROUP_PRICING_NUMERIC_FIELDS) {
    const rawText = draft[numericField.field].trim()
    // 留空表示继承全局，这个键就不写进 JSON 喵。
    if (rawText === '') {
      continue
    }
    const parsedValue = Number(rawText)
    // 喵~防御：非数字或 NaN/Infinity 会一路污染额度计算，直接拒绝保存喵。
    if (!Number.isFinite(parsedValue)) {
      return { ok: false, messageKey: 'Pricing values must be finite numbers' }
    }
    // 喵~防御：负价格会算出负额度（等于给用户返钱），绝对不允许喵。
    if (parsedValue < 0) {
      return { ok: false, messageKey: 'Pricing values cannot be negative' }
    }
    override[numericField.jsonKey] = parsedValue
  }

  // 喵~防御：强制按次却没填单价时，是否合法取决于全局有没有配按次价，前端看不到，
  // 所以这里要求必须显式填写，避免静默变成 0 元/次喵。
  if (
    draft.billingMode === GROUP_BILLING_MODE_PER_CALL &&
    override.model_price === undefined
  ) {
    return { ok: false, messageKey: 'Per-request price is required' }
  }

  return { ok: true, override }
}

/**
 * 生成表格里「定价」列的摘要文案 key 喵。
 *
 * 输入：该模型的定价覆盖项与分组级计费方式（都可能 undefined）。
 * 输出：一个 i18n key，调用方负责 t() 翻译。
 * 之所以只返回 key 而不返回拼好的句子，是为了让中英文都能自然表达喵。
 */
export function formatOverrideSummary(
  override: GroupPricingOverride | undefined,
  groupBillingMode: string | undefined
): string {
  // 分组级阶梯计费优先级最高，此时倍率对这个分组不生效喵。
  if (groupBillingMode === GROUP_BILLING_MODE_TIERED) {
    return 'Tiered expression'
  }
  const billingMode = override?.billing_mode ?? GROUP_BILLING_MODE_INHERIT
  if (billingMode === GROUP_BILLING_MODE_PER_CALL) {
    return 'Per-request'
  }
  if (billingMode === GROUP_BILLING_MODE_PER_TOKEN) {
    return 'Per-token'
  }
  // 喵~防御：覆盖项完全为空（例如只写了个空对象）时也要给出可读文案喵。
  return 'Inherit global'
}
