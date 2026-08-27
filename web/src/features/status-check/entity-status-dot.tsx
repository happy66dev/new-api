/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Popover,
  PopoverContent,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from '@/components/ui/popover'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import {
  formatLatency,
  getSuccessRateDotClass,
  getSuccessRateTextClass,
} from '@/features/performance-metrics/lib/format'
import { formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'

import { AvailabilityBars } from './availability-bars'

// EntityStatusSummary 是状态检测聚合摘要，兼容上游/虚拟模型两类后端载荷的公共字段喵。
export type EntityStatusSummary = {
  // 可用性是 0-100 的百分比喵。
  availability: number
  avg_latency_ms: number
  request_count: number
  // availability_24h 是最近 24 个采样点的可用性序列，缺省时无详情弹层喵。
  availability_24h?: number[]
  last_at?: number
  last_success?: boolean
  last_latency_ms?: number
  last_error?: string
}

type EntityStatusDotProps = {
  // summary 是当前实体主维度（自用/整体）的聚合摘要喵。
  summary?: EntityStatusSummary | null
  // shared 是共享调用维度的聚合摘要，存在时弹层内提供自用/共享切换喵。
  shared?: EntityStatusSummary | null
  // loading 表示状态仍在请求中，展示脉冲灰点喵。
  loading?: boolean
  // error 表示状态请求失败，展示不可用灰点喵。
  error?: boolean
  // label 是弹层标题展示的实体名称，缺省时显示通用文案喵。
  label?: string
  // showAvailabilityText 控制是否内联展示可用性百分比，缺省为 true喵。
  showAvailabilityText?: boolean
  // onOpenPerformance 存在时点击圆点调用该回调（打开性能抽屉），替代内联详情弹层喵。
  onOpenPerformance?: () => void
}

// EntityStatusDot 展示实体的健康状态圆点与可选详情弹层喵。
// 圆点颜色由可用性分级决定；悬停显示摘要，有 24h 序列时点击展开详情喵。
export function EntityStatusDot(props: EntityStatusDotProps) {
  const { t } = useTranslation()
  // 弹层当前展示的维度：self 为自用/整体，shared 为共享维度喵。
  const [dimension, setDimension] = useState<'self' | 'shared'>('self')
  const showAvailabilityText = props.showAvailabilityText ?? true
  // 共享维度没有数据时回退到自用维度，避免弹层内空白喵。
  const activeSummary =
    dimension === 'shared' && props.shared ? props.shared : props.summary
  const hasData = Boolean(activeSummary && activeSummary.request_count > 0)
  const availability = hasData
    ? (activeSummary?.availability ?? Number.NaN)
    : Number.NaN
  // 只有携带 24h 序列时才启用可点击详情弹层喵。
  const canOpenDetails =
    hasData && (activeSummary?.availability_24h?.length ?? 0) > 0

  // 圆点颜色：请求中为脉冲灰，无数据为灰，有数据按可用性分级喵。
  let dotColorClass = 'bg-muted-foreground/40'
  if (props.loading) {
    dotColorClass = 'animate-pulse bg-muted-foreground/40'
  } else if (hasData) {
    dotColorClass = getSuccessRateDotClass(availability)
  }
  const dotNode = (
    <span
      className={cn(
        'inline-block size-2.5 shrink-0 rounded-full',
        dotColorClass
      )}
      aria-hidden='true'
      data-testid='entity-status-dot'
    />
  )

  // 可用性文本：无数据时展示占位符，有数据时按分级着色喵。
  let availabilityNode: ReactNode = null
  if (showAvailabilityText) {
    if (props.loading || props.error) {
      availabilityNode = (
        <span className='text-muted-foreground/50 text-xs tabular-nums'>—</span>
      )
    } else if (hasData) {
      availabilityNode = (
        <span
          className={cn(
            'font-mono text-xs tabular-nums',
            getSuccessRateTextClass(availability)
          )}
        >
          {availability.toFixed(2)}%
        </span>
      )
    } else {
      availabilityNode = (
        <span className='text-muted-foreground/50 text-xs tabular-nums'>—</span>
      )
    }
  }

  // 悬停摘要文案：请求中/失败/无数据/正常四种状态喵。
  let summaryText: string
  if (props.loading) {
    summaryText = t('Loading')
  } else if (props.error) {
    summaryText = t('Unable to load status')
  } else if (!hasData) {
    summaryText = t('No status data yet')
  } else {
    summaryText = `${t('Availability')}: ${availability.toFixed(2)}% · ${t('Average latency')}: ${formatLatency(
      activeSummary?.avg_latency_ms ?? 0
    )} · ${t('Request Count')}: ${activeSummary?.request_count ?? 0}`
  }

  // 圆点与可用性文本组合行，作为悬停与点击的触发器喵。
  const dotRow = (
    <span className='inline-flex items-center gap-1.5'>
      {dotNode}
      {availabilityNode}
    </span>
  )

  // 配置了性能抽屉回调（虚拟候选/上游模型）：点击圆点打开抽屉，保留悬停提示喵。
  if (props.onOpenPerformance) {
    return (
      <TooltipProvider delay={300}>
        <Tooltip>
          <TooltipTrigger
            render={
              <button
                type='button'
                className='inline-flex cursor-pointer items-center gap-1.5'
                onClick={props.onOpenPerformance}
                aria-label={
                  props.label
                    ? `${props.label} ${t('Performance')}`
                    : t('Performance')
                }
              >
                {dotRow}
              </button>
            }
          />
          <TooltipContent>{summaryText}</TooltipContent>
        </Tooltip>
      </TooltipProvider>
    )
  }

  // 无 24h 序列（如共享视角）：仅保留悬停提示，不可点击喵。
  if (!canOpenDetails) {
    return (
      <TooltipProvider delay={300}>
        <Tooltip>
          <TooltipTrigger render={dotRow} />
          <TooltipContent>{summaryText}</TooltipContent>
        </Tooltip>
      </TooltipProvider>
    )
  }

  // 有 24h 序列：悬停看摘要，点击展开可用性柱状图与最近一次调用喵。
  return (
    <Popover>
      <PopoverTrigger
        render={
          <span
            className='inline-flex cursor-pointer items-center gap-1.5'
            tabIndex={0}
          >
            <TooltipProvider delay={300}>
              <Tooltip>
                <TooltipTrigger render={dotRow} />
                <TooltipContent>{summaryText}</TooltipContent>
              </Tooltip>
            </TooltipProvider>
          </span>
        }
        aria-label={t('Status')}
      />
      <PopoverContent className='w-80' align='start'>
        <PopoverHeader>
          <PopoverTitle className='flex flex-wrap items-center justify-between gap-2'>
            {props.label ?? t('Status')}
            {/* 属主行有共享维度时提供自用/共享切换喵。 */}
            {props.shared && (
              <ToggleGroup
                value={[dimension]}
                onValueChange={(values) => {
                  const next = values[0]
                  if (next === 'self' || next === 'shared') {
                    setDimension(next)
                  }
                }}
                variant='outline'
                size='sm'
                spacing={1}
                aria-label={t('Status dimension')}
              >
                <ToggleGroupItem value='self'>{t('Self')}</ToggleGroupItem>
                <ToggleGroupItem value='shared'>{t('Shared')}</ToggleGroupItem>
              </ToggleGroup>
            )}
          </PopoverTitle>
        </PopoverHeader>
        {/* 复用可用性柱状图展示最近 24 个采样点喵。 */}
        <AvailabilityBars rates={activeSummary?.availability_24h ?? []} />
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
              {formatLatency(activeSummary?.avg_latency_ms ?? 0)}
            </div>
          </div>
          <div>
            <div className='text-muted-foreground text-[10px]'>
              {t('Request Count')}
            </div>
            <div className='font-mono text-xs tabular-nums'>
              {activeSummary?.request_count ?? 0}
            </div>
          </div>
        </div>
        {/* 最近一次调用：成功/失败状态点 + 相对时间 + 错误明细喵。 */}
        {(activeSummary?.last_at ?? 0) > 0 && (
          <div className='flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs'>
            <span
              className={cn(
                'inline-block size-2 rounded-full',
                activeSummary?.last_success ? 'bg-emerald-500' : 'bg-red-500'
              )}
              aria-hidden='true'
            />
            <span className='text-muted-foreground'>
              {t('Last call')}: {formatTimestampToDate(activeSummary?.last_at ?? 0)}
            </span>
            {activeSummary?.last_error ? (
              <span
                className='text-muted-foreground truncate'
                title={activeSummary.last_error}
              >
                {activeSummary.last_error}
              </span>
            ) : null}
          </div>
        )}
      </PopoverContent>
    </Popover>
  )
}
