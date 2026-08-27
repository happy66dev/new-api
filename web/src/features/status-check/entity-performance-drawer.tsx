/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { VChart } from '@visactor/react-vchart'
import dayjs from 'dayjs'
import { Gauge, HeartPulse, Timer, TrendingUp } from 'lucide-react'
import { Suspense, useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import {
  sideDrawerContentClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
  SideDrawerSection,
  SideDrawerSectionHeader,
} from '@/components/drawer-layout'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Skeleton } from '@/components/ui/skeleton'
import {
  formatLatency,
  formatThroughput,
  getSuccessRateTextClass,
} from '@/features/performance-metrics/lib/format'
import { useChartTheme } from '@/lib/use-chart-theme'
import { cn } from '@/lib/utils'
import { VCHART_OPTION } from '@/lib/vchart'

// EntityProbeBucket 是实体被动统计的单个小时桶明细，与后端 series 字段对应喵。
export type EntityProbeBucket = {
  ts: number
  request_count: number
  success_rate: number
  avg_latency_ms: number
  avg_ttft_ms: number
  cache_hit_rate: number
  input_tokens: number
  output_tokens: number
  cached_tokens: number
}

// EntityPerformanceData 是性能抽屉需要的聚合与逐桶系列数据喵。
export type EntityPerformanceData = {
  availability: number
  avg_latency_ms: number
  avg_ttft_ms: number
  cache_hit_rate: number
  request_count: number
  total_tokens: number
  series: EntityProbeBucket[]
}

type EntityPerformanceDrawerProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  // title 是抽屉标题，例如候选名或上游模型名喵。
  title: string
  // description 是标题下方的说明文案喵。
  description: string
  // data 是聚合与系列数据；为空或仍在加载时展示占位喵。
  data: EntityPerformanceData | null
  loading?: boolean
  error?: boolean
}

// getChartThemeTokens 提取当前主题的文字与网格颜色，供各图表统一使用喵。
function getChartThemeTokens(resolvedTheme: string) {
  return {
    textColor:
      resolvedTheme === 'dark'
        ? 'rgba(255, 255, 255, 0.68)'
        : 'rgba(15, 23, 42, 0.58)',
    gridColor:
      resolvedTheme === 'dark'
        ? 'rgba(255, 255, 255, 0.12)'
        : 'rgba(15, 23, 42, 0.12)',
  }
}

// bucketData 把逐桶系列转换为折线图通用数据，时间戳格式化为 HH:mm 喵。
export function bucketData(
  series: EntityProbeBucket[],
  pick: (bucket: EntityProbeBucket) => number
): Array<{ time: string; value: number }> {
  return series
    .map((bucket) => ({
      time: dayjs.unix(bucket.ts).format('HH:mm'),
      value: pick(bucket),
    }))
    .filter((point) => point.value > 0)
}

// toThroughputSeries 从输出 token 与平均延迟估算每桶吞吐（token/秒）喵。
function toThroughputSeries(
  series: EntityProbeBucket[]
): Array<{ time: string; value: number }> {
  return series
    .map((bucket) => {
      // 喵~防御：平均延迟为零时跳过该桶，避免除零喵。
      if (bucket.avg_latency_ms <= 0 || bucket.output_tokens <= 0) return null
      return {
        time: dayjs.unix(bucket.ts).format('HH:mm'),
        value: bucket.output_tokens / (bucket.avg_latency_ms / 1000),
      }
    })
    .filter((point): point is { time: string; value: number } => point !== null)
}

// StatCard 是抽屉顶部的单值指标卡喵。
function StatCard(props: {
  icon: React.ComponentType<{ className?: string }>
  label: string
  value: string
  valueClassName?: string
}) {
  const Icon = props.icon
  return (
    <div className='bg-background flex flex-col gap-0.5 rounded-md border p-2 text-center'>
      <span className='text-muted-foreground inline-flex items-center justify-center gap-1 text-[10px]'>
        <Icon className='size-3' />
        {props.label}
      </span>
      <span
        className={cn(
          'font-mono text-xs font-semibold tabular-nums',
          props.valueClassName
        )}
      >
        {props.value}
      </span>
    </div>
  )
}

// lineChartSpec 构造单序列折线图配置，主题文字与网格颜色直接注入轴喵。
function lineChartSpec(
  values: Array<{ time: string; value: number }>,
  lineColor: string,
  formatValue: (value: number) => string,
  labelKey: string,
  textColor: string,
  gridColor: string
) {
  return {
    type: 'line' as const,
    data: [{ id: 'line', values }],
    xField: 'time',
    yField: 'value',
    smooth: true,
    line: { style: { stroke: lineColor, lineWidth: 2.5 } },
    point: {
      visible: true,
      style: { size: 5, fill: lineColor, lineWidth: 1.5 },
    },
    legends: { visible: false },
    tooltip: {
      mark: {
        title: { value: (datum: { time: string }) => datum.time },
        content: [
          {
            key: labelKey,
            value: (datum: { value: number }) => formatValue(datum.value),
          },
        ],
      },
    },
    axes: [
      {
        orient: 'bottom',
        label: { style: { fill: textColor, fontSize: 10 }, autoLimit: true },
        tick: { visible: false },
      },
      {
        orient: 'left',
        min: 0,
        label: {
          formatMethod: (value: number | string) => formatValue(Number(value)),
          style: { fill: textColor, fontSize: 10 },
        },
        grid: { visible: true, style: { lineDash: [3, 3], stroke: gridColor } },
      },
    ],
  }
}

