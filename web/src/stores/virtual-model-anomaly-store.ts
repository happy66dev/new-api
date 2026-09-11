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
import { create } from 'zustand'
import { persist } from 'zustand/middleware'

// VirtualModelAnomalyFingerprint 是虚拟模型候选异常的唯一指纹，用于判定某条异常是否已读喵。
export type VirtualModelAnomalyFingerprint = string

// buildVirtualModelAnomalyFingerprint 由异常的关键字段拼接出稳定指纹喵。
// 指纹包含候选编号、原因码与路由目标：同一候选出现不同原因或目标变化时视为新异常，会重新亮红点喵。
export function buildVirtualModelAnomalyFingerprint(input: {
  virtualModelID: number
  candidateID: number
  reasonCode: string
  groupName?: string
  realModelName?: string
}): VirtualModelAnomalyFingerprint {
  return [
    input.virtualModelID,
    input.candidateID,
    input.reasonCode,
    input.groupName ?? '',
    input.realModelName ?? '',
  ].join(':')
}

interface VirtualModelAnomalyState {
  // readAnomalyFingerprints 是用户已看过的候选异常指纹集合，持久化到 localStorage 喵。
  readAnomalyFingerprints: VirtualModelAnomalyFingerprint[]
  // markAnomaliesRead 把一批指纹标记为已读（去重合并）喵。
  markAnomaliesRead: (fingerprints: VirtualModelAnomalyFingerprint[]) => void
  // isAnomalyRead 判断某条异常指纹是否已读喵。
  isAnomalyRead: (fingerprint: VirtualModelAnomalyFingerprint) => boolean
}

// useVirtualModelAnomalyStore 管理候选被动变化异常的已读状态喵。
// 已读存浏览器本地：换设备/浏览器后红点会重新出现，与通知中心行为一致喵。
export const useVirtualModelAnomalyStore = create<VirtualModelAnomalyState>()(
  persist(
    (set, get) => ({
      readAnomalyFingerprints: [],

      markAnomaliesRead: (fingerprints) => {
        // 去重合并新指纹，避免重复项膨胀 localStorage 喵。
        set((state) => ({
          readAnomalyFingerprints: [
            ...new Set([...state.readAnomalyFingerprints, ...fingerprints]),
          ],
        }))
      },

      isAnomalyRead: (fingerprint) => {
        return get().readAnomalyFingerprints.includes(fingerprint)
      },
    }),
    {
      name: 'virtual-model-anomaly-storage',
      partialize: (state) => ({
        readAnomalyFingerprints: state.readAnomalyFingerprints,
      }),
    }
  )
)
