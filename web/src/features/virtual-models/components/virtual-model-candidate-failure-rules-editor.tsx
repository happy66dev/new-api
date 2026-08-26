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

// VirtualModelCandidateFailureRulesEditor 管理单个候选按从上到下首条命中的失败规则喵。
// 候选配置了自己的规则后，运行时优先使用这组规则；未命中或未配置时回退模型级全局兜底规则喵。
export function VirtualModelCandidateFailureRulesEditor({
  model,
  candidateID,
  candidateLabel,
  rules,
  onSaved,
}: {
  model: VirtualModel
  candidateID: number
  candidateLabel: string
  rules: VirtualModelFailureRule[]
  onSaved: () => void
}) {
  // 读取双语翻译函数，保持控制台文案与现有页面一致喵。
  const { t } = useTranslation()
  // 保存规则草稿顺序，数组下标就是规则首条命中优先级喵。
  const [draftRules, setDraftRules] = useState<FailureRuleDraft[]>([])
  // 保存中状态阻止并发编辑和重复提交喵。
  const [isSaving, setIsSaving] = useState(false)

  // 当候选、模型版本或规则变化时，以最新响应重建规则草稿喵。
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
    <div className='space-y-4'>
      <p className='text-muted-foreground text-xs'>{t('Editing rules for candidate: {{candidate}}', { candidate: candidateLabel })}</p>

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
