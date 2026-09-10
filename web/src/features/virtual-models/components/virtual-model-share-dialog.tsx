/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'

import {
  createVirtualModelShareCode,
  getVirtualModelShareCodes,
  revokeVirtualModelShareCode,
  type VirtualModel,
  type VirtualModelShareCodeCreated,
  type VirtualModelShareCodeSummary,
} from '../api'
import {
  extractShareCodeErrorMessage,
  resolveShareCodeStatus,
  shareCodeStatusTextKeys,
} from '../lib/share-code'

// ShareCodeRow 渲染单枚分享码及其状态与撤销入口喵。
function ShareCodeRow({
  shareCode,
  nowSeconds,
  isRevoking,
  onRevoke,
}: {
  shareCode: VirtualModelShareCodeSummary
  nowSeconds: number
  isRevoking: boolean
  onRevoke: (shareCodeID: number) => void
}) {
  const { t } = useTranslation()
  const status = resolveShareCodeStatus(shareCode, nowSeconds)
  return (
    <div className='flex items-center justify-between gap-3 border-b py-2 last:border-b-0'>
      <div className='min-w-0'>
        <p className='truncate font-mono text-xs'>{shareCode.code}</p>
        <p className='text-muted-foreground text-xs'>
          {t('Imported {{count}} times', { count: shareCode.import_count })}
        </p>
      </div>
      <div className='flex shrink-0 items-center gap-2'>
        <Badge variant={status === 'active' ? 'default' : 'secondary'}>
          {t(shareCodeStatusTextKeys[status])}
        </Badge>
        <Button
          disabled={status !== 'active' || isRevoking}
          onClick={() => onRevoke(shareCode.id)}
          size='sm'
          variant='outline'
        >
          {t('Revoke')}
        </Button>
      </div>
    </div>
  )
}

// VirtualModelShareDialog 生成、查看并撤销某个虚拟模型的分享码喵。
// 分享的是脱敏方案快照：内部候选原样，自定义候选只保留上游地址，API Key 永不出本机喵。
export function VirtualModelShareDialog({
  model,
  open,
  onOpenChange,
}: {
  model: VirtualModel | null
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { copyToClipboard } = useCopyToClipboard()
  // createdShareCode 保存本次刚生成的分享码，供用户马上复制喵。
  const [createdShareCode, setCreatedShareCode] =
    useState<VirtualModelShareCodeCreated | null>(null)

  const shareCodesQuery = useQuery({
    queryKey: ['virtual-models', 'share-codes'],
    queryFn: getVirtualModelShareCodes,
    // 弹窗关闭时不请求，避免在虚拟模型页面里平白多一次轮询喵。
    enabled: open,
  })
  const shareCodes = shareCodesQuery.data?.data?.share_codes ?? []

  const createMutation = useMutation({
    mutationFn: async () => {
      // 喵~防御：没有选中模型时不产生请求，避免用零值编号创建无主分享码喵。
      if (!model) throw new Error(t('Select a virtual model first'))
      const response = await createVirtualModelShareCode(model.id, {})
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Unable to create share code'))
      }
      return response.data
    },
    onSuccess: (created) => {
      setCreatedShareCode(created)
      void queryClient.invalidateQueries({
        queryKey: ['virtual-models', 'share-codes'],
      })
    },
    onError: (error) => {
      toast.error(
        extractShareCodeErrorMessage(error, t('Unable to create share code'))
      )
    },
  })

  const revokeMutation = useMutation({
    mutationFn: async (shareCodeID: number) => {
      const response = await revokeVirtualModelShareCode(shareCodeID)
      if (!response.success) {
        throw new Error(response.message || t('Unable to revoke share code'))
      }
    },
    onSuccess: () => {
      toast.success(t('Share code revoked'))
      void queryClient.invalidateQueries({
        queryKey: ['virtual-models', 'share-codes'],
      })
    },
    onError: (error) => {
      toast.error(
        extractShareCodeErrorMessage(error, t('Unable to revoke share code'))
      )
    },
  })

  // nowSeconds 取打开弹窗那一刻的时间，用于判定过期；状态随列表刷新自然更新喵。
  const nowSeconds = Math.floor(Date.now() / 1000)

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='max-h-[85vh] overflow-auto sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>{t('Share plan')}</DialogTitle>
          <DialogDescription>
            {t(
              'Share codes copy this plan to another account. They never contain your API Key.'
            )}
          </DialogDescription>
        </DialogHeader>

        {model && (
          <Badge variant='secondary'>{`virtual/${model.normalized_name}`}</Badge>
        )}

        {/* 隐私告知：地址明文会随分享码发出，主人需要知道发出去的到底是什么喵。 */}
        <Alert>
          <AlertDescription>
            {t(
              'A share code keeps internal candidates as-is and keeps the upstream address of custom candidates. The receiver still has to fill in their own API Key.'
            )}
          </AlertDescription>
        </Alert>

        {createdShareCode && (
          <div className='space-y-2 rounded-md border p-3'>
            <p className='text-sm font-medium'>{t('New share code')}</p>
            <Input
              readOnly
              value={createdShareCode.code}
              onFocus={(event) => event.target.select()}
            />
            <div className='flex items-center justify-between gap-3'>
              <Button
                onClick={() => void copyToClipboard(createdShareCode.code)}
                size='sm'
                variant='outline'
              >
                {t('Copy')}
              </Button>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Internal candidates: {{internal}}, custom candidates: {{custom}}',
                  {
                    internal: createdShareCode.internal_candidate_count,
                    custom: createdShareCode.custom_candidate_count,
                  }
                )}
              </p>
            </div>
            {createdShareCode.omitted_reference_candidates > 0 && (
              <p className='text-muted-foreground text-xs'>
                {t(
                  '{{count}} candidate(s) that reference your own upstream models were left out and cannot be shared.',
                  {
                    count: createdShareCode.omitted_reference_candidates,
                  }
                )}
              </p>
            )}
          </div>
        )}

        <div className='space-y-1'>
          <p className='text-sm font-medium'>{t('Existing share codes')}</p>
          {shareCodesQuery.isLoading && (
            <p className='text-muted-foreground text-xs'>{t('Loading')}</p>
          )}
          {!shareCodesQuery.isLoading && shareCodes.length === 0 && (
            <p className='text-muted-foreground text-xs'>
              {t('No share codes yet')}
            </p>
          )}
          {shareCodes.map((shareCode) => (
            <ShareCodeRow
              key={shareCode.id}
              shareCode={shareCode}
              nowSeconds={nowSeconds}
              isRevoking={revokeMutation.isPending}
              onRevoke={(shareCodeID) => revokeMutation.mutate(shareCodeID)}
            />
          ))}
        </div>

        <DialogFooter>
          <Button onClick={() => onOpenChange(false)} variant='outline'>
            {t('Close')}
          </Button>
          <Button
            disabled={!model || createMutation.isPending}
            onClick={() => createMutation.mutate()}
          >
            {createMutation.isPending
              ? t('Generating')
              : t('Generate share code')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
