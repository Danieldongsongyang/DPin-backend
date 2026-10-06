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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { BelloBuddyBindings } from '..'
import type { JobBinding, JobCandidate } from '../api'
import { BindingDialog } from '../components/binding-dialog'
import { MappingDialog } from '../components/mapping-dialog'

const binding: JobBinding = {
  id: 7,
  shared_key: 'support',
  name: 'Support bot',
  channel_id: 2,
  model: 'support-reply',
  enabled: true,
  version: 3,
}
const jobs: JobCandidate[] = [
  {
    job_config_id: 11,
    account_id: 1,
    user_id: 1,
    account_name: 'Account A',
    job_id: 'job-a',
    name: 'Support',
    city: 'Hangzhou',
    online: true,
    binding_id: 0,
    version: 4,
  },
  {
    job_config_id: 22,
    account_id: 2,
    user_id: 2,
    account_name: 'Account B',
    job_id: 'job-b',
    name: 'Support',
    city: 'Hangzhou',
    online: true,
    binding_id: 0,
    version: 8,
  },
]
let client: QueryClient

beforeEach(() => {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
})

afterEach(() => {
  client.clear()
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

function renderFeature(children: ReactNode) {
  return render(
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

test('same-name jobs remain unselected and confirmation submits only the selected identities and versions', async () => {
  const user = userEvent.setup()
  const onClose = vi.fn()
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: jobs, total: 2 } },
  })
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  renderFeature(<MappingDialog binding={binding} onClose={onClose} />)

  const first = await screen.findByRole('checkbox', {
    name: 'Select job job-a for user 1',
  })
  expect(first).not.toBeChecked()
  expect(
    screen.getByRole('checkbox', { name: 'Select job job-b for user 2' })
  ).not.toBeChecked()
  expect(
    screen.getByRole('button', { name: 'Review assignment (0)' })
  ).toBeDisabled()
  await user.click(first)
  await user.click(
    screen.getByRole('button', { name: 'Review assignment (1)' })
  )
  const confirm = within(
    screen.getByRole('alertdialog', { name: 'Confirm robot assignment' })
  )
  expect(confirm.getByRole('listitem')).toHaveTextContent('Account A')
  expect(confirm.queryByText(/Account B/)).not.toBeInTheDocument()
  expect(put).not.toHaveBeenCalled()
  await user.click(confirm.getByRole('button', { name: 'Assign robot' }))
  await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
  expect(put).toHaveBeenCalledExactlyOnceWith(
    '/api/bellobuddy/admin/job-bindings/7/jobs',
    { expected_version: 3, jobs: [{ job_config_id: 11, expected_version: 4 }] }
  )
})

test('a version conflict clears stale selection, refreshes candidates and prevents resubmission', async () => {
  const user = userEvent.setup()
  const onClose = vi.fn()
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: jobs, total: 2 } },
  })
  const put = vi.spyOn(api, 'put').mockRejectedValue({
    response: {
      status: 409,
      data: { success: false, code: 'BELLOBUDDY_VERSION_CONFLICT' },
    },
  })
  renderFeature(<MappingDialog binding={binding} onClose={onClose} />)
  await user.click(
    await screen.findByRole('checkbox', { name: 'Select job job-a for user 1' })
  )
  await user.click(
    screen.getByRole('button', { name: 'Review assignment (1)' })
  )
  await user.click(screen.getByRole('button', { name: 'Assign robot' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'The configuration changed. Close this dialog and reopen it to review the latest version.'
  )
  await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(
    screen.getByRole('checkbox', { name: 'Select job job-a for user 1' })
  ).not.toBeChecked()
  expect(
    screen.getByRole('button', { name: 'Review assignment (0)' })
  ).toBeDisabled()
  expect(put).toHaveBeenCalledOnce()
  expect(onClose).not.toHaveBeenCalled()
})

