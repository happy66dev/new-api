/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { getVirtualModels } from '@/features/virtual-models/api'

export function VirtualModels() {
  const { t } = useTranslation()
  const [selectedModelId, setSelectedModelId] = useState<number | null>(null)
  const virtualModelsQuery = useQuery({
    queryKey: ['virtual-models'],
    queryFn: getVirtualModels,
  })
  const virtualModels = virtualModelsQuery.data?.data ?? []
  const selectedModel = virtualModels.find((item) => item.id === selectedModelId) ?? virtualModels[0]

  return (
    <SectionPageLayout fixedContent>
      <SectionPageLayout.Title>{t('Virtual Models')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button size='sm' disabled>{t('Create virtual model')}</Button>
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
                <Badge variant={item.enabled ? 'default' : 'secondary'}>{item.enabled ? t('Enabled') : t('Disabled')}</Badge>
              </button>
            ))}
          </div>
          <div className='min-h-0 flex-1 overflow-auto'>
            {selectedModel ? (
              <Tabs defaultValue='overview'>
                <TabsList className='max-w-full flex-wrap justify-start'>
                  <TabsTrigger value='overview'>{t('Overview')}</TabsTrigger>
                  <TabsTrigger value='candidates'>{t('Candidate Chain')}</TabsTrigger>
                  <TabsTrigger value='bindings'>{t('API Key Authorization')}</TabsTrigger>
                  <TabsTrigger value='status'>{t('Runtime Status')}</TabsTrigger>
                </TabsList>
                <TabsContent className='mt-4' value='overview'>
                  <div className='space-y-2 rounded-md border p-4'>
                    <h2 className='text-lg font-semibold'>{selectedModel.display_name}</h2>
                    <p className='text-sm text-muted-foreground'>{`virtual/${selectedModel.normalized_name}`}</p>
                    <p className='text-sm'>{t('Candidate count')}: {selectedModel.candidates?.length ?? 0}</p>
                  </div>
                </TabsContent>
                <TabsContent className='mt-4' value='candidates'>
                  <div className='space-y-2 rounded-md border p-4'>
                    {(selectedModel.candidates ?? []).map((candidate) => (
                      <div className='flex flex-wrap items-center justify-between gap-2 border-b py-3 last:border-0' key={candidate.id}>
                        <span>{candidate.group_name ? `${candidate.group_name} / ${candidate.real_model_name}` : candidate.source_type}</span>
                        <Badge variant={candidate.enabled ? 'default' : 'secondary'}>{candidate.enabled ? t('Enabled') : t('Disabled')}</Badge>
                      </div>
                    ))}
                  </div>
                </TabsContent>
                <TabsContent className='mt-4' value='bindings'>
                  <div className='rounded-md border p-4 text-sm'>{t('Bound API Keys')}: {selectedModel.binding_token_ids?.length ?? 0}</div>
                </TabsContent>
                <TabsContent className='mt-4' value='status'>
                  <div className='rounded-md border p-4 text-sm'>{t('Runtime status is available after execution is enabled')}</div>
                </TabsContent>
              </Tabs>
            ) : (
              <p className='p-4 text-sm text-muted-foreground'>{t('Select a virtual model')}</p>
            )}
          </div>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
