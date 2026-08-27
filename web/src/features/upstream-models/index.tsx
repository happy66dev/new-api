/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Pencil, Plus, Settings2, Target, Trash2, Users, Wand2, Wallet } from 'lucide-react'
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
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
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
import { Textarea } from '@/components/ui/textarea'
import { formatTimestampToDate } from '@/lib/format'
import { EntityStatusDot, type EntityStatusSummary } from '@/features/status-check/entity-status-dot'
import { EntityPerformanceDrawer } from '@/features/status-check/entity-performance-drawer'

import {
  clearUpstreamModelUserUsage,
  createUserUpstreamModel,
  deleteUserUpstreamModel,
  getUpstreamModelStatus,
  getUpstreamModelUserUsage,
  getUserUpstreamModels,
  syncUserUpstreamModelAvailable,
  syncUserUpstreamModelBalance,
  updateUserUpstreamModel,
} from './api'
import type {
  UpstreamModelSharedStatus,
  UpstreamModelStatus,
  UpstreamModelUserUsage,
  UserUpstreamModel,
  UserUpstreamModelInput,
} from './api'

// 金额辅助函数：分与元互转，用户以元输入、后端以分存储喵。
const centsToYuan = (cents: number): string => (cents / 100).toFixed(2)
const yuanToCents = (yuan: string): number => {
  // 喵~防御：非法或空输入回退为 0，避免 NaN 写入后端喵。
  const parsed = Number.parseFloat(yuan)
  return Number.isFinite(parsed) && parsed >= 0 ? Math.round(parsed * 100) : 0
}

// 自定义请求头预设：统一用户标识 UA，防止上游 UA 判断拦截请求喵。
// "*" 标记表示该配置对全部请求生效，本身不是真实请求头喵。
const CUSTOM_HEADERS_PRESET = JSON.stringify(
  {
    '*': true,
    'User-Agent': 'Kilo-Code/7.3.50 ai-sdk/provider-utils/4.0.27 runtime/bun/1.3.14',
  },
  null,
  2
)

