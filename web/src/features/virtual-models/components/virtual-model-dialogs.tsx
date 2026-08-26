/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'

import type { VirtualModel, VirtualModelInput } from '../api'
import {
  createVirtualModel,
  deleteVirtualModel,
  updateVirtualModel,
} from '../api'

// VirtualModelMutateDialog 提供创建和编辑用户私有虚拟模型的最小安全闭环喵。
export function VirtualModelMutateDialog({
  model,
  open,
  onOpenChange,
}: {
  model?: VirtualModel
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [normalizedName, setNormalizedName] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [loopEnabled, setLoopEnabled] = useState(false)
  const [totalTimeoutSeconds, setTotalTimeoutSeconds] = useState('120')
  const [maxLoopRounds, setMaxLoopRounds] = useState('1')

  // 弹窗打开或编辑对象变化时重置本地草稿，避免把上一次模型字段带入新模型喵。
  useEffect(() => {
    if (!open) return
    setNormalizedName(model?.normalized_name ?? '')
    setDisplayName(model?.display_name ?? '')
    setEnabled(model?.enabled ?? true)
    setLoopEnabled(model?.loop_enabled ?? false)
    setTotalTimeoutSeconds(String(model?.total_timeout_seconds ?? 120))
    setMaxLoopRounds(String(model?.max_loop_rounds ?? 1))
  }, [model, open])

  const saveMutation = useMutation({
    mutationFn: async () => {
      const totalTimeout = Number(totalTimeoutSeconds)
      const maxRounds = Number(maxLoopRounds)
      // 喵~防御：在发起请求前拒绝空名称、非整数和超出后端约束的输入喵。
      if (!/^[A-Za-z0-9_-]{1,96}$/.test(normalizedName.trim())) {
        throw new Error(t('Virtual model name can only contain letters, numbers, hyphens, and underscores'))
      }
      if (!displayName.trim() || displayName.trim().length > 128) {
        throw new Error(t('Virtual model display name is required'))
      }
      if (!Number.isInteger(totalTimeout) || totalTimeout < 1 || totalTimeout > 3600) {
        throw new Error(t('Total timeout must be between 1 and 3600 seconds'))
      }
      if (!Number.isInteger(maxRounds) || maxRounds < 1 || maxRounds > 100) {
        throw new Error(t('Maximum loop rounds must be between 1 and 100'))
      }
      const input: VirtualModelInput = {
        normalized_name: normalizedName.trim(),
        display_name: displayName.trim(),
        enabled,
        loop_enabled: loopEnabled,
        total_timeout_seconds: totalTimeout,
        max_loop_rounds: maxRounds,
        ...(model ? { version: model.version } : {}),
      }
      const response = model
        ? await updateVirtualModel(model.id, input)
        : await createVirtualModel(input)
      if (!response.success) {
        throw new Error(response.message || t('Unable to save virtual model'))
      }
      return response.data
    },
    onSuccess: () => {
      toast.success(t('Virtual model saved'))
      void queryClient.invalidateQueries({ queryKey: ['virtual-models'] })
      onOpenChange(false)
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Unable to save virtual model'))
    },
  })

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{model ? t('Edit virtual model') : t('Create virtual model')}</AlertDialogTitle>
          <AlertDialogDescription>{t('Changes take effect for new requests immediately.')}</AlertDialogDescription>
        </AlertDialogHeader>
        <div className='grid gap-3 py-2'>
          <label className='grid gap-1 text-sm font-medium'>
            {t('Virtual model name')}
            <Input
              value={normalizedName}
              onChange={(event) => setNormalizedName(event.target.value)}
              placeholder='research-route'
              disabled={Boolean(model) || saveMutation.isPending}
            />
          </label>
          <label className='grid gap-1 text-sm font-medium'>
            {t('Display name')}
            <Input
              value={displayName}
              onChange={(event) => setDisplayName(event.target.value)}
              disabled={saveMutation.isPending}
            />
          </label>
          <div className='grid grid-cols-2 gap-3'>
            <label className='grid gap-1 text-sm font-medium'>
              {t('Total timeout seconds')}
              <Input
                inputMode='numeric'
                value={totalTimeoutSeconds}
                onChange={(event) => setTotalTimeoutSeconds(event.target.value)}
                disabled={saveMutation.isPending}
              />
            </label>
            <label className='grid gap-1 text-sm font-medium'>
              {t('Maximum loop rounds')}
              <Input
                inputMode='numeric'
                value={maxLoopRounds}
                onChange={(event) => setMaxLoopRounds(event.target.value)}
                disabled={saveMutation.isPending}
              />
            </label>
          </div>
          <label className='flex items-center justify-between gap-3 text-sm'>
            <span>{t('Enabled')}</span>
            <Switch checked={enabled} onCheckedChange={setEnabled} disabled={saveMutation.isPending} />
          </label>
          <label className='flex items-center justify-between gap-3 text-sm'>
            <span>{t('Enable candidate loop')}</span>
            <Switch checked={loopEnabled} onCheckedChange={setLoopEnabled} disabled={saveMutation.isPending} />
          </label>
        </div>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={saveMutation.isPending}>{t('Cancel')}</AlertDialogCancel>
          <AlertDialogAction onClick={() => saveMutation.mutate()} disabled={saveMutation.isPending}>
            {saveMutation.isPending ? t('Saving') : t('Save')}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

// VirtualModelDeleteDialog 在永久删除模型及加密凭据前提供显式二次确认喵。
export function VirtualModelDeleteDialog({
  model,
  open,
  onOpenChange,
  onDeleted,
}: {
  model?: VirtualModel
  open: boolean
  onOpenChange: (open: boolean) => void
  onDeleted?: (modelID: number) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const deleteMutation = useMutation({
    mutationFn: async () => {
      // 喵~防御：无选中模型时不产生删除请求，避免误删或路径零值错误喵。
      if (!model) throw new Error(t('Select a virtual model first'))
      const response = await deleteVirtualModel(model.id, { version: model.version })
      if (!response.success) throw new Error(response.message || t('Unable to delete virtual model'))
    },
    onSuccess: () => {
      if (model) onDeleted?.(model.id)
      toast.success(t('Virtual model deleted'))
      void queryClient.invalidateQueries({ queryKey: ['virtual-models'] })
      onOpenChange(false)
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Unable to delete virtual model'))
    },
  })

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t('Delete virtual model')}</AlertDialogTitle>
          <AlertDialogDescription>
            {t('This permanently deletes the model, candidates, rules, API Key bindings, and encrypted credentials.')}
          </AlertDialogDescription>
        </AlertDialogHeader>
        {model && <Badge variant='secondary'>{`virtual/${model.normalized_name}`}</Badge>}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={deleteMutation.isPending}>{t('Cancel')}</AlertDialogCancel>
          <AlertDialogAction variant='destructive' onClick={() => deleteMutation.mutate()} disabled={deleteMutation.isPending}>
            {deleteMutation.isPending ? t('Deleting') : t('Delete')}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
