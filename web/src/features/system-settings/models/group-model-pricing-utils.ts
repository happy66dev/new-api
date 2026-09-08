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
import { formatPricingNumber } from './pricing-format'

/**
 * 分组定制定价的前端数据结构与「草稿 ↔ 配置」互转工具喵。
 *
 * 这里的字段名必须和后端 setting/ratio_setting/group_model_pricing.go 里
 * GroupModelPriceOverride 的 json tag 完全一致，否则保存后后端读不到喵。
 *
 * 最重要的一条约定喵：**字段缺失（undefined）表示「继承全局配置」**，
 * 显式填 0 表示「这个分组真的免费」。所以草稿里的空字符串必须转成「不写这个键」，
 * 绝不能转成 0，不然会把继承悄悄改成免费喵。
 *
 * 编辑面板里管理员直接填「美元价格」，而配置里存的是后端要的「倍率」，
 * 所以这里负责价格 ⇄ 倍率的换算，口径与全局编辑器（model-pricing-core）完全一致喵：
 *
 *   - model_ratio = 输入价 ÷ 2（因为 $1 = 500000 额度，倍率 × 2 才是 $/1M token）
 *   - completion_ratio = 输出价 ÷ 基准输入价
 *   - cache_ratio / create_cache_ratio / image_ratio / audio_ratio 同理除以基准输入价
 *   - audio_completion_ratio = 音频输出价 ÷ 基准音频输入价
 *
 * 「基准输入价」优先取本组填的输入价，没填就回落到全局输入价（全局 ModelRatio × 2），
 * 这样「只定制输出价、继承全局输入价」也能换算成倍率喵。
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
 * 把 billing_mode（字符串字段）排除掉，换算循环里才能安全地按 key 赋数字喵。
 */
export type GroupPricingNumericKey = Exclude<
  keyof GroupPricingOverride,
  'billing_mode'
>

/**
 * 某个模型的全局定价基准，供「本组没填输入价/音频输入价」时回落换算用喵。
 * 都是美元/百万 token 的价格，预乘了「倍率 × 2」这一层换算喵。
 */
export type GlobalPricingBases = {
  /** 全局输入价 = 全局 ModelRatio × 2 喵。 */
  inputPrice: number
  /** 全局音频输入价 = 全局输入价 × 全局 AudioRatio 喵。 */
  audioInputPrice: number
}

/** 模型名 -> 该模型的全局定价基准，ratio-settings-card 一次性算好传进来喵。 */
export type GlobalPricingBasesMap = Record<string, GlobalPricingBases>

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

/** 编辑面板里的草稿：所有价格都用字符串存，空串代表「留空 = 继承全局」喵。 */
export type GroupPricingDraft = {
  /** 模型名，例如 deepseek-chat 喵。 */
  modelName: string
  /** 当前选中的计费方式，取值见 GROUP_BILLING_MODE_* 常量喵。 */
  billingMode: string
  /** 按次价格输入框的原始文本，单位美元/次，仅按次计费模式有意义喵。 */
  modelPrice: string
  /** 输入价输入框的原始文本，单位美元/百万 token 喵。 */
  inputPrice: string
  /** 输出价输入框的原始文本，单位美元/百万 token 喵。 */
  outputPrice: string
  /** 缓存读取价输入框的原始文本，单位美元/百万 token 喵。 */
  cachePrice: string
  /** 缓存写入价输入框的原始文本，单位美元/百万 token 喵。 */
  createCachePrice: string
  /** 图片价输入框的原始文本，单位美元/百万 token 喵。 */
  imagePrice: string
  /** 音频输入价输入框的原始文本，单位美元/百万 token 喵。 */
  audioPrice: string
  /** 音频输出价输入框的原始文本，单位美元/百万 token 喵。 */
  audioOutputPrice: string
  /** 阶梯计费表达式的原始文本，仅计费方式为 tiered_expr 时有意义喵。 */
  billingExpr: string
}

