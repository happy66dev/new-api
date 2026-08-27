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
  COMMON_HTTP_STATUS_RANGES,
  FREEZE_UNITS,
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
    // 默认按 HTTP 状态码匹配，超时与卡流为独立可选条件喵。
    conditionType: 'http',
    freezeSeconds: '0',
    freezeField: '',
    freezeUnit: 'seconds',
    httpStatus: '0',
    // 流式探测参数默认零，表示使用后端默认值喵。
    stallTimeoutSeconds: '0',
    minContentChars: '0',
    probeTotalTimeoutSeconds: '0',
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
      // 非 timeout 的错误分类（限流等）回退 HTTP 状态码条件，保留原状态码喵。
      conditionType: 'http',
      freezeSeconds: '30',
      freezeField: '',
      freezeUnit: 'seconds',
      httpStatus: '429',
      stallTimeoutSeconds: '0',
      minContentChars: '0',
      probeTotalTimeoutSeconds: '0',
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
      // 无超时分类时默认按 HTTP 状态码条件编辑喵。
      conditionType: 'http',
      freezeSeconds: '0',
      freezeField: '',
      freezeUnit: 'seconds',
      httpStatus: '0',
      stallTimeoutSeconds: '0',
      minContentChars: '0',
      probeTotalTimeoutSeconds: '0',
      id: undefined,
      action: 'next',
    })
  })

  it('falls back to seconds when the stored freeze unit is an empty string', () => {
    // 旧规则未配置单位时后端返回空串，必须回退到秒而不是错位成自动识别喵。
    const draft = toFailureRuleDraft({ http_status: 0, error_class: '', body_regex: '', action: 'freeze', freeze_seconds: 0, freeze_unit: '' as never })
    expect(draft.freezeUnit).toBe('seconds')
  })

  it('falls back to seconds for an unknown stored freeze unit', () => {
    // 非法单位值必须安全回退，避免下拉显示与字段隐藏逻辑错位喵。
    const draft = toFailureRuleDraft({ http_status: 0, error_class: '', body_regex: '', action: 'freeze', freeze_seconds: 0, freeze_unit: 'light-years' as never })
    expect(draft.freezeUnit).toBe('seconds')
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
      // 默认规则按 HTTP 状态码条件匹配喵。
      conditionType: 'http',
      freezeSeconds: '0',
      freezeField: '',
      freezeUnit: 'seconds',
      httpStatus: '0',
      stallTimeoutSeconds: '0',
      minContentChars: '0',
      probeTotalTimeoutSeconds: '0',
      action: 'next',
    })
  })
})

