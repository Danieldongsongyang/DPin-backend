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
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { BelloBuddyPlans } from '..'
import { installPlanEditorBridge } from '../editor-bridge'
import type { PlanMetadata } from '../types'

const plan: PlanMetadata = {
  id: 21,
  kind: 'communication',
  name: 'Sales',
  visibility: 'private',
  owner_id: 8,
  owner_username: 'alice',
  enabled: true,
  version: 4,
  schema_version: 1,
  filter_plan_id: 31,
  created_at: 1,
  updated_at: 2,
}
const raw = '{"scheme_name":"Sales","disabled":false,"opaque":9007199254740993}'
test('screening guidance optimization uses the current editable frame without saving a plan', async () => {
  const target = { postMessage: vi.fn() } as unknown as Window
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        guidance: '必须条件：京东运营经验',
        changes: [],
        questions: [],
      },
    },
  })
  const put = vi.spyOn(api, 'put')
  const options = {
    frame: target,
    plan: { ...plan, kind: 'filter' as const },
    canEdit: true,
    isCurrentIdentity: () => true,
    onDirty: vi.fn(),
    onBack: vi.fn(),
    onSaved: vi.fn(),
  }
  const dispose = installPlanEditorBridge(options)
  const dispatch = () =>
    window.dispatchEvent(
      new MessageEvent('message', {
        source: target,
        data: {
          protocol: 'bellobuddy:filter-editor:v1',
          type: 'request',
          id: 'optimize-1',
          operation: 'optimize-guidance',
          payload: { guidance: '京东运营经验', mode: 'level' },
        },
      })
    )
  dispatch()
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  expect(post).toHaveBeenCalledWith(
    '/api/bellobuddy/ai/guidance/optimize',
    { schema_version: 1, guidance: '京东运营经验', mode: 'level' },
    expect.objectContaining({ signal: expect.any(AbortSignal) })
  )
  expect(put).not.toHaveBeenCalled()
  dispose()
  post.mockClear()
  const readonly = installPlanEditorBridge({ ...options, canEdit: false })
  dispatch()
  await waitFor(() =>
    expect(target.postMessage).toHaveBeenLastCalledWith(
      expect.objectContaining({ ok: false }),
      '*'
    )
  )
  expect(post).not.toHaveBeenCalled()
  readonly()
})
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

function renderPlans() {
  const root = createRootRoute({ component: BelloBuddyPlans })
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}
function listResponse(items = [plan], total = items.length) {
  return {
    data: { success: true, data: { items, total, page: 1, page_size: 50 } },
  }
}
function getFrame(): HTMLIFrameElement {
  return screen.getByTitle('Communication plan editor') as HTMLIFrameElement
}
function frameWindow(frame: HTMLIFrameElement): Window {
  if (!frame.contentWindow) throw new Error('The editor frame has no window')
  return frame.contentWindow
}
function message(
  frame: HTMLIFrameElement,
  operation: string,
  payload: unknown,
  id = '1'
) {
  act(() =>
    window.dispatchEvent(
      new MessageEvent('message', {
        source: frame.contentWindow,
        origin: 'null',
        data: {
          protocol: 'bellobuddy:communication-editor:v1',
          type: 'request',
          id,
          operation,
          payload,
        },
      })
    )
  )
}

