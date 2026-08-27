/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as
 published by the Free Software Foundation, either version 3 of the
 License, or (at your option) any later version.
*/
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, test, vi } from 'vitest'

import type { VirtualModelStatus } from '../api'
import { VirtualModelOverviewStatus } from '../components/virtual-model-overview-status'

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
    request_count: 12,
    availability_24h: [100, 95, 90],
    last_at: Math.floor(Date.now() / 1000) - 60,
    last_success: true,
    last_latency_ms: 120,
    last_error: '',
    candidates: [
      {
        candidate_id: 42,
        label: 'gpt-4.1',
        availability: 95,
        avg_latency_ms: 180,
        request_count: 12,
        last_at: Math.floor(Date.now() / 1000) - 60,
        last_success: true,
        last_error: '',
      },
    ],
    ...overrides,
  }
}

describe('VirtualModelOverviewStatus', () => {
  test('shows a loading placeholder while the status is being fetched', () => {
    render(
      <VirtualModelOverviewStatus
        status={null}
        loading
        onNavigateToCandidates={() => undefined}
        onRefresh={() => undefined}
      />
    )
    expect(screen.getByText('Loading')).toBeInTheDocument()
  })

  test('shows an empty-data placeholder when there is no request sample yet', () => {
    render(
      <VirtualModelOverviewStatus
        status={makeStatus({ request_count: 0 })}
        onNavigateToCandidates={() => undefined}
        onRefresh={() => undefined}
      />
    )
    expect(screen.getByText('No status data yet')).toBeInTheDocument()
  })

  test('renders overall metrics, last call, and candidate summary rows', () => {
    render(
      <VirtualModelOverviewStatus
        status={makeStatus()}
        onNavigateToCandidates={() => undefined}
        onRefresh={() => undefined}
      />
    )
    // 三项核心指标标签喵。
    expect(screen.getByText('Availability')).toBeInTheDocument()
    expect(screen.getByText('Average latency')).toBeInTheDocument()
    expect(screen.getByText('Request Count')).toBeInTheDocument()
    // 可用性数值 95.00% 出现（指标区）喵。
    expect(screen.getAllByText('95.00%').length).toBeGreaterThanOrEqual(1)
    // 最近一次调用行喵。
    expect(screen.getByText(/Last call/)).toBeInTheDocument()
    // 候选摘要行展示候选真实模型名喵。
    expect(screen.getByText('gpt-4.1')).toBeInTheDocument()
  })

  test('fires the refresh callback from the header button', () => {
    const onRefresh = vi.fn()
    render(
      <VirtualModelOverviewStatus
        status={makeStatus()}
        onNavigateToCandidates={() => undefined}
        onRefresh={onRefresh}
      />
    )
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(onRefresh).toHaveBeenCalledTimes(1)
  })

  test('jumps to the candidates tab when a candidate summary row is clicked', () => {
    const onNavigateToCandidates = vi.fn()
    render(
      <VirtualModelOverviewStatus
        status={makeStatus()}
        onNavigateToCandidates={onNavigateToCandidates}
        onRefresh={() => undefined}
      />
    )
    fireEvent.click(screen.getByText('gpt-4.1'))
    expect(onNavigateToCandidates).toHaveBeenCalledTimes(1)
  })
})
