import { api } from '@/lib/api'

// UserUpstreamModel 是控制面读取用户上游模型时可安全展示的脱敏响应结构喵。
export type UserUpstreamModel = {
  id: number
  owner_user_id: number
  normalized_name: string
  display_name: string
  // description 是模型简介，展示在模型广场卡片上喵。
  description: string
  enabled: boolean
  // api_key_set 标记是否已配置密钥，响应中绝不回显密钥喵。
  api_key_set: boolean
  // base_url 返回明文地址（非密钥）便于前端编辑展示喵。
  base_url: string
  real_model_name: string
  auth_style: 'bearer' | 'api_key' | 'anthropic' | string
  model_ratio: string
  completion_ratio: string
  cache_ratio: string
  cache_creation_ratio: string
  cache_creation_5m_ratio: string
  cache_creation_1h_ratio: string
  image_ratio: string
  audio_ratio: string
  audio_completion_ratio: string
  // 以下金额字段单位都是"分"（RMB）喵。
  balance_cents: number
  // 可用额度 = 用户能接受用那么多（递减账户）喵。
  available_cents: number
  // 以下字段为旧版余额体系遗留，仅作展示兼容不再参与扣费喵。
  spend_limit_cents: number
  total_spent_cents: number
  upstream_remaining_cents: number
  upstream_remaining_at: number
  balance_check_enabled: boolean
  balance_check_path: string
  share_enabled: boolean
  share_limit_cents: number
  share_spent_cents: number
  show_balance_enabled: boolean
  version: number
  created_time: number
  updated_time: number
}

// UserUpstreamModelInput 描述创建或更新用户上游模型的可写字段喵。
export type UserUpstreamModelInput = {
  normalized_name: string
  display_name: string
  description: string
  enabled: boolean
  // base_url/api_key 编辑时留空表示保留原有配置喵。
  base_url?: string
  api_key?: string
  real_model_name: string
  auth_style: string
  model_ratio: string
  completion_ratio: string
  cache_ratio: string
  cache_creation_ratio: string
  cache_creation_5m_ratio: string
  cache_creation_1h_ratio: string
  image_ratio: string
  audio_ratio: string
  audio_completion_ratio: string
  balance_cents: number
  available_cents: number
  // 嗅探配置在 P3 实现，P1 前端暂不提供编辑入口，字段保持可选喵。
  balance_check_enabled?: boolean
  balance_check_path?: string
  share_enabled: boolean
  share_limit_cents: number
  show_balance_enabled: boolean
  version?: number
}

export type UserUpstreamModelDeleteInput = {
  version: number
}

export type UserUpstreamModelApiResponse<T> = {
  success: boolean
  message?: string
  code?: string
  data?: T
}

export async function getUserUpstreamModels(): Promise<
  UserUpstreamModelApiResponse<UserUpstreamModel[]>
> {
  const response = await api.get('/api/upstream-models')
  return response.data
}

export async function createUserUpstreamModel(
  input: UserUpstreamModelInput
): Promise<UserUpstreamModelApiResponse<UserUpstreamModel>> {
  const response = await api.post('/api/upstream-models', input)
  return response.data
}

export async function updateUserUpstreamModel(
  id: number,
  input: UserUpstreamModelInput
): Promise<UserUpstreamModelApiResponse<UserUpstreamModel>> {
  const response = await api.put(`/api/upstream-models/${id}`, input)
  return response.data
}

export async function deleteUserUpstreamModel(
  id: number,
  input: UserUpstreamModelDeleteInput
): Promise<UserUpstreamModelApiResponse<{ id: number }>> {
  const response = await api.delete(`/api/upstream-models/${id}`, { data: input })
  return response.data
}

// 嗅探剩余额度：只更新展示字段，不改变可用余额喵。
export async function checkUserUpstreamModelBalance(
  id: number
): Promise<UserUpstreamModelApiResponse<{ upstream_remaining_cents: number; upstream_remaining_at: number }>> {
  const response = await api.post(`/api/upstream-models/${id}/balance-check`)
  return response.data
}

// 一键设为余额：把嗅探结果写入「余额」账户喵。
export async function syncUserUpstreamModelBalance(
  id: number
): Promise<UserUpstreamModelApiResponse<{ balance_cents: number; upstream_remaining_cents: number }>> {
  const response = await api.post(`/api/upstream-models/${id}/balance/sync`)
  return response.data
}

// 一键设为可用：把嗅探结果写入「可用额度」账户喵。
export async function syncUserUpstreamModelAvailable(
  id: number
): Promise<UserUpstreamModelApiResponse<{ available_cents: number; upstream_remaining_cents: number }>> {
  const response = await api.post(`/api/upstream-models/${id}/balance/sync-available`)
  return response.data
}
