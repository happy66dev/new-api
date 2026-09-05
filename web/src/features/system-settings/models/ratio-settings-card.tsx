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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import { resetModelRatios } from '../api'
import { SettingsPageTitleStatusPortal } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import { safeJsonParse } from '../utils/json-parser'
import { positiveIntegerSchema } from '../utils/numeric-field'
import { GroupModelPricingForm } from './group-model-pricing-form'
import type { GroupModelPricingFormValues } from './group-model-pricing-utils'
import { GroupRatioForm } from './group-ratio-form'
import { ModelRatioForm } from './model-ratio-form'
import { ToolPriceSettings } from './tool-price-settings'
import { UpstreamRatioSync } from './upstream-ratio-sync'
import {
  formatJsonForTextarea,
  type JsonValidationError,
  normalizeJsonString,
  validateJsonString,
} from './utils'

type Translate = (key: string, options?: Record<string, unknown>) => string

function filterDefaultModelsByGroups(
  defaultModels: string,
  groupValues: string[]
): string {
  const modelMap = safeJsonParse<Record<string, string>>(defaultModels, {
    fallback: {},
    silent: true,
  })
  const validGroups = new Set(groupValues)
  const filtered = Object.fromEntries(
    Object.entries(modelMap).filter(([group]) => validGroups.has(group))
  )
  return JSON.stringify(filtered, null, 2)
}

function formatJsonValidationError(
  t: Translate,
  error?: JsonValidationError,
  fallback = 'Invalid JSON'
) {
  if (!error) return t(fallback)

  if (error.type === 'required') return t('Value is required')
  if (error.type === 'structure') {
    return t(
      fallback === 'Invalid JSON' ? 'JSON structure is invalid' : fallback
    )
  }

  let locationMessage: string
  if (error.line && error.column) {
    locationMessage = t(
      'JSON is invalid at line {{line}}, column {{column}}.',
      {
        line: error.line,
        column: error.column,
      }
    )
  } else if (error.position !== undefined) {
    locationMessage = t('JSON is invalid at position {{position}}.', {
      position: error.position,
    })
  } else {
    locationMessage = t('JSON is invalid. Please check the syntax.')
  }

  const parts = [locationMessage]

  if (error.missingCommaLine) {
    parts.push(
      t('Check line {{line}} for a missing comma.', {
        line: error.missingCommaLine,
      })
    )
  }

  return parts.join(' ')
}

function createJsonStringField(
  t: Translate,
  options?: Parameters<typeof validateJsonString>[1]
) {
  return z.string().superRefine((value, ctx) => {
    const result = validateJsonString(value, options)
    if (!result.valid) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: formatJsonValidationError(t, result.error, result.message),
      })
    }
  })
}

const createModelSchema = (t: Translate) =>
  z.object({
    ModelPrice: createJsonStringField(t),
    ModelRatio: createJsonStringField(t),
    CacheRatio: createJsonStringField(t),
    CreateCacheRatio: createJsonStringField(t),
    CompletionRatio: createJsonStringField(t),
    ImageRatio: createJsonStringField(t),
    AudioRatio: createJsonStringField(t),
    AudioCompletionRatio: createJsonStringField(t),
    ExposeRatioEnabled: z.boolean(),
    BillingMode: createJsonStringField(t),
    BillingExpr: createJsonStringField(t),
  })

