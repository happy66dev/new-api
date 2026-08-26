/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import type { VirtualModelFailureRule } from '../api'

// FailureRuleDraft 保存单条失败规则的受控编辑状态，字符串字段避免输入中间态被过早截断喵。
export type FailureRuleDraft = {
  bodyRegex: string
  errorClass: string
  freezeSeconds: string
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

// toFailureRuleDraft 将读取响应映射为可编辑草稿，并为缺失字段提供明确默认值喵。
export function toFailureRuleDraft(rule: VirtualModelFailureRule): FailureRuleDraft {
  return {
    bodyRegex: rule.body_regex ?? '',
    errorClass: rule.error_class ?? '',
    freezeSeconds: String(rule.freeze_seconds ?? 0),
    httpStatus: String(rule.http_status ?? 0),
    id: rule.id,
    action: rule.action ?? 'next',
  }
}

// createFailureRuleDraft 创建默认失败规则草稿，默认动作与未命中规则时的候选切换语义对齐喵。
export function createFailureRuleDraft(): FailureRuleDraft {
  return {
    bodyRegex: '',
    errorClass: '',
    freezeSeconds: '0',
    httpStatus: '0',
    action: 'next',
  }
}

// validateFailureRuleDraft 将用户输入转换为 API 结构，并尽早阻止越界和不完整数值喵。
export function validateFailureRuleDraft(
  rule: FailureRuleDraft,
  index: number,
  t: (key: string, options?: Record<string, unknown>) => string
): VirtualModelFailureRule {
  // 将状态码文本转换为数值，零表示不限制状态码喵。
  const httpStatus = Number(rule.httpStatus)
  // 将冻结秒数文本转换为数值，零表示不追加固定冻结时间喵。
  const freezeSeconds = Number(rule.freezeSeconds)
  // 喵~防御：状态码必须是 0 到 599 的整数，避免后端请求因输入中间态而失败喵。
  if (!Number.isInteger(httpStatus) || httpStatus < 0 || httpStatus > MAXIMUM_HTTP_STATUS) {
    throw new Error(t('Failure rule {{index}} HTTP status must be between 0 and 599', { index: index + 1 }))
  }
  // 喵~防御：冻结时长必须处于零到一天，防止意外长期冻结候选喵。
  if (!Number.isInteger(freezeSeconds) || freezeSeconds < 0 || freezeSeconds > MAXIMUM_FREEZE_SECONDS) {
    throw new Error(t('Failure rule {{index}} freeze duration must be between 0 and 86400 seconds', { index: index + 1 }))
  }
  // 返回后端期待的下划线字段，空条件表示该维度不限制匹配喵。
  return {
    id: rule.id,
    http_status: httpStatus,
    error_class: rule.errorClass.trim(),
    body_regex: rule.bodyRegex.trim(),
    action: rule.action,
    freeze_seconds: freezeSeconds,
  }
}
