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
import type { VirtualModel } from '@/features/virtual-models/api'

import type { GroupOption, ModelOption } from '../../types'

export function filterChatGroups(groups: GroupOption[]): GroupOption[] {
  return groups.filter((group) => group.value !== 'auto')
}

export function getModelFallback(
  models: ModelOption[],
  currentModel: string
): string | null {
  const hasCurrentModel = models.some((model) => model.value === currentModel)

  if (hasCurrentModel || models.length === 0) {
    return null
  }

  return models[0].value
}

export function shouldClearModelForGroup(
  models: ModelOption[],
  currentModel: string
): boolean {
  if (currentModel === '') {
    return false
  }

  return !models.some((model) => model.value === currentModel)
}

export function getGroupFallback(
  groups: GroupOption[],
  currentGroup: string
): string | null {
  const hasCurrentGroup = groups.some((group) => group.value === currentGroup)

  if (hasCurrentGroup || groups.length === 0) {
    return null
  }

  return (
    groups.find((group) => group.value === 'default')?.value ?? groups[0].value
  )
}

export function getOptionLoadErrorMessage(
  error: unknown,
  fallbackMessage: string
): string {
  return error instanceof Error ? error.message : fallbackMessage
}

// buildVirtualModelOptions 把启用状态的虚拟模型转成「虚拟」分组下的下拉选项喵。
export function buildVirtualModelOptions(
  // 只依赖虚拟模型的三个展示字段，避免与完整控制面类型强耦合喵。
  virtualModels: Pick<
    VirtualModel,
    'normalized_name' | 'display_name' | 'enabled'
  >[] | undefined
): ModelOption[] {
  // 喵~防御：虚拟模型接口未返回数据时回退为空数组，避免展开 undefined 报错喵。
  return (virtualModels ?? [])
    // 只把启用状态的虚拟模型暴露给游乐场，停用的模型不可选喵。
    .filter((virtualModel) => virtualModel.enabled)
    .map((virtualModel) => {
      // display_name 为空时回退为 virtual/规范名，保证下拉选项始终可辨识喵。
      const label =
        virtualModel.display_name.trim() !== ''
          ? virtualModel.display_name
          : `virtual/${virtualModel.normalized_name}`
      return {
        label,
        // value 使用 virtual/ 前缀，与后端虚拟模型分发识别规则保持一致喵。
        value: `virtual/${virtualModel.normalized_name}`,
      }
    })
    // 虚拟模型之间按显示名自然排序，让下拉顺序稳定可预测喵。
    .sort((a, b) =>
      a.label.localeCompare(b.label, undefined, {
        numeric: true,
        sensitivity: 'base',
      })
    )
}
