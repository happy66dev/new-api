/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { useMutation, useQueryClient } from '@tanstack/react-query'
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

import type { VirtualModel } from '../api'
import { deleteVirtualModel } from '../api'

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
