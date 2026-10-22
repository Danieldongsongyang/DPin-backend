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
import { t } from 'i18next'

import { api } from '@/lib/api'
import {
  getServerErrorStatus,
  requireServerSuccess,
} from '@/lib/server-error-message'

import type {
  PlanDetail,
  PlanKind,
  PlanListInput,
  PlanMetadata,
  PlanPage,
} from './types'

type Response<T> = { success: boolean; data: T }

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

export function invalidEditorRequest(): Error {
  return Object.assign(new Error(t('Invalid plan editor request')), {
    code: 'INVALID_REQUEST',
  })
}

export function positivePlanID(value: unknown): value is number {
  return Number.isSafeInteger(value) && (value as number) > 0
}

export async function listPlans(
  input: PlanListInput,
  signal?: AbortSignal
): Promise<PlanPage> {
  const response = await api.get<Response<PlanPage>>(
    '/api/bellobuddy/admin/plans',
    {
      params: input,
      signal,
    }
  )
  return requireServerSuccess(response.data).data
}

export async function getPlan(
  id: number,
  kind: PlanKind,
  signal?: AbortSignal,
  forPlanID?: number
): Promise<PlanDetail> {
  const response = await api.get<Response<PlanDetail>>(
    `/api/bellobuddy/admin/plans/${id}`,
    {
      params: forPlanID === undefined ? undefined : { for_plan_id: forPlanID },
      signal,
    }
  )
  const detail = requireServerSuccess(response.data).data
  if (
    detail.id !== id ||
    detail.kind !== kind ||
    typeof detail.content_json !== 'string' ||
    !isRecord(detail.content)
  ) {
    throw invalidEditorRequest()
  }
  return detail
}

export async function savePlan(
  plan: PlanMetadata,
  payload: Record<string, unknown>,
  signal?: AbortSignal
): Promise<PlanDetail> {
  if (
    payload.id !== plan.id ||
    payload.kind !== plan.kind ||
    payload.schema_version !== plan.schema_version ||
    !positivePlanID(payload.expected_version) ||
    typeof payload.content_json !== 'string'
  ) {
    throw invalidEditorRequest()
  }
  let content: unknown
  try {
    content = JSON.parse(payload.content_json)
  } catch {
    throw invalidEditorRequest()
  }
  if (!isRecord(content)) throw invalidEditorRequest()
  const name = content[plan.kind === 'communication' ? 'scheme_name' : 'name']
  if (
    typeof name !== 'string' ||
    !name.trim() ||
    (content.disabled !== undefined && typeof content.disabled !== 'boolean')
  ) {
    throw invalidEditorRequest()
  }
  let filterPlanID = 0
  if (plan.kind === 'communication') {
    if (
      !Number.isSafeInteger(payload.filter_plan_id) ||
      (payload.filter_plan_id as number) < 0
    ) {
      throw invalidEditorRequest()
    }
    filterPlanID = payload.filter_plan_id as number
  }
  // Only extract typed metadata. The original body string keeps unknown values and large integers intact.
  let response
  try {
    response = await api.put<Response<PlanDetail>>(
      `/api/bellobuddy/admin/plans/${plan.id}`,
      {
        kind: plan.kind,
        name,
        enabled:
          content.disabled === undefined ? plan.enabled : !content.disabled,
        schema_version: plan.schema_version,
        expected_version: payload.expected_version,
        filter_plan_id: filterPlanID,
        content_json: payload.content_json,
      },
      { skipAuthRefresh: true, skipErrorHandler: true, signal }
    )
  } catch (error) {
    const status = getServerErrorStatus(error)
    if (status === undefined || status >= 500) {
      throw Object.assign(
        new Error(
          t('Save result is unconfirmed. Reload the plan before saving again.')
        ),
        { code: 'SAVE_UNCONFIRMED' }
      )
    }
    throw error
  }
  if (
    !isRecord(response.data) ||
    (response.data.success !== true && response.data.success !== false)
  ) {
    throw Object.assign(
      new Error(
        t('Save result is unconfirmed. Reload the plan before saving again.')
      ),
      { code: 'INVALID_SAVE_RESPONSE' }
    )
  }
  const saved = requireServerSuccess(response.data).data
  if (
    !saved ||
    saved.id !== plan.id ||
    saved.kind !== plan.kind ||
    typeof saved.content_json !== 'string' ||
    !isRecord(saved.content)
  ) {
    throw Object.assign(
      new Error(
        t('Save result is unconfirmed. Reload the plan before saving again.')
      ),
      { code: 'INVALID_SAVE_RESPONSE' }
    )
  }
  return saved
}

export async function listFilterCandidates(
  planID: number,
  signal: AbortSignal
): Promise<PlanMetadata[]> {
  const items: PlanMetadata[] = []
  for (let page = 1; ; page++) {
    const result = await listPlans(
      { kind: 'filter', for_plan_id: planID, page, page_size: 100 },
      signal
    )
    items.push(...result.items)
    if (items.length >= result.total || result.items.length === 0) return items
  }
}
