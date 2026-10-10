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

// AutoCheckin 池额度配置表单校验：路径为字符串，时效天数为正整数
const schema = z.object({
  enabled: z.boolean(),
  dataDbPath: z.string(),
  freshnessDays: z.coerce.number().int().min(1),
})

type Values = z.infer<typeof schema>

export function AutoCheckinSettingsSection({
  defaultValues,
}: {
  defaultValues: {
    enabled: boolean
    dataDbPath: string
    freshnessDays: number
  }
}) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const form = useForm<Values>({
    resolver: zodResolver(schema) as unknown as Resolver<Values>,
    defaultValues: {
      enabled: defaultValues.enabled,
      dataDbPath: defaultValues.dataDbPath ?? '',
      freshnessDays: defaultValues.freshnessDays,
    },
  })

  const { isDirty, isSubmitting } = form.formState
  const enabled = form.watch('enabled')

  async function onSubmit(values: Values) {
    // 收集发生变化的字段，逐个写回 autocheckin_setting.* 配置键
    const updates: Array<{ key: string; value: string }> = []

    if (values.enabled !== defaultValues.enabled) {
      updates.push({
        key: 'autocheckin_setting.enabled',
        value: String(values.enabled),
      })
    }
    if (values.dataDbPath !== (defaultValues.dataDbPath ?? '')) {
      updates.push({
        key: 'autocheckin_setting.data_db_path',
        value: values.dataDbPath,
      })
    }
    if (values.freshnessDays !== defaultValues.freshnessDays) {
      updates.push({
        key: 'autocheckin_setting.freshness_days',
        value: String(values.freshnessDays),
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
    <SettingsSection title={t('Pool Quota Settings')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete='off'>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending || isSubmitting}
            isSaveDisabled={!isDirty}
            saveLabel='Save pool quota settings'
          />
          <FormField
            control={form.control}
            name='enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable pool quota display')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Read the AutoCheckin data.db (read-only) and show pool balances on the status page; also powers the relief fund pool balance.'
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
            <div className='grid gap-6 md:grid-cols-2'>
              <FormField
                control={form.control}
                name='dataDbPath'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('AutoCheckin data.db path')}</FormLabel>
                    <FormControl>
                      <Input
                        placeholder='C:\\AutoCheck\\webui\\data.db'
                        {...field}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('Absolute path, opened read-only; never written to.')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='freshnessDays'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Pool data freshness (days)')}</FormLabel>
                    <FormControl>
                      <Input type='number' min={1} {...field} />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Only count balances from accounts checked in within this many days.'
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
