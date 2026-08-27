/* Copyright (C) 2023-2026 QuantumNous */
import { describe, expect, test } from 'vitest'

import {
  filterChatGroups,
  getGroupFallback,
  buildUserUpstreamModelOptions,
  buildVirtualModelOptions,
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

describe('buildVirtualModelOptions', () => {
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

  test('builds only enabled virtual models with the virtual/ prefix', () => {
    // 「虚拟」分组下只输出启用虚拟模型，value 带 virtual/ 前缀，普通模型不参与喵。
    const options = buildVirtualModelOptions(virtualModels)

    expect(options).toEqual([{ label: '我的网关', value: 'virtual/my-gateway' }])
  })

  test('falls back the label to the normalized name when display name is empty', () => {
    // display_name 为空时，label 应回退为 virtual/规范名保证可辨识喵。
    const options = buildVirtualModelOptions([
      { normalized_name: 'anon', display_name: '', enabled: true },
    ])

    expect(options[0]).toEqual({
      label: 'virtual/anon',
      value: 'virtual/anon',
    })
  })

  test('sorts virtual model options by display name', () => {
    // 虚拟模型按显示名自然排序，让下拉顺序稳定可预测喵。
    const options = buildVirtualModelOptions([
      { normalized_name: 'b', display_name: 'Beta', enabled: true },
      { normalized_name: 'a', display_name: 'Alpha', enabled: true },
    ])

    expect(options.map((model) => model.value)).toEqual([
      'virtual/a',
      'virtual/b',
    ])
  })

  test('returns an empty list when virtual model data is absent', () => {
    // 虚拟模型接口未加载时，「虚拟」分组下模型列表应为空而非报错喵。
    expect(buildVirtualModelOptions(undefined)).toEqual([])
  })
})

describe('buildUserUpstreamModelOptions', () => {
  // 构造一个启用、一个停用的自定上游模型，用于验证过滤与转换喵。
  const userUpstreamModels = [
    {
      normalized_name: 'kimi-pro',
      display_name: '我的 Kimi',
      enabled: true,
    },
    {
      normalized_name: 'disabled-upstream',
      display_name: '停用上游',
      enabled: false,
    },
  ]

  test('builds only enabled upstream models with the user/ prefix', () => {
    // 「自定上游」分组下只输出启用模型，value 带 user/ 前缀喵。
    const options = buildUserUpstreamModelOptions(userUpstreamModels)

    expect(options).toEqual([{ label: '我的 Kimi', value: 'user/kimi-pro' }])
  })

  test('falls back the label to the normalized name when display name is empty', () => {
    // display_name 为空时，label 应回退为 user/规范名保证可辨识喵。
    const options = buildUserUpstreamModelOptions([
      { normalized_name: 'anon-upstream', display_name: '', enabled: true },
    ])

    expect(options[0]).toEqual({
      label: 'user/anon-upstream',
      value: 'user/anon-upstream',
    })
  })

  test('sorts upstream options by display name', () => {
    // 自定上游模型按显示名自然排序，让下拉顺序稳定可预测喵。
    const options = buildUserUpstreamModelOptions([
      { normalized_name: 'b', display_name: 'Beta', enabled: true },
      { normalized_name: 'a', display_name: 'Alpha', enabled: true },
    ])

    expect(options.map((model) => model.value)).toEqual(['user/a', 'user/b'])
  })

  test('returns an empty list when upstream data is absent', () => {
    // 自定上游接口未加载时，分组下模型列表应为空而非报错喵。
    expect(buildUserUpstreamModelOptions(undefined)).toEqual([])
  })
})
