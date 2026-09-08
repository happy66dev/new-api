/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/

import { useQueryClient } from '@tanstack/react-query'
import { Ban } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { useIsAdmin } from '@/hooks/use-admin'

import { stopSharingUserUpstreamModel } from '../api'
import type { PricingModel } from '../types'

export interface UserSharedStopPanelProps {
  model: PricingModel
  /** 停止共享成功后的回调：用于让宿主（详情页/抽屉）收起当前视图喵。 */
  onStopSharingSuccess?: () => void
}

/**
 * UserSharedStopPanel 是模型广场用户共享模型详情「概览」里的管理员停共享面板喵。
 * 仅对已登录且 role >= 管理员 的用户可见，且只作用于 owner_by === 'user-shared' 的条目喵。
 */
export function UserSharedStopPanel(props: UserSharedStopPanelProps) {
  const { t } = useTranslation()
  const isAdmin = useIsAdmin()
  const queryClient = useQueryClient()
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [isStopping, setIsStopping] = useState(false)

  // 属主用户 id：用户共享条目由后端固定填充，缺失时视为无法定位而不渲染面板喵。
  const ownerUserId = props.model.share_owner_user_id

  // 喵~防御：非管理员、非用户共享条目、或缺少属主 id 时整个面板都不渲染喵。
  if (!isAdmin || props.model.owner_by !== 'user-shared' || !ownerUserId) {
    return null
  }

  // 执行停止共享：成功后刷新广场数据并通知宿主收起视图喵。
  const handleConfirmStop = async () => {
    setConfirmOpen(false)
    setIsStopping(true)
    try {
      const result = await stopSharingUserUpstreamModel(
        ownerUserId,
        props.model.model_name
      )
      // 喵~防御：HTTP 200 但 success=false 的业务失败已由 http-client 拦截器统一提示，这里只在真正成功时收尾喵。
      if (result?.success) {
        toast.success(t('Stopped sharing this shared model'))
        props.onStopSharingSuccess?.()
        void queryClient.invalidateQueries({ queryKey: ['pricing'] })
      }
    } catch {
      // 喵~防御：非 2xx（模型已停止共享/不存在等）已由 http-client 拦截器统一提示，无需在此重复弹窗喵。
    } finally {
      setIsStopping(false)
    }
  }

  return (
    <section className='space-y-3 rounded-xl border border-destructive/40 bg-destructive/5 p-4'>
      <div className='flex items-center justify-between gap-3'>
        <div className='flex min-w-0 items-start gap-2'>
          <Ban
            className='text-destructive mt-0.5 size-4 shrink-0'
            aria-hidden='true'
          />
          <div className='min-w-0 space-y-1'>
            <p className='text-foreground text-sm font-semibold'>
              {t('Shared model management')}
            </p>
            <p className='text-muted-foreground text-xs leading-relaxed'>
              {t(
                'Admin-only: stopping this shared model makes it unavailable to all other users. The owner keeps private access and can re-enable sharing later.'
              )}
            </p>
          </div>
        </div>
        <Button
          variant='destructive'
          size='sm'
          className='shrink-0'
          disabled={isStopping}
          onClick={() => setConfirmOpen(true)}
        >
          {t('Stop sharing')}
        </Button>
      </div>

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t('Stop sharing this shared model?')}
        desc={t(
          'Stopping this shared model immediately makes it unavailable to other users. The owner keeps private use and can share the model again later. This does not delete the model.'
        )}
        destructive
        isLoading={isStopping}
        confirmText={t('Yes, stop sharing')}
        handleConfirm={() => void handleConfirmStop()}
      />
    </section>
  )
}
