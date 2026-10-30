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

import {
  getServerErrorMessage,
  getServerErrorSources,
} from '@/lib/server-error-message'

import {
  getPlan,
  invalidEditorRequest,
  isRecord,
  listFilterCandidates,
  positivePlanID,
  savePlan,
  optimizeFilterGuidance,
} from './api'
import type { PlanDetail, PlanMetadata } from './types'

type BridgeOptions = {
  frame: Window
  plan: PlanMetadata
  canEdit: boolean
  isCurrentIdentity: () => boolean
  onDirty: (dirty: boolean) => void
  onBack: () => void
  onSaved: (plan: PlanMetadata) => void
  onSavingChange?: (saving: boolean) => void
}

export function installPlanEditorBridge(options: BridgeOptions): () => void {
  const protocol = `bellobuddy:${options.plan.kind}-editor:v1`
  const controller = new AbortController()
  const seen = new Set<string>()
  let active = true
  let currentPlan = options.plan
  let savePending = false
  let saveUnconfirmed = false
  let saveAttempt = 0
  const isCurrent = () => active && options.isCurrentIdentity()

  function detailForEditor(detail: PlanDetail, canEdit = options.canEdit) {
    const { content, content_json, ...metadata } = detail
    return {
      metadata: { ...metadata, can_edit: canEdit, admin_mode: true },
      content,
      content_json,
    }
  }

  async function dispatch(
    operation: string,
    payload: Record<string, unknown>
  ): Promise<unknown> {
    if (operation === 'dirty') {
      if (
        typeof payload.dirty !== 'boolean' ||
        (payload.save_unconfirmed !== undefined &&
          typeof payload.save_unconfirmed !== 'boolean')
      ) {
        throw invalidEditorRequest()
      }
      options.onDirty(
        payload.dirty || payload.save_unconfirmed === true || saveUnconfirmed
      )
      return {}
    }
    if (operation === 'back') return {}
    if (operation === 'optimize-guidance') {
      if (!options.canEdit || currentPlan.kind !== 'filter') {
        throw invalidEditorRequest()
      }
      return optimizeFilterGuidance(payload, controller.signal)
    }
    if (operation === 'list') return [currentPlan]
    if (operation === 'detail') {
      if (payload.id !== currentPlan.id) throw invalidEditorRequest()
      const attempt = saveAttempt
      const detail = await getPlan(
        currentPlan.id,
        currentPlan.kind,
        controller.signal
      )
      if (isCurrent()) {
        const { content: _content, content_json: _source, ...metadata } = detail
        currentPlan = metadata
        if (attempt === saveAttempt && !savePending) saveUnconfirmed = false
      }
      return detailForEditor(detail)
    }
    if (operation === 'save') {
      if (!options.canEdit) {
        throw Object.assign(
          new Error(t('You do not have permission to edit this plan')),
          { code: 'FORBIDDEN' }
        )
      }
      if (savePending) throw invalidEditorRequest()
      if (saveUnconfirmed) {
        throw Object.assign(
          new Error(
            t(
              'Save result is unconfirmed. Reload the plan before saving again.'
            )
          ),
          { code: 'SAVE_UNCONFIRMED' }
        )
      }
      savePending = true
      saveAttempt++
      options.onSavingChange?.(true)
      try {
        const saved = await savePlan(currentPlan, payload, controller.signal)
        if (isCurrent()) {
          const {
            content: _content,
            content_json: _source,
            ...metadata
          } = saved
          currentPlan = metadata
        }
        return detailForEditor(saved)
      } catch (error) {
        if (
          getServerErrorSources(error).some(
            (source) =>
              source.code === 'SAVE_UNCONFIRMED' ||
              source.code === 'INVALID_SAVE_RESPONSE'
          )
        ) {
          saveUnconfirmed = true
          if (isCurrent()) options.onDirty(true)
        }
        throw error
      } finally {
        savePending = false
        if (isCurrent()) options.onSavingChange?.(false)
      }
    }
    if (currentPlan.kind === 'communication' && operation === 'filter-list') {
      return listFilterCandidates(currentPlan.id, controller.signal)
    }
    if (currentPlan.kind === 'communication' && operation === 'filter-detail') {
      if (!positivePlanID(payload.id)) throw invalidEditorRequest()
      return detailForEditor(
        await getPlan(payload.id, 'filter', controller.signal, currentPlan.id),
        false
      )
    }
    throw invalidEditorRequest()
  }

  function receive(event: MessageEvent) {
    if (
      !isCurrent() ||
      event.source !== options.frame ||
      !isRecord(event.data)
    ) {
      return
    }
    const data = event.data
    if (
      data.protocol !== protocol ||
      data.type !== 'request' ||
      typeof data.id !== 'string' ||
      !data.id ||
      data.id.length > 128 ||
      seen.has(data.id)
    ) {
      return
    }
    seen.add(data.id)
    // Bound memory while retaining the IDs of recent request/response pairs.
    const oldest = seen.values().next().value
    if (seen.size > 2048 && oldest !== undefined) seen.delete(oldest)
    const respond = (response: Record<string, unknown>) => {
      if (isCurrent()) {
        options.frame.postMessage(
          { protocol, type: 'response', id: data.id, ...response },
          '*'
        )
      }
    }
    const result =
      typeof data.operation === 'string' && isRecord(data.payload)
        ? dispatch(data.operation, data.payload)
        : Promise.reject(invalidEditorRequest())
    void result
      .then((value) => {
        if (!isCurrent()) return
        respond({ ok: true, data: value })
        if (data.operation === 'save') options.onSaved(currentPlan)
        if (data.operation === 'back') options.onBack()
      })
      .catch((error: unknown) => {
        const sources = getServerErrorSources(error)
        const responseSource = sources.find(
          (item) =>
            isRecord(item.response) &&
            isRecord(item.response.data) &&
            typeof item.response.data.code === 'string'
        )
        const businessCode =
          isRecord(responseSource?.response) &&
          isRecord(responseSource.response.data)
            ? responseSource.response.data.code
            : undefined
        const source = sources.find((item) => typeof item.code === 'string')
        respond({
          ok: false,
          code: businessCode ?? source?.code ?? 'REQUEST_FAILED',
          message: getServerErrorMessage(error, t('Failed to load plan')),
        })
      })
  }

  window.addEventListener('message', receive)
  return () => {
    if (!active) return
    active = false
    controller.abort()
    window.removeEventListener('message', receive)
    options.frame.postMessage({ protocol, type: 'identity-changed' }, '*')
  }
}
