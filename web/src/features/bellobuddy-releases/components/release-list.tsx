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
import type { ColumnDef, PaginationState } from '@tanstack/react-table'
import { Plus, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  DataTablePage,
  DataTableToolbar,
  TruncatedCell,
  useDataTable,
} from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { formatDateTimeStr, formatNumber } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { listReleases } from '../api'
import type { Release, ReleaseAccess } from '../types'
import { ReleaseDraftDialog } from './release-draft-dialog'
import { ReleaseStateBadge } from './release-state'

type Props = ReleaseAccess & { onOpen: (id: number) => void }

export function ReleaseList(props: Props) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: 20,
  })
  const [creating, setCreating] = useState(false)
  const query = useQuery({
    queryKey: ['bellobuddy-releases', props.identity, pagination.pageIndex],
    queryFn: ({ signal }) => listReleases(pagination.pageIndex + 1, signal),
    gcTime: 0,
    meta: { errorToast: false },
  })
  const columns: ColumnDef<Release, unknown>[] = [
    {
      accessorKey: 'version',
      header: t('Version'),
      cell: ({ row }) => (
        <span className='font-mono'>{row.original.version}</span>
      ),
    },
    {
      accessorKey: 'title',
      header: t('Title'),
      cell: ({ row }) => <TruncatedCell>{row.original.title}</TruncatedCell>,
    },
    {
      accessorKey: 'build_id',
      header: t('Build identifier'),
      cell: ({ row }) => <TruncatedCell>{row.original.build_id}</TruncatedCell>,
    },
    {
      accessorKey: 'state',
      header: t('Status'),
      cell: ({ row }) => <ReleaseStateBadge release={row.original} />,
    },
    {
      accessorKey: 'created_by',
      header: t('Created by'),
      cell: ({ row }) => formatNumber(row.original.created_by, locale),
    },
    {
      accessorKey: 'published_at',
      header: t('Published At'),
      cell: ({ row }) =>
        row.original.published_at
          ? formatDateTimeStr(new Date(row.original.published_at * 1000))
          : '-',
    },
    {
      id: 'actions',
      header: t('Actions'),
      cell: ({ row }) => (
        <Button
          variant='outline'
          size='sm'
          onClick={() => props.onOpen(row.original.id)}
        >
          {t('View')}
        </Button>
      ),
    },
  ]
  const { table } = useDataTable({
    data: query.data?.items ?? [],
    columns,
    totalCount: query.data?.total ?? 0,
    pagination,
    onPaginationChange: setPagination,
    manualPagination: true,
    manualSorting: true,
    manualFiltering: true,
    columnFilters: [],
    globalFilter: '',
    enableSorting: false,
    enableRowSelection: false,
    getRowId: (row) => String(row.id),
  })
  const channel = query.data?.channel
  let target = t('No version is currently recommended')
  if (channel?.release_id) {
    target =
      channel.version ||
      query.data?.items.find((item) => item.id === channel.release_id)
        ?.version ||
      `#${channel.release_id}`
  }
  return (
    <div className='flex h-full min-h-0 flex-col gap-4'>
      {channel && (
        <Alert>
          <AlertTitle>{t('Current stable release')} · Windows x64</AlertTitle>
          <AlertDescription>
            {target} · {t('Channel revision')}:{' '}
            {formatNumber(channel.revision, locale)}
          </AlertDescription>
        </Alert>
      )}
      <DataTableToolbar
        table={table}
        customSearch={<span />}
        hideViewOptions
        preActions={
          <>
            <Button
              variant='outline'
              disabled={query.isFetching}
              onClick={() => {
                void query.refetch()
              }}
            >
              <RefreshCw aria-hidden='true' />
              {t('Refresh')}
            </Button>
            {props.canWrite && (
              <Button onClick={() => setCreating(true)}>
                <Plus aria-hidden='true' />
                {t('Create release draft')}
              </Button>
            )}
          </>
        }
      />
      {query.isError ? (
        <ErrorState
          description={getServerErrorMessage(query.error)}
          onRetry={() => {
            void query.refetch()
          }}
        />
      ) : (
        <DataTablePage
          table={table}
          columns={columns}
          isLoading={query.isPending}
          isFetching={query.isFetching}
          emptyTitle={t('No releases yet')}
          className='min-h-0 flex-1'
        />
      )}
      {creating && (
        <ReleaseDraftDialog
          identity={props.identity}
          onClose={() => setCreating(false)}
        />
      )}
    </div>
  )
}
