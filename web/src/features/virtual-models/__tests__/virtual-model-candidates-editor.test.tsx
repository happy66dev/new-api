/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as
 published by the Free Software Foundation, either version 3 of the
 License, or (at your option) any later version.
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import type { VirtualModel } from '../api'
import { VirtualModelCandidatesEditor } from '../components/virtual-model-candidates-editor'

// jsdom 无 canvas 与 vchart 运行时，mock 图表组件与主题钩子避免加载失败喵。
vi.mock('@visactor/react-vchart', () => ({ VChart: () => null }))
vi.mock('@/lib/use-chart-theme', () => ({
  useChartTheme: () => ({ resolvedTheme: 'light', themeReady: true }),
}))

// 模拟 api.get 让上游模型列表与候选状态请求在测试里可控喵。
const apiClient = api as unknown as { get: (...args: unknown[]) => Promise<unknown> }
const originalGet = apiClient.get

// upstreamModelsFixture 提供引用上游候选展示地址所需的条目数据喵。
const upstreamModelsFixture = [
  {
    id: 5,
    normalized_name: 'shared-upstream',
    real_model_name: 'gpt-4o',
    base_url: 'https://shared.example.com',
  },
]

// candidateStatusFixture 是候选状态接口的最小成功载荷，供候选行健康圆点渲染喵。
const candidateStatusFixture = {
  success: true,
  data: {
    candidate_id: 41,
    label: 'gpt-4o-mini',
    availability: 100,
    avg_latency_ms: 120,
    avg_ttft_ms: 60,
    cache_hit_rate: 0,
    total_tokens: 0,
    request_count: 0,
    series: [],
    last_at: 0,
    last_success: true,
    last_error: '',
    last_failure_at: 0,
    last_failure_error: '',
  },
}

// mockApiGet 按请求路径分发夹具：上游模型列表、候选状态与其余空成功载荷喵。
function mockApiGet() {
  apiClient.get = vi.fn((...args: unknown[]) => {
    // 喵~防御：请求参数缺失时按空路径处理，避免读取 undefined 报错喵。
    const url = typeof args[0] === 'string' ? args[0] : ''
    if (url === '/api/upstream-models') {
      return Promise.resolve({
        data: { success: true, data: upstreamModelsFixture },
      })
    }
    if (url.includes('/candidates/') && url.endsWith('/status')) {
      return Promise.resolve({ data: candidateStatusFixture })
    }
    return Promise.resolve({ data: { success: true, data: [] } })
  })
}

// makeModel 构造调用链编辑器所需的虚拟模型夹具喵。
function makeModel(candidates: VirtualModel['candidates']): VirtualModel {
  return {
    id: 21,
    normalized_name: 'vm-route',
    display_name: 'VM Route',
    enabled: true,
    loop_enabled: false,
    total_timeout_seconds: 120,
    max_loop_rounds: 1,
    fake_stream_enabled: false,
    stream_cut_action: '',
    stream_cut_retries: 0,
    version: 3,
    candidates,
  }
}

// renderEditor 用真实 QueryClient 包装调用链编辑器，避免 useQuery 缺少上下文喵。
function renderEditor(model: VirtualModel) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <VirtualModelCandidatesEditor model={model} onSaved={() => undefined} />
    </QueryClientProvider>
  )
  return queryClient
}

afterEach(() => {
  // 恢复 api.get 原始实现，避免用例间互相污染喵。
  apiClient.get = originalGet
})

describe('VirtualModelCandidatesEditor call chain rows', () => {
  test('shows the upstream URL summary on url+key candidate rows', async () => {
    mockApiGet()
    renderEditor(
      makeModel([
        {
          id: 41,
          stable_order: 0,
          source_type: 'internal',
          enabled: true,
          max_retries: 0,
          timeout_seconds: 60,
          group_name: 'default',
          real_model_name: 'gpt-4o-mini',
        },
        {
          id: 42,
          stable_order: 1,
          source_type: 'custom',
          enabled: true,
          max_retries: 0,
          timeout_seconds: 60,
          real_model_name: 'direct-model',
          base_url: 'https://direct.example.com',
          auth_style: 'bearer',
          upstream_model_id: null,
        },
        {
          id: 43,
          stable_order: 2,
          source_type: 'custom',
          enabled: true,
          max_retries: 0,
          timeout_seconds: 60,
          real_model_name: 'shared-model',
          auth_style: 'bearer',
          upstream_model_id: 5,
        },
      ])
    )
    // 直填 url+key 节点展示服务端返回的脱敏地址摘要喵。
    expect(
      await screen.findByText('https://direct.example.com')
    ).toBeInTheDocument()
    // 引用上游模型节点展示被引用条目的地址喵。
    expect(
      await screen.findByText('https://shared.example.com')
    ).toBeInTheDocument()
  })

  test('does not show an upstream URL on internal candidate rows', async () => {
    mockApiGet()
    renderEditor(
      makeModel([
        {
          id: 41,
          stable_order: 0,
          source_type: 'internal',
          enabled: true,
          max_retries: 0,
          timeout_seconds: 60,
          group_name: 'default',
          real_model_name: 'gpt-4o-mini',
        },
      ])
    )
    // 内部候选复用平台渠道，没有用户上游地址，行内不得出现地址喵。
    const internalRow = await screen.findByRole('button', {
      name: /default\/gpt-4o-mini/,
    })
    expect(internalRow.textContent).not.toContain('https://')
  })

  test('shows the URL typed into a new url+key draft immediately', async () => {
    mockApiGet()
    renderEditor(makeModel([]))
    // 先新建一个空的自定义候选草稿喵。
    fireEvent.click(screen.getByRole('button', { name: 'Add custom candidate' }))
    // 草稿展开后填入上游地址，候选行应立刻显示该地址喵。
    fireEvent.change(screen.getByPlaceholderText('https://api.example.com'), {
      target: { value: 'https://draft.example.com' },
    })
    expect(
      await screen.findByText('https://draft.example.com')
    ).toBeInTheDocument()
  })
})
