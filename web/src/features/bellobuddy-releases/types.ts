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
export type ReleaseState =
  | 'draft'
  | 'validating'
  | 'ready'
  | 'published'
  | 'withdrawn'
export type ArtifactKind = 'installer' | 'blockmap' | 'metadata' | 'manifest'
export type ReleaseAction = 'publish' | 'withdraw' | 'disable'

export type Release = {
  id: number
  product: string
  version: string
  build_id: string
  title: string
  notes: string
  minimum_system_version: string
  state: ReleaseState
  disabled: boolean
  disabled_reason: string
  revision: number
  created_by: number
  created_at: number
  updated_at: number
  published_at: number
  validation_report: Record<string, unknown> | null
  acceptance_report: Record<string, unknown> | null
}

export type ReleaseArtifact = {
  id: number
  release_id: number
  kind: ArtifactKind
  filename: string
  size: number
  sha256: string
  sha512: string
}

export type ReleaseUpload = {
  id: number
  kind: ArtifactKind
  filename: string
  state: string
  bytes_received: number
  code: string
  created_at: number
}

export type ReleaseChannel = {
  release_id: number
  revision: number
  version?: string
}

export type ReleaseOperation = {
  id: number
  action: string
  actor_id: number
  from_release_id: number
  to_release_id: number
  reason: string
  created_at: number
  idempotency_key: string
}

export type ReleasePage = {
  items: Release[]
  total: number
  page: number
  page_size: number
  channel: ReleaseChannel
}

export type ReleaseDetail = {
  release: Release
  artifacts: ReleaseArtifact[]
  uploads: ReleaseUpload[]
  operations: ReleaseOperation[]
  channel: ReleaseChannel
}

export type ReleaseDraftInput = {
  version: string
  build_id: string
  title: string
  notes: string
  minimum_system_version: string
}

export type ReleaseAccess = {
  identity: string
  canWrite: boolean
  canPublish: boolean
  canWithdraw: boolean
}

export type AcceptanceEnvelope = { payload: string; signature: string }
