import { api } from '@/lib/api'

// VirtualModelCandidate 是控制面读取候选链时可安全展示的脱敏响应结构喵。
export type VirtualModelCandidate = {
  // id 是后端为候选分配的稳定标识，用于冻结和解除冻结操作喵。
  id: number
  stable_order: number
  source_type: 'internal' | 'custom' | string
  enabled: boolean
  max_retries: number
  timeout_seconds: number
  group_name?: string
  real_model_name?: string
  // 喵~防御：候选响应绝不声明或接收可回显的上游 API Key 喵。
  base_url?: string
  auth_style?: VirtualModelCandidateAuthStyle
  // 引用用户上游模型条目时非空，凭据与真实模型名以该条目为准喵。
  upstream_model_id?: number | null
  failure_rules?: VirtualModelFailureRule[]
}

// VirtualModelCandidateAuthStyle 限制自定义上游能使用的认证头协议喵。
export type VirtualModelCandidateAuthStyle = 'bearer' | 'api_key' | 'anthropic'

// VirtualModelCandidateInput 是仅用于写入候选链的结构，API Key 不会出现在读取响应中喵。
export type VirtualModelCandidateInput = {
  id?: number
  source_type: 'internal' | 'custom'
  enabled: boolean
  max_retries: number
  timeout_seconds: number
  group_name?: string
  real_model_name?: string
  base_url?: string
  api_key?: string
  auth_style?: VirtualModelCandidateAuthStyle
  // 引用用户上游模型条目时非空，直填凭据字段可省略喵。
  upstream_model_id?: number | null
}

// VirtualModelCandidatesReplaceInput 将候选链与读取时模型版本作为同一个原子写入请求发送喵。
export type VirtualModelCandidatesReplaceInput = {
  version: number
  candidates: VirtualModelCandidateInput[]
}

export type VirtualModelBindingsInput = {
  token_ids: number[]
  version: number
}

export type VirtualModel = {
  id: number
  normalized_name: string
  display_name: string
  enabled: boolean
  loop_enabled: boolean
  total_timeout_seconds: number
  max_loop_rounds: number
  version: number
  candidates?: VirtualModelCandidate[]
  binding_token_ids?: number[]
  // global_failure_rules 是模型级全局兜底失败规则，候选未配置规则时运行时按其决策喵。
  global_failure_rules?: VirtualModelFailureRule[]
}

export type VirtualModelInput = {
  normalized_name: string
  display_name: string
  enabled: boolean
  loop_enabled: boolean
  total_timeout_seconds: number
  max_loop_rounds: number
  version?: number
}

// VirtualModelVersionedDeleteInput 表示必须以读取时版本确认的删除请求喵。
export type VirtualModelVersionedDeleteInput = {
  version: number
}

// VirtualModelStatus 是虚拟模型整体状态响应，含整体指标与候选节点摘要喵。
export type VirtualModelStatus = {
  model: string
  enabled: boolean
  candidate_count: number
  enabled_candidates: number
  // 以下为实体状态检测新增字段：整体可用性/延迟/请求数/24h 序列/最近一次喵。
  availability: number
  avg_latency_ms: number
  request_count: number
  availability_24h: number[]
  last_at: number
  last_success: boolean
  last_latency_ms: number
  last_error: string
  // candidates 是启用候选快照的节点摘要，供 Overview 状态卡片展示喵。
  candidates: VirtualModelCandidateStatus[]
}

// VirtualModelCandidateStatus 是单个候选节点的状态摘要，不含 24h 序列喵。
export type VirtualModelCandidateStatus = {
  candidate_id: number
  label: string
  availability: number
  avg_latency_ms: number
  request_count: number
  last_at: number
  last_success: boolean
  last_error: string
}

export type VirtualModelApiResponse<T> = {
  success: boolean
  message?: string
  code?: string
  data?: T
}

export async function getVirtualModels(): Promise<VirtualModelApiResponse<VirtualModel[]>> {
  const response = await api.get('/api/virtual-models')
  return response.data
}

export async function createVirtualModel(
  input: VirtualModelInput
): Promise<VirtualModelApiResponse<VirtualModel>> {
  const response = await api.post('/api/virtual-models', input)
  return response.data
}

