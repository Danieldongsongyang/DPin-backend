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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataTablePage, useDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { getServerErrorStatus } from '@/lib/server-error-message'

import {
  listJobCandidates,
  mapJobsToBinding,
  type JobBinding,
  type JobCandidate,
} from '../api'

type Props = { binding: JobBinding; onClose: () => void }

export function MappingDialog(props: Props) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [city, setCity] = useState('')
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 50 })
  const [selected, setSelected] = useState<Map<number, JobCandidate>>(new Map())
  const [confirm, setConfirm] = useState(false)
  const [conflict, setConflict] = useState(false)
  const query = useQuery({
    queryKey: ['bellobuddy-candidates', name, city, pagination],
    queryFn: ({ signal }) =>
      listJobCandidates(
        {
          page: pagination.pageIndex + 1,
          page_size: pagination.pageSize,
          name,
          city,
        },
        signal
      ),
    gcTime: 0,
  })
  const mutation = useMutation({
    mutationFn: () => mapJobsToBinding(props.binding, [...selected.values()]),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ['bellobuddy-candidates'],
      })
      void queryClient.invalidateQueries({ queryKey: ['bellobuddy-bindings'] })
      props.onClose()
    },
    onError: (error) => {
      if (getServerErrorStatus(error) !== 409) return
      setConflict(true)
      setConfirm(false)
      setSelected(new Map())
      void queryClient.invalidateQueries({ queryKey: ['bellobuddy-bindings'] })
      void query.refetch()
    },
  })
  const columns: ColumnDef<JobCandidate, unknown>[] = [
    {
      id: 'select',
      header: t('Select'),
      cell: ({ row }) => (
        <Checkbox
          aria-label={t('Select job {{job}} for user {{user}}', {
            job: row.original.job_id,
            user: row.original.user_id,
          })}
          checked={selected.has(row.original.job_config_id)}
          disabled={
            mutation.isPending ||
            query.isFetching ||
            (!selected.has(row.original.job_config_id) && selected.size >= 200)
          }
          onCheckedChange={(checked) =>
            setSelected((previous) => {
              const next = new Map(previous)
              if (checked) next.set(row.original.job_config_id, row.original)
              else next.delete(row.original.job_config_id)
              return next
            })
          }
        />
      ),
    },
    { accessorKey: 'name', header: t('Job name') },
    { accessorKey: 'city', header: t('City') },
    { accessorKey: 'account_name', header: t('Account') },
    {
      accessorKey: 'user_id',
      header: t('User ID'),
      cell: ({ row }) => formatNumber(row.original.user_id, locale),
    },
    { accessorKey: 'job_id', header: t('Job ID') },
    {
      accessorKey: 'binding_id',
      header: t('Current robot binding'),
      cell: ({ row }) =>
        row.original.binding_id
          ? formatNumber(row.original.binding_id, locale)
          : t('Not configured'),
    },
  ]
  const { table } = useDataTable({
    data: query.data?.items ?? [],
    columns,
    pagination,
    onPaginationChange: setPagination,
    totalCount: query.data?.total ?? 0,
    manualPagination: true,
    manualSorting: true,
    enableSorting: false,
    getRowId: (row) => String(row.job_config_id),
  })
  return (
    <>
      <Dialog
        open
        onOpenChange={(open) => {
          if (!open && !mutation.isPending) props.onClose()
        }}
        title={t('Assign jobs to {{binding}}', { binding: props.binding.name })}
        description={t('Select the exact jobs that should share this robot')}
        contentClassName='sm:max-w-5xl'
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
              disabled={
                conflict ||
                !selected.size ||
                mutation.isPending ||
                query.isFetching
              }
              onClick={() => setConfirm(true)}
            >
              {t('Review assignment')} ({formatNumber(selected.size, locale)})
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
        <FieldGroup className='mb-4 sm:grid sm:grid-cols-2'>
          <Field>
            <FieldLabel htmlFor='job-name-filter'>{t('Job name')}</FieldLabel>
            <Input
              id='job-name-filter'
              value={name}
              disabled={mutation.isPending}
              onChange={(event) => {
                setName(event.target.value)
                setPagination((previous) => ({ ...previous, pageIndex: 0 }))
              }}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor='job-city-filter'>{t('City')}</FieldLabel>
            <Input
              id='job-city-filter'
              value={city}
              disabled={mutation.isPending}
              onChange={(event) => {
                setCity(event.target.value)
                setPagination((previous) => ({ ...previous, pageIndex: 0 }))
              }}
            />
          </Field>
        </FieldGroup>
        {query.isError ? (
          <ErrorState
            onRetry={() => {
              void query.refetch()
            }}
          />
        ) : (
          <DataTablePage
            table={table}
            columns={columns}
            fixedHeight={false}
            paginationInFooter={false}
            isLoading={query.isPending}
            isFetching={query.isFetching}
            emptyTitle={t('No jobs found')}
          />
        )}
      </Dialog>
      <ConfirmDialog
        open={confirm}
        onOpenChange={(open) => {
          if (!mutation.isPending) setConfirm(open)
        }}
        title={t('Confirm robot assignment')}
        desc={
          <div className='space-y-2'>
            <p>
              {t('The selected jobs will use {{binding}}', {
                binding: props.binding.name,
              })}
            </p>
            <ul className='max-h-64 list-disc overflow-auto pl-5'>
              {[...selected.values()].map((job) => (
                <li key={job.job_config_id}>
                  {job.name} · {job.city} · {job.account_name} · {t('User ID')}:{' '}
                  {formatNumber(job.user_id, locale)} · {t('Job ID')}:{' '}
                  {job.job_id}
                </li>
              ))}
            </ul>
          </div>
        }
        confirmText={t('Assign robot')}
        isLoading={mutation.isPending}
        handleConfirm={() => mutation.mutate()}
      />
    </>
  )
}
