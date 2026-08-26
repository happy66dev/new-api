/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { describe, expect, it } from 'vitest'

import type { VirtualModelFailureRule } from '../api'
import {
  MAXIMUM_FREEZE_SECONDS,
  MAXIMUM_HTTP_STATUS,
  createFailureRuleDraft,
  toFailureRuleDraft,
  validateFailureRuleDraft,
} from '../lib/failure-rules'

// identityTranslator 返回 key 本身，让校验函数只关心输入而不依赖真实 i18n 实例喵。
const identityTranslator = (key: string): string => key

// describe toFailureRuleDraft：服务端响应到可编辑草稿的映射喵。
describe('toFailureRuleDraft', () => {
  it('maps a complete rule keeping the stable id and action', () => {
    // 构造包含所有字段的完整失败规则喵。
    const rule: VirtualModelFailureRule = {
      id: 7,
      http_status: 429,
      error_class: 'rate_limited',
      body_regex: 'capacity',
      action: 'freeze',
      freeze_seconds: 30,
    }
    const draft = toFailureRuleDraft(rule)
    expect(draft).toEqual({
      bodyRegex: 'capacity',
      errorClass: 'rate_limited',
      freezeSeconds: '30',
      httpStatus: '429',
      id: 7,
      action: 'freeze',
    })
  })

  it('provides safe defaults for missing optional fields', () => {
    // 缺失的可选字段必须以零/空/next 兜底，避免编辑表单出现 undefined 喵。
    const draft = toFailureRuleDraft({ http_status: 0, error_class: '', body_regex: '', action: 'next', freeze_seconds: 0 })
    expect(draft).toEqual({
      bodyRegex: '',
      errorClass: '',
      freezeSeconds: '0',
      httpStatus: '0',
      id: undefined,
      action: 'next',
    })
  })
})

// describe createFailureRuleDraft：新规则的默认草稿喵。
describe('createFailureRuleDraft', () => {
  it('creates a default next-action draft with no match constraints', () => {
    // 默认规则不限制状态码、错误分类与响应体，动作与未命中时切换下一候选语义一致喵。
    const draft = createFailureRuleDraft()
    expect(draft).toEqual({
      bodyRegex: '',
      errorClass: '',
      freezeSeconds: '0',
      httpStatus: '0',
      action: 'next',
    })
  })
})

// describe validateFailureRuleDraft：用户输入到 API 载荷的转换与防御喵。
describe('validateFailureRuleDraft', () => {
  it('converts valid draft to API structure trimming text fields', () => {
    // 合法输入应保留 id、去空白并转为数值状态码与冻结秒数喵。
    const payload = validateFailureRuleDraft(
      { httpStatus: '503', errorClass: ' upstream_server_error ', bodyRegex: ' overloaded ', action: 'retry', freezeSeconds: '5', id: 3 },
      0,
      identityTranslator
    )
    expect(payload).toEqual({
      id: 3,
      http_status: 503,
      error_class: 'upstream_server_error',
      body_regex: 'overloaded',
      action: 'retry',
      freeze_seconds: 5,
    })
  })

  it('accepts boundary values zero and maximum', () => {
    // 零状态码表示不限制，599 与一天冻结秒数均为合法上界喵。
    const payload = validateFailureRuleDraft(
      { httpStatus: String(MAXIMUM_HTTP_STATUS), errorClass: '', bodyRegex: '', action: 'passthrough', freezeSeconds: String(MAXIMUM_FREEZE_SECONDS) },
      0,
      identityTranslator
    )
    expect(payload.http_status).toBe(MAXIMUM_HTTP_STATUS)
    expect(payload.freeze_seconds).toBe(MAXIMUM_FREEZE_SECONDS)
  })

  it('rejects negative http status', () => {
    // 喵~防御：负数状态码是畸形输入，必须抛错而不是写进后端喵。
    expect(() =>
      validateFailureRuleDraft(
        { httpStatus: '-1', errorClass: '', bodyRegex: '', action: 'next', freezeSeconds: '0' },
        0,
        identityTranslator
      )
    ).toThrow()
  })

  it('rejects http status above 599', () => {
    // 喵~防御：超出合法状态码上限的输入必须拒绝喵。
    expect(() =>
      validateFailureRuleDraft(
        { httpStatus: '600', errorClass: '', bodyRegex: '', action: 'next', freezeSeconds: '0' },
        0,
        identityTranslator
      )
    ).toThrow()
  })

  it('rejects non-integer http status', () => {
    // 喵~防御：小数状态码不是合法 HTTP 状态，必须拒绝喵。
    expect(() =>
      validateFailureRuleDraft(
        { httpStatus: '429.5', errorClass: '', bodyRegex: '', action: 'next', freezeSeconds: '0' },
        0,
        identityTranslator
      )
    ).toThrow()
  })

  it('rejects negative freeze seconds', () => {
    // 喵~防御：负冻结时长无意义，必须拒绝喵。
    expect(() =>
      validateFailureRuleDraft(
        { httpStatus: '0', errorClass: '', bodyRegex: '', action: 'next', freezeSeconds: '-3' },
        0,
        identityTranslator
      )
    ).toThrow()
  })

  it('rejects freeze seconds beyond one day', () => {
    // 喵~防御：超过一天冻结时长会长期阻断候选，必须拒绝喵。
    expect(() =>
      validateFailureRuleDraft(
        { httpStatus: '0', errorClass: '', bodyRegex: '', action: 'next', freezeSeconds: String(MAXIMUM_FREEZE_SECONDS + 1) },
        0,
        identityTranslator
      )
    ).toThrow()
  })

  it('reports the one-based rule index in the error message', () => {
    // 错误消息应携带规则序号，方便用户定位第三条规则的输入问题喵。
    const t = (key: string, options?: Record<string, unknown>): string => `${key}#${String(options?.index)}`
    expect(() =>
      validateFailureRuleDraft(
        { httpStatus: '999', errorClass: '', bodyRegex: '', action: 'next', freezeSeconds: '0' },
        2,
        t
      )
    ).toThrow('3')
  })
})
