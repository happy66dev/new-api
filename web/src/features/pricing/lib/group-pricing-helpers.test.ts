/* Copyright (C) 2023-2026 QuantumNous */
import { describe, expect, test } from 'vitest'

import type { PricingModel } from '../types'
import {
  getGroupQuotaType,
  hasGroupPricingOverrides,
  hasPriceTypeRatio,
  resolveGroupPricingModel,
} from './model-helpers'

// 一个「全局按量、A 组按次、B 组按量但更便宜、C 组走阶梯表达式」的模型喵。
// 这正是主人的场景：同一个模型 id 在不同分组下有完全不同的计费口径喵。
const model: PricingModel = {
  id: 1,
  model_name: 'deepseek-chat',
  quota_type: 0,
  model_ratio: 1,
  completion_ratio: 2,
  cache_ratio: 0.1,
  enable_groups: ['default', 'group-a', 'group-b', 'group-c'],
  group_ratio: { default: 1, 'group-a': 1, 'group-b': 1, 'group-c': 1 },
  group_pricing: {
    'group-a': {
      quota_type: 1,
      model_ratio: 0,
      model_price: 0.02,
      completion_ratio: 0,
    },
    'group-b': {
      quota_type: 0,
      model_ratio: 0.27,
      model_price: -1,
      completion_ratio: 3,
      cache_ratio: 1,
    },
    'group-c': {
      quota_type: 0,
      model_ratio: 0,
      model_price: 0,
      completion_ratio: 0,
      billing_mode: 'tiered_expr',
      billing_expr: 'p * 0.27 + c * 1.1',
    },
  },
}

describe('resolveGroupPricingModel', () => {
  test('returns the same object when no group is selected', () => {
    // 引用相等很重要：不做投影时不能产生新对象，否则卡片会无谓重渲染喵。
    expect(resolveGroupPricingModel(model, undefined)).toBe(model)
    expect(resolveGroupPricingModel(model, 'all')).toBe(model)
  })

  test('returns the same object for groups without an override', () => {
    expect(resolveGroupPricingModel(model, 'default')).toBe(model)
  })

  test('projects a per-call group onto the top-level fields', () => {
    const projected = resolveGroupPricingModel(model, 'group-a')
    expect(projected.quota_type).toBe(1)
    expect(projected.model_price).toBe(0.02)
    // 没有下发的可选倍率必须被投影成 null，绝不能漏回全局的 0.1 缓存倍率喵。
    expect(projected.cache_ratio).toBeNull()
    expect(projected.billing_mode).toBeUndefined()
  })

  test('projects a per-token group with its own ratios', () => {
    const projected = resolveGroupPricingModel(model, 'group-b')
    expect(projected.quota_type).toBe(0)
    expect(projected.model_ratio).toBe(0.27)
    expect(projected.completion_ratio).toBe(3)
    // 该分组显式把缓存倍率配成 1，表示「这家上游没有缓存折扣」喵。
    expect(projected.cache_ratio).toBe(1)
  })

  test('projects a group-scoped tiered expression', () => {
    const projected = resolveGroupPricingModel(model, 'group-c')
    expect(projected.billing_mode).toBe('tiered_expr')
    expect(projected.billing_expr).toBe('p * 0.27 + c * 1.1')
  })

  test('leaves the source model untouched', () => {
    resolveGroupPricingModel(model, 'group-a')
    // 投影必须是纯函数：原模型的全局价不能被改写，否则「全部分组」视图会显示错价喵。
    expect(model.quota_type).toBe(0)
    expect(model.model_ratio).toBe(1)
    expect(model.cache_ratio).toBe(0.1)
  })
})

describe('getGroupQuotaType', () => {
  test('reads the billing mode of the requested group', () => {
    expect(getGroupQuotaType(model, 'group-a')).toBe(1)
    expect(getGroupQuotaType(model, 'group-b')).toBe(0)
  })

  test('falls back to the global billing mode for plain groups', () => {
    expect(getGroupQuotaType(model, 'default')).toBe(0)
    expect(getGroupQuotaType(model, 'unknown-group')).toBe(0)
  })

  test('ignores a non-finite quota_type from the backend', () => {
    // 喵~防御：后端下发脏数据时必须回落全局值，不能把 NaN 当成计费方式喵。
    const dirty = {
      ...model,
      quota_type: 1,
      group_pricing: {
        broken: {
          quota_type: Number.NaN,
          model_ratio: 1,
          model_price: 0,
          completion_ratio: 1,
        },
      },
    }
    expect(getGroupQuotaType(dirty, 'broken')).toBe(1)
  })
})

describe('hasGroupPricingOverrides', () => {
  test('detects models with per-group pricing', () => {
    expect(hasGroupPricingOverrides(model)).toBe(true)
  })

  test('handles missing and empty maps', () => {
    // 喵~防御：group_pricing 缺失或为空对象都算「没有分组定制」喵。
    expect(
      hasGroupPricingOverrides({ ...model, group_pricing: undefined })
    ).toBe(false)
    expect(hasGroupPricingOverrides({ ...model, group_pricing: {} })).toBe(
      false
    )
  })
})

describe('hasPriceTypeRatio', () => {
  test('input and output prices always exist for token billing', () => {
    expect(hasPriceTypeRatio(model, 'input')).toBe(true)
    expect(hasPriceTypeRatio(model, 'output')).toBe(true)
  })

  test('optional ratios follow whether the group configured them', () => {
    const withCache = resolveGroupPricingModel(model, 'group-b')
    expect(hasPriceTypeRatio(withCache, 'cache')).toBe(true)
    const withoutCache = resolveGroupPricingModel(model, 'group-a')
    // 这一条是防 NaN 的关键：该分组没有缓存倍率时表格要显示 '-' 而不是算出 NaN 喵。
    expect(hasPriceTypeRatio(withoutCache, 'cache')).toBe(false)
    expect(hasPriceTypeRatio(withoutCache, 'image')).toBe(false)
  })

  test('a zero ratio still counts as configured', () => {
    // 显式配 0 表示「这个分组该项真免费」，不能被当成未配置喵。
    expect(hasPriceTypeRatio({ ...model, cache_ratio: 0 }, 'cache')).toBe(true)
  })

  test('audio output needs both audio ratios', () => {
    expect(
      hasPriceTypeRatio({ ...model, audio_ratio: 5 }, 'audio_output')
    ).toBe(false)
    expect(
      hasPriceTypeRatio(
        { ...model, audio_ratio: 5, audio_completion_ratio: 2 },
        'audio_output'
      )
    ).toBe(true)
  })
})