test('search combines name or ID, owner, visibility and status on the server and switching kinds resets pagination', async () => {
  const user = userEvent.setup()
  const get = vi.spyOn(api, 'get').mockResolvedValue(listResponse([plan], 51))
  renderPlans()
  expect(await screen.findByText('alice')).toBeInTheDocument()
  const firstCalls = get.mock.calls.length
  await user.click(screen.getByRole('button', { name: 'Refresh' }))
  await waitFor(() => expect(get).toHaveBeenCalledTimes(firstCalls + 1))
  await user.click(screen.getByRole('button', { name: 'Go to next page' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith(
      '/api/bellobuddy/admin/plans',
      expect.objectContaining({ params: expect.objectContaining({ page: 2 }) })
    )
  )
  await user.type(
    screen.getByRole('textbox', { name: 'Plan name or ID' }),
    '21'
  )
  await user.type(
    screen.getByRole('textbox', { name: 'Owner username or ID' }),
    'alice'
  )
  await user.click(screen.getByRole('combobox', { name: 'Plan visibility' }))
  await user.click(screen.getByRole('option', { name: 'Private' }))
  await user.click(screen.getByRole('combobox', { name: 'Plan status' }))
  await user.click(screen.getByRole('option', { name: 'Disabled' }))
  await user.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith(
      '/api/bellobuddy/admin/plans',
      expect.objectContaining({
        params: expect.objectContaining({
          page: 1,
          keyword: '21',
          owner: 'alice',
          visibility: 'private',
          enabled: 'false',
          kind: 'communication',
        }),
      })
    )
  )
  await user.click(screen.getByRole('tab', { name: 'Screening plans' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith(
      '/api/bellobuddy/admin/plans',
      expect.objectContaining({
        params: expect.objectContaining({ page: 1, kind: 'filter' }),
      })
    )
  )
  expect(screen.getByRole('tab', { name: 'Screening plans' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
})

test('viewing one plan opens its exact editor directly in a sandbox and confirmed dirty return restores the native list', async () => {
  const user = userEvent.setup()
  vi.spyOn(api, 'get').mockResolvedValue(listResponse())
  renderPlans()
  await user.click(await screen.findByRole('button', { name: 'View / Edit' }))
  const frame = getFrame()
  expect(frame).toHaveAttribute(
    'src',
    '/plan-editors/communication/index.html?admin_host=1&plan_id=21'
  )
  expect(frame).toHaveAttribute('sandbox', 'allow-scripts')
  expect(screen.queryByRole('table')).not.toBeInTheDocument()
  message(frame, 'dirty', { dirty: true, save_unconfirmed: false })
  await user.click(screen.getByRole('button', { name: 'Back to plans' }))
  const confirm = within(
    await screen.findByRole('alertdialog', { name: 'Unsaved changes' })
  )
  await user.click(confirm.getByRole('button', { name: 'Stay' }))
  expect(getFrame()).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Back to plans' }))
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Leave',
    })
  )
  expect(
    await screen.findByRole('button', { name: 'View / Edit' })
  ).toBeInTheDocument()
  expect(
    screen.queryByTitle('Communication plan editor')
  ).not.toBeInTheDocument()
})

test('an administrator with read permission gets a read-only editor and no private visibility option', async () => {
  const user = userEvent.setup()
  useAuthStore.getState().auth.setUser({
    id: 2,
    username: 'reader',
    role: 10,
    permissions: {
      admin_permissions: { bellobuddy_plan: { read: true, write: false } },
    },
  })
  vi.spyOn(api, 'get').mockResolvedValue(
    listResponse([{ ...plan, visibility: 'public' }])
  )
  renderPlans()
  await user.click(
    await screen.findByRole('combobox', { name: 'Plan visibility' })
  )
  expect(
    screen.queryByRole('option', { name: 'Private' })
  ).not.toBeInTheDocument()
  await user.keyboard('{Escape}')
  await user.click(screen.getByRole('button', { name: 'View' }))
  expect(screen.getByText('Read only')).toBeInTheDocument()
  const put = vi.spyOn(api, 'put')
  const frame = getFrame()
  const post = vi.spyOn(frameWindow(frame), 'postMessage')
  message(frame, 'save', { id: 21, kind: 'communication', content_json: raw })
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      expect.objectContaining({ ok: false, code: 'FORBIDDEN' }),
      '*'
    )
  )
  expect(put).not.toHaveBeenCalled()
})

test('an identity change aborts the old detail request and prevents its response from reaching the editor', async () => {
  const user = userEvent.setup()
  let finish!: (value: unknown) => void
  let signal: AbortSignal | undefined
  vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
    if (url.endsWith('/21')) {
      signal = config?.signal as AbortSignal
      return new Promise((resolve) => {
        finish = resolve
      })
    }
    return listResponse()
  })
  renderPlans()
  await user.click(await screen.findByRole('button', { name: 'View / Edit' }))
  const frame = getFrame()
  const post = vi.spyOn(frameWindow(frame), 'postMessage')
  message(frame, 'detail', { id: 21 })
  await waitFor(() => expect(signal).toBeDefined())
  act(() =>
    useAuthStore
      .getState()
      .auth.setUser({ id: 3, username: 'other', role: 100 })
  )
  expect(signal?.aborted).toBe(true)
  await act(async () =>
    finish({
      data: {
        success: true,
        data: { ...plan, content: JSON.parse(raw), content_json: raw },
      },
    })
  )
  expect(
    post.mock.calls.filter(([value]) => value.type === 'response')
  ).toHaveLength(0)
  expect(
    screen.queryByTitle('Communication plan editor')
  ).not.toBeInTheDocument()
})

test('saving without prior changes still protects navigation while the write is pending', async () => {
  const user = userEvent.setup()
  vi.spyOn(api, 'get').mockResolvedValue(listResponse())
  let rejectSave!: (error: unknown) => void
  vi.spyOn(api, 'put').mockImplementation(
    () =>
      new Promise((_resolve, reject) => {
        rejectSave = reject
      })
  )
  renderPlans()
  await user.click(await screen.findByRole('button', { name: 'View / Edit' }))
  message(getFrame(), 'save', {
    id: 21,
    kind: 'communication',
    schema_version: 1,
    expected_version: 4,
    filter_plan_id: 31,
    content_json: raw,
  })
  await user.click(screen.getByRole('button', { name: 'Back to plans' }))
  expect(
    await screen.findByRole('alertdialog', { name: 'Unsaved changes' })
  ).toBeInTheDocument()
  await act(async () => rejectSave({ code: 'ERR_NETWORK' }))
})

test('empty, failed and forbidden lists show their actual state without exposing an editor', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue(listResponse([]))
  renderPlans()
  expect(await screen.findByText('No plans found')).toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'View / Edit' })
  ).not.toBeInTheDocument()
  get.mockRejectedValue({
    response: { data: { message: 'Cannot read plans' } },
  })
  await act(async () => {
    await client.invalidateQueries({ queryKey: ['bellobuddy-admin-plans'] })
  })
  expect(await screen.findByText('Cannot read plans')).toBeInTheDocument()
  const calls = get.mock.calls.length
  act(() =>
    useAuthStore
      .getState()
      .auth.setUser({ id: 2, username: 'ordinary', role: 1 })
  )
  expect(
    await screen.findByText('You do not have permission to view plans')
  ).toBeInTheDocument()
  expect(get).toHaveBeenCalledTimes(calls)
})
