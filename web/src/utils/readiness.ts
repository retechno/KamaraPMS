import { t, te } from '@/i18n'
import { formatDate } from '@/utils/format'

/** A finding of the accounting go-live check (GET .../accounting/readiness, and accounting_readiness of the night audit): a code, the English sentence of the server and what it is about. */
export interface ReadinessFinding {
  code: string
  message: string
  ref?: string
}

/** Where a finding is fixed: the screen and the permission of its menu item. SUPPORT: the accounting data of the property is incomplete and no screen fixes it (docs/backup-restore.md). */
export type ReadinessFix = { to: string; label: string; permission: string } | 'SUPPORT'

const MAPPING = { to: '/accounting/mapping', label: 'nav.items.systemAccounts', permission: 'accounting.view' }

const FIX: Record<string, ReadinessFix> = {
  ACCOUNTING_NOT_SET_UP: 'SUPPORT',
  ACCOUNTING_NOT_STARTED: 'SUPPORT',
  JOURNAL_SEQUENCE_MISSING: 'SUPPORT',
  ACCOUNT_MAP_MISSING: MAPPING,
  ACCOUNT_MAP_UNUSABLE: MAPPING,
  ACCOUNT_MAP_OPTIONAL: MAPPING,
  CODE_UNMAPPED: MAPPING,
  ROOM_CHARGE_CODE_UNMAPPED: { to: '/setup/charge-codes', label: 'nav.items.chargeCodes', permission: 'billing_config.manage' },
  DEPARTMENT_SETUP_INCOMPLETE: { to: '/accounting/departments', label: 'nav.items.departments', permission: 'accounting.view' },
  TRANSFER_ACCOUNT_NOT_ZERO: { to: '/accounting/journals', label: 'nav.items.journals', permission: 'accounting.view' },
}

/** The screen that fixes a finding; undefined for a code this release does not know. */
export const readinessFix = (code: string): ReadinessFix | undefined => FIX[code]

/** The finding in the language of the page; the sentence of the server for a code this release does not know. */
export function readinessText(f: ReadinessFinding): string {
  const key = `readiness.code.${f.code}`
  if (!te(key)) return f.message
  const ref = f.code === 'ACCOUNTING_NOT_STARTED' && f.ref ? formatDate(f.ref) : (f.ref ?? '')
  return t(key as never, { ref })
}
