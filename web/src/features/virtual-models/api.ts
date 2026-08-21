import { api } from '@/lib/api'

export type VirtualModelCandidate = {
  id: number
  stable_order: number
  source_type: 'internal' | 'custom' | string
  enabled: boolean
  max_retries: number
  timeout_seconds: number
  group_name?: string
  real_model_name?: string
  base_url?: string
  auth_style?: string
  api_key?: string
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

export type VirtualModelStatus = {
  model: string
  enabled: boolean
  candidate_count: number
  available_candidates: number
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

export async function deleteVirtualModel(id: number): Promise<VirtualModelApiResponse<{ id: number }>> {
  const response = await api.delete(`/api/virtual-models/${id}`)
  return response.data
}

export async function getVirtualModelStatus(
  id: number
): Promise<VirtualModelApiResponse<VirtualModelStatus>> {
  const response = await api.get(`/api/virtual-models/${id}/status`)
  return response.data
}
