/* Copyright (C) 2023-2026 QuantumNous */
import { describe, expect, test } from 'vitest'

import {
  filterChatGroups,
  getGroupFallback,
  mergeVirtualModelOptions,
} from './playground-option-utils'

describe('chat group filtering', () => {
  test('does not expose the virtual auto group in the chat selector', () => {
    const groups = [
      { label: 'Default', value: 'default', ratio: 1 },
      { label: 'Auto', value: 'auto', ratio: 1 },
      { label: 'Premium', value: 'premium', ratio: 1 },
    ]

    expect(filterChatGroups(groups).map((group) => group.value)).toEqual([
      'default',
      'premium',
    ])
  })

  test('falls back from a persisted auto group to a selectable chat group', () => {
    const groups = filterChatGroups([
      { label: 'Default', value: 'default', ratio: 1 },
      { label: 'Auto', value: 'auto', ratio: 1 },
    ])

    expect(getGroupFallback(groups, 'auto')).toBe('default')
  })
})

describe('mergeVirtualModelOptions', () => {
  // 组装带普通模型的基线数据，各用例在此基础上追加虚拟模型喵。
  const baseModels = [{ label: 'gpt-4o', value: 'gpt-4o' }]
  // 构造一个启用、一个停用的虚拟模型，用于验证过滤与转换喵。
  const virtualModels = [
    {
      normalized_name: 'my-gateway',
      display_name: '我的网关',
      enabled: true,
    },
    {
      normalized_name: 'disabled-model',
      display_name: '停用模型',
      enabled: false,
    },
  ]

  test('appends enabled virtual models with the virtual/ prefix', () => {
    // 合并后普通模型保留在原位，启用虚拟模型追加到末尾且 value 带前缀喵。
    const merged = mergeVirtualModelOptions(baseModels, virtualModels)

    expect(merged).toEqual([
      { label: 'gpt-4o', value: 'gpt-4o' },
      { label: '我的网关', value: 'virtual/my-gateway' },
    ])
  })

  test('falls back the label to the normalized name when display name is empty', () => {
    // display_name 为空时，label 应回退为 virtual/规范名保证可辨识喵。
    const merged = mergeVirtualModelOptions(baseModels, [
      { normalized_name: 'anon', display_name: '', enabled: true },
    ])

    expect(merged[1]).toEqual({
      label: 'virtual/anon',
      value: 'virtual/anon',
    })
  })

  test('sorts virtual model options by display name', () => {
    // 合并结果中的虚拟模型应按显示名自然排序，不受普通模型顺序影响喵。
    const merged = mergeVirtualModelOptions([], [
      { normalized_name: 'b', display_name: 'Beta', enabled: true },
      { normalized_name: 'a', display_name: 'Alpha', enabled: true },
    ])

    expect(merged.map((model) => model.value)).toEqual([
      'virtual/a',
      'virtual/b',
    ])
  })

  test('returns only plain models when virtual model data is absent', () => {
    // 虚拟模型接口未加载时，合并结果应与普通模型列表一致喵。
    const merged = mergeVirtualModelOptions(baseModels, undefined)

    expect(merged).toEqual([{ label: 'gpt-4o', value: 'gpt-4o' }])
  })
})
