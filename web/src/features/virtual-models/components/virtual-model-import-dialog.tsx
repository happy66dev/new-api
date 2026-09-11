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
import {
  describeShareCodeError,
  normalizeShareCodeSuggestedName,
} from '../lib/share-code'
import { VirtualModelShareSkippedList } from './virtual-model-share-skipped-list'

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
  // normalizedName 是导入后对外使用的模型标识（virtual/ 后面那段），由用户自己填写喵。
  const [normalizedName, setNormalizedName] = useState('')
  // displayName 是导入后可修改的显示名，预检通过后按快照显示名预填，但仍需用户确认喵。
  const [displayName, setDisplayName] = useState('')
  const [preview, setPreview] = useState<VirtualModelShareImportPreview | null>(
    null
  )

  // resetDraft 清空弹窗内的临时状态，避免上一次的码与预检结果残留到下次打开喵。
  const resetDraft = () => {
    setCode('')
    setNormalizedName('')
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
      // 预检成功才预填两个名字，避免用上一次的码覆盖用户正在编辑的名称喵。
      // 模型标识只在还空着时预填一个建议值，用户仍然可以改；显示名恒按快照刷新喵。
      const suggestedName = normalizeShareCodeSuggestedName(
        previewData.display_name
      )
      if (suggestedName !== '') {
        setNormalizedName((currentName) =>
          currentName.trim() === '' ? suggestedName : currentName
        )
      }
      setDisplayName(previewData.display_name)
    },
    onError: (error) => {
      // 预检失败要清掉旧结果，否则界面会显示与当前码不符的过时清单喵。
      setPreview(null)
      // 后端对不存在／已删除／已过期／次数用尽给了不同错误码，这里按码取本地化文案喵。
      toast.error(
        describeShareCodeError(
          error,
          t('Unable to read the share code'),
          (key) => t(key)
        )
      )
    },
  })

  const importMutation = useMutation({
    mutationFn: async () => {
      // 喵~防御：没有预检结果时拒绝导入，保证"先预检再导入"的流程不被绕过喵。
      if (!preview) throw new Error(t('Run the check first'))
      const trimmedNormalizedName = normalizedName.trim()
      // 喵~防御：模型标识必填，空值时本地就拦下，省掉一次注定失败的往返喵。
      if (trimmedNormalizedName === '') {
        throw new Error(t('Enter a model ID first'))
      }
      const trimmedDisplayName = displayName.trim()
      // 喵~防御：显示名同样是必填项喵。
      if (trimmedDisplayName === '') {
        throw new Error(t('Enter a display name first'))
      }
      const response = await importVirtualModelShareCode({
        code: code.trim(),
        normalized_name: trimmedNormalizedName,
        display_name: trimmedDisplayName,
      })
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Unable to import the plan'))
      }
      return response.data
    },
    onSuccess: (result) => {
      toast.success(
        t('Imported {{count}} candidate(s)', {
          count: result.imported_candidate_count,
        })
      )
      void queryClient.invalidateQueries({ queryKey: ['virtual-models'] })
      resetDraft()
      onOpenChange(false)
    },
    onError: (error) => {
      // 导入阶段可能撞上重名或候选全部不可用，同样按后端错误码取本地化文案喵。
      toast.error(
        describeShareCodeError(error, t('Unable to import the plan'), (key) =>
          t(key)
        )
      )
    },
  })

  const handleOpenChange = (nextOpen: boolean) => {
    // 关闭时清空草稿，保证下次打开是干净的导入流程喵。
    if (!nextOpen) resetDraft()
    onOpenChange(nextOpen)
  }

  // canConfirmImport 表示两个必填名字都已填写，可以提交导入喵。
  const canConfirmImport =
    normalizedName.trim() !== '' && displayName.trim() !== ''

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className='max-h-[85vh] overflow-auto sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>{t('Import plan')}</DialogTitle>
          <DialogDescription>
            {t(
              'Paste a share code to copy that plan. Imported models stay disabled until you review them.'
            )}
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
                {t(
                  'This plan has {{total}} candidate(s); {{importable}} can be imported into your account.',
                  {
                    total: preview.candidate_count,
                    importable: preview.importable_candidate_count,
                  }
                )}
              </AlertDescription>
            </Alert>
            <label className='grid gap-1 text-sm font-medium'>
              {t('Model ID')}
              <Input
                onChange={(event) => setNormalizedName(event.target.value)}
                placeholder={t('my-model-id')}
                value={normalizedName}
              />
              <span className='text-muted-foreground text-xs'>
                {/* 前缀是固定的，用户只需要填后面那段，这里把最终调用名展示出来避免歧义喵。 */}
                {t(
                  'The model will be available as virtual/{{name}}. Only ASCII letters, digits, hyphens and underscores are allowed, and the name must be free.',
                  { name: normalizedName.trim() || t('my-model-id') }
                )}
              </span>
            </label>
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
              // 喵~防御：两个名字都是必填项，缺任意一个就禁用按钮，避免用户白点一次喵。
              disabled={!canConfirmImport || importMutation.isPending}
              onClick={() => importMutation.mutate()}
            >
              {importMutation.isPending ? t('Importing') : t('Confirm import')}
            </Button>
          ) : (
            <Button
              disabled={precheckMutation.isPending || code.trim() === ''}
              onClick={() => precheckMutation.mutate()}
            >
              {precheckMutation.isPending
                ? t('Checking')
                : t('Check share code')}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
