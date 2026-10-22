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
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

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
const protocol = 'bellobuddy:communication-editor:v1'
let dispose: () => void
let frame: Window
let post: ReturnType<typeof vi.fn>
let current: boolean
let onBack: ReturnType<typeof vi.fn<() => void>>
let onDirty: ReturnType<typeof vi.fn<(dirty: boolean) => void>>

beforeEach(() => {
  current = true
  post = vi.fn()
  frame = { postMessage: post } as unknown as Window
  onBack = vi.fn()
  onDirty = vi.fn()
})
afterEach(() => {
  dispose?.()
  vi.restoreAllMocks()
})

function mount(canEdit = true) {
  dispose = installPlanEditorBridge({
    frame,
    plan,
    canEdit,
    isCurrentIdentity: () => current,
    onBack,
    onDirty,
    onSaved: vi.fn(),
  })
}
function request(
  operation: string,
  payload: unknown,
  id = '1',
  source = frame,
  customProtocol = protocol
) {
  window.dispatchEvent(
    new MessageEvent('message', {
      source,
      origin: 'null',
      data: {
        protocol: customProtocol,
        type: 'request',
        id,
        operation,
        payload,
      },
    })
  )
}
function detailResponse() {
  return {
    data: {
      success: true,
      data: { ...plan, content: JSON.parse(raw), content_json: raw },
    },
  }
}

test('only the selected frame, protocol and plan identity can read a detail', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue(detailResponse())
  mount()
  request('detail', { id: 21 }, 'wrong-source', window)
  request('detail', { id: 21 }, 'wrong-protocol', frame, 'unknown')
  request('detail', { id: 999 }, 'wrong-plan')
  request('detail', { id: 21 })
  await vi.waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      expect.objectContaining({ id: '1', ok: true }),
      '*'
    )
  )
  expect(get).toHaveBeenCalledOnce()
  expect(
    post.mock.calls.find(([message]) => message.id === '1')?.[0].data
  ).toEqual({
    metadata: { ...plan, can_edit: true, admin_mode: true },
    content: JSON.parse(raw),
    content_json: raw,
  })
})

test('saving sends the original content string without rounding unknown integers or changing ownership', async () => {
  vi.spyOn(api, 'get').mockResolvedValue(detailResponse())
  const put = vi.spyOn(api, 'put').mockResolvedValue(detailResponse())
  mount()
  request('save', {
    id: 21,
    kind: 'communication',
    schema_version: 1,
    expected_version: 4,
    filter_plan_id: 31,
    content_json: raw,
  })
  await vi.waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      expect.objectContaining({ ok: true }),
      '*'
    )
  )
  expect(put).toHaveBeenCalledExactlyOnceWith(
    '/api/bellobuddy/admin/plans/21',
    {
      kind: 'communication',
      name: 'Sales',
      enabled: true,
      schema_version: 1,
      expected_version: 4,
      filter_plan_id: 31,
      content_json: raw,
    },
    expect.objectContaining({
      skipAuthRefresh: true,
      skipErrorHandler: true,
      signal: expect.any(AbortSignal),
    })
  )
})

test('a read-only editor cannot save, create or select another plan', async () => {
  const put = vi.spyOn(api, 'put')
  mount(false)
  request('save', { id: 21, kind: 'communication', content_json: raw })
  request('save', { id: 0, kind: 'communication', content_json: raw }, '2')
  await vi.waitFor(() => expect(post).toHaveBeenCalledTimes(2))
  expect(post.mock.calls.every(([message]) => message.ok === false)).toBe(true)
  expect(put).not.toHaveBeenCalled()
})

test('filter association lookups carry the selected communication plan as the permission target', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValueOnce({
      data: { success: true, data: { items: [], total: 0 } },
    })
    .mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          ...plan,
          id: 31,
          kind: 'filter',
          content: {},
          content_json: '{}',
        },
      },
    })
  mount()
  request('filter-list', {})
  await vi.waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      expect.objectContaining({ id: '1', ok: true }),
      '*'
    )
  )
  request('filter-detail', { id: 31 }, '2')
  await vi.waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      expect.objectContaining({ id: '2', ok: true }),
      '*'
    )
  )
  expect(get).toHaveBeenNthCalledWith(
    1,
    '/api/bellobuddy/admin/plans',
    expect.objectContaining({
      params: { kind: 'filter', for_plan_id: 21, page: 1, page_size: 100 },
    })
  )
  expect(get).toHaveBeenNthCalledWith(
    2,
    '/api/bellobuddy/admin/plans/31',
    expect.objectContaining({ params: { for_plan_id: 21 } })
  )
})

