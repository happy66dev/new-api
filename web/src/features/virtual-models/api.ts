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
  // hedge_threshold 连续失败自动避险阈值，达到该次数才冻结退避；零表示关闭自动避险喵。
  hedge_threshold?: number
  // hedge_freeze_seconds 达到连续失败阈值后的退避冻结秒数；阈值非零时必填且必须为正数喵。
  hedge_freeze_seconds?: number
  group_name?: string
  real_model_name?: string
  // 喵~防御：候选响应绝不声明或接收可回显的上游 API Key 喵。
  base_url?: string
  auth_style?: VirtualModelCandidateAuthStyle
  // 引用用户上游模型条目时非空，凭据与真实模型名以该条目为准喵。
  upstream_model_id?: number | null
  // frozen_until 当前手动冻结到期时间（Unix 秒），未冻结时缺省，供调用链页面展示已冻结徽章喵。
  frozen_until?: number
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
  // hedge_threshold 连续失败自动避险阈值，达到该次数才冻结退避；零表示关闭自动避险喵。
  hedge_threshold?: number
  // hedge_freeze_seconds 达到连续失败阈值后的退避冻结秒数；阈值非零时必填且必须为正数喵。
  hedge_freeze_seconds?: number
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
  // 流转伪流：开启后上游流式全量缓存到 [DONE] 再一次性伪流发出，断流按处理措施决策喵。
  fake_stream_enabled: boolean
  stream_cut_action: 'retry' | 'next' | 'freeze' | 'passthrough' | ''
  stream_cut_retries: number
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
  // 流转伪流配置：开启后上游流式全量缓存到 [DONE] 再一次性伪流发出喵。
  fake_stream_enabled: boolean
  // 目标模式断流处理措施已由全局兜底失败规则代替，保留可选字段仅兼容旧数据喵。
  stream_cut_action?: 'retry' | 'next' | 'freeze' | 'passthrough' | ''
  stream_cut_retries?: number
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
  avg_ttft_ms: number
  cache_hit_rate: number
  total_tokens: number
  request_count: number
  availability_24h: number[]
  // series 是逐小时桶明细，供 Overview 图表与性能抽屉展示喵。
  series: EntityProbeBucket[]
  last_at: number
  last_success: boolean
  last_latency_ms: number
  last_error: string
  // last_failure_at 最近一次失败调用时间戳，即使最近一次调用成功也保留喵。
  last_failure_at: number
  // last_failure_error 最近一次失败调用错误分类，即使最近一次调用成功也保留喵。
  last_failure_error: string
  // candidates 是启用候选快照的节点摘要，供 Overview 状态卡片展示喵。
  candidates: VirtualModelCandidateStatus[]
  // current_requests 当前处理中的客户端请求数，供概览实时统计喵。
  current_requests: number
  // active_requests 活跃请求详情列表，展示当前调用链喵。
  active_requests: VirtualModelActiveRequest[]
}

// VirtualModelActiveRequest 是单个活跃虚拟模型请求的调用链详情喵。
export type VirtualModelActiveRequest = {
  request_id: string
  model_id: number
  model_name: string
  // candidate_index 当前候选在链上的序号，从 1 起喵。
  candidate_index: number
  // candidate_label 当前候选展示名，内部候选为真实模型名喵。
  candidate_label: string
  // started_at 请求进入活跃状态的时间点（ISO 8601）喵。
  started_at: string
}

// VirtualModelCandidateStatus 是单个候选节点的状态摘要，含富系列明细喵。
export type VirtualModelCandidateStatus = {
  candidate_id: number
  label: string
  availability: number
  avg_latency_ms: number
  avg_ttft_ms: number
  cache_hit_rate: number
  total_tokens: number
  request_count: number
  // series 是候选节点的逐小时桶明细，供性能抽屉图表喵。
  series: EntityProbeBucket[]
  last_at: number
  last_success: boolean
  last_error: string
  // last_failure_at 最近一次失败调用时间戳，即使最近一次调用成功也保留喵。
  last_failure_at: number
  // last_failure_error 最近一次失败调用错误分类，即使最近一次调用成功也保留喵。
  last_failure_error: string
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
  // stall_timeout_seconds 静默多久判定流式卡流，单位：秒；零或省略表示默认 60 喵。
  stall_timeout_seconds?: number
  // min_content_chars 探测放流前需累积的内容字符门槛，零或省略表示默认 10 喵。
  min_content_chars?: number
  // probe_total_timeout_seconds 探测阶段总预算，单位：秒；零或省略表示默认 300 喵。
  probe_total_timeout_seconds?: number
  // timeout_seconds 超时条件判定阈值，单位：秒；零或省略表示沿用候选级执行超时喵。
  timeout_seconds?: number
  // retry_count 规则重试当前候选的最大重试次数，零或省略表示未配置时沿用候选 MaxRetries 喵。
  retry_count?: number
}

