/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { useQueryClient } from '@tanstack/react-query'
import { Settings2 } from 'lucide-react'
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

// VirtualModelDrawer 提供创建/编辑虚拟模型基本信息的一体化抽屉喵。
// 抽屉只承载模型编辑这一个功能；候选链、失效规则与授权仍在页面选项卡中配置喵。
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
  // 基本信息表单的受控状态，字符串字段避免输入中间态被过早截断喵。
  const [normalizedName, setNormalizedName] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [loopEnabled, setLoopEnabled] = useState(false)
  const [totalTimeoutSeconds, setTotalTimeoutSeconds] = useState('120')
  const [maxLoopRounds, setMaxLoopRounds] = useState('1')
  const [isSavingBasics, setIsSavingBasics] = useState(false)

  // 打开状态或编辑对象变化时重置本地草稿，避免把上一次模型字段带入新模型喵。
  useEffect(() => {
    if (!open) return
    setNormalizedName(model?.normalized_name ?? '')
    setDisplayName(model?.display_name ?? '')
    setEnabled(model?.enabled ?? true)
    setLoopEnabled(model?.loop_enabled ?? false)
    setTotalTimeoutSeconds(String(model?.total_timeout_seconds ?? 120))
    setMaxLoopRounds(String(model?.max_loop_rounds ?? 1))
  }, [model, open])

  // saveBasics 保存或创建模型基本信息，成功后刷新列表让选项卡展示最新配置喵。
  const saveBasics = async () => {
    // 喵~防御：创建时必须提供合法资源名，编辑模式沿用既有名称喵。
    const trimmedNormalizedName = normalizedName.trim()
    if (!model && !/^[A-Za-z0-9_-]{1,96}$/.test(trimmedNormalizedName)) {
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
      ...(model ? { version: model.version } : {}),
    }
    try {
      setIsSavingBasics(true)
      const response = model
        ? await updateVirtualModel(model.id, input)
        : await createVirtualModel(input)
      // 喵~防御：业务失败必须抛出后端消息，不能把过期版本伪装成保存成功喵。
      if (!response.success) {
        throw new Error(response.message || t('Unable to save virtual model'))
      }
      toast.success(t('Virtual model saved'))
      // 刷新列表让选项卡拿到最新模型版本与配置喵。
      void queryClient.invalidateQueries({ queryKey: ['virtual-models'] })
      onOpenChange(false)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Unable to save virtual model'))
    } finally {
      setIsSavingBasics(false)
    }
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className={sideDrawerContentClassName('max-w-none sm:!max-w-[560px]')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{model ? t('Edit virtual model') : t('Create virtual model')}</SheetTitle>
          <SheetDescription>
            {model ? t('Update the virtual model by providing necessary info.') : t('Add a new virtual model by providing necessary info.')}
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
                disabled={Boolean(model) || isSavingBasics}
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
          </SideDrawerSection>
        </div>

        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' className='w-full sm:w-auto' />}>
            {t('Close')}
          </SheetClose>
          <Button type='button' onClick={() => void saveBasics()} disabled={isSavingBasics} className='w-full sm:w-auto'>
            {isSavingBasics ? t('Saving') : model ? t('Save changes') : t('Create virtual model')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
