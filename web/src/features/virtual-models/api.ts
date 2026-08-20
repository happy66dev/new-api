import { api } from '@/lib/api'

export type VirtualModelCandidate = {
  id: number
  stable_order: number
  source_type: string
  enabled: boolean
  max_retries: number
  timeout_seconds: number
  group_name?: string
  real_model_name?: string
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

export async function getVirtualModels(): Promise<{ success: boolean; data?: VirtualModel[] }> {
  const response = await api.get('/api/virtual-models')
  return response.data
}