test('identity changes or editor disposal discard in-flight responses', async () => {
  let finish!: (value: ReturnType<typeof detailResponse>) => void
  vi.spyOn(api, 'get').mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve
      })
  )
  mount()
  request('detail', { id: 21 })
  current = false
  finish(detailResponse())
  await Promise.resolve()
  await Promise.resolve()
  expect(post).not.toHaveBeenCalled()
  current = true
  request('detail', { id: 21 }, '2')
  dispose()
  finish(detailResponse())
  await Promise.resolve()
  await Promise.resolve()
  expect(
    post.mock.calls.filter(([message]) => message.type === 'response')
  ).toHaveLength(0)
})

test('dirty state and confirmed editor back are handled without issuing API requests', async () => {
  const get = vi.spyOn(api, 'get')
  mount()
  request('dirty', { dirty: true, save_unconfirmed: true })
  await vi.waitFor(() => expect(onDirty).toHaveBeenCalledWith(true))
  request('back', {}, '2')
  await vi.waitFor(() => expect(onBack).toHaveBeenCalledOnce())
  expect(get).not.toHaveBeenCalled()
})

test('replayed save request ids execute once and failures retain the server conflict message', async () => {
  const put = vi.spyOn(api, 'put').mockRejectedValue({
    code: 'ERR_BAD_REQUEST',
    response: {
      status: 409,
      data: {
        code: 'VERSION_CONFLICT',
        message: '配置已更新，请重新读取后保存',
      },
    },
  })
  mount()
  const payload = {
    id: 21,
    kind: 'communication',
    schema_version: 1,
    expected_version: 4,
    filter_plan_id: 31,
    content_json: raw,
  }
  request('save', payload)
  request('save', payload)
  await vi.waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      expect.objectContaining({
        ok: false,
        code: 'VERSION_CONFLICT',
        message: '配置已更新，请重新读取后保存',
      }),
      '*'
    )
  )
  expect(put).toHaveBeenCalledOnce()
})

test.each([
  ['network failure', { code: 'ERR_NETWORK' }, 'SAVE_UNCONFIRMED'],
  [
    'server failure',
    { code: 'ERR_BAD_RESPONSE', response: { status: 503 } },
    'SAVE_UNCONFIRMED',
  ],
  ['empty response', null, 'INVALID_SAVE_RESPONSE'],
] as const)(
  '%s pauses further writes until a successful fresh detail read',
  async (_label, failure, code) => {
    const put = vi.spyOn(api, 'put')
    if (failure === null) put.mockResolvedValue({ data: null })
    else put.mockRejectedValue(failure)
    vi.spyOn(api, 'get').mockResolvedValue(detailResponse())
    mount()
    const payload = {
      id: 21,
      kind: 'communication',
      schema_version: 1,
      expected_version: 4,
      filter_plan_id: 31,
      content_json: raw,
    }
    request('save', payload)
    await vi.waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        expect.objectContaining({ id: '1', ok: false, code }),
        '*'
      )
    )
    request('save', payload, '2')
    await vi.waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        expect.objectContaining({
          id: '2',
          ok: false,
          code: 'SAVE_UNCONFIRMED',
        }),
        '*'
      )
    )
    expect(put).toHaveBeenCalledOnce()
    request('detail', { id: 21 }, '3')
    await vi.waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        expect.objectContaining({ id: '3', ok: true }),
        '*'
      )
    )
    put.mockResolvedValue(detailResponse())
    request('save', payload, '4')
    await vi.waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        expect.objectContaining({ id: '4', ok: true }),
        '*'
      )
    )
    expect(put).toHaveBeenCalledTimes(2)
  }
)
