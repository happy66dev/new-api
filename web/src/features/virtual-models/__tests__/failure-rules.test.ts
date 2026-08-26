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
  BODY_REGEX_PRESETS,
  MAXIMUM_FREEZE_SECONDS,
  MAXIMUM_HTTP_STATUS,
  createFailureRuleDraft,
  escapeRegex,
  parseHttpStatusText,
  resolveBodyRegex,
  toFailureRuleDraft,
  validateFailureRuleDraft,
  type FailureRuleDraft,
} from '../lib/failure-rules'

// identityTranslator 返回 key 本身，让校验函数只关心输入而不依赖真实 i18n 实例喵。
const identityTranslator = (key: string): string => key

// makeDraft 构造包含完整响应体正则编辑状态的草稿，减少重复字段喵。
function makeDraft(overrides: Partial<FailureRuleDraft> = {}): FailureRuleDraft {
  return {
    bodyRegex: '',
    bodyRegexMode: 'none',
    bodyRegexPreset: '',
    bodyRegexSimple: '',
    errorClass: '',
    freezeSeconds: '0',
    httpStatus: '0',
    action: 'next',
    ...overrides,
  }
}

// describe toFailureRuleDraft：服务端响应到可编辑草稿的映射喵。
describe('toFailureRuleDraft', () => {
  it('maps a complete custom-regex rule keeping the stable id and action', () => {
    // 非预设正则按自定义模式还原，保证已有配置不被丢失喵。
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
      bodyRegexMode: 'custom',
      bodyRegexPreset: '',
      bodyRegexSimple: '',
      errorClass: 'rate_limited',
      freezeSeconds: '30',
      httpStatus: '429',
      id: 7,
      action: 'freeze',
    })
  })

  it('recognizes a preset pattern as preset mode', () => {
    // 命中内置预设值的正则应还原为预设模式，方便用户继续调整喵。
    const presetPattern = BODY_REGEX_PRESETS.rate_limited.pattern
    const draft = toFailureRuleDraft({ http_status: 0, error_class: '', body_regex: presetPattern, action: 'next', freeze_seconds: 0 })
    expect(draft.bodyRegexMode).toBe('preset')
    expect(draft.bodyRegexPreset).toBe('rate_limited')
  })

  it('renders a status range as min~max text', () => {
    // 存储中的范围上界非零时应还原为 "min~max" 文本喵。
    const draft = toFailureRuleDraft({ http_status: 500, http_status_max: 524, error_class: '', body_regex: '', action: 'next', freeze_seconds: 0 })
    expect(draft.httpStatus).toBe('500~524')
  })

  it('provides safe defaults for missing optional fields', () => {
    // 缺失的可选字段必须以零/空/next 兜底，避免编辑表单出现 undefined 喵。
    const draft = toFailureRuleDraft({ http_status: 0, error_class: '', body_regex: '', action: 'next', freeze_seconds: 0 })
    expect(draft).toEqual({
      bodyRegex: '',
      bodyRegexMode: 'none',
      bodyRegexPreset: '',
      bodyRegexSimple: '',
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
      bodyRegexMode: 'none',
      bodyRegexPreset: '',
      bodyRegexSimple: '',
      errorClass: '',
      freezeSeconds: '0',
      httpStatus: '0',
      action: 'next',
    })
  })
})

// describe escapeRegex：简易文本到字面正则的转义喵。
describe('escapeRegex', () => {
  it('escapes regex metacharacters so text matches literally', () => {
    // 括号与点号等元字符必须转义，避免简易文本被当作正则语法喵。
    expect(escapeRegex('capacity(1).extra')).toBe('capacity\\(1\\)\\.extra')
  })

  it('leaves plain text and empty input untouched', () => {
    // 无元字符文本保持原样，空输入返回空串喵。
    expect(escapeRegex('rate limit')).toBe('rate limit')
    expect(escapeRegex('')).toBe('')
  })
})

