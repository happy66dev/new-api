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

// UPSTREAM_MODEL_PREFIX 是模型广场中用户共享上游模型的调用前缀喵。
const UPSTREAM_MODEL_PREFIX = 'upstream/'

type SharedUpstreamStatusDotProps = {
  // modelName 是模型广场中的条目名称，形如 upstream/<name> 喵。
  modelName: string
}

// SharedUpstreamStatusDot 以共享使用者身份拉取共享聚合状态并渲染健康圆点喵。
// 共享视角后端不返回 24h 序列，因此只展示圆点与悬停摘要，无详情弹层喵。
export function SharedUpstreamStatusDot(props: SharedUpstreamStatusDotProps) {
  // 从调用前缀剥离出规范名，供共享状态接口查询喵。
  const normalizedName = props.modelName.startsWith(UPSTREAM_MODEL_PREFIX)
    ? props.modelName.slice(UPSTREAM_MODEL_PREFIX.length)
    : props.modelName
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
