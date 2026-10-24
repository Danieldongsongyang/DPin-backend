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

import { currentReleaseIdentity } from './access'
import { ReleaseDetailView } from './components/release-detail'
import { ReleaseList } from './components/release-list'
import type { ReleaseAccess } from './types'

export function BelloBuddyReleases() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const session = useAuthStore((state) => state.auth.session?.sid)
  const canRead =
    !!user &&
    user.role >= ROLE.ADMIN &&
    hasPermission(user, 'bellobuddy_release', 'read')
  const identity = JSON.stringify([
    user?.id,
    user?.role,
    session,
    user?.permissions,
  ])
  if (!canRead) {
    return (
      <SectionPageLayout>
        <SectionPageLayout.Title>
          {t('BelloBuddy releases')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Content>
          <ErrorState
            description={t('You do not have permission to manage releases')}
          />
        </SectionPageLayout.Content>
      </SectionPageLayout>
    )
  }
  return (
    <ReleaseWorkspace
      key={identity}
      identity={identity}
      canWrite={hasPermission(user, 'bellobuddy_release', 'write')}
      canPublish={hasPermission(user, 'bellobuddy_release', 'publish')}
      canWithdraw={hasPermission(user, 'bellobuddy_release', 'withdraw')}
    />
  )
}

function ReleaseWorkspace(props: ReleaseAccess) {
  const { t } = useTranslation()
  const [selected, setSelected] = useState<number | null>(null)
  const current = currentReleaseIdentity() === props.identity
  return (
    <SectionPageLayout fixedContent={selected === null}>
      <SectionPageLayout.Title>
        {t('BelloBuddy releases')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        {current &&
          (selected === null ? (
            <ReleaseList {...props} onOpen={setSelected} />
          ) : (
            <ReleaseDetailView
              key={selected}
              {...props}
              id={selected}
              onBack={() => setSelected(null)}
            />
          ))}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
