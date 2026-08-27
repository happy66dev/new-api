/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'

import {
  EntityStatusDot,
  type EntityStatusSummary,
} from '@/features/status-check/entity-status-dot'
import { getSharedUpstreamModelStatus } from '@/features/upstream-models/api'

// USER_MODEL_PREFIX 是用户上游模型的规范调用前缀，模型广场条目与 playground 均使用它喵。
const USER_MODEL_PREFIX = 'user/'
// LEGACY_UPSTREAM_PREFIX 是历史遗留前缀，仍被 NormalizeUserUpstreamModelName 兼容解析喵。
const LEGACY_UPSTREAM_PREFIX = 'upstream/'

type SharedUpstreamStatusDotProps = {
  // modelName 是模型广场中的条目名称，形如 user/<name>，历史数据也可能是 upstream/<name> 喵。
  modelName: string
}

// extractSharedModelName 从调用前缀中剥离出规范名，供共享状态接口查询喵。
function extractSharedModelName(modelName: string): string {
  // 喵~防御：空名称原样返回，避免切片越界喵。
  if (modelName.startsWith(USER_MODEL_PREFIX)) {
    return modelName.slice(USER_MODEL_PREFIX.length)
  }
  if (modelName.startsWith(LEGACY_UPSTREAM_PREFIX)) {
    return modelName.slice(LEGACY_UPSTREAM_PREFIX.length)
  }
  return modelName
}

// SharedUpstreamStatusDot 以共享使用者身份拉取共享聚合状态并渲染健康圆点喵。
// 共享视角后端不返回 24h 序列，因此只展示圆点与悬停摘要，无详情弹层喵。
export function SharedUpstreamStatusDot(props: SharedUpstreamStatusDotProps) {
  // 从调用前缀剥离出规范名，供共享状态接口查询喵。
  const normalizedName = extractSharedModelName(props.modelName)
  const statusQuery = useQuery({
    queryKey: ['upstream-shared-status', normalizedName],
    queryFn: () => getSharedUpstreamModelStatus(normalizedName),
    staleTime: 30 * 1000,
    retry: false,
  })
  const status = statusQuery.data?.data
  // 共享载荷字段较少，规整为摘要的公共形状喵。
  const summary: EntityStatusSummary | undefined = status
    ? {
        availability: status.availability,
        avg_latency_ms: status.avg_latency_ms,
        request_count: status.request_count,
        last_at: status.last_at,
        last_success: status.last_success,
      }
    : undefined
  return (
    <EntityStatusDot
      // 悬停摘要优先展示原始条目名，方便识别是哪个共享模型喵。
      label={props.modelName}
      summary={summary}
      loading={statusQuery.isLoading}
      error={Boolean(statusQuery.isError)}
      // 卡片空间紧凑，只展示圆点不展示可用性百分比文字喵。
      showAvailabilityText={false}
    />
  )
}
