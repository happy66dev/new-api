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
import { Code2, Eye, Plus, Save, Trash2 } from 'lucide-react'
import { useCallback, useMemo, useState } from 'react'
import type { UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StaticDataTable } from '@/components/data-table'
import { GroupBadge } from '@/components/group-badge'
import { JsonCodeEditor } from '@/components/json-code-editor'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'

import { SettingsForm } from '../components/settings-form-layout'
import { safeJsonParse } from '../utils/json-parser'
import {
  GROUP_BILLING_MODE_INHERIT,
  GROUP_BILLING_MODE_PER_CALL,
  GROUP_BILLING_MODE_PER_TOKEN,
  GROUP_BILLING_MODE_TIERED,
  GROUP_PRICING_NUMERIC_FIELDS,
  type GroupModelPricingFormValues,
  type GroupModelPricingMap,
  type GroupPricingDraft,
  type GroupPricingOverride,
  buildDraftFromOverride,
  draftToOverride,
  formatOverrideSummary,
  parseGroupBillingText,
  stringifyGroupPricing,
} from './group-model-pricing-utils'

type GroupModelPricingFormProps = {
  form: UseFormReturn<GroupModelPricingFormValues>
  onSave: (values: GroupModelPricingFormValues) => Promise<void>
  isSaving: boolean
  /** 站点已配置的分组名列表，来自分组倍率配置喵。 */
  availableGroups: string[]
}

/** 空草稿：所有字段留空表示「继承全局」喵。 */
const EMPTY_DRAFT: GroupPricingDraft = {
  modelName: '',
  billingMode: GROUP_BILLING_MODE_INHERIT,
  modelPrice: '',
  modelRatio: '',
  completionRatio: '',
  cacheRatio: '',
  createCacheRatio: '',
  imageRatio: '',
  audioRatio: '',
  audioCompletionRatio: '',
  billingExpr: '',
}

