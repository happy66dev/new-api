/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test } from 'vitest'

import { HelpHint } from '../help-hint'

// renderHelpHint 渲染一个带短提示与完整教程的帮助图标，供各用例复用喵。
function renderHelpHint() {
  render(
    <HelpHint hint='Short hint'>
      <p>Full detail</p>
    </HelpHint>
  )
  // 返回无障碍名称固定的帮助按钮，后续用例直接操作它喵。
  return screen.getByRole('button', { name: 'Show help' })
}

describe('HelpHint', () => {
  test('renders an accessible help button', () => {
    // 帮助入口必须是可聚焦按钮，并提供稳定的无障碍名称喵。
    expect(renderHelpHint()).toBeInTheDocument()
  })

  test('shows the short hint on hover', async () => {
    // 使用真实用户事件模拟悬停，验证短提示会在延迟后出现喵。
    const user = userEvent.setup()
    const helpButton = renderHelpHint()
    await user.hover(helpButton)
    expect(await screen.findByText('Short hint')).toBeInTheDocument()
  })

  test('opens the full tutorial on click', async () => {
    // 点击帮助按钮后必须弹出完整教程正文喵。
    const user = userEvent.setup()
    const helpButton = renderHelpHint()
    await user.click(helpButton)
    expect(await screen.findByText('Full detail')).toBeInTheDocument()
  })

  test('does not focus a sibling input when clicked inside a label', async () => {
    // 喵~防御：帮助按钮位于 label 内时，点击不得把焦点转移给相邻输入框喵。
    const user = userEvent.setup()
    render(
      <label>
        Field label
        <HelpHint hint='Short hint'>
          <p>Full detail</p>
        </HelpHint>
        <input aria-label='field input' />
      </label>
    )
    const fieldInput = screen.getByLabelText('field input')
    await user.click(screen.getByRole('button', { name: 'Show help' }))
    expect(fieldInput).not.toHaveFocus()
  })
})
