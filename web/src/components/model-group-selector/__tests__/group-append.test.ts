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

import {
  appendVirtualGroupIfEnabled,
  VIRTUAL_GROUP_VALUE,
} from '../../model-group-selector'

describe('appendVirtualGroupIfEnabled', () => {
  // 普通分组数据，与后端 getUserGroups 返回形状一致喵。
  const groups = [
    { label: 'Default', value: 'default', ratio: 1 },
    { label: 'Premium', value: 'premium', ratio: 1 },
  ]

  test('keeps groups unchanged when showVirtualModels is false', () => {
    // 默认 false 时（渠道测试、模型测试等非游乐场页面）不出现虚拟模型分类喵。
    expect(appendVirtualGroupIfEnabled(groups, false, 'Virtual Models')).toEqual(
      groups
    )
  })

  test('appends the virtual group at the end when enabled', () => {
    // 仅游乐场生效：虚拟模型分类追加到分组列表末尾喵。
    const result = appendVirtualGroupIfEnabled(
      groups,
      true,
      'Virtual Models'
    )

    expect(result).toEqual([
      { label: 'Default', value: 'default', ratio: 1 },
      { label: 'Premium', value: 'premium', ratio: 1 },
      { label: 'Virtual Models', value: VIRTUAL_GROUP_VALUE },
    ])
  })

  test('does not mutate the original groups array', () => {
    // 追加操作应返回新数组，不影响调用方持有的原始分组数据喵。
    const snapshot = [...groups]
    appendVirtualGroupIfEnabled(groups, true, 'Virtual Models')

    expect(groups).toEqual(snapshot)
  })
})
