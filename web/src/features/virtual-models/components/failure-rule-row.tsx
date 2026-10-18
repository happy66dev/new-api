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

import { HelpHint } from '@/components/help-hint'
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
  type ConditionType,
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
          {/* 失败条件标签带帮助，说明四种条件分别匹配什么失败喵。 */}
          <span className='flex items-center gap-1'>
            {t('Failure condition')}
            <HelpHint hint={t('Choose which kind of failure this rule matches.')}>
              <p>{t('HTTP status code matches the upstream HTTP status. Timeout matches a request that exceeded its timeout. Stalled stream matches a stream that stopped sending content after it started. Stream cut matches a fake-stream request whose upstream stream ended before [DONE], and only works when Fake stream is enabled in Target Mode.')}</p>
            </HelpHint>
          </span>
          {/* 失败条件二选一：HTTP 状态码或超时，互斥选择喵。大部分失败用状态码即可，超时无状态码需独立覆盖喵。 */}
          <select className='border-input bg-background h-9 rounded-md border px-3 text-sm' value={rule.conditionType} disabled={isSaving} onChange={(event) => onChange({ conditionType: event.target.value as ConditionType })}>
            <option value='http'>{t('HTTP status code')}</option>
            <option value='timeout'>{t('Timeout')}</option>
            <option value='stalled'>{t('Stalled stream')}</option>
            <option value='stream-cut'>{t('Stream cut')}</option>
          </select>
          {/* 断流条件仅在目标模式开启流转伪流时才会出现，需提示用户避免困惑喵。 */}
          {rule.conditionType === 'stream-cut' && <span className='text-xs text-muted-foreground'>{t('Only effective when fake stream is enabled in Target Mode')}</span>}
        </label>
        {/* HTTP 条件：展示状态码输入与常用预设喵。 */}
        {rule.conditionType === 'http' && (
          <label className='grid gap-1 text-sm font-medium'>
            {/* 状态码标签带帮助，说明单值、范围与 0 的写法喵。 */}
            <span className='flex items-center gap-1'>
              {t('HTTP status')}
              <HelpHint hint={t('Status code or range to match. 0 matches any status.')}>
                <p>{t('Use a single code such as 429, an inclusive range such as 500~524, or 0 to match any HTTP status. Values must stay between 0 and 599.')}</p>
              </HelpHint>
            </span>
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
        )}
        {/* 超时条件：无状态码可配置，展示固定说明并可配置超时判定阈值喵。 */}
        {rule.conditionType === 'timeout' && (
          <>
            <label className='grid gap-1 text-sm font-medium'>
              {t('Error class (fixed)')}
              <div className='flex h-9 items-center rounded-md border px-3 text-sm text-muted-foreground'>
                {t('Matches timeout failures')}
              </div>
            </label>
            <label className='grid gap-1 text-sm font-medium'>
              {/* 超时字段带帮助，说明它是自定义候选的执行超时且候选规则优先喵。 */}
              <span className='flex items-center gap-1'>
                {t('Timeout seconds')}
                <HelpHint hint={t('Per-attempt timeout for custom candidates; candidate rules override global rules.')}>
                  <p>{t('Range: 0 to 600 seconds. For custom candidates, a value greater than 0 sets the wall-clock timeout for one upstream attempt. Candidate-level rules are checked before global rules; when all are 0, the referenced upstream model timeout or the candidate timeout is used. For internal candidates this field does not add a separate deadline; the virtual model total timeout is the outer limit.')}</p>
                </HelpHint>
              </span>
              <Input inputMode='numeric' value={rule.timeoutSeconds} disabled={isSaving} placeholder={t('Seconds before a candidate is judged timed out (default: candidate timeout)')} onChange={(event) => onChange({ timeoutSeconds: event.target.value })} />
            </label>
          </>
        )}
        {/* 卡流条件：展示固定说明，并可配置流式探测参数（静默秒数、内容字符门槛、探测总预算）喵。 */}
        {rule.conditionType === 'stalled' && (
          <>
            <label className='grid gap-1 text-sm font-medium'>
              {t('Error class (fixed)')}
              <div className='flex h-9 items-center rounded-md border px-3 text-sm text-muted-foreground'>
                {t('Matches stalled stream failures')}
              </div>
            </label>
            <label className='grid gap-1 text-sm font-medium'>
              {/* 卡流静默秒数带帮助，说明探测会在收到新内容时重置喵。 */}
              <span className='flex items-center gap-1'>
                {t('Stall timeout seconds')}
                <HelpHint hint={t('Silence duration that marks a stream as stalled (default 60).')}>
                  <p>{t('Range: 0 to 600 seconds; 0 uses the backend default of 60 seconds. The value is resolved from candidate rules first, then global rules, then the default. The probe restarts whenever upstream sends new content. This budget only controls the stream probe and does not extend the per-attempt execution timeout.')}</p>
                </HelpHint>
              </span>
              <Input inputMode='numeric' value={rule.stallTimeoutSeconds} disabled={isSaving} placeholder={t('Seconds of silence before judging stalled (default 60)')} onChange={(event) => onChange({ stallTimeoutSeconds: event.target.value })} />
            </label>
            <label className='grid gap-1 text-sm font-medium'>
              {/* 内容字符门槛带帮助，说明提高门槛会延后首个可见字符喵。 */}
              <span className='flex items-center gap-1'>
                {t('Min content chars')}
                <HelpHint hint={t('Characters to buffer before relaying a stream (default 10).')}>
                  <p>{t('Range: 0 to 1024 characters; 0 uses the backend default of 10. The value is resolved from candidate rules first, then global rules, then the default. A higher threshold avoids releasing an almost empty stream before an upstream failure is detected, at the cost of a slightly later first visible character.')}</p>
                </HelpHint>
              </span>
              <Input inputMode='numeric' value={rule.minContentChars} disabled={isSaving} placeholder={t('Content characters to buffer before relaying (default 10)')} onChange={(event) => onChange({ minContentChars: event.target.value })} />
            </label>
            <label className='grid gap-1 text-sm font-medium'>
              {/* 探测总预算带帮助，说明预算耗尽即使未静默也会判定卡流喵。 */}
              <span className='flex items-center gap-1'>
                {t('Probe total timeout seconds')}
                <HelpHint hint={t('Total time budget for the stream probe phase (default 300).')}>
                  <p>{t('Range: 0 to 3600 seconds; 0 uses the backend default of 300. The value is resolved from candidate rules first, then global rules, then the default. This budget limits the whole probe phase and is separate from the per-attempt execution timeout. When it is exhausted, the stream is treated as stalled even if the silence threshold has not been reached.')}</p>
                </HelpHint>
              </span>
              <Input inputMode='numeric' value={rule.probeTotalTimeoutSeconds} disabled={isSaving} placeholder={t('Total budget for the probe phase (default 300)')} onChange={(event) => onChange({ probeTotalTimeoutSeconds: event.target.value })} />
            </label>
          </>
        )}
        <label className='grid gap-1 text-sm font-medium'>
          {/* 动作标签带帮助，说明四种动作对候选链的影响喵。 */}
          <span className='flex items-center gap-1'>
            {t('Action')}
            <HelpHint hint={t('What the gateway does when this rule matches.')}>
              <p>{t('Retry current candidate retries the same candidate. Use next candidate moves to the next node in the chain. Freeze candidate marks it unavailable for a period. Return upstream error stops routing and returns the upstream error to the client.')}</p>
            </HelpHint>
          </span>
          <select className='border-input bg-background h-9 rounded-md border px-3 text-sm' value={rule.action} disabled={isSaving} onChange={(event) => onChange({ action: event.target.value as FailureRuleDraft['action'] })}>
            <option value='retry'>{t('Retry current candidate')}</option>
            <option value='next'>{t('Use next candidate')}</option>
            <option value='freeze'>{t('Freeze candidate')}</option>
            <option value='passthrough'>{t('Return upstream error')}</option>
          </select>
          {/* 重试次数只在 retry 动作时展示，表示本规则最多重试当前候选的次数喵。 */}
          {rule.action === 'retry' && (
            <span className='text-xs text-muted-foreground'>{t('Capped at the rule retry count, otherwise the candidate default')}</span>
          )}
        </label>
        {/* 重试动作的最大重试次数输入：零表示未配置时沿用候选 MaxRetries 喵。 */}
        {rule.action === 'retry' && (
          <label className='grid gap-1 text-sm font-medium'>
            {/* 规则级重试次数带帮助，说明 0 会沿用候选默认值喵。 */}
            <span className='flex items-center gap-1'>
              {t('Max retries')}
              <HelpHint hint={t('Retries for this rule; 0 uses the candidate Maximum retries.')}>
                <p>{t("Only used by Retry current candidate. Range: 0 to 20. When greater than 0, it replaces the candidate Maximum retries for this rule; when 0, the candidate value is used. The total request timeout and auto hedge freeze can still stop retries earlier.")}</p>
              </HelpHint>
            </span>
            <Input inputMode='numeric' value={rule.retryCount} disabled={isSaving} placeholder={t('0 = candidate default, up to 20')} onChange={(event) => onChange({ retryCount: event.target.value })} />
          </label>
        )}
        {/* 冻结秒数只在冻结动作时展示，避免其它动作暴露无意义的冻结配置喵。 */}
        {rule.action === 'freeze' && (
          <>
            <label className='grid gap-1 text-sm font-medium'>
              {/* 固定冻结时长带帮助，说明 0 会改为从响应体解析喵。 */}
              <span className='flex items-center gap-1'>
                {t('Freeze seconds')}
                <HelpHint hint={t('Fixed freeze duration. 0 parses the duration from the response body.')}>
                  <p>{t('Range: 0 to 86400 seconds. A fixed value is applied immediately after this rule matches. 0 means the gateway reads the duration from the response body using the advanced settings below.')}</p>
                </HelpHint>
              </span>
              <Input inputMode='numeric' value={rule.freezeSeconds} disabled={isSaving} placeholder={t('0 = auto from the response body field')} onChange={(event) => onChange({ freezeSeconds: event.target.value })} />
            </label>
            {/* 高级冻结：从响应体指定字段解析冻结时间，适配上游不返回 Retry-After 头的情况喵。 */}
            <div className='grid gap-3 rounded-md border border-dashed p-3 sm:col-span-3'>
              {/* 高级冻结标题带帮助，说明适用于上游不返回 Retry-After 的场景喵。 */}
              <span className='flex items-center gap-1'>
                <p className='text-sm font-medium'>{t('Advanced freeze from response body')}</p>
                <HelpHint hint={t('Read the freeze duration from a field in the upstream response body.')}>
                  <p>{t('Use this when the upstream does not return a Retry-After header. The gateway reads the configured field and converts its value with the selected Freeze unit. Auto mode scans the whole response body for natural-language durations such as "in 22 minutes" or "after 5 seconds".')}</p>
                </HelpHint>
              </span>
              <div className='grid gap-3 sm:grid-cols-2'>
                {rule.freezeUnit === 'auto' ? (
                  // auto 模式自动扫描响应体中的自然语言时间，无需用户指定字段名喵。
                  <p className='text-sm text-muted-foreground'>{t('Auto-detects a duration like "in 22 minutes" or "after 5 seconds" anywhere in the response body')}</p>
                ) : (
                  <label className='grid gap-1 text-sm font-medium'>
                    {/* 响应体字段名带帮助，说明只支持顶层字段喵。 */}
                    <span className='flex items-center gap-1'>
                      {t('Response body field')}
                      <HelpHint hint={t('Response body field that contains the freeze duration.')}>
                        <p>{t('Enter the exact field name, for example retry_after. The field must appear at the top level of the response JSON; nested values are not supported. Auto mode in Freeze unit does not use this field.')}</p>
                      </HelpHint>
                    </span>
                    <Input value={rule.freezeField} disabled={isSaving} placeholder={t('Field name holding the freeze time, e.g. retry_after')} onChange={(event) => onChange({ freezeField: event.target.value })} />
                  </label>
                )}
                <label className='grid gap-1 text-sm font-medium'>
                  {/* 冻结单位带帮助，说明四种单位的换算方式喵。 */}
                  <span className='flex items-center gap-1'>
                    {t('Freeze unit')}
                    <HelpHint hint={t('How to interpret the value read from the response body.')}>
                      <p>{t('Seconds treats the value as seconds. Minutes multiplies it by 60. Mixed accepts formats such as 1m30s. Auto ignores the field name and scans the response body for natural-language durations.')}</p>
                    </HelpHint>
                  </span>
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
            {/* 响应体正则标签带帮助，说明四种匹配模式的区别喵。 */}
            <span className='flex items-center gap-1'>
              {t('Response body regex')}
              <HelpHint hint={t('Optionally require the response body to match this rule.')}>
                <p>{t('Do not match ignores the body. Use a preset picks a built-in upstream error pattern. Simple text match checks whether the body contains plain text. Custom regex matches a regular expression you write.')}</p>
              </HelpHint>
            </span>
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
                {/* 预设标签带帮助，说明预设覆盖的常见上游错误喵。 */}
                <span className='flex items-center gap-1'>
                  {t('Preset')}
                  <HelpHint hint={t('Built-in pattern for common upstream errors.')}>
                    <p>{t('Presets cover rate limiting, overload or capacity, insufficient quota, context length exceeded, and temporarily unavailable. Selecting a preset shows the text pattern it matches.')}</p>
                  </HelpHint>
                </span>
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
              {/* 简易文本匹配带帮助，说明按字面量且区分大小写喵。 */}
              <span className='flex items-center gap-1'>
                {t('Text to match')}
                <HelpHint hint={t('Plain text that must appear in the response body.')}>
                  <p>{t('Matching is case-sensitive and treats the text literally; regular expression syntax is not interpreted. Use this instead of a custom regex when you only need a simple substring check.')}</p>
                </HelpHint>
              </span>
              <Input value={rule.bodyRegexSimple} disabled={isSaving} placeholder={t('Matches response bodies containing this text')} onChange={(event) => onChange({ bodyRegexSimple: event.target.value })} />
            </label>
          )}
          {rule.bodyRegexMode === 'custom' && (
            <label className='grid gap-1 text-sm font-medium'>
              {/* 自定义正则带帮助，说明非法表达式会在保存前被拒绝喵。 */}
              <span className='flex items-center gap-1'>
                {t('Regular expression')}
                <HelpHint hint={t('Regular expression applied to the response body.')}>
                  <p>{t('The expression is matched against the raw response body text. Invalid expressions are rejected before saving. Prefer a preset or simple text match when possible.')}</p>
                </HelpHint>
              </span>
              <Input value={rule.bodyRegex} disabled={isSaving} placeholder={t('Regular expression matched against the response body')} onChange={(event) => onChange({ bodyRegex: event.target.value })} />
            </label>
          )}
        </div>
      </div>
    </div>
  )
}
