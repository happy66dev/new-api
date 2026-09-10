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
import { render, screen } from '@testing-library/react'
import i18next from 'i18next'
import { afterEach, beforeAll, describe, expect, test } from 'vitest'

import { formatLogQuota } from '@/lib/format'

import type { UsageLog } from '../../data/schema'
import type { LogOtherData } from '../../types'
import { DetailsDialog } from '../dialogs/details-dialog'

const i18nKeys = {
  'Log Details': 'Log Details',
  Consume: 'Consume',
  'Candidate Attempts': 'Candidate Attempts',
  Candidate: 'Candidate',
  'Billed quota': 'Billed quota',
  'Custom cost': 'Custom cost',
  'Billed despite being skipped': 'Billed despite being skipped',
  Tokens: 'Tokens',
  'Copy to clipboard': 'Copy to clipboard',
}

function makeLog(other: LogOtherData): UsageLog {
  return {
    id: 1,
    user_id: 1,
    created_at: 1,
    type: 9,
    content: '',
    username: 'user',
    token_name: 'token',
    model_name: 'virtual/混合链',
    model_icon: '',
    provider_icon: '',
    quota: 9000,
    prompt_tokens: 300,
    completion_tokens: 60,
    use_time: 2,
    is_stream: true,
    channel: 0,
    channel_name: '',
    token_id: 1,
    group: 'default',
    ip: '',
    other: JSON.stringify(other),
    request_id: 'req-virtual-1',
    upstream_request_id: '',
  }
}

/** 渲染指定 other 的虚拟模型日志详情弹窗，并返回可用于清理的 QueryClient 喵。 */
function renderDetails(other: LogOtherData): QueryClient {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const freshAt = Date.now() + 60_000
  queryClient.setQueryData(['status'], {}, { updatedAt: freshAt })
  queryClient.setQueryData(
    ['pricing'],
    { data: [], vendors: [] },
    { updatedAt: freshAt }
  )

  render(
    <QueryClientProvider client={queryClient}>
      <DetailsDialog
        log={makeLog(other)}
        isAdmin={false}
        isRoot={false}
        open
        onOpenChange={() => undefined}
      />
    </QueryClientProvider>
  )
  return queryClient
}

/** 去掉空白以便宽松断言，避免受 JSX 换行与空格影响喵。 */
function normalizedText(value: string | null): string {
  return (value ?? '').replaceAll(/\s/g, '')
}

describe('virtual model candidate billing display', () => {
  const queryClients: QueryClient[] = []

  beforeAll(() => {
    i18next.addResourceBundle('en', 'translation', i18nKeys)
  })

  afterEach(() => {
    for (const queryClient of queryClients) {
      queryClient.clear()
    }
    queryClients.length = 0
  })

  test('被跳过但计费的候选展示额度、跳过计费标记与用量', () => {
    queryClients.push(
      renderDetails({
        virtual_model: 'virtual/混合链',
        final_success: true,
        candidates: [
          {
            seq: 1,
            source: 'internal',
            label: 'gpt-4o',
            success: false,
            error_class: 'stalled_stream',
            quota: 1500,
            billed_on_skip: true,
            prompt_tokens: 100,
            completion_tokens: 20,
          },
          {
            seq: 2,
            source: 'custom',
            label: 'my-upstream',
            success: true,
            status_code: 200,
            quota: 7500,
            custom_cost_cents: 250000,
            prompt_tokens: 200,
            completion_tokens: 40,
          },
        ],
      })
    )

    const text = normalizedText(document.body.textContent)
    // 跳过候选的额度与标记都要出现，证明后端写的 billed_on_skip 被前端正确渲染喵。
    expect(text).toContain(
      normalizedText(`Billed quota: ${formatLogQuota(1500)}`)
    )
    expect(text).toContain(normalizedText('Billed despite being skipped'))
    // custom 候选的 cents 计费要换算成 5 位小数元展示（250000 cents = 2.5 元）喵。
    expect(text).toContain(normalizedText('Custom cost: ¥2.50000'))
  })

  test('没有计费字段的候选不渲染计费行，避免出现空标签', () => {
    queryClients.push(
      renderDetails({
        virtual_model: 'virtual/混合链',
        candidates: [
          {
            seq: 1,
            source: 'internal',
            label: 'gpt-4o',
            success: false,
            error_class: 'stream_cut',
          },
        ],
      })
    )

    expect(screen.queryByText(/Billed quota/)).toBeNull()
    expect(screen.queryByText(/Custom cost/)).toBeNull()
    expect(screen.queryByText(/Billed despite being skipped/)).toBeNull()
  })
})