export type VirtualModelFailureRulesReplaceInput = {
  version: number
  rules: VirtualModelFailureRule[]
  // hedge_threshold 候选级连续失败自动避险阈值，随规则保存一并写入候选表；零表示关闭自动避险喵。
  hedge_threshold?: number
  // hedge_freeze_seconds 候选级达到连续失败阈值后的退避冻结秒数；阈值非零时必填且必须为正数喵。
  hedge_freeze_seconds?: number
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
// disableDuplicate 关闭 GET 在途去重，让概览「刷新」按钮与轮询都能真正命中后端拿最新值喵。
export async function getVirtualModelStatus(
  id: number
): Promise<VirtualModelApiResponse<VirtualModelStatus>> {
  const response = await api.get(`/api/virtual-models/${id}/status`, {
    skipErrorHandler: true,
    disableDuplicate: true,
  })
  return response.data
}

// getVirtualModelCandidateStatus 读取单个候选节点的状态摘要；
// 候选被删除后 in-flight 请求会返回 404，因此同样跳过全局错误弹窗喵。
// disableDuplicate 保证打开候选性能抽屉时总能拉到最新样本喵。
export async function getVirtualModelCandidateStatus(
  modelID: number,
  candidateID: number
): Promise<VirtualModelApiResponse<VirtualModelCandidateStatus>> {
  const response = await api.get(
    `/api/virtual-models/${modelID}/candidates/${candidateID}/status`,
    { skipErrorHandler: true, disableDuplicate: true }
  )
  return response.data
}

export async function freezeVirtualModelCandidate(
  modelID: number,
  candidateID: number,
  freezeSeconds: number,
  version: number
): Promise<VirtualModelApiResponse<{ candidate_id: number; expires_at: number; version: number }>> {
  const response = await api.post(
    `/api/virtual-models/${modelID}/candidates/${candidateID}/freeze`,
    // 以自定义秒数冻结，由后端换算到期时间戳喵。
    { freeze_seconds: freezeSeconds, version }
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

// VirtualModelShareCodeCreateInput 是生成分享码的请求体；两项都留空表示永不过期且不限次数喵。
export type VirtualModelShareCodeCreateInput = {
  // expires_at 是可选到期时间（Unix 秒），零或省略表示永不过期喵。
  expires_at?: number
  // max_imports 是可选最大导入次数，零或省略表示不限喵。
  max_imports?: number
}

// VirtualModelShareCodeCreated 是生成分享码的结果，含分享码本体与快照统计喵。
export type VirtualModelShareCodeCreated = {
  id: number
  code: string
  display_name: string
  candidate_count: number
  internal_candidate_count: number
  custom_candidate_count: number
  expires_at: number
  max_imports: number
  created_time: number
}

// VirtualModelShareCodeSummary 是分享码列表项，供分享者自查与删除喵。
export type VirtualModelShareCodeSummary = {
  id: number
  code: string
  display_name: string
  import_count: number
  max_imports: number
  expires_at: number
  created_time: number
}

// VirtualModelShareSkippedCandidate 描述导入时被跳过的候选及其稳定原因码喵。
export type VirtualModelShareSkippedCandidate = {
  order: number
  source: string
  group?: string
  model?: string
  // reason 是后端给出的稳定原因码，前端据此选择已翻译的说明文案喵。
  reason: string
  // message 是后端中文兜底说明，仅在原因码未知时使用喵。
  message: string
}

// VirtualModelShareImportPreview 是导入预检结果，不写入任何数据喵。
export type VirtualModelShareImportPreview = {
  share_code_id: number
  display_name: string
  plan: {
    loop_enabled: boolean
    total_timeout_seconds: number
    max_loop_rounds: number
    fake_stream_enabled: boolean
    stream_cut_action: string
    stream_cut_retries: number
  }
  // candidate_count 是快照里的候选总数，importable_candidate_count 是本次能导入的数量喵。
  candidate_count: number
  importable_candidate_count: number
  skipped_candidates: VirtualModelShareSkippedCandidate[]
  // warnings 是提示码，不阻断导入；前端按码翻译后展示喵。
  warnings: string[]
}

// VirtualModelShareImportResult 是真正导入后的结果喵。
export type VirtualModelShareImportResult = {
  id: number
  normalized_name: string
  display_name: string
  imported_candidate_count: number
  skipped_candidates: VirtualModelShareSkippedCandidate[]
  warnings: string[]
  // enabled 恒为 false：导入的模型默认停用，核对凭据后需用户自行启用喵。
  enabled: boolean
}

// createVirtualModelShareCode 为一个虚拟模型生成脱敏方案的分享码喵。
export async function createVirtualModelShareCode(
  modelID: number,
  input: VirtualModelShareCodeCreateInput
): Promise<VirtualModelApiResponse<VirtualModelShareCodeCreated>> {
  const response = await api.post(`/api/virtual-models/share-codes`, {
    virtual_model_id: modelID,
    ...input,
  })
  return response.data
}

// getVirtualModelShareCodes 读取当前用户生成过的分享码，供撤销与自查喵。
export async function getVirtualModelShareCodes(): Promise<
  VirtualModelApiResponse<{
    share_codes: VirtualModelShareCodeSummary[]
    total: number
  }>
> {
  const response = await api.get('/api/virtual-models/share-codes')
  return response.data
}

// deleteVirtualModelShareCode 删除自己的一枚分享码；删除后这枚码立即不可再导入喵。
export async function deleteVirtualModelShareCode(
  shareCodeID: number
): Promise<VirtualModelApiResponse<{ id: number }>> {
  const response = await api.delete(
    `/api/virtual-models/share-codes/${shareCodeID}`
  )
  return response.data
}

// precheckVirtualModelShareImport 解析分享码并按权限预演导入，不落库喵。
// 分享码无效属于用户可预期的输入错误，因此跳过全局错误弹窗，由弹窗内联展示喵。
export async function precheckVirtualModelShareImport(
  code: string
): Promise<VirtualModelApiResponse<VirtualModelShareImportPreview>> {
  const response = await api.post(
    '/api/virtual-models/import/precheck',
    { code },
    { skipErrorHandler: true }
  )
  return response.data
}

// importVirtualModelShareCode 把分享码里的方案复制一份到当前用户名下喵。
// 这里同样跳过全局错误弹窗，导入失败原因直接显示在导入弹窗里更方便重试喵。
// normalized_name 与 display_name 都由导入方自己决定且必填，撞名时后端返回 409 冲突喵。
export async function importVirtualModelShareCode(input: {
  code: string
  normalized_name: string
  display_name: string
}): Promise<VirtualModelApiResponse<VirtualModelShareImportResult>> {
  const response = await api.post('/api/virtual-models/import', input, {
    skipErrorHandler: true,
  })
  return response.data
}

// VirtualModelAnomaly 是候选被动变化异常项，供红点提醒与弹层展示喵。
export type VirtualModelAnomaly = {
  // virtual_model_id 与 virtual_model_name 定位所属虚拟模型喵。
  virtual_model_id: number
  virtual_model_name: string
  // candidate_id 定位候选，供逐候选展示红点喵。
  candidate_id: number
  source_type: string
  // group_name 与 real_model_name 是候选路由目标，供定位具体异常项喵。
  group_name?: string
  real_model_name?: string
  // reason_code 是稳定机器可读原因码，前端据此查 i18n 文案喵。
  reason_code: string
  // reason_message 是后端中文兜底说明，仅在原因码未知时使用喵。
  reason_message: string
}

// getVirtualModelAnomalies 读取当前用户虚拟模型候选的被动异常清单喵。
// 扫描是只读操作，失败时由页面内联处理而非全局 toast，避免轮询失败反复弹窗喵。
export async function getVirtualModelAnomalies(): Promise<
  VirtualModelApiResponse<{ anomalies: VirtualModelAnomaly[] }>
> {
  const response = await api.get('/api/virtual-models/anomalies', {
    skipErrorHandler: true,
  })
  return response.data
}
