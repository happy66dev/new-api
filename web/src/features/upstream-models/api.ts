import { api } from '@/lib/api'

// UserUpstreamModel 是控制面读取用户上游模型时可安全展示的脱敏响应结构喵。
export type UserUpstreamModel = {
  id: number
  owner_user_id: number
  normalized_name: string
  display_name: string
  // description 是模型简介，展示在模型广场卡片上喵。
  description: string
  // icon 是模型广场卡片的 @lobehub/icons 图标键名，可选喵。
  icon?: string
  enabled: boolean
  // api_key_set 标记是否已配置密钥，响应中绝不回显密钥喵。
  api_key_set: boolean
  // base_url 返回明文地址（非密钥）便于前端编辑展示喵。
  base_url: string
  real_model_name: string
  auth_style: 'bearer' | 'api_key' | 'anthropic' | string
  // api_type 上游 API 类型：openai=OpenAI 兼容（默认）/ anthropic=Anthropic 原生，决定 relay 格式转换方向喵。
  api_type: 'openai' | 'anthropic' | string
  // timeout_seconds 自用调用超时，单位：秒；零表示使用默认 60 秒喵。
  timeout_seconds: number
  // 自定义请求头：结构化 JSON（{"*": true, "User-Agent": "..."}），防止 UA 判断拦截喵。
  custom_headers: string
  // 请求字段替换：字段路径 → 旧值→新值映射表（如 {"reasoning_effort": {"max": "xhigh"}}）喵。
  field_replacements: string
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
  // 共享白名单/黑名单：逗号分隔的用户 id，非白名单或黑名单用户不可见不可调用喵。
  share_whitelist: string
  share_blacklist: string
  // 共享名单模式：whitelist=白名单制 / blacklist=黑名单制，显式二选一喵。
  share_list_mode: '' | 'whitelist' | 'blacklist' | string
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
  // icon 是模型广场卡片的 @lobehub/icons 图标键名，创建/编辑时可选喵。
  icon?: string
  enabled: boolean
  // base_url/api_key 编辑时留空表示保留原有配置喵。
  base_url?: string
  api_key?: string
  real_model_name: string
  auth_style: string
  // api_type 上游 API 类型：openai=OpenAI 兼容（默认）/ anthropic=Anthropic 原生，编辑时可选喵。
  api_type?: string
  // timeout_seconds 自用调用超时，单位：秒；零表示使用默认 60 秒喵。
  timeout_seconds?: number
  // 自定义请求头与字段替换在 Q 实现，P1 前端暂不提供编辑入口，字段保持可选喵。
  custom_headers?: string
  field_replacements?: string
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
  // 共享白名单/黑名单：逗号分隔的用户 id，编辑时可选喵。
  share_whitelist?: string
  share_blacklist?: string
  // 共享名单模式：whitelist=白名单制 / blacklist=黑名单制 / 空=不限制，显式二选一喵。
  share_list_mode?: string
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

// UpstreamModelStatus 是状态检测接口返回的属主视角聚合统计喵。
export type UpstreamModelStatus = {
  // 可用性是 0-100 的百分比喵。
  availability: number
  avg_latency_ms: number
  avg_ttft_ms: number
  cache_hit_rate: number
  total_tokens: number
  request_count: number
  // availability_24h 是最近 24 个采样点的可用性序列，供 AvailabilityBars 展示喵。
  availability_24h: number[]
  // series 是逐小时桶明细，供性能抽屉图表喵。
  series: EntityProbeBucket[]
  last_at: number
  last_success: boolean
  last_latency_ms: number
  last_error: string
  // current_requests 当前处理中的客户端请求数（自用维度）喵。
  current_requests: number
  // shared 是共享调用维度的聚合，仅属主请求 include_shared=true 且共享有数据时携带喵。
  shared?: UpstreamModelSharedStatus
}

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
  // 缓存写入 5m/1h 分类 token（Claude 语义），测试模型探测记录喵。
  cache_creation_5m_tokens: number
  cache_creation_1h_tokens: number
}

// UpstreamModelSharedStatus 是共享使用者视角的状态，不含错误明细与 24h 序列喵。
export type UpstreamModelSharedStatus = {
  availability: number
  avg_latency_ms: number
  avg_ttft_ms: number
  cache_hit_rate: number
  total_tokens: number
  request_count: number
  // current_requests 当前处理中的共享调用请求数喵。
  current_requests: number
  // series 是共享调用维度的逐小时桶明细喵。
  series: EntityProbeBucket[]
  last_at: number
  last_success: boolean
}

// getUpstreamModelStatus 查询属主视角的上游模型状态，includeShared 时附加共享维度喵。
export async function getUpstreamModelStatus(
  id: number,
  includeShared = false
): Promise<UserUpstreamModelApiResponse<UpstreamModelStatus>> {
  const response = await api.get(`/api/upstream-models/${id}/status`, {
    params: { include_shared: includeShared ? 'true' : 'false' },
  })
  return response.data
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

// UpstreamModelUserUsage 是共享模型按用户聚合的使用情况单行喵。
// 只含请求数、总 token（输入+输出合计）与费用金额（分），不再细分输入/输出喵。
export type UpstreamModelUserUsage = {
  user_id: number
  username: string
  request_count: number
  total_tokens: number
  cost_cents: number
  last_at: number
}

// getUpstreamModelUserUsage 查询共享上游模型按用户的使用情况，仅属主可访问喵。
export async function getUpstreamModelUserUsage(
  id: number
): Promise<UserUpstreamModelApiResponse<UpstreamModelUserUsage[]>> {
  const response = await api.get(`/api/upstream-models/${id}/usage`)
  return response.data
}

// clearUpstreamModelUserUsage 清空共享上游模型的按用户使用记录，仅属主可访问喵。
export async function clearUpstreamModelUserUsage(
  id: number
): Promise<UserUpstreamModelApiResponse<{ id: number }>> {
  const response = await api.delete(`/api/upstream-models/${id}/usage`)
  return response.data
}
