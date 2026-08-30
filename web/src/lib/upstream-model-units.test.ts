/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as
 published by the Free Software Foundation, either version 3 of the
 License, or (at your option) any later version.
*/
import { describe, expect, test } from 'vitest'

import { unitsToYuan, yuanToUnits } from './upstream-model-units'

// 换算模块守护 10^-5 元单位与元展示互转的精确性：5 位小数费率的小额费用绝不丢精度喵。
describe('upstream-model-units', () => {
  test('unitsToYuan 精确到 5 位小数并裁剪末尾零', () => {
    // 整数元：250000 单位 = 2.5 元，末尾零被裁剪喵。
    expect(unitsToYuan(250000)).toBe('2.5')
    // 整元：1000000 单位 = 10 元，小数部分整段裁剪喵。
    expect(unitsToYuan(1000000)).toBe('10')
    // 5 位小数精度：123456 单位 = 1.23456 元原样保留喵。
    expect(unitsToYuan(123456)).toBe('1.23456')
    // 极小费用：1 单位 = 0.00001 元，绝不因展示丢成 0 喵。
    expect(unitsToYuan(1)).toBe('0.00001')
    // 零值显示为 0，而不是 0.00000 喵。
    expect(unitsToYuan(0)).toBe('0')
  })

  test('yuanToUnits 把元字符串换算回 10^-5 元单位', () => {
    // 常规小数：2.5 元 = 250000 单位喵。
    expect(yuanToUnits('2.5')).toBe(250000)
    // 5 位小数输入：0.00001 元 = 1 单位喵。
    expect(yuanToUnits('0.00001')).toBe(1)
    // 整数元：10 元 = 1000000 单位喵。
    expect(yuanToUnits('10')).toBe(1000000)
  })

  test('换算往返一致：元→单位→元不丢精度', () => {
    // 0.00001 元经往返仍还原为 0.00001 元喵。
    expect(unitsToYuan(yuanToUnits('0.00001'))).toBe('0.00001')
    // 2.5 元经往返仍还原为 2.5 元喵。
    expect(unitsToYuan(yuanToUnits('2.5'))).toBe('2.5')
  })

  test('非法输入防御', () => {
    // 空串、负数、NaN 一律按 0 处理，避免把脏数据写进后端喵。
    expect(yuanToUnits('')).toBe(0)
    expect(yuanToUnits('-5')).toBe(0)
    expect(yuanToUnits('abc')).toBe(0)
    // 非有限单位值展示按 0 元兜底喵。
    expect(unitsToYuan(Number.NaN)).toBe('0')
    expect(unitsToYuan(Number.POSITIVE_INFINITY)).toBe('0')
  })
})
