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
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

import type { VirtualModelAnomaly } from '../api'
import { useVirtualModelAnomalies } from '../hooks/use-virtual-model-anomalies'

// anomalyReasonTranslationKey 把稳定原因码映射到 i18n 文案 key 喵。
// 映射不到的未知原因码返回空串，组件回退展示后端中文兜底说明喵。
function anomalyReasonTranslationKey(reasonCode: string): string {
  switch (reasonCode) {
    case 'group_not_configured':
      return 'Candidate group is not configured'
    case 'auto_group_not_supported':
      return 'Auto group is not supported for virtual models'
    case 'group_not_accessible':
      return 'The candidate group is no longer accessible to your account'
    case 'model_not_available':
      return 'The candidate model is no longer available in its group'
    case 'upstream_feature_disabled':
      return 'User upstream models are disabled by the administrator'
    case 'referenced_upstream_missing':
      return 'The referenced user upstream model no longer exists'
    default:
      return ''
  }
}

// anomalyCandidateLabel 生成候选的展示名：内部候选显示 分组/模型，自定义候选显示模型或路由目标喵。
function anomalyCandidateLabel(anomaly: VirtualModelAnomaly): string {
  if (anomaly.group_name) {
    return `${anomaly.group_name}/${anomaly.real_model_name ?? ''}`
  }
  return anomaly.real_model_name || String(anomaly.candidate_id)
}

// VirtualModelAnomalyDialog 在进入虚拟模型页后展示被动变化异常提醒弹层喵。
// 弹层列出全部未读异常与原因；用户关闭时把这些异常全部标记已读喵。
// 喵~防御：同一会话内轮询到的新异常不再自动重弹，避免频繁打断用户操作，只更新红点喵。
export function VirtualModelAnomalyDialog() {
  const { t } = useTranslation()
  const { unreadAnomalies, hasUnread, markAllUnreadAsRead } =
    useVirtualModelAnomalies()
  const [open, setOpen] = useState(false)
  // sessionShownRef 记录本会话是否已经弹过一次，保证只自动弹出一次喵。
  const sessionShownRef = useRef(false)

  // 首次有未读异常时自动弹出提醒喵。
  useEffect(() => {
    if (hasUnread && !sessionShownRef.current) {
      sessionShownRef.current = true
      setOpen(true)
    }
  }, [hasUnread])

  // handleClose 关闭弹层并把这些异常标记为已读喵。
  const handleClose = () => {
    setOpen(false)
    markAllUnreadAsRead()
  }

  // 通过遮罩点击或 ESC 关闭同样走 handleClose，保证已读语义一致喵。
  const handleOpenChange = (nextOpen: boolean) => {
    if (!nextOpen) {
      handleClose()
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className='max-h-[70vh] overflow-y-auto'>
        <DialogHeader>
          <DialogTitle>{t('Virtual model candidate anomalies')}</DialogTitle>
          <DialogDescription>
            {t(
              'Some candidates of your virtual models are no longer available. Review the reasons below.'
            )}
          </DialogDescription>
        </DialogHeader>
        <ul className='divide-y'>
          {unreadAnomalies.map((anomaly) => {
            // 原因码已知时用 i18n 文案，未知时回退后端中文兜底说明喵。
            const reasonKey = anomalyReasonTranslationKey(anomaly.reason_code)
            const reasonText = reasonKey ? t(reasonKey) : anomaly.reason_message
            return (
              <li
                className='flex items-start gap-3 py-3'
                key={anomaly.candidate_id}
              >
                <span
                  className='mt-1.5 size-2 shrink-0 rounded-full bg-red-500'
                  aria-hidden='true'
                />
                <div className='min-w-0'>
                  <p className='truncate text-sm font-medium'>
                    {anomaly.virtual_model_name} ·{' '}
                    {anomalyCandidateLabel(anomaly)}
                  </p>
                  <p className='text-muted-foreground text-xs'>{reasonText}</p>
                </div>
              </li>
            )
          })}
        </ul>
        <DialogFooter>
          <Button onClick={handleClose}>{t('Got it')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
