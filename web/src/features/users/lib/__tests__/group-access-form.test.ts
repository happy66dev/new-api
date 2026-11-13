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
import { describe, expect, test } from 'vitest'

import { parseUsableGroups, transformUserToFormDefaults } from '../user-form'

// 最小用户 fixture：只填充被测字段相关属性喵。
const baseUser = {
  id: 7,
  username: 'alice',
  display_name: 'Alice',
  quota: 0,
  used_quota: 0,
  request_count: 0,
  group: 'default',
  status: 1,
  role: 1,
}

describe('parseUsableGroups', () => {
  test('returns the stored group list for a valid JSON array', () => {
    // 后端以 JSON 数组字符串落库，编辑表单需要还原成字符串数组喵。
    expect(parseUsableGroups('["vip","virtual"]')).toEqual(['vip', 'virtual'])
  })

  test('returns an empty list for an empty or missing value', () => {
    // 喵~防御：空串与 undefined 都表示"白名单留空"，必须返回空数组而不是抛错喵。
    expect(parseUsableGroups('')).toEqual([])
    expect(parseUsableGroups(undefined)).toEqual([])
  })

  test('returns an empty list for malformed JSON and non-array payloads', () => {
    // 喵~防御：脏数据只能让用户少几个分组，不能抛异常打断表单渲染喵。
    expect(parseUsableGroups('not-json')).toEqual([])
    expect(parseUsableGroups('{"vip":true}')).toEqual([])
  })

  test('drops non-string entries from the array', () => {
    // 喵~防御：数组里混入数字或 null 时只保留字符串项喵。
    expect(parseUsableGroups('["vip",1,null,"virtual"]')).toEqual([
      'vip',
      'virtual',
    ])
  })
})

describe('transformUserToFormDefaults group access fields', () => {
  test('hydrates the stored mode and usable group list', () => {
    // 管理员保存过的模式与白名单必须原样回填到编辑表单喵。
    const defaults = transformUserToFormDefaults({
      ...baseUser,
      group_access_mode: 'custom',
      usable_groups: '["vip","user-shared"]',
    })

    expect(defaults.group_access_mode).toBe('custom')
    expect(defaults.usable_groups).toEqual(['vip', 'user-shared'])
  })

  test('falls back to follow-global with an empty list for legacy users', () => {
    // 存量用户没有这两个字段时应回落到"跟随全局 + 空白名单"喵。
    const defaults = transformUserToFormDefaults(baseUser)

    expect(defaults.group_access_mode).toBe('')
    expect(defaults.usable_groups).toEqual([])
  })
})
