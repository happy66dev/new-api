/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { Add01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

import type { VirtualModel, VirtualModelFailureRule } from '../api'
import { replaceVirtualModelCandidateFailureRules } from '../api'
import {
  MAXIMUM_FAILURE_RULES,
  createFailureRuleDraft,
  toFailureRuleDraft,
  validateFailureRuleDraft,
  type FailureRuleDraft,
} from '../lib/failure-rules'
import { FailureRuleEditorRow } from './failure-rule-row'

// MAXIMUM_HEDGE_THRESHOLD 限制候选自动避险连续失败阈值上限，与后端控制面一致喵。
const MAXIMUM_HEDGE_THRESHOLD = 1000
// MAXIMUM_HEDGE_FREEZE_SECONDS 限制候选自动避险退避秒数上限，与后端控制面一致喵。
const MAXIMUM_HEDGE_FREEZE_SECONDS = 24 * 60 * 60

// VirtualModelCandidateFailureRulesEditor 管理单个候选按从上到下首条命中的失败规则喵。
// 候选配置了自己的规则后，运行时优先使用这组规则；未命中或未配置时回退模型级全局兜底规则喵。
// 抽屉顶部同时配置该候选的自动避险（连续失败达阈值即冻结退避），随规则一并保存喵。
export function VirtualModelCandidateFailureRulesEditor({
  model,
  candidateID,
  candidateLabel,
  rules,
  hedgeThreshold,
  hedgeFreezeSeconds,
  onSaved,
}: {
  model: VirtualModel
  candidateID: number
  candidateLabel: string
  rules: VirtualModelFailureRule[]
  // hedgeThreshold 当前候选的自动避险连续失败阈值，零表示关闭自动避险喵。
  hedgeThreshold: number
  // hedgeFreezeSeconds 当前候选达到连续失败阈值后的退避冻结秒数喵。
  hedgeFreezeSeconds: number
  onSaved: () => void
}) {
  // 读取双语翻译函数，保持控制台文案与现有页面一致喵。
  const { t } = useTranslation()
  // 保存规则草稿顺序，数组下标就是规则首条命中优先级喵。
  const [draftRules, setDraftRules] = useState<FailureRuleDraft[]>([])
  // 自动避险连续失败阈值文本，空串表示关闭自动避险喵。
  const [hedgeThresholdText, setHedgeThresholdText] = useState('')
  // 自动避险退避秒数文本，阈值非零时必填喵。
  const [hedgeFreezeSecondsText, setHedgeFreezeSecondsText] = useState('')
  // 保存中状态阻止并发编辑和重复提交喵。
  const [isSaving, setIsSaving] = useState(false)

  // 当候选、模型版本或规则变化时，以最新响应重建规则草稿与自动避险配置喵。
  useEffect(() => {
    setDraftRules(rules.map(toFailureRuleDraft))
    setHedgeThresholdText(String(hedgeThreshold || 0))
    setHedgeFreezeSecondsText(String(hedgeFreezeSeconds || 0))
  }, [candidateID, model.version, rules, hedgeThreshold, hedgeFreezeSeconds])

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

  // parseHedgeConfig 解析自动避险两个输入并校验边界，非法时抛错阻止保存喵。
  const parseHedgeConfig = () => {
    const hedgeThreshold = Number(hedgeThresholdText.trim())
    // 喵~防御：阈值必须是零到一千之间的整数，空串按关闭处理喵。
    if (hedgeThresholdText.trim() === '' || hedgeThresholdText.trim() === '0') {
      return { hedgeThreshold: 0, hedgeFreezeSeconds: 0 }
    }
    if (!Number.isInteger(hedgeThreshold) || hedgeThreshold < 1 || hedgeThreshold > MAXIMUM_HEDGE_THRESHOLD) {
      throw new Error(t('Auto hedge threshold must be between 1 and 1000'))
    }
    const hedgeFreezeSeconds = Number(hedgeFreezeSecondsText.trim())
    // 喵~防御：阈值非零时退避秒数必填且必须为正数，否则冻结退避形同虚设喵。
    if (!Number.isInteger(hedgeFreezeSeconds) || hedgeFreezeSeconds < 1 || hedgeFreezeSeconds > MAXIMUM_HEDGE_FREEZE_SECONDS) {
      throw new Error(t('Auto hedge backoff seconds must be between 1 and 86400'))
    }
    return { hedgeThreshold, hedgeFreezeSeconds }
  }

  // saveRules 将整个有序规则集、自动避险配置与当前模型 version 一并原子替换喵。
  const saveRules = async () => {
    try {
      // 设置保存态，防止请求进行期间再次提交或重排喵。
      setIsSaving(true)
      // 在发请求前逐条校验，将草稿转换成 API 载荷喵。
      const validatedRules = draftRules.map((rule, index) => validateFailureRuleDraft(rule, index, t))
      // 解析自动避险配置，非法值在发请求前抛出喵。
      const hedgeConfig = parseHedgeConfig()
      // 通过候选范围接口提交模型版本、完整规则链与自动避险配置喵。
      const response = await replaceVirtualModelCandidateFailureRules(model.id, candidateID, {
        version: model.version,
        rules: validatedRules,
        hedge_threshold: hedgeConfig.hedgeThreshold,
        hedge_freeze_seconds: hedgeConfig.hedgeFreezeSeconds,
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
    <div className='space-y-4'>
      <p className='text-muted-foreground text-xs'>{t('Editing rules for candidate: {{candidate}}', { candidate: candidateLabel })}</p>

      {/* 候选级自动避险配置区：独立于失效规则，连续失败达阈值即冻结退避喵。 */}
      <div className='rounded-md border border-dashed p-3'>
        <h4 className='font-medium'>{t('Auto hedge')}</h4>
        <p className='text-muted-foreground text-sm'>{t('Independent of failure rules. Freezes this candidate after this many consecutive failures (4xx client errors are exempt).')}</p>
        <div className='mt-3 grid gap-3 sm:grid-cols-2'>
          <label className='grid gap-1 text-sm font-medium'>
            {t('Consecutive failure threshold')}
            <Input inputMode='numeric' value={hedgeThresholdText} disabled={isSaving} placeholder={t('0 = disabled, up to 1000')} onChange={(event) => setHedgeThresholdText(event.target.value)} />
          </label>
          <label className='grid gap-1 text-sm font-medium'>
            {t('Backoff seconds')}
            <Input inputMode='numeric' value={hedgeFreezeSecondsText} disabled={isSaving} placeholder={t('Required when threshold is set, up to 86400')} onChange={(event) => setHedgeFreezeSecondsText(event.target.value)} />
          </label>
        </div>
      </div>

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

      {draftRules.map((rule, index) => (
        <FailureRuleEditorRow
          key={rule.id ?? `new-rule-${index}`}
          index={index}
          total={draftRules.length}
          rule={rule}
          isSaving={isSaving}
          onChange={(patch) => updateRule(index, patch)}
          onMove={(direction) => moveRule(index, direction)}
          onRemove={() => removeRule(index)}
        />
      ))}

      {draftRules.length === 0 && <p className='text-muted-foreground text-sm'>{t('No failure rules configured. This candidate falls back to the global fallback rules.')}</p>}
      <div className='flex justify-end'>
        <Button type='button' onClick={() => void saveRules()} disabled={isSaving}>
          {isSaving ? t('Saving') : t('Save failure rules')}
        </Button>
      </div>
    </div>
  )
}
