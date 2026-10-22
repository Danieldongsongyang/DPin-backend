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
export type PlanKind = 'communication' | 'filter'

export type PlanMetadata = {
  id: number
  kind: PlanKind
  name: string
  visibility: 'public' | 'private'
  owner_id: number
  owner_username: string
  enabled: boolean
  version: number
  schema_version: number
  filter_plan_id?: number
  created_at: number
  updated_at: number
}

export type PlanDetail = PlanMetadata & {
  content: Record<string, unknown>
  content_json: string
}

export type PlanPage = {
  items: PlanMetadata[]
  total: number
  page: number
  page_size: number
}

export type PlanFilters = {
  keyword: string
  owner: string
  visibility: 'all' | 'public' | 'private'
  enabled: 'all' | 'true' | 'false'
}

export type PlanListInput = {
  kind: PlanKind
  page: number
  page_size: number
  keyword?: string
  owner?: string
  visibility?: 'public' | 'private'
  enabled?: 'true' | 'false'
  for_plan_id?: number
}

export const EMPTY_PLAN_FILTERS: PlanFilters = {
  keyword: '',
  owner: '',
  visibility: 'all',
  enabled: 'all',
}
