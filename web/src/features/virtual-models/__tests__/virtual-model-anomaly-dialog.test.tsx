/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'
import {
  buildVirtualModelAnomalyFingerprint,
  useVirtualModelAnomalyStore,
} from '@/stores/virtual-model-anomaly-store'

import { VirtualModelAnomalyDialog } from '../components/virtual-model-anomaly-dialog'

// 模拟 api.get 让异常扫描接口在测试里可控喵。
const apiClient = api as unknown as {
  get: (...args: unknown[]) => Promise<unknown>
}
const originalGet = apiClient.get

// anomaliesResponse 是一条「分组下模型被删除」的异常载荷喵。
const anomaliesResponse = {
  success: true,
  data: {
    anomalies: [
      {
        virtual_model_id: 1,
        virtual_model_name: 'alpha',
        candidate_id: 2,
        source_type: 'internal',
        group_name: 'default',
        real_model_name: 'gpt-old',
        reason_code: 'model_not_available',
        reason_message: '分组「default」下已没有模型「gpt-old」的可用渠道',
      },
    ],
  },
}

// alphaAnomalyFingerprint 是与载荷一致的异常指纹，用于断言已读喵。
const alphaAnomalyFingerprint = buildVirtualModelAnomalyFingerprint({
  virtualModelID: 1,
  candidateID: 2,
  reasonCode: 'model_not_available',
  groupName: 'default',
  realModelName: 'gpt-old',
})

function renderDialog() {
  // 每用例独立 QueryClient，避免缓存串味喵。
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <VirtualModelAnomalyDialog />
    </QueryClientProvider>
  )
}

describe('VirtualModelAnomalyDialog', () => {
  beforeEach(() => {
    // 构造已登录用户，保证异常轮询被启用喵。
    useAuthStore.setState({
      auth: {
        ...useAuthStore.getState().auth,
        user: { id: 7, username: 'tester', role: 1 },
      },
    })
    // 清空已读集合与 localStorage，让用例互不影响喵。
    useVirtualModelAnomalyStore.setState({ readAnomalyFingerprints: [] })
    window.localStorage.clear()
    // 注意：getVirtualModelAnomalies 内部取 response.data，因此 mock 需把载荷包在 data 字段里喵。
    apiClient.get = vi.fn().mockResolvedValue({ data: anomaliesResponse })
  })

  afterEach(() => {
    apiClient.get = originalGet
    // 还原匿名登录态，避免污染其它测试文件喵。
    useAuthStore.setState({
      auth: { ...useAuthStore.getState().auth, user: null },
    })
  })

  it('存在未读异常时自动弹出并列出模型、候选与原因', async () => {
    renderDialog()

    // 异常项包含模型名与候选路由目标喵。
    expect(
      await screen.findByText(/alpha · default\/gpt-old/)
    ).toBeInTheDocument()
    // 原因文案（无翻译时展示英文原文 key）喵。
    expect(
      screen.getByText(
        'The candidate model is no longer available in its group'
      )
    ).toBeInTheDocument()
  })

  it('点击关闭后把这些异常全部标记已读', async () => {
    renderDialog()
    await screen.findByText(/alpha · default\/gpt-old/)

    fireEvent.click(screen.getByRole('button', { name: 'Got it' }))

    await waitFor(() => {
      expect(
        useVirtualModelAnomalyStore
          .getState()
          .isAnomalyRead(alphaAnomalyFingerprint)
      ).toBe(true)
    })
  })

  it('异常已全部已读时不再自动弹出', async () => {
    // 预先标记已读：扫描结果返回但无未读，弹层保持关闭喵。
    useVirtualModelAnomalyStore
      .getState()
      .markAnomaliesRead([alphaAnomalyFingerprint])

    renderDialog()

    // 等待轮询完成后断言异常内容始终不出现喵。
    await waitFor(() => {
      expect(
        screen.queryByText(/alpha · default\/gpt-old/)
      ).not.toBeInTheDocument()
    })
  })
})