// LineChart 是抽屉内的通用折线图容器，Overview 与抽屉共用喵。
export function LineChart(props: {
  values: Array<{ time: string; value: number }>
  color: string
  formatValue: (value: number) => string
  labelKey: string
  emptyText: string
}) {
  const { resolvedTheme, themeReady } = useChartTheme()
  const { textColor, gridColor } = getChartThemeTokens(resolvedTheme)
  const spec = useMemo(() => {
    if (props.values.length === 0) return null
    return lineChartSpec(
      props.values,
      props.color,
      props.formatValue,
      props.labelKey,
      textColor,
      gridColor
    )
  }, [gridColor, props.color, props.formatValue, props.labelKey, props.values, textColor])

  if (props.values.length === 0) {
    return (
      <div className='text-muted-foreground flex h-40 items-center justify-center rounded-lg border text-xs'>
        {props.emptyText}
      </div>
    )
  }

  return (
    <div className='h-44 rounded-lg border p-2'>
      {themeReady && spec && (
        <VChart
          key={`entity-perf-${props.color}-${resolvedTheme}`}
          spec={{
            ...spec,
            theme: resolvedTheme === 'dark' ? 'dark' : 'light',
            background: 'transparent',
          }}
          option={VCHART_OPTION}
        />
      )}
    </div>
  )
}

// TokenUsageChart 是输入/输出/缓存命中的堆叠柱状图，Overview 与抽屉共用喵。
export function TokenUsageChart(props: { series: EntityProbeBucket[] }) {
  const { t } = useTranslation()
  const { resolvedTheme, themeReady } = useChartTheme()
  const { textColor, gridColor } = getChartThemeTokens(resolvedTheme)
  // 把每桶 token 分类拍平成 seriesField 行，供 VChart 按 type 堆叠喵。
  const data = useMemo(
    () =>
      props.series.flatMap((bucket) => {
        const time = dayjs.unix(bucket.ts).format('HH:mm')
        return [
          { time, type: t('Cached'), value: bucket.cached_tokens },
          { time, type: t('Input'), value: bucket.input_tokens },
          { time, type: t('Output'), value: bucket.output_tokens },
        ]
      }),
    [props.series, t]
  )
  const spec = useMemo(() => {
    if (data.length === 0) return null
    return {
      type: 'bar' as const,
      data: [{ id: 'tokens', values: data }],
      xField: 'time',
      yField: 'value',
      seriesField: 'type',
      stack: true,
      bar: { style: { cornerRadius: 2 } },
      color: ['#22d3ee', '#60a5fa', '#a78bfa'],
      legends: { visible: true, position: 'top', orient: 'top' },
      tooltip: {
        mark: {
          title: { value: (datum: { time: string }) => datum.time },
        },
      },
      axes: [
        {
          orient: 'bottom',
          label: { style: { fill: textColor, fontSize: 10 } },
          tick: { visible: false },
        },
        {
          orient: 'left',
          label: { style: { fill: textColor, fontSize: 10 } },
          grid: { visible: true, style: { lineDash: [3, 3], stroke: gridColor } },
        },
      ],
    }
  }, [data, gridColor, textColor])

  if (data.length === 0) {
    return (
      <div className='text-muted-foreground flex h-44 items-center justify-center rounded-lg border text-xs'>
        {t('No token usage data available')}
      </div>
    )
  }

  return (
    <div className='h-44 rounded-lg border p-2'>
      {themeReady && spec && (
        <VChart
          key={`entity-token-${resolvedTheme}`}
          spec={{
            ...spec,
            theme: resolvedTheme === 'dark' ? 'dark' : 'light',
            background: 'transparent',
          }}
          option={VCHART_OPTION}
        />
      )}
    </div>
  )
}