export async function updateVirtualModel(
  id: number,
  input: VirtualModelInput
): Promise<VirtualModelApiResponse<VirtualModel>> {
  const response = await api.put(`/api/virtual-models/${id}`, input)
  return response.data
}

export async function replaceVirtualModelCandidates(
  id: number,
  input: VirtualModelCandidatesReplaceInput
): Promise<VirtualModelApiResponse<VirtualModel>> {
  const response = await api.put(`/api/virtual-models/${id}/candidates`, input)
  return response.data
}

export type VirtualModelFailureRule = {
  id?: number
  http_status: number
  // http_status_max 是状态码范围匹配上界，零或省略表示仅匹配 http_status 单值喵。
  http_status_max?: number
  error_class: string
  body_regex: string
  action: 'retry' | 'next' | 'freeze' | 'passthrough'
  freeze_seconds: number
  // freeze_field 是响应体中的冻结时间字段名，非空时启用从响应体解析冻结时间喵。
  freeze_field?: string
  // freeze_unit 标记响应体字段冻结时间的单位，仅在 freeze_field 非空时生效喵。
  freeze_unit?: 'seconds' | 'minutes' | 'mixed' | 'auto'
}

export type VirtualModelFailureRulesReplaceInput = {
  version: number
  rules: VirtualModelFailureRule[]
}

export async function replaceVirtualModelCandidateFailureRules(
  modelID: number,
  candidateID: number,
  input: VirtualModelFailureRulesReplaceInput
): Promise<VirtualModelApiResponse<VirtualModel>> {
  const response = await api.put(
    `/api/virtual-models/${modelID}/candidates/${candidateID}/failure-rules`,
    input
  )
  return response.data
}

// replaceVirtualModelGlobalFailureRules 原子替换模型级全局兜底失败规则喵。
export async function replaceVirtualModelGlobalFailureRules(
  modelID: number,
  input: VirtualModelFailureRulesReplaceInput
): Promise<VirtualModelApiResponse<VirtualModel>> {
  const response = await api.put(`/api/virtual-models/${modelID}/failure-rules`, input)
  return response.data
}

export async function replaceVirtualModelBindings(
  id: number,
  input: VirtualModelBindingsInput
): Promise<VirtualModelApiResponse<VirtualModel>> {
  const response = await api.put(`/api/virtual-models/${id}/key-bindings`, input)
  return response.data
}

export async function deleteVirtualModel(
  id: number,
  input: VirtualModelVersionedDeleteInput
): Promise<VirtualModelApiResponse<{ id: number }>> {
  const response = await api.delete(`/api/virtual-models/${id}`, { data: input })
  return response.data
}

// getVirtualModelStatus 读取运行状态；失败时由页面内联展示而非全局 toast，
// 避免模型删除后 in-flight 状态请求返回 404 触发"虚拟模型不存在"弹窗喵。
export async function getVirtualModelStatus(
  id: number
): Promise<VirtualModelApiResponse<VirtualModelStatus>> {
  const response = await api.get(`/api/virtual-models/${id}/status`, {
    skipErrorHandler: true,
  })
  return response.data
}

// getVirtualModelCandidateStatus 读取单个候选节点的状态摘要；
// 候选被删除后 in-flight 请求会返回 404，因此同样跳过全局错误弹窗喵。
export async function getVirtualModelCandidateStatus(
  modelID: number,
  candidateID: number
): Promise<VirtualModelApiResponse<VirtualModelCandidateStatus>> {
  const response = await api.get(
    `/api/virtual-models/${modelID}/candidates/${candidateID}/status`,
    { skipErrorHandler: true }
  )
  return response.data
}

export async function freezeVirtualModelCandidate(
  modelID: number,
  candidateID: number,
  expiresAt: number,
  version: number
): Promise<VirtualModelApiResponse<{ candidate_id: number; expires_at: number; version: number }>> {
  const response = await api.post(
    `/api/virtual-models/${modelID}/candidates/${candidateID}/freeze`,
    { expires_at: expiresAt, version }
  )
  return response.data
}

export async function unfreezeVirtualModelCandidate(
  modelID: number,
  candidateID: number,
  version: number
): Promise<VirtualModelApiResponse<{ candidate_id: number; version: number }>> {
  const response = await api.delete(
    `/api/virtual-models/${modelID}/candidates/${candidateID}/freeze`,
    { data: { version } }
  )
  return response.data
}
