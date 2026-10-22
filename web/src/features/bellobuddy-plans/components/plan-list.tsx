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
import { RefreshCw } from 'lucide-react'
import type { Dispatch, SetStateAction } from 'react'
import { useTranslation } from 'react-i18next'

import {
  DataTablePage,
  DataTableToolbar,
  TruncatedCell,
  useDataTable,
} from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { toIntlLocale } from '@/i18n/languages'
import { formatDateTimeStr, formatNumber } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { listPlans } from '../api'
import {
  EMPTY_PLAN_FILTERS,
  type PlanFilters,
  type PlanKind,
  type PlanMetadata,
} from '../types'
import { PlanFilterInputs } from './plan-filters'

export type PlanBrowserState = {
  kind: PlanKind
  draft: PlanFilters
  filters: PlanFilters
  pagination: PaginationState
}
type Props = {
  identity: string
  state: PlanBrowserState
  setState: Dispatch<SetStateAction<PlanBrowserState>>
  allOwners: boolean
  canEdit: boolean
  onOpen: (plan: PlanMetadata) => void
}

export function PlanList(props: Props) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const state = props.state
  const query = useQuery({
    queryKey: [
      'bellobuddy-admin-plans',
      props.identity,
      state.kind,
      state.filters,
      state.pagination,
    ],
    queryFn: ({ signal }) =>
      listPlans(
        {
          kind: state.kind,
          page: state.pagination.pageIndex + 1,
          page_size: state.pagination.pageSize,
          keyword: state.filters.keyword || undefined,
          owner: state.filters.owner || undefined,
          visibility:
            state.filters.visibility === 'all'
              ? undefined
              : state.filters.visibility,
          enabled:
            state.filters.enabled === 'all' ? undefined : state.filters.enabled,
        },
        signal
      ),
    gcTime: 0,
  })
  const columns: ColumnDef<PlanMetadata, unknown>[] = [
    {
      accessorKey: 'id',
      header: t('ID'),
      cell: ({ row }) => formatNumber(row.original.id, locale),
    },
    {
      accessorKey: 'name',
      header: t('Name'),
      cell: ({ row }) => <TruncatedCell>{row.original.name}</TruncatedCell>,
    },
    {
      accessorKey: 'owner_id',
      header: t('Owner'),
      cell: ({ row }) => {
        if (row.original.owner_id === 0) return t('Platform')
        return (
          <span className='break-all'>
            {row.original.owner_username || t('User')}{' '}
            <span className='text-muted-foreground'>
              #{row.original.owner_id}
            </span>
          </span>
        )
      },
    },
    {
      accessorKey: 'visibility',
      header: t('Plan visibility'),
      cell: ({ row }) => (
        <Badge variant='outline'>
          {row.original.visibility === 'private' ? t('Private') : t('Public')}
        </Badge>
      ),
    },
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
      accessorKey: 'updated_at',
      header: t('Updated At'),
      cell: ({ row }) =>
        row.original.updated_at
          ? formatDateTimeStr(new Date(row.original.updated_at * 1000))
          : '-',
    },
    {
      id: 'actions',
      header: t('Actions'),
      cell: ({ row }) => (
        <Button
          variant='outline'
          size='sm'
          disabled={query.isFetching}
          onClick={() => props.onOpen(row.original)}
        >
          {props.canEdit ? t('View / Edit') : t('View')}
        </Button>
      ),
    },
  ]
  const { table } = useDataTable({
    data: query.data?.items ?? [],
    columns,
    totalCount: query.data?.total ?? 0,
    pagination: state.pagination,
    onPaginationChange: (update) =>
      props.setState((previous) => ({
        ...previous,
        pagination:
          typeof update === 'function' ? update(previous.pagination) : update,
      })),
    manualPagination: true,
    manualSorting: true,
    manualFiltering: true,
    columnFilters: [],
    globalFilter: '',
    enableSorting: false,
    enableRowSelection: false,
    getRowId: (row) => String(row.id),
  })
  const search = () =>
    props.setState((previous) => ({
      ...previous,
      filters: {
        ...previous.draft,
        keyword: previous.draft.keyword.trim(),
        owner: previous.draft.owner.trim(),
      },
      pagination: { ...previous.pagination, pageIndex: 0 },
    }))
  const reset = () =>
    props.setState((previous) => ({
      ...previous,
      draft: EMPTY_PLAN_FILTERS,
      filters: EMPTY_PLAN_FILTERS,
      pagination: { ...previous.pagination, pageIndex: 0 },
    }))
  const hasFilters = Object.entries(state.draft).some(
    ([key, value]) => value !== EMPTY_PLAN_FILTERS[key as keyof PlanFilters]
  )
  return (
    <div className='flex h-full min-h-0 flex-col gap-3'>
      <Tabs
        value={state.kind}
        onValueChange={(value) => {
          if (value === 'communication' || value === 'filter') {
            props.setState((previous) => ({
              ...previous,
              kind: value,
              pagination: { ...previous.pagination, pageIndex: 0 },
            }))
          }
        }}
      >
        <TabsList aria-label={t('Plan type')}>
          <TabsTrigger value='communication'>
            {t('Communication plans')}
          </TabsTrigger>
          <TabsTrigger value='filter'>{t('Screening plans')}</TabsTrigger>
        </TabsList>
      </Tabs>
      <DataTableToolbar
        table={table}
        customSearch={
          <PlanFilterInputs
            value={state.draft}
            allOwners={props.allOwners}
            onSearch={search}
            onChange={(draft) =>
              props.setState((previous) => ({ ...previous, draft }))
            }
          />
        }
        hasAdditionalFilters={hasFilters}
        onSearch={search}
        onReset={reset}
        searchLoading={query.isFetching}
        preActions={
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
        }
        hideViewOptions
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
          emptyTitle={t('No plans found')}
          className='min-h-0 flex-1'
        />
      )}
    </div>
  )
}
