import type { GlAccount } from '@/api/types'

export const ACCOUNT_TYPES = ['ASSET', 'LIABILITY', 'EQUITY', 'REVENUE', 'EXPENSE'] as const

/** The statement groups an account of each type may belong to (the same lists as the server's). */
export const GROUPS: Record<string, { value: string; label: string }[]> = {
  ASSET: [
    { value: 'CASH', label: 'Cash and equivalents' }, { value: 'RECEIVABLES', label: 'Receivables' }, { value: 'INVENTORIES', label: 'Inventories' },
    { value: 'PREPAID', label: 'Prepaid and other current' }, { value: 'FIXED_ASSETS', label: 'Property and equipment' }, { value: 'OTHER_ASSETS', label: 'Other assets' },
  ],
  LIABILITY: [
    { value: 'PAYABLES', label: 'Payables' }, { value: 'ACCRUED', label: 'Accrued expenses' }, { value: 'DEPOSITS', label: 'Guest deposits and unearned revenue' },
    { value: 'TAXES_PAYABLE', label: 'Taxes and service charges payable' }, { value: 'OTHER_CURRENT_LIABILITIES', label: 'Other current liabilities' },
    { value: 'LONG_TERM_DEBT', label: 'Long-term liabilities' }, { value: 'SUSPENSE', label: 'Suspense' },
  ],
  EQUITY: [{ value: 'EQUITY', label: 'Equity' }],
  REVENUE: [
    { value: 'REV_ROOMS', label: 'Rooms revenue' }, { value: 'REV_FB', label: 'Food and beverage revenue' }, { value: 'REV_OOD', label: 'Other operated departments' },
    { value: 'REV_RENTAL_OTHER', label: 'Rentals and other income' }, { value: 'REV_MISC', label: 'Miscellaneous income' },
  ],
  EXPENSE: [
    { value: 'EXP_ROOMS', label: 'Rooms expenses' }, { value: 'EXP_FB', label: 'Food and beverage expenses' }, { value: 'EXP_OOD', label: 'Other operated departments expenses' },
    { value: 'UND_AG', label: 'Administrative and general' }, { value: 'UND_IT', label: 'Information and telecommunications' }, { value: 'UND_SM', label: 'Sales and marketing' },
    { value: 'UND_POM', label: 'Property operations and maintenance' }, { value: 'UND_UTIL', label: 'Utilities' }, { value: 'MGMT_FEES', label: 'Management and franchise fees' },
    { value: 'NONOP', label: 'Non-operating (rent, taxes, insurance)' }, { value: 'DEPRECIATION', label: 'Depreciation and amortization' }, { value: 'INTEREST', label: 'Interest' },
    { value: 'INCOME_TAX', label: 'Income taxes' },
  ],
}

export const TYPE_LABEL: Record<string, string> = { ASSET: 'Assets', LIABILITY: 'Liabilities', EQUITY: 'Equity', REVENUE: 'Revenue', EXPENSE: 'Expenses' }

export interface TreeRow {
  account: GlAccount
  depth: number
}

/** Orders accounts as a tree (children under their parent, each level by code) with the depth of every row. */
export function toTree(accounts: GlAccount[]): TreeRow[] {
  const children = new Map<number | null, GlAccount[]>()
  const ids = new Set(accounts.map((a) => a.id))
  for (const a of accounts) {
    // An account whose parent is not in the list (filtered out) is shown at the top.
    const parent = a.parent_id !== null && ids.has(a.parent_id) ? a.parent_id : null
    children.set(parent, [...(children.get(parent) ?? []), a])
  }
  const out: TreeRow[] = []
  const walk = (parent: number | null, depth: number): void => {
    for (const a of (children.get(parent) ?? []).sort((x, y) => x.code.localeCompare(y.code))) {
      out.push({ account: a, depth })
      walk(a.id, depth + 1)
    }
  }
  walk(null, 0)
  return out
}

const SCALE = 1000n

/** A decimal string as thousandths, or null when it is not a plain decimal: amounts are never floats. */
export function toMilli(value: string): bigint | null {
  const m = /^(-?)(\d+)(?:\.(\d{1,3}))?$/.exec(value.trim())
  if (!m) return null
  const whole = BigInt(m[2] ?? '0') * SCALE
  const frac = BigInt((m[3] ?? '').padEnd(3, '0') || '0')
  return m[1] ? -(whole + frac) : whole + frac
}

/** Thousandths as a decimal string without trailing zeros. */
export function fromMilli(n: bigint): string {
  const neg = n < 0n
  const abs = neg ? -n : n
  const whole = abs / SCALE
  const frac = (abs % SCALE).toString().padStart(3, '0').replace(/0+$/, '')
  return `${neg ? '-' : ''}${whole}${frac ? `.${frac}` : ''}`
}

/** Adds the debit and credit columns of journal form lines; an entry that is not an amount counts as zero. */
export function totals(lines: { debit: string; credit: string }[]): { debit: bigint; credit: bigint; valid: boolean } {
  let debit = 0n
  let credit = 0n
  let valid = true
  for (const l of lines) {
    for (const [v, side] of [[l.debit, 'd'], [l.credit, 'c']] as const) {
      if (v.trim() === '') continue
      const n = toMilli(v)
      if (n === null || n < 0n) {
        valid = false
        continue
      }
      if (side === 'd') debit += n
      else credit += n
    }
  }
  return { debit, credit, valid }
}
