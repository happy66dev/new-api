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
*/
import { zodResolver } from '@hookform/resolvers/zod'
import { useEffect } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import * as z from 'zod'

import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormLabel,
} from '@/components/ui/form'
import { Switch } from '@/components/ui/switch'

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

const userUpstreamSchema = z.object({
  UserUpstreamEnabled: z.boolean(),
  UserUpstreamSharingEnabled: z.boolean(),
})

type UserUpstreamFormValues = z.infer<typeof userUpstreamSchema>

type UserUpstreamSectionProps = {
  defaultValues: UserUpstreamFormValues
}

export function UserUpstreamSection({
  defaultValues,
}: UserUpstreamSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const form = useForm<UserUpstreamFormValues>({
    resolver: zodResolver(userUpstreamSchema),
    defaultValues,
  })
  // 监听总开关当前值：总开关关闭时共享开关必须联动关闭并置灰喵。
  const masterEnabled = useWatch({
    control: form.control,
    name: 'UserUpstreamEnabled',
  })

  useEffect(() => {
    form.reset(defaultValues)
  }, [defaultValues, form])

  useEffect(() => {
    // 总开关关闭时，把共享开关的值一并改为 false，避免提交后仍残留开启状态喵。
    if (!masterEnabled && form.getValues('UserUpstreamSharingEnabled')) {
      form.setValue('UserUpstreamSharingEnabled', false)
    }
  }, [masterEnabled, form])

  const onSubmit = async (values: UserUpstreamFormValues) => {
    // 共享开关实际生效值依赖总开关：总开关关闭时共享视为关闭喵。
    const effectiveShare = values.UserUpstreamEnabled
      ? values.UserUpstreamSharingEnabled
      : false
    const updates: Array<{ key: string; value: boolean }> = []
    if (values.UserUpstreamEnabled !== defaultValues.UserUpstreamEnabled) {
      updates.push({
        key: 'UserUpstreamEnabled',
        value: values.UserUpstreamEnabled,
      })
    }
    if (effectiveShare !== defaultValues.UserUpstreamSharingEnabled) {
      updates.push({
        key: 'UserUpstreamSharingEnabled',
        value: effectiveShare,
      })
    }
    for (const update of updates) {
      await updateOption.mutateAsync({
        key: update.key,
        value: update.value,
      })
    }
  }

  return (
    <SettingsSection title={t('User Upstream Models')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
            saveLabel='Save user upstream models'
          />
          <div className='space-y-4'>
            <FormField
              control={form.control}
              name='UserUpstreamEnabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>
                      {t('Allow users to create their own upstream models')}
                    </FormLabel>
                    <FormDescription>
                      {t(
                        'Controls whether users can add and use their own upstream models (user/...). Turning it off hides the Upstream Models page, deletes all virtual model references to user upstreams, and freezes existing entries without deleting their data.'
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />

            <FormField
              control={form.control}
              name='UserUpstreamSharingEnabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>
                      {t(
                        'Allow users to share upstream models to other users'
                      )}
                    </FormLabel>
                    <FormDescription>
                      {t(
                        'Controls whether users can share their upstream models with everyone through the user-shared group. Turning it off stops all sharing, hides the user-shared group, deletes virtual model references to shared models, and prevents users from enabling sharing again.'
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                      disabled={!masterEnabled}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />
          </div>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
