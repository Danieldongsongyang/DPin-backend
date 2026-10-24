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
import { nanoid } from 'nanoid'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Field, FieldLabel } from '@/components/ui/field'
import { Textarea } from '@/components/ui/textarea'
import { toIntlLocale } from '@/i18n/languages'
import { formatDateTimeStr, formatNumber } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { currentReleaseIdentity } from '../access'
import { operateRelease } from '../api'
import type {
  ReleaseAccess,
  ReleaseAction,
  ReleaseDetail,
  ReleaseOperation,
} from '../types'

type OperationRequest = {
  action: ReleaseAction
  revision: number
  expected_channel_revision: number
  idempotency_key: string
  reason: string | null
  fromVersion: string
}
type Props = ReleaseAccess & {
  detail: ReleaseDetail
  busy: boolean
  onBusy: (busy: boolean) => void
}

export function ReleaseOperations(props: Props) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const queryClient = useQueryClient()
  const [request, setRequest] = useState<OperationRequest | null>(null)
  const [reason, setReason] = useState('')
  const release = props.detail.release
  const channel = props.detail.channel
  const mutation = useMutation({
    mutationFn: (input: OperationRequest & { reason: string }) => {
      props.onBusy(true)
      return operateRelease(release.id, input.action, {
        revision: input.revision,
        expected_channel_revision: input.expected_channel_revision,
        idempotency_key: input.idempotency_key,
        reason: input.reason,
      })
    },
    retry: false,
    meta: { errorToast: false },
    onSuccess: () => {
      if (currentReleaseIdentity() === props.identity) setRequest(null)
    },
    onSettled: async () => {
      if (currentReleaseIdentity() !== props.identity) return
      await queryClient.invalidateQueries({
        queryKey: ['bellobuddy-release', props.identity, release.id],
      })
      await queryClient.invalidateQueries({ queryKey: ['bellobuddy-releases'] })
      props.onBusy(false)
    },
  })
  const open = (action: ReleaseAction) => {
    mutation.reset()
    setReason('')
    setRequest({
      action,
      revision: release.revision,
      expected_channel_revision: channel.revision,
      idempotency_key: nanoid(32),
      fromVersion:
        channel.version ||
        (channel.release_id ? `#${channel.release_id}` : t('None')),
      reason: null,
    })
  }
  let title = t('Publish release')
  let description = t(
    'The stable channel will switch from {{from}} to {{to}}. The server will verify frozen files, acceptance evidence and channel revision before publication.',
    {
      from:
        request?.fromVersion ??
        channel.version ??
        (channel.release_id ? `#${channel.release_id}` : t('None')),
      to: release.version,
    }
  )
  if (request?.action === 'withdraw') {
    title = t('Withdraw release')
    description = t(
      'Stop recommending this version. Existing downloads remain available unless the version is emergency disabled. No automatic downgrade will occur.'
    )
  }
  if (request?.action === 'disable') {
    title = t('Emergency disable release')
    description = t(
      'Stop recommending this version and deny new download and installation requests. This does not remove already installed software.'
    )
  }
  const columns: StaticDataTableColumn<ReleaseOperation>[] = [
    {
      id: 'action',
      header: t('Action'),
      cell: (operation) => {
        if (operation.action === 'publish') return t('Publish release')
        if (operation.action === 'withdraw') return t('Withdraw release')
        if (operation.action === 'disable') {
          return t('Emergency disable release')
        }
        return operation.action
      },
    },
    {
      id: 'actor',
      header: t('Operator'),
      cell: (operation) => formatNumber(operation.actor_id, locale),
    },
    {
      id: 'time',
      header: t('Time'),
      cell: (operation) =>
        formatDateTimeStr(new Date(operation.created_at * 1000)),
    },
    {
      id: 'reason',
      header: t('Reason'),
      cell: (operation) => operation.reason || '-',
    },
  ]
  const frozen = ['ready', 'published', 'withdrawn'].includes(release.state)
  return (
    <section className='space-y-3'>
      <h2 className='font-semibold'>{t('Publication')}</h2>
      <div className='flex flex-wrap gap-2'>
        {props.canPublish && (
          <Button
            disabled={
              props.busy ||
              !frozen ||
              release.disabled ||
              channel.release_id === release.id
            }
            onClick={() => open('publish')}
          >
            {t('Publish release')}
          </Button>
        )}
        {props.canWithdraw && (
          <Button
            variant='outline'
            disabled={
              props.busy ||
              channel.release_id !== release.id ||
              release.disabled
            }
            onClick={() => open('withdraw')}
          >
            {t('Withdraw release')}
          </Button>
        )}
        {props.canWithdraw && (
          <Button
            variant='destructive'
            disabled={props.busy || !frozen || release.disabled}
            onClick={() => open('disable')}
          >
            {t('Emergency disable release')}
          </Button>
        )}
      </div>
      <p className='text-muted-foreground text-sm'>
        {t(
          'File validation does not mean formal publication is approved. Unsigned internal packages cannot be published to the stable channel.'
        )}
      </p>
      <h3 className='pt-2 text-sm font-medium'>
        {t('Release operation history')}
      </h3>
      <StaticDataTable
        columns={columns}
        data={props.detail.operations}
        getRowKey={(operation) => operation.id}
        emptyContent={t('No release operations yet')}
      />
      <ConfirmDialog
        open={request !== null}
        onOpenChange={(openValue) => {
          if (!openValue && !mutation.isPending) setRequest(null)
        }}
        title={title}
        desc={description}
        confirmText={title}
        destructive={request?.action !== 'publish'}
        isLoading={mutation.isPending}
        disabled={
          request?.action !== 'publish' && !(request?.reason ?? reason).trim()
        }
        handleConfirm={() => {
          if (!request) return
          const snapshot = {
            ...request,
            reason: request.reason ?? reason.trim(),
          }
          setRequest(snapshot)
          mutation.mutate(snapshot)
        }}
      >
        {request?.action !== 'publish' && (
          <Field>
            <FieldLabel htmlFor='release-operation-reason'>
              {t('Reason')}
            </FieldLabel>
            <Textarea
              id='release-operation-reason'
              rows={3}
              maxLength={512}
              value={request?.reason ?? reason}
              disabled={mutation.isPending || request?.reason !== null}
              onChange={(event) => setReason(event.target.value)}
            />
          </Field>
        )}
        {mutation.isError && (
          <Alert variant='destructive'>
            <AlertDescription className='space-y-2'>
              <p>{getServerErrorMessage(mutation.error)}</p>
              <p>
                {t(
                  'The result may already be recorded. Retrying in this dialog uses the same operation key; close and review the latest state before starting a different operation.'
                )}
              </p>
            </AlertDescription>
          </Alert>
        )}
      </ConfirmDialog>
    </section>
  )
}
