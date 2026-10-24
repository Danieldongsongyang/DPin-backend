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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { currentReleaseIdentity } from '../access'
import { recoverReleaseValidation, validateRelease } from '../api'
import type { Release } from '../types'

type Props = {
  identity: string
  release: Release
  artifactCount: number
  canWrite: boolean
  busy: boolean
  onBusy: (busy: boolean) => void
}

export function ReleaseValidation(props: Props) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [recovering, setRecovering] = useState(false)
  const mutation = useMutation({
    mutationFn: (action: 'validate' | 'recover') => {
      props.onBusy(true)
      if (action === 'recover') {
        return recoverReleaseValidation(
          props.release.id,
          props.release.revision
        )
      }
      return validateRelease(props.release.id, props.release.revision)
    },
    retry: false,
    meta: { errorToast: false },
    onSuccess: () => {
      if (currentReleaseIdentity() === props.identity) setRecovering(false)
    },
    onSettled: async () => {
      if (currentReleaseIdentity() !== props.identity) return
      await queryClient.invalidateQueries({
        queryKey: ['bellobuddy-release', props.identity, props.release.id],
      })
      await queryClient.invalidateQueries({ queryKey: ['bellobuddy-releases'] })
      props.onBusy(false)
    },
  })
  return (
    <section className='space-y-3'>
      {props.canWrite && props.release.state === 'draft' && (
        <Button
          disabled={props.busy || props.artifactCount !== 4}
          onClick={() => mutation.mutate('validate')}
        >
          {mutation.isPending
            ? t('Validating release files')
            : t('Validate and freeze files')}
        </Button>
      )}
      {props.canWrite && props.release.state === 'validating' && (
        <div className='space-y-2'>
          <Button
            variant='outline'
            disabled={props.busy}
            onClick={() => setRecovering(true)}
          >
            {t('Recover interrupted validation')}
          </Button>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Recovery is available only when validation has been in progress for at least 10 minutes. The server enforces this limit.'
            )}
          </p>
        </div>
      )}
      {mutation.isError && !recovering && (
        <Alert variant='destructive'>
          <AlertDescription>
            {getServerErrorMessage(mutation.error)}
          </AlertDescription>
        </Alert>
      )}
      <ConfirmDialog
        open={recovering}
        onOpenChange={(open) => {
          if (!mutation.isPending) setRecovering(open)
        }}
        title={t('Recover interrupted validation')}
        desc={t(
          'Return this version to draft so its files can be validated again. The server permits recovery only after validation has been in progress for at least 10 minutes.'
        )}
        confirmText={t('Recover interrupted validation')}
        isLoading={mutation.isPending}
        handleConfirm={() => mutation.mutate('recover')}
      >
        {mutation.isError && (
          <Alert variant='destructive'>
            <AlertDescription>
              {getServerErrorMessage(mutation.error)}
            </AlertDescription>
          </Alert>
        )}
      </ConfirmDialog>
    </section>
  )
}
