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
import { hasPermission } from '@/lib/admin-permissions'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

export function currentReleaseIdentity(): string {
  const auth = useAuthStore.getState().auth
  return JSON.stringify([
    auth.user?.id,
    auth.user?.role,
    auth.session?.sid,
    auth.user?.permissions,
  ])
}

export function releasePermission(action: string): boolean {
  const user = useAuthStore.getState().auth.user
  return (
    !!user &&
    user.role >= ROLE.ADMIN &&
    hasPermission(user, 'bellobuddy_release', action)
  )
}
