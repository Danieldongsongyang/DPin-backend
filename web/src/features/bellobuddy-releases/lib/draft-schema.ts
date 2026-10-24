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
import type { TFunction } from 'i18next'
import { z } from 'zod'

export function releaseDraftSchema(t: TFunction) {
  return z.object({
    version: z
      .string()
      .trim()
      .min(1, t('Required'))
      .max(96, t('Invalid value'))
      .regex(
        /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/,
        t('Enter a semantic version such as 3.0.29')
      ),
    build_id: z
      .string()
      .trim()
      .min(1, t('Required'))
      .max(128, t('Invalid value')),
    title: z.string().trim().min(1, t('Required')).max(256, t('Invalid value')),
    notes: z.string().max(65536, t('Invalid value')),
    minimum_system_version: z
      .string()
      .trim()
      .min(1, t('Required'))
      .max(64, t('Invalid value'))
      .refine(
        (value) => value === '' || /^\d+\.\d+\.\d+(?:\.\d+)?$/.test(value),
        t('Use the Windows system version, for example 10.0.19045')
      ),
  })
}
