/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as
 published by the Free Software Foundation, either version 3 of the
 License, or (at your option) any later version.
*/
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, test } from 'vitest'

import { EntityStatusDot, type EntityStatusSummary } from '../entity-status-dot'

// makeSummary 构造一份默认状态摘要，供各用例按需覆盖喵。
function makeSummary(
  overrides: Partial<EntityStatusSummary> = {}
): EntityStatusSummary {
  return {
    availability: 100,
    avg_latency_ms: 250,
    request_count: 12,
    availability_24h: [100, 100, 80],
    last_at: Math.floor(Date.now() / 1000) - 60,
    last_success: true,
    last_latency_ms: 120,
    last_error: '',
    ...overrides,
  }
}

describe('EntityStatusDot', () => {
  test('shows a gray dot and a dash placeholder when there is no data yet', () => {
    render(<EntityStatusDot summary={null} />)
    const dot = screen.getByTestId('entity-status-dot')
    // 无数据时圆点为灰色喵。
    expect(dot.className).toContain('bg-muted-foreground/40')
    // 可用性占位符为破折号喵。
    expect(screen.getByText('—')).toBeInTheDocument()
  })

  test('shows a pulsing gray dot while loading', () => {
    render(<EntityStatusDot summary={null} loading />)
    const dot = screen.getByTestId('entity-status-dot')
    expect(dot.className).toContain('animate-pulse')
    expect(screen.getByText('—')).toBeInTheDocument()
  })

  test('shows an emerald dot with a formatted percentage at 100% availability', () => {
    render(<EntityStatusDot summary={makeSummary({ availability: 100 })} />)
    const dot = screen.getByTestId('entity-status-dot')
    expect(dot.className).toContain('bg-emerald-500')
    expect(screen.getByText('100.00%')).toBeInTheDocument()
  })

  test('shows an amber dot when availability is in the warning band', () => {
    render(<EntityStatusDot summary={makeSummary({ availability: 75 })} />)
    const dot = screen.getByTestId('entity-status-dot')
    expect(dot.className).toContain('bg-amber-500')
  })

  test('shows a red dot when availability is below the warning threshold', () => {
    render(<EntityStatusDot summary={makeSummary({ availability: 50 })} />)
    const dot = screen.getByTestId('entity-status-dot')
    expect(dot.className).toContain('bg-red-500')
  })

  test('opens a details popover with metrics and last call info when 24h data exists', async () => {
    render(
      <EntityStatusDot
        summary={makeSummary({
          availability: 95,
          request_count: 12,
          last_success: true,
        })}
      />
    )
    fireEvent.click(screen.getByLabelText('Status'))
    // 弹层出现请求数指标标签喵。
    expect(await screen.findByText('Request Count')).toBeInTheDocument()
    // 最近一次调用行出现喵。
    expect(screen.getByText(/Last call/)).toBeInTheDocument()
    // 可用性在圆点旁与弹层指标区各出现一次喵。
    expect(screen.getAllByText('95.00%').length).toBeGreaterThanOrEqual(2)
  })

  test('switches between self and shared dimensions when shared data exists', async () => {
    const sharedSummary = makeSummary({
      availability: 60,
      request_count: 5,
      last_success: false,
      last_error: 'rate_limited',
    })
    render(
      <EntityStatusDot
        summary={makeSummary({ availability: 100 })}
        shared={sharedSummary}
      />
    )
    fireEvent.click(screen.getByLabelText('Status'))
    // 弹层出现自用/共享维度切换按钮喵。
    const sharedButton = await screen.findByRole('button', { name: 'Shared' })
    fireEvent.click(sharedButton)
    await waitFor(() => {
      // 切换后可用性切换为共享维度值喵。
      expect(screen.getAllByText('60.00%').length).toBeGreaterThanOrEqual(2)
    })
    // 共享维度的错误明细可见喵。
    expect(screen.getByText('rate_limited')).toBeInTheDocument()
  })
})
