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
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { getVirtualModelStatus, getVirtualModels } from '@/features/virtual-models/api'
import { VirtualModelBindingsEditor } from '@/features/virtual-models/components/virtual-model-bindings-editor'
import { VirtualModelCandidatesEditor } from '@/features/virtual-models/components/virtual-model-candidates-editor'
import { VirtualModelDeleteDialog } from '@/features/virtual-models/components/virtual-model-dialogs'
import { VirtualModelDrawer } from '@/features/virtual-models/components/virtual-model-drawer'
import { VirtualModelGlobalFailureRulesEditor } from '@/features/virtual-models/components/virtual-model-global-failure-rules-editor'
import { VirtualModelOverviewStatus } from '@/features/virtual-models/components/virtual-model-overview-status'

export function VirtualModels() {
  const { t } = useTranslation()
  const [selectedModelId, setSelectedModelId] = useState<number | null>(null)
  const [isDrawerOpen, setIsDrawerOpen] = useState(false)
  const [isDeleteDialogOpen, setIsDeleteDialogOpen] = useState(false)
  const [editingModelId, setEditingModelId] = useState<number | null>(null)
  // activeTab 记录当前选项卡；Overview 候选摘要行可跳转到候选链选项卡喵。
  const [activeTab, setActiveTab] = useState<string>('overview')
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

  // refreshSelectedModel 触发列表刷新，供各选项卡保存后同步最新模型版本喵。
  const refreshSelectedModel = () => {
    void virtualModelsQuery.refetch()
    void virtualModelStatusQuery.refetch()
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
              <Tabs
                value={activeTab}
                onValueChange={(value) => setActiveTab(String(value))}
              >
                <TabsList className='max-w-full flex-wrap justify-start'>
                  <TabsTrigger value='overview'>{t('Overview')}</TabsTrigger>
                  <TabsTrigger value='candidates'>{t('Candidate Chain')}</TabsTrigger>
                  <TabsTrigger value='failure-rules'>{t('Failure Rules')}</TabsTrigger>
                  <TabsTrigger value='bindings'>{t('API Key Authorization')}</TabsTrigger>
                  <TabsTrigger value='status'>{t('Runtime Status')}</TabsTrigger>
                </TabsList>
                <TabsContent className='mt-4' value='overview'>
                  <div className='space-y-2 rounded-md border p-4'>
                    <div className='flex flex-wrap items-center justify-between gap-3'>
                      <div>
                        <h2 className='text-lg font-semibold'>{selectedModel.display_name}</h2>
                        <p className='text-sm text-muted-foreground'>{`virtual/${selectedModel.normalized_name}`}</p>
                      </div>
                      <div className='flex gap-2'>
                        <Button size='sm' variant='outline' onClick={openEditDrawer}>{t('Edit')}</Button>
                        <Button size='sm' variant='destructive' onClick={() => setIsDeleteDialogOpen(true)}>{t('Delete')}</Button>
                      </div>
                    </div>
                    <p className='text-sm'>{t('Candidate count')}: {selectedModel.candidates?.length ?? 0}</p>
                  </div>
                  {/* 基本信息下方的整体状态卡片：指标 + 24h 柱状图 + 候选摘要喵。 */}
                  <div className='mt-4'>
                    <VirtualModelOverviewStatus
                      status={virtualModelStatus}
                      loading={virtualModelStatusQuery.isLoading}
                      error={virtualModelStatusQuery.isError}
                      onNavigateToCandidates={() => setActiveTab('candidates')}
                      onRefresh={() => void virtualModelStatusQuery.refetch()}
                    />
                  </div>
                </TabsContent>
                <TabsContent className='mt-4' value='candidates'>
                  <VirtualModelCandidatesEditor model={selectedModel} onSaved={refreshSelectedModel} />
                </TabsContent>
                <TabsContent className='mt-4' value='failure-rules'>
                  <VirtualModelGlobalFailureRulesEditor model={selectedModel} onSaved={refreshSelectedModel} />
                </TabsContent>
                <TabsContent className='mt-4' value='bindings'>
                  <VirtualModelBindingsEditor model={selectedModel} onSaved={refreshSelectedModel} />
                </TabsContent>
                <TabsContent className='mt-4' value='status'>
                  <div className='space-y-3 rounded-md border p-4 text-sm'>
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
                </TabsContent>
              </Tabs>
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
