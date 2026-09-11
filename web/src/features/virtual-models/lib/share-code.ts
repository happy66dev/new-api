/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
)*/

// 从全局错误码表取本地化文案，分享弹窗与导入弹窗共用同一套错误表达喵。
import { getServerErrorMessageKey } from '@/lib/server-error-message'

// shareSkipReasonTextKeys 把后端返回的稳定跳过原因码映射成英文 i18n 键喵。
// 后端同时会带一句中文兜底说明；只有在原因码未知（例如后端升级新增了原因）时才回退到它喵。
export const shareSkipReasonTextKeys: Record<string, string> = {
  group_not_configured: 'This candidate has no group configured.',
  auto_group_not_supported:
    'This candidate uses the automatic group, which virtual models do not support.',
  group_not_accessible: 'Your account cannot access this group.',
  model_not_available_in_group:
    'This group currently has no available channel for this model.',
  base_url_rejected:
    'This candidate upstream address was rejected by this instance policy.',
}

// shareWarningTextKeys 把后端返回的提示码映射成英文 i18n 键喵。
export const shareWarningTextKeys: Record<string, string> = {
  no_directly_callable_candidate:
    'No candidate can be called right away. Fill in the API Key for custom candidates before enabling the model.',
  all_candidates_skipped:
    'Every candidate was skipped. The imported plan is empty and cannot be used.',
}

// describeShareSkipReason 返回某个跳过原因的用户可读说明喵。
// 参数 translate 由调用方从 useTranslation() 取得，便于测试注入假实现喵。
export function describeShareSkipReason(
  reason: string,
  fallbackMessage: string,
  translate: (key: string) => string
): string {
  const textKey = shareSkipReasonTextKeys[reason]
  // 喵~防御：未知原因码退回后端兜底说明，再退回原因码本身，避免界面出现空白喵。
  if (!textKey) {
    return fallbackMessage || reason
  }
  return translate(textKey)
}

// describeShareWarning 返回某个提示码的用户可读说明喵。
export function describeShareWarning(
  warning: string,
  translate: (key: string) => string
): string {
  const textKey = shareWarningTextKeys[warning]
  // 喵~防御：未知提示码直接展示原始码，保证信息不丢而不是静默吞掉喵。
  if (!textKey) {
    return warning
  }
  return translate(textKey)
}

// extractShareCodeErrorMessage 从接口异常里取出后端消息喵。
// 分享码相关的错误（不存在、已删除、已过期）都属于用户可预期输入，需要内联展示而不是依赖全局弹窗喵。
export function extractShareCodeErrorMessage(
  error: unknown,
  fallbackMessage: string
): string {
  // 喵~防御：异常结构不可控，逐层判断而不是直接断言属性存在，避免二次抛错喵。
  if (typeof error !== 'object' || error === null) {
    return fallbackMessage
  }
  const response = (error as { response?: { data?: { message?: unknown } } })
    .response
  const serverMessage = response?.data?.message
  if (typeof serverMessage === 'string' && serverMessage.trim() !== '') {
    return serverMessage
  }
  if (error instanceof Error && error.message.trim() !== '') {
    return error.message
  }
  return fallbackMessage
}

// describeShareCodeError 把接口异常翻译成可以直接展示给用户的文案喵。
// 优先级：后端稳定错误码 > 后端返回的说明 > 调用方兜底文案喵。
export function describeShareCodeError(
  error: unknown,
  fallbackMessage: string,
  translate: (key: string) => string
): string {
  // 喵~防御：后端错误码只有登记在 server-error-message 里才认得出来，未登记的一律走原文兜底喵。
  const textKey = getServerErrorMessageKey(error)
  if (textKey !== null && textKey !== '') {
    return translate(textKey)
  }
  return extractShareCodeErrorMessage(error, fallbackMessage)
}

// isShareCodeGone 判断分享码是否"曾经有效但现在已经不能用了"（已过期 / 次数用尽）喵。
// 后端对这两类返回 410，前端据此提示"换一枚码"而不是"码写错了"喵。
// 被删除的分享码走的是 404（表里已经没有这一行了），与"从来没存在过"共用同一个响应喵。
export function isShareCodeGone(error: unknown): boolean {
  if (typeof error !== 'object' || error === null) {
    return false
  }
  const status = (error as { response?: { status?: unknown } }).response?.status
  return status === 410
}

