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
import { HelpHint } from '@/components/help-hint'
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

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
  // 流转伪流配置：全量缓存到 [DONE] 后一次性伪流发出，断流失败由全局兜底失败规则统一决策喵。
  const [fakeStreamEnabled, setFakeStreamEnabled] = useState(false)
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
    setFakeStreamEnabled(model?.fake_stream_enabled ?? false)
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
      fake_stream_enabled: fakeStreamEnabled,
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
          <Tabs defaultValue='basic' className='w-full'>
            <TabsList className='w-full justify-start'>
              <TabsTrigger value='basic'>{t('Basic Information')}</TabsTrigger>
              {/* 目标模式选项卡承载候选循环重试配置，参考 autoapi 的 target_mode 语义喵。 */}
              <TabsTrigger value='target'>{t('Target Mode')}</TabsTrigger>
            </TabsList>
            {/* 基本信息选项卡：只保留名称信息与是否启用喵。 */}
            <TabsContent className='mt-4' value='basic'>
              <SideDrawerSection>
                <SideDrawerSectionHeader
                  title={t('Basic Information')}
                  description={t('Set virtual model name and enable state')}
                  icon={<Settings2 className='size-4' />}
                  iconTone='info'
                />
                <label className='grid gap-1 text-sm font-medium'>
                  {/* 名称标签与帮助图标同排，悬停看短提示、点击看完整教程喵。 */}
                  <span className='flex items-center gap-1'>
                    {t('Virtual model name')}
                    <HelpHint hint={t('The model identifier after virtual/. It cannot be changed after creation.')}>
                      <p>{t('Only ASCII letters, digits, hyphens, and underscores are allowed, up to 96 characters. API calls use the full name virtual/<name>. Choose a stable name because it cannot be modified after creation.')}</p>
                    </HelpHint>
                  </span>
                  <Input
                    value={normalizedName}
                    onChange={(event) => setNormalizedName(event.target.value)}
                    placeholder='research-route'
                    disabled={Boolean(model) || isSavingBasics}
                  />
                </label>
                <label className='grid gap-1 text-sm font-medium'>
                  {/* 显示名帮助说明只影响管理界面、不进入 API 调用喵。 */}
                  <span className='flex items-center gap-1'>
                    {t('Display name')}
                    <HelpHint hint={t('The human-readable name shown in the management console.')}>
                      <p>{t('The display name is only used in the management UI and can be changed at any time. It does not affect API calls. Maximum 128 characters.')}</p>
                    </HelpHint>
                  </span>
                  <Input value={displayName} onChange={(event) => setDisplayName(event.target.value)} disabled={isSavingBasics} />
                </label>
                <label className='flex items-center justify-between gap-3 text-sm'>
                  {/* 启用开关帮助说明关闭后的调用与数据保留行为喵。 */}
                  <span className='flex items-center gap-1'>
                    {t('Enabled')}
                    <HelpHint hint={t('Controls whether this virtual model accepts API requests.')}>
                      <p>{t('When disabled, requests to virtual/<name> are rejected and the model cannot be selected for new calls. Existing candidates, rules, and statistics are kept, so you can enable it again at any time.')}</p>
                    </HelpHint>
                  </span>
                  <Switch checked={enabled} onCheckedChange={setEnabled} disabled={isSavingBasics} />
                </label>
              </SideDrawerSection>
            </TabsContent>
            {/* 目标模式选项卡：候选链循环重试的等待与轮数配置喵。 */}
            <TabsContent className='mt-4' value='target'>
              <SideDrawerSection>
                <SideDrawerSectionHeader
                  title={t('Target Mode')}
                  description={t('Configure candidate loop retry behavior')}
                  icon={<Settings2 className='size-4' />}
                  iconTone='info'
                />
                <label className='flex items-center justify-between gap-3 text-sm'>
                  {/* 循环开关帮助说明用尽候选链后的重试行为喵。 */}
                  <span className='flex items-center gap-1'>
                    {t('Enable candidate loop')}
                    <HelpHint hint={t('Starts again from the first candidate after the chain is exhausted.')}>
                      <p>{t('When enabled, the gateway loops through the candidate chain up to the maximum loop rounds instead of returning a failure after the last candidate. Each round still respects the total timeout.')}</p>
                    </HelpHint>
                  </span>
                  <Switch checked={loopEnabled} onCheckedChange={setLoopEnabled} disabled={isSavingBasics} />
                </label>
                <div className='grid grid-cols-2 gap-3'>
                  <label className='grid gap-1 text-sm font-medium'>
                    {/* 总超时帮助说明计时覆盖候选、重试、循环与上游响应喵。 */}
                    <span className='flex items-center gap-1'>
                      {t('Total timeout seconds')}
                      <HelpHint hint={t('Overall time budget for one request, including all candidates and loops.')}>
                        <p>{t('Range: 1 to 3600 seconds. The timer starts when the request enters the virtual model and covers candidate selection, retries, loops, and the upstream response. When it expires, the request fails with a timeout.')}</p>
                      </HelpHint>
                    </span>
                    <Input inputMode='numeric' value={totalTimeoutSeconds} onChange={(event) => setTotalTimeoutSeconds(event.target.value)} disabled={isSavingBasics} />
                  </label>
                  <label className='grid gap-1 text-sm font-medium'>
                    {/* 循环轮数帮助说明一轮等于完整遍历一次候选链喵。 */}
                    <span className='flex items-center gap-1'>
                      {t('Maximum loop rounds')}
                      <HelpHint hint={t('How many times the candidate chain may loop (1 to 100).')}>
                        <p>{t('Only used when Enable candidate loop is on. One round means one full pass through the enabled candidates in order. The total timeout takes precedence, so a loop may end earlier.')}</p>
                      </HelpHint>
                    </span>
                    <Input inputMode='numeric' value={maxLoopRounds} onChange={(event) => setMaxLoopRounds(event.target.value)} disabled={isSavingBasics} />
                  </label>
                </div>
                <div className='border-border/60 bg-muted/25 space-y-3 rounded-lg border p-3'>
                  <label className='flex items-center justify-between gap-3 text-sm'>
                    {/* 流转伪流帮助说明缓存回放带来的延迟与内存权衡喵。 */}
                    <span className='flex items-center gap-1.5'>
                      {t('Fake stream (cache then replay)')}
                      <HelpHint hint={t('Buffers the upstream stream and replays it after [DONE].')}>
                        <p>{t('When enabled, chunks are not forwarded as they arrive. The gateway keeps the complete upstream stream in memory until [DONE], then sends it to the client at once. This resists intermittent upstream disconnects, but increases first-token latency and memory use for long responses. Logs still record the request as streaming, and stream-cut failures are handled by the global fallback failure rules.')}</p>
                      </HelpHint>
                    </span>
                    <Switch checked={fakeStreamEnabled} onCheckedChange={setFakeStreamEnabled} disabled={isSavingBasics} />
                  </label>
                  <p className='text-muted-foreground text-xs'>
                    {t('When enabled, upstream streaming is fully buffered until [DONE] then replayed to the client at once, resisting network fluctuation cut-offs. Logs still record streaming. Stream cut failures are handled by the global fallback failure rules.')}
                  </p>
                </div>
              </SideDrawerSection>
            </TabsContent>
          </Tabs>
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
