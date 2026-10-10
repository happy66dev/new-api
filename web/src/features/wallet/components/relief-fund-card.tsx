/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { LifeBuoy } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { handleServerError } from '@/lib/handle-server-error'

import { claimRelief, getReliefStatus, type ReliefStatusData } from '../api'

interface ReliefFundCardProps {
  // 领取成功后回调父级刷新钱包余额
  onClaimed?: () => void
}

// formatUsd 把美元数值格式化成 "$12.34"（固定两位小数），对外展示用中性文案
function formatUsd(value: number | undefined): string {
  // 空值兜底为 0，避免显示 "$undefined"
  return `$${(value ?? 0).toFixed(2)}`
}

// formatCountdown 把剩余秒数格式化成 "H:MM:SS"，用于冷却倒计时展示
function formatCountdown(totalSeconds: number): string {
  // 夹到非负，避免负数倒计时
  const safe = Math.max(0, Math.floor(totalSeconds))
  const hours = Math.floor(safe / 3600) // 小时部分
  const minutes = Math.floor((safe % 3600) / 60) // 分钟部分
  const seconds = safe % 60 // 秒部分
  // 分、秒补零到两位
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${hours}:${pad(minutes)}:${pad(seconds)}`
}

export function ReliefFundCard(props: ReliefFundCardProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  // 拉取救济金状态（资格、剩余、冷却等）
  const statusQuery = useQuery({
    queryKey: ['relief-status'],
    queryFn: getReliefStatus,
  })
  const data: ReliefStatusData | undefined = statusQuery.data?.data

  // 本地冷却倒计时：以接口返回的剩余秒数为基准，每秒自减一次
  const [cooldownLeft, setCooldownLeft] = useState(0)
  useEffect(() => {
    // 接口刷新时重置倒计时基准
    setCooldownLeft(data?.cooldown_remaining_seconds ?? 0)
  }, [data?.cooldown_remaining_seconds])
  useEffect(() => {
    // 没有剩余冷却就不开定时器
    if (cooldownLeft <= 0) return
    const timer = setInterval(() => {
      setCooldownLeft((prev) => (prev <= 1 ? 0 : prev - 1))
    }, 1000)
    // 组件卸载或依赖变化时清理定时器，避免泄漏
    return () => clearInterval(timer)
  }, [cooldownLeft])

  // 领取救济金的变更操作
  const claimMutation = useMutation({
    mutationFn: claimRelief,
    onSuccess: (response) => {
      // 后端业务失败（success=false）：展示其中性中文消息
      if (!response.success) {
        toast.error(response.message || t('Failed to claim relief fund'))
        return
      }
      toast.success(t('Relief fund claimed successfully'))
      // 刷新救济金状态与父级钱包余额
      void queryClient.invalidateQueries({ queryKey: ['relief-status'] })
      props.onClaimed?.()
    },
    onError: (error: Error) => {
      handleServerError(error, t('Failed to claim relief fund'))
    },
  })

  // 加载中骨架屏
  if (statusQuery.isLoading) {
    return (
      <Card data-card-hover='false' className='bg-muted/20 py-0'>
        <CardContent className='flex items-center gap-3 p-3 sm:p-4'>
          <Skeleton className='size-9 rounded-lg' />
          <div className='flex-1'>
            <Skeleton className='h-5 w-32' />
            <Skeleton className='mt-2 h-4 w-48' />
          </div>
          <Skeleton className='h-9 w-24 rounded-lg' />
        </CardContent>
      </Card>
    )
  }

  // 接口异常或未启用时不渲染卡片（父级也已按 relief_enabled 控制，这里再兜一层）
  if (!data || !data.enabled) {
    return null
  }

  // 冷却是否进行中（以本地倒计时为准）
  const cooling = cooldownLeft > 0
  // 是否可领取：后端判定可领 且 本地冷却已结束
  const canClaim = Boolean(data.eligible) && !cooling

  // 不可领取时的原因文案：把后端机器码翻译成本地化中性文案
  const reasonText = (() => {
    // 冷却中优先展示倒计时提示
    if (cooling) {
      return t('Relief fund is cooling down, time remaining {{time}}', {
        time: formatCountdown(cooldownLeft),
      })
    }
    switch (data.reason_code) {
      case 'balance_too_high':
        return t('Your wallet balance is above the claim threshold')
      case 'subscription_available':
        return t('You still have an available subscription')
      case 'pool_empty':
        return t('Relief pool is temporarily depleted')
      case 'cooldown':
        return t('Relief fund is cooling down')
      default:
        return ''
    }
  })()

  return (
    <Card data-card-hover='false' className='bg-muted/20 py-0'>
      <CardContent className='grid gap-3 p-3 sm:gap-4 sm:p-4 lg:grid-cols-[minmax(220px,1fr)_minmax(200px,0.9fr)_auto] lg:items-center'>
        <div className='flex min-w-0 items-center gap-2.5'>
          <IconBadge tone='warning'>
            <LifeBuoy />
          </IconBadge>
          <div className='min-w-0'>
            <h3 className='truncate text-sm font-semibold'>
              {t('Relief Fund')}
            </h3>
            <p className='text-muted-foreground line-clamp-2 text-xs'>
              {t(
                'When your balance runs low and subscriptions are used up, claim a time-limited relief grant.'
              )}
            </p>
          </div>
        </div>

        <div className='grid grid-cols-2 gap-1.5 text-center'>
          <div>
            <div className='text-muted-foreground truncate text-[10px] font-medium tracking-wider uppercase'>
              {t('Relief Remaining')}
            </div>
            <div className='mt-0.5 truncate text-sm font-semibold tabular-nums'>
              {formatUsd(data.remaining_usd)}
            </div>
          </div>
          <div>
            <div className='text-muted-foreground truncate text-[10px] font-medium tracking-wider uppercase'>
              {t('Grant Amount')}
            </div>
            <div className='mt-0.5 truncate text-sm font-semibold tabular-nums'>
              {formatUsd(data.grant_amount_usd)}
            </div>
          </div>
        </div>

        <div className='flex flex-col items-stretch gap-1 lg:items-end'>
          <Button
            onClick={() => claimMutation.mutate()}
            disabled={!canClaim || claimMutation.isPending}
            className='h-9 shrink-0 px-3'
            size='sm'
          >
            {t('Claim Relief Fund')}
          </Button>
          {!canClaim && reasonText ? (
            <p className='text-muted-foreground text-right text-xs'>
              {reasonText}
            </p>
          ) : null}
        </div>
      </CardContent>
    </Card>
  )
}