const createGroupSchema = (t: Translate) =>
  z.object({
    GroupRatio: createJsonStringField(t),
    TopupGroupRatio: createJsonStringField(t),
    UserUsableGroups: createJsonStringField(t),
    GroupDescriptions: createJsonStringField(t),
    GroupGroupRatio: createJsonStringField(t),
    AutoGroups: createJsonStringField(t, {
      predicate: (parsed) =>
        Array.isArray(parsed) &&
        parsed.every((item) => typeof item === 'string'),
      predicateMessage: 'Expected a JSON array of group identifiers',
    }),
    AutoGroupDescription: z.string(),
    MaxTokenAutoGroups: positiveIntegerSchema(t('Enter a positive integer')),
    DefaultUseAutoGroup: z.boolean(),
    GroupSpecialUsableGroup: createJsonStringField(t),
    GroupDefaultModel: createJsonStringField(t),
    GroupRetryTimes: createJsonStringField(t, {
      predicate: (parsed) =>
        typeof parsed === 'object' &&
        parsed !== null &&
        !Array.isArray(parsed) &&
        Object.entries(parsed).every(
          ([group, value]) =>
            group.trim() !== '' &&
            typeof value === 'number' &&
            Number.isInteger(value) &&
            value >= 0 &&
            value <= 10
        ),
      predicateMessage: 'Expected retry counts from 0 to 10 by group',
    }),
    ModelSquareVisibleGroups: createJsonStringField(t, {
      predicate: (parsed) =>
        Array.isArray(parsed) && parsed.every((item) => typeof item === 'string'),
      predicateMessage: 'Expected a JSON array of group identifiers',
    }),
  })

// 分组定制定价这一页的三份配置都是「分组 -> 模型 -> ...」两层 JSON 对象，
// 只校验 JSON 合法性，具体数值的合法性交给后端 CheckGroupModelPricing 把关喵。
const createGroupModelPricingSchema = (t: Translate) =>
  z.object({
    GroupModelPricing: createJsonStringField(t),
    GroupBillingMode: createJsonStringField(t),
    GroupBillingExpr: createJsonStringField(t),
  })

type ModelFormValues = z.infer<ReturnType<typeof createModelSchema>>
type GroupFormValues = z.infer<ReturnType<typeof createGroupSchema>>
type RatioTabId =
  | 'models'
  | 'unset-models'
  | 'groups'
  | 'group-model-pricing'
  | 'tool-prices'
  | 'upstream-sync'

type RatioSettingsCardProps = {
  modelDefaults: ModelFormValues
  groupDefaults: GroupFormValues
  /** 分组定制定价三份配置的初始值，来自系统设置接口喵。 */
  groupModelPricingDefaults: GroupModelPricingFormValues
  toolPricesDefault: string
  titleKey?: string
  visibleTabs?: RatioTabId[]
}

