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
import { useForm, type Resolver } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

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
import { Switch } from '@/components/ui/switch'

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

// 救济金配置表单校验：金额/小时均为非负数
const schema = z.object({
  enabled: z.boolean(),
  reserveAmount: z.coerce.number().min(0),
  balanceThresholdUsd: z.coerce.number().min(0),
  cooldownHours: z.coerce.number().min(0),
  grantAmountUsd: z.coerce.number().min(0),
})

type Values = z.infer<typeof schema>

export function ReliefSettingsSection({
  defaultValues,
}: {
  defaultValues: {
    enabled: boolean
    reserveAmount: number
    balanceThresholdUsd: number
    cooldownHours: number
    grantAmountUsd: number
  }
}) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const form = useForm<Values>({
    resolver: zodResolver(schema) as unknown as Resolver<Values>,
    defaultValues: {
      enabled: defaultValues.enabled,
      reserveAmount: defaultValues.reserveAmount,
      balanceThresholdUsd: defaultValues.balanceThresholdUsd,
      cooldownHours: defaultValues.cooldownHours,
      grantAmountUsd: defaultValues.grantAmountUsd,
    },
  })

  const { isDirty, isSubmitting } = form.formState
  const enabled = form.watch('enabled')

  async function onSubmit(values: Values) {
    // 收集发生变化的字段，逐个写回对应的 relief_setting.* 配置键
    const updates: Array<{ key: string; value: string }> = []

    if (values.enabled !== defaultValues.enabled) {
      updates.push({
        key: 'relief_setting.enabled',
        value: String(values.enabled),
      })
    }
    if (values.reserveAmount !== defaultValues.reserveAmount) {
      updates.push({
        key: 'relief_setting.reserve_amount',
        value: String(values.reserveAmount),
      })
    }
    if (values.balanceThresholdUsd !== defaultValues.balanceThresholdUsd) {
      updates.push({
        key: 'relief_setting.balance_threshold_usd',
        value: String(values.balanceThresholdUsd),
      })
    }
    if (values.cooldownHours !== defaultValues.cooldownHours) {
      updates.push({
        key: 'relief_setting.cooldown_hours',
        value: String(values.cooldownHours),
      })
    }
    if (values.grantAmountUsd !== defaultValues.grantAmountUsd) {
      updates.push({
        key: 'relief_setting.grant_amount_usd',
        value: String(values.grantAmountUsd),
      })
    }

    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }

    for (const update of updates) {
      await updateOption.mutateAsync(update)
    }

    form.reset(values)
  }

  return (
    <SettingsSection title={t('Relief Fund Settings')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete='off'>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending || isSubmitting}
            isSaveDisabled={!isDirty}
            saveLabel='Save relief fund settings'
          />
          <FormField
            control={form.control}
            name='enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable relief fund')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Allow low-balance users whose subscriptions are used up to claim a time-limited relief grant.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                    disabled={updateOption.isPending || isSubmitting}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          {enabled && (
            <div className='grid gap-6 md:grid-cols-2 lg:grid-cols-4'>
              {/* RELIEF_SETTINGS_FIELDS_PLACEHOLDER */}
              <FormField
                control={form.control}
                name='reserveAmount'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Relief pool reserve (USD)')}</FormLabel>
                    <FormControl>
                      <Input type='number' min={0} step='0.01' {...field} />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Reserved from the pool. Relief remaining = pool balance - reserve - granted.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='balanceThresholdUsd'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Claim balance threshold (USD)')}</FormLabel>
                    <FormControl>
                      <Input type='number' min={0} step='0.01' {...field} />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Only users whose wallet balance is below this value can claim.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              {/* RELIEF_SETTINGS_FIELDS_PLACEHOLDER_2 */}
              <FormField
                control={form.control}
                name='cooldownHours'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Cooldown between claims (hours)')}</FormLabel>
                    <FormControl>
                      <Input type='number' min={0} step='0.1' {...field} />
                    </FormControl>
                    <FormDescription>
                      {t('Users must wait this long before claiming again.')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='grantAmountUsd'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Grant amount (USD)')}</FormLabel>
                    <FormControl>
                      <Input type='number' min={0} step='0.01' {...field} />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Total quota granted per claim, valid for 1 day. Applied when the relief plan is auto-created.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
          )}
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
