/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { afterEach, describe, expect, test } from 'vitest'

import { api } from '@/lib/api'

import {
  createVirtualModel,
  deleteVirtualModel,
  freezeVirtualModelCandidate,
  getVirtualModelStatus,
  getVirtualModels,
  replaceVirtualModelCandidates,
  replaceVirtualModelBindings,
  unfreezeVirtualModelCandidate,
  updateVirtualModel,
} from './api'

// MockableApiClient 只声明本测试需要替换的 HTTP 方法，避免伪造完整 Axios 实例喵。
type MockableApiClient = {
  delete: (url: string, config?: { data?: unknown }) => Promise<{ data: unknown }>
  get: (url: string) => Promise<{ data: unknown }>
  post: (url: string, payload: unknown) => Promise<{ data: unknown }>
  put: (url: string, payload: unknown) => Promise<{ data: unknown }>
}

// apiClient 允许测试验证 API 请求路径和载荷，不会发出真实网络请求喵。
const apiClient = api as unknown as MockableApiClient
// originalGet 保存原始读取方法，保证每个用例结束后恢复共享客户端喵。
const originalGet = apiClient.get
// originalPost 保存原始创建方法，保证每个用例结束后恢复共享客户端喵。
const originalPost = apiClient.post
// originalPut 保存原始更新方法，保证每个用例结束后恢复共享客户端喵。
const originalPut = apiClient.put
// originalDelete 保存原始删除方法，保证每个用例结束后恢复共享客户端喵。
const originalDelete = apiClient.delete

// modelInput 提供满足后端约束的最小虚拟模型表单数据喵。
const modelInput = {
  normalized_name: 'research-route',
  display_name: 'Research Route',
  enabled: true,
  loop_enabled: false,
  total_timeout_seconds: 120,
  max_loop_rounds: 1,
}

// 每个用例后恢复全局 HTTP 客户端，防止 mock 泄漏到其他测试喵。
afterEach(() => {
  apiClient.get = originalGet
  apiClient.post = originalPost
  apiClient.put = originalPut
  apiClient.delete = originalDelete
})

describe('virtual model API', () => {
  test('loads the current user virtual models from the user endpoint', async () => {
    // mock GET 校验列表读取不会误用管理员或 Token AutoRoutes 接口喵。
    apiClient.get = async (url) => {
      expect(url).toBe('/api/virtual-models')
      return { data: { success: true, data: [] } }
    }

    const result = await getVirtualModels()

    expect(result).toEqual({ success: true, data: [] })
  })

  test('creates a virtual model with all configuration fields intact', async () => {
    // mock POST 校验创建请求完整保留用户配置字段喵。
    apiClient.post = async (url, payload) => {
      expect(url).toBe('/api/virtual-models')
      expect(payload).toEqual(modelInput)
      return { data: { success: true, data: { id: 21, ...modelInput, version: 1 } } }
    }

    const result = await createVirtualModel(modelInput)

    expect(result.data?.id).toBe(21)
  })

  test('updates a virtual model at its scoped resource endpoint', async () => {
    const updateInput = { ...modelInput, enabled: false, version: 7 }
    // mock PUT 校验更新使用资源编号并携带乐观锁版本喵。
    apiClient.put = async (url, payload) => {
      expect(url).toBe('/api/virtual-models/21')
      expect(payload).toEqual(updateInput)
      return { data: { success: true, data: { id: 21, ...updateInput } } }
    }

    const result = await updateVirtualModel(21, updateInput)

    expect(result.data?.enabled).toBe(false)
  })

  test('replaces the entire candidate chain through the scoped endpoint', async () => {
    const candidates = [
      {
        source_type: 'internal' as const,
        enabled: true,
        max_retries: 0,
        timeout_seconds: 60,
        group_name: 'default',
        real_model_name: 'gpt-4.1-mini',
      },
      {
        source_type: 'custom' as const,
        enabled: true,
        max_retries: 2,
        timeout_seconds: 120,
        base_url: 'https://api.example.com',
        api_key: 'upstream-secret',
        auth_style: 'bearer' as const,
        real_model_name: 'gpt-4.1',
      },
    ]
    const candidateInput = { version: 7, candidates }
    // mock PUT 校验自定义候选凭据只随写入载荷传输，候选顺序与来源字段完整传递到版本化整体替换接口喵。
    apiClient.put = async (url, payload) => {
      expect(url).toBe('/api/virtual-models/21/candidates')
      expect(payload).toEqual(candidateInput)
      return { data: { success: true, data: { id: 21, ...modelInput, version: 2 } } }
    }

    const result = await replaceVirtualModelCandidates(21, candidateInput)

    expect(result.data?.version).toBe(2)
  })

  test('replaces API Key bindings with the current optimistic-lock version', async () => {
    const bindings = { token_ids: [4, 8], version: 7 }
    // mock PUT 校验授权更新使用模型范围路径并携带 version 喵。
    apiClient.put = async (url, payload) => {
      expect(url).toBe('/api/virtual-models/21/key-bindings')
      expect(payload).toEqual(bindings)
      return { data: { success: true, data: { id: 21, ...modelInput, version: 8 } } }
    }

    const result = await replaceVirtualModelBindings(21, bindings)

    expect(result.data?.version).toBe(8)
  })

  test('loads runtime status for one virtual model', async () => {
    // mock GET 校验状态查询始终挂在指定模型下喵。
    apiClient.get = async (url) => {
      expect(url).toBe('/api/virtual-models/21/status')
      return {
        data: {
          success: true,
          data: { model: 'virtual/research-route', enabled: true, candidate_count: 2, enabled_candidates: 1 },
        },
      }
    }

    const result = await getVirtualModelStatus(21)

    expect(result.data?.enabled_candidates).toBe(1)
  })

  test('freezes one candidate with an explicit future expiration timestamp', async () => {
    const expiresAt = 1_900_000_000
    const version = 7
    // mock POST 校验冻结请求同时提交候选编号、到期时间和乐观锁版本喵。
    apiClient.post = async (url, payload) => {
      expect(url).toBe('/api/virtual-models/21/candidates/42/freeze')
      expect(payload).toEqual({ expires_at: expiresAt, version })
      return { data: { success: true, data: { candidate_id: 42, expires_at: expiresAt, version: 8 } } }
    }

    const result = await freezeVirtualModelCandidate(21, 42, expiresAt, version)

    expect(result.data?.candidate_id).toBe(42)
  })

  test('unfreezes one candidate through its scoped resource endpoint', async () => {
    const version = 7
    // mock DELETE 校验解除冻结不会误删候选或请求模型级端点，并携带乐观锁版本喵。
    apiClient.delete = async (url, config) => {
      expect(url).toBe('/api/virtual-models/21/candidates/42/freeze')
      expect(config).toEqual({ data: { version } })
      return { data: { success: true, data: { candidate_id: 42, version: 8 } } }
    }

    const result = await unfreezeVirtualModelCandidate(21, 42, version)

    expect(result.data?.candidate_id).toBe(42)
  })

  test('deletes a virtual model through its scoped resource endpoint', async () => {
    const version = 7
    // mock DELETE 校验删除不会错误请求列表端点，并提交乐观锁版本喵。
    apiClient.delete = async (url, config) => {
      expect(url).toBe('/api/virtual-models/21')
      expect(config).toEqual({ data: { version } })
      return { data: { success: true, data: { id: 21 } } }
    }

    const result = await deleteVirtualModel(21, { version })

    expect(result.data?.id).toBe(21)
  })
})
