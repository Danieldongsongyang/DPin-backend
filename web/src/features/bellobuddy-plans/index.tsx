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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { hasPermission } from '@/lib/admin-permissions'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { PlanEditorHost } from './components/plan-editor-host'
import { PlanList, type PlanBrowserState } from './components/plan-list'
import { EMPTY_PLAN_FILTERS, type PlanMetadata } from './types'

function currentIdentity(): string {
  const auth = useAuthStore.getState().auth
  return JSON.stringify([
    auth.user?.id,
    auth.user?.role,
    auth.session?.sid,
    hasPermission(auth.user, 'bellobuddy_plan', 'read'),
    hasPermission(auth.user, 'bellobuddy_plan', 'write'),
  ])
}

export function BelloBuddyPlans() {
  const user = useAuthStore((state) => state.auth.user)
  const sessionID = useAuthStore((state) => state.auth.session?.sid)
  const canRead =
    !!user &&
    user.role >= ROLE.ADMIN &&
    hasPermission(user, 'bellobuddy_plan', 'read')
  const canEdit =
    !!user &&
    user.role >= ROLE.ADMIN &&
    hasPermission(user, 'bellobuddy_plan', 'write')
  const identity = JSON.stringify([
    user?.id,
    user?.role,
    sessionID,
    canRead,
    canEdit,
  ])
  return (
    <PlanWorkspace
      key={identity}
      identity={identity}
      canRead={canRead}
      canEdit={canEdit}
      allOwners={user?.role === ROLE.SUPER_ADMIN}
    />
  )
}

function PlanWorkspace(props: {
  identity: string
  canRead: boolean
  canEdit: boolean
  allOwners: boolean
}) {
  const { t } = useTranslation()
  const [selected, setSelected] = useState<PlanMetadata | null>(null)
  const [browser, setBrowser] = useState<PlanBrowserState>({
    kind: 'communication',
    draft: EMPTY_PLAN_FILTERS,
    filters: EMPTY_PLAN_FILTERS,
    pagination: { pageIndex: 0, pageSize: 50 },
  })
  if (props.canRead && selected) {
    return (
      <PlanEditorHost
        key={`${selected.kind}:${selected.id}`}
        plan={selected}
        canEdit={props.canEdit}
        isCurrentIdentity={() => currentIdentity() === props.identity}
        onBack={() => setSelected(null)}
      />
    )
  }
  return (
    <SectionPageLayout fixedContent>
      <SectionPageLayout.Title>{t('Plan management')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        {props.canRead ? (
          <PlanList
            identity={props.identity}
            state={browser}
            setState={setBrowser}
            allOwners={props.allOwners}
            canEdit={props.canEdit}
            onOpen={setSelected}
          />
        ) : (
          <ErrorState
            description={t('You do not have permission to view plans')}
          />
        )}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
