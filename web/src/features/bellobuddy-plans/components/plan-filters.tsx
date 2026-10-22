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
import { useTranslation } from 'react-i18next'

import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import type { PlanFilters } from '../types'

type Props = {
  value: PlanFilters
  allOwners: boolean
  onChange: (filters: PlanFilters) => void
  onSearch: () => void
}

export function PlanFilterInputs(props: Props) {
  const { t } = useTranslation()
  let visibilityLabel = t('All visibility')
  if (props.value.visibility === 'public') visibilityLabel = t('Public')
  if (props.value.visibility === 'private') visibilityLabel = t('Private')
  let statusLabel = t('All status')
  if (props.value.enabled === 'true') statusLabel = t('Enabled')
  if (props.value.enabled === 'false') statusLabel = t('Disabled')
  return (
    <>
      <Input
        aria-label={t('Plan name or ID')}
        placeholder={t('Plan name or ID')}
        className='w-full sm:w-56'
        value={props.value.keyword}
        onChange={(event) =>
          props.onChange({ ...props.value, keyword: event.target.value })
        }
        onKeyDown={(event) => {
          if (event.key === 'Enter' && !event.nativeEvent.isComposing) {
            props.onSearch()
          }
        }}
      />
      <Input
        aria-label={t('Owner username or ID')}
        placeholder={t('Owner username or ID')}
        className='w-full sm:w-52'
        value={props.value.owner}
        onChange={(event) =>
          props.onChange({ ...props.value, owner: event.target.value })
        }
        onKeyDown={(event) => {
          if (event.key === 'Enter' && !event.nativeEvent.isComposing) {
            props.onSearch()
          }
        }}
      />
      <Select
        value={props.value.visibility}
        onValueChange={(value) => {
          if (value === 'all' || value === 'public' || value === 'private') {
            props.onChange({ ...props.value, visibility: value })
          }
        }}
      >
        <SelectTrigger
          aria-label={t('Plan visibility')}
          className='w-full sm:w-36'
        >
          <SelectValue>{visibilityLabel}</SelectValue>
        </SelectTrigger>
        <SelectContent>
          <SelectItem value='all'>{t('All visibility')}</SelectItem>
          <SelectItem value='public'>{t('Public')}</SelectItem>
          {props.allOwners && (
            <SelectItem value='private'>{t('Private')}</SelectItem>
          )}
        </SelectContent>
      </Select>
      <Select
        value={props.value.enabled}
        onValueChange={(value) => {
          if (value === 'all' || value === 'true' || value === 'false') {
            props.onChange({ ...props.value, enabled: value })
          }
        }}
      >
        <SelectTrigger aria-label={t('Plan status')} className='w-full sm:w-36'>
          <SelectValue>{statusLabel}</SelectValue>
        </SelectTrigger>
        <SelectContent>
          <SelectItem value='all'>{t('All status')}</SelectItem>
          <SelectItem value='true'>{t('Enabled')}</SelectItem>
          <SelectItem value='false'>{t('Disabled')}</SelectItem>
        </SelectContent>
      </Select>
    </>
  )
}