// ShareCodeStatus 是分享码当前的可用状态喵。
// 已删除的分享码根本不会出现在列表里，所以这里没有"已撤销"这种状态喵。
export type ShareCodeStatus = 'active' | 'expired' | 'exhausted'

// shareCodeStatusTextKeys 把分享码状态映射成英文 i18n 键喵。
export const shareCodeStatusTextKeys: Record<ShareCodeStatus, string> = {
  active: 'Active',
  expired: 'Expired',
  exhausted: 'Import limit reached',
}

// resolveShareCodeStatus 按后端约定的优先级判定分享码状态喵。
// 判定顺序与后端 loadVirtualModelSharePayload 保持一致：过期 > 次数用尽 > 可用喵。
export function resolveShareCodeStatus(
  shareCode: {
    expires_at: number
    import_count: number
    max_imports: number
  },
  nowSeconds: number
): ShareCodeStatus {
  // 喵~防御：expires_at 为零表示永不过期，不能把它当成"已过期"喵。
  if (shareCode.expires_at !== 0 && shareCode.expires_at <= nowSeconds) {
    return 'expired'
  }
  // 喵~防御：max_imports 为零表示不限次数，同样不能判成"已用尽"喵。
  if (
    shareCode.max_imports > 0 &&
    shareCode.import_count >= shareCode.max_imports
  ) {
    return 'exhausted'
  }
  return 'active'
}

// shareCodeMaxModelNameLength 与后端 NormalizeVirtualModelName 的 96 字符上限保持一致喵。
const shareCodeMaxModelNameLength = 96

// shareCodeModelNamePattern 与后端 virtualModelNamePattern 口径一致：只允许 ASCII 字母、数字、短横线与下划线喵。
const shareCodeModelNamePattern = /[^A-Za-z0-9_-]/g

// normalizeShareCodeSuggestedName 把快照显示名推导成一个合法的模型标识建议值喵。
// 只用于预填输入框，最终合法性仍然由后端 NormalizeVirtualModelName 决定喵。
export function normalizeShareCodeSuggestedName(displayName: string): string {
  const trimmedDisplayName = displayName.trim()
  // 喵~防御：空显示名推导不出建议值，返回空串让输入框留空由用户自己填喵。
  if (trimmedDisplayName === '') {
    return ''
  }
  // 先剥掉 virtual/ 前缀，与后端"允许用户连前缀一起填"的口径保持一致喵。
  const withoutPrefix = trimmedDisplayName.startsWith('virtual/')
    ? trimmedDisplayName.slice('virtual/'.length)
    : trimmedDisplayName
  // 非法字符统一换成短横线，避免纯中文显示名被清空后一个字符都不剩喵。
  const sanitizedName = withoutPrefix
    .replaceAll(shareCodeModelNamePattern, '-')
    .replaceAll(/^-+|-+$/g, '')
  // 喵~防御：清洗后什么都不剩就说明这个名字推导不出标识，交给用户自己填喵。
  if (sanitizedName === '') {
    return ''
  }
  // 喵~防御：先按后端上限截断，保证预填出来的建议值一定能通过后端校验喵。
  return sanitizedName.slice(0, shareCodeMaxModelNameLength).toLowerCase()
}

// countShareableCandidates 统计会被真正写进分享码快照的候选数量喵。
// 口径与后端 buildVirtualModelSharePayload 对齐：内部候选与全部自定义候选都会进快照，
// 其中引用型自定义候选（upstream_model_id 指向用户自己的上游条目）会被解析成 url 后照常导出喵。
export function countShareableCandidates(
  candidates: ReadonlyArray<{ source_type?: string }> | undefined
): number {
  // 喵~防御：候选列表还没加载出来时按 0 处理，宁可先禁用按钮，也不放行一次注定失败的请求喵。
  if (!candidates) {
    return 0
  }
  return candidates.filter(
    (candidate) =>
      candidate.source_type === 'internal' || candidate.source_type === 'custom'
  ).length
}
