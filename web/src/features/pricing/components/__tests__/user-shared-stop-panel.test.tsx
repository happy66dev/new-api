/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { useIsAdmin } from '@/hooks/use-admin'

import { stopSharingUserUpstreamModel } from '../../api'
import type { PricingModel } from '../../types'
import { UserSharedStopPanel } from '../user-shared-stop-panel'

// 模拟管理员判断 hook，直接控制 isAdmin 值以便聚焦面板自身的门控逻辑喵。
vi.mock('@/hooks/use-admin', () => ({
  useIsAdmin: vi.fn(),
}))

// 模拟停共享 API，避免真实网络请求喵。
vi.mock('@/features/pricing/api', () => ({
  stopSharingUserUpstreamModel: vi.fn(),
}))

const mockedUseIsAdmin = vi.mocked(useIsAdmin)
const mockedStopSharing = vi.mocked(stopSharingUserUpstreamModel)

// makeModel 构造最简用户共享模型条目，供各用例覆盖喵。
function makeModel(
  overrides: Partial<PricingModel> = {}
): PricingModel {
  return {
    id: 1,
    model_name: 'user/shared-model',
    quota_type: 0,
    model_ratio: 1,
    completion_ratio: 1,
    enable_groups: ['user-shared'],
    owner_by: 'user-shared',
    share_owner_user_id: 42,
    ...overrides,
  }
}

// renderPanel 用全新 QueryClient 包裹渲染面板，避免跨用例共享查询缓存喵。
function renderPanel(
  model: PricingModel,
  onStopSharingSuccess?: () => void
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const wrapper = (props: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{props.children}</QueryClientProvider>
  )
  return render(
    <UserSharedStopPanel
      model={model}
      onStopSharingSuccess={onStopSharingSuccess}
    />,
    { wrapper }
  )
}

describe('UserSharedStopPanel', () => {
  beforeEach(() => {
    mockedUseIsAdmin.mockReset()
    mockedStopSharing.mockReset()
  })

  afterEach(() => {
    cleanup()
  })

  test('is hidden when the viewer is not an admin', () => {
    mockedUseIsAdmin.mockReturnValue(false)
    renderPanel(makeModel())
    // 非管理员即便在看用户共享模型也不显示入口喵。
    expect(
      screen.queryByRole('button', { name: 'Stop sharing' })
    ).not.toBeInTheDocument()
  })

  test('is hidden for admins on non-user-shared models', () => {
    mockedUseIsAdmin.mockReturnValue(true)
    renderPanel(makeModel({ owner_by: undefined }))
    // 普通站内模型不属于用户共享条目，不显示停共享入口喵。
    expect(
      screen.queryByRole('button', { name: 'Stop sharing' })
    ).not.toBeInTheDocument()
  })

  test('is hidden when the shared owner id is missing', () => {
    mockedUseIsAdmin.mockReturnValue(true)
    renderPanel(makeModel({ share_owner_user_id: undefined }))
    // 缺属主 id 无法定位目标模型，不显示入口喵。
    expect(
      screen.queryByRole('button', { name: 'Stop sharing' })
    ).not.toBeInTheDocument()
  })

  test('stops sharing a user-shared model after confirmation for admins', async () => {
    mockedUseIsAdmin.mockReturnValue(true)
    const onStopSharingSuccess = vi.fn()
    renderPanel(makeModel(), onStopSharingSuccess)

    // 管理员打开停共享确认框喵。
    fireEvent.click(screen.getByRole('button', { name: 'Stop sharing' }))
    expect(
      screen.getByText('Stop sharing this shared model?')
    ).toBeInTheDocument()

    // 确认后调用后端并以属主 id + 对外调用名传参喵。
    mockedStopSharing.mockResolvedValueOnce({ success: true })
    fireEvent.click(screen.getByRole('button', { name: 'Yes, stop sharing' }))

    await waitFor(() => {
      expect(mockedStopSharing).toHaveBeenCalledWith(42, 'user/shared-model')
    })
    // 成功后通知宿主收起当前视图喵。
    await waitFor(() => {
      expect(onStopSharingSuccess).toHaveBeenCalledTimes(1)
    })
  })

  test('cancel does not call the stop-sharing api', () => {
    mockedUseIsAdmin.mockReturnValue(true)
    renderPanel(makeModel())

    fireEvent.click(screen.getByRole('button', { name: 'Stop sharing' }))
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    // 取消后不触发任何停共享请求喵。
    expect(mockedStopSharing).not.toHaveBeenCalled()
  })

  test('does not report success when the backend rejects with success=false', async () => {
    mockedUseIsAdmin.mockReturnValue(true)
    const onStopSharingSuccess = vi.fn()
    renderPanel(makeModel(), onStopSharingSuccess)

    fireEvent.click(screen.getByRole('button', { name: 'Stop sharing' }))
    // 模拟 HTTP 200 但业务失败：success=false，调用方应等待后端统一提示而非执行成功收尾喵。
    mockedStopSharing.mockResolvedValueOnce({ success: false })
    fireEvent.click(screen.getByRole('button', { name: 'Yes, stop sharing' }))

    await waitFor(() => {
      expect(mockedStopSharing).toHaveBeenCalledTimes(1)
    })
    // 业务失败不触发宿主收起与成功回调喵。
    expect(onStopSharingSuccess).not.toHaveBeenCalled()
  })
})
