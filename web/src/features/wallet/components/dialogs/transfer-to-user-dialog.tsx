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
import { Loader2, Search, X } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  formatQuota,
  getEditableQuotaStep,
  parseQuotaFromDollars,
  quotaUnitsToEditableAmount,
} from '@/lib/format'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import { searchUserTransferTargets, transferQuotaToUser } from '../../api'
import type { TransferTarget } from '../../types'

interface TransferToUserDialogProps {
  /** 弹窗是否打开 */
  open: boolean
  /** 父级控制开关回调 */
  onOpenChange: (open: boolean) => void
  /** 当前用户主余额（内部额度），用于限制最大可转金额 */
  availableQuota: number
  /** 转账成功后通知父级刷新钱包余额 */
  onSuccess: () => void
}

type RecipientSearchState = 'idle' | 'loading' | 'done' | 'error'

/**
 * 用户间主余额转账对话框：先按“用户ID/用户名”实时搜索并预览收款人，
 * 再输入按当前显示币种换算的金额，确认后调用转账接口喵。
 */
export function TransferToUserDialog(props: TransferToUserDialogProps) {
  const { t } = useTranslation()
  const currencyConfig = useSystemConfigStore((state) => state.config.currency)

  // 与后端最小转账额度保持一致：内部额度 = quotaPerUnit / 100（约 0.01 美元）喵
  const quotaPerUnit =
    currencyConfig.quotaPerUnit > 0
      ? currencyConfig.quotaPerUnit
      : DEFAULT_CURRENCY_CONFIG.quotaPerUnit
  const minimumQuota = Math.max(1, Math.floor(quotaPerUnit / 100))
  const minimumAmount = quotaUnitsToEditableAmount(minimumQuota)
  const maximumAmount = quotaUnitsToEditableAmount(props.availableQuota)

  // 收款人搜索输入与选中结果
  const [keyword, setKeyword] = useState('')
  const [recipients, setRecipients] = useState<TransferTarget[]>([])
  const [searchState, setSearchState] = useState<RecipientSearchState>('idle')
  const [selected, setSelected] = useState<TransferTarget | null>(null)
  // 转账金额（按当前显示币种输入）
  const [amount, setAmount] = useState('')
  const [transferring, setTransferring] = useState(false)

  // 打开弹窗时重置全部状态，避免上次残留喵
  useEffect(() => {
    if (props.open) {
      setKeyword('')
      setRecipients([])
      setSearchState('idle')
      setSelected(null)
      setAmount('')
      setTransferring(false)
    }
  }, [props.open])

  // 关键词变化后防抖实时搜索收款人（用户名/显示名模糊，数字关键词同时按用户ID精确匹配）喵
  useEffect(() => {
    const trimmed = keyword.trim()
    if (!props.open || !trimmed) {
      setRecipients([])
      setSearchState('idle')
      return
    }

    setSearchState('loading')
    const timer = setTimeout(async () => {
      try {
        const response = await searchUserTransferTargets(trimmed)
        if (response.success || response.message === 'success') {
          setRecipients(response.data ?? [])
          setSearchState('done')
        } else {
          setRecipients([])
          setSearchState('error')
        }
      } catch {
        setRecipients([])
        setSearchState('error')
      }
    }, 300)
    return () => clearTimeout(timer)
  }, [keyword, props.open])

  // 用户选择的收款人一旦变化，就清空关键词输入，收起搜索下拉喵
  const handleSelectRecipient = (target: TransferTarget) => {
    setSelected(target)
    setKeyword('')
  }

  const handleClearRecipient = () => {
    setSelected(null)
  }

  const amountValue = Number(amount)
  const transferQuota = parseQuotaFromDollars(amountValue)
  const canTransfer =
    selected != null &&
    Number.isFinite(amountValue) &&
    amountValue > 0 &&
    transferQuota >= minimumQuota &&
    transferQuota <= props.availableQuota
  // 主余额不足最小转账额时，即使填了也无法发起喵
  const insufficientForMinimum = props.availableQuota < minimumQuota

  const handleConfirm = async () => {
    if (!canTransfer || selected == null) return
    setTransferring(true)
    try {
      const response = await transferQuotaToUser({
        to_user_id: selected.id,
        quota: transferQuota,
      })
      if (response.success || response.message === 'success') {
        toast.success(response.message || t('Transfer successful'))
        props.onSuccess()
        props.onOpenChange(false)
      } else {
        toast.error(response.message || t('Transfer failed'))
      }
    } catch {
      toast.error(t('Transfer failed'))
    } finally {
      setTransferring(false)
    }
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Transfer to User')}
      description={t('Send main balance quota to another user account')}
      contentClassName='max-sm:w-[calc(100vw-1.5rem)] sm:max-w-md'
      titleClassName='text-xl font-semibold'
      footerClassName='grid grid-cols-2 gap-2 sm:flex'
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button
            variant='outline'
            onClick={() => props.onOpenChange(false)}
            disabled={transferring}
          >
            {t('Cancel')}
          </Button>
          <Button
            onClick={handleConfirm}
            disabled={transferring || !canTransfer}
          >
            {transferring && <Loader2 className='mr-2 h-4 w-4 animate-spin' />}
            {t('Transfer')}
          </Button>
        </>
      }
    >
      <div className='space-y-4 py-3 sm:space-y-6 sm:py-4'>
        {/* 收款人选择：实时搜索 + 下拉预览 */}
        <div className='space-y-3'>
          <Label
            htmlFor='transfer-recipient'
            className='text-muted-foreground text-xs font-medium tracking-wider uppercase'
          >
            {t('Recipient')}
          </Label>
          {selected == null ? (
            <div className='relative'>
              <Search className='text-muted-foreground pointer-events-none absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2' />
              <Input
                id='transfer-recipient'
                type='text'
                value={keyword}
                onChange={(event) => {
                  setKeyword(event.target.value)
                  handleClearRecipient()
                }}
                placeholder={t('Search by user ID or username')}
                className='pl-9'
                autoComplete='off'
              />
              {searchState === 'done' && keyword.trim() !== '' && (
                <ul className='bg-popover absolute z-10 mt-1 max-h-48 w-full overflow-y-auto rounded-md border shadow-md'>
                  {recipients.length === 0 ? (
                    <li className='text-muted-foreground px-3 py-2 text-sm'>
                      {t('No matching users found')}
                    </li>
                  ) : (
                    recipients.map((target) => (
                      <li key={target.id}>
                        <button
                          type='button'
                          className='hover:bg-accent flex w-full items-center gap-2 px-3 py-2 text-left text-sm'
                          onClick={() => handleSelectRecipient(target)}
                        >
                          <span className='font-mono text-xs opacity-60'>
                            #{target.id}
                          </span>
                          <span className='truncate font-medium'>
                            {target.display_name || target.username}
                          </span>
                          <span className='text-muted-foreground truncate text-xs'>
                            @{target.username}
                          </span>
                        </button>
                      </li>
                    ))
                  )}
                </ul>
              )}
              {searchState === 'error' && (
                <p className='text-destructive mt-1 text-xs'>
                  {t('No matching users found')}
                </p>
              )}
            </div>
          ) : (
            <div className='bg-muted/40 flex items-center justify-between gap-2 rounded-md border px-3 py-2'>
              <div className='flex min-w-0 items-center gap-2 text-sm'>
                <span className='font-mono text-xs opacity-60'>
                  #{selected.id}
                </span>
                <span className='truncate font-medium'>
                  {selected.display_name || selected.username}
                </span>
                <span className='text-muted-foreground truncate text-xs'>
                  @{selected.username}
                </span>
              </div>
              <button
                type='button'
                aria-label={t('Remove')}
                className='text-muted-foreground hover:text-foreground shrink-0'
                onClick={handleClearRecipient}
              >
                <X className='h-4 w-4' />
              </button>
            </div>
          )}
        </div>

        {/* 转账金额：按当前显示币种输入，前端换算成内部额度提交喵 */}
        <div className='space-y-3'>
          <Label
            htmlFor='transfer-user-amount'
            className='text-muted-foreground text-xs font-medium tracking-wider uppercase'
          >
            {t('Transfer Amount')}
          </Label>
          <Input
            id='transfer-user-amount'
            type='number'
            value={amount}
            onChange={(event) => setAmount(event.target.value)}
            min={minimumAmount}
            max={maximumAmount}
            step={getEditableQuotaStep()}
            placeholder={t('Minimum: {{amount}}', {
              amount: quotaUnitsToEditableAmount(minimumQuota),
            })}
            className='font-mono text-lg'
          />
        </div>

        <div className='text-muted-foreground space-y-1 text-xs'>
          <div className='flex items-center justify-between gap-2'>
            <span>{t('Available Balance')}</span>
            <span className='font-mono font-medium'>
              {formatQuota(props.availableQuota)}
            </span>
          </div>
          <div className='flex items-center justify-between gap-2'>
            <span>{t('Minimum:')}</span>
            <span className='font-mono font-medium'>
              {formatQuota(minimumQuota)}
            </span>
          </div>
          {insufficientForMinimum && (
            <p className='text-destructive'>
              {t('Insufficient balance for the minimum transfer')}
            </p>
          )}
        </div>
      </div>
    </Dialog>
  )
}