test('selection survives pagination without adding another same-name job automatically', async () => {
  const user = userEvent.setup()
  vi.spyOn(api, 'get').mockImplementation(async (_url, config) => ({
    data: {
      success: true,
      data: { items: [jobs[config?.params.page === 2 ? 1 : 0]], total: 51 },
    },
  }))
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  renderFeature(<MappingDialog binding={binding} onClose={vi.fn()} />)
  await user.click(
    await screen.findByRole('checkbox', { name: 'Select job job-a for user 1' })
  )
  await user.click(screen.getByRole('button', { name: 'Go to next page' }))
  const second = await screen.findByRole('checkbox', {
    name: 'Select job job-b for user 2',
  })
  expect(second).not.toBeChecked()
  await user.click(second)
  await user.click(
    screen.getByRole('button', { name: 'Review assignment (2)' })
  )
  expect(
    within(screen.getByRole('alertdialog')).getAllByRole('listitem')
  ).toHaveLength(2)
  await user.click(screen.getByRole('button', { name: 'Assign robot' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(
      '/api/bellobuddy/admin/job-bindings/7/jobs',
      {
        expected_version: 3,
        jobs: [
          { job_config_id: 11, expected_version: 4 },
          { job_config_id: 22, expected_version: 8 },
        ],
      }
    )
  )
})

test('read-only administrators can view bindings but cannot create, edit or map jobs', async () => {
  useAuthStore.getState().auth.setUser({
    id: 2,
    username: 'reader',
    role: 10,
    permissions: {
      admin_permissions: {
        bellobuddy_job_binding: { read: true, write: false },
      },
    },
  })
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: [binding], total: 1 } },
  })
  renderFeature(<BelloBuddyBindings />)
  expect(await screen.findByText('Support bot')).toBeVisible()
  for (const name of ['Create robot binding', 'Edit', 'Assign jobs']) {
    expect(screen.getByRole('button', { name })).toBeDisabled()
  }
})

test('enabling a binding requires a valid channel and logical model before submission', async () => {
  const user = userEvent.setup()
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true, data: binding } })
  const onClose = vi.fn()
  renderFeature(<BindingDialog binding={null} onClose={onClose} />)
  await user.type(
    screen.getByRole('textbox', { name: 'Shared job key' }),
    'support'
  )
  await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Support bot')
  await user.click(screen.getByRole('checkbox', { name: 'Enabled' }))
  await user.click(screen.getByRole('button', { name: 'Save' }))
  expect(
    await screen.findByText('Select a channel before enabling the robot')
  ).toBeVisible()
  expect(post).not.toHaveBeenCalled()
  await user.clear(screen.getByRole('spinbutton', { name: 'Channel ID' }))
  await user.type(screen.getByRole('spinbutton', { name: 'Channel ID' }), '2')
  await user.type(
    screen.getByRole('textbox', { name: 'Logical model' }),
    'support-reply'
  )
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
  expect(post).toHaveBeenCalledExactlyOnceWith(
    '/api/bellobuddy/admin/job-bindings',
    {
      shared_key: 'support',
      name: 'Support bot',
      channel_id: 2,
      model: 'support-reply',
      enabled: true,
      expected_version: 0,
    }
  )
})

test('editing preserves the shared job key and rejects a stale version without reporting success', async () => {
  const user = userEvent.setup()
  const onClose = vi.fn()
  const put = vi
    .spyOn(api, 'put')
    .mockRejectedValue({ response: { status: 409 } })
  renderFeature(<BindingDialog binding={binding} onClose={onClose} />)
  expect(
    screen.getByRole('textbox', { name: 'Shared job key' })
  ).toHaveAttribute('readonly')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'The configuration changed.'
  )
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
  expect(put).toHaveBeenCalledExactlyOnceWith(
    '/api/bellobuddy/admin/job-bindings/7',
    {
      shared_key: 'support',
      name: 'Support bot',
      channel_id: 2,
      model: 'support-reply',
      enabled: true,
      expected_version: 3,
    }
  )
  expect(onClose).not.toHaveBeenCalled()
})

test('a failed list shows a retry action and a successful empty result shows the empty state', async () => {
  const user = userEvent.setup()
  vi.spyOn(api, 'get')
    .mockRejectedValueOnce({ response: { status: 503 } })
    .mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
  renderFeature(<BelloBuddyBindings />)
  await user.click(await screen.findByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('No robot bindings found')).toBeVisible()
})
