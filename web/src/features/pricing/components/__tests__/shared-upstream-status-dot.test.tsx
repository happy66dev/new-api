/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as
 published by the Free Software Foundation, either version 3 of the
 License, or (at your option) any later version.
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import { describe, expect, test, vi } from 'vitest'

import { getSharedUpstreamModelStatus } from '@/features/upstream-models/api'

import { SharedUpstreamStatusDot } from '../shared-upstream-status-dot'

// 共享状态接口用 mock 替换，避免测试触碰真实网络喵。
vi.mock('@/features/upstream-models/api', async (importOriginal) => {
  const actual =
    await importOriginal<typeof import('@/features/upstream-models/api')>()
  return {
    ...actual,
    getSharedUpstreamModelStatus: vi.fn(),
  }
})

// renderDot 以隔离的 QueryClient 渲染共享状态圆点喵。
function renderDot(modelName: string) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <SharedUpstreamStatusDot modelName={modelName} />
    </QueryClientProvider>
  )
}

describe('SharedUpstreamStatusDot', () => {
  test('queries the shared status with the canonical user/ prefix stripped off', async () => {
    vi.mocked(getSharedUpstreamModelStatus).mockResolvedValue({
      success: true,
      data: {
        availability: 95,
        avg_latency_ms: 340,
        request_count: 9,
        last_at: Math.floor(Date.now() / 1000) - 60,
        last_success: true,
      },
    })
    // 模型广场条目的 model_name 形如 user/<name>，必须剥离 user/ 前缀才能命中共享状态接口喵。
    renderDot('user/my-shared-model')
    // 请求携带剥离前缀后的规范名喵。
    await waitFor(() => {
      expect(getSharedUpstreamModelStatus).toHaveBeenCalledWith(
        'my-shared-model'
      )
    })
    // 数据到达后圆点按可用性分级着色（95% 落入 good 档位喵）。
    await waitFor(() => {
      expect(screen.getByTestId('entity-status-dot').className).toContain(
        'bg-emerald'
      )
    })
  })

  test('also strips the legacy upstream/ prefix for historical model names', async () => {
    vi.mocked(getSharedUpstreamModelStatus).mockResolvedValue({
      success: true,
      data: undefined,
    })
    renderDot('upstream/legacy-model')
    await waitFor(() => {
      expect(getSharedUpstreamModelStatus).toHaveBeenCalledWith('legacy-model')
    })
  })

  test('falls back to the full name when no known prefix is present', async () => {
    vi.mocked(getSharedUpstreamModelStatus).mockResolvedValue({
      success: true,
      data: undefined,
    })
    renderDot('plain-name')
    await waitFor(() => {
      expect(getSharedUpstreamModelStatus).toHaveBeenCalledWith('plain-name')
    })
    // 无数据时圆点保持灰色喵。
    expect(screen.getByTestId('entity-status-dot').className).toContain(
      'bg-muted-foreground/40'
    )
  })
})
