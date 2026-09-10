/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'

import type { VirtualModelShareSkippedCandidate } from '../api'
import { describeShareSkipReason, describeShareWarning } from '../lib/share-code'

// VirtualModelShareSkippedList 展示导入预检/导入结果里被跳过的候选与提示喵。
// 候选为什么被跳过必须逐条说明，否则用户只会看到"少了几条"而不知道原因喵。
export function VirtualModelShareSkippedList({
  skippedCandidates,
  warnings,
}: {
  skippedCandidates: VirtualModelShareSkippedCandidate[]
  warnings: string[]
}) {
  const { t } = useTranslation()
  // 喵~防御：没有跳过项也没有提示时不渲染空壳区块，避免界面出现无意义留白喵。
  if (skippedCandidates.length === 0 && warnings.length === 0) {
    return null
  }
  return (
    <div className='space-y-2'>
      {skippedCandidates.length > 0 && (
        <div className='space-y-1'>
          <p className='text-sm font-medium'>{t('Skipped candidates')}</p>
          <ul className='space-y-1'>
            {skippedCandidates.map((skippedCandidate) => (
              <li
                className='border-border/60 bg-muted/25 rounded-md border p-2 text-xs'
                key={`${skippedCandidate.order}-${skippedCandidate.reason}`}
              >
                <div className='flex flex-wrap items-center gap-2'>
                  <Badge variant='outline'>
                    {t('Candidate {{index}}', { index: skippedCandidate.order })}
                  </Badge>
                  {/* 分组与模型分开展示，让用户一眼看出是哪条路由目标不可用喵。 */}
                  {skippedCandidate.group && (
                    <span className='font-mono'>{t('Group')}: {skippedCandidate.group}</span>
                  )}
                  {skippedCandidate.model && (
                    <span className='font-mono'>{t('Model')}: {skippedCandidate.model}</span>
                  )}
                </div>
                <p className='text-muted-foreground mt-1'>
                  {describeShareSkipReason(skippedCandidate.reason, skippedCandidate.message, t)}
                </p>
              </li>
            ))}
          </ul>
        </div>
      )}
      {warnings.length > 0 && (
        <ul className='space-y-1'>
          {warnings.map((warning) => (
            <li className='text-destructive text-xs' key={warning}>
              {describeShareWarning(warning, t)}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
