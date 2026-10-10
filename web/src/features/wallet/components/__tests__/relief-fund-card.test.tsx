/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import type { ReactElement } from 'react'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import * as walletApi from '../../api'
import { ReliefFundCard } from '../relief-fund-card'

// mock 掉钱包 API 模块：只替换救济金两个函数，其余保持原样
vi.mock('../../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api')>()
  return {
    ...actual,
    getReliefStatus: vi.fn(),
    claimRelief: vi.fn(),
  }
})

// renderWithClient 用一个全新的 QueryClient 包裹组件，关闭重试避免测试等待
function renderWithClient(ui: ReactElement) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>{ui}</QueryClientProvider>
  )
}

describe('ReliefFundCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  test('enables the claim button when the user is eligible', async () => {
    // 可领取：eligible=true 且无冷却
    vi.mocked(walletApi.getReliefStatus).mockResolvedValue({
      success: true,
      data: {
        enabled: true,
        eligible: true,
        remaining_usd: 1800,
        grant_amount_usd: 200,
        cooldown_remaining_seconds: 0,
      },
    })
    renderWithClient(<ReliefFundCard />)
    const button = await screen.findByRole('button', {
      name: 'Claim Relief Fund',
    })
    // 可领取时按钮应可点击
    expect(button).toBeEnabled()
    // 救济金剩余按美元格式展示
    expect(screen.getByText('$1800.00')).toBeInTheDocument()
  })

  test('disables the button and shows a reason when balance is too high', async () => {
    // 不可领取：余额高于门槛
    vi.mocked(walletApi.getReliefStatus).mockResolvedValue({
      success: true,
      data: {
        enabled: true,
        eligible: false,
        reason_code: 'balance_too_high',
        remaining_usd: 1800,
        grant_amount_usd: 200,
        cooldown_remaining_seconds: 0,
      },
    })
    renderWithClient(<ReliefFundCard />)
    const button = await screen.findByRole('button', {
      name: 'Claim Relief Fund',
    })
    // 不满足条件时按钮禁用
    expect(button).toBeDisabled()
    // 展示对应的中性原因文案（测试环境 i18n 回退为英文 key）
    expect(
      screen.getByText('Your wallet balance is above the claim threshold')
    ).toBeInTheDocument()
  })

  test('renders nothing when the relief feature is disabled', async () => {
    // 功能关闭：后端只回 enabled=false
    vi.mocked(walletApi.getReliefStatus).mockResolvedValue({
      success: true,
      data: { enabled: false },
    })
    renderWithClient(<ReliefFundCard />)
    // 等待查询真正结算（mock 被调用）
    await vi.waitFor(() =>
      expect(walletApi.getReliefStatus).toHaveBeenCalled()
    )
    // 功能关闭时不渲染卡片标题，也没有领取按钮
    expect(screen.queryByText('Relief Fund')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Claim Relief Fund' })
    ).not.toBeInTheDocument()
  })
})