/** 价格输入框 ↔ 覆盖项倍率字段 ↔ 界面标签 的对应关系，编辑面板直接遍历它渲染价格输入框喵。
 * 标签文案与全局编辑器（model-pricing-core 的 laneConfigs）保持一致，直接复用同一批 i18n key 喵。
 * 换算基准分两种喵：
 *   - 'input'：倍率 = 该价格 ÷ 基准输入价（输出/缓存/缓存写入/图片/音频输入都归此类）；
 *   - 'audioInput'：倍率 = 音频输出价 ÷ 基准音频输入价。
 */
export const GROUP_PRICING_PRICE_FIELDS: ReadonlyArray<{
  /** 草稿里的价格字段名喵。 */
  field: keyof GroupPricingDraft
  /** 覆盖项 JSON 里的倍率字段名，只可能是数值字段喵。 */
  ratioKey: GroupPricingNumericKey
  /** 该价格换算成倍率时的基准类别喵。 */
  base: 'input' | 'audioInput'
  /** i18n 文案 key，与全局编辑器的车道标题一致喵。 */
  labelKey: string
}> = [
  {
    field: 'outputPrice',
    ratioKey: 'completion_ratio',
    base: 'input',
    labelKey: 'Completion price',
  },
  {
    field: 'cachePrice',
    ratioKey: 'cache_ratio',
    base: 'input',
    labelKey: 'Cache read price',
  },
  {
    field: 'createCachePrice',
    ratioKey: 'create_cache_ratio',
    base: 'input',
    labelKey: 'Cache write price',
  },
  {
    field: 'imagePrice',
    ratioKey: 'image_ratio',
    base: 'input',
    labelKey: 'Image input price',
  },
  {
    field: 'audioPrice',
    ratioKey: 'audio_ratio',
    base: 'input',
    labelKey: 'Audio input price',
  },
  {
    field: 'audioOutputPrice',
    ratioKey: 'audio_completion_ratio',
    base: 'audioInput',
    labelKey: 'Audio output price',
  },
]

/** 全局基准缺失时的兜底：输入价与音频输入价都是 0，表示「全局也没定价」喵。 */
const EMPTY_GLOBAL_BASES: GlobalPricingBases = {
  inputPrice: 0,
  audioInputPrice: 0,
}

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

/** 把已配置的倍率换算回可编辑的价格文本：undefined/null/非有限数一律转空串喵。 */
function ratioToPriceText(value: number | undefined | null): string {
  // 喵~防御：未配置的字段必须显示成空，不能显示 0，否则用户会误以为这个分组免费喵。
  if (value === undefined || value === null) {
    return ''
  }
  // formatPricingNumber 内部对非有限数也返回空串，NaN/Infinity 不会漏进输入框喵。
  return formatPricingNumber(value)
}

/** 把价格数值四舍五入成可写的倍率数值，避免「0.27 / 0.5 = 0.5400000000000001」这种浮点噪音喵。 */
function priceToRatioNumber(value: number): number {
  const formatted = formatPricingNumber(value)
  // formatPricingNumber 对非有限数返回空串，此时按 0 处理，调用方早该拦掉了喵。
  return formatted === '' ? 0 : Number(formatted)
}

/**
 * 把一条已有配置还原成编辑面板的草稿喵。
 *
 * 输入：模型名、该模型在三份配置里各自的值（都可能是 undefined，表示那份没配），
 * 以及该模型的全局定价基准（用于「本组没填输入价时回落到全局价换算」喵）。
 * 输出：填好的草稿。
 * 边界：分组级阶梯计费优先——只要分组计费方式是 tiered_expr，面板就切到表达式模式，
 * 因为此时定价覆盖里的倍率对这个分组已经不生效了喵。
 */
