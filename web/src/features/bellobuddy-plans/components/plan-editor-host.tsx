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
import { useQueryClient } from '@tanstack/react-query'
import { ArrowLeft } from 'lucide-react'
import { useLayoutEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { FormNavigationGuard } from '@/features/system-settings/components/form-navigation-guard'
import { useAuthStore } from '@/stores/auth-store'

import { installPlanEditorBridge } from '../editor-bridge'
import type { PlanMetadata } from '../types'

type Props = {
  plan: PlanMetadata
  canEdit: boolean
  isCurrentIdentity: () => boolean
  onBack: () => void
}

export function PlanEditorHost(props: Props) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const frame = useRef<HTMLIFrameElement>(null)
  const [dirty, setDirty] = useState(false)
  const [confirmBack, setConfirmBack] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [saving, setSaving] = useState(false)
  const [metadata, setMetadata] = useState(props.plan)
  const latestProps = useRef(props)
  latestProps.current = props
  useLayoutEffect(() => {
    const target = frame.current?.contentWindow
    if (!target) return
    const dispose = installPlanEditorBridge({
      frame: target,
      plan: props.plan,
      canEdit: props.canEdit,
      isCurrentIdentity: () => latestProps.current.isCurrentIdentity(),
      onDirty: setDirty,
      onSavingChange: setSaving,
      onBack: () => {
        setDirty(false)
        latestProps.current.onBack()
      },
      onSaved: (plan) => {
        setMetadata(plan)
        void client.invalidateQueries({ queryKey: ['bellobuddy-admin-plans'] })
      },
    })
    const unsubscribe = useAuthStore.subscribe(() => {
      if (!latestProps.current.isCurrentIdentity()) dispose()
    })
    return () => {
      unsubscribe()
      dispose()
    }
  }, [props.plan, props.canEdit, client])
  const back = () => {
    if (dirty || saving) setConfirmBack(true)
    else props.onBack()
  }
  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>{metadata.name}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Badge variant='outline'>
            {metadata.visibility === 'private' ? t('Private') : t('Public')}
          </Badge>
          {!props.canEdit && (
            <Badge variant='secondary'>{t('Read only')}</Badge>
          )}
          <Button variant='outline' onClick={back}>
            <ArrowLeft aria-hidden='true' />
            {t('Back to plans')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='flex h-full min-h-0 flex-col gap-2'>
            <p className='text-muted-foreground shrink-0 text-xs break-all'>
              {metadata.kind === 'communication'
                ? t('Communication plans')
                : t('Screening plans')}{' '}
              · ID {metadata.id} · {t('Owner')}:{' '}
              {metadata.owner_id === 0
                ? t('Platform')
                : `${metadata.owner_username || t('User')} #${metadata.owner_id}`}{' '}
              · {t('Version')}: {metadata.version}
            </p>
            <div className='relative min-h-0 flex-1 overflow-hidden rounded-lg border'>
              {!loaded && (
                <div className='bg-background absolute inset-0 z-10'>
                  <LoadingState />
                </div>
              )}
              <iframe
                ref={frame}
                src={`/plan-editors/${metadata.kind}/index.html?admin_host=1&plan_id=${metadata.id}`}
                title={
                  metadata.kind === 'communication'
                    ? t('Communication plan editor')
                    : t('Screening plan editor')
                }
                sandbox='allow-scripts'
                referrerPolicy='no-referrer'
                className='h-full w-full border-0'
                onLoad={() => setLoaded(true)}
              />
            </div>
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>
      <FormNavigationGuard when={dirty || saving} />
      <ConfirmDialog
        open={confirmBack}
        onOpenChange={setConfirmBack}
        title={t('Unsaved changes')}
        desc={t('You have unsaved changes. Are you sure you want to leave?')}
        confirmText={t('Leave')}
        cancelBtnText={t('Stay')}
        destructive
        handleConfirm={() => {
          setDirty(false)
          props.onBack()
        }}
      />
    </>
  )
}
