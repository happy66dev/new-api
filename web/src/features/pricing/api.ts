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
import { api } from '@/lib/api'

import type { PricingData } from './types'

// ----------------------------------------------------------------------------
// Pricing APIs
// ----------------------------------------------------------------------------

// Get model pricing data
export async function getPricing(): Promise<PricingData> {
  const res = await api.get('/api/pricing')
  return res.data
}

// 管理员停止某个用户共享模型：关闭其共享开关，属主自用保留喵。
// 入参取自模型广场条目的 share_owner_user_id 与对外调用名 model_name（形如 user/<name>）喵。
export async function stopSharingUserUpstreamModel(
  ownerUserId: number,
  modelName: string
): Promise<{ success: boolean; message?: string }> {
  const res = await api.post('/api/upstream-models/admin/stop-sharing', {
    owner_user_id: ownerUserId,
    model_name: modelName,
  })
  return res.data
}