export function buildDraftFromOverride(
  modelName: string,
  override: GroupPricingOverride | undefined,
  groupBillingMode: string | undefined,
  groupBillingExpr: string | undefined,
  globalBases: GlobalPricingBases | undefined
): GroupPricingDraft {
  // 分组级声明了阶梯计费时直接进表达式模式，价格框留空避免误导喵。
  if (groupBillingMode === GROUP_BILLING_MODE_TIERED) {
    return {
      modelName,
      billingMode: GROUP_BILLING_MODE_TIERED,
      modelPrice: '',
      inputPrice: '',
      outputPrice: '',
      cachePrice: '',
      createCachePrice: '',
      imagePrice: '',
      audioPrice: '',
      audioOutputPrice: '',
      billingExpr: groupBillingExpr ?? '',
    }
  }
  const global = globalBases ?? EMPTY_GLOBAL_BASES

  // 本组输入价 = 组内 model_ratio × 2；基准输入价优先用组内值，没配再回落全局喵。
  const inputPrice =
    override?.model_ratio != null
      ? ratioToPriceText(override.model_ratio * 2)
      : ''
  const effectiveInputPrice =
    inputPrice !== '' && Number(inputPrice) > 0
      ? Number(inputPrice)
      : global.inputPrice

  // 音频输入价同样：组内 audio_ratio × 基准输入价；基准音频输入价优先组内、回落全局喵。
  const audioPrice =
    override?.audio_ratio != null && effectiveInputPrice > 0
      ? ratioToPriceText(override.audio_ratio * effectiveInputPrice)
      : ''
  const effectiveAudioInputPrice =
    audioPrice !== '' && Number(audioPrice) > 0
      ? Number(audioPrice)
      : global.audioInputPrice

  // 喵~防御：基准为 0（本组与全局都没定价）时倍率换算不出价格，一律留空展示喵。
  const laneToPrice = (ratio: number | undefined, base: number): string => {
    if (ratio == null || base <= 0) return ''
    return ratioToPriceText(ratio * base)
  }

  return {
    modelName,
    // 喵~防御：覆盖项缺失或没写 billing_mode 时按「继承全局」显示喵。
    billingMode: override?.billing_mode ?? GROUP_BILLING_MODE_INHERIT,
    modelPrice: ratioToPriceText(override?.model_price),
    inputPrice,
    outputPrice: laneToPrice(override?.completion_ratio, effectiveInputPrice),
    cachePrice: laneToPrice(override?.cache_ratio, effectiveInputPrice),
    createCachePrice: laneToPrice(
      override?.create_cache_ratio,
      effectiveInputPrice
    ),
    imagePrice: laneToPrice(override?.image_ratio, effectiveInputPrice),
    audioPrice,
    audioOutputPrice: laneToPrice(
      override?.audio_completion_ratio,
      effectiveAudioInputPrice
    ),
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
 *  1. 阶梯计费模式只需要表达式，价格一律不写，先单独校验表达式非空；
 *  2. 按次计费只需要按次价，且必须显式填写（前端看不到全局按次价，避免静默变 0 元/次）；
 *  3. 按量 / 继承模式把价格逐个换算成倍率：输入价 ÷ 2 得到 model_ratio，
 *     其余价格 ÷ 基准（组内输入价，没配回落全局）得到对应倍率；
 *  4. 声明按次计费却没填按次价时拦下来——后端也会拦，但前端先提示体验更好喵。
 *
 * 输入：草稿与该模型的全局定价基准。输出：成功时是覆盖项，失败时是错误文案 key。
 * 边界：负数、NaN、Infinity、非数字文本全部判为非法；基准缺失或为 0 时无法换算，
 * 一律拒绝保存并提示先填输入价，绝不写 0/Infinity 进计费配置喵。
 */
export function draftToOverride(
  draft: GroupPricingDraft,
  globalBases: GlobalPricingBases | undefined
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

  // 按次计费：只收按次价，价格倍率全部不写喵。
  if (draft.billingMode === GROUP_BILLING_MODE_PER_CALL) {
    const modelPrice = draft.modelPrice.trim()
    // 喵~防御：强制按次却没填单价时，是否合法取决于全局有没有配按次价，前端看不到，
    // 所以这里要求必须显式填写，避免静默变成 0 元/次喵。
    if (modelPrice === '') {
      return { ok: false, messageKey: 'Per-request price is required' }
    }
    const parsedPrice = Number(modelPrice)
    if (!Number.isFinite(parsedPrice)) {
      return { ok: false, messageKey: 'Pricing values must be finite numbers' }
    }
    if (parsedPrice < 0) {
      return { ok: false, messageKey: 'Pricing values cannot be negative' }
    }
    override.model_price = parsedPrice
    return { ok: true, override }
  }

  // 按量 / 继承模式：价格 → 倍率喵。
  const global = globalBases ?? EMPTY_GLOBAL_BASES

  // 继承模式也可能只想改按次价（当全局是按次模型时）：面板会同时显示按次价框，
  // 这里把它一并写进 model_price，与改造前「继承模式下能填按次价」的行为保持一致喵。
  if (draft.billingMode === GROUP_BILLING_MODE_INHERIT) {
    const modelPriceText = draft.modelPrice.trim()
    if (modelPriceText !== '') {
      const parsedPrice = Number(modelPriceText)
      // 喵~防御：非数字或 NaN/Infinity 会一路污染额度计算，直接拒绝保存喵。
      if (!Number.isFinite(parsedPrice)) {
        return {
          ok: false,
          messageKey: 'Pricing values must be finite numbers',
        }
      }
      // 喵~防御：负价格会算出负额度（等于给用户返钱），绝对不允许喵。
      if (parsedPrice < 0) {
        return { ok: false, messageKey: 'Pricing values cannot be negative' }
      }
      override.model_price = parsedPrice
    }
  }

  // 先解析输入价：留空表示继承全局，此时基准输入价回落全局值喵。
  const inputPriceText = draft.inputPrice.trim()
  let inputPrice: number | null = null
  if (inputPriceText !== '') {
    inputPrice = Number(inputPriceText)
    // 喵~防御：非数字或 NaN/Infinity 会一路污染额度计算，直接拒绝保存喵。
    if (!Number.isFinite(inputPrice)) {
      return { ok: false, messageKey: 'Pricing values must be finite numbers' }
    }
    // 喵~防御：负价格会算出负额度（等于给用户返钱），绝对不允许喵。
    if (inputPrice < 0) {
      return { ok: false, messageKey: 'Pricing values cannot be negative' }
    }
    override.model_ratio = priceToRatioNumber(inputPrice / 2)
  }
  const effectiveInputPrice = inputPrice ?? global.inputPrice

  // 同理解析音频输入价，作为音频输出价的换算基准喵。
  const audioPriceText = draft.audioPrice.trim()
  let audioPrice: number | null = null
  if (audioPriceText !== '') {
    audioPrice = Number(audioPriceText)
    if (!Number.isFinite(audioPrice)) {
      return { ok: false, messageKey: 'Pricing values must be finite numbers' }
    }
    if (audioPrice < 0) {
      return { ok: false, messageKey: 'Pricing values cannot be negative' }
    }
  }
  const effectiveAudioInputPrice =
    audioPrice ?? (effectiveInputPrice > 0 ? global.audioInputPrice : 0)

  for (const priceField of GROUP_PRICING_PRICE_FIELDS) {
    const rawText = draft[priceField.field].trim()
    // 留空表示继承全局，这个键就不写进 JSON 喵。
    if (rawText === '') {
      continue
    }
    const parsedValue = Number(rawText)
    // 喵~防御：非数字或 NaN/Infinity 直接拒绝，绝不写脏值进计费配置喵。
    if (!Number.isFinite(parsedValue)) {
      return { ok: false, messageKey: 'Pricing values must be finite numbers' }
    }
    // 喵~防御：负价格会算出负额度（等于给用户返钱），绝对不允许喵。
    if (parsedValue < 0) {
      return { ok: false, messageKey: 'Pricing values cannot be negative' }
    }
    const base =
      priceField.base === 'input'
        ? effectiveInputPrice
        : effectiveAudioInputPrice
    // 喵~防御：换算基准为 0 时倍率无从算起（会变成 Infinity），要求先填输入价喵。
    if (base <= 0) {
      return {
        ok: false,
        messageKey: 'Input price is required to convert per-token prices.',
      }
    }
    override[priceField.ratioKey] = priceToRatioNumber(parsedValue / base)
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
