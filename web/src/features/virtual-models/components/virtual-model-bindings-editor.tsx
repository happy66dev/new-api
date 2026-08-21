/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { useMutation, useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { MultiSelect } from '@/components/multi-select'
import { Button } from '@/components/ui/button'
import { getApiKeys } from '@/features/keys/api'

import type { VirtualModel } from '../api'
import { replaceVirtualModelBindings } from '../api'

// API_KEY_QUERY_SIZE 覆盖用户常用的 API Key 数量，同时避免控制面首次加载无限量资源喵。
const API_KEY_QUERY_SIZE = 100

// VirtualModelBindingsEditor 编辑当前用户 API Key 与虚拟模型的显式授权关系喵。
export function VirtualModelBindingsEditor({
  model,
  onSaved,
}: {
  model: VirtualModel
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const [selectedTokenIDs, setSelectedTokenIDs] = useState<string[]>([])
  const apiKeysQuery = useQuery({
    queryKey: ['api-keys', 'virtual-model-bindings'],
    queryFn: () => getApiKeys({ p: 1, size: API_KEY_QUERY_SIZE }),
  })
  const [isSaving, setIsSaving] = useState(false)

  // 服务端响应变化时同步已绑定 token，避免保存后仍显示过期选择喵。
  useEffect(() => {
    setSelectedTokenIDs((model.binding_token_ids ?? []).map(String))
  }, [model.binding_token_ids, model.id, model.version])

  const apiKeyOptions = (apiKeysQuery.data?.data?.items ?? []).map((apiKey) => ({
    label: apiKey.name || `${t('API Key')} #${apiKey.id}`,
    value: String(apiKey.id),
  }))

  const saveBindings = async () => {
    // 喵~防御：拒绝 NaN、零值与重复 Token ID，避免把畸形绑定提交到后端喵。
    const tokenIDs = selectedTokenIDs.map(Number)
    if (tokenIDs.some((tokenID) => !Number.isInteger(tokenID) || tokenID <= 0)) {
      toast.error(t('Selected API Key is invalid'))
      return
    }
    if (new Set(tokenIDs).size !== tokenIDs.length) {
      toast.error(t('Selected API Keys must be unique'))
      return
    }
    try {
      setIsSaving(true)
      const response = await replaceVirtualModelBindings(model.id, {
        token_ids: tokenIDs,
        version: model.version,
      })
      if (!response.success) {
        throw new Error(response.message || t('Unable to save API Key authorization'))
      }
      toast.success(t('API Key authorization saved'))
      onSaved()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Unable to save API Key authorization'))
    } finally {
      setIsSaving(false)
    }
  }

  return (
    <div className='space-y-4 rounded-md border p-4'>
      <div>
        <h3 className='font-medium'>{t('API Key Authorization')}</h3>
        <p className='text-muted-foreground text-sm'>{t('Only explicitly authorized API Keys can call this virtual model.')}</p>
      </div>
      {apiKeysQuery.isLoading && <p className='text-muted-foreground text-sm'>{t('Loading')}</p>}
      {apiKeysQuery.isError && <p className='text-destructive text-sm'>{t('Unable to load API Keys')}</p>}
      {!apiKeysQuery.isLoading && !apiKeysQuery.isError && (
        <MultiSelect
          options={apiKeyOptions}
          selected={selectedTokenIDs}
          onChange={setSelectedTokenIDs}
          placeholder={t('Select API Keys')}
          disabled={isSaving}
          maxVisibleChips={5}
        />
      )}
      <p className='text-muted-foreground text-xs'>{t('Saving an empty selection removes all API Key authorizations.')}</p>
      <div className='flex justify-end'>
        <Button type='button' onClick={() => void saveBindings()} disabled={isSaving || apiKeysQuery.isLoading || apiKeysQuery.isError}>
          {isSaving ? t('Saving') : t('Save API Key authorization')}
        </Button>
      </div>
    </div>
  )
}
