/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { getVirtualModelStatus, getVirtualModels } from '@/features/virtual-models/api'
import { VirtualModelDeleteDialog } from '@/features/virtual-models/components/virtual-model-dialogs'
import { VirtualModelDrawer } from '@/features/virtual-models/components/virtual-model-drawer'

export function VirtualModels() {
  const { t } = useTranslation()
  const [selectedModelId, setSelectedModelId] = useState<number | null>(null)
  const [isDrawerOpen, setIsDrawerOpen] = useState(false)
  const [isDeleteDialogOpen, setIsDeleteDialogOpen] = useState(false)
  const [editingModelId, setEditingModelId] = useState<number | null>(null)
  const virtualModelsQuery = useQuery({
    queryKey: ['virtual-models'],
    queryFn: getVirtualModels,
  })
  const virtualModels = virtualModelsQuery.data?.data ?? []
  // 列表刷新后保留仍存在的选择；已删除的选项回退到第一个模型喵。
  useEffect(() => {
    if (selectedModelId !== null && !virtualModels.some((item) => item.id === selectedModelId)) {
      setSelectedModelId(virtualModels[0]?.id ?? null)
    }
  }, [selectedModelId, virtualModels])
  const selectedModel = virtualModels.find((item) => item.id === selectedModelId) ?? virtualModels[0]
  const editingModel = editingModelId !== null ? virtualModels.find((item) => item.id === editingModelId) ?? null : null
  const virtualModelStatusQuery = useQuery({
    queryKey: ['virtual-models', selectedModel?.id, 'status'],
    queryFn: () => getVirtualModelStatus(selectedModel!.id),
    enabled: Boolean(selectedModel),
  })
  const virtualModelStatus = virtualModelStatusQuery.data?.data

  const openCreateDrawer = () => {
    // 创建模式不绑定任何已有模型喵。
    setEditingModelId(null)
    setIsDrawerOpen(true)
  }

  const openEditDrawer = () => {
    // 没有选中模型时不打开编辑抽屉喵。
    if (!selectedModel) return
    setEditingModelId(selectedModel.id)
    setIsDrawerOpen(true)
  }

  const handleDeletedModel = (deletedModelID: number) => {
    // 删除后清空对应的选中与编辑状态，避免引用不存在的模型喵。
    setSelectedModelId((currentSelectedModelID) =>
      currentSelectedModelID === deletedModelID ? null : currentSelectedModelID
    )
    setEditingModelId((currentEditingModelID) =>
      currentEditingModelID === deletedModelID ? null : currentEditingModelID
    )
  }

  return (
    <SectionPageLayout fixedContent>
      <SectionPageLayout.Title>{t('Virtual Models')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button size='sm' onClick={openCreateDrawer}>{t('Create virtual model')}</Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='flex h-full min-h-0 flex-col gap-4 lg:flex-row'>
          <div className='min-h-0 w-full overflow-auto rounded-md border lg:w-80'>
            {virtualModelsQuery.isLoading && <p className='p-4 text-sm text-muted-foreground'>{t('Loading')}</p>}
            {virtualModelsQuery.isError && <p className='p-4 text-sm text-destructive'>{t('Unable to load virtual models')}</p>}
            {!virtualModelsQuery.isLoading && !virtualModelsQuery.isError && virtualModels.length === 0 && (
              <p className='p-4 text-sm text-muted-foreground'>{t('No virtual models configured')}</p>
            )}
            {virtualModels.map((item) => (
              <button
                className='flex w-full items-center justify-between border-b px-4 py-3 text-left hover:bg-muted/50'
                key={item.id}
                onClick={() => setSelectedModelId(item.id)}
                type='button'
              >
                <span className='truncate'>{item.display_name || item.normalized_name}</span>
                <div className='flex shrink-0 items-center gap-2'>
                  <Badge variant={item.enabled ? 'default' : 'secondary'}>{item.enabled ? t('Enabled') : t('Disabled')}</Badge>
                  <Badge variant='outline'>{item.candidates?.length ?? 0}</Badge>
                </div>
              </button>
            ))}
          </div>
          <div className='min-h-0 flex-1 overflow-auto'>
            {selectedModel ? (
              <div className='space-y-4'>
                <div className='flex flex-wrap items-center justify-between gap-3 rounded-md border p-4'>
                  <div>
                    <h2 className='text-lg font-semibold'>{selectedModel.display_name}</h2>
                    <p className='text-sm text-muted-foreground'>{`virtual/${selectedModel.normalized_name}`}</p>
                  </div>
                  <div className='flex gap-2'>
                    <Button size='sm' variant='outline' onClick={openEditDrawer}>{t('Edit')}</Button>
                    <Button size='sm' variant='destructive' onClick={() => setIsDeleteDialogOpen(true)}>{t('Delete')}</Button>
                  </div>
                </div>

                <div className='space-y-3 rounded-md border p-4 text-sm'>
                  <p className='font-medium'>{t('Runtime Status')}</p>
                  {virtualModelStatusQuery.isLoading && <p className='text-muted-foreground'>{t('Loading')}</p>}
                  {virtualModelStatusQuery.isError && <p className='text-destructive'>{t('Unable to load virtual model status')}</p>}
                  {virtualModelStatus && (
                    <>
                      <p>{t('Enabled')}: {virtualModelStatus.enabled ? t('Yes') : t('No')}</p>
                      <p>{t('Candidate count')}: {virtualModelStatus.candidate_count}</p>
                      <p>{t('Enabled candidates')}: {virtualModelStatus.enabled_candidates}</p>
                    </>
                  )}
                  <Button size='sm' variant='outline' onClick={() => void virtualModelStatusQuery.refetch()}>{t('Refresh')}</Button>
                </div>
              </div>
            ) : (
              <p className='p-4 text-sm text-muted-foreground'>{t('Select a virtual model')}</p>
            )}
          </div>
        </div>
        <VirtualModelDrawer
          model={editingModel}
          open={isDrawerOpen}
          onOpenChange={setIsDrawerOpen}
        />
        <VirtualModelDeleteDialog
          model={selectedModel}
          open={isDeleteDialogOpen}
          onOpenChange={setIsDeleteDialogOpen}
          onDeleted={handleDeletedModel}
        />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
