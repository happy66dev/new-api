/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Pencil, Plus, Settings2, Trash2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  SideDrawerSection,
  SideDrawerSectionHeader,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { SectionPageLayout } from '@/components/layout'
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'

import {
  createUserUpstreamModel,
  deleteUserUpstreamModel,
  getUserUpstreamModels,
  updateUserUpstreamModel,
} from './api'
import type { UserUpstreamModel, UserUpstreamModelInput } from './api'

// 金额辅助函数：分与元互转，用户以元输入、后端以分存储喵。
const centsToYuan = (cents: number): string => (cents / 100).toFixed(2)
const yuanToCents = (yuan: string): number => {
  // 喵~防御：非法或空输入回退为 0，避免 NaN 写入后端喵。
  const parsed = Number.parseFloat(yuan)
  return Number.isFinite(parsed) && parsed >= 0 ? Math.round(parsed * 100) : 0
}

// UpstreamModelDrawer 提供创建/编辑用户上游模型的一体化抽屉喵。
function UpstreamModelDrawer({
  open,
  onOpenChange,
  model,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  model?: UserUpstreamModel | null
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  // 表单受控状态集中管理，字符串字段避免输入中间态被过早截断喵。
  const [normalizedName, setNormalizedName] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [baseURL, setBaseURL] = useState('')
  const [apiKey, setAPIKey] = useState('')
  const [realModelName, setRealModelName] = useState('')
  const [authStyle, setAuthStyle] = useState('bearer')
  const [modelRatio, setModelRatio] = useState('')
  const [completionRatio, setCompletionRatio] = useState('1')
  const [cacheRatio, setCacheRatio] = useState('1')
  const [cacheCreationRatio, setCacheCreationRatio] = useState('1')
  const [cacheCreation5mRatio, setCacheCreation5mRatio] = useState('1')
  const [cacheCreation1hRatio, setCacheCreation1hRatio] = useState('1')
  const [imageRatio, setImageRatio] = useState('1')
  const [audioRatio, setAudioRatio] = useState('1')
  const [audioCompletionRatio, setAudioCompletionRatio] = useState('1')
  const [balanceYuan, setBalanceYuan] = useState('0')
  const [spendLimitYuan, setSpendLimitYuan] = useState('0')
  const [upstreamRemainingYuan, setUpstreamRemainingYuan] = useState('0')
  const [shareEnabled, setShareEnabled] = useState(false)
  const [shareLimitYuan, setShareLimitYuan] = useState('0')
  const [showBalanceEnabled, setShowBalanceEnabled] = useState(false)
  const [isSaving, setIsSaving] = useState(false)

  // 打开状态或编辑对象变化时重置本地草稿，避免把上一次字段带入新模型喵。
  useEffect(() => {
    if (!open) return
    setNormalizedName(model?.normalized_name ?? '')
    setDisplayName(model?.display_name ?? '')
    setEnabled(model?.enabled ?? true)
    // 编辑模式不自动回填密钥，留空表示保留服务端密文喵。
    setBaseURL(model?.base_url ?? '')
    setAPIKey('')
    setRealModelName(model?.real_model_name ?? '')
    setAuthStyle(model?.auth_style ?? 'bearer')
    setModelRatio(model?.model_ratio ?? '')
    setCompletionRatio(model?.completion_ratio ?? '1')
    setCacheRatio(model?.cache_ratio ?? '1')
    setCacheCreationRatio(model?.cache_creation_ratio ?? '1')
    setCacheCreation5mRatio(model?.cache_creation_5m_ratio ?? '1')
    setCacheCreation1hRatio(model?.cache_creation_1h_ratio ?? '1')
    setImageRatio(model?.image_ratio ?? '1')
    setAudioRatio(model?.audio_ratio ?? '1')
    setAudioCompletionRatio(model?.audio_completion_ratio ?? '1')
    setBalanceYuan(centsToYuan(model?.balance_cents ?? 0))
    setSpendLimitYuan(centsToYuan(model?.spend_limit_cents ?? 0))
    setUpstreamRemainingYuan(centsToYuan(model?.upstream_remaining_cents ?? 0))
    setShareEnabled(model?.share_enabled ?? false)
    setShareLimitYuan(centsToYuan(model?.share_limit_cents ?? 0))
    setShowBalanceEnabled(model?.show_balance_enabled ?? false)
  }, [model, open])

  // saveModel 校验并保存用户上游模型，成功后刷新列表喵。
  const saveModel = async () => {
    // 喵~防御：创建时必须提供合法资源名，编辑模式沿用既有名称喵。
    const trimmedNormalizedName = normalizedName.trim()
    if (!model && !/^[A-Za-z0-9_-]{1,96}$/.test(trimmedNormalizedName)) {
      toast.error(t('Upstream model name can only contain letters, numbers, hyphens, and underscores'))
      return
    }
    // 喵~防御：真实模型名必填，避免无模型名的上游请求喵。
    if (!realModelName.trim() || realModelName.trim().length > 128) {
      toast.error(t('Real model name is required'))
      return
    }
    // 喵~防御：创建时上游地址与密钥必填，编辑留空表示保留原值喵。
    if (!model && (!baseURL.trim() || !apiKey.trim())) {
      toast.error(t('Upstream URL and API key are required'))
      return
    }
    // 组装创建或更新请求载荷，编辑模式携带版本号做乐观并发控制喵。
    const input: UserUpstreamModelInput = {
      normalized_name: trimmedNormalizedName,
      display_name: displayName.trim(),
      enabled,
      base_url: baseURL.trim(),
      api_key: apiKey,
      real_model_name: realModelName.trim(),
      auth_style: authStyle,
      model_ratio: modelRatio,
      completion_ratio: completionRatio,
      cache_ratio: cacheRatio,
      cache_creation_ratio: cacheCreationRatio,
      cache_creation_5m_ratio: cacheCreation5mRatio,
      cache_creation_1h_ratio: cacheCreation1hRatio,
      image_ratio: imageRatio,
      audio_ratio: audioRatio,
      audio_completion_ratio: audioCompletionRatio,
      balance_cents: yuanToCents(balanceYuan),
      spend_limit_cents: yuanToCents(spendLimitYuan),
      upstream_remaining_cents: yuanToCents(upstreamRemainingYuan),
      share_enabled: shareEnabled,
      share_limit_cents: yuanToCents(shareLimitYuan),
      show_balance_enabled: showBalanceEnabled,
      ...(model ? { version: model.version } : {}),
    }
    try {
      setIsSaving(true)
      const response = model
        ? await updateUserUpstreamModel(model.id, input)
        : await createUserUpstreamModel(input)
      // 喵~防御：业务失败必须抛出后端消息，不能把过期版本伪装成保存成功喵。
      if (!response.success) {
        throw new Error(response.message || t('Unable to save upstream model'))
      }
      toast.success(t('Upstream model saved'))
      void queryClient.invalidateQueries({ queryKey: ['upstream-models'] })
      onOpenChange(false)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Unable to save upstream model'))
    } finally {
      setIsSaving(false)
    }
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className={sideDrawerContentClassName('max-w-none sm:!max-w-[640px]')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{model ? t('Edit upstream model') : t('Create upstream model')}</SheetTitle>
          <SheetDescription>
            {model
              ? t('Update the upstream model by providing necessary info.')
              : t('Add a new upstream model by providing necessary info.')}
          </SheetDescription>
        </SheetHeader>

        <div className={sideDrawerFormClassName('gap-5')}>
          <SideDrawerSection>
            <SideDrawerSectionHeader
              title={t('Basic Information')}
              description={t('Set upstream model basic information')}
              icon={<Settings2 className='size-4' />}
              iconTone='info'
            />
            <label className='grid gap-1 text-sm font-medium'>
              {t('Upstream model name')}
              <Input
                value={normalizedName}
                onChange={(event) => setNormalizedName(event.target.value)}
                placeholder='my-upstream'
                disabled={Boolean(model) || isSaving}
              />
            </label>
            <label className='grid gap-1 text-sm font-medium'>
              {t('Display name')}
              <Input value={displayName} onChange={(event) => setDisplayName(event.target.value)} disabled={isSaving} />
            </label>
            <label className='flex items-center justify-between gap-3 text-sm'>
              <span>{t('Enabled')}</span>
              <Switch checked={enabled} onCheckedChange={setEnabled} disabled={isSaving} />
            </label>
          </SideDrawerSection>

          <SideDrawerSection>
            <SideDrawerSectionHeader
              title={t('Upstream Connection')}
              description={t('Configure the upstream endpoint and credentials')}
              icon={<Settings2 className='size-4' />}
              iconTone='info'
            />
            <label className='grid gap-1 text-sm font-medium'>
              {t('Upstream URL')}
              <Input
                value={baseURL}
                onChange={(event) => setBaseURL(event.target.value)}
                placeholder='https://api.openai.com'
                disabled={isSaving}
              />
            </label>
            <label className='grid gap-1 text-sm font-medium'>
              {t('Upstream API Key')}
              <Input
                type='password'
                value={apiKey}
                onChange={(event) => setAPIKey(event.target.value)}
                placeholder={model ? t('Leave empty to keep the current key') : ''}
                disabled={isSaving}
              />
            </label>
            <label className='grid gap-1 text-sm font-medium'>
              {t('Real model name')}
              <Input
                value={realModelName}
                onChange={(event) => setRealModelName(event.target.value)}
                placeholder='gpt-4o'
                disabled={isSaving}
              />
            </label>
            <label className='grid gap-1 text-sm font-medium'>
              {t('Authentication style')}
              <select
                className='h-9 rounded-md border border-input bg-background px-3 text-sm'
                value={authStyle}
                onChange={(event) => setAuthStyle(event.target.value)}
                disabled={isSaving}
              >
                <option value='bearer'>Bearer</option>
                <option value='api_key'>x-api-key</option>
                <option value='anthropic'>Anthropic</option>
              </select>
            </label>
          </SideDrawerSection>

          <SideDrawerSection>
            <SideDrawerSectionHeader
              title={t('Pricing Configuration')}
              description={t('RMB per million tokens using the new-api ratio system')}
              icon={<Settings2 className='size-4' />}
              iconTone='info'
            />
            <label className='grid gap-1 text-sm font-medium'>
              {t('Model ratio (RMB per million tokens)')}
              <Input inputMode='decimal' value={modelRatio} onChange={(event) => setModelRatio(event.target.value)} placeholder='18.5' disabled={isSaving} />
            </label>
            <div className='grid grid-cols-2 gap-3'>
              <label className='grid gap-1 text-sm font-medium'>
                {t('Completion ratio')}
                <Input inputMode='decimal' value={completionRatio} onChange={(event) => setCompletionRatio(event.target.value)} disabled={isSaving} />
              </label>
              <label className='grid gap-1 text-sm font-medium'>
                {t('Cache ratio')}
                <Input inputMode='decimal' value={cacheRatio} onChange={(event) => setCacheRatio(event.target.value)} disabled={isSaving} />
              </label>
              <label className='grid gap-1 text-sm font-medium'>
                {t('Cache creation ratio')}
                <Input inputMode='decimal' value={cacheCreationRatio} onChange={(event) => setCacheCreationRatio(event.target.value)} disabled={isSaving} />
              </label>
              <label className='grid gap-1 text-sm font-medium'>
                {t('Cache creation 5m ratio')}
                <Input inputMode='decimal' value={cacheCreation5mRatio} onChange={(event) => setCacheCreation5mRatio(event.target.value)} disabled={isSaving} />
              </label>
              <label className='grid gap-1 text-sm font-medium'>
                {t('Cache creation 1h ratio')}
                <Input inputMode='decimal' value={cacheCreation1hRatio} onChange={(event) => setCacheCreation1hRatio(event.target.value)} disabled={isSaving} />
              </label>
              <label className='grid gap-1 text-sm font-medium'>
                {t('Image ratio')}
                <Input inputMode='decimal' value={imageRatio} onChange={(event) => setImageRatio(event.target.value)} disabled={isSaving} />
              </label>
              <label className='grid gap-1 text-sm font-medium'>
                {t('Audio ratio')}
                <Input inputMode='decimal' value={audioRatio} onChange={(event) => setAudioRatio(event.target.value)} disabled={isSaving} />
              </label>
              <label className='grid gap-1 text-sm font-medium'>
                {t('Audio completion ratio')}
                <Input inputMode='decimal' value={audioCompletionRatio} onChange={(event) => setAudioCompletionRatio(event.target.value)} disabled={isSaving} />
              </label>
            </div>
          </SideDrawerSection>

          <SideDrawerSection>
            <SideDrawerSectionHeader
              title={t('Balance & Limits')}
              description={t('RMB amounts in yuan, independent billing system')}
              icon={<Settings2 className='size-4' />}
              iconTone='info'
            />
            <div className='grid grid-cols-2 gap-3'>
              <label className='grid gap-1 text-sm font-medium'>
                {t('Balance (yuan)')}
                <Input inputMode='decimal' value={balanceYuan} onChange={(event) => setBalanceYuan(event.target.value)} disabled={isSaving} />
              </label>
              <label className='grid gap-1 text-sm font-medium'>
                {t('Spend limit (yuan)')}
                <Input inputMode='decimal' value={spendLimitYuan} onChange={(event) => setSpendLimitYuan(event.target.value)} disabled={isSaving} />
              </label>
              <label className='grid gap-1 text-sm font-medium'>
                {t('Upstream remaining (yuan)')}
                <Input inputMode='decimal' value={upstreamRemainingYuan} onChange={(event) => setUpstreamRemainingYuan(event.target.value)} disabled={isSaving} />
              </label>
            </div>
            <label className='flex items-center justify-between gap-3 text-sm'>
              <span>{t('Show balance on model square')}</span>
              <Switch checked={showBalanceEnabled} onCheckedChange={setShowBalanceEnabled} disabled={isSaving} />
            </label>
          </SideDrawerSection>

          <SideDrawerSection>
            <SideDrawerSectionHeader
              title={t('Sharing')}
              description={t('Share this upstream model with all users in the user-shared group')}
              icon={<Settings2 className='size-4' />}
              iconTone='info'
            />
            <label className='flex items-center justify-between gap-3 text-sm'>
              <span>{t('Share to all users')}</span>
              <Switch checked={shareEnabled} onCheckedChange={setShareEnabled} disabled={isSaving} />
            </label>
            <label className='grid gap-1 text-sm font-medium'>
              {t('Share limit (yuan)')}
              <Input inputMode='decimal' value={shareLimitYuan} onChange={(event) => setShareLimitYuan(event.target.value)} disabled={isSaving || !shareEnabled} />
            </label>
          </SideDrawerSection>
        </div>

        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' className='w-full sm:w-auto' />}>
            {t('Close')}
          </SheetClose>
          <Button type='button' onClick={() => void saveModel()} disabled={isSaving} className='w-full sm:w-auto'>
            {isSaving ? t('Saving') : model ? t('Save changes') : t('Create upstream model')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

// UpstreamModels 提供用户上游模型的管理列表页喵。
export function UpstreamModels() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [isDrawerOpen, setIsDrawerOpen] = useState(false)
  const [editingModel, setEditingModel] = useState<UserUpstreamModel | null>(null)
  const [deletingModel, setDeletingModel] = useState<UserUpstreamModel | null>(null)
  const upstreamModelsQuery = useQuery({
    queryKey: ['upstream-models'],
    queryFn: getUserUpstreamModels,
  })
  const upstreamModels = upstreamModelsQuery.data?.data ?? []

  // handleDelete 执行删除并刷新列表喵。
  const handleDelete = async () => {
    // 喵~防御：没有待删除模型时不执行喵。
    if (!deletingModel) return
    try {
      const response = await deleteUserUpstreamModel(deletingModel.id, { version: deletingModel.version })
      // 喵~防御：业务失败必须展示后端消息喵。
      if (!response.success) {
        throw new Error(response.message || t('Unable to delete upstream model'))
      }
      toast.success(t('Upstream model deleted'))
      setDeletingModel(null)
      void queryClient.invalidateQueries({ queryKey: ['upstream-models'] })
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Unable to delete upstream model'))
    }
  }

  return (
    <SectionPageLayout fixedContent>
      <SectionPageLayout.Title>{t('Upstream Models')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          size='sm'
          onClick={() => {
            setEditingModel(null)
            setIsDrawerOpen(true)
          }}
        >
          <Plus className='size-4' />
          {t('Create upstream model')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='overflow-auto rounded-md border'>
          {upstreamModelsQuery.isLoading && <p className='p-4 text-sm text-muted-foreground'>{t('Loading')}</p>}
          {upstreamModelsQuery.isError && <p className='p-4 text-sm text-destructive'>{t('Unable to load upstream models')}</p>}
          {!upstreamModelsQuery.isLoading && !upstreamModelsQuery.isError && upstreamModels.length === 0 && (
            <p className='p-4 text-sm text-muted-foreground'>{t('No upstream models configured')}</p>
          )}
          {upstreamModels.map((item) => (
            <div
              className='flex flex-wrap items-center justify-between gap-3 border-b px-4 py-3 last:border-b-0'
              key={item.id}
            >
              <div className='min-w-0'>
                <div className='flex items-center gap-2'>
                  <span className='truncate font-medium'>{item.display_name || item.normalized_name}</span>
                  <Badge variant={item.enabled ? 'default' : 'secondary'}>{item.enabled ? t('Enabled') : t('Disabled')}</Badge>
                  {item.share_enabled && <Badge variant='outline'>{t('Shared')}</Badge>}
                </div>
                <div className='text-muted-foreground mt-0.5 truncate text-xs'>
                  upstream/{item.normalized_name} · {item.real_model_name}
                  {item.base_url ? ` · ${item.base_url}` : ''}
                </div>
                <div className='text-muted-foreground mt-1 text-xs'>
                  {t('Balance')}: {centsToYuan(item.balance_cents)} ¥ · {t('Limit')}: {centsToYuan(item.spend_limit_cents)} ¥
                </div>
              </div>
              <div className='flex shrink-0 items-center gap-2'>
                <Button
                  size='sm'
                  variant='outline'
                  onClick={() => {
                    setEditingModel(item)
                    setIsDrawerOpen(true)
                  }}
                >
                  <Pencil className='size-3.5' />
                  {t('Edit')}
                </Button>
                <Button size='sm' variant='outline' onClick={() => setDeletingModel(item)}>
                  <Trash2 className='size-3.5' />
                  {t('Delete')}
                </Button>
              </div>
            </div>
          ))}
        </div>
      </SectionPageLayout.Content>

      <UpstreamModelDrawer
        open={isDrawerOpen}
        onOpenChange={setIsDrawerOpen}
        model={editingModel}
      />

      <AlertDialog open={Boolean(deletingModel)} onOpenChange={(open) => !open && setDeletingModel(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Delete upstream model')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t('Are you sure you want to delete this upstream model?')}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('Cancel')}</AlertDialogCancel>
            <AlertDialogAction onClick={() => void handleDelete()}>{t('Delete')}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </SectionPageLayout>
  )
}