// describe parseHttpStatusText：状态码文本到下界/上界的解析喵。
describe('parseHttpStatusText', () => {
  it('parses empty and zero as any status', () => {
    // 空串与零都表示不限制状态码喵。
    expect(parseHttpStatusText('')).toEqual({ min: 0, max: 0 })
    expect(parseHttpStatusText('0')).toEqual({ min: 0, max: 0 })
  })

  it('parses a single status code', () => {
    // 单值状态码没有范围上界喵。
    expect(parseHttpStatusText('429')).toEqual({ min: 429, max: 0 })
  })

  it('parses a range with tilde or hyphen separator', () => {
    // 范围分隔符支持波浪号与连字符喵。
    expect(parseHttpStatusText('500~524')).toEqual({ min: 500, max: 524 })
    expect(parseHttpStatusText('500-524')).toEqual({ min: 500, max: 524 })
  })

  it('returns null for malformed status text', () => {
    // 喵~防御：非整数、不完整范围与多段范围都无法安全解析喵。
    expect(parseHttpStatusText('abc')).toBeNull()
    expect(parseHttpStatusText('500~')).toBeNull()
    expect(parseHttpStatusText('500~524~600')).toBeNull()
  })
})

// describe resolveBodyRegex：按编辑模式生成最终响应体正则喵。
describe('resolveBodyRegex', () => {
  it('uses preset pattern when preset mode is selected', () => {
    // 预设模式直接采用内置正则，不依赖用户手写喵。
    expect(resolveBodyRegex(makeDraft({ bodyRegexMode: 'preset', bodyRegexPreset: 'overloaded' }))).toBe(BODY_REGEX_PRESETS.overloaded.pattern)
  })

  it('escapes plain text in simple mode and trims surrounding whitespace', () => {
    // 简易模式把普通文本转义为包含匹配，并清理首尾空白喵。
    expect(resolveBodyRegex(makeDraft({ bodyRegexMode: 'simple', bodyRegexSimple: '  capacity(1)  ' }))).toBe('capacity\\(1\\)')
  })

  it('keeps custom regex verbatim after trimming', () => {
    // 自定义模式直接使用用户正则，仅清理首尾空白喵。
    expect(resolveBodyRegex(makeDraft({ bodyRegexMode: 'custom', bodyRegex: '  rate.{0,5}limit  ' }))).toBe('rate.{0,5}limit')
  })

  it('falls back to empty for none mode or unknown preset', () => {
    // 不匹配响应体或预设键无效时回退为空串喵。
    expect(resolveBodyRegex(makeDraft({ bodyRegexMode: 'none' }))).toBe('')
    expect(resolveBodyRegex(makeDraft({ bodyRegexMode: 'preset', bodyRegexPreset: 'missing' }))).toBe('')
  })
})

