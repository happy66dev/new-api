/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { useQueryClient } from '@tanstack/react-query'
import { KeyRound, ListTree, Settings2, ShieldCheck } from 'lucide-react'
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

import { createVirtualModel, updateVirtualModel } from '../api'
import type { VirtualModel, VirtualModelInput } from '../api'
import { VirtualModelBindingsEditor } from './virtual-model-bindings-editor'
import { VirtualModelCandidatesEditor } from './virtual-model-candidates-editor'
import { VirtualModelGlobalFailureRulesEditor } from './virtual-model-global-failure-rules-editor'

// VirtualModelDrawer 提供创建/编辑虚拟模型的一体化抽屉喵。
// 抽屉按 API Key 抽屉风格分段：基本信息、调用链、全局兜底失效规则与 API Key 授权喵。
export function VirtualModelDrawer({
  open,
  onOpenChange,
  model,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  model?: VirtualModel | null
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  // currentModel 跟随抽屉内最新保存结果；创建模式先为空，基本信息保存成功后填入新建模型喵。
  const [currentModel, setCurrentModel] = useState<VirtualModel | null>(model ?? null)

  // 基本信息表单的受控状态，字符串字段避免输入中间态被过早截断喵。
  const [normalizedName, setNormalizedName] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [loopEnabled, setLoopEnabled] = useState(false)
  const [totalTimeoutSeconds, setTotalTimeoutSeconds] = useState('120')
  const [maxLoopRounds, setMaxLoopRounds] = useState('1')
  const [isSavingBasics, setIsSavingBasics] = useState(false)

  // 父组件列表刷新后同步最新模型数据，保证候选/规则编辑基于最新版本喵。
  useEffect(() => {
    setCurrentModel(model ?? null)
  }, [model, open])

  // 当前模型或打开状态变化时重置基本信息表单，避免把上一个模型的字段带入喵。
  useEffect(() => {
    if (!open) return
    setNormalizedName(currentModel?.normalized_name ?? '')
    setDisplayName(currentModel?.display_name ?? '')
    setEnabled(currentModel?.enabled ?? true)
    setLoopEnabled(currentModel?.loop_enabled ?? false)
    setTotalTimeoutSeconds(String(currentModel?.total_timeout_seconds ?? 120))
    setMaxLoopRounds(String(currentModel?.max_loop_rounds ?? 1))
  }, [currentModel, open])

  // refreshVirtualModels 使列表查询失效，父页面会自动重新拉取最新模型数据喵。
  const refreshVirtualModels = () => {
    void queryClient.invalidateQueries({ queryKey: ['virtual-models'] })
  }

  // saveBasics 保存或创建模型基本信息，创建成功后自动进入编辑模式继续配置候选喵。
  const saveBasics = async () => {
    // 喵~防御：创建时必须提供合法资源名，编辑模式沿用既有名称喵。
    const trimmedNormalizedName = normalizedName.trim()
    if (!currentModel && !/^[A-Za-z0-9_-]{1,96}$/.test(trimmedNormalizedName)) {
      toast.error(t('Virtual model name can only contain letters, numbers, hyphens, and underscores'))
      return
    }
    // 喵~防御：显示名不能为空且长度受限，避免保存无意义的空白模型喵。
    if (!displayName.trim() || displayName.trim().length > 128) {
      toast.error(t('Virtual model display name is required'))
      return
    }
    // 喵~防御：总超时必须是 1 到 3600 秒的整数，阻止越界值写入后端喵。
    const totalTimeout = Number(totalTimeoutSeconds)
    if (!Number.isInteger(totalTimeout) || totalTimeout < 1 || totalTimeout > 3600) {
      toast.error(t('Total timeout must be between 1 and 3600 seconds'))
      return
    }
    // 喵~防御：最大循环轮数必须是 1 到 100 的整数喵。
    const maxRounds = Number(maxLoopRounds)
    if (!Number.isInteger(maxRounds) || maxRounds < 1 || maxRounds > 100) {
      toast.error(t('Maximum loop rounds must be between 1 and 100'))
      return
    }
    // 组装创建或更新请求载荷，编辑模式携带版本号做乐观并发控制喵。
    const input: VirtualModelInput = {
      normalized_name: trimmedNormalizedName,
      display_name: displayName.trim(),
      enabled,
      loop_enabled: loopEnabled,
      total_timeout_seconds: totalTimeout,
      max_loop_rounds: maxRounds,
      ...(currentModel ? { version: currentModel.version } : {}),
    }
    try {
      setIsSavingBasics(true)
      const response = currentModel
        ? await updateVirtualModel(currentModel.id, input)
        : await createVirtualModel(input)
      // 喵~防御：业务失败必须抛出后端消息，不能把过期版本伪装成保存成功喵。
      if (!response.success) {
        throw new Error(response.message || t('Unable to save virtual model'))
      }
      // 保存成功后记录最新模型并刷新列表，让后续候选/规则编辑基于新版本喵。
      setCurrentModel(response.data ?? null)
      toast.success(t('Virtual model saved'))
      refreshVirtualModels()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Unable to save virtual model'))
    } finally {
      setIsSavingBasics(false)
    }
  }

  return (
    <Sheet
      open={open}
      onOpenChange={(nextOpen) => {
        onOpenChange(nextOpen)
        // 关闭抽屉时清空内部模型，避免下次打开残留旧模型引用喵。
        if (!nextOpen) {
          setCurrentModel(model ?? null)
        }
      }}
    >
      <SheetContent className={sideDrawerContentClassName('max-w-none sm:!max-w-[720px]')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{currentModel ? t('Edit virtual model') : t('Create virtual model')}</SheetTitle>
          <SheetDescription>
            {currentModel ? t('Update the virtual model by providing necessary info.') : t('Add a new virtual model by providing necessary info.')}
          </SheetDescription>
        </SheetHeader>

        <div className={sideDrawerFormClassName('gap-5')}>
          <SideDrawerSection>
            <SideDrawerSectionHeader
              title={t('Basic Information')}
              description={t('Set virtual model basic information')}
              icon={<Settings2 className='size-4' />}
              iconTone='info'
            />
            <label className='grid gap-1 text-sm font-medium'>
              {t('Virtual model name')}
              <Input
                value={normalizedName}
                onChange={(event) => setNormalizedName(event.target.value)}
                placeholder='research-route'
                disabled={Boolean(currentModel) || isSavingBasics}
              />
            </label>
            <label className='grid gap-1 text-sm font-medium'>
              {t('Display name')}
              <Input value={displayName} onChange={(event) => setDisplayName(event.target.value)} disabled={isSavingBasics} />
            </label>
            <div className='grid grid-cols-2 gap-3'>
              <label className='grid gap-1 text-sm font-medium'>
                {t('Total timeout seconds')}
                <Input inputMode='numeric' value={totalTimeoutSeconds} onChange={(event) => setTotalTimeoutSeconds(event.target.value)} disabled={isSavingBasics} />
              </label>
              <label className='grid gap-1 text-sm font-medium'>
                {t('Maximum loop rounds')}
                <Input inputMode='numeric' value={maxLoopRounds} onChange={(event) => setMaxLoopRounds(event.target.value)} disabled={isSavingBasics} />
              </label>
            </div>
            <label className='flex items-center justify-between gap-3 text-sm'>
              <span>{t('Enabled')}</span>
              <Switch checked={enabled} onCheckedChange={setEnabled} disabled={isSavingBasics} />
            </label>
            <label className='flex items-center justify-between gap-3 text-sm'>
              <span>{t('Enable candidate loop')}</span>
              <Switch checked={loopEnabled} onCheckedChange={setLoopEnabled} disabled={isSavingBasics} />
            </label>
            <div className='flex justify-end'>
              <Button type='button' onClick={() => void saveBasics()} disabled={isSavingBasics}>
                {isSavingBasics ? t('Saving') : currentModel ? t('Save changes') : t('Create virtual model')}
              </Button>
            </div>
          </SideDrawerSection>

          {/* 模型创建成功后才会出现调用链、全局规则与授权分区喵。 */}
          {currentModel && (
            <>
              <SideDrawerSection>
                <SideDrawerSectionHeader
                  title={t('Call chain')}
                  description={t('Ordered candidates used when a request fails.')}
                  icon={<ListTree className='size-4' />}
                  iconTone='info'
                />
                <VirtualModelCandidatesEditor model={currentModel} onSaved={refreshVirtualModels} />
              </SideDrawerSection>

              <SideDrawerSection>
                <SideDrawerSectionHeader
                  title={t('Global fallback failure rules')}
                  description={t('Used when a candidate has no failure rules of its own.')}
                  icon={<ShieldCheck className='size-4' />}
                  iconTone='success'
                />
                <VirtualModelGlobalFailureRulesEditor model={currentModel} onSaved={refreshVirtualModels} />
              </SideDrawerSection>

              <SideDrawerSection>
                <SideDrawerSectionHeader
                  title={t('API Key Authorization')}
                  description={t('Only explicitly authorized API Keys can call this virtual model.')}
                  icon={<KeyRound className='size-4' />}
                  iconTone='info'
                />
                <VirtualModelBindingsEditor model={currentModel} onSaved={refreshVirtualModels} />
              </SideDrawerSection>
            </>
          )}
        </div>

        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' className='w-full sm:w-auto' />}>
            {t('Close')}
          </SheetClose>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