// describe toFailureRuleDraft：错误分类到条件类型的推断喵。
describe('toFailureRuleDraft condition inference', () => {
  it('maps a stable timeout class to the timeout condition', () => {
    // 服务端 timeout 分类应还原为固定超时条件喵。
    const draft = toFailureRuleDraft({ http_status: 0, error_class: 'timeout', body_regex: '', action: 'next', freeze_seconds: 0 })
    expect(draft.conditionType).toBe('timeout')
  })

  it('falls back to the http condition for a network error class', () => {
    // 网络错误等非超时分类回退 HTTP 状态码条件，因为状态码已能表达大部分失败喵。
    const draft = toFailureRuleDraft({ http_status: 0, error_class: 'network_error', body_regex: '', action: 'next', freeze_seconds: 0 })
    expect(draft.conditionType).toBe('http')
  })

  it('falls back to the http condition for an unknown error class', () => {
    // 白名单外的分类同样回退 HTTP 状态码条件，避免产生无法编辑的规则喵。
    const draft = toFailureRuleDraft({ http_status: 0, error_class: 'upstream_custom_gateway', body_regex: '', action: 'next', freeze_seconds: 0 })
    expect(draft.conditionType).toBe('http')
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
  it('converts a valid HTTP-status draft to API structure trimming text fields', () => {
    // 合法 HTTP 条件输入应保留 id、去空白并转为数值状态码与冻结秒数，响应体正则按自定义模式生成喵。
    const payload = validateFailureRuleDraft(
      makeDraft({
        httpStatus: '503',
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
      // HTTP 条件时错误分类必须置空，保证二选一语义喵。
      error_class: '',
      body_regex: 'overloaded',
      action: 'retry',
      freeze_seconds: 5,
      freeze_field: '',
      freeze_unit: 'seconds',
      // HTTP 条件不写探测参数，保持默认喵。
      stall_timeout_seconds: 0,
      min_content_chars: 0,
      probe_total_timeout_seconds: 0,
    })
  })

  it('maps the timeout condition to its stable error class', () => {
    // 超时条件应写入后端 timeout 分类，HTTP 状态码保持不限制喵。
    const timeoutPayload = validateFailureRuleDraft(makeDraft({ conditionType: 'timeout' }), 0, identityTranslator)
    expect(timeoutPayload.error_class).toBe('timeout')
    expect(timeoutPayload.http_status).toBe(0)
  })

  it('clears the error class for the http condition', () => {
    // HTTP 状态码条件必须清空错误分类，保证二选一语义喵。
    const httpPayload = validateFailureRuleDraft(makeDraft({ httpStatus: '503' }), 0, identityTranslator)
    expect(httpPayload.error_class).toBe('')
    expect(httpPayload.http_status).toBe(503)
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

  it('writes response-body freeze field and unit into the payload', () => {
    // 高级冻结配置应原样写入后端字段，字段名去空白喵。
    const payload = validateFailureRuleDraft(
      makeDraft({ freezeField: ' retry_after ', freezeUnit: 'mixed', action: 'freeze' }),
      0,
      identityTranslator
    )
    expect(payload.freeze_field).toBe('retry_after')
    expect(payload.freeze_unit).toBe('mixed')
  })

  it('rejects an overlong response-body freeze field', () => {
    // 喵~防御：字段名超过数据库列宽会截断产生歧义，必须拒绝喵。
    expect(() => validateFailureRuleDraft(makeDraft({ freezeField: 'x'.repeat(65) }), 0, identityTranslator)).toThrow()
  })

  it('round-trips an advanced freeze rule through toFailureRuleDraft', () => {
    // 已有高级冻结配置的服务端规则应还原为编辑草稿，供用户继续调整喵。
    const rule: VirtualModelFailureRule = {
      id: 9,
      http_status: 429,
      error_class: '',
      body_regex: '',
      action: 'freeze',
      freeze_seconds: 0,
      freeze_field: 'retry_after',
      freeze_unit: 'minutes',
    }
    const draft = toFailureRuleDraft(rule)
    expect(draft.freezeField).toBe('retry_after')
    expect(draft.freezeUnit).toBe('minutes')
  })
})

// describe BODY_REGEX_PRESETS：预设正则与描述文案的完整性喵。
describe('BODY_REGEX_PRESETS', () => {
  it('every preset ships a description key and a non-empty pattern', () => {
    // 预设必须同时提供可翻译的描述键与非空正则，供界面提示与运行时匹配使用喵。
    for (const preset of Object.values(BODY_REGEX_PRESETS)) {
      expect(preset.descriptionKey).not.toBe('')
      expect(preset.pattern).not.toBe('')
    }
  })
})

// describe FREEZE_UNITS：冻结单位选项的完整性喵。
describe('FREEZE_UNITS', () => {
  it('offers the auto option for scanning natural language durations', () => {
    // auto 单位必须存在于下拉选项，用于全文扫描自然语言时间喵。
    expect(FREEZE_UNITS.some((unit) => unit.value === 'auto')).toBe(true)
  })
})

// describe COMMON_HTTP_STATUS_RANGES：快速填入范围预设的完整性喵。
describe('COMMON_HTTP_STATUS_RANGES', () => {
  it('offers a 500~524 quick range preset that parses cleanly', () => {
    // 范围预设必须能被状态码解析函数识别，点击填入后即可生效喵。
    expect(COMMON_HTTP_STATUS_RANGES).toContain('500~524')
    expect(parseHttpStatusText('500~524')).toEqual({ min: 500, max: 524 })
  })
})

// describe validateFailureRuleDraft：auto 单位的透传喵。
describe('validateFailureRuleDraft with auto unit', () => {
  it('passes the auto unit through to the payload', () => {
    // auto 单位应在载荷中原样透传，供后端全文扫描自然语言时间喵。
    const payload = validateFailureRuleDraft(makeDraft({ freezeUnit: 'auto', action: 'freeze' }), 0, identityTranslator)
    expect(payload.freeze_unit).toBe('auto')
  })

  it('round-trips an auto unit rule back into the draft', () => {
    // 服务端返回 auto 单位时应还原为编辑草稿，供用户继续调整喵。
    const rule: VirtualModelFailureRule = { id: 11, http_status: 429, error_class: '', body_regex: '', action: 'freeze', freeze_seconds: 0, freeze_unit: 'auto' }
    expect(toFailureRuleDraft(rule).freezeUnit).toBe('auto')
  })

  it('clears a leftover freeze field when auto is selected', () => {
    // 从字段模式切换到 auto 后，残留的字段名不应写入载荷喵。
    const payload = validateFailureRuleDraft(makeDraft({ freezeUnit: 'auto', freezeField: 'retry_after', action: 'freeze' }), 0, identityTranslator)
    expect(payload.freeze_field).toBe('')
  })

  it('skips the field length check when auto is selected', () => {
    // auto 模式字段名无意义，即使残留超长文本也不应拦截保存喵。
    const payload = validateFailureRuleDraft(makeDraft({ freezeUnit: 'auto', freezeField: 'x'.repeat(70), action: 'freeze' }), 0, identityTranslator)
    expect(payload.freeze_unit).toBe('auto')
  })
})

// describe toFailureRuleDraft：卡流分类的推断喵。
describe('toFailureRuleDraft stalled inference', () => {
  it('maps a stalled_stream class to the stalled condition', () => {
    // 服务端 stalled_stream 分类应还原为卡流条件，并回填探测参数喵。
    const draft = toFailureRuleDraft({ http_status: 0, error_class: 'stalled_stream', body_regex: '', action: 'next', freeze_seconds: 0, stall_timeout_seconds: 45, min_content_chars: 20, probe_total_timeout_seconds: 240 })
    expect(draft.conditionType).toBe('stalled')
    expect(draft.stallTimeoutSeconds).toBe('45')
    expect(draft.minContentChars).toBe('20')
    expect(draft.probeTotalTimeoutSeconds).toBe('240')
  })

  it('falls back to the stalled condition for a rule without params', () => {
    // 无探测参数的 stalled 规则同样还原为卡流条件，参数回退默认零喵。
    const draft = toFailureRuleDraft({ http_status: 0, error_class: 'stalled_stream', body_regex: '', action: 'next', freeze_seconds: 0 })
    expect(draft.conditionType).toBe('stalled')
    expect(draft.stallTimeoutSeconds).toBe('0')
  })
})

// describe validateFailureRuleDraft：卡流条件的保存喵。
describe('validateFailureRuleDraft with stalled condition', () => {
  it('maps the stalled condition to its stable error class and probe params', () => {
    // 卡流条件应写入后端 stalled_stream 分类并携带探测参数喵。
    const payload = validateFailureRuleDraft(
      makeDraft({ conditionType: 'stalled', stallTimeoutSeconds: '45', minContentChars: '20', probeTotalTimeoutSeconds: '240' }),
      0,
      identityTranslator
    )
    expect(payload.error_class).toBe('stalled_stream')
    expect(payload.http_status).toBe(0)
    expect(payload.stall_timeout_seconds).toBe(45)
    expect(payload.min_content_chars).toBe(20)
    expect(payload.probe_total_timeout_seconds).toBe(240)
  })

  it('treats empty and zero probe params as unset', () => {
    // 空串与零都表示未配置，保存为零喵。
    const payload = validateFailureRuleDraft(makeDraft({ conditionType: 'stalled' }), 0, identityTranslator)
    expect(payload.stall_timeout_seconds).toBe(0)
    expect(payload.min_content_chars).toBe(0)
    expect(payload.probe_total_timeout_seconds).toBe(0)
  })

  it('writes zero probe params for the http condition', () => {
    // 非卡流条件不写探测参数，保持默认喵。
    const payload = validateFailureRuleDraft(makeDraft({ httpStatus: '503' }), 0, identityTranslator)
    expect(payload.stall_timeout_seconds).toBe(0)
    expect(payload.error_class).toBe('')
  })

  it('rejects a stalled timeout above 600 seconds', () => {
    // 喵~防御：静默秒数超过上界必须拒绝喵。
    expect(() => validateFailureRuleDraft(makeDraft({ conditionType: 'stalled', stallTimeoutSeconds: '601' }), 0, identityTranslator)).toThrow()
  })

  it('rejects a negative min content chars', () => {
    // 喵~防御：负数内容门槛必须拒绝喵。
    expect(() => validateFailureRuleDraft(makeDraft({ conditionType: 'stalled', minContentChars: '-1' }), 0, identityTranslator)).toThrow()
  })

  it('rejects a non-integer probe total timeout', () => {
    // 喵~防御：小数探测总预算必须拒绝喵。
    expect(() => validateFailureRuleDraft(makeDraft({ conditionType: 'stalled', probeTotalTimeoutSeconds: '300.5' }), 0, identityTranslator)).toThrow()
  })
})
