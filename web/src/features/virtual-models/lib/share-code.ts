/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
)*/

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
// 分享码相关的错误（不存在、已撤销、已过期）都属于用户可预期输入，需要内联展示而不是依赖全局弹窗喵。
export function extractShareCodeErrorMessage(
  error: unknown,
  fallbackMessage: string
): string {
  // 喵~防御：异常结构不可控，逐层判断而不是直接断言属性存在，避免二次抛错喵。
  if (typeof error !== 'object' || error === null) {
    return fallbackMessage
  }
  const response = (error as { response?: { data?: { message?: unknown } } }).response
  const serverMessage = response?.data?.message
  if (typeof serverMessage === 'string' && serverMessage.trim() !== '') {
    return serverMessage
  }
  if (error instanceof Error && error.message.trim() !== '') {
    return error.message
  }
  return fallbackMessage
}

// isShareCodeRevoked 判断分享码是否已失效（撤销 / 过期 / 次数用尽）喵。
// 这三类都返回 410，前端据此提示"换一枚码"而不是"码写错了"喵。
export function isShareCodeGone(error: unknown): boolean {
  if (typeof error !== 'object' || error === null) {
    return false
  }
  const status = (error as { response?: { status?: unknown } }).response?.status
  return status === 410
}

// ShareCodeStatus 是分享码当前的可用状态喵。
export type ShareCodeStatus = 'active' | 'revoked' | 'expired' | 'exhausted'

// shareCodeStatusTextKeys 把分享码状态映射成英文 i18n 键喵。
export const shareCodeStatusTextKeys: Record<ShareCodeStatus, string> = {
  active: 'Active',
  revoked: 'Revoked',
  expired: 'Expired',
  exhausted: 'Import limit reached',
}

// resolveShareCodeStatus 按后端约定的优先级判定分享码状态喵。
// 判定顺序与后端 loadVirtualModelSharePayload 保持一致：撤销 > 过期 > 次数用尽 > 可用喵。
export function resolveShareCodeStatus(
  shareCode: {
    revoked_at: number
    expires_at: number
    import_count: number
    max_imports: number
  },
  nowSeconds: number
): ShareCodeStatus {
  if (shareCode.revoked_at !== 0) {
    return 'revoked'
  }
  // 喵~防御：expires_at 为零表示永不过期，不能把它当成"已过期"喵。
  if (shareCode.expires_at !== 0 && shareCode.expires_at <= nowSeconds) {
    return 'expired'
  }
  // 喵~防御：max_imports 为零表示不限次数，同样不能判成"已用尽"喵。
  if (shareCode.max_imports > 0 && shareCode.import_count >= shareCode.max_imports) {
    return 'exhausted'
  }
  return 'active'
}
