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

import type { VirtualModelStatus } from '../api'
import { VirtualModelOverviewStatus } from '../components/virtual-model-overview-status'

// jsdom 无 canvas 与 vchart 运行时，mock 图表组件与主题钩子避免加载失败喵。
vi.mock('@visactor/react-vchart', () => ({ VChart: () => null }))
vi.mock('@/lib/use-chart-theme', () => ({
  useChartTheme: () => ({ resolvedTheme: 'light', themeReady: true }),
}))

// 模拟 api.get 让候选状态请求在测试里可控喵。
const apiClient = api as unknown as { get: (...args: unknown[]) => Promise<unknown> }
const originalGet = apiClient.get

// candidateStatusFixture 是候选状态接口返回的富系列载荷喵。
const candidateStatusFixture = {
  success: true,
  data: {
    candidate_id: 42,
    label: 'gpt-4.1',
    availability: 100,
    avg_latency_ms: 160,
    avg_ttft_ms: 80,
    cache_hit_rate: 25,
    total_tokens: 500,
    request_count: 2,
    series: [
      {
        ts: Math.floor(Date.now() / 1000) - 3600,
        request_count: 2,
        success_rate: 100,
        avg_latency_ms: 160,
        avg_ttft_ms: 80,
        cache_hit_rate: 25,
        input_tokens: 400,
        output_tokens: 100,
        cached_tokens: 100,
        cache_creation_5m_tokens: 50,
        cache_creation_1h_tokens: 20,
      },
    ],
    last_at: Math.floor(Date.now() / 1000) - 60,
    last_success: true,
    last_error: '',
  },
}

// makeStatus 构造一份默认整体状态载荷，供各用例按需覆盖喵。
function makeStatus(
  overrides: Partial<VirtualModelStatus> = {}
): VirtualModelStatus {
  return {
    model: 'virtual/research-route',
    enabled: true,
    candidate_count: 1,
    enabled_candidates: 1,
    availability: 95,
    avg_latency_ms: 180,
    avg_ttft_ms: 90,
    cache_hit_rate: 20,
    total_tokens: 1200,
    request_count: 12,
    availability_24h: [100, 95, 90],
    series: [
      {
        ts: Math.floor(Date.now() / 1000) - 3600,
        request_count: 12,
        success_rate: 95,
        avg_latency_ms: 180,
        avg_ttft_ms: 90,
        cache_hit_rate: 20,
        input_tokens: 1000,
        output_tokens: 200,
        cached_tokens: 200,
        cache_creation_5m_tokens: 60,
        cache_creation_1h_tokens: 30,
      },
    ],
    last_at: Math.floor(Date.now() / 1000) - 60,
    last_success: true,
    last_latency_ms: 120,
    last_error: '',
    // 最近一次失败调用独立保留，即使最近一次调用成功也展示失败历史喵。
    last_failure_at: 0,
    last_failure_error: '',
    // 实时概览字段：默认无活跃请求喵。
    current_requests: 0,
    active_requests: [],
    candidates: [
      {
        candidate_id: 42,
        label: 'gpt-4.1',
        availability: 95,
        avg_latency_ms: 180,
        avg_ttft_ms: 90,
        cache_hit_rate: 20,
        total_tokens: 1200,
        request_count: 12,
        series: [],
        last_at: Math.floor(Date.now() / 1000) - 60,
        last_success: true,
        last_error: '',
        // 最近一次失败调用独立保留喵。
        last_failure_at: 0,
        last_failure_error: '',
      },
    ],
    ...overrides,
  }
}

// renderOverview 用真实 QueryClient 包装组件，避免 useQuery 缺少上下文喵。
function renderOverview(props: {
  modelID?: number
  status?: VirtualModelStatus | null
  loading?: boolean
  onRefresh?: () => void
  variant?: 'overview' | 'runtime'
}) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <VirtualModelOverviewStatus
        modelID={props.modelID ?? 1}
        status={props.status ?? null}
        loading={props.loading ?? false}
        onRefresh={props.onRefresh ?? (() => undefined)}
        variant={props.variant ?? 'overview'}
      />
    </QueryClientProvider>
  )
  return queryClient
}

afterEach(() => {
  // 恢复 api.get 原始实现，避免用例间互相污染喵。
  apiClient.get = originalGet
})