export function GroupModelPricingForm(props: GroupModelPricingFormProps) {
  const { t } = useTranslation()
  const [editMode, setEditMode] = useState<'visual' | 'json'>('visual')
  const [selectedGroup, setSelectedGroup] = useState<string>(
    props.availableGroups[0] ?? ''
  )
  const [draft, setDraft] = useState<GroupPricingDraft>(EMPTY_DRAFT)
  // 正在编辑的原模型名；为空表示这是一条新增项喵。
  const [editingModelName, setEditingModelName] = useState<string>('')

  const pricingText = props.form.watch('GroupModelPricing')
  const groupModeText = props.form.watch('GroupBillingMode')
  const groupExprText = props.form.watch('GroupBillingExpr')

  // 三份配置解析成同一张「分组 -> 模型 -> 定价」视图，表格与编辑面板都读它喵。
  const pricingMap = useMemo(
    () =>
      safeJsonParse<GroupModelPricingMap>(pricingText, {
        fallback: {},
        silent: true,
      }),
    [pricingText]
  )
  const groupModeMap = useMemo(
    () => parseGroupBillingText(groupModeText),
    [groupModeText]
  )
  const groupExprMap = useMemo(
    () => parseGroupBillingText(groupExprText),
    [groupExprText]
  )

  // 当前分组下已配置定制的模型名：定价覆盖、分组计费方式、分组表达式三者的并集喵。
  const configuredModels = useMemo(() => {
    const names = new Set<string>()
    for (const modelName of Object.keys(pricingMap[selectedGroup] ?? {})) {
      names.add(modelName)
    }
    for (const modelName of Object.keys(groupModeMap[selectedGroup] ?? {})) {
      names.add(modelName)
    }
    for (const modelName of Object.keys(groupExprMap[selectedGroup] ?? {})) {
      names.add(modelName)
    }
    return [...names].sort((left, right) => left.localeCompare(right))
  }, [groupExprMap, groupModeMap, pricingMap, selectedGroup])

  const setFormValue = useCallback(
    (field: keyof GroupModelPricingFormValues, value: string) => {
      props.form.setValue(field, value, {
        shouldValidate: true,
        shouldDirty: true,
      })
    },
    [props.form]
  )

  /** 把一条定制项写回三份 JSON 配置喵。传 null 表示删除该模型的定制喵。 */
  const writeOverride = useCallback(
    (
      modelName: string,
      override: GroupPricingOverride | null,
      expr: string
    ) => {
      const nextPricing: GroupModelPricingMap = {
        ...pricingMap,
        // 展开 undefined 得到空对象，所以这里不需要 ?? {} 兜底，新分组会自然拿到一张空表喵。
        [selectedGroup]: { ...pricingMap[selectedGroup] },
      }
      const nextMode = {
        ...groupModeMap,
        [selectedGroup]: { ...groupModeMap[selectedGroup] },
      }
      const nextExpr = {
        ...groupExprMap,
        [selectedGroup]: { ...groupExprMap[selectedGroup] },
      }

      // 删除：三份配置里都把这个模型摘掉，空分组也一并清掉，避免留下空对象喵。
      if (override === null) {
        delete nextPricing[selectedGroup][modelName]
        delete nextMode[selectedGroup][modelName]
        delete nextExpr[selectedGroup][modelName]
      } else if (override.billing_mode === GROUP_BILLING_MODE_TIERED) {
        // 阶梯计费：计费方式与表达式写进 billing_setting 那两份配置，
        // 定价覆盖里不再保留这个模型，避免同一个模型同时存在两套价格口径喵。
        delete nextPricing[selectedGroup][modelName]
        nextMode[selectedGroup][modelName] = GROUP_BILLING_MODE_TIERED
        nextExpr[selectedGroup][modelName] = expr
      } else {
        nextPricing[selectedGroup][modelName] = override
        // 切回按量/按次时必须清掉分组级阶梯配置，否则计费仍会走表达式喵。
        delete nextMode[selectedGroup][modelName]
        delete nextExpr[selectedGroup][modelName]
      }

      for (const bucket of [nextPricing, nextMode, nextExpr]) {
        if (Object.keys(bucket[selectedGroup] ?? {}).length === 0) {
          delete bucket[selectedGroup]
        }
      }

      setFormValue('GroupModelPricing', stringifyGroupPricing(nextPricing))
      setFormValue('GroupBillingMode', stringifyGroupPricing(nextMode))
      setFormValue('GroupBillingExpr', stringifyGroupPricing(nextExpr))
    },
    [groupExprMap, groupModeMap, pricingMap, selectedGroup, setFormValue]
  )

  const handleEditRow = useCallback(
    (modelName: string) => {
      setEditingModelName(modelName)
      setDraft(
        buildDraftFromOverride(
          modelName,
          pricingMap[selectedGroup]?.[modelName],
          groupModeMap[selectedGroup]?.[modelName],
          groupExprMap[selectedGroup]?.[modelName]
        )
      )
    },
    [groupExprMap, groupModeMap, pricingMap, selectedGroup]
  )

  const handleDeleteRow = useCallback(
    (modelName: string) => {
      writeOverride(modelName, null, '')
      // 删掉的正好是正在编辑的那条时清空面板，避免面板还停在已不存在的模型上喵。
      if (editingModelName === modelName) {
        setEditingModelName('')
        setDraft(EMPTY_DRAFT)
      }
    },
    [editingModelName, writeOverride]
  )

  const handleApplyDraft = useCallback(() => {
    const modelName = draft.modelName.trim()
    // 喵~防御：模型名为空时不可能命中任何请求，直接拦下来并提示喵。
    if (modelName === '') {
      toast.error(t('Model name is required'))
      return
    }
    const result = draftToOverride(draft)
    // 喵~防御：数值非法（负数、非数字）时拒绝写入，绝不让脏价格进到计费配置里喵。
    if (!result.ok) {
      toast.error(t(result.messageKey))
      return
    }
    // 改名相当于「删旧增新」，先把旧模型名的定制摘掉喵。
    if (editingModelName !== '' && editingModelName !== modelName) {
      writeOverride(editingModelName, null, '')
    }
    writeOverride(modelName, result.override, draft.billingExpr.trim())
    setEditingModelName(modelName)
    toast.success(t('Override applied. Remember to save.'))
  }, [draft, editingModelName, t, writeOverride])

  const handleNewDraft = useCallback(() => {
    setEditingModelName('')
    setDraft(EMPTY_DRAFT)
  }, [])

  const handleSave = useCallback(async () => {
    await props.form.handleSubmit(props.onSave)()
  }, [props.form, props.onSave])

  const toggleEditMode = useCallback(() => {
    setEditMode((prev) => (prev === 'visual' ? 'json' : 'visual'))
  }, [])

  const isTieredDraft = draft.billingMode === GROUP_BILLING_MODE_TIERED

  /** Select 关闭时可能回传 null，统一收敛成空串再交给状态，避免出现 null 分组喵。 */
  const handleGroupChange = useCallback((value: string | null) => {
    // 喵~防御：null 表示「清空选择」，按空串处理会让编辑面板整体禁用，是最安全的行为喵。
    setSelectedGroup(value ?? '')
  }, [])

  return (
    <div className='space-y-6'>
      <div className='flex flex-wrap justify-end gap-2'>
        <Button
          type='button'
          size='sm'
          onClick={handleSave}
          disabled={props.isSaving}
        >
          <Save data-icon='inline-start' />
          {props.isSaving ? t('Saving...') : t('Save group custom pricing')}
        </Button>
        <Button variant='outline' size='sm' onClick={toggleEditMode}>
          {editMode === 'visual' ? (
            <>
              <Code2 className='mr-2 h-4 w-4' />
              {t('Switch to JSON')}
            </>
          ) : (
            <>
              <Eye className='mr-2 h-4 w-4' />
              {t('Switch to Visual')}
            </>
          )}
        </Button>
      </div>

      <Form {...props.form}>
        {editMode === 'visual' ? (
          <div className='space-y-5'>
            <p className='text-muted-foreground text-xs leading-5'>
              {t(
                'Per-group pricing lets the same model bill differently in each group. Group ratio still applies on top of the custom price.'
              )}
            </p>

            <div className='flex flex-wrap items-end gap-3'>
              <div className='flex min-w-56 flex-col gap-1.5'>
                <Label>{t('Group')}</Label>
                <Select
                  items={props.availableGroups.map((group) => ({
                    value: group,
                    label: group,
                  }))}
                  value={selectedGroup}
                  onValueChange={handleGroupChange}
                >
                  <SelectTrigger>
                    <SelectValue placeholder={t('Select a group')} />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      {props.availableGroups.map((group) => (
                        <SelectItem key={group} value={group}>
                          {group}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </div>
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={handleNewDraft}
                disabled={selectedGroup === ''}
              >
                <Plus data-icon='inline-start' />
                {t('Add model override')}
              </Button>
            </div>

            <StaticDataTable
              tableClassName='text-sm'
              data={configuredModels}
              getRowKey={(modelName) => modelName}
              emptyContent={t('No group custom pricing configured yet')}
              columns={[
                {
                  id: 'model',
                  header: t('Model'),
                  cellClassName: 'py-2.5 font-mono text-xs',
                  cell: (modelName: string) => modelName,
                },
                {
                  id: 'group',
                  header: t('Group'),
                  cellClassName: 'py-2.5',
                  cell: () => <GroupBadge group={selectedGroup} size='sm' />,
                },
                {
                  id: 'summary',
                  header: t('Pricing'),
                  cellClassName: 'text-muted-foreground py-2.5 text-xs',
                  cell: (modelName: string) =>
                    t(
                      formatOverrideSummary(
                        pricingMap[selectedGroup]?.[modelName],
                        groupModeMap[selectedGroup]?.[modelName]
                      )
                    ),
                },
                {
                  id: 'actions',
                  header: t('Actions'),
                  className: 'text-right',
                  cellClassName: 'py-2.5 text-right',
                  cell: (modelName: string) => (
                    <div className='flex justify-end gap-1'>
                      <Button
                        type='button'
                        variant='ghost'
                        size='sm'
                        onClick={() => handleEditRow(modelName)}
                      >
                        {t('Edit')}
                      </Button>
                      <Button
                        type='button'
                        variant='ghost'
                        size='icon-sm'
                        aria-label={t('Delete')}
                        onClick={() => handleDeleteRow(modelName)}
                      >
                        <Trash2 className='h-4 w-4' />
                      </Button>
                    </div>
                  ),
                },
              ]}
            />

            <GroupPricingDraftPanel
              draft={draft}
              isEditing={editingModelName !== ''}
              isTiered={isTieredDraft}
              disabled={selectedGroup === ''}
              onChange={setDraft}
              onApply={handleApplyDraft}
            />
          </div>
        ) : (
          <SettingsForm onSubmit={props.form.handleSubmit(props.onSave)}>
            <div className='grid min-w-0 gap-x-5 gap-y-8 lg:grid-cols-2 2xl:grid-cols-3'>
              <GroupPricingJsonField
                form={props.form}
                name='GroupModelPricing'
                label={t('Group custom pricing')}
                description={t(
                  'JSON map of group → model → pricing override. Empty fields inherit the global price.'
                )}
              />
              <GroupPricingJsonField
                form={props.form}
                name='GroupBillingMode'
                label={t('Group billing mode')}
                description={t(
                  'JSON map of group → model → billing mode. Only tiered_expr is meaningful here.'
                )}
              />
              <GroupPricingJsonField
                form={props.form}
                name='GroupBillingExpr'
                label={t('Group billing expression')}
                description={t(
                  'JSON map of group → model → tiered billing expression.'
                )}
              />
            </div>
          </SettingsForm>
        )}
      </Form>
    </div>
  )
}

function GroupPricingJsonField(props: {
  form: UseFormReturn<GroupModelPricingFormValues>
  name: keyof GroupModelPricingFormValues
  label: string
  description: string
}) {
  return (
    <FormField
      control={props.form.control}
      name={props.name}
      render={({ field }) => (
        <FormItem className='flex min-w-0 flex-col gap-2'>
          <FormLabel>{props.label}</FormLabel>
          <FormControl>
            <JsonCodeEditor
              value={field.value}
              onChange={(value) => field.onChange(value)}
              name={field.name}
              onBlur={field.onBlur}
              textareaRef={field.ref}
            />
          </FormControl>
          <FormDescription className='text-xs leading-5'>
            {props.description}
          </FormDescription>
          <FormMessage />
        </FormItem>
      )}
    />
  )
}

/** 单条定制项的编辑面板：留空即继承全局价，是这份 UI 最重要的约定喵。 */
function GroupPricingDraftPanel(props: {
  draft: GroupPricingDraft
  isEditing: boolean
  isTiered: boolean
  disabled: boolean
  onChange: (draft: GroupPricingDraft) => void
  onApply: () => void
}) {
  const { t } = useTranslation()

  const updateField = (field: keyof GroupPricingDraft, value: string) => {
    props.onChange({ ...props.draft, [field]: value })
  }

  return (
    <div className='space-y-4 rounded-lg border p-4'>
      <div className='text-sm font-medium'>
        {props.isEditing ? t('Edit model override') : t('Add model override')}
      </div>

      <div className='grid gap-4 sm:grid-cols-2 lg:grid-cols-3'>
        <div className='flex flex-col gap-1.5'>
          <Label>{t('Model')}</Label>
          <Input
            value={props.draft.modelName}
            placeholder='deepseek-chat'
            disabled={props.disabled}
            onChange={(event) => updateField('modelName', event.target.value)}
          />
        </div>

        <div className='flex flex-col gap-1.5'>
          <Label>{t('Billing mode')}</Label>
          <Select
            items={[
              {
                value: GROUP_BILLING_MODE_INHERIT,
                label: t('Inherit global'),
              },
              { value: GROUP_BILLING_MODE_PER_TOKEN, label: t('Per-token') },
              { value: GROUP_BILLING_MODE_PER_CALL, label: t('Per-request') },
              {
                value: GROUP_BILLING_MODE_TIERED,
                label: t('Tiered expression'),
              },
            ]}
            value={props.draft.billingMode}
            onValueChange={(value) =>
              // 喵~防御：Select 清空时回传 null，按「继承全局」处理，绝不写 null 进草稿喵。
              updateField('billingMode', value ?? GROUP_BILLING_MODE_INHERIT)
            }
          >
            <SelectTrigger disabled={props.disabled}>
              <SelectValue placeholder={t('Inherit global')} />
            </SelectTrigger>
            <SelectContent alignItemWithTrigger={false}>
              <SelectGroup>
                <SelectItem value={GROUP_BILLING_MODE_INHERIT}>
                  {t('Inherit global')}
                </SelectItem>
                <SelectItem value={GROUP_BILLING_MODE_PER_TOKEN}>
                  {t('Per-token')}
                </SelectItem>
                <SelectItem value={GROUP_BILLING_MODE_PER_CALL}>
                  {t('Per-request')}
                </SelectItem>
                <SelectItem value={GROUP_BILLING_MODE_TIERED}>
                  {t('Tiered expression')}
                </SelectItem>
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>

        {!props.isTiered &&
          GROUP_PRICING_NUMERIC_FIELDS.map((numericField) => (
            <div key={numericField.field} className='flex flex-col gap-1.5'>
              <Label>{t(numericField.labelKey)}</Label>
              <Input
                type='number'
                inputMode='decimal'
                step='any'
                min={0}
                value={props.draft[numericField.field]}
                placeholder={t('Inherit global')}
                disabled={props.disabled}
                onChange={(event) =>
                  updateField(numericField.field, event.target.value)
                }
              />
            </div>
          ))}
      </div>

      {props.isTiered && (
        <div className='flex flex-col gap-1.5'>
          <Label>{t('Group billing expression')}</Label>
          <Textarea
            value={props.draft.billingExpr}
            rows={3}
            disabled={props.disabled}
            placeholder='p * 0.27 + c * 1.1'
            onChange={(event) => updateField('billingExpr', event.target.value)}
          />
          <p className='text-muted-foreground text-xs leading-5'>
            {t(
              'Coefficients are USD per 1M tokens. The expression fully replaces ratios for this group.'
            )}
          </p>
        </div>
      )}

      <div className='flex justify-end'>
        <Button
          type='button'
          size='sm'
          variant='outline'
          disabled={props.disabled}
          onClick={props.onApply}
        >
          {props.isEditing ? t('Update override') : t('Add override')}
        </Button>
      </div>
    </div>
  )
}
