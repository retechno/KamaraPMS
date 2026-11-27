import type { CityLedgerInvoice } from '@/api/types'
import { t } from '@/i18n'
import { formatMoney } from '@/utils/format'
import { fromMilli, toMilli } from '@/views/accounting/accountMeta'

/**
 * The split of a payment of a company over its open invoices (KD-04 with the receipt, KD-27 after it: docs/architecture/41-receipt-allocation.md).
 * Amounts are thousandths (bigint), never floats.
 */

/** The invoices a payment can pay: issued and still owing, the oldest first. */
export function openInvoicesOf(invoices: CityLedgerInvoice[]): CityLedgerInvoice[] {
  return invoices
    .filter((i) => i.status === 'ISSUED' && (toMilli(i.outstanding) ?? 0n) > 0n)
    .sort((a, b) => a.invoice_date.localeCompare(b.invoice_date) || a.id - b.id)
}

/** The amount spread over the invoices from the oldest: each takes what it still owes until nothing is left. */
export function splitFromOldest(amount: string, invoices: CityLedgerInvoice[]): Record<number, string> {
  const total = toMilli(amount) ?? 0n
  let left = total > 0n ? total : 0n
  const out: Record<number, string> = {}
  for (const inv of invoices) {
    const owes = toMilli(inv.outstanding) ?? 0n
    const take = left < owes ? left : owes
    out[inv.id] = take > 0n ? fromMilli(take) : ''
    left -= take
  }
  return out
}

export interface AllocationLine {
  inv: CityLedgerInvoice
  milli: bigint | null
  bad: boolean
  over: boolean
}

/** What the clerk typed, checked against the invoices and the amount: the lines, what is left, the first problem, and the body to send. */
export function checkAllocations(model: Record<number, string>, invoices: CityLedgerInvoice[], amount: string) {
  const lines: AllocationLine[] = invoices.map((inv) => {
    const value = (model[inv.id] ?? '').trim()
    const milli = value === '' ? 0n : toMilli(value)
    return { inv, milli, bad: milli === null || milli < 0n, over: milli !== null && milli > (toMilli(inv.outstanding) ?? 0n) }
  })
  const allocated = lines.reduce((sum, l) => sum + (l.milli !== null && l.milli > 0n ? l.milli : 0n), 0n)
  const unallocated = (toMilli(amount) ?? 0n) - allocated
  let problem = ''
  const over = lines.find((l) => l.over)
  if (lines.some((l) => l.bad)) problem = t('clAccount.allocInvalid')
  else if (over) problem = t('clAccount.allocExceedsInvoice', { number: over.inv.invoice_number, amount: formatMoney(over.inv.outstanding) })
  else if (unallocated < 0n) problem = t('clAccount.allocExceedsAmount')
  const payload = lines.filter((l) => l.milli !== null && l.milli > 0n).map((l) => ({ invoice_id: l.inv.id, amount: fromMilli(l.milli as bigint) }))
  return { lines, allocated, unallocated, problem, payload }
}
