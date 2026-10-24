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
import { ArrowLeft, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { getRelease } from '../api'
import type { ReleaseAccess } from '../types'
import { ReleaseAcceptance } from './release-acceptance'
import { ReleaseArtifacts } from './release-artifacts'
import { ReleaseDraftDialog } from './release-draft-dialog'
import { ReleaseOperations } from './release-operations'
import { ReleaseStateBadge } from './release-state'
import { ReleaseValidation } from './release-validation'

type Props = ReleaseAccess & { id: number; onBack: () => void }

export function ReleaseDetailView(props: Props) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [editing, setEditing] = useState(false)
  const [working, setWorking] = useState(false)
  const query = useQuery({
    queryKey: ['bellobuddy-release', props.identity, props.id],
    queryFn: ({ signal }) => getRelease(props.id, signal),
    gcTime: 0,
    meta: { errorToast: false },
  })
  if (query.isPending) return <LoadingState />
  if (query.isError) {
    return (
      <ErrorState
        description={getServerErrorMessage(query.error)}
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  }
  const detail = query.data
  const release = detail.release
  const busy = working || query.isFetching
  return (
    <div className='space-y-6 pb-4'>
      <div className='flex flex-wrap items-center gap-2'>
        <Button variant='outline' disabled={busy} onClick={props.onBack}>
          <ArrowLeft aria-hidden='true' />
          {t('Back')}
        </Button>
        <Button
          variant='outline'
          disabled={busy}
          onClick={() => {
            void query.refetch()
          }}
        >
          <RefreshCw aria-hidden='true' />
          {t('Refresh')}
        </Button>
        {props.canWrite && release.state === 'draft' && (
          <Button
            variant='outline'
            disabled={busy}
            onClick={() => setEditing(true)}
          >
            {t('Edit release draft')}
          </Button>
        )}
      </div>
      <header className='space-y-2'>
        <div className='flex flex-wrap items-center gap-3'>
          <h1 className='text-lg font-semibold'>
            {release.version} · {release.title}
          </h1>
          <ReleaseStateBadge release={release} />
        </div>
        <p className='text-muted-foreground text-sm'>
          {t('Build identifier')}: {release.build_id} · {t('Revision')}:{' '}
          {formatNumber(release.revision, locale)} · Windows x64
        </p>
        {release.minimum_system_version && (
          <p className='text-muted-foreground text-sm'>
            {t('Minimum Windows system version')}:{' '}
            {release.minimum_system_version}
          </p>
        )}
        <p className='text-sm whitespace-pre-wrap'>{release.notes}</p>
      </header>
      {release.disabled && (
        <Alert variant='destructive'>
          <AlertDescription>{release.disabled_reason}</AlertDescription>
        </Alert>
      )}
      <ReleaseArtifacts
        identity={props.identity}
        detail={detail}
        canWrite={props.canWrite}
        busy={busy}
        onBusy={setWorking}
      />
      <ReleaseValidation
        identity={props.identity}
        release={release}
        artifactCount={detail.artifacts.length}
        canWrite={props.canWrite}
        busy={busy}
        onBusy={setWorking}
      />
      <ReleaseReport
        title={t('File validation report')}
        report={release.validation_report}
        empty={t('Not validated yet')}
      />
      <ReleaseAcceptance
        identity={props.identity}
        release={release}
        canAccept={props.canPublish}
        busy={busy}
        onBusy={setWorking}
      />
      <ReleaseReport
        title={t('Acceptance report')}
        report={release.acceptance_report}
        empty={t('No trusted acceptance evidence recorded')}
      />
      <ReleaseOperations
        {...props}
        detail={detail}
        busy={busy}
        onBusy={setWorking}
      />
      {editing && (
        <ReleaseDraftDialog
          identity={props.identity}
          release={release}
          onClose={() => setEditing(false)}
        />
      )}
    </div>
  )
}

function ReleaseReport(props: {
  title: string
  report: Record<string, unknown> | null
  empty: string
}) {
  return (
    <section className='space-y-2'>
      <h2 className='font-semibold'>{props.title}</h2>
      {props.report ? (
        <pre className='bg-muted max-h-80 overflow-auto rounded-lg p-3 text-xs break-all whitespace-pre-wrap'>
          {JSON.stringify(props.report, null, 2)}
        </pre>
      ) : (
        <p className='text-muted-foreground text-sm'>{props.empty}</p>
      )}
    </section>
  )
}
