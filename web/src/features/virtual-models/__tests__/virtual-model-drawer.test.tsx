/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as
 published by the Free Software Foundation, either version 3 of the
 License, or (at your option) any later version.
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, test } from 'vitest'

import { VirtualModelDrawer } from '../components/virtual-model-drawer'

// renderDrawer 用真实 QueryClient 包装抽屉，供各用例使用喵。
function renderDrawer() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <VirtualModelDrawer open onOpenChange={() => undefined} model={null} />
    </QueryClientProvider>
  )
  return queryClient
}

describe('VirtualModelDrawer', () => {
  test('basic tab shows only name fields and the enabled switch', () => {
    renderDrawer()
    // 基本信息选项卡只保留名称信息与是否启用喵。
    expect(screen.getByText('Virtual model name')).toBeInTheDocument()
    expect(screen.getByText('Display name')).toBeInTheDocument()
    expect(screen.getByText('Enabled')).toBeInTheDocument()
    // 目标模式字段不出现在基本信息选项卡中喵。
    expect(screen.queryByText('Total timeout seconds')).not.toBeInTheDocument()
    expect(screen.queryByText('Maximum loop rounds')).not.toBeInTheDocument()
  })

  test('switching to the target tab reveals candidate loop config', () => {
    renderDrawer()
    // 切换到目标模式选项卡喵。
    fireEvent.click(screen.getByText('Target Mode'))
    expect(screen.getByText('Enable candidate loop')).toBeInTheDocument()
    expect(screen.getByText('Total timeout seconds')).toBeInTheDocument()
    expect(screen.getByText('Maximum loop rounds')).toBeInTheDocument()
  })
})
