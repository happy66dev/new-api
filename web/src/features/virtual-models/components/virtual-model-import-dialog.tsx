/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription } from '@/components/ui/alert'
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
import { Textarea } from '@/components/ui/textarea'

import {
  importVirtualModelShareCode,
  precheckVirtualModelShareImport,
  type VirtualModelShareImportPreview,
} from '../api'
import { VirtualModelShareSkippedList } from './virtual-model-share-skipped-list'
import { extractShareCodeErrorMessage } from '../lib/share-code'

// VirtualModelImportDialog 用分享码把别人的虚拟模型方案复制一份到当前账号喵。
// 流程固定为"先预检、再确认导入"：预检只读不落库，用户看到跳过清单后再决定是否导入喵。
export function VirtualModelImportDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [code, setCode] = useState('')
  // displayName 是导入后可修改的显示名，预检通过后按快照显示名预填喵。
  const [displayName, setDisplayName] = useState('')
  const [preview, setPreview] = useState<VirtualModelShareImportPreview | null>(null)

  // resetDraft 清空弹窗内的临时状态，避免上一次的码与预检结果残留到下次打开喵。
  const resetDraft = () => {
    setCode('')
    setDisplayName('')
    setPreview(null)
  }

  const precheckMutation = useMutation({
    mutationFn: async () => {
      const trimmedCode = code.trim()
      // 喵~防御：空分享码不发请求，避免无意义往返与后端空值分支喵。
      if (trimmedCode === '') throw new Error(t('Enter a share code first'))
      const response = await precheckVirtualModelShareImport(trimmedCode)
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Unable to read the share code'))
      }
      return response.data
    },
    onSuccess: (previewData) => {
      setPreview(previewData)
      // 预检成功才预填显示名，避免用上一次的码覆盖用户正在编辑的名称喵。
      setDisplayName(previewData.display_name)
    },
    onError: (error) => {
      // 预检失败要清掉旧结果，否则界面会显示与当前码不符的过时清单喵。
      setPreview(null)
      toast.error(extractShareCodeErrorMessage(error, t('Unable to read the share code')))
    },
  })

  const importMutation = useMutation({
    mutationFn: async () => {
      // 喵~防御：没有预检结果时拒绝导入，保证"先预检再导入"的流程不被绕过喵。
      if (!preview) throw new Error(t('Run the check first'))
      const response = await importVirtualModelShareCode({
        code: code.trim(),
        display_name: displayName.trim() || undefined,
      })
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Unable to import the plan'))
      }
      return response.data
    },
    onSuccess: (result) => {
      toast.success(
        t('Imported {{count}} candidate(s)', { count: result.imported_candidate_count })
      )
      void queryClient.invalidateQueries({ queryKey: ['virtual-models'] })
      resetDraft()
      onOpenChange(false)
    },
    onError: (error) => {
      toast.error(extractShareCodeErrorMessage(error, t('Unable to import the plan')))
    },
  })

  const handleOpenChange = (nextOpen: boolean) => {
    // 关闭时清空草稿，保证下次打开是干净的导入流程喵。
    if (!nextOpen) resetDraft()
    onOpenChange(nextOpen)
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className='max-h-[85vh] overflow-auto sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>{t('Import plan')}</DialogTitle>
          <DialogDescription>
            {t('Paste a share code to copy that plan. Imported models stay disabled until you review them.')}
          </DialogDescription>
        </DialogHeader>

        <label className='grid gap-1 text-sm font-medium'>
          {t('Share code')}
          <Textarea
            className='font-mono text-xs'
            onChange={(event) => {
              setCode(event.target.value)
              // 码变了旧预检结果就作废，防止拿 A 的清单去导 B 的码喵。
              setPreview(null)
            }}
            rows={3}
            value={code}
          />
        </label>

        {preview && (
          <>
            <Alert>
              <AlertDescription>
                {t('This plan has {{total}} candidate(s); {{importable}} can be imported into your account.', {
                  total: preview.candidate_count,
                  importable: preview.importable_candidate_count,
                })}
              </AlertDescription>
            </Alert>
            <label className='grid gap-1 text-sm font-medium'>
              {t('Display name')}
              <Input
                onChange={(event) => setDisplayName(event.target.value)}
                value={displayName}
              />
            </label>
            <VirtualModelShareSkippedList
              skippedCandidates={preview.skipped_candidates}
              warnings={preview.warnings}
            />
          </>
        )}

        <DialogFooter>
          <Button onClick={() => handleOpenChange(false)} variant='outline'>
            {t('Cancel')}
          </Button>
          {preview ? (
            <Button
              disabled={importMutation.isPending}
              onClick={() => importMutation.mutate()}
            >
              {importMutation.isPending ? t('Importing') : t('Confirm import')}
            </Button>
          ) : (
            <Button
              disabled={precheckMutation.isPending || code.trim() === ''}
              onClick={() => precheckMutation.mutate()}
            >
              {precheckMutation.isPending ? t('Checking') : t('Check share code')}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
