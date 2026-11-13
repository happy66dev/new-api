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
import { z } from 'zod'

import {
  type PermissionCatalog,
  type AdminPermissionMatrix,
  normalizeAdminPermissions,
} from '@/lib/admin-permissions'
import { quotaUnitsToDollars } from '@/lib/format'
import { ROLE } from '@/lib/roles'

import { DEFAULT_GROUP } from '../constants'
import { type UserFormData, type User } from '../types'

// ============================================================================
// Form Schema
// ============================================================================

export const userFormSchema = z.object({
  username: z.string().min(1, 'Username is required'),
  display_name: z.string().optional(),
  password: z.string().optional(),
  role: z.number().optional(),
  quota_dollars: z.number().min(0).optional(),
  group: z.string().optional(),
  // 分组权限模式：'' = 跟随全局设置，'legacy' = 原 newapi 旧口径，'custom' = 仅新分组管理喵。
  group_access_mode: z.string().optional(),
  // 可用分组白名单：custom 模式下决定用户能访问哪些分组喵。
  usable_groups: z.array(z.string()).optional(),
  remark: z.string().optional(),
  admin_permissions: z
    .record(z.string(), z.record(z.string(), z.boolean()))
    .optional(),
})

export type UserFormValues = z.infer<typeof userFormSchema>

// ============================================================================
// Form Defaults
// ============================================================================

export const USER_FORM_DEFAULT_VALUES: UserFormValues = {
  username: '',
  display_name: '',
  password: '',
  role: 1, // Default to common user
  quota_dollars: 0,
  group: DEFAULT_GROUP,
  // 默认跟随全局分组管理模式，管理员需要时才显式切换喵。
  group_access_mode: '',
  usable_groups: [],
  remark: '',
  // Filled against the backend catalog at render time; see UsersMutateDrawer.
  admin_permissions: {},
}

// ============================================================================
// Form Data Transformation
// ============================================================================

/**
 * Transform form data to API payload
 */
export function transformFormDataToPayload(
  data: UserFormValues,
  userId?: number,
  catalog?: PermissionCatalog
): UserFormData & { id?: number } {
  const payload: UserFormData & { id?: number } = {
    username: data.username,
    display_name: data.display_name || data.username,
    password: data.password || undefined,
  }

  const role = userId === undefined ? data.role || 1 : (data.role ?? 0)

  // Only send the permission matrix when the target is an admin and the catalog
  // is available; without the catalog we cannot build a full matrix, so we omit
  // the field (the backend then leaves existing permissions untouched).
  if (role >= ROLE.ADMIN && catalog) {
    payload.admin_permissions = normalizeAdminPermissions(
      data.admin_permissions as AdminPermissionMatrix | undefined,
      catalog
    )
  }

  // For create: only send required fields
  if (userId === undefined) {
    payload.role = role
  } else {
    // For update: quota is adjusted atomically via /api/user/manage, not sent here
    payload.group = data.group
    payload.group_access_mode = data.group_access_mode ?? ''
    // 后端 model.User.UsableGroups 是字符串列，且内部按 JSON 数组解析；
    // 直接发数组会触发 "cannot unmarshal array into Go struct field" 导致整单更新失败，
    // 因此这里必须序列化成 JSON 数组字符串再提交喵。
    payload.usable_groups = JSON.stringify(data.usable_groups ?? [])
    payload.remark = data.remark || undefined
    payload.id = userId
  }

  return payload
}

/**
 * Transform user data to form defaults. The admin permission matrix is passed
 * through as-is (the backend already returns a full matrix); it is filled against
 * the catalog at render time in UsersMutateDrawer.
 */
export function transformUserToFormDefaults(user: User): UserFormValues {
  return {
    username: user.username,
    display_name: user.display_name,
    password: '',
    role: user.role,
    quota_dollars: quotaUnitsToDollars(user.quota),
    group: user.group || DEFAULT_GROUP,
    group_access_mode: user.group_access_mode ?? '',
    // 后端存的是 JSON 数组字符串，编辑表单里转成字符串数组方便多选组件直接使用喵。
    usable_groups: parseUsableGroups(user.usable_groups),
    remark: user.remark || '',
    admin_permissions: user.admin_permissions ?? {},
  }
}

/**
 * Parse the backend `usable_groups` JSON string into a string array.
 * Malformed or legacy payloads fall back to an empty list, which the backend
 * interprets as "only the built-in pseudo groups" in custom mode.
 */
export function parseUsableGroups(raw: string | undefined): string[] {
  // 喵~防御：空值与空串直接返回空数组，避免 JSON.parse 抛异常喵。
  if (!raw) return []
  try {
    const parsed: unknown = JSON.parse(raw)
    // 喵~防御：非数组或元素不是字符串时按空列表处理，保守方向只是让用户少几个分组喵。
    if (!Array.isArray(parsed)) return []
    return parsed.filter((item): item is string => typeof item === 'string')
  } catch {
    return []
  }
}
