/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import type { VirtualModelFailureRule } from '../api'

// BodyRegexMode 描述响应体正则的编辑模式喵。
// none 不匹配响应体；preset 使用内置预设；simple 用普通文本做包含匹配；custom 直接写正则喵。
export type BodyRegexMode = 'none' | 'preset' | 'simple' | 'custom'

// FreezeUnit 描述响应体冻结时间字段值的单位喵。
// seconds 直接按秒；minutes 按分钟乘以 60；mixed 支持 "1m30s" 复合格式；auto 自动扫描自然语言时间喵。
export type FreezeUnit = 'seconds' | 'minutes' | 'mixed' | 'auto'

// FREEZE_UNITS 提供响应体冻结单位下拉选项，labelKey 供 i18n 翻译喵。
export const FREEZE_UNITS: { value: FreezeUnit; labelKey: string }[] = [
  { value: 'auto', labelKey: 'Freeze unit: auto' },
  { value: 'seconds', labelKey: 'Freeze unit: seconds' },
  { value: 'minutes', labelKey: 'Freeze unit: minutes' },
  { value: 'mixed', labelKey: 'Freeze unit: mixed (e.g. 1m30s)' },
]

// FailureRuleDraft 保存单条失败规则的受控编辑状态，字符串字段避免输入中间态被过早截断喵。
export type FailureRuleDraft = {
  bodyRegex: string
  // bodyRegexMode 记录响应体匹配的编辑模式，决定保存时如何生成 body_regex 喵。
  bodyRegexMode: BodyRegexMode
  // bodyRegexPreset 记录选中的预设键，仅在 preset 模式生效喵。
  bodyRegexPreset: string
  // bodyRegexSimple 记录简易模式输入的普通文本，保存时转义为正则喵。
  bodyRegexSimple: string
  errorClass: string
  freezeSeconds: string
  // freezeField 是响应体中的冻结时间字段名，非空时启用从响应体解析冻结时间喵。
  freezeField: string
  // freezeUnit 标记响应体字段冻结时间的单位，仅在 freezeField 非空时生效喵。
  freezeUnit: FreezeUnit
  httpStatus: string
  id?: number
  action: VirtualModelFailureRule['action']
}

// MAXIMUM_FAILURE_RULES 限制单个候选或模型的失败规则数，必须与控制面安全上限一致喵。
export const MAXIMUM_FAILURE_RULES = 32
// MAXIMUM_HTTP_STATUS 是合法 HTTP 状态码的控制面最大值喵。
export const MAXIMUM_HTTP_STATUS = 599
// MAXIMUM_FREEZE_SECONDS 限制单条规则最多冻结一个自然日喵。
export const MAXIMUM_FREEZE_SECONDS = 24 * 60 * 60
// COMMON_HTTP_STATUSES 提供常见失败状态码预设，点击即可填入喵。
export const COMMON_HTTP_STATUSES = [429, 500, 502, 503, 504, 524]
// COMMON_HTTP_STATUS_RANGES 提供常见状态码范围预设，文本与 parseHttpStatusText 兼容喵。
export const COMMON_HTTP_STATUS_RANGES = ['500~524']
// ERROR_CLASS_OPTIONS 列出后端可稳定分类的错误分类选项，供下拉选择喵。
export const ERROR_CLASS_OPTIONS = [
  'rate_limited',
  'timeout',
  'upstream_server_error',
  'upstream_client_error',
  'network_error',
  'upstream_error',
] as const

// BODY_REGEX_PRESETS 提供常用上游错误响应体的预设正则喵。
// pattern 是实际写入的正则；labelKey 是供 i18n 翻译的预设名称键；descriptionKey 描述该预设匹配的文字特征喵。
export const BODY_REGEX_PRESETS: Record<string, { labelKey: string; descriptionKey: string; pattern: string }> = {
  rate_limited: { labelKey: 'Preset: rate limited', descriptionKey: 'Preset description: rate limited', pattern: 'rate.{0,12}limit' },
  overloaded: { labelKey: 'Preset: overloaded or capacity', descriptionKey: 'Preset description: overloaded or capacity', pattern: '(capacity|overloaded)' },
  insufficient_quota: { labelKey: 'Preset: insufficient quota', descriptionKey: 'Preset description: insufficient quota', pattern: 'insufficient.{0,20}quota' },
  context_length: { labelKey: 'Preset: context length exceeded', descriptionKey: 'Preset description: context length exceeded', pattern: 'context.{0,12}length' },
  temporarily_unavailable: { labelKey: 'Preset: temporarily unavailable', descriptionKey: 'Preset description: temporarily unavailable', pattern: 'temporarily.{0,20}(unavailable|down)' },
}

