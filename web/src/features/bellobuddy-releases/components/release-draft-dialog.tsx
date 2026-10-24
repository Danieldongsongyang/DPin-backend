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
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Form } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { currentReleaseIdentity } from '../access'
import { saveRelease } from '../api'
import { releaseDraftSchema } from '../lib/draft-schema'
import type { Release, ReleaseDraftInput } from '../types'

type Props = { identity: string; release?: Release; onClose: () => void }

export function ReleaseDraftDialog(props: Props) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const form = useForm<ReleaseDraftInput>({
    resolver: zodResolver(releaseDraftSchema(t)),
    defaultValues: {
      version: props.release?.version ?? '',
      build_id: props.release?.build_id ?? '',
      title: props.release?.title ?? '',
      notes: props.release?.notes ?? '',
      minimum_system_version: props.release?.minimum_system_version ?? '',
    },
  })
  const mutation = useMutation({
    mutationFn: (input: ReleaseDraftInput) =>
      saveRelease(props.release?.id, input, props.release?.revision),
    retry: false,
    meta: { errorToast: false },
    onSuccess: async () => {
      if (currentReleaseIdentity() !== props.identity) return
      await queryClient.invalidateQueries({ queryKey: ['bellobuddy-releases'] })
      await queryClient.invalidateQueries({ queryKey: ['bellobuddy-release'] })
      props.onClose()
    },
  })
  return (
    <Dialog
      open
      title={
        props.release ? t('Edit release draft') : t('Create release draft')
      }
      description={t('Files are private until validated and published.')}
      showCloseButton={!mutation.isPending}
      onOpenChange={(open) => {
        if (!open && !mutation.isPending) props.onClose()
      }}
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
            form='bellobuddy-release-draft'
            type='submit'
            disabled={mutation.isPending}
          >
            {t('Save')}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <form
          id='bellobuddy-release-draft'
          onSubmit={form.handleSubmit((input) => mutation.mutate(input))}
        >
          <FieldGroup>
            <Field data-invalid={!!form.formState.errors.version}>
              <FieldLabel htmlFor='release-version'>{t('Version')}</FieldLabel>
              <Input
                id='release-version'
                placeholder='3.0.29'
                readOnly={!!props.release}
                disabled={mutation.isPending}
                aria-invalid={!!form.formState.errors.version}
                {...form.register('version')}
              />
              <FieldError errors={[form.formState.errors.version]} />
            </Field>
            <Field data-invalid={!!form.formState.errors.build_id}>
              <FieldLabel htmlFor='release-build'>
                {t('Build identifier')}
              </FieldLabel>
              <Input
                id='release-build'
                disabled={mutation.isPending}
                aria-invalid={!!form.formState.errors.build_id}
                {...form.register('build_id')}
              />
              <FieldError errors={[form.formState.errors.build_id]} />
            </Field>
            <Field data-invalid={!!form.formState.errors.title}>
              <FieldLabel htmlFor='release-title'>{t('Title')}</FieldLabel>
              <Input
                id='release-title'
                disabled={mutation.isPending}
                aria-invalid={!!form.formState.errors.title}
                {...form.register('title')}
              />
              <FieldError errors={[form.formState.errors.title]} />
            </Field>
            <Field data-invalid={!!form.formState.errors.notes}>
              <FieldLabel htmlFor='release-notes'>
                {t('Release notes')}
              </FieldLabel>
              <Textarea
                id='release-notes'
                rows={5}
                disabled={mutation.isPending}
                aria-invalid={!!form.formState.errors.notes}
                {...form.register('notes')}
              />
              <FieldError errors={[form.formState.errors.notes]} />
            </Field>
            <Field
              data-invalid={!!form.formState.errors.minimum_system_version}
            >
              <FieldLabel htmlFor='release-minimum-system'>
                {t('Minimum Windows system version')}
              </FieldLabel>
              <Input
                id='release-minimum-system'
                placeholder='10.0.19045.0'
                disabled={mutation.isPending}
                aria-invalid={!!form.formState.errors.minimum_system_version}
                {...form.register('minimum_system_version')}
              />
              <FieldError
                errors={[form.formState.errors.minimum_system_version]}
              />
            </Field>
          </FieldGroup>
        </form>
      </Form>
      {mutation.isError && (
        <Alert variant='destructive' className='mt-4'>
          <AlertDescription>
            {getServerErrorMessage(mutation.error)}
          </AlertDescription>
        </Alert>
      )}
    </Dialog>
  )
}