// 字段替换预设：把思考强度 max 映射为上游更青睐的 xhigh，只作用于请求参数喵。
const FIELD_REPLACEMENTS_PRESET = JSON.stringify(
  { reasoning_effort: { max: 'xhigh' } },
  null,
  2
)

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
  // 模型简介独立于显示名，用于模型广场卡片展示喵。
  const [description, setDescription] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [baseURL, setBaseURL] = useState('')
  const [apiKey, setAPIKey] = useState('')
  const [realModelName, setRealModelName] = useState('')
  const [authStyle, setAuthStyle] = useState('bearer')
  const [modelRatio, setModelRatio] = useState('1')
  const [completionRatio, setCompletionRatio] = useState('1')
  const [cacheRatio, setCacheRatio] = useState('1')
  const [cacheCreationRatio, setCacheCreationRatio] = useState('1')
  const [cacheCreation5mRatio, setCacheCreation5mRatio] = useState('1')
  const [cacheCreation1hRatio, setCacheCreation1hRatio] = useState('1')
  const [imageRatio, setImageRatio] = useState('1')
  const [audioRatio, setAudioRatio] = useState('1')
  const [audioCompletionRatio, setAudioCompletionRatio] = useState('1')
  const [balanceYuan, setBalanceYuan] = useState('0')
  const [availableYuan, setAvailableYuan] = useState('0')
  const [shareEnabled, setShareEnabled] = useState(false)
  const [shareLimitYuan, setShareLimitYuan] = useState('0')
  // 共享名单模式：all=不限制、whitelist=白名单制、blacklist=黑名单制，用户显式二选一喵。
  const [shareListMode, setShareListMode] = useState<'all' | 'whitelist' | 'blacklist'>('all')
  // shareList 是当前模式下生效的逗号分隔用户 id 名单喵。
  const [shareList, setShareList] = useState('')
  const [showBalanceEnabled, setShowBalanceEnabled] = useState(false)
  // 请求定制：自定义请求头与字段替换的 JSON 文本，保存时校验合法性喵。
  const [customHeadersJSON, setCustomHeadersJSON] = useState('')
  const [fieldReplacementsJSON, setFieldReplacementsJSON] = useState('')
  // timeoutSeconds 自用调用超时（秒），空串表示使用默认 60 秒喵。
  const [timeoutSeconds, setTimeoutSeconds] = useState('')
  const [isSaving, setIsSaving] = useState(false)

  // 打开状态或编辑对象变化时重置本地草稿，避免把上一次字段带入新模型喵。
  useEffect(() => {
    if (!open) return
    setNormalizedName(model?.normalized_name ?? '')
    setDisplayName(model?.display_name ?? '')
    setDescription(model?.description ?? '')
    setEnabled(model?.enabled ?? true)
    // 编辑模式不自动回填密钥，留空表示保留服务端密文喵。
    setBaseURL(model?.base_url ?? '')
    setAPIKey('')
    setRealModelName(model?.real_model_name ?? '')
    setAuthStyle(model?.auth_style ?? 'bearer')
    setModelRatio(model?.model_ratio ?? '1')
    setCompletionRatio(model?.completion_ratio ?? '1')
    setCacheRatio(model?.cache_ratio ?? '1')
    setCacheCreationRatio(model?.cache_creation_ratio ?? '1')
    setCacheCreation5mRatio(model?.cache_creation_5m_ratio ?? '1')
    setCacheCreation1hRatio(model?.cache_creation_1h_ratio ?? '1')
    setImageRatio(model?.image_ratio ?? '1')
    setAudioRatio(model?.audio_ratio ?? '1')
    setAudioCompletionRatio(model?.audio_completion_ratio ?? '1')
    setBalanceYuan(centsToYuan(model?.balance_cents ?? 0))
    setAvailableYuan(centsToYuan(model?.available_cents ?? model?.spend_limit_cents ?? 0))
    setShareEnabled(model?.share_enabled ?? false)
    setShareLimitYuan(centsToYuan(model?.share_limit_cents ?? 0))
    // 模式回填：显式 share_list_mode 优先，旧数据按非空名单推断喵。
    const listMode = model?.share_list_mode
    if (listMode === 'whitelist' || listMode === 'blacklist') {
      setShareListMode(listMode)
      setShareList(listMode === 'whitelist' ? (model?.share_whitelist ?? '') : (model?.share_blacklist ?? ''))
    } else if (model?.share_whitelist) {
      setShareListMode('whitelist')
      setShareList(model.share_whitelist)
    } else if (model?.share_blacklist) {
      setShareListMode('blacklist')
      setShareList(model.share_blacklist)
    } else {
      setShareListMode('all')
      setShareList('')
    }
    setShowBalanceEnabled(model?.show_balance_enabled ?? false)
    setCustomHeadersJSON(model?.custom_headers ?? '')
    setFieldReplacementsJSON(model?.field_replacements ?? '')
    // 超时秒数回填，零视为默认显示空串喵。
    setTimeoutSeconds(model?.timeout_seconds && model.timeout_seconds > 0 ? String(model.timeout_seconds) : '')
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
    // 喵~防御：自定义请求头与字段替换必须是合法 JSON，否则拒绝保存喵。
    const trimmedCustomHeaders = customHeadersJSON.trim()
    const trimmedFieldReplacements = fieldReplacementsJSON.trim()
    try {
      if (trimmedCustomHeaders) JSON.parse(trimmedCustomHeaders)
      if (trimmedFieldReplacements) JSON.parse(trimmedFieldReplacements)
    } catch {
      toast.error(t('Request customization JSON is invalid'))
      return
    }
    // 喵~防御：超时秒数必须是非负整数且不超过 600，空串表示使用默认 60 秒喵。
    let resolvedTimeoutSeconds = 0
    if (timeoutSeconds.trim() !== '') {
      const parsedTimeout = Number(timeoutSeconds)
      if (!Number.isInteger(parsedTimeout) || parsedTimeout < 0 || parsedTimeout > 600) {
        toast.error(t('Timeout must be between 0 and 600 seconds'))
        return
      }
      resolvedTimeoutSeconds = parsedTimeout
    }
    // 组装创建或更新请求载荷，编辑模式携带版本号做乐观并发控制喵。
    const input: UserUpstreamModelInput = {
      normalized_name: trimmedNormalizedName,
      display_name: displayName.trim(),
      description: description.trim(),
      enabled,
      base_url: baseURL.trim(),
      api_key: apiKey,
      real_model_name: realModelName.trim(),
      auth_style: authStyle,
      timeout_seconds: resolvedTimeoutSeconds,
      custom_headers: trimmedCustomHeaders,
      field_replacements: trimmedFieldReplacements,
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
      available_cents: yuanToCents(availableYuan),
      share_enabled: shareEnabled,
      share_limit_cents: yuanToCents(shareLimitYuan),
      // 白名单/黑名单模式二选一：只写对应列，另一列清空；all 模式两者都空喵。
      share_whitelist: shareListMode === 'whitelist' ? shareList : '',
      share_blacklist: shareListMode === 'blacklist' ? shareList : '',
      share_list_mode: shareListMode === 'all' ? '' : shareListMode,
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
              title={t('Request Customization')}
              description={t('Custom headers and field replacements for upstream requests')}
              icon={<Settings2 className='size-4' />}
              iconTone='info'
            />
            <label className='grid gap-1 text-sm font-medium'>
              {t('Timeout seconds')}
              <Input inputMode='numeric' value={timeoutSeconds} disabled={isSaving} placeholder={t('0 = default 60 seconds')} onChange={(event) => setTimeoutSeconds(event.target.value)} />
              <span className='text-muted-foreground text-xs'>
                {t('Seconds before an upstream request is judged timed out')}
              </span>
            </label>
            <div className='flex items-center justify-between gap-3'>
              <span className='text-sm font-medium'>{t('Custom request headers')}</span>
              <Button
                type='button'
                size='sm'
                variant='outline'
                onClick={() => setCustomHeadersJSON(CUSTOM_HEADERS_PRESET)}
                disabled={isSaving}
              >
                <Wand2 className='size-3.5' />
                {t('Use preset')}
              </Button>
            </div>
            <Textarea
              value={customHeadersJSON}
              onChange={(event) => setCustomHeadersJSON(event.target.value)}
              placeholder={t('Custom headers JSON (e.g. {"*": true, "User-Agent": "..."})')}
              disabled={isSaving}
              className='min-h-[96px] font-mono text-xs'
            />
            <p className='text-muted-foreground text-xs'>
              {t('Custom headers apply to all upstream requests and override client headers. Authentication headers are managed separately.')}
            </p>
            <div className='flex items-center justify-between gap-3'>
              <span className='text-sm font-medium'>{t('Field replacements')}</span>
              <Button
                type='button'
                size='sm'
                variant='outline'
                onClick={() => setFieldReplacementsJSON(FIELD_REPLACEMENTS_PRESET)}
                disabled={isSaving}
              >
                <Wand2 className='size-3.5' />
                {t('Use preset')}
              </Button>
            </div>
            <Textarea
              value={fieldReplacementsJSON}
              onChange={(event) => setFieldReplacementsJSON(event.target.value)}
              placeholder={t('Field replacement JSON (e.g. {"reasoning_effort": {"max": "xhigh"}})')}
              disabled={isSaving}
              className='min-h-[80px] font-mono text-xs'
            />
            <p className='text-muted-foreground text-xs'>
              {t('Field replacements map request parameter values (e.g. reasoning effort) and never modify message contents.')}
            </p>
          </SideDrawerSection>

          <SideDrawerSection>
            <SideDrawerSectionHeader
              title={t('Pricing Configuration')}
              description={t('Prices are RMB per million tokens for each type')}
              icon={<Settings2 className='size-4' />}
              iconTone='info'
            />
            <label className='grid gap-1 text-sm font-medium'>
              {t('Input price (RMB per million tokens)')}
              <Input inputMode='decimal' value={modelRatio} onChange={(event) => setModelRatio(event.target.value)} placeholder='1' disabled={isSaving} />
            </label>
            <div className='grid grid-cols-2 gap-3'>
              <label className='grid gap-1 text-sm font-medium'>
                {t('Output price (RMB per million tokens)')}
                <Input inputMode='decimal' value={completionRatio} onChange={(event) => setCompletionRatio(event.target.value)} disabled={isSaving} />
              </label>
              <label className='grid gap-1 text-sm font-medium'>
                {t('Cache hit price (RMB per million tokens)')}
                <Input inputMode='decimal' value={cacheRatio} onChange={(event) => setCacheRatio(event.target.value)} disabled={isSaving} />
              </label>
              <label className='grid gap-1 text-sm font-medium'>
                {t('Cache write price (RMB per million tokens)')}
                <Input inputMode='decimal' value={cacheCreationRatio} onChange={(event) => setCacheCreationRatio(event.target.value)} disabled={isSaving} />
              </label>
              <label className='grid gap-1 text-sm font-medium'>
                {t('Cache write 5m price (RMB per million tokens)')}
                <Input inputMode='decimal' value={cacheCreation5mRatio} onChange={(event) => setCacheCreation5mRatio(event.target.value)} disabled={isSaving} />
              </label>
              <label className='grid gap-1 text-sm font-medium'>
                {t('Cache write 1h price (RMB per million tokens)')}
                <Input inputMode='decimal' value={cacheCreation1hRatio} onChange={(event) => setCacheCreation1hRatio(event.target.value)} disabled={isSaving} />
              </label>
              <label className='grid gap-1 text-sm font-medium'>
                {t('Image input price (RMB per million tokens)')}
                <Input inputMode='decimal' value={imageRatio} onChange={(event) => setImageRatio(event.target.value)} disabled={isSaving} />
              </label>
              <label className='grid gap-1 text-sm font-medium'>
                {t('Audio input price (RMB per million tokens)')}
                <Input inputMode='decimal' value={audioRatio} onChange={(event) => setAudioRatio(event.target.value)} disabled={isSaving} />
              </label>
              <label className='grid gap-1 text-sm font-medium'>
                {t('Audio output price (RMB per million tokens)')}
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
                {t('Available (yuan)')}
                <Input inputMode='decimal' value={availableYuan} onChange={(event) => setAvailableYuan(event.target.value)} disabled={isSaving} />
              </label>
            </div>
            <p className='text-muted-foreground text-xs'>
              {t('Balance is how much this model can theoretically still be used; available is how much you accept to spend.')}
            </p>
            {model && model.upstream_remaining_cents > 0 && (
              <p className='text-muted-foreground text-xs'>
                {t('Upstream remaining')}: {centsToYuan(model.upstream_remaining_cents)} ¥
              </p>
            )}
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
            <label className='grid gap-1 text-sm font-medium'>
              {t('Model description')}
              <Textarea
                value={description}
                onChange={(event) => setDescription(event.target.value)}
                placeholder={t('Describe what upstream this is, its performance, the model, and its availability...')}
                disabled={isSaving}
                rows={3}
              />
            </label>
            <label className='flex items-center justify-between gap-3 text-sm'>
              <span>{t('Share to all users')}</span>
              <Switch checked={shareEnabled} onCheckedChange={setShareEnabled} disabled={isSaving} />
            </label>
            <label className='grid gap-1 text-sm font-medium'>
              {t('Share limit (yuan)')}
              <Input inputMode='decimal' value={shareLimitYuan} onChange={(event) => setShareLimitYuan(event.target.value)} disabled={isSaving || !shareEnabled} />
            </label>
            <label className='grid gap-1.5 text-sm'>
              <span className='font-medium'>{t('Sharing policy')}</span>
              {/* 名单模式显式二选一：全开放 / 白名单制 / 黑名单制喵。 */}
              <RadioGroup
                value={shareListMode}
                onValueChange={(value) => setShareListMode(value as 'all' | 'whitelist' | 'blacklist')}
                disabled={isSaving || !shareEnabled}
              >
                <label className='flex items-center gap-2'>
                  <RadioGroupItem value='all' />
                  <span>{t('All users')}</span>
                </label>
                <label className='flex items-center gap-2'>
                  <RadioGroupItem value='whitelist' />
                  <span>{t('Whitelist only')}</span>
                </label>
                <label className='flex items-center gap-2'>
                  <RadioGroupItem value='blacklist' />
                  <span>{t('Blacklist only')}</span>
                </label>
              </RadioGroup>
            </label>
            {shareListMode !== 'all' && (
              <label className='grid gap-1 text-sm font-medium'>
                {shareListMode === 'whitelist' ? t('Whitelist user ids') : t('Blacklist user ids')}
                <Textarea
                  value={shareList}
                  onChange={(event) => setShareList(event.target.value)}
                  placeholder='1, 2, 3'
                  disabled={isSaving || !shareEnabled}
                  rows={2}
                />
              </label>
            )}
            <p className='text-muted-foreground text-xs'>
              {shareListMode === 'whitelist'
                ? t('Only whitelisted users can view and call this model. The owner is always allowed.')
                : shareListMode === 'blacklist'
                  ? t('All users except the blacklist can view and call this model.')
                  : t('All users can view and call this model.')}
            </p>
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

// mapUpstreamModelStatus 把后端状态载荷规整为状态组件可消费的摘要喵。
function mapUpstreamModelStatus(status: UpstreamModelStatus): EntityStatusSummary {
  return {
    availability: status.availability,
    avg_latency_ms: status.avg_latency_ms,
    request_count: status.request_count,
    current_requests: status.current_requests,
    availability_24h: status.availability_24h,
    last_at: status.last_at,
    last_success: status.last_success,
    last_latency_ms: status.last_latency_ms,
    last_error: status.last_error,
  }
}

// mapUpstreamModelSharedStatus 共享维度载荷字段较少，单独规整为摘要喵。
function mapUpstreamModelSharedStatus(status: UpstreamModelSharedStatus): EntityStatusSummary {
  return {
    availability: status.availability,
    avg_latency_ms: status.avg_latency_ms,
    request_count: status.request_count,
    current_requests: status.current_requests,
    last_at: status.last_at,
    last_success: status.last_success,
  }
}

// UpstreamModelStatusIndicator 为单个上游模型行拉取状态并渲染健康圆点喵。
// 点击圆点打开该上游模型的性能抽屉喵。
function UpstreamModelStatusIndicator({ model }: { model: UserUpstreamModel }) {
  const { t } = useTranslation()
  // isDrawerOpen 记录性能抽屉开关喵。
  const [isDrawerOpen, setIsDrawerOpen] = useState(false)
  const statusQuery = useQuery({
    // 以模型 id 为键，属主行附带共享维度喵。
    queryKey: ['upstream-model-status', model.id],
    queryFn: () => getUpstreamModelStatus(model.id, true),
    staleTime: 30 * 1000,
    retry: false,
  })
  const status = statusQuery.data?.data
  return (
    <>
      <EntityStatusDot
        label={model.display_name || model.normalized_name}
        // 喵~防御：接口未返回数据时保持未加载状态喵。
        summary={status ? mapUpstreamModelStatus(status) : undefined}
        // 共享维度仅在属主请求 include_shared=true 且共享有调用时由后端携带喵。
        shared={status?.shared ? mapUpstreamModelSharedStatus(status.shared) : undefined}
        loading={statusQuery.isLoading}
        error={Boolean(statusQuery.isError)}
        onOpenPerformance={() => setIsDrawerOpen(true)}
      />
      {/* 性能抽屉：展示上游模型自用维度的 TTFT/缓存命中率/Token/请求量图表喵。 */}
      <EntityPerformanceDrawer
        open={isDrawerOpen}
        onOpenChange={setIsDrawerOpen}
        title={model.display_name || model.normalized_name}
        description={t('Upstream model performance over the last 24 hours')}
        data={status ?? null}
        loading={statusQuery.isLoading}
        error={Boolean(statusQuery.isError)}
      />
    </>
  )
}

// UpstreamModelUsageDrawer 展示某个共享上游模型按用户聚合的使用情况喵。
// 属主可见每用户 user id/用户名/请求数/token/最近调用喵。
export function UpstreamModelUsageDrawer({
  open,
  onOpenChange,
  model,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  model: UserUpstreamModel | null
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  // 以模型 id 为键拉取使用情况，仅在抽屉打开且选中模型时请求喵。
  const usageQuery = useQuery({
    queryKey: ['upstream-model-usage', model?.id],
    queryFn: () => (model ? getUpstreamModelUserUsage(model.id) : Promise.resolve({ success: false, data: [] })),
    // 喵~防御：没有选中模型或抽屉未打开时禁用请求，避免无效查询喵。
    enabled: Boolean(model) && open,
    retry: false,
  })
  // 喵~防御：接口失败或无数据时按空列表处理，避免空指针喵。
  const usage: UpstreamModelUserUsage[] = usageQuery.data?.data ?? []
  // isClearConfirmOpen 控制清空记录的二次确认对话框喵。
  const [isClearConfirmOpen, setIsClearConfirmOpen] = useState(false)
  // isClearing 标记清空请求进行中，禁用按钮避免重复提交喵。
  const [isClearing, setIsClearing] = useState(false)

  // handleClearUsage 清空共享使用记录并刷新当前抽屉数据喵。
  const handleClearUsage = async () => {
    // 喵~防御：没有选中模型时不执行清空喵。
    if (!model) return
    setIsClearing(true)
    try {
      const response = await clearUpstreamModelUserUsage(model.id)
      // 喵~防御：业务失败必须展示后端消息喵。
      if (!response.success) {
        throw new Error(response.message || t('Unable to clear shared usage'))
      }
      toast.success(t('Shared usage cleared'))
      setIsClearConfirmOpen(false)
      void queryClient.invalidateQueries({ queryKey: ['upstream-model-usage', model.id] })
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Unable to clear shared usage'))
    } finally {
      setIsClearing(false)
    }
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className={sideDrawerContentClassName('max-w-none sm:!max-w-[640px]')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{t('Shared model usage')}</SheetTitle>
          <SheetDescription>
            {model ? `${model.display_name || model.normalized_name} · user/${model.normalized_name}` : ''}
          </SheetDescription>
        </SheetHeader>
        <div className={sideDrawerFormClassName()}>
          {usageQuery.isLoading && <p className='text-sm text-muted-foreground'>{t('Loading')}</p>}
          {usageQuery.isError && <p className='text-sm text-destructive'>{t('Unable to load shared usage')}</p>}
          {!usageQuery.isLoading && !usageQuery.isError && usage.length === 0 && (
            <p className='text-sm text-muted-foreground'>{t('No shared usage yet')}</p>
          )}
          {usage.length > 0 && (
            <div className='overflow-auto rounded-md border'>
              <table className='w-full text-sm'>
                <thead>
                  <tr className='border-b bg-muted/50 text-left text-xs text-muted-foreground'>
                    <th className='px-3 py-2'>ID</th>
                    <th className='px-3 py-2'>{t('Username')}</th>
                    <th className='px-3 py-2'>{t('Requests')}</th>
                    <th className='px-3 py-2'>{t('Cost')}</th>
                    <th className='px-3 py-2'>{t('Total tokens')}</th>
                    <th className='px-3 py-2'>{t('Last call')}</th>
                  </tr>
                </thead>
                <tbody>
                  {usage.map((row) => (
                    <tr key={row.user_id} className='border-b last:border-b-0'>
                      {/* 明确展示用户 id，供属主识别谁在使用共享模型喵。 */}
                      <td className='px-3 py-2'>{row.user_id}</td>
                      <td className='px-3 py-2'>{row.username || '-'}</td>
                      <td className='px-3 py-2'>{row.request_count}</td>
                      {/* 费用金额（分）转元展示，只统计该用户使用产生的费用喵。 */}
                      <td className='px-3 py-2'>¥{centsToYuan(row.cost_cents)}</td>
                      {/* 总 token = 输入+输出合计，不再细分喵。 */}
                      <td className='px-3 py-2'>{row.total_tokens.toLocaleString()}</td>
                      <td className='px-3 py-2'>{formatTimestampToDate(row.last_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' className='w-full sm:w-auto' />}>{t('Close')}</SheetClose>
          {/* 清空记录按钮：二次确认后删除全部共享调用统计喵。 */}
          <Button
            type='button'
            variant='destructive'
            className='w-full sm:w-auto'
            disabled={isClearing || usage.length === 0}
            onClick={() => setIsClearConfirmOpen(true)}
          >
            {isClearing ? t('Clearing') : t('Clear usage')}
          </Button>
        </SheetFooter>
        {/* 清空记录二次确认对话框喵。 */}
        <AlertDialog open={isClearConfirmOpen} onOpenChange={setIsClearConfirmOpen}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>{t('Clear shared usage')}</AlertDialogTitle>
              <AlertDialogDescription>
                {t('This will permanently delete all shared usage records for this model. This action cannot be undone.')}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel disabled={isClearing}>{t('Cancel')}</AlertDialogCancel>
              <AlertDialogAction onClick={() => void handleClearUsage()} disabled={isClearing}>
                {isClearing ? t('Clearing') : t('Clear')}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
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
  // usageModel 记录当前正在查看使用情况的上游模型喵。
  const [usageModel, setUsageModel] = useState<UserUpstreamModel | null>(null)
  // 正在执行嗅探或同步操作的模型 id，用于展示按钮加载态喵。
  const [balanceActionId, setBalanceActionId] = useState<number | null>(null)
  const upstreamModelsQuery = useQuery({
    queryKey: ['upstream-models'],
    queryFn: getUserUpstreamModels,
  })
  const upstreamModels = upstreamModelsQuery.data?.data ?? []

  // handleSyncBalance 把嗅探结果一键同步为余额并刷新列表喵。
  const handleSyncBalance = async (item: UserUpstreamModel) => {
    // 喵~防御：同一时间只允许一个同步操作喵。
    if (balanceActionId !== null) return
    setBalanceActionId(item.id)
    try {
      const response = await syncUserUpstreamModelBalance(item.id)
      // 喵~防御：业务失败必须展示后端消息喵。
      if (!response.success) {
        throw new Error(response.message || t('Unable to sync upstream model balance'))
      }
      toast.success(t('Upstream balance synced to available balance'))
      void queryClient.invalidateQueries({ queryKey: ['upstream-models'] })
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Unable to sync upstream model balance'))
    } finally {
      setBalanceActionId(null)
    }
  }

  // handleSyncAvailable 把嗅探结果一键同步为可用额度并刷新列表喵。
  const handleSyncAvailable = async (item: UserUpstreamModel) => {
    // 喵~防御：同一时间只允许一个同步操作喵。
    if (balanceActionId !== null) return
    setBalanceActionId(item.id)
    try {
      const response = await syncUserUpstreamModelAvailable(item.id)
      // 喵~防御：业务失败必须展示后端消息喵。
      if (!response.success) {
        throw new Error(response.message || t('Unable to sync upstream model available'))
      }
      toast.success(t('Upstream available synced'))
      void queryClient.invalidateQueries({ queryKey: ['upstream-models'] })
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Unable to sync upstream model available'))
    } finally {
      setBalanceActionId(null)
    }
  }

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
                  user/{item.normalized_name} · {item.real_model_name}
                  {item.base_url ? ` · ${item.base_url}` : ''}
                </div>
                <div className='text-muted-foreground mt-1 text-xs'>
                  {t('Balance')}: {centsToYuan(item.balance_cents)} ¥ · {t('Available')}: {centsToYuan(item.available_cents)} ¥ ·{' '}
                  {t('Shared balance')}: {centsToYuan(item.share_limit_cents)} ¥
                </div>
              </div>
              <div className='flex shrink-0 items-center gap-2'>
                {/* 行状态指示器：余额操作按钮左侧展示健康圆点喵。 */}
                <UpstreamModelStatusIndicator model={item} />
                <Button
                  size='sm'
                  variant='outline'
                  disabled={balanceActionId !== null}
                  onClick={() => void handleSyncBalance(item)}
                  title={t('Sync checked remaining quota into balance')}
                >
                  <Wallet className='size-3.5' />
                  {t('Sync to balance')}
                </Button>
                <Button
                  size='sm'
                  variant='outline'
                  disabled={balanceActionId !== null}
                  onClick={() => void handleSyncAvailable(item)}
                  title={t('Sync checked remaining quota into available')}
                >
                  <Target className='size-3.5' />
                  {t('Sync to available')}
                </Button>
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
                <Button
                  size='sm'
                  variant='outline'
                  onClick={() => setUsageModel(item)}
                  title={t('View per-user usage of this shared model')}
                >
                  <Users className='size-3.5' />
                  {t('Usage')}
                </Button>
                <Button size='sm' variant='outline' onClick={() => setDeletingModel(item)}>
                  <Trash2 className='size-3.5' />
                  {t('Delete')}
                </Button>
              </div>
            </div>
          ))}
        </div>

        <UpstreamModelDrawer
          open={isDrawerOpen}
          onOpenChange={setIsDrawerOpen}
          model={editingModel}
        />

        {/* 使用情况抽屉：展示该共享模型的每用户调用统计喵。 */}
        <UpstreamModelUsageDrawer
          open={Boolean(usageModel)}
          onOpenChange={(open) => !open && setUsageModel(null)}
          model={usageModel}
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
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
