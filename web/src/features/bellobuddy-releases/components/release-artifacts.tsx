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
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { EmptyState } from '@/components/empty-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Progress } from '@/components/ui/progress'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { currentReleaseIdentity } from '../access'
import { uploadReleaseArtifact } from '../api'
import type { ArtifactKind, ReleaseArtifact, ReleaseDetail } from '../types'

const kinds: ArtifactKind[] = ['installer', 'blockmap', 'metadata', 'manifest']

export function ReleaseArtifacts(props: {
  identity: string
  detail: ReleaseDetail
  canWrite: boolean
  busy: boolean
  onBusy: (busy: boolean) => void
}) {
  const { t } = useTranslation()
  const uploadLabels: Record<string, string> = {
    uploading: t('Uploading...'),
    complete: t('Completed'),
    failed: t('Failed'),
  }
  return (
    <section className='space-y-4'>
      <h2 className='font-semibold'>{t('Release files')}</h2>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Upload the installer, its blockmap, latest.yml and release-manifest.json from one build.'
        )}
      </p>
      {kinds.map((kind) => (
        <ArtifactUpload
          key={kind}
          kind={kind}
          identity={props.identity}
          detail={props.detail}
          artifact={props.detail.artifacts.find((item) => item.kind === kind)}
          canWrite={props.canWrite}
          busy={props.busy}
          onBusy={props.onBusy}
        />
      ))}
      {props.detail.uploads.length > 0 && (
        <details>
          <summary className='cursor-pointer text-sm font-medium'>
            {t('Upload history')}
          </summary>
          <ul className='mt-2 space-y-2 text-sm'>
            {props.detail.uploads.map((upload) => (
              <li key={upload.id} className='break-all'>
                {upload.filename} · {uploadLabels[upload.state] ?? upload.state}
                {upload.code ? ` · ${upload.code}` : ''}
              </li>
            ))}
          </ul>
        </details>
      )}
      {props.detail.artifacts.length === 0 &&
        props.detail.release.state !== 'draft' && (
          <EmptyState title={t('No release files')} />
        )}
    </section>
  )
}

function ArtifactUpload(props: {
  identity: string
  detail: ReleaseDetail
  kind: ArtifactKind
  artifact?: ReleaseArtifact
  canWrite: boolean
  busy: boolean
  onBusy: (busy: boolean) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const queryClient = useQueryClient()
  const [file, setFile] = useState<File | null>(null)
  const [progress, setProgress] = useState(0)
  const controller = useRef<AbortController | null>(null)
  useEffect(() => () => controller.current?.abort(), [])
  const mutation = useMutation({
    mutationFn: (selected: File) => {
      const abort = new AbortController()
      controller.current = abort
      props.onBusy(true)
      setProgress(0)
      return uploadReleaseArtifact(
        props.detail.release.id,
        props.detail.release.revision,
        props.kind,
        selected,
        abort.signal,
        setProgress
      )
    },
    retry: false,
    meta: { errorToast: false },
    onSuccess: () => {
      if (currentReleaseIdentity() === props.identity) setFile(null)
    },
    onSettled: async () => {
      if (currentReleaseIdentity() !== props.identity) return
      await queryClient.invalidateQueries({
        queryKey: [
          'bellobuddy-release',
          props.identity,
          props.detail.release.id,
        ],
      })
      await queryClient.invalidateQueries({ queryKey: ['bellobuddy-releases'] })
      props.onBusy(false)
    },
  })
  const labels: Record<ArtifactKind, string> = {
    installer: t('Windows installer'),
    blockmap: t('Installer blockmap'),
    metadata: t('Update metadata'),
    manifest: t('Build manifest'),
  }
  const accepts: Record<ArtifactKind, string> = {
    installer: '.exe',
    blockmap: '.blockmap',
    metadata: '.yml,.yaml',
    manifest: '.json',
  }
  const editable = props.canWrite && props.detail.release.state === 'draft'
  return (
    <div className='space-y-2 rounded-lg border p-3'>
      <div className='flex flex-wrap items-start justify-between gap-2'>
        <div className='min-w-0'>
          <h3 className='text-sm font-medium'>{labels[props.kind]}</h3>
          {props.artifact ? (
            <p className='text-muted-foreground mt-1 text-xs break-all'>
              {props.artifact.filename} ·{' '}
              {formatNumber(props.artifact.size / 1024 / 1024, locale)} MiB
            </p>
          ) : (
            <p className='text-muted-foreground text-xs'>{t('Not uploaded')}</p>
          )}
        </div>
        {props.artifact && (
          <CopyButton
            value={props.artifact.sha256}
            tooltip={`${t('Copy')} SHA256`}
          />
        )}
      </div>
      {editable && (
        <div className='flex flex-wrap items-end gap-2'>
          <Field className='min-w-0 flex-1'>
            <FieldLabel
              htmlFor={`release-file-${props.kind}`}
              className='sr-only'
            >
              {labels[props.kind]}
            </FieldLabel>
            <Input
              id={`release-file-${props.kind}`}
              type='file'
              accept={accepts[props.kind]}
              disabled={props.busy}
              onChange={(event) => {
                setFile(event.target.files?.[0] ?? null)
                event.target.value = ''
              }}
            />
          </Field>
          <Button
            variant='outline'
            disabled={!file || props.busy}
            onClick={() => {
              if (file) mutation.mutate(file)
            }}
          >
            {t('Upload')}
          </Button>
          {mutation.isPending && (
            <Button
              variant='outline'
              onClick={() => controller.current?.abort()}
            >
              {t('Cancel upload')}
            </Button>
          )}
        </div>
      )}
      {file && (
        <p className='text-muted-foreground text-xs break-all'>
          {t('Selected file')}: {file.name}
        </p>
      )}
      {mutation.isPending && (
        <div role='status' className='space-y-1'>
          <Progress value={progress} aria-label={t('Upload progress')} />
          <p className='text-muted-foreground text-xs'>
            {progress === 100
              ? t('Transfer complete, waiting for server validation')
              : t('Uploading release file')}{' '}
            {formatNumber(progress, locale)}%
          </p>
        </div>
      )}
      {mutation.isError && (
        <Alert variant='destructive'>
          <AlertDescription>
            {getServerErrorMessage(mutation.error)}
          </AlertDescription>
        </Alert>
      )}
    </div>
  )
}
