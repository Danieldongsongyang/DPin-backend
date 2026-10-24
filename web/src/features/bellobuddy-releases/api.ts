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
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type {
  AcceptanceEnvelope,
  ArtifactKind,
  ReleaseAction,
  ReleaseDetail,
  ReleaseDraftInput,
  ReleasePage,
} from './types'

type Response<T> = { success: boolean; data: T }
const base = '/api/bellobuddy/admin/releases'

export async function listReleases(
  page: number,
  signal?: AbortSignal
): Promise<ReleasePage> {
  const response = await api.get<Response<ReleasePage>>(base, {
    params: { page, page_size: 20 },
    signal,
  })
  return requireServerSuccess(response.data).data
}

export async function getRelease(
  id: number,
  signal?: AbortSignal
): Promise<ReleaseDetail> {
  const response = await api.get<Response<ReleaseDetail>>(`${base}/${id}`, {
    signal,
  })
  return requireServerSuccess(response.data).data
}

export async function saveRelease(
  id: number | undefined,
  input: ReleaseDraftInput,
  revision?: number
): Promise<unknown> {
  const response =
    id === undefined
      ? await api.post<Response<unknown>>(base, input)
      : await api.put<Response<unknown>>(`${base}/${id}`, {
          ...input,
          revision,
        })
  return requireServerSuccess(response.data).data
}

export async function uploadReleaseArtifact(
  id: number,
  revision: number,
  kind: ArtifactKind,
  file: File,
  signal: AbortSignal,
  onProgress: (percent: number) => void
): Promise<unknown> {
  const form = new FormData()
  form.append('file', file)
  const response = await api.post<Response<unknown>>(
    `${base}/${id}/artifacts/${kind}?revision=${revision}`,
    form,
    {
      signal,
      timeout: 0,
      headers: { 'Content-Type': undefined },
      onUploadProgress: (event) => {
        if (event.total) {
          onProgress(
            Math.min(100, Math.round((event.loaded / event.total) * 100))
          )
        }
      },
    }
  )
  return requireServerSuccess(response.data).data
}

export async function validateRelease(
  id: number,
  revision: number
): Promise<unknown> {
  const response = await api.post<Response<unknown>>(
    `${base}/${id}/validate`,
    { revision },
    { timeout: 0 }
  )
  return requireServerSuccess(response.data).data
}

export async function acceptRelease(
  id: number,
  revision: number,
  evidence: AcceptanceEnvelope
): Promise<unknown> {
  const response = await api.post<Response<unknown>>(
    `${base}/${id}/acceptance`,
    { revision, evidence }
  )
  return requireServerSuccess(response.data).data
}

export async function operateRelease(
  id: number,
  action: ReleaseAction,
  input: {
    revision: number
    expected_channel_revision: number
    idempotency_key: string
    reason: string
  }
): Promise<unknown> {
  const response = await api.post<Response<unknown>>(
    `${base}/${id}/${action}`,
    input
  )
  return requireServerSuccess(response.data).data
}

export async function recoverReleaseValidation(
  id: number,
  revision: number
): Promise<unknown> {
  const response = await api.post<Response<unknown>>(
    `${base}/${id}/recover-validation`,
    { revision }
  )
  return requireServerSuccess(response.data).data
}
