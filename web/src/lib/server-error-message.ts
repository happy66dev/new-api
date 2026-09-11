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
const serverErrorMessageKeys = {
  AUTH_SESSION_LIMIT:
    'Too many active login sessions. On a device where you are already signed in, open Login sessions and use “Sign out other sessions” to revoke them. If you cannot access a signed-in device, reset your password to sign out all sessions.',
  AUTH_SESSION_ISSUANCE_LIMIT:
    'Too many login sessions were created recently. Please wait for the rolling window to pass, then try again.',
  TELEGRAM_BIND_DISABLED: 'Telegram binding is disabled.',
  TELEGRAM_BIND_INVALID_REQUEST:
    'The Telegram authorization request is invalid or expired.',
  TELEGRAM_BIND_FLOW_INVALID:
    'This Telegram binding request has expired or has already been used.',
  TELEGRAM_BIND_SESSION_INVALID:
    'The login session that started this Telegram binding is no longer valid.',
  TELEGRAM_BIND_ALREADY_BOUND: 'This Telegram account is already bound.',
  TELEGRAM_BIND_USER_DELETED: 'This user account no longer exists.',
  TELEGRAM_BIND_USER_DISABLED: 'This user account is disabled.',
  TELEGRAM_BIND_INTERNAL_ERROR: 'Telegram binding failed. Please try again.',
  // 虚拟模型分享码相关错误：后端返回的是小写 code，这里按原样登记才能精确命中喵。
  virtual_model_share_no_candidate: 'This plan has no shareable candidate.',
  virtual_model_share_code_not_found:
    'This share code does not exist or has been deleted.',
  virtual_model_share_code_expired: 'This share code has expired.',
  virtual_model_share_code_exhausted:
    'This share code has reached its import limit.',
  virtual_model_share_payload_invalid: 'This share code content is not valid.',
  virtual_model_share_nothing_importable:
    'No candidate in this share code is available to your account.',
  virtual_model_name_conflict:
    'That model name is already taken. Please choose another one.',
} as const

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object'
}

function serverErrorPayload(value: unknown): Record<string, unknown> | null {
  if (!isRecord(value)) return null

  const response = value.response
  if (isRecord(response) && isRecord(response.data)) {
    return response.data
  }
  return value
}

export function getServerErrorMessageKey(value: unknown): string | null {
  const payload = serverErrorPayload(value)
  if (!payload || typeof payload.code !== 'string') return null

  return (
    serverErrorMessageKeys[
      payload.code as keyof typeof serverErrorMessageKeys
    ] ?? null
  )
}
