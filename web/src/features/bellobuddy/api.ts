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

export type JobBinding = {
  id: number
  shared_key: string
  name: string
  channel_id: number
  model: string
  enabled: boolean
  version: number
}

export type JobCandidate = {
  job_config_id: number
  account_id: number
  user_id: number
  account_name: string
  job_id: string
  name: string
  city: string
  online: boolean
  binding_id: number
  version: number
}

export type Page<T> = {
  items: T[]
  total: number
  page: number
  page_size: number
}

type Response<T> = { success: boolean; data: T }
export type BindingInput = Omit<JobBinding, 'id' | 'version'> & {
  expected_version: number
}

export async function listJobBindings(
  page: number,
  pageSize: number,
  signal?: AbortSignal
): Promise<Page<JobBinding>> {
  const response = await api.get<Response<Page<JobBinding>>>(
    '/api/bellobuddy/admin/job-bindings',
    { params: { page, page_size: pageSize }, signal }
  )
  return requireServerSuccess(response.data).data
}

export async function saveJobBinding(
  id: number,
  input: BindingInput
): Promise<JobBinding> {
  const endpoint = '/api/bellobuddy/admin/job-bindings'
  const response = id
    ? await api.put<Response<JobBinding>>(`${endpoint}/${id}`, input)
    : await api.post<Response<JobBinding>>(endpoint, input)
  return requireServerSuccess(response.data).data
}

export async function listJobCandidates(
  params: { page: number; page_size: number; name: string; city: string },
  signal?: AbortSignal
): Promise<Page<JobCandidate>> {
  const response = await api.get<Response<Page<JobCandidate>>>(
    '/api/bellobuddy/admin/job-bindings/candidates',
    { params, signal }
  )
  return requireServerSuccess(response.data).data
}

export async function mapJobsToBinding(
  binding: JobBinding,
  jobs: JobCandidate[]
): Promise<void> {
  const response = await api.put<Response<unknown>>(
    `/api/bellobuddy/admin/job-bindings/${binding.id}/jobs`,
    {
      expected_version: binding.version,
      jobs: jobs.map((job) => ({
        job_config_id: job.job_config_id,
        expected_version: job.version,
      })),
    }
  )
  requireServerSuccess(response.data)
}
