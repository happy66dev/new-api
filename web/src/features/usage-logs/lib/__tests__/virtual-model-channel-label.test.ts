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

import type { LogOtherData } from '../../types'
import { getVirtualModelCandidateLabel } from '../format'

describe('getVirtualModelCandidateLabel', () => {
  test('优先取成功候选的标识（多候选故障转移链）', () => {
    const candidates: LogOtherData['candidates'] = [
      { seq: 1, source: 'internal', label: 'gpt-4o', success: false, status_code: 429 },
      { seq: 2, source: 'custom', label: 'my-upstream', success: true, status_code: 200 },
    ]
    expect(getVirtualModelCandidateLabel(candidates)).toBe('my-upstream')
  })

  test('首个候选直接成功也能取到标识', () => {
    const candidates: LogOtherData['candidates'] = [
      { seq: 1, source: 'custom', label: 'deepseek-v4', success: true, status_code: 200 },
    ]
    expect(getVirtualModelCandidateLabel(candidates)).toBe('deepseek-v4')
  })

  test('失败日志无成功候选时回退最后一个尝试候选的标识', () => {
    const candidates: LogOtherData['candidates'] = [
      { seq: 1, source: 'internal', label: 'gpt-4o', success: false, status_code: 503 },
      { seq: 2, source: 'custom', label: 'my-upstream', success: false, status_code: 429 },
    ]
    expect(getVirtualModelCandidateLabel(candidates)).toBe('my-upstream')
  })

  test('空数组与缺失候选序列返回 null', () => {
    expect(getVirtualModelCandidateLabel([])).toBeNull()
    expect(getVirtualModelCandidateLabel(undefined)).toBeNull()
  })

  test('候选序列存在但所有候选都没有 label 时返回 null', () => {
    const candidates: LogOtherData['candidates'] = [
      { seq: 1, success: true, status_code: 200 },
    ]
    expect(getVirtualModelCandidateLabel(candidates)).toBeNull()
  })
})
