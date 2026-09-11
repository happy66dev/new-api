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
import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'

import { useAuthStore } from '@/stores/auth-store'
import {
  buildVirtualModelAnomalyFingerprint,
  useVirtualModelAnomalyStore,
} from '@/stores/virtual-model-anomaly-store'

import { getVirtualModelAnomalies, type VirtualModelAnomaly } from '../api'

// anomalyFingerprint 由异常记录生成稳定指纹，供已读判定使用喵。
function anomalyFingerprint(anomaly: VirtualModelAnomaly): string {
  return buildVirtualModelAnomalyFingerprint({
    virtualModelID: anomaly.virtual_model_id,
    candidateID: anomaly.candidate_id,
    reasonCode: anomaly.reason_code,
    groupName: anomaly.group_name,
    realModelName: anomaly.real_model_name,
  })
}

// useVirtualModelAnomalies 拉取并整理候选被动变化异常清单喵。
// 每 5 秒轮询一次（与 Support 未读一致），侧边栏/页面红点据此保持最新喵。
export function useVirtualModelAnomalies() {
  // 未登录时不轮询异常接口，避免匿名请求污染认证态喵。
  const userId = useAuthStore((state) => state.auth.user?.id)
  const anomaliesQuery = useQuery({
    queryKey: ['virtual-model-anomalies'],
    queryFn: getVirtualModelAnomalies,
    // 轮询接口禁用缓存，确保总能拿到后端最新扫描结果喵。
    staleTime: 0,
    refetchInterval: 5000,
    refetchIntervalInBackground: false,
    retry: false,
    enabled: Boolean(userId),
  })
  // 用 useMemo 稳定异常数组引用：data 未加载时返回同一空数组，避免依赖变化引发重渲染喵。
  const anomalies = useMemo(
    () => anomaliesQuery.data?.data?.anomalies ?? [],
    [anomaliesQuery.data]
  )

  // 订阅已读指纹集合；数组变化（用户标记已读）会触发重算红点喵。
  const readFingerprints = useVirtualModelAnomalyStore(
    (state) => state.readAnomalyFingerprints
  )
  const markAnomaliesRead = useVirtualModelAnomalyStore(
    (state) => state.markAnomaliesRead
  )

  return useMemo(() => {
    // 未读异常 = 扫描结果中指纹不在已读集合里的异常喵。
    const unreadAnomalies = anomalies.filter((anomaly) => {
      const fingerprint = anomalyFingerprint(anomaly)
      return !readFingerprints.includes(fingerprint)
    })
    // 按虚拟模型归组，供模型卡片红点判定喵。
    const unreadByModelId = new Map<number, VirtualModelAnomaly[]>()
    for (const anomaly of unreadAnomalies) {
      const grouped = unreadByModelId.get(anomaly.virtual_model_id) ?? []
      grouped.push(anomaly)
      unreadByModelId.set(anomaly.virtual_model_id, grouped)
    }
    // 按候选归组，供候选行红点判定喵。
    const unreadByCandidateId = new Map<number, VirtualModelAnomaly>()
    for (const anomaly of unreadAnomalies) {
      unreadByCandidateId.set(anomaly.candidate_id, anomaly)
    }
    return {
      anomalies,
      unreadAnomalies,
      unreadByModelId,
      unreadByCandidateId,
      hasUnread: unreadAnomalies.length > 0,
      // markAllUnreadAsRead 把当前全部未读异常标记已读，供弹层关闭时调用喵。
      markAllUnreadAsRead: () => {
        if (unreadAnomalies.length === 0) return
        markAnomaliesRead(unreadAnomalies.map(anomalyFingerprint))
      },
    }
  }, [anomalies, readFingerprints, markAnomaliesRead])
}
