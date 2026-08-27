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
import { useQuery } from '@tanstack/react-query'
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
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { USER_UPSTREAM_GROUP_VALUE, VIRTUAL_GROUP_VALUE } from '@/components/model-group-selector'
import { getVirtualModels } from '@/features/virtual-models/api'
import { getUserUpstreamModels } from '@/features/upstream-models/api'
import { getUserGroups, getUserModels } from '../api'
import {
  buildUserUpstreamModelOptions,
  buildVirtualModelOptions,
  getGroupFallback,
  getModelFallback,
  getOptionLoadErrorMessage,
  shouldClearModelForGroup,
} from '../lib'
import type { GroupOption, ModelOption, PlaygroundConfig } from '../types'

type UsePlaygroundOptionsParams = {
  currentGroup: string
  currentModel: string
  setGroups: (groups: GroupOption[]) => void
  setModels: (models: ModelOption[]) => void
  updateConfig: <K extends keyof PlaygroundConfig>(
    key: K,
    value: PlaygroundConfig[K]
  ) => void
}

export function usePlaygroundOptions({
  currentGroup,
  currentModel,
  setGroups,
  setModels,
  updateConfig,
}: UsePlaygroundOptionsParams) {
  const { t } = useTranslation()

  // 虚拟分组与自定上游分组都是前端追加的分类，选中后模型来自本端各自的模型列表喵。
  const isVirtualGroup = currentGroup === VIRTUAL_GROUP_VALUE
  const isUserUpstreamGroup = currentGroup === USER_UPSTREAM_GROUP_VALUE
  // 追加分组不需要真实分组模型接口，统一判断避免重复条件喵。
  const isAppendedGroup = isVirtualGroup || isUserUpstreamGroup

  const {
    data: modelsData,
    error: modelsError,
    isError: isModelsError,
    isLoading: isLoadingModels,
  } = useQuery({
    queryKey: ['playground-models', currentGroup],
    queryFn: () => getUserModels(currentGroup),
    // 喵~防御：追加分组下不请求真实分组模型接口（后端无此分组），避免 404 喵。
    enabled: currentGroup !== '' && !isAppendedGroup,
  })

  // 拉取当前登录用户自己的虚拟模型，虚拟模型不随分组变化，独立缓存喵。
  const { data: virtualModelsData } = useQuery({
    queryKey: ['playground-virtual-models'],
    queryFn: getVirtualModels,
  })

  // 拉取当前登录用户自己的自定上游模型，供「自定上游」分组展示喵。
  const { data: userUpstreamModelsData } = useQuery({
    queryKey: ['playground-user-upstream-models'],
    queryFn: getUserUpstreamModels,
  })

  const {
    data: groupsData,
    error: groupsError,
    isError: isGroupsError,
  } = useQuery({
    queryKey: ['playground-groups'],
    queryFn: getUserGroups,
  })

  useEffect(() => {
    if (!isModelsError) return

    toast.error(
      getOptionLoadErrorMessage(
        modelsError,
        t('Failed to load playground models')
      )
    )
  }, [isModelsError, modelsError, t])

  useEffect(() => {
    if (!isGroupsError) return

    toast.error(
      getOptionLoadErrorMessage(
        groupsError,
        t('Failed to load playground groups')
      )
    )
  }, [isGroupsError, groupsError, t])

  useEffect(() => {
    // 追加分组下不展示普通模型，模型来自本端虚拟模型或自定上游列表喵。
    if (isAppendedGroup) return
    if (!modelsData) return

    setModels(modelsData)
    const fallback = getModelFallback(modelsData, currentModel)

    if (fallback) {
      updateConfig('model', fallback)
      return
    }

    if (shouldClearModelForGroup(modelsData, currentModel)) {
      updateConfig('model', '')
    }
  }, [modelsData, isAppendedGroup, currentModel, setModels, updateConfig])

  useEffect(() => {
    // 选中「虚拟」分组时，模型下拉只显示启用状态的虚拟模型，不再追加到普通模型末尾喵。
    if (!isVirtualGroup) return

    const virtualOptions = buildVirtualModelOptions(virtualModelsData?.data)
    setModels(virtualOptions)
    // 默认分组为 virtual 时，模型下拉自动选中第一个启用虚拟模型喵。
    const fallback = getModelFallback(virtualOptions, currentModel)

    if (fallback) {
      updateConfig('model', fallback)
    }
  }, [isVirtualGroup, virtualModelsData, currentModel, setModels, updateConfig])

  useEffect(() => {
    // 选中「自定上游」分组时，模型下拉只显示启用状态的自定上游模型（user/xxx）喵。
    if (!isUserUpstreamGroup) return

    const userUpstreamOptions = buildUserUpstreamModelOptions(userUpstreamModelsData?.data)
    setModels(userUpstreamOptions)
    // 默认分组为自定上游时，模型下拉自动选中第一个启用自定上游模型喵。
    const fallback = getModelFallback(userUpstreamOptions, currentModel)

    if (fallback) {
      updateConfig('model', fallback)
    }
  }, [isUserUpstreamGroup, userUpstreamModelsData, currentModel, setModels, updateConfig])

  useEffect(() => {
    if (!groupsData) return

    setGroups(groupsData)
    // 追加分组是前端追加的分类，不在后端分组数据里，跳过回退避免被重置喵。
    if (isAppendedGroup) return
    const fallback = getGroupFallback(groupsData, currentGroup)

    if (fallback) {
      updateConfig('group', fallback)
    }
  }, [groupsData, isAppendedGroup, currentGroup, setGroups, updateConfig])

  return {
    isLoadingModels,
  }
}
