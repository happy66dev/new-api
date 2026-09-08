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
import {
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { describe, expect, test, vi } from 'vitest'

import { transferQuotaToUser } from '../../../api'
import { TransferToUserDialog } from '../transfer-to-user-dialog'

vi.mock('../../../api', () => ({
  searchUserTransferTargets: vi.fn(async () => ({
    success: true,
    data: [{ id: 1, username: 'alice', display_name: 'Alice' }],
  })),
  transferQuotaToUser: vi.fn(async () => ({ success: true, message: '' })),
}))

const mockedTransfer = vi.mocked(transferQuotaToUser)

function setupDialog() {
  const onOpenChange = vi.fn()
  const onSuccess = vi.fn()
  render(
    <TransferToUserDialog
      open
      onOpenChange={onOpenChange}
      availableQuota={100000}
      onSuccess={onSuccess}
    />
  )
  return { onOpenChange, onSuccess }
}

describe('transfer to user dialog', () => {
  test('keeps Transfer disabled until a recipient is chosen and a valid amount is entered', async () => {
    setupDialog()

    // 初始：没有收款人也没有金额，确认按钮必须禁用喵
    expect(screen.getByRole('button', { name: 'Transfer' })).toBeDisabled()

    // 输入收款人关键词触发实时搜索，选中 Alice 喵
    fireEvent.change(screen.getByLabelText('Recipient'), {
      target: { value: 'alice' },
    })
    const recipientOption = await screen.findByRole('button', {
      name: /alice/i,
    })
    fireEvent.click(recipientOption)

    // 未填金额前仍禁用喵
    expect(screen.getByRole('button', { name: 'Transfer' })).toBeDisabled()

    // 金额低于最小转账额时仍禁用（0.01 美元以下为 5000 额度 = 最小额度）喵
    fireEvent.change(screen.getByLabelText('Transfer Amount'), {
      target: { value: '0.005' },
    })
    expect(screen.getByRole('button', { name: 'Transfer' })).toBeDisabled()

    // 达到合法金额后启用喵
    fireEvent.change(screen.getByLabelText('Transfer Amount'), {
      target: { value: '0.02' },
    })
    expect(screen.getByRole('button', { name: 'Transfer' })).toBeEnabled()
  })

  test('submits transfer to the selected recipient and refreshes on success', async () => {
    const { onOpenChange, onSuccess } = setupDialog()

    // 选中搜索到的 Alice 收款人喵
    fireEvent.change(screen.getByLabelText('Recipient'), {
      target: { value: 'alice' },
    })
    const recipientOption = await screen.findByRole('button', {
      name: /alice/i,
    })
    fireEvent.click(recipientOption)

    // 输入 $0.02 → 内部 10000 额度（默认 1 美元 = 500000 额度）喵
    fireEvent.change(screen.getByLabelText('Transfer Amount'), {
      target: { value: '0.02' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Transfer' }))

    await waitFor(() => {
      expect(mockedTransfer).toHaveBeenCalledTimes(1)
      expect(mockedTransfer).toHaveBeenCalledWith({
        to_user_id: 1,
        quota: 10000,
      })
    })
    expect(onSuccess).toHaveBeenCalledTimes(1)
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })
})
