/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import i18next from 'i18next'
import { describe, expect, test, vi } from 'vitest'

import { VirtualModels } from '../index'

// 测试初始化默认会转义插值中的斜杠，这里与生产 i18n 配置保持一致喵。
i18next.options.interpolation = {
  ...i18next.options.interpolation,
  escapeValue: false,
}

// jsdom 无 canvas 与 vchart 运行时，mock 图表组件与主题钩子避免加载失败喵。
vi.mock('@visactor/react-vchart', () => ({ VChart: () => null }))
vi.mock('@/lib/use-chart-theme', () => ({
  useChartTheme: () => ({ resolvedTheme: 'light', themeReady: true }),
}))

// 模拟虚拟模型接口，让页面在测试里稳定展示一个固定模型喵。
vi.mock('@/features/virtual-models/api', async (importOriginal) => {
  const actual =
    await importOriginal<typeof import('@/features/virtual-models/api')>()
  return {
    ...actual,
    // 喵~防御：只返回一个启用模型，避免页面在空列表分支里不渲染概览头部喵。
    getVirtualModels: vi.fn(async () => ({
      success: true,
      data: [
        {
          id: 7,
          normalized_name: 'research-route',
          display_name: 'Research Route',
          enabled: true,
          loop_enabled: false,
          total_timeout_seconds: 120,
          max_loop_rounds: 1,
          fake_stream_enabled: false,
          stream_cut_action: '',
          stream_cut_retries: 0,
          version: 1,
          candidates: [],
          binding_token_ids: [],
          global_failure_rules: [],
        },
      ],
    })),
    // 返回空状态载荷，避免测试触发图表与轮询逻辑喵。
    getVirtualModelStatus: vi.fn(async () => ({
      success: true,
      data: {
        model: 'virtual/research-route',
        enabled: true,
        candidate_count: 0,
        enabled_candidates: 0,
        availability: 0,
        avg_latency_ms: 0,
        avg_ttft_ms: 0,
        cache_hit_rate: 0,
        total_tokens: 0,
        request_count: 0,
        availability_24h: [],
        series: [],
        last_at: 0,
        last_success: false,
        last_failure_at: 0,
        last_failure_error: '',
        active_requests: [],
        current_requests: 0,
        candidates: [],
      },
    })),
  }
})

// 异常轮询与页面标题无关，测试里直接返回空的未读集合喵。
vi.mock('@/features/virtual-models/hooks/use-virtual-model-anomalies', () => ({
  useVirtualModelAnomalies: () => ({
    unreadByModelId: new Map(),
    unreadByCandidateId: new Map(),
    unreadAnomalies: [],
    hasUnread: false,
    markAllUnreadAsRead: () => undefined,
  }),
}))

describe('VirtualModels overview header', () => {
  test('shows the model ID before the virtual route and offers a copy button', async () => {
    // 每个用例使用独立 QueryClient，避免请求缓存跨用例污染喵。
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={queryClient}>
        <VirtualModels />
      </QueryClientProvider>
    )
    // 概览头部必须展示「模型ID: virtual/xxx」而不是裸路由喵。
    expect(
      await screen.findByText('Model ID: virtual/research-route')
    ).toBeInTheDocument()
    // 复制按钮必须提供稳定的无障碍名称，方便键盘与读屏用户使用喵。
    expect(
      screen.getByRole('button', { name: 'Copy model ID' })
    ).toBeInTheDocument()
  })
})
