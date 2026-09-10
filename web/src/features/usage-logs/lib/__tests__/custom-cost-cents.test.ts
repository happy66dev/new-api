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

import { formatCustomCostCents } from '../format'

describe('formatCustomCostCents', () => {
  test('按 100000 cents = 1 元换算并保留 5 位小数（与后端 %.5f 同口径）', () => {
    expect(formatCustomCostCents(100000)).toBe('1.00000')
    expect(formatCustomCostCents(150000)).toBe('1.50000')
    expect(formatCustomCostCents(123)).toBe('0.00123')
  })

  test('小数不足 5 位时右侧补零，保证列宽稳定', () => {
    expect(formatCustomCostCents(1)).toBe('0.00001')
    expect(formatCustomCostCents(10000)).toBe('0.10000')
  })

  test('零值视为该候选没有自定义计费，返回 null 由调用方跳过展示', () => {
    expect(formatCustomCostCents(0)).toBeNull()
  })

  test('字段缺失（undefined / null）返回 null', () => {
    expect(formatCustomCostCents(undefined)).toBeNull()
    expect(formatCustomCostCents(null)).toBeNull()
  })

  test('非法数值（NaN / Infinity）返回 null，避免渲染出 "NaN" 文本', () => {
    expect(formatCustomCostCents(Number.NaN)).toBeNull()
    expect(formatCustomCostCents(Number.POSITIVE_INFINITY)).toBeNull()
    expect(formatCustomCostCents(Number.NEGATIVE_INFINITY)).toBeNull()
  })

  test('负数（退款调差场景）按原值展示，便于审计差额', () => {
    expect(formatCustomCostCents(-100)).toBe('-0.00100')
  })
})