// describe validateFailureRuleDraft：用户输入到 API 载荷的转换与防御喵。
describe('validateFailureRuleDraft', () => {
  it('converts valid draft to API structure trimming text fields', () => {
    // 合法输入应保留 id、去空白并转为数值状态码与冻结秒数，响应体正则按自定义模式生成喵。
    const payload = validateFailureRuleDraft(
      makeDraft({
        httpStatus: '503',
        errorClass: ' upstream_server_error ',
        bodyRegexMode: 'custom',
        bodyRegex: ' overloaded ',
        action: 'retry',
        freezeSeconds: '5',
        id: 3,
      }),
      0,
      identityTranslator
    )
    expect(payload).toEqual({
      id: 3,
      http_status: 503,
      http_status_max: 0,
      error_class: 'upstream_server_error',
      body_regex: 'overloaded',
      action: 'retry',
      freeze_seconds: 5,
    })
  })

  it('resolves simple-text body matching into an escaped regex', () => {
    // 简易模式保存时写入的是转义后的字面包含正则喵。
    const payload = validateFailureRuleDraft(
      makeDraft({ bodyRegexMode: 'simple', bodyRegexSimple: 'insufficient quota', httpStatus: '400' }),
      0,
      identityTranslator
    )
    expect(payload.body_regex).toBe('insufficient quota')
  })

  it('writes preset pattern and empty regex for none mode', () => {
    // 预设模式写入固定正则，none 模式写入空串喵。
    const presetPayload = validateFailureRuleDraft(
      makeDraft({ bodyRegexMode: 'preset', bodyRegexPreset: 'context_length' }),
      0,
      identityTranslator
    )
    expect(presetPayload.body_regex).toBe(BODY_REGEX_PRESETS.context_length.pattern)
    const nonePayload = validateFailureRuleDraft(makeDraft({ bodyRegexMode: 'none' }), 0, identityTranslator)
    expect(nonePayload.body_regex).toBe('')
  })

  it('accepts boundary values zero and maximum', () => {
    // 零状态码表示不限制，599 与一天冻结秒数均为合法上界喵。
    const payload = validateFailureRuleDraft(
      makeDraft({ httpStatus: String(MAXIMUM_HTTP_STATUS), action: 'passthrough', freezeSeconds: String(MAXIMUM_FREEZE_SECONDS) }),
      0,
      identityTranslator
    )
    expect(payload.http_status).toBe(MAXIMUM_HTTP_STATUS)
    expect(payload.freeze_seconds).toBe(MAXIMUM_FREEZE_SECONDS)
  })

  it('writes a status range into min and max fields', () => {
    // 状态码范围文本应拆分为下界与上界字段写给后端喵。
    const payload = validateFailureRuleDraft(
      makeDraft({ httpStatus: '500~524', action: 'passthrough' }),
      0,
      identityTranslator
    )
    expect(payload.http_status).toBe(500)
    expect(payload.http_status_max).toBe(524)
  })

  it('rejects a range above 599 or below zero', () => {
    // 喵~防御：范围端点超出合法状态码区间必须拒绝喵。
    expect(() => validateFailureRuleDraft(makeDraft({ httpStatus: '600~700' }), 0, identityTranslator)).toThrow()
    expect(() => validateFailureRuleDraft(makeDraft({ httpStatus: '-1~5' }), 0, identityTranslator)).toThrow()
  })

  it('rejects an inverted range where max is below min', () => {
    // 喵~防御：上界小于下界会产生永远无法命中的空范围，必须拒绝喵。
    expect(() => validateFailureRuleDraft(makeDraft({ httpStatus: '524~500' }), 0, identityTranslator)).toThrow()
  })

  it('rejects malformed status text', () => {
    // 喵~防御：无法解析的状态码文本必须拒绝喵。
    expect(() => validateFailureRuleDraft(makeDraft({ httpStatus: 'abc' }), 0, identityTranslator)).toThrow()
    expect(() => validateFailureRuleDraft(makeDraft({ httpStatus: '500~' }), 0, identityTranslator)).toThrow()
  })

  it('rejects negative http status', () => {
    // 喵~防御：负数状态码是畸形输入，必须抛错而不是写进后端喵。
    expect(() => validateFailureRuleDraft(makeDraft({ httpStatus: '-1' }), 0, identityTranslator)).toThrow()
  })

  it('rejects http status above 599', () => {
    // 喵~防御：超出合法状态码上限的输入必须拒绝喵。
    expect(() => validateFailureRuleDraft(makeDraft({ httpStatus: '600' }), 0, identityTranslator)).toThrow()
  })

  it('rejects non-integer http status', () => {
    // 喵~防御：小数状态码不是合法 HTTP 状态，必须拒绝喵。
    expect(() => validateFailureRuleDraft(makeDraft({ httpStatus: '429.5' }), 0, identityTranslator)).toThrow()
  })

  it('rejects negative freeze seconds', () => {
    // 喵~防御：负冻结时长无意义，必须拒绝喵。
    expect(() => validateFailureRuleDraft(makeDraft({ freezeSeconds: '-3' }), 0, identityTranslator)).toThrow()
  })

  it('rejects freeze seconds beyond one day', () => {
    // 喵~防御：超过一天冻结时长会长期阻断候选，必须拒绝喵。
    expect(() => validateFailureRuleDraft(makeDraft({ freezeSeconds: String(MAXIMUM_FREEZE_SECONDS + 1) }), 0, identityTranslator)).toThrow()
  })

  it('reports the one-based rule index in the error message', () => {
    // 错误消息应携带规则序号，方便用户定位第三条规则的输入问题喵。
    const t = (key: string, options?: Record<string, unknown>): string => `${key}#${String(options?.index)}`
    expect(() => validateFailureRuleDraft(makeDraft({ httpStatus: '999' }), 2, t)).toThrow('3')
  })
})