// EntityPerformanceDrawer 展示实体的聚合性能与逐小时图表喵。
// 供虚拟模型候选、虚拟模型整体与上游模型共用，避免各页重复实现图表喵。
export function EntityPerformanceDrawer(props: EntityPerformanceDrawerProps) {
  const { t } = useTranslation()
  const data = props.data
  const series = data?.series ?? []
  const hasData = Boolean(data && data.request_count > 0)

  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent
        side='right'
        className={sideDrawerContentClassName('sm:max-w-2xl')}
      >
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{props.title}</SheetTitle>
          <SheetDescription>{props.description}</SheetDescription>
        </SheetHeader>
        <div className={sideDrawerFormClassName()}>
          {props.loading && (
            <p className='text-muted-foreground text-sm'>{t('Loading')}</p>
          )}
          {props.error && (
            <p className='text-destructive text-sm'>
              {t('Unable to load status')}
            </p>
          )}
          {!props.loading && !props.error && !hasData && (
            <p className='text-muted-foreground text-sm'>
              {t('No status data yet')}
            </p>
          )}
          {hasData && data && (
            <>
              {/* 顶部指标卡：可用性 / 平均延迟 / 平均 TTFT / 缓存命中率 / 请求数 / 总 token 喵。 */}
              <div className='grid grid-cols-3 gap-2'>
                <StatCard
                  icon={HeartPulse}
                  label={t('Availability')}
                  value={`${data.availability.toFixed(2)}%`}
                  valueClassName={getSuccessRateTextClass(data.availability)}
                />
                <StatCard
                  icon={Timer}
                  label={t('Average latency')}
                  value={formatLatency(data.avg_latency_ms)}
                />
                <StatCard
                  icon={Timer}
                  label={t('Average TTFT')}
                  value={formatLatency(data.avg_ttft_ms)}
                />
                <StatCard
                  icon={Gauge}
                  label={t('Cache hit rate')}
                  value={`${data.cache_hit_rate.toFixed(2)}%`}
                />
                <StatCard
                  icon={TrendingUp}
                  label={t('Request Count')}
                  value={data.request_count.toLocaleString()}
                />
                <StatCard
                  icon={TrendingUp}
                  label={t('Total Tokens')}
                  value={data.total_tokens.toLocaleString()}
                />
              </div>

              {/* 首字延迟与可用性趋势喵。 */}
              <SideDrawerSection>
                <SideDrawerSectionHeader
                  title={t('Average TTFT')}
                  description={t('Time to first token over the last 24 hours')}
                  icon={<Timer aria-hidden='true' />}
                  iconTone='chart-1'
                />
                <Suspense fallback={<Skeleton className='h-44 w-full rounded-lg' />}>
                  <LineChart
                    values={bucketData(series, (bucket) => bucket.avg_ttft_ms)}
                    color='#60a5fa'
                    formatValue={(value) => `${Math.round(value)} ms`}
                    labelKey={t('Average TTFT')}
                    emptyText={t('No history data available')}
                  />
                </Suspense>
              </SideDrawerSection>

              <SideDrawerSection>
                <SideDrawerSectionHeader
                  title={t('Availability')}
                  description={t('Request success rate over the last 24 hours')}
                  icon={<HeartPulse aria-hidden='true' />}
                  iconTone='chart-2'
                />
                <Suspense fallback={<Skeleton className='h-44 w-full rounded-lg' />}>
                  <LineChart
                    values={bucketData(series, (bucket) => bucket.success_rate)}
                    color='#10b981'
                    formatValue={(value) => `${value.toFixed(2)}%`}
                    labelKey={t('Availability')}
                    emptyText={t('No history data available')}
                  />
                </Suspense>
              </SideDrawerSection>

              <SideDrawerSection>
                <SideDrawerSectionHeader
                  title={t('Cache hit rate')}
                  description={t('Cached tokens over input tokens per hour')}
                  icon={<Gauge aria-hidden='true' />}
                  iconTone='chart-3'
                />
                <Suspense fallback={<Skeleton className='h-44 w-full rounded-lg' />}>
                  <LineChart
                    values={bucketData(series, (bucket) => bucket.cache_hit_rate)}
                    color='#22d3ee'
                    formatValue={(value) => `${value.toFixed(2)}%`}
                    labelKey={t('Cache hit rate')}
                    emptyText={t('No history data available')}
                  />
                </Suspense>
              </SideDrawerSection>

              <SideDrawerSection>
                <SideDrawerSectionHeader
                  title={t('Token usage')}
                  description={t('Input, cached, and output tokens per hour')}
                  icon={<TrendingUp aria-hidden='true' />}
                  iconTone='chart-4'
                />
                <TokenUsageChart series={series} />
              </SideDrawerSection>

              <SideDrawerSection>
                <SideDrawerSectionHeader
                  title={t('Request volume')}
                  description={t('Requests per hour')}
                  icon={<TrendingUp aria-hidden='true' />}
                  iconTone='chart-1'
                />
                <Suspense fallback={<Skeleton className='h-44 w-full rounded-lg' />}>
                  <LineChart
                    values={bucketData(series, (bucket) => bucket.request_count)}
                    color='#f59e0b'
                    formatValue={(value) => Math.round(value).toLocaleString()}
                    labelKey={t('Request Count')}
                    emptyText={t('No history data available')}
                  />
                </Suspense>
              </SideDrawerSection>

              <SideDrawerSection>
                <SideDrawerSectionHeader
                  title={t('Throughput')}
                  description={t('Output tokens per second per hour')}
                  icon={<TrendingUp aria-hidden='true' />}
                  iconTone='chart-2'
                />
                <Suspense fallback={<Skeleton className='h-44 w-full rounded-lg' />}>
                  <LineChart
                    values={toThroughputSeries(series)}
                    color='#6366f1'
                    formatValue={(value) => formatThroughput(value)}
                    labelKey={t('Throughput')}
                    emptyText={t('No history data available')}
                  />
                </Suspense>
              </SideDrawerSection>
            </>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}

export default EntityPerformanceDrawer
