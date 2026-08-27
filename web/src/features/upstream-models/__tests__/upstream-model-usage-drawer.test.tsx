/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as
 published by the Free Software Foundation, either version 3 of the
 License, or (at your option) any later version.
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

// jsdom 无 vchart 运行时，mock 图表组件与主题钩子避免加载失败喵。
vi.mock('@visactor/react-vchart', () => ({ VChart: () => null }))
vi.mock('@/lib/use-chart-theme', () => ({
  useChartTheme: () => ({ resolvedTheme: 'light', themeReady: true }),
}))

import { UpstreamModelUsageDrawer } from '../index'
import type { UserUpstreamModel } from '../api'

// 模拟 api.get/delete 让使用情况接口与清空接口在测试里可控喵。
const apiClient = api as unknown as { get: (...args: unknown[]) => Promise<unknown>; delete: (...args: unknown[]) => Promise<unknown> }
const originalGet = apiClient.get
const originalDelete = apiClient.delete

// sharedModelFixture 是一个已开启共享的上游模型行喵。
const sharedModelFixture: UserUpstreamModel = {
  id: 5,
  owner_user_id: 7,
  normalized_name: 'usage-model',
  display_name: 'Usage Model',
  description: '',
  enabled: true,
  api_key_set: true,
  base_url: 'https://example.com',
  real_model_name: 'gpt-4o',
  auth_style: 'bearer',
  model_ratio: '1',
  completion_ratio: '1',
  cache_ratio: '1',
  cache_creation_ratio: '1',
  cache_creation_5m_ratio: '1',
  cache_creation_1h_ratio: '1',
  image_ratio: '1',
  audio_ratio: '1',
  audio_completion_ratio: '1',
  balance_cents: 1000,
  available_cents: 800,
  spend_limit_cents: 0,
  total_spent_cents: 0,
  upstream_remaining_cents: 0,
  upstream_remaining_at: 0,
  balance_check_enabled: false,
  balance_check_path: '',
  share_enabled: true,
  share_limit_cents: 500,
  share_spent_cents: 0,
  share_whitelist: '',
  share_blacklist: '',
  share_list_mode: '',
  show_balance_enabled: false,
  version: 1,
  created_time: 100,
  updated_time: 100,
}

// renderUsage 用真实 QueryClient 包装抽屉，供各用例使用喵。
function renderUsage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <UpstreamModelUsageDrawer open onOpenChange={() => undefined} model={sharedModelFixture} />
    </QueryClientProvider>
  )
  return queryClient
}

afterEach(() => {
  // 恢复 api.get/delete 原始实现，避免用例间互相污染喵。
  apiClient.get = originalGet
  apiClient.delete = originalDelete
})

describe('UpstreamModelUsageDrawer', () => {
  test('renders per-user usage rows including the user id column', async () => {
    // 模拟共享模型使用情况接口返回两名用户的聚合数据喵。
    apiClient.get = vi.fn().mockResolvedValue({
      data: {
        success: true,
        data: [
          { user_id: 8, username: 'user8', request_count: 2, prompt_tokens: 150, completion_tokens: 30, last_at: 2000 },
          { user_id: 9, username: 'user9', request_count: 1, prompt_tokens: 30, completion_tokens: 5, last_at: 1500 },
        ],
      },
    })
    renderUsage()
    // 等待异步请求完成并渲染喵。
    expect(await screen.findByText('user8')).toBeInTheDocument()
    // 明确展示用户 id 列，属主能识别谁在调用共享模型喵。
    expect(screen.getByText('8')).toBeInTheDocument()
    expect(screen.getByText('9')).toBeInTheDocument()
    // 请求数、token 聚合与最近调用列同步展示喵。
    expect(screen.getByText('2')).toBeInTheDocument()
    expect(screen.getByText('150')).toBeInTheDocument()
    // 30 同时是 user8 的完成 token 与 user9 的输入 token，需用 getAllByText 喵。
    expect(screen.getAllByText('30').length).toBeGreaterThanOrEqual(1)
  })

  test('shows an empty placeholder when there is no shared usage yet', async () => {
    // 模拟空使用情况接口返回喵。
    apiClient.get = vi.fn().mockResolvedValue({ data: { success: true, data: [] } })
    renderUsage()
    expect(await screen.findByText('No shared usage yet')).toBeInTheDocument()
  })

  test('keeps the shared model name in the drawer description', async () => {
    // 空数据用例，仅校验标题描述带模型名喵。
    apiClient.get = vi.fn().mockResolvedValue({ data: { success: true, data: [] } })
    renderUsage()
    expect(await screen.findByText(/user\/usage-model/)).toBeInTheDocument()
  })

  test('clears shared usage after a confirmation dialog', async () => {
    // 模拟使用情况返回一行数据，让清空按钮可用喵。
    apiClient.get = vi.fn().mockResolvedValue({
      data: { success: true, data: [{ user_id: 8, username: 'user8', request_count: 2, prompt_tokens: 150, completion_tokens: 30, last_at: 2000 }] },
    })
    // 模拟清空接口成功喵。
    const deleteMock = vi.fn().mockResolvedValue({ data: { success: true, data: { id: 5 } } })
    apiClient.delete = deleteMock
    renderUsage()
    // 先等待数据渲染完成，确保清空按钮解除禁用喵。
    expect(await screen.findByText('user8')).toBeInTheDocument()
    // 点击清空按钮打开二次确认对话框喵。
    fireEvent.click(screen.getByText('Clear usage'))
    // 二次确认对话框出现，点确认清空喵。
    fireEvent.click(await screen.findByText('Clear'))
    // 断言清空接口被调用且路径含模型 id 喵。
    await waitFor(() => {
      expect(deleteMock).toHaveBeenCalledWith('/api/upstream-models/5/usage')
    })
    // 清空成功后抽屉通过 invalidate 重新拉取使用情况，get 应至少被调用两次喵。
    await waitFor(() => {
      expect(apiClient.get).toHaveBeenCalledTimes(2)
    })
  })
})
