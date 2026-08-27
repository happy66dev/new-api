/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as
 published by the Free Software Foundation, either version 3 of the
 License, or (at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  formatLatency,
  getSuccessRateDotClass,
  getSuccessRateTextClass,
} from '@/features/performance-metrics/lib/format'
import { AvailabilityBars } from '@/features/status-check/availability-bars'
import {
  bucketData,
  EntityPerformanceDrawer,
  LineChart,
  TokenUsageChart,
} from '@/features/status-check/entity-performance-drawer'
import { formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'

import {
  getVirtualModelCandidateStatus,
  type VirtualModelStatus,
} from '../api'

// VirtualModelOverviewStatusProps 是 Overview 状态卡片的输入，附带跳转与刷新回调喵。
type VirtualModelOverviewStatusProps = {
  // modelID 是虚拟模型编号，用于候选抽屉按需拉取候选状态喵。
  modelID: number
  // status 是整体状态响应，可能为空或仍在加载喵。
  status?: VirtualModelStatus | null
  loading?: boolean
  error?: boolean
  // onRefresh 重新拉取整体状态喵。
  onRefresh: () => void
}

// VirtualModelOverviewStatus 在 Overview 选项卡基本信息下方展示整体运行状态喵。
// 包含可用性/延迟/TTFT/缓存命中率指标、24h 柱状图、逐小时图表与候选节点摘要喵。
export function VirtualModelOverviewStatus(
  props: VirtualModelOverviewStatusProps
) {
  const { t } = useTranslation()
  // 有真实调用样本时才按可用性着色，否则展示占位符喵。
  const hasData = Boolean(props.status && props.status.request_count > 0)
  const availability = hasData
    ? (props.status?.availability ?? Number.NaN)
    : Number.NaN
  // openCandidate 记录被点击的候选编号与标签，非空时打开该候选的性能抽屉喵。
  const [openCandidate, setOpenCandidate] = useState<{
    id: number
    label: string
  } | null>(null)
  const candidateQuery = useQuery({
    queryKey: ['virtual-model-candidate-status', props.modelID, openCandidate?.id],
    // 喵~防御：仅在候选被选中且模型编号有效时才拉取候选状态喵。
    queryFn: () => getVirtualModelCandidateStatus(props.modelID, openCandidate!.id),
    enabled: Boolean(props.modelID && openCandidate),
    staleTime: 30 * 1000,
    retry: false,
  })
  const series = props.status?.series ?? []

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
          {/* 六项核心指标：可用性 / 平均延迟 / 平均 TTFT / 缓存命中率 / 请求数 / 总 token 喵。 */}
          <div className='grid grid-cols-3 gap-1 rounded-md border p-2 text-center sm:grid-cols-6'>
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
                {t('Average TTFT')}
              </div>
              <div className='font-mono text-xs tabular-nums'>
                {formatLatency(props.status?.avg_ttft_ms ?? 0)}
              </div>
            </div>
            <div>
              <div className='text-muted-foreground text-[10px]'>
                {t('Cache hit rate')}
              </div>
              <div className='font-mono text-xs tabular-nums'>
                {props.status?.cache_hit_rate != null
                  ? `${props.status.cache_hit_rate.toFixed(2)}%`
                  : '-'}
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
            <div>
              <div className='text-muted-foreground text-[10px]'>
                {t('Total Tokens')}
              </div>
              <div className='font-mono text-xs tabular-nums'>
                {props.status?.total_tokens?.toLocaleString() ?? 0}
              </div>
            </div>
          </div>

          {/* 最近 24 个采样点的可用性柱状图喵。 */}
          <AvailabilityBars rates={props.status?.availability_24h ?? []} />

          {/* 逐小时图表：TTFT / 缓存命中率 / token 消耗 / 请求量喵。 */}
          <div className='grid grid-cols-1 gap-3 lg:grid-cols-2'>
            <div className='min-w-0'>
              <div className='text-muted-foreground mb-1 text-xs'>
                {t('Average TTFT')}
              </div>
              <LineChart
                values={bucketData(series, (bucket) => bucket.avg_ttft_ms)}
                color='#60a5fa'
                formatValue={(value) => `${Math.round(value)} ms`}
                labelKey={t('Average TTFT')}
                emptyText={t('No history data available')}
              />
            </div>
            <div className='min-w-0'>
              <div className='text-muted-foreground mb-1 text-xs'>
                {t('Cache hit rate')}
              </div>
              <LineChart
                values={bucketData(series, (bucket) => bucket.cache_hit_rate)}
                color='#22d3ee'
                formatValue={(value) => `${value.toFixed(2)}%`}
                labelKey={t('Cache hit rate')}
                emptyText={t('No history data available')}
              />
            </div>
            <div className='min-w-0'>
              <div className='text-muted-foreground mb-1 text-xs'>
                {t('Token usage')}
              </div>
              <TokenUsageChart series={series} />
            </div>
            <div className='min-w-0'>
              <div className='text-muted-foreground mb-1 text-xs'>
                {t('Request volume')}
              </div>
              <LineChart
                values={bucketData(series, (bucket) => bucket.request_count)}
                color='#f59e0b'
                formatValue={(value) => Math.round(value).toLocaleString()}
                labelKey={t('Request Count')}
                emptyText={t('No history data available')}
              />
            </div>
          </div>

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

          {/* 候选节点摘要：点击行打开该候选的性能抽屉喵。 */}
          {(props.status?.candidates?.length ?? 0) > 0 && (
            <div className='space-y-1'>
              <div className='text-muted-foreground text-xs'>
                {t('Candidates')}
              </div>
              {props.status?.candidates.map((candidate) => (
                <button
                  type='button'
                  key={candidate.candidate_id}
                  onClick={() =>
                    setOpenCandidate({
                      id: candidate.candidate_id,
                      label: candidate.label,
                    })
                  }
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

      {/* 候选性能抽屉：按需拉取选中候选的富系列数据喵。 */}
      <EntityPerformanceDrawer
        open={Boolean(openCandidate)}
        onOpenChange={(open) => {
          // 关闭抽屉时清除选中候选，避免残留过期状态喵。
          if (!open) setOpenCandidate(null)
        }}
        title={openCandidate?.label ?? ''}
        description={t('Candidate performance over the last 24 hours')}
        data={candidateQuery.data?.data ?? null}
        loading={candidateQuery.isLoading}
        error={Boolean(candidateQuery.isError)}
      />
    </div>
  )
}
