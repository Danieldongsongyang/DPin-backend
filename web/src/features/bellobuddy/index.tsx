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
import type { ColumnDef } from '@tanstack/react-table'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { hasPermission } from '@/lib/admin-permissions'
import { formatNumber } from '@/lib/format'
import { useAuthStore } from '@/stores/auth-store'

import { listJobBindings, type JobBinding } from './api'
import { BindingDialog } from './components/binding-dialog'
import { MappingDialog } from './components/mapping-dialog'

export function BelloBuddyBindings() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const user = useAuthStore((state) => state.auth.user)
  const canRead = hasPermission(user, 'bellobuddy_job_binding', 'read')
  const canWrite = hasPermission(user, 'bellobuddy_job_binding', 'write')
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 50 })
  const [editing, setEditing] = useState<{ binding: JobBinding | null } | null>(
    null
  )
  const [mapping, setMapping] = useState<JobBinding | null>(null)
  const query = useQuery({
    queryKey: ['bellobuddy-bindings', pagination],
    queryFn: ({ signal }) =>
      listJobBindings(pagination.pageIndex + 1, pagination.pageSize, signal),
    enabled: canRead,
    gcTime: 0,
  })
  const columns: ColumnDef<JobBinding, unknown>[] = [
    { accessorKey: 'name', header: t('Name') },
    { accessorKey: 'shared_key', header: t('Shared job key') },
    {
      accessorKey: 'channel_id',
      header: t('Channel ID'),
      cell: ({ row }) => formatNumber(row.original.channel_id, locale),
    },
    { accessorKey: 'model', header: t('Logical model') },
    {
      accessorKey: 'enabled',
      header: t('Status'),
      cell: ({ row }) => (
        <Badge variant={row.original.enabled ? 'default' : 'secondary'}>
          {row.original.enabled ? t('Enabled') : t('Disabled')}
        </Badge>
      ),
    },
    {
      accessorKey: 'version',
      header: t('Version'),
      cell: ({ row }) => formatNumber(row.original.version, locale),
    },
    {
      id: 'actions',
      header: t('Actions'),
      cell: ({ row }) => (
        <div className='flex flex-wrap gap-2'>
          <Button
            variant='outline'
            size='sm'
            disabled={!canWrite || query.isFetching}
            onClick={() => setEditing({ binding: row.original })}
          >
            {t('Edit')}
          </Button>
          <Button
            variant='outline'
            size='sm'
            disabled={!canWrite || query.isFetching}
            onClick={() => setMapping(row.original)}
          >
            {t('Assign jobs')}
          </Button>
        </div>
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
    enableSorting: false,
    getRowId: (row) => String(row.id),
  })
  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>
          {t('BelloBuddy robot bindings')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button
            disabled={!canWrite}
            onClick={() => setEditing({ binding: null })}
          >
            {t('Create robot binding')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          {query.isError || !canRead ? (
            <ErrorState
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
              emptyTitle={t('No robot bindings found')}
            />
          )}
        </SectionPageLayout.Content>
      </SectionPageLayout>
      {editing && (
        <BindingDialog
          binding={editing.binding}
          onClose={() => setEditing(null)}
        />
      )}
      {mapping && (
        <MappingDialog binding={mapping} onClose={() => setMapping(null)} />
      )}
    </>
  )
}
