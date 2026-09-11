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
import { beforeEach, describe, expect, it } from 'vitest'

import {
  buildVirtualModelAnomalyFingerprint,
  useVirtualModelAnomalyStore,
} from '../virtual-model-anomaly-store'

describe('virtual-model-anomaly-store', () => {
  beforeEach(() => {
    // 每个用例前重置已读集合，避免模块级单例状态污染后续断言喵。
    useVirtualModelAnomalyStore.setState({ readAnomalyFingerprints: [] })
    window.localStorage.clear()
  })

  it('buildVirtualModelAnomalyFingerprint 拼接稳定指纹并覆盖路由目标变化', () => {
    // 同候选同原因同目标：指纹稳定可重复喵。
    const fingerprintA = buildVirtualModelAnomalyFingerprint({
      virtualModelID: 1,
      candidateID: 2,
      reasonCode: 'model_not_available',
      groupName: 'default',
      realModelName: 'gpt-old',
    })
    const fingerprintB = buildVirtualModelAnomalyFingerprint({
      virtualModelID: 1,
      candidateID: 2,
      reasonCode: 'model_not_available',
      groupName: 'default',
      realModelName: 'gpt-old',
    })
    expect(fingerprintA).toBe(fingerprintB)
    // 路由目标变化（模型换成 gpt-new）视为新异常，指纹不同喵。
    const fingerprintC = buildVirtualModelAnomalyFingerprint({
      virtualModelID: 1,
      candidateID: 2,
      reasonCode: 'model_not_available',
      groupName: 'default',
      realModelName: 'gpt-new',
    })
    expect(fingerprintA).not.toBe(fingerprintC)
  })

  it('markAnomaliesRead 去重合并已读指纹，isAnomalyRead 正确判定', () => {
    const fingerprintA = buildVirtualModelAnomalyFingerprint({
      virtualModelID: 1,
      candidateID: 2,
      reasonCode: 'upstream_feature_disabled',
    })
    const fingerprintB = buildVirtualModelAnomalyFingerprint({
      virtualModelID: 3,
      candidateID: 4,
      reasonCode: 'group_not_accessible',
      groupName: 'vip',
    })
    const store = useVirtualModelAnomalyStore.getState()

    expect(store.isAnomalyRead(fingerprintA)).toBe(false)

    // 标记 A 后：A 已读，B 仍未读喵。
    store.markAnomaliesRead([fingerprintA])
    expect(
      useVirtualModelAnomalyStore.getState().isAnomalyRead(fingerprintA)
    ).toBe(true)
    expect(
      useVirtualModelAnomalyStore.getState().isAnomalyRead(fingerprintB)
    ).toBe(false)

    // 重复标记 A 不会膨胀集合，且追加 B 后两者都已读喵。
    store.markAnomaliesRead([fingerprintA, fingerprintB])
    const stateAfter = useVirtualModelAnomalyStore.getState()
    expect(stateAfter.isAnomalyRead(fingerprintA)).toBe(true)
    expect(stateAfter.isAnomalyRead(fingerprintB)).toBe(true)
    expect(stateAfter.readAnomalyFingerprints).toHaveLength(2)
  })

  it('已读指纹持久化到 localStorage', () => {
    const fingerprint = buildVirtualModelAnomalyFingerprint({
      virtualModelID: 9,
      candidateID: 8,
      reasonCode: 'referenced_upstream_missing',
    })
    useVirtualModelAnomalyStore.getState().markAnomaliesRead([fingerprint])

    // persist 中间件把状态序列化进 localStorage，供跨会话恢复喵。
    const stored = window.localStorage.getItem('virtual-model-anomaly-storage')
    expect(stored).toBeTruthy()
    expect(stored).toContain(fingerprint)
  })
})
