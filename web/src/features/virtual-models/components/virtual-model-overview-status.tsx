/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as
 published by the Free Software Foundation, either version 3 of the
 License, or (at your option) any later version.
*/
import { RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  formatLatency,
  getSuccessRateDotClass,
  getSuccessRateTextClass,
} from '@/features/performance-metrics/lib/format'
import { AvailabilityBars } from '@/features/status-check/availability-bars'
import { formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'

import type { VirtualModelStatus } from '../api'

// VirtualModelOverviewStatusProps 是 Overview 状态卡片的输入，附带跳转与刷新回调喵。
type VirtualModelOverviewStatusProps = {
  // status 是整体状态响应，可能为空或仍在加载喵。
  status?: VirtualModelStatus | null
  loading?: boolean
  error?: boolean
  // onNavigateToCandidates 点击候选摘要行时切换到候选链选项卡喵。
  onNavigateToCandidates: () => void
  // onRefresh 重新拉取整体状态喵。
  onRefresh: () => void
}

// VirtualModelOverviewStatus 在 Overview 选项卡基本信息下方展示整体运行状态喵。
// 包含可用性/延迟/请求数指标、24h 柱状图、最近一次调用与候选节点摘要喵。
export function VirtualModelOverviewStatus(
  props: VirtualModelOverviewStatusProps
) {
  const { t } = useTranslation()
  // 有真实调用样本时才按可用性着色，否则展示占位符喵。
  const hasData = Boolean(props.status && props.status.request_count > 0)
  const availability = hasData
    ? (props.status?.availability ?? Number.NaN)
    : Number.NaN

  return (
    <div className='space-y-3 rounded-md border p-4 text-sm'>
      {/* 卡片标题行：状态标题 + 刷新按钮喵。 */}
      <div className='flex items-center justify-between gap-3'>
        <h3 className='font-medium'>{t('Runtime Status')}</h3>
        <Button
          size='sm'
          variant='outline'
          onClick={props.onRefresh}
          aria-label={t('Refresh')}
        >
          <RefreshCw className='size-3.5' />
          {t('Refresh')}
        </Button>
      </div>

      {props.loading && <p className='text-muted-foreground'>{t('Loading')}</p>}
      {props.error && (
        <p className='text-destructive'>
          {t('Unable to load virtual model status')}
        </p>
      )}
      {!props.loading && !props.error && !hasData && (
        <p className='text-muted-foreground'>{t('No status data yet')}</p>
      )}

      {hasData && (
        <>
          {/* 三项核心指标：可用性 / 平均延迟 / 请求数喵。 */}
          <div className='grid grid-cols-3 gap-1 rounded-md border p-2 text-center'>
            <div>
              <div className='text-muted-foreground text-[10px]'>
                {t('Availability')}
              </div>
              <div
                className={cn(
                  'font-mono text-xs tabular-nums',
                  getSuccessRateTextClass(availability)
                )}
              >
                {availability.toFixed(2)}%
              </div>
            </div>
            <div>
              <div className='text-muted-foreground text-[10px]'>
                {t('Average latency')}
              </div>
              <div className='font-mono text-xs tabular-nums'>
                {formatLatency(props.status?.avg_latency_ms ?? 0)}
              </div>
            </div>
            <div>
              <div className='text-muted-foreground text-[10px]'>
                {t('Request Count')}
              </div>
              <div className='font-mono text-xs tabular-nums'>
                {props.status?.request_count ?? 0}
              </div>
            </div>
          </div>

          {/* 最近 24 个采样点的可用性柱状图喵。 */}
          <AvailabilityBars rates={props.status?.availability_24h ?? []} />

          {/* 最近一次调用：成功/失败状态点 + 时间 + 错误明细喵。 */}
          {(props.status?.last_at ?? 0) > 0 && (
            <div className='flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs'>
              <span
                className={cn(
                  'inline-block size-2 rounded-full',
                  props.status?.last_success ? 'bg-emerald-500' : 'bg-red-500'
                )}
                aria-hidden='true'
              />
              <span className='text-muted-foreground'>
                {t('Last call')}:{' '}
                {formatTimestampToDate(props.status?.last_at ?? 0)}
              </span>
              {props.status?.last_error ? (
                <span
                  className='text-muted-foreground truncate'
                  title={props.status.last_error}
                >
                  {props.status.last_error}
                </span>
              ) : null}
            </div>
          )}

          {/* 候选节点摘要：点击行切换到候选链选项卡喵。 */}
          {(props.status?.candidates?.length ?? 0) > 0 && (
            <div className='space-y-1'>
              <div className='text-muted-foreground text-xs'>
                {t('Candidates')}
              </div>
              {props.status?.candidates.map((candidate) => (
                <button
                  type='button'
                  key={candidate.candidate_id}
                  onClick={props.onNavigateToCandidates}
                  className='hover:bg-muted/50 flex w-full items-center justify-between gap-2 rounded-md border px-2 py-1.5 text-left'
                >
                  <span className='flex min-w-0 items-center gap-2'>
                    <span
                      className={cn(
                        'inline-block size-2 shrink-0 rounded-full',
                        candidate.request_count > 0
                          ? getSuccessRateDotClass(candidate.availability)
                          : 'bg-muted-foreground/40'
                      )}
                      aria-hidden='true'
                    />
                    <span className='truncate text-xs'>{candidate.label}</span>
                  </span>
                  <span className='text-muted-foreground shrink-0 text-xs'>
                    {candidate.request_count > 0
                      ? `${candidate.availability.toFixed(2)}% · ${candidate.request_count}`
                      : t('No data')}
                  </span>
                </button>
              ))}
            </div>
          )}
        </>
      )}
    </div>
  )
}