describe('VirtualModelOverviewStatus', () => {
  test('shows a loading placeholder while the status is being fetched', () => {
    renderOverview({ status: null, loading: true })
    expect(screen.getByText('Loading')).toBeInTheDocument()
  })

  test('shows an empty-data placeholder when there is no request sample yet', () => {
    renderOverview({ status: makeStatus({ request_count: 0 }) })
    expect(screen.getByText('No status data yet')).toBeInTheDocument()
  })

  test('overview variant renders metrics and last call', () => {
    renderOverview({ status: makeStatus(), variant: 'overview' })
    // 六项核心指标标签喵。
    expect(screen.getByText('Availability')).toBeInTheDocument()
    expect(screen.getByText('Average latency')).toBeInTheDocument()
    expect(screen.getByText('Average TTFT')).toBeInTheDocument()
    expect(screen.getByText('Cache hit rate')).toBeInTheDocument()
    expect(screen.getByText('Request Count')).toBeInTheDocument()
    expect(screen.getByText('Total Tokens')).toBeInTheDocument()
    // 可用性数值 95.00% 出现（指标区）喵。
    expect(screen.getAllByText('95.00%').length).toBeGreaterThanOrEqual(1)
    // 最近一次调用行喵。
    expect(screen.getByText(/Last call/)).toBeInTheDocument()
  })

  test('overview variant shows the current request count and active call chain', () => {
    // 带两个活跃请求的实时状态载荷喵。
    renderOverview({
      status: makeStatus({
        current_requests: 2,
        active_requests: [
          {
            request_id: '7-1',
            model_id: 1,
            model_name: 'virtual/research-route',
            candidate_index: 1,
            candidate_label: 'gpt-4.1',
            started_at: new Date(Date.now() - 5_000).toISOString(),
          },
          {
            request_id: '7-2',
            model_id: 1,
            model_name: 'virtual/research-route',
            candidate_index: 2,
            candidate_label: 'claude-4-sonnet',
            started_at: new Date(Date.now() - 30_000).toISOString(),
          },
        ],
      }),
      variant: 'overview',
    })
    // 当前请求数出现在实时区喵。
    expect(screen.getByText('2')).toBeInTheDocument()
    // 活跃请求列表展示调用链：候选正在被调用时显示「Calling <候选名>」而非「正在选择候选」喵。
    expect(screen.getByText(/Calling gpt-4\.1/)).toBeInTheDocument()
    expect(screen.getByText(/Calling claude-4-sonnet/)).toBeInTheDocument()
    expect(screen.getByText(/5s/)).toBeInTheDocument()
  })

  test('overview variant shows an empty hint when no request is active', () => {
    // 无活跃请求时展示空态提示而不是空列表喵。
    renderOverview({ status: makeStatus(), variant: 'overview' })
    expect(screen.getByText('No active requests right now')).toBeInTheDocument()
  })

  test('runtime variant renders charts and candidate summary rows', () => {
    renderOverview({ status: makeStatus(), variant: 'runtime' })
    // 逐小时图表标题出现（runtime 专属）喵。
    expect(screen.getAllByText('Average TTFT').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('Cache hit rate').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('Request volume')).toBeInTheDocument()
    // 候选摘要行展示候选真实模型名喵。
    expect(screen.getByText('gpt-4.1')).toBeInTheDocument()
  })

  test('fires the refresh callback from the header button', () => {
    const onRefresh = vi.fn()
    renderOverview({ status: makeStatus(), onRefresh })
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(onRefresh).toHaveBeenCalledTimes(1)
  })

  test('opens the candidate performance drawer when a candidate row is clicked', async () => {
    // 候选状态请求返回富系列载荷喵。
    apiClient.get = async (url: unknown) => {
      if (String(url).startsWith('/api/virtual-models/')) {
        return { data: candidateStatusFixture }
      }
      return { data: { success: true } }
    }
    renderOverview({ status: makeStatus(), variant: 'runtime' })
    fireEvent.click(screen.getByText('gpt-4.1'))
    // 抽屉打开后候选名出现在候选行与抽屉标题两处，用 findAllByText 喵。
    expect(await screen.findAllByText('gpt-4.1')).not.toHaveLength(0)
    // 数据加载后展示候选缓存命中率指标 25.00% 喵。
    expect(await screen.findByText('25.00%')).toBeInTheDocument()
  })
})