// toFailureRuleDraft 将读取响应映射为可编辑草稿，并为缺失字段提供明确默认值喵。
// 已存在的响应体正则若匹配预设则进入 preset 模式，否则作为自定义正则展示喵。
// 状态码范围以 "min~max" 文本呈现，单值直接显示，零表示任意喵。
export function toFailureRuleDraft(rule: VirtualModelFailureRule): FailureRuleDraft {
  const bodyRegex = rule.body_regex ?? ''
  // 默认不匹配响应体，待根据已有正则内容推断编辑模式喵。
  let bodyRegexMode: BodyRegexMode = 'none'
  let bodyRegexPreset = ''
  if (bodyRegex !== '') {
    // 查找已有正则是哪个预设的固定值，命中则标记为预设模式喵。
    const presetEntry = Object.entries(BODY_REGEX_PRESETS).find(([, preset]) => preset.pattern === bodyRegex)
    if (presetEntry) {
      bodyRegexMode = 'preset'
      bodyRegexPreset = presetEntry[0]
    } else {
      // 非预设值按自定义正则处理，避免丢失用户已有配置喵。
      bodyRegexMode = 'custom'
    }
  }
  // 存储中的范围上界非零时以 "min~max" 文本呈现，否则显示单值喵。
  const httpStatusValue = rule.http_status ?? 0
  const httpStatusMaxValue = rule.http_status_max ?? 0
  const httpStatusText = httpStatusMaxValue > 0 ? `${httpStatusValue}~${httpStatusMaxValue}` : String(httpStatusValue)
  // 冻结单位必须是合法枚举，空串或旧数据非法值时回退到秒，避免下拉显示错位喵。
  const freezeUnit = FREEZE_UNITS.find((unit) => unit.value === rule.freeze_unit)?.value ?? 'seconds'
  return {
    bodyRegex,
    bodyRegexMode,
    bodyRegexPreset,
    bodyRegexSimple: '',
    errorClass: rule.error_class ?? '',
    freezeSeconds: String(rule.freeze_seconds ?? 0),
    freezeField: rule.freeze_field ?? '',
    freezeUnit,
    httpStatus: httpStatusText,
    id: rule.id,
    action: rule.action ?? 'next',
  }
}

// createFailureRuleDraft 创建默认失败规则草稿，默认动作与未命中规则时的候选切换语义对齐喵。
export function createFailureRuleDraft(): FailureRuleDraft {
  return {
    bodyRegex: '',
    bodyRegexMode: 'none',
    bodyRegexPreset: '',
    bodyRegexSimple: '',
    errorClass: '',
    freezeSeconds: '0',
    freezeField: '',
    freezeUnit: 'seconds',
    httpStatus: '0',
    action: 'next',
  }
}

// parseHttpStatusText 把状态码文本解析为匹配下界与可选上界喵。
// 支持 "0"（任意）、"429"（单值）与 "500~524" 或 "500-524"（范围）喵。
export function parseHttpStatusText(text: string): { min: number; max: number } | null {
  const trimmed = text.trim()
  // 空串与零都表示不限制状态码喵。
  if (trimmed === '' || trimmed === '0') return { min: 0, max: 0 }
  // 范围匹配：两端均为非负整数，分隔符支持波浪号与连字符喵。
  const rangeMatch = /^(\d+)\s*[~-]\s*(\d+)$/.exec(trimmed)
  if (rangeMatch) {
    return { min: Number(rangeMatch[1]), max: Number(rangeMatch[2]) }
  }
  // 单值匹配：必须为非负整数，负号、小数与空段都视为畸形输入喵。
  const singleMatch = /^\d+$/.exec(trimmed)
  if (singleMatch) {
    return { min: Number(singleMatch[0]), max: 0 }
  }
  // 喵~防御：无法识别的文本返回空，交由校验层拒绝喵。
  return null
}

