/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { Add01Icon, ArrowDown01Icon, ArrowUp01Icon, Cancel01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

import type { VirtualModel, VirtualModelFailureRule } from '../api'
import { replaceVirtualModelCandidateFailureRules } from '../api'

// FailureRuleDraft 保存单条失败规则的受控编辑状态，字符串字段避免输入中间态被过早截断喵。
type FailureRuleDraft = {
  bodyRegex: string
  errorClass: string
  freezeSeconds: string
  httpStatus: string
  id?: number
  action: VirtualModelFailureRule['action']
}

// MAXIMUM_FAILURE_RULES 限制单个候选的规则数，必须与控制面安全上限一致喵。
const MAXIMUM_FAILURE_RULES = 32
// MAXIMUM_HTTP_STATUS 是合法 HTTP 状态码的控制面最大值喵。
const MAXIMUM_HTTP_STATUS = 599
// MAXIMUM_FREEZE_SECONDS 限制单条规则最多冻结一个自然日喵。
const MAXIMUM_FREEZE_SECONDS = 24 * 60 * 60

// toFailureRuleDraft 将读取响应映射为可编辑草稿，并为缺失字段提供明确默认值喵。
function toFailureRuleDraft(rule: VirtualModelFailureRule): FailureRuleDraft {
  return {
    bodyRegex: rule.body_regex ?? '',
    errorClass: rule.error_class ?? '',
    freezeSeconds: String(rule.freeze_seconds ?? 0),
    httpStatus: String(rule.http_status ?? 0),
    id: rule.id,
    action: rule.action ?? 'next',
  }
}

// createFailureRuleDraft 创建默认兜底规则草稿，默认动作与未命中规则时的候选切换语义对齐喵。
function createFailureRuleDraft(): FailureRuleDraft {
  return {
    bodyRegex: '',
    errorClass: '',
    freezeSeconds: '0',
    httpStatus: '0',
    action: 'next',
  }
}

// validateFailureRuleDraft 将用户输入转换为 API 结构，并尽早阻止越界和不完整数值喵。
function validateFailureRuleDraft(
  rule: FailureRuleDraft,
  index: number,
  t: (key: string, options?: Record<string, unknown>) => string
): VirtualModelFailureRule {
  // 将状态码文本转换为数值，零表示不限制状态码喵。
  const httpStatus = Number(rule.httpStatus)
  // 将冻结秒数文本转换为数值，零表示不追加固定冻结时间喵。
  const freezeSeconds = Number(rule.freezeSeconds)
  // 喵~防御：状态码必须是 0 到 599 的整数，避免后端请求因输入中间态而失败喵。
  if (!Number.isInteger(httpStatus) || httpStatus < 0 || httpStatus > MAXIMUM_HTTP_STATUS) {
    throw new Error(t('Failure rule {{index}} HTTP status must be between 0 and 599', { index: index + 1 }))
  }
  // 喵~防御：冻结时长必须处于零到一天，防止意外长期冻结候选喵。
  if (!Number.isInteger(freezeSeconds) || freezeSeconds < 0 || freezeSeconds > MAXIMUM_FREEZE_SECONDS) {
    throw new Error(t('Failure rule {{index}} freeze duration must be between 0 and 86400 seconds', { index: index + 1 }))
  }
  // 返回后端期待的下划线字段，空条件表示该维度不限制匹配喵。
  return {
    id: rule.id,
    http_status: httpStatus,
    error_class: rule.errorClass.trim(),
    body_regex: rule.bodyRegex.trim(),
    action: rule.action,
    freeze_seconds: freezeSeconds,
  }
}

// VirtualModelCandidateFailureRulesEditor 管理单个候选按从上到下首条命中的失败规则喵。
export function VirtualModelCandidateFailureRulesEditor({
  candidateID,
  candidateLabel,
  model,
  rules,
  onSaved,
}: {
  candidateID: number
  candidateLabel: string
  model: VirtualModel
  rules: VirtualModelFailureRule[]
  onSaved: () => void
}) {
  // 读取双语翻译函数，保持控制台文案与现有页面一致喵。
  const { t } = useTranslation()
  // 保存规则草稿顺序，数组下标就是规则首条命中优先级喵。
  const [draftRules, setDraftRules] = useState<FailureRuleDraft[]>([])
  // 保存中状态阻止并发编辑和重复提交喵。
  const [isSaving, setIsSaving] = useState(false)

  // 当候选或服务端模型版本改变时，以最新响应重建规则草稿喵。
  useEffect(() => {
    setDraftRules(rules.map(toFailureRuleDraft))
  }, [candidateID, model.version, rules])

  // updateRule 精确更新指定规则的局部字段，其他规则保持原有顺序与内容喵。
  const updateRule = (index: number, patch: Partial<FailureRuleDraft>) => {
    setDraftRules((currentRules) =>
      currentRules.map((rule, ruleIndex) => (ruleIndex === index ? { ...rule, ...patch } : rule))
    )
  }

  // moveRule 在数组内交换规则，直观表达第一条命中优先级喵。
  const moveRule = (index: number, direction: 'up' | 'down') => {
    // 计算目标规则位置，上移减少下标、下移增加下标喵。
    const targetIndex = direction === 'up' ? index - 1 : index + 1
    // 喵~防御：第一条不能继续上移、最后一条不能继续下移，避免数组越界喵。
    if (targetIndex < 0 || targetIndex >= draftRules.length) return
    setDraftRules((currentRules) => {
      // 复制数组，避免直接修改 React state 喵。
      const nextRules = [...currentRules]
      // 交换当前规则和目标规则，以保存首条命中顺序喵。
      ;[nextRules[index], nextRules[targetIndex]] = [nextRules[targetIndex], nextRules[index]]
      // 返回新的规则数组触发界面更新喵。
      return nextRules
    })
  }

  // removeRule 从本地草稿中移除指定规则，保存前不会改变服务端配置喵。
  const removeRule = (index: number) => {
    setDraftRules((currentRules) => currentRules.filter((_, ruleIndex) => ruleIndex !== index))
  }

  // addRule 在规则数量未到安全上限时追加一条默认规则喵。
  const addRule = () => {
    // 喵~防御：前端同步后端的 32 条上限，避免用户编辑后才收到请求失败喵。
    if (draftRules.length >= MAXIMUM_FAILURE_RULES) {
      toast.error(t('A candidate can contain at most 32 failure rules'))
      return
    }
    // 追加默认草稿，供用户选择匹配条件和动作喵。
    setDraftRules((currentRules) => [...currentRules, createFailureRuleDraft()])
  }

  // saveRules 将整个有序规则集与当前模型 version 一并原子替换喵。
  const saveRules = async () => {
    try {
      // 设置保存态，防止请求进行期间再次提交或重排喵。
      setIsSaving(true)
      // 在发请求前逐条校验，将草稿转换成 API 载荷喵。
      const validatedRules = draftRules.map((rule, index) => validateFailureRuleDraft(rule, index, t))
      // 通过候选范围接口提交模型版本与完整规则链喵。
      const response = await replaceVirtualModelCandidateFailureRules(model.id, candidateID, {
        version: model.version,
        rules: validatedRules,
      })
      // 喵~防御：业务失败响应必须显示后端消息，不能把过期版本伪装成保存成功喵。
      if (!response.success) {
        throw new Error(response.message || t('Unable to save failure rules'))
      }
      // 告知用户规则已落盘，再由父页面刷新最新模型版本与候选数据喵。
      toast.success(t('Failure rules saved'))
      onSaved()
    } catch (error) {
      // 喵~防御：网络、解析和业务错误统一显示安全错误消息，避免未处理 Promise 拒绝喵。
      toast.error(error instanceof Error ? error.message : t('Unable to save failure rules'))
    } finally {
      // 无论成功还是失败均解除保存态，允许用户修正后重试喵。
      setIsSaving(false)
    }
  }

  return (
    <div className='space-y-4 rounded-md border p-4'>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <div>
          <h3 className='font-medium'>{t('Failure Rules')}</h3>
          <p className='text-muted-foreground text-sm'>{t('Rules are evaluated from top to bottom. The first matching rule decides the candidate action.')}</p>
        </div>
        <Button type='button' size='sm' variant='outline' onClick={addRule} disabled={isSaving}>
          <HugeiconsIcon icon={Add01Icon} strokeWidth={2} aria-hidden='true' />
          {t('Add failure rule')}
        </Button>
      </div>

      <p className='text-muted-foreground text-xs'>{t('Editing rules for candidate: {{candidate}}', { candidate: candidateLabel })}</p>

      {draftRules.map((rule, index) => (
        <div className='space-y-3 rounded-md border p-3' key={rule.id ?? `new-rule-${index}`}>
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <Badge variant='secondary'>{index + 1}</Badge>
            <div className='flex gap-1'>
              <Button type='button' size='icon-sm' variant='ghost' disabled={isSaving || index === 0} onClick={() => moveRule(index, 'up')} aria-label={t('Move failure rule up')}>
                <HugeiconsIcon icon={ArrowUp01Icon} strokeWidth={2} aria-hidden='true' />
              </Button>
              <Button type='button' size='icon-sm' variant='ghost' disabled={isSaving || index === draftRules.length - 1} onClick={() => moveRule(index, 'down')} aria-label={t('Move failure rule down')}>
                <HugeiconsIcon icon={ArrowDown01Icon} strokeWidth={2} aria-hidden='true' />
              </Button>
              <Button type='button' size='icon-sm' variant='ghost' disabled={isSaving} onClick={() => removeRule(index)} aria-label={t('Remove failure rule')}>
                <HugeiconsIcon icon={Cancel01Icon} strokeWidth={2} aria-hidden='true' />
              </Button>
            </div>
          </div>

          <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-3'>
            <label className='grid gap-1 text-sm font-medium'>
              {t('HTTP status')}
              <Input inputMode='numeric' value={rule.httpStatus} disabled={isSaving} placeholder={t('0 means any status')} onChange={(event) => updateRule(index, { httpStatus: event.target.value })} />
            </label>
            <label className='grid gap-1 text-sm font-medium'>
              {t('Error class')}
              <Input value={rule.errorClass} disabled={isSaving} placeholder={t('Leave blank to match any error class')} onChange={(event) => updateRule(index, { errorClass: event.target.value })} />
            </label>
            <label className='grid gap-1 text-sm font-medium'>
              {t('Action')}
              <select className='border-input bg-background h-9 rounded-md border px-3 text-sm' value={rule.action} disabled={isSaving} onChange={(event) => updateRule(index, { action: event.target.value as VirtualModelFailureRule['action'] })}>
                <option value='retry'>{t('Retry current candidate')}</option>
                <option value='next'>{t('Use next candidate')}</option>
                <option value='freeze'>{t('Freeze candidate')}</option>
                <option value='passthrough'>{t('Return upstream error')}</option>
              </select>
            </label>
            <label className='grid gap-1 text-sm font-medium sm:col-span-2'>
              {t('Response body regex')}
              <Input value={rule.bodyRegex} disabled={isSaving} placeholder={t('Leave blank to match any response body')} onChange={(event) => updateRule(index, { bodyRegex: event.target.value })} />
            </label>
            <label className='grid gap-1 text-sm font-medium'>
              {t('Freeze seconds')}
              <Input inputMode='numeric' value={rule.freezeSeconds} disabled={isSaving} placeholder='0' onChange={(event) => updateRule(index, { freezeSeconds: event.target.value })} />
            </label>
          </div>
        </div>
      ))}

      {draftRules.length === 0 && <p className='text-muted-foreground text-sm'>{t('No failure rules configured. Unmatched failures use the next candidate.')}</p>}
      <div className='flex justify-end'>
        <Button type='button' onClick={() => void saveRules()} disabled={isSaving}>
          {isSaving ? t('Saving') : t('Save failure rules')}
        </Button>
      </div>
    </div>
  )
}