export function RatioSettingsCard({
  modelDefaults,
  groupDefaults,
  groupModelPricingDefaults,
  toolPricesDefault,
  titleKey = 'Pricing Ratios',
  visibleTabs = ['models', 'groups', 'tool-prices', 'upstream-sync'],
}: RatioSettingsCardProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const queryClient = useQueryClient()
  const [confirmOpen, setConfirmOpen] = useState(false)

  const resetMutation = useMutation({
    mutationFn: resetModelRatios,
    onSuccess: (data) => {
      if (data.success) {
        toast.success(t('Model prices reset successfully'))
        queryClient.invalidateQueries({ queryKey: ['system-options'] })
        setConfirmOpen(false)
      } else {
        toast.error(data.message || t('Failed to reset model ratios'))
      }
    },
    onError: (error: Error) => {
      toast.error(error.message || t('Failed to reset model ratios'))
    },
  })

  const modelNormalizedDefaults = useRef({
    ModelPrice: normalizeJsonString(modelDefaults.ModelPrice),
    ModelRatio: normalizeJsonString(modelDefaults.ModelRatio),
    CacheRatio: normalizeJsonString(modelDefaults.CacheRatio),
    CreateCacheRatio: normalizeJsonString(modelDefaults.CreateCacheRatio),
    CompletionRatio: normalizeJsonString(modelDefaults.CompletionRatio),
    ImageRatio: normalizeJsonString(modelDefaults.ImageRatio),
    AudioRatio: normalizeJsonString(modelDefaults.AudioRatio),
    AudioCompletionRatio: normalizeJsonString(
      modelDefaults.AudioCompletionRatio
    ),
    ExposeRatioEnabled: modelDefaults.ExposeRatioEnabled,
    BillingMode: normalizeJsonString(modelDefaults.BillingMode),
    BillingExpr: normalizeJsonString(modelDefaults.BillingExpr),
  })
  const [savedModelValues, setSavedModelValues] = useState(
    modelNormalizedDefaults.current
  )

  const groupNormalizedDefaults = useRef({
    GroupRatio: normalizeJsonString(groupDefaults.GroupRatio),
    TopupGroupRatio: normalizeJsonString(groupDefaults.TopupGroupRatio),
    UserUsableGroups: normalizeJsonString(groupDefaults.UserUsableGroups),
    GroupDescriptions: normalizeJsonString(groupDefaults.GroupDescriptions),
    GroupGroupRatio: normalizeJsonString(groupDefaults.GroupGroupRatio),
    AutoGroups: normalizeJsonString(groupDefaults.AutoGroups),
    AutoGroupDescription: groupDefaults.AutoGroupDescription,
    MaxTokenAutoGroups: groupDefaults.MaxTokenAutoGroups,
    DefaultUseAutoGroup: groupDefaults.DefaultUseAutoGroup,
    GroupSpecialUsableGroup: normalizeJsonString(
      groupDefaults.GroupSpecialUsableGroup
    ),
    GroupDefaultModel: normalizeJsonString(groupDefaults.GroupDefaultModel),
    GroupRetryTimes: normalizeJsonString(groupDefaults.GroupRetryTimes),
    ModelSquareVisibleGroups: normalizeJsonString(
      groupDefaults.ModelSquareVisibleGroups
    ),
  })
  const modelSchema = useMemo(() => createModelSchema(t), [t])
  const groupSchema = useMemo(() => createGroupSchema(t), [t])
  const groupModelPricingSchema = useMemo(
    () => createGroupModelPricingSchema(t),
    [t]
  )

  // 分组定制定价的「已保存值」快照：保存时只把真正变过的那份配置发给后端喵。
  const groupModelPricingNormalizedDefaults = useRef({
    GroupModelPricing: normalizeJsonString(
      groupModelPricingDefaults.GroupModelPricing
    ),
    GroupBillingMode: normalizeJsonString(
      groupModelPricingDefaults.GroupBillingMode
    ),
    GroupBillingExpr: normalizeJsonString(
      groupModelPricingDefaults.GroupBillingExpr
    ),
  })

  const modelForm = useForm<ModelFormValues>({
    resolver: zodResolver(modelSchema),
    mode: 'onChange',
    defaultValues: {
      ...modelDefaults,
      ModelPrice: formatJsonForTextarea(modelDefaults.ModelPrice),
      ModelRatio: formatJsonForTextarea(modelDefaults.ModelRatio),
      CacheRatio: formatJsonForTextarea(modelDefaults.CacheRatio),
      CreateCacheRatio: formatJsonForTextarea(modelDefaults.CreateCacheRatio),
      CompletionRatio: formatJsonForTextarea(modelDefaults.CompletionRatio),
      ImageRatio: formatJsonForTextarea(modelDefaults.ImageRatio),
      AudioRatio: formatJsonForTextarea(modelDefaults.AudioRatio),
      AudioCompletionRatio: formatJsonForTextarea(
        modelDefaults.AudioCompletionRatio
      ),
      BillingMode: formatJsonForTextarea(modelDefaults.BillingMode),
      BillingExpr: formatJsonForTextarea(modelDefaults.BillingExpr),
    },
  })

  const groupForm = useForm<GroupFormValues>({
    resolver: zodResolver(groupSchema),
    mode: 'onChange',
    defaultValues: {
      ...groupDefaults,
      GroupRatio: formatJsonForTextarea(groupDefaults.GroupRatio),
      TopupGroupRatio: formatJsonForTextarea(groupDefaults.TopupGroupRatio),
      UserUsableGroups: formatJsonForTextarea(groupDefaults.UserUsableGroups),
      GroupDescriptions: formatJsonForTextarea(groupDefaults.GroupDescriptions),
      GroupGroupRatio: formatJsonForTextarea(groupDefaults.GroupGroupRatio),
      AutoGroups: formatJsonForTextarea(groupDefaults.AutoGroups),
      AutoGroupDescription: groupDefaults.AutoGroupDescription,
      MaxTokenAutoGroups: groupDefaults.MaxTokenAutoGroups,
      GroupSpecialUsableGroup: formatJsonForTextarea(
        groupDefaults.GroupSpecialUsableGroup
      ),
      GroupDefaultModel: formatJsonForTextarea(groupDefaults.GroupDefaultModel),
      GroupRetryTimes: formatJsonForTextarea(groupDefaults.GroupRetryTimes),
      ModelSquareVisibleGroups: formatJsonForTextarea(
        groupDefaults.ModelSquareVisibleGroups
      ),
    },
  })

  const groupModelPricingForm = useForm<GroupModelPricingFormValues>({
    resolver: zodResolver(groupModelPricingSchema),
    mode: 'onChange',
    defaultValues: {
      GroupModelPricing: formatJsonForTextarea(
        groupModelPricingDefaults.GroupModelPricing
      ),
      GroupBillingMode: formatJsonForTextarea(
        groupModelPricingDefaults.GroupBillingMode
      ),
      GroupBillingExpr: formatJsonForTextarea(
        groupModelPricingDefaults.GroupBillingExpr
      ),
    },
  })

  useEffect(() => {
    modelNormalizedDefaults.current = {
      ModelPrice: normalizeJsonString(modelDefaults.ModelPrice),
      ModelRatio: normalizeJsonString(modelDefaults.ModelRatio),
      CacheRatio: normalizeJsonString(modelDefaults.CacheRatio),
      CreateCacheRatio: normalizeJsonString(modelDefaults.CreateCacheRatio),
      CompletionRatio: normalizeJsonString(modelDefaults.CompletionRatio),
      ImageRatio: normalizeJsonString(modelDefaults.ImageRatio),
      AudioRatio: normalizeJsonString(modelDefaults.AudioRatio),
      AudioCompletionRatio: normalizeJsonString(
        modelDefaults.AudioCompletionRatio
      ),
      ExposeRatioEnabled: modelDefaults.ExposeRatioEnabled,
      BillingMode: normalizeJsonString(modelDefaults.BillingMode),
      BillingExpr: normalizeJsonString(modelDefaults.BillingExpr),
    }
    setSavedModelValues(modelNormalizedDefaults.current)

    modelForm.reset({
      ...modelDefaults,
      ModelPrice: formatJsonForTextarea(modelDefaults.ModelPrice),
      ModelRatio: formatJsonForTextarea(modelDefaults.ModelRatio),
      CacheRatio: formatJsonForTextarea(modelDefaults.CacheRatio),
      CreateCacheRatio: formatJsonForTextarea(modelDefaults.CreateCacheRatio),
      CompletionRatio: formatJsonForTextarea(modelDefaults.CompletionRatio),
      ImageRatio: formatJsonForTextarea(modelDefaults.ImageRatio),
      AudioRatio: formatJsonForTextarea(modelDefaults.AudioRatio),
      AudioCompletionRatio: formatJsonForTextarea(
        modelDefaults.AudioCompletionRatio
      ),
      BillingMode: formatJsonForTextarea(modelDefaults.BillingMode),
      BillingExpr: formatJsonForTextarea(modelDefaults.BillingExpr),
    })
  }, [modelDefaults, modelForm])

  useEffect(() => {
    groupNormalizedDefaults.current = {
      GroupRatio: normalizeJsonString(groupDefaults.GroupRatio),
      TopupGroupRatio: normalizeJsonString(groupDefaults.TopupGroupRatio),
      UserUsableGroups: normalizeJsonString(groupDefaults.UserUsableGroups),
      GroupDescriptions: normalizeJsonString(groupDefaults.GroupDescriptions),
      GroupGroupRatio: normalizeJsonString(groupDefaults.GroupGroupRatio),
      AutoGroups: normalizeJsonString(groupDefaults.AutoGroups),
      AutoGroupDescription: groupDefaults.AutoGroupDescription,
      MaxTokenAutoGroups: groupDefaults.MaxTokenAutoGroups,
      DefaultUseAutoGroup: groupDefaults.DefaultUseAutoGroup,
      GroupSpecialUsableGroup: normalizeJsonString(
        groupDefaults.GroupSpecialUsableGroup
      ),
      GroupDefaultModel: normalizeJsonString(groupDefaults.GroupDefaultModel),
      GroupRetryTimes: normalizeJsonString(groupDefaults.GroupRetryTimes),
      ModelSquareVisibleGroups: normalizeJsonString(
        groupDefaults.ModelSquareVisibleGroups
      ),
    }

    groupForm.reset({
      ...groupDefaults,
      GroupRatio: formatJsonForTextarea(groupDefaults.GroupRatio),
      TopupGroupRatio: formatJsonForTextarea(groupDefaults.TopupGroupRatio),
      UserUsableGroups: formatJsonForTextarea(groupDefaults.UserUsableGroups),
      GroupDescriptions: formatJsonForTextarea(groupDefaults.GroupDescriptions),
      GroupGroupRatio: formatJsonForTextarea(groupDefaults.GroupGroupRatio),
      AutoGroups: formatJsonForTextarea(groupDefaults.AutoGroups),
      AutoGroupDescription: groupDefaults.AutoGroupDescription,
      MaxTokenAutoGroups: groupDefaults.MaxTokenAutoGroups,
      GroupSpecialUsableGroup: formatJsonForTextarea(
        groupDefaults.GroupSpecialUsableGroup
      ),
      GroupDefaultModel: formatJsonForTextarea(groupDefaults.GroupDefaultModel),
      GroupRetryTimes: formatJsonForTextarea(groupDefaults.GroupRetryTimes),
      ModelSquareVisibleGroups: formatJsonForTextarea(
        groupDefaults.ModelSquareVisibleGroups
      ),
    })
  }, [groupDefaults, groupForm])

  useEffect(() => {
    groupModelPricingNormalizedDefaults.current = {
      GroupModelPricing: normalizeJsonString(
        groupModelPricingDefaults.GroupModelPricing
      ),
      GroupBillingMode: normalizeJsonString(
        groupModelPricingDefaults.GroupBillingMode
      ),
      GroupBillingExpr: normalizeJsonString(
        groupModelPricingDefaults.GroupBillingExpr
      ),
    }

    groupModelPricingForm.reset({
      GroupModelPricing: formatJsonForTextarea(
        groupModelPricingDefaults.GroupModelPricing
      ),
      GroupBillingMode: formatJsonForTextarea(
        groupModelPricingDefaults.GroupBillingMode
      ),
      GroupBillingExpr: formatJsonForTextarea(
        groupModelPricingDefaults.GroupBillingExpr
      ),
    })
  }, [groupModelPricingDefaults, groupModelPricingForm])

  const saveModelRatios = useCallback(
    async (values: ModelFormValues) => {
      const normalized = {
        ModelPrice: normalizeJsonString(values.ModelPrice),
        ModelRatio: normalizeJsonString(values.ModelRatio),
        CacheRatio: normalizeJsonString(values.CacheRatio),
        CreateCacheRatio: normalizeJsonString(values.CreateCacheRatio),
        CompletionRatio: normalizeJsonString(values.CompletionRatio),
        ImageRatio: normalizeJsonString(values.ImageRatio),
        AudioRatio: normalizeJsonString(values.AudioRatio),
        AudioCompletionRatio: normalizeJsonString(values.AudioCompletionRatio),
        ExposeRatioEnabled: values.ExposeRatioEnabled,
        BillingMode: normalizeJsonString(values.BillingMode),
        BillingExpr: normalizeJsonString(values.BillingExpr),
      }

      const apiKeyMap: Record<string, string> = {
        BillingMode: 'billing_setting.billing_mode',
        BillingExpr: 'billing_setting.billing_expr',
      }

      const updates = (
        Object.keys(normalized) as Array<keyof ModelFormValues>
      ).filter(
        (key) => normalized[key] !== modelNormalizedDefaults.current[key]
      )

      if (updates.length === 0) {
        toast.info(t('No model price changes to save'))
        return
      }

      for (const key of updates) {
        const apiKey = apiKeyMap[key as string] || (key as string)
        await updateOption.mutateAsync({ key: apiKey, value: normalized[key] })
      }

      modelNormalizedDefaults.current = normalized
      setSavedModelValues(normalized)
    },
    [t, updateOption]
  )

  const saveGroupRatios = useCallback(
    async (values: GroupFormValues) => {
      const normalized = {
        GroupRatio: normalizeJsonString(values.GroupRatio),
        TopupGroupRatio: normalizeJsonString(values.TopupGroupRatio),
        UserUsableGroups: normalizeJsonString(values.UserUsableGroups),
        GroupDescriptions: normalizeJsonString(values.GroupDescriptions),
        GroupGroupRatio: normalizeJsonString(values.GroupGroupRatio),
        AutoGroups: normalizeJsonString(values.AutoGroups),
        AutoGroupDescription: values.AutoGroupDescription.trim(),
        MaxTokenAutoGroups: values.MaxTokenAutoGroups,
        DefaultUseAutoGroup: values.DefaultUseAutoGroup,
        GroupSpecialUsableGroup: normalizeJsonString(
          values.GroupSpecialUsableGroup
        ),
        GroupDefaultModel: filterDefaultModelsByGroups(
          values.GroupDefaultModel,
          Object.keys(
            safeJsonParse<Record<string, number>>(values.GroupRatio, {
              fallback: {},
              silent: true,
            })
          )
        ),
        GroupRetryTimes: normalizeJsonString(values.GroupRetryTimes),
        ModelSquareVisibleGroups: normalizeJsonString(
          values.ModelSquareVisibleGroups
        ),
      }

      // Map form field names to API keys (most are 1:1, except GroupSpecialUsableGroup)
      const apiKeyMap: Record<string, string> = {
        GroupSpecialUsableGroup:
          'group_ratio_setting.group_special_usable_group',
        ModelSquareVisibleGroups:
          'console_setting.model_square_visible_groups',
      }

      const updates = (
        Object.keys(normalized) as Array<keyof typeof normalized>
      ).filter(
        (key) => normalized[key] !== groupNormalizedDefaults.current[key]
      )

      for (const key of updates) {
        const apiKey = apiKeyMap[key] || key
        await updateOption.mutateAsync({ key: apiKey, value: normalized[key] })
      }
      groupNormalizedDefaults.current = normalized
    },
    [updateOption]
  )

  /**
   * 保存分组定制定价喵。
   *
   * 三份配置各自对应一个后端 option key，只有内容真的变了才发请求，
   * 避免用户只改了一份却把三份全写一遍（写操作会触发后端重载定价缓存）喵。
   */
  const saveGroupModelPricing = useCallback(
    async (values: GroupModelPricingFormValues) => {
      const normalized = {
        GroupModelPricing: normalizeJsonString(values.GroupModelPricing),
        GroupBillingMode: normalizeJsonString(values.GroupBillingMode),
        GroupBillingExpr: normalizeJsonString(values.GroupBillingExpr),
      }

      // 表单字段名到后端 option key 的映射，分组计费方式与表达式都归 billing_setting 管喵。
      const apiKeyMap: Record<keyof GroupModelPricingFormValues, string> = {
        GroupModelPricing: 'GroupModelPricing',
        GroupBillingMode: 'billing_setting.group_billing_mode',
        GroupBillingExpr: 'billing_setting.group_billing_expr',
      }

      const updates = (
        Object.keys(normalized) as Array<keyof GroupModelPricingFormValues>
      ).filter(
        (key) =>
          normalized[key] !== groupModelPricingNormalizedDefaults.current[key]
      )

      // 喵~防御：没有任何改动时提示一声就返回，别白发三个请求喵。
      if (updates.length === 0) {
        toast.info(t('No group custom pricing changes to save'))
        return
      }

      for (const key of updates) {
        await updateOption.mutateAsync({
          key: apiKeyMap[key],
          value: normalized[key],
        })
      }

      groupModelPricingNormalizedDefaults.current = normalized
    },
    [t, updateOption]
  )

  const handleResetRatios = useCallback(() => {
    setConfirmOpen(true)
  }, [])

  const { mutate: resetMutate } = resetMutation
  const handleConfirmReset = useCallback(() => {
    resetMutate()
  }, [resetMutate])

  // 分组下拉框的候选项来自分组倍率配置的 key：站点配过倍率的分组才是真实存在的分组喵。
  const availableGroups = useMemo(() => {
    const groupRatioMap = safeJsonParse<Record<string, number>>(
      groupDefaults.GroupRatio,
      { fallback: {}, silent: true }
    )
    // 喵~防御：解析失败或配置为空时返回空数组，编辑面板会因此整体禁用而不是崩掉喵。
    return Object.keys(groupRatioMap).sort((left, right) =>
      left.localeCompare(right)
    )
  }, [groupDefaults.GroupRatio])

  const tabLabels: Record<RatioTabId, string> = {
    models: 'Model prices',
    'unset-models': 'Unset price models',
    groups: 'Group ratios',
    'group-model-pricing': 'Group custom pricing',
    'tool-prices': 'Tool prices',
    'upstream-sync': 'Upstream price sync',
  }
  const tabsGridClass =
    {
      1: 'grid-cols-1',
      2: 'grid-cols-2',
      3: 'grid-cols-3',
      4: 'grid-cols-4',
      5: 'grid-cols-5',
      6: 'grid-cols-6',
    }[visibleTabs.length] ?? 'grid-cols-4'
  const defaultTab = visibleTabs[0] ?? 'models'

  const renderTabContent = (tab: RatioTabId) => {
    if (tab === 'models' || tab === 'unset-models') {
      return (
        <ModelRatioForm
          form={modelForm}
          savedValues={savedModelValues}
          onSave={saveModelRatios}
          onReset={handleResetRatios}
          isSaving={updateOption.isPending}
          isResetting={resetMutation.isPending}
          variant={tab === 'unset-models' ? 'unset' : 'default'}
        />
      )
    }
    if (tab === 'groups') {
      return (
        <GroupRatioForm
          form={groupForm}
          onSave={saveGroupRatios}
          isSaving={updateOption.isPending}
        />
      )
    }
    if (tab === 'group-model-pricing') {
      return (
        <GroupModelPricingForm
          form={groupModelPricingForm}
          onSave={saveGroupModelPricing}
          isSaving={updateOption.isPending}
          availableGroups={availableGroups}
        />
      )
    }
    if (tab === 'tool-prices') {
      return <ToolPriceSettings defaultValue={toolPricesDefault} />
    }
    return (
      <UpstreamRatioSync
        modelRatios={{
          ModelPrice: modelDefaults.ModelPrice,
          ModelRatio: modelDefaults.ModelRatio,
          CompletionRatio: modelDefaults.CompletionRatio,
          CacheRatio: modelDefaults.CacheRatio,
          CreateCacheRatio: modelDefaults.CreateCacheRatio,
          ImageRatio: modelDefaults.ImageRatio,
          AudioRatio: modelDefaults.AudioRatio,
          AudioCompletionRatio: modelDefaults.AudioCompletionRatio,
          'billing_setting.billing_mode': modelDefaults.BillingMode,
          'billing_setting.billing_expr': modelDefaults.BillingExpr,
        }}
      />
    )
  }

  const renderTabSwitcher = () => (
    <TabsList className={`grid w-fit max-w-full ${tabsGridClass}`}>
      {visibleTabs.map((tab) => (
        <TabsTrigger key={tab} value={tab}>
          {t(tabLabels[tab])}
        </TabsTrigger>
      ))}
    </TabsList>
  )

  return (
    <>
      {visibleTabs.length === 1 ? (
        <SettingsSection title={t(titleKey)}>
          {renderTabContent(defaultTab)}
        </SettingsSection>
      ) : (
        <Tabs defaultValue={defaultTab} className='h-full min-h-0 gap-6'>
          <SettingsPageTitleStatusPortal>
            {renderTabSwitcher()}
          </SettingsPageTitleStatusPortal>

          <SettingsSection title={t(titleKey)} className='min-h-0 flex-1'>
            {visibleTabs.map((tab) => (
              <TabsContent key={tab} value={tab} className='min-h-0'>
                {renderTabContent(tab)}
              </TabsContent>
            ))}
          </SettingsSection>
        </Tabs>
      )}

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t('Reset all model prices?')}
        desc={t(
          'This will clear custom pricing ratios and revert to upstream defaults.'
        )}
        destructive
        isLoading={resetMutation.isPending}
        handleConfirm={handleConfirmReset}
        confirmText={t('Reset')}
      />
    </>
  )
}