// escapeRegex 将普通文本中的正则元字符转义，使输入文本按字面包含语义匹配喵。
export function escapeRegex(text: string): string {
  // 喵~防御：空文本直接返回，避免无意义转义喵。
  if (text === '') return ''
  // 转义正则全部元字符，使普通文本按字面量匹配喵。
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

// resolveBodyRegex 根据编辑模式生成最终写入的响应体正则喵。
export function resolveBodyRegex(draft: FailureRuleDraft): string {
  // 喵~防御：未选择模式或预设键不存在时回退为空，视为不匹配响应体喵。
  switch (draft.bodyRegexMode) {
    case 'preset':
      return BODY_REGEX_PRESETS[draft.bodyRegexPreset]?.pattern ?? ''
    case 'simple':
      // 简易模式把用户普通文本转义为"包含该文本"的正则喵。
      return escapeRegex(draft.bodyRegexSimple.trim())
    case 'custom':
      return draft.bodyRegex.trim()
    default:
      return ''
  }
}

// validateFailureRuleDraft 将用户输入转换为 API 结构，并尽早阻止越界和不完整数值喵。
export function validateFailureRuleDraft(
  rule: FailureRuleDraft,
  index: number,
  t: (key: string, options?: Record<string, unknown>) => string
): VirtualModelFailureRule {
  // 将状态码文本解析为下界与可选上界，零表示不限制状态码喵。
  const parsedStatus = parseHttpStatusText(rule.httpStatus)
  // 喵~防御：状态码必须可解析且落在 0 到 599 之间，范围上界非零时不得小于下界喵。
  if (
    !parsedStatus ||
    parsedStatus.min < 0 ||
    parsedStatus.min > MAXIMUM_HTTP_STATUS ||
    parsedStatus.max < 0 ||
    parsedStatus.max > MAXIMUM_HTTP_STATUS ||
    (parsedStatus.max > 0 && parsedStatus.max < parsedStatus.min)
  ) {
    throw new Error(t('Failure rule {{index}} HTTP status must be 0, a single code, or a range like 500~524', { index: index + 1 }))
  }
  // 将冻结秒数文本转换为数值，零表示不追加固定冻结时间喵。
  const freezeSeconds = Number(rule.freezeSeconds)
  // 喵~防御：冻结时长必须处于零到一天，防止意外长期冻结候选喵。
  if (!Number.isInteger(freezeSeconds) || freezeSeconds < 0 || freezeSeconds > MAXIMUM_FREEZE_SECONDS) {
    throw new Error(t('Failure rule {{index}} freeze duration must be between 0 and 86400 seconds', { index: index + 1 }))
  }
  // auto 单位自动扫描响应体全文，字段名无意义，强制置空避免残留旧配置喵。
  const freezeField = rule.freezeUnit === 'auto' ? '' : rule.freezeField.trim()
  // 喵~防御：非 auto 模式下响应体字段名过长会超过数据库列宽，必须拒绝保存喵。
  if (rule.freezeUnit !== 'auto' && freezeField.length > 64) {
    throw new Error(t('Failure rule {{index}} freeze field is too long', { index: index + 1 }))
  }
  // 根据编辑模式生成最终响应体正则，简易与自定义输入均在此收敛喵。
  const bodyRegex = resolveBodyRegex(rule)
  // 返回后端期待的下划线字段，空条件表示该维度不限制匹配喵。
  return {
    id: rule.id,
    http_status: parsedStatus.min,
    http_status_max: parsedStatus.max,
    error_class: rule.errorClass.trim(),
    body_regex: bodyRegex,
    action: rule.action,
    freeze_seconds: freezeSeconds,
    freeze_field: freezeField,
    freeze_unit: rule.freezeUnit,
  }
}
