/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import {
  ArrowDown01Icon,
  ArrowUp01Icon,
  Cancel01Icon,
  Add01Icon,
  ChevronDownIcon,
  ChevronUpIcon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useQuery } from '@tanstack/react-query'
import { ShieldCheck } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ModelGroupSelector } from '@/components/model-group-selector'
import {
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
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
import { getUserGroups, getUserModels } from '@/features/playground/api'
import { shouldClearModelForGroup } from '@/features/playground/lib/options/playground-option-utils'

import type {
  VirtualModel,
  VirtualModelCandidate,
  VirtualModelCandidateAuthStyle,
  VirtualModelCandidateInput,
} from '../api'
import { replaceVirtualModelCandidates } from '../api'
import { VirtualModelCandidateFailureRulesEditor } from './virtual-model-candidate-failure-rules-editor'

// CandidateDraft 仅保存编辑候选链需要的字段；已有自定义候选的密钥不回填到草稿喵。
type CandidateDraft = {
  apiKey: string
  authStyle: VirtualModelCandidateAuthStyle
  baseURL: string
  enabled: boolean
  groupName: string
  id?: number
  maxRetries: string
  realModelName: string
  sourceType: 'internal' | 'custom'
  timeoutSeconds: string
}

// DEFAULT_TIMEOUT_SECONDS 是新候选在未改动时的保守单次超时秒数喵。
const DEFAULT_TIMEOUT_SECONDS = '60'
// DEFAULT_MAX_RETRIES 是新候选默认的自定义上游重试次数喵。
const DEFAULT_MAX_RETRIES = '0'

// toCandidateDraft 把脱敏响应映射到可编辑草稿，绝不从响应读取 API Key 喵。
function toCandidateDraft(candidate: VirtualModelCandidate): CandidateDraft {
  return {
    apiKey: '',
    authStyle: candidate.auth_style ?? 'bearer',
    // 喵~防御：响应中的 base_url 仅是脱敏摘要，不能回传覆盖真实加密地址，因此既有候选草稿保持为空喵。
    baseURL: candidate.id ? '' : (candidate.base_url ?? ''),
    enabled: candidate.enabled,
    groupName: candidate.group_name ?? '',
    id: candidate.id,
    maxRetries: String(candidate.max_retries),
    realModelName: candidate.real_model_name ?? '',
    sourceType: candidate.source_type === 'custom' ? 'custom' : 'internal',
    timeoutSeconds: String(candidate.timeout_seconds),
  }
}

// createCandidateDraft 生成安全的空候选草稿，避免缺失字段被误解释为已有凭据喵。
function createCandidateDraft(sourceType: 'internal' | 'custom'): CandidateDraft {
  return {
    apiKey: '',
    authStyle: 'bearer',
    baseURL: '',
    enabled: true,
    groupName: '',
    maxRetries: DEFAULT_MAX_RETRIES,
    realModelName: '',
    sourceType,
    timeoutSeconds: DEFAULT_TIMEOUT_SECONDS,
  }
}

// InternalCandidateSelector 复用游乐场的模型/分组选择器，让内部候选直接选择当前用户可访问的分组与真实模型喵。
function InternalCandidateSelector({
  group,
  model,
  onGroupChange,
  onModelChange,
  disabled,
}: {
  group: string
  model: string
  onGroupChange: (group: string) => void
  onModelChange: (model: string) => void
  disabled?: boolean
}) {
  // 加载当前用户可访问的分组列表，数据源与游乐场选择器一致喵。
  const groupsQuery = useQuery({
    queryKey: ['virtual-model-groups'],
    queryFn: getUserGroups,
  })
  // 按候选当前分组加载该分组可用的模型，避免展示无权访问的真实模型喵。
  const modelsQuery = useQuery({
    queryKey: ['virtual-model-models', group],
    queryFn: () => getUserModels(group),
    enabled: group !== '',
  })
  // 模型列表就绪后，若已选模型不在当前分组可用列表内则清空，与游乐场分组切换行为一致喵。
  // 喵~防御：useRef 持有最新回调，避免内联回调导致 useEffect 每次渲染都触发清空。
  const onModelChangeRef = useRef(onModelChange)
  onModelChangeRef.current = onModelChange
  const shouldClearModel =
    !modelsQuery.isLoading && shouldClearModelForGroup(modelsQuery.data ?? [], model)
  useEffect(() => {
    if (shouldClearModel) {
      onModelChangeRef.current('')
    }
  }, [shouldClearModel])

  return (
    <ModelGroupSelector
      selectedGroup={group}
      groups={groupsQuery.data ?? []}
      onGroupChange={onGroupChange}
      selectedModel={model}
      models={modelsQuery.data ?? []}
      onModelChange={onModelChange}
      disabled={disabled}
    />
  )
}

// validateCandidateDraft 在提交前拦截空必填项、非法数值和未重新输入的自定义密钥喵。
function validateCandidateDraft(
  candidate: CandidateDraft,
  index: number,
  t: (key: string, options?: Record<string, unknown>) => string
): VirtualModelCandidateInput {
  const maxRetries = Number(candidate.maxRetries)
  const timeoutSeconds = Number(candidate.timeoutSeconds)
  const realModelName = candidate.realModelName.trim()
  if (!realModelName) {
    throw new Error(t('Candidate {{index}} requires a real model name', { index: index + 1 }))
  }
  if (!Number.isInteger(maxRetries) || maxRetries < 0 || maxRetries > 20) {
    throw new Error(t('Candidate {{index}} retry count must be between 0 and 20', { index: index + 1 }))
  }
  if (!Number.isInteger(timeoutSeconds) || timeoutSeconds < 1 || timeoutSeconds > 600) {
    throw new Error(t('Candidate {{index}} timeout must be between 1 and 600 seconds', { index: index + 1 }))
  }
  if (candidate.sourceType === 'internal') {
    const groupName = candidate.groupName.trim()
    if (!groupName) {
      throw new Error(t('Candidate {{index}} requires a group', { index: index + 1 }))
    }
    return {
      id: candidate.id,
      source_type: 'internal',
      enabled: candidate.enabled,
      max_retries: maxRetries,
      timeout_seconds: timeoutSeconds,
      group_name: groupName,
      real_model_name: realModelName,
    }
  }

  const baseURL = candidate.baseURL.trim()
  const apiKey = candidate.apiKey.trim()
  // 喵~防御：新自定义候选需要 URL，已保存候选可不回传脱敏摘要以保留服务端加密地址喵。
  if (!candidate.id && !baseURL) {
    throw new Error(t('Candidate {{index}} requires an upstream URL', { index: index + 1 }))
  }
  // 喵~防御：新候选没有旧密文可以沿用，已有候选允许留空以让服务端保留加密凭据喵。
  if (!candidate.id && !apiKey) {
    throw new Error(t('Candidate {{index}} requires an upstream API Key', { index: index + 1 }))
  }
  return {
    id: candidate.id,
    source_type: 'custom',
    enabled: candidate.enabled,
    max_retries: maxRetries,
    timeout_seconds: timeoutSeconds,
    base_url: baseURL || undefined,
    api_key: apiKey || undefined,
    auth_style: candidate.authStyle,
    real_model_name: realModelName,
  }
}

// VirtualModelCandidatesEditor 管理候选链顺序；列表默认只显示模型名，点击候选行展开完整参数编辑喵。
// 内部候选显示 分组/真实模型，自定义候选显示真实模型并带 Custom 标记喵。
export function VirtualModelCandidatesEditor({
  model,
  onSaved,
}: {
  model: VirtualModel
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const [draftCandidates, setDraftCandidates] = useState<CandidateDraft[]>([])
  const [isSaving, setIsSaving] = useState(false)
  // expandedIndex 记录当前展开完整编辑的候选下标，同一时刻只展开一个喵。
  const [expandedIndex, setExpandedIndex] = useState<number | null>(null)
  // rulesCandidateIndex 记录正在编辑失败规则的候选下标，非空时打开候选级规则抽屉喵。
  const [rulesCandidateIndex, setRulesCandidateIndex] = useState<number | null>(null)
  // pendingDeleteIndex 记录等待确认删除的候选下标，非空时打开删除确认弹窗喵。
  const [pendingDeleteIndex, setPendingDeleteIndex] = useState<number | null>(null)

  // 当服务端模型版本变化时重新生成草稿，避免对已替换候选链进行过期编辑喵。
  useEffect(() => {
    setDraftCandidates((model.candidates ?? []).map(toCandidateDraft))
    // 列表数据刷新后收起展开态，避免展开内容与最新数据错位喵。
    setExpandedIndex(null)
  }, [model.id, model.version])

  // candidateDisplayName 生成候选行紧凑显示的模型名喵。
  const candidateDisplayName = (candidate: CandidateDraft) => {
    // 内部候选显示 分组/模型，便于区分同真实模型不同分组的候选喵。
    if (candidate.sourceType === 'internal') {
      const groupName = candidate.groupName.trim()
      if (candidate.realModelName.trim()) {
        return groupName ? `${groupName}/${candidate.realModelName.trim()}` : candidate.realModelName.trim()
      }
      return groupName || t('Unnamed candidate')
    }
    // 自定义候选显示真实模型名，未填写时给出占位文案喵。
    return candidate.realModelName.trim() || t('Unnamed candidate')
  }

  const updateCandidate = (index: number, patch: Partial<CandidateDraft>) => {
    setDraftCandidates((currentCandidates) =>
      currentCandidates.map((candidate, candidateIndex) =>
        candidateIndex === index ? { ...candidate, ...patch } : candidate
      )
    )
  }

  const moveCandidate = (index: number, direction: 'up' | 'down') => {
    const targetIndex = direction === 'up' ? index - 1 : index + 1
    if (targetIndex < 0 || targetIndex >= draftCandidates.length) return
    setDraftCandidates((currentCandidates) => {
      const nextCandidates = [...currentCandidates]
      ;[nextCandidates[index], nextCandidates[targetIndex]] = [
        nextCandidates[targetIndex],
        nextCandidates[index],
      ]
      return nextCandidates
    })
  }

  // removeCandidate 点击删除按钮：已保存候选打开确认弹窗，新增草稿直接移除喵。
  const removeCandidate = (index: number) => {
    const removedCandidate = draftCandidates[index]
    if (removedCandidate?.id) {
      setPendingDeleteIndex(index)
      return
    }
    performRemove(index)
  }

  // performRemove 实际从草稿链移除指定候选，并收起对应展开态喵。
  const performRemove = (index: number) => {
    setDraftCandidates((currentCandidates) =>
      currentCandidates.filter((_, candidateIndex) => candidateIndex !== index)
    )
    // 删除当前展开项后收起编辑区，避免留下指向已删候选的展开状态喵。
    if (expandedIndex === index) {
      setExpandedIndex(null)
    }
    setPendingDeleteIndex(null)
  }

  const addInternalCandidate = () => {
    if (draftCandidates.length >= 32) {
      toast.error(t('A virtual model can contain at most 32 candidates'))
      return
    }
    setDraftCandidates((currentCandidates) => [
      ...currentCandidates,
      createCandidateDraft('internal'),
    ])
    // 新候选追加后自动展开，方便立即选择模型喵。
    setExpandedIndex(draftCandidates.length)
  }

  const addCustomCandidate = () => {
    if (draftCandidates.length >= 32) {
      toast.error(t('A virtual model can contain at most 32 candidates'))
      return
    }
    setDraftCandidates((currentCandidates) => [
      ...currentCandidates,
      createCandidateDraft('custom'),
    ])
    setExpandedIndex(draftCandidates.length)
  }

  const saveCandidates = async () => {
    if (draftCandidates.length === 0) {
      toast.error(t('A virtual model needs at least one candidate'))
      return
    }
    try {
      setIsSaving(true)
      const candidates = draftCandidates.map((candidate, index) =>
        validateCandidateDraft(candidate, index, t)
      )
      const response = await replaceVirtualModelCandidates(model.id, {
        version: model.version,
        candidates,
      })
      if (!response.success) {
        throw new Error(response.message || t('Unable to save candidate chain'))
      }
      toast.success(t('Candidate chain saved'))
      onSaved()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Unable to save candidate chain'))
    } finally {
      setIsSaving(false)
    }
  }

  return (
    <div className='space-y-4'>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <div>
          <h3 className='font-medium'>{t('Call chain')}</h3>
          <p className='text-muted-foreground text-sm'>{t('Candidates run from top to bottom when a request fails.')}</p>
        </div>
        <div className='flex gap-2'>
          <Button type='button' size='sm' variant='outline' onClick={addInternalCandidate} disabled={isSaving}>
            <HugeiconsIcon icon={Add01Icon} strokeWidth={2} aria-hidden='true' />
            {t('Add internal candidate')}
          </Button>
          <Button type='button' size='sm' variant='outline' onClick={addCustomCandidate} disabled={isSaving}>
            <HugeiconsIcon icon={Add01Icon} strokeWidth={2} aria-hidden='true' />
            {t('Add custom candidate')}
          </Button>
        </div>
      </div>

      {draftCandidates.some((candidate) => candidate.sourceType === 'custom') && (
        <p className='rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-sm text-amber-800 dark:text-amber-300'>
          {t('Saved upstream URLs and API Keys are never displayed. Leave either field blank to retain a saved custom candidate value, or enter a new value to rotate it.')}
        </p>
      )}

      {draftCandidates.map((candidate, index) => (
        <div className='overflow-hidden rounded-md border' key={candidate.id ?? `new-${index}`}>
          {/* 候选行折叠态只显示模型名与状态，点击整行展开或收起完整参数编辑喵。 */}
          <div className='flex flex-wrap items-center justify-between gap-2 p-3'>
            <button
              className='flex min-w-0 flex-1 items-center gap-2 text-left'
              type='button'
              onClick={() => setExpandedIndex(expandedIndex === index ? null : index)}
              aria-expanded={expandedIndex === index}
            >
              <Badge variant='secondary'>{index + 1}</Badge>
              <Badge variant={candidate.sourceType === 'internal' ? 'outline' : 'secondary'}>
                {candidate.sourceType === 'internal' ? t('Internal') : t('Custom')}
              </Badge>
              <span className='min-w-0 flex-1 truncate font-medium'>{candidateDisplayName(candidate)}</span>
              <Badge variant={candidate.enabled ? 'default' : 'secondary'}>{candidate.enabled ? t('Enabled') : t('Disabled')}</Badge>
              <HugeiconsIcon icon={expandedIndex === index ? ChevronUpIcon : ChevronDownIcon} strokeWidth={2} className='size-4 shrink-0 text-muted-foreground' aria-hidden='true' />
            </button>
            <div className='flex gap-1'>
              <Button type='button' size='icon-sm' variant='ghost' disabled={isSaving || index === 0} onClick={() => moveCandidate(index, 'up')} aria-label={t('Move candidate up')}>
                <HugeiconsIcon icon={ArrowUp01Icon} strokeWidth={2} aria-hidden='true' />
              </Button>
              <Button type='button' size='icon-sm' variant='ghost' disabled={isSaving || index === draftCandidates.length - 1} onClick={() => moveCandidate(index, 'down')} aria-label={t('Move candidate down')}>
                <HugeiconsIcon icon={ArrowDown01Icon} strokeWidth={2} aria-hidden='true' />
              </Button>
              <Button type='button' size='icon-sm' variant='ghost' disabled={isSaving || candidate.id === undefined} onClick={() => setRulesCandidateIndex(index)} aria-label={t('Candidate failure rules')}>
                <ShieldCheck className='size-4' />
              </Button>
              <Button type='button' size='icon-sm' variant='ghost' disabled={isSaving} onClick={() => removeCandidate(index)} aria-label={t('Remove candidate')}>
                <HugeiconsIcon icon={Cancel01Icon} strokeWidth={2} aria-hidden='true' />
              </Button>
            </div>
          </div>

          {/* 展开后展示该候选的完整参数编辑，保证原有配置能力不丢失喵。 */}
          {expandedIndex === index && (
            <div className='space-y-3 border-t p-3'>
              {candidate.sourceType === 'internal' ? (
                <InternalCandidateSelector
                  group={candidate.groupName}
                  model={candidate.realModelName}
                  onGroupChange={(groupName) => updateCandidate(index, { groupName })}
                  onModelChange={(realModelName) => updateCandidate(index, { realModelName })}
                  disabled={isSaving}
                />
              ) : (
                <div className='grid gap-3 sm:grid-cols-2'>
                  <label className='grid gap-1 text-sm font-medium'>
                    {t('Upstream URL')}
                    <Input value={candidate.baseURL} disabled={isSaving} placeholder={candidate.id ? t('Leave blank to retain the saved upstream URL') : 'https://api.example.com'} onChange={(event) => updateCandidate(index, { baseURL: event.target.value })} />
                  </label>
                  <label className='grid gap-1 text-sm font-medium'>
                    {t('Real model name')}
                    <Input value={candidate.realModelName} disabled={isSaving} onChange={(event) => updateCandidate(index, { realModelName: event.target.value })} />
                  </label>
                  <label className='grid gap-1 text-sm font-medium'>
                    {t('Upstream API Key')}
                    <Input type='password' autoComplete='new-password' value={candidate.apiKey} disabled={isSaving} placeholder={candidate.id ? t('Enter a new API Key to save this custom candidate') : t('Enter upstream API Key')} onChange={(event) => updateCandidate(index, { apiKey: event.target.value })} />
                  </label>
                  <label className='grid gap-1 text-sm font-medium'>
                    {t('Authentication style')}
                    <select className='border-input bg-background h-9 rounded-md border px-3 text-sm' value={candidate.authStyle} disabled={isSaving} onChange={(event) => updateCandidate(index, { authStyle: event.target.value as VirtualModelCandidateAuthStyle })}>
                      <option value='bearer'>{t('Bearer token')}</option>
                      <option value='api_key'>{t('API Key header')}</option>
                      <option value='anthropic'>{t('Anthropic API Key')}</option>
                    </select>
                  </label>
                  {candidate.id && <p className='text-muted-foreground text-xs sm:col-span-2'>{t('For security, the saved upstream URL and API Key are never displayed. Leave either field blank to retain it, or enter a new value to rotate it.')}</p>}
                </div>
              )}

              <div className='grid gap-3 sm:grid-cols-3'>
                <label className='grid gap-1 text-sm font-medium'>
                  {t('Timeout seconds')}
                  <Input inputMode='numeric' value={candidate.timeoutSeconds} disabled={isSaving} onChange={(event) => updateCandidate(index, { timeoutSeconds: event.target.value })} />
                </label>
                <label className='grid gap-1 text-sm font-medium'>
                  {t('Maximum retries')}
                  <Input inputMode='numeric' value={candidate.maxRetries} disabled={isSaving} onChange={(event) => updateCandidate(index, { maxRetries: event.target.value })} />
                </label>
                <label className='flex items-end justify-between gap-3 pb-2 text-sm'>
                  <span>{t('Enabled')}</span>
                  <Switch checked={candidate.enabled} disabled={isSaving} onCheckedChange={(enabled) => updateCandidate(index, { enabled })} />
                </label>
              </div>
            </div>
          )}
        </div>
      ))}

      {draftCandidates.length === 0 && <p className='text-muted-foreground text-sm'>{t('No candidates configured')}</p>}
      <div className='flex justify-end'>
        <Button type='button' onClick={() => void saveCandidates()} disabled={isSaving}>
          {isSaving ? t('Saving') : t('Save candidate chain')}
        </Button>
      </div>

      {/* 候选级失败规则抽屉：为选中候选配置独立规则，未配置时运行时回退模型级全局兜底喵。 */}
      <Sheet
        open={rulesCandidateIndex !== null}
        onOpenChange={(open) => {
          if (!open) setRulesCandidateIndex(null)
        }}
      >
        <SheetContent className={sideDrawerContentClassName('max-w-none sm:!max-w-[620px]')}>
          <SheetHeader className={sideDrawerHeaderClassName()}>
            <SheetTitle>{t('Candidate failure rules')}</SheetTitle>
            <SheetDescription>{t('Configure the failure rules used when this candidate fails.')}</SheetDescription>
          </SheetHeader>
          <div className={sideDrawerFormClassName('gap-5')}>
            {rulesCandidateIndex !== null && draftCandidates[rulesCandidateIndex] && draftCandidates[rulesCandidateIndex].id !== undefined && (
              <VirtualModelCandidateFailureRulesEditor
                model={model}
                candidateID={draftCandidates[rulesCandidateIndex].id ?? 0}
                candidateLabel={candidateDisplayName(draftCandidates[rulesCandidateIndex])}
                rules={(model.candidates ?? []).find((candidate) => candidate.id === draftCandidates[rulesCandidateIndex].id)?.failure_rules ?? []}
                onSaved={() => {
                  // 保存成功后刷新父级模型数据并关闭抽屉喵。
                  onSaved()
                  setRulesCandidateIndex(null)
                }}
              />
            )}
          </div>
          <SheetFooter className={sideDrawerFooterClassName()}>
            <SheetClose render={<Button variant='outline' className='w-full sm:w-auto' />}>
              {t('Close')}
            </SheetClose>
          </SheetFooter>
        </SheetContent>
      </Sheet>

      {/* 删除候选确认弹窗：与服务端已有候选的破坏性删除语义对齐喵。 */}
      <AlertDialog open={pendingDeleteIndex !== null} onOpenChange={(open) => { if (!open) setPendingDeleteIndex(null) }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Remove candidate')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t('Removing this candidate permanently deletes its failure rules, manual freezes, and encrypted credentials. Continue?')}
            </AlertDialogDescription>
          </AlertDialogHeader>
          {pendingDeleteIndex !== null && draftCandidates[pendingDeleteIndex] && (
            <Badge variant='secondary'>{candidateDisplayName(draftCandidates[pendingDeleteIndex])}</Badge>
          )}
          <AlertDialogFooter>
            <AlertDialogCancel disabled={isSaving}>{t('Cancel')}</AlertDialogCancel>
            <AlertDialogAction variant='destructive' disabled={isSaving} onClick={() => performRemove(pendingDeleteIndex ?? 0)}>
              {t('Remove')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
