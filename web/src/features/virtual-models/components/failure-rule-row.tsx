/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { ArrowDown01Icon, ArrowUp01Icon, Cancel01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

import {
  BODY_REGEX_PRESETS,
  COMMON_HTTP_STATUS_RANGES,
  COMMON_HTTP_STATUSES,
  FREEZE_UNITS,
  type BodyRegexMode,
  type FailureRuleDraft,
  type FreezeUnit,
} from '../lib/failure-rules'

// FailureRuleEditorRow 渲染单条失败规则的完整编辑表单喵。
// 状态码提供常用预设、错误分类为明确下拉、冻结秒数仅在冻结动作时显示、响应体正则支持预设/简易/自定义模式喵。
export function FailureRuleEditorRow({
  rule,
  index,
  total,
  isSaving,
  onChange,
  onMove,
  onRemove,
}: {
  rule: FailureRuleDraft
  index: number
  total: number
  isSaving: boolean
  onChange: (patch: Partial<FailureRuleDraft>) => void
  onMove: (direction: 'up' | 'down') => void
  onRemove: () => void
}) {
  const { t } = useTranslation()

  return (
    <div className='space-y-3 rounded-md border p-3'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <Badge variant='secondary'>{index + 1}</Badge>
        <div className='flex gap-1'>
          <Button type='button' size='icon-sm' variant='ghost' disabled={isSaving || index === 0} onClick={() => onMove('up')} aria-label={t('Move failure rule up')}>
            <HugeiconsIcon icon={ArrowUp01Icon} strokeWidth={2} aria-hidden='true' />
          </Button>
          <Button type='button' size='icon-sm' variant='ghost' disabled={isSaving || index === total - 1} onClick={() => onMove('down')} aria-label={t('Move failure rule down')}>
            <HugeiconsIcon icon={ArrowDown01Icon} strokeWidth={2} aria-hidden='true' />
          </Button>
          <Button type='button' size='icon-sm' variant='ghost' disabled={isSaving} onClick={onRemove} aria-label={t('Remove failure rule')}>
            <HugeiconsIcon icon={Cancel01Icon} strokeWidth={2} aria-hidden='true' />
          </Button>
        </div>
      </div>

      <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-3'>
        <label className='grid gap-1 text-sm font-medium'>
          {t('HTTP status')}
          <Input value={rule.httpStatus} disabled={isSaving} placeholder={t('0 = any, e.g. 429 or 500~524')} onChange={(event) => onChange({ httpStatus: event.target.value })} />
          {/* 常用状态码预设：点击填入，再次点击清除喵。 */}
          <span className='flex flex-wrap gap-1'>
            {COMMON_HTTP_STATUSES.map((status) => {
              const active = Number(rule.httpStatus) === status
              return (
                <button
                  type='button'
                  key={status}
                  disabled={isSaving}
                  className={cn(
                    'rounded-full border px-2 py-0.5 text-xs transition-colors',
                    active ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-muted'
                  )}
                  onClick={() => onChange({ httpStatus: active ? '0' : String(status) })}
                >
                  {status}
                </button>
              )
            })}
            {/* 常用状态码范围预设：点击填入范围文本，再次点击清除喵。 */}
            {COMMON_HTTP_STATUS_RANGES.map((rangeText) => {
              const active = rule.httpStatus.trim() === rangeText
              return (
                <button
                  type='button'
                  key={rangeText}
                  disabled={isSaving}
                  className={cn(
                    'rounded-full border px-2 py-0.5 text-xs transition-colors',
                    active ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-muted'
                  )}
                  onClick={() => onChange({ httpStatus: active ? '0' : rangeText })}
                >
                  {rangeText}
                </button>
              )
            })}
          </span>
        </label>
        <label className='grid gap-1 text-sm font-medium'>
          {t('Action')}
          <select className='border-input bg-background h-9 rounded-md border px-3 text-sm' value={rule.action} disabled={isSaving} onChange={(event) => onChange({ action: event.target.value as FailureRuleDraft['action'] })}>
            <option value='retry'>{t('Retry current candidate')}</option>
            <option value='next'>{t('Use next candidate')}</option>
            <option value='freeze'>{t('Freeze candidate')}</option>
            <option value='passthrough'>{t('Return upstream error')}</option>
          </select>
        </label>
        {/* 冻结秒数只在冻结动作时展示，避免其它动作暴露无意义的冻结配置喵。 */}
        {rule.action === 'freeze' && (
          <>
            <label className='grid gap-1 text-sm font-medium'>
              {t('Freeze seconds')}
              <Input inputMode='numeric' value={rule.freezeSeconds} disabled={isSaving} placeholder={t('0 = auto from the response body field')} onChange={(event) => onChange({ freezeSeconds: event.target.value })} />
            </label>
            {/* 高级冻结：从响应体指定字段解析冻结时间，适配上游不返回 Retry-After 头的情况喵。 */}
            <div className='grid gap-3 rounded-md border border-dashed p-3 sm:col-span-3'>
              <p className='text-sm font-medium'>{t('Advanced freeze from response body')}</p>
              <div className='grid gap-3 sm:grid-cols-2'>
                {rule.freezeUnit === 'auto' ? (
                  // auto 模式自动扫描响应体中的自然语言时间，无需用户指定字段名喵。
                  <p className='text-sm text-muted-foreground'>{t('Auto-detects a duration like "in 22 minutes" or "after 5 seconds" anywhere in the response body')}</p>
                ) : (
                  <label className='grid gap-1 text-sm font-medium'>
                    {t('Response body field')}
                    <Input value={rule.freezeField} disabled={isSaving} placeholder={t('Field name holding the freeze time, e.g. retry_after')} onChange={(event) => onChange({ freezeField: event.target.value })} />
                  </label>
                )}
                <label className='grid gap-1 text-sm font-medium'>
                  {t('Freeze unit')}
                  <select className='border-input bg-background h-9 rounded-md border px-3 text-sm' value={rule.freezeUnit} disabled={isSaving} onChange={(event) => onChange({ freezeUnit: event.target.value as FreezeUnit })}>
                    {FREEZE_UNITS.map((unit) => (
                      <option key={unit.value} value={unit.value}>{t(unit.labelKey)}</option>
                    ))}
                  </select>
                </label>
              </div>
            </div>
          </>
        )}

        {/* 响应体正则区域：预设、简易文本与自定义正则三种模式喵。 */}
        <div className='grid gap-3 sm:col-span-2'>
          <label className='grid gap-1 text-sm font-medium'>
            {t('Response body regex')}
            <select className='border-input bg-background h-9 rounded-md border px-3 text-sm' value={rule.bodyRegexMode} disabled={isSaving} onChange={(event) => onChange({ bodyRegexMode: event.target.value as BodyRegexMode })}>
              <option value='none'>{t('Do not match the response body')}</option>
              <option value='preset'>{t('Use a preset')}</option>
              <option value='simple'>{t('Simple text match')}</option>
              <option value='custom'>{t('Custom regex')}</option>
            </select>
          </label>
          {rule.bodyRegexMode === 'preset' && (
            <div className='grid gap-1'>
              <label className='grid gap-1 text-sm font-medium'>
                {t('Preset')}
                <select className='border-input bg-background h-9 rounded-md border px-3 text-sm' value={rule.bodyRegexPreset} disabled={isSaving} onChange={(event) => onChange({ bodyRegexPreset: event.target.value })}>
                  <option value=''>{t('Select a preset')}</option>
                  {Object.entries(BODY_REGEX_PRESETS).map(([presetKey, preset]) => (
                    <option key={presetKey} value={presetKey}>{t(preset.labelKey)}</option>
                  ))}
                </select>
              </label>
              {/* 已选预设时提示其匹配的文字特征，方便用户确认正则语义喵。 */}
              {rule.bodyRegexPreset !== '' && BODY_REGEX_PRESETS[rule.bodyRegexPreset]?.descriptionKey != null && (
                <p className='text-xs text-muted-foreground'>{t(BODY_REGEX_PRESETS[rule.bodyRegexPreset].descriptionKey)}</p>
              )}
            </div>
          )}
          {rule.bodyRegexMode === 'simple' && (
            <label className='grid gap-1 text-sm font-medium'>
              {t('Text to match')}
              <Input value={rule.bodyRegexSimple} disabled={isSaving} placeholder={t('Matches response bodies containing this text')} onChange={(event) => onChange({ bodyRegexSimple: event.target.value })} />
            </label>
          )}
          {rule.bodyRegexMode === 'custom' && (
            <label className='grid gap-1 text-sm font-medium'>
              {t('Regular expression')}
              <Input value={rule.bodyRegex} disabled={isSaving} placeholder={t('Regular expression matched against the response body')} onChange={(event) => onChange({ bodyRegex: event.target.value })} />
            </label>
          )}
        </div>
      </div>
    </div>
  )
}
