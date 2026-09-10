/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { describe, expect, it } from 'vitest'

import {
  describeShareSkipReason,
  describeShareWarning,
  extractShareCodeErrorMessage,
  isShareCodeGone,
  resolveShareCodeStatus,
} from '../lib/share-code'

// identityTranslator 返回 i18n 键本身，让断言只关心映射结果而不依赖真实翻译实例喵。
const identityTranslator = (key: string): string => key

// makeShareCode 构造分享码状态判定所需的完整字段，避免每个用例重复写字面量喵。
function makeShareCode(overrides: {
  revoked_at?: number
  expires_at?: number
  import_count?: number
  max_imports?: number
}) {
  return {
    revoked_at: overrides.revoked_at ?? 0,
    expires_at: overrides.expires_at ?? 0,
    import_count: overrides.import_count ?? 0,
    max_imports: overrides.max_imports ?? 0,
  }
}

describe('resolveShareCodeStatus', () => {
  it('未撤销、未过期且未达上限时判定为可用', () => {
    expect(resolveShareCodeStatus(makeShareCode({}), 1_700_000_000)).toBe(
      'active'
    )
  })

  it('撤销时间非零时优先判定为已撤销，即使同时已过期', () => {
    const status = resolveShareCodeStatus(
      makeShareCode({ revoked_at: 1_600_000_000, expires_at: 1_600_000_000 }),
      1_700_000_000
    )
    expect(status).toBe('revoked')
  })

  it('到期时间早于当前时间时判定为已过期', () => {
    const status = resolveShareCodeStatus(
      makeShareCode({ expires_at: 1_600_000_000 }),
      1_700_000_000
    )
    expect(status).toBe('expired')
  })

  it('到期时间为零表示永不过期，不应判定为已过期', () => {
    expect(
      resolveShareCodeStatus(makeShareCode({ expires_at: 0 }), 1_700_000_000)
    ).toBe('active')
  })

  it('导入次数达到非零上限时判定为已达上限', () => {
    const status = resolveShareCodeStatus(
      makeShareCode({ import_count: 3, max_imports: 3 }),
      1_700_000_000
    )
    expect(status).toBe('exhausted')
  })

  it('上限为零表示不限次数，达到任意导入次数仍判定为可用', () => {
    const status = resolveShareCodeStatus(
      makeShareCode({ import_count: 999, max_imports: 0 }),
      1_700_000_000
    )
    expect(status).toBe('active')
  })
})

describe('describeShareSkipReason', () => {
  it('已知原因码返回对应翻译，而不是后端中文兜底说明', () => {
    const text = describeShareSkipReason(
      'group_not_accessible',
      '你的账号没有分组「vip」的访问权限',
      identityTranslator
    )
    expect(text).toBe('Your account cannot access this group.')
  })

  it('未知原因码回退到后端兜底说明', () => {
    const text = describeShareSkipReason(
      'brand_new_reason',
      '后端说明',
      identityTranslator
    )
    expect(text).toBe('后端说明')
  })

  it('未知原因码且兜底说明为空时回退到原因码本身，避免界面空白', () => {
    const text = describeShareSkipReason(
      'brand_new_reason',
      '',
      identityTranslator
    )
    expect(text).toBe('brand_new_reason')
  })
})

describe('describeShareWarning', () => {
  it('已知提示码返回对应翻译', () => {
    const text = describeShareWarning(
      'all_candidates_skipped',
      identityTranslator
    )
    expect(text).toBe(
      'Every candidate was skipped. The imported plan is empty and cannot be used.'
    )
  })

  it('未知提示码直接展示原始码，保证信息不丢失', () => {
    expect(describeShareWarning('unknown_warning', identityTranslator)).toBe(
      'unknown_warning'
    )
  })
})

describe('extractShareCodeErrorMessage', () => {
  it('优先取后端响应体里的 message', () => {
    const error = {
      response: { status: 410, data: { message: '分享码已被撤销' } },
    }
    expect(extractShareCodeErrorMessage(error, '兜底')).toBe('分享码已被撤销')
  })

  it('响应体没有 message 时回退到 Error 自身的 message', () => {
    expect(extractShareCodeErrorMessage(new Error('网络异常'), '兜底')).toBe(
      '网络异常'
    )
  })

  it('空对象、null 与基本类型都回退到默认兜底文案', () => {
    expect(extractShareCodeErrorMessage({}, '兜底')).toBe('兜底')
    expect(extractShareCodeErrorMessage(null, '兜底')).toBe('兜底')
    expect(extractShareCodeErrorMessage('boom', '兜底')).toBe('兜底')
  })

  it('后端 message 为空白字符串时视为没有消息', () => {
    const error = { response: { data: { message: '   ' } } }
    expect(extractShareCodeErrorMessage(error, '兜底')).toBe('兜底')
  })
})

describe('isShareCodeGone', () => {
  it('410 表示分享码已撤销、过期或用尽', () => {
    expect(isShareCodeGone({ response: { status: 410 } })).toBe(true)
  })

  it('404 表示分享码不存在，与已失效区分开', () => {
    expect(isShareCodeGone({ response: { status: 404 } })).toBe(false)
  })

  it('异常结构缺失时返回 false 而不是抛错', () => {
    expect(isShareCodeGone(null)).toBe(false)
    expect(isShareCodeGone(new Error('boom'))).toBe(false)
  })
})
