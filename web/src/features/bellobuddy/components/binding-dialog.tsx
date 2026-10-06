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
import { useForm, Controller } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Field,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Form } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { getServerErrorStatus } from '@/lib/server-error-message'

import { saveJobBinding, type JobBinding } from '../api'

type Props = { binding: JobBinding | null; onClose: () => void }

export function BindingDialog(props: Props) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const schema = z
    .object({
      shared_key: z
        .string()
        .min(1, t('Required'))
        .max(128)
        .regex(
          /^[A-Za-z0-9_.:-]+$/,
          t('Use letters, numbers, dots, colons, underscores or hyphens')
        ),
      name: z.string().trim().min(1, t('Required')).max(128),
      channel_id: z
        .number({ error: t('Invalid value') })
        .int()
        .min(0)
        .max(Number.MAX_SAFE_INTEGER),
      model: z.string().max(128),
      enabled: z.boolean(),
    })
    .superRefine((value, context) => {
      if (value.enabled && value.channel_id === 0) {
        context.addIssue({
          code: 'custom',
          path: ['channel_id'],
          message: t('Select a channel before enabling the robot'),
        })
      }
      if ((value.enabled || value.channel_id > 0) && !value.model.trim()) {
        context.addIssue({
          code: 'custom',
          path: ['model'],
          message: t('Required'),
        })
      }
    })
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    defaultValues: {
      shared_key: props.binding?.shared_key ?? '',
      name: props.binding?.name ?? '',
      channel_id: props.binding?.channel_id ?? 0,
      model: props.binding?.model ?? '',
      enabled: props.binding?.enabled ?? false,
    },
  })
  const mutation = useMutation({
    mutationFn: (value: z.infer<typeof schema>) =>
      saveJobBinding(props.binding?.id ?? 0, {
        ...value,
        expected_version: props.binding?.version ?? 0,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['bellobuddy-bindings'] })
      props.onClose()
    },
    onError: (error) => {
      if (getServerErrorStatus(error) === 409) {
        void queryClient.invalidateQueries({
          queryKey: ['bellobuddy-bindings'],
        })
      }
    },
  })
  const conflict =
    mutation.isError && getServerErrorStatus(mutation.error) === 409
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !mutation.isPending) props.onClose()
      }}
      title={
        props.binding ? t('Edit robot binding') : t('Create robot binding')
      }
      footer={
        <>
          <Button
            variant='outline'
            disabled={mutation.isPending}
            onClick={props.onClose}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='submit'
            form='bellobuddy-binding-form'
            disabled={mutation.isPending || conflict}
          >
            {t('Save')}
          </Button>
        </>
      }
    >
      {conflict && (
        <Alert variant='destructive' className='mb-4'>
          <AlertDescription>
            {t(
              'The configuration changed. Close this dialog and reopen it to review the latest version.'
            )}
          </AlertDescription>
        </Alert>
      )}
      <Form {...form}>
        <form
          id='bellobuddy-binding-form'
          onSubmit={form.handleSubmit((value) => {
            if (!conflict) mutation.mutate(value)
          })}
        >
          <FieldGroup>
            <Field data-invalid={!!form.formState.errors.shared_key}>
              <FieldLabel htmlFor='binding-key'>
                {t('Shared job key')}
              </FieldLabel>
              <Input
                id='binding-key'
                readOnly={!!props.binding}
                disabled={mutation.isPending}
                aria-invalid={!!form.formState.errors.shared_key}
                {...form.register('shared_key')}
              />
              <FieldError errors={[form.formState.errors.shared_key]} />
            </Field>
            <Field data-invalid={!!form.formState.errors.name}>
              <FieldLabel htmlFor='binding-name'>{t('Name')}</FieldLabel>
              <Input
                id='binding-name'
                disabled={mutation.isPending}
                aria-invalid={!!form.formState.errors.name}
                {...form.register('name')}
              />
              <FieldError errors={[form.formState.errors.name]} />
            </Field>
            <Field data-invalid={!!form.formState.errors.channel_id}>
              <FieldLabel htmlFor='binding-channel'>
                {t('Channel ID')}
              </FieldLabel>
              <Input
                id='binding-channel'
                type='number'
                min={0}
                step={1}
                disabled={mutation.isPending}
                aria-invalid={!!form.formState.errors.channel_id}
                {...form.register('channel_id', { valueAsNumber: true })}
              />
              <FieldError errors={[form.formState.errors.channel_id]} />
            </Field>
            <Field data-invalid={!!form.formState.errors.model}>
              <FieldLabel htmlFor='binding-model'>
                {t('Logical model')}
              </FieldLabel>
              <Input
                id='binding-model'
                disabled={mutation.isPending}
                aria-invalid={!!form.formState.errors.model}
                {...form.register('model')}
              />
              <FieldError errors={[form.formState.errors.model]} />
            </Field>
            <Field orientation='horizontal'>
              <Controller
                control={form.control}
                name='enabled'
                render={({ field }) => (
                  <Checkbox
                    id='binding-enabled'
                    checked={field.value}
                    onCheckedChange={field.onChange}
                    disabled={mutation.isPending}
                  />
                )}
              />
              <FieldLabel htmlFor='binding-enabled'>{t('Enabled')}</FieldLabel>
            </Field>
          </FieldGroup>
        </form>
      </Form>
    </Dialog>
  )
}
