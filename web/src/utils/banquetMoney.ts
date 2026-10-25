import type { BanquetTier, BanquetTierInput } from '@/api/types'

/** Pure rules of the money screens of the banquet (docs/architecture/36-banquet.md 4 and 6). */

export type TierRow = { days_from: number; days_to: number | null; percent: string; action: 'FEE' | 'FORFEIT' }

export const tierRowOf = (t: BanquetTier): TierRow => ({ days_from: t.days_from, days_to: t.days_to ?? null, percent: t.percent, action: t.action })

/** The tiers as the request takes them: a blank upper limit is open, a blank percent is 0. */
export function tierInputs(rows: TierRow[]): BanquetTierInput[] {
  return rows.map((r) => ({ days_from: Number(r.days_from) || 0, days_to: r.days_to === null || (r.days_to as unknown) === '' ? null : Number(r.days_to), percent: r.percent.trim() || '0', action: r.action }))
}

/** A tier is in force when the days left are in its range; an open upper limit has none. */
export const tierCovers = (t: Pick<BanquetTier, 'days_from' | 'days_to'>, days: number): boolean => days >= t.days_from && (t.days_to === null || days <= t.days_to)

/** The row of the schedule that is not paid yet and is the one a deposit is due for first. */
export const nextDue = <T extends { status: string }>(rows: T[]): T | undefined => rows.find((r) => r.status !== 'PAID')

export const rowVariant = (status: string): 'success' | 'destructive' | 'booked' | 'outline' =>
  status === 'PAID' ? 'success' : status === 'OVERDUE' ? 'destructive' : status === 'DUE_SOON' ? 'booked' : 'outline'

/** A percent typed by hand that the server will take: 0 to 100 with up to two decimals. */
export const isPercent = (v: string): boolean => /^\d{1,3}(\.\d{1,2})?$/.test(v.trim()) && Number(v) <= 100

/** The number of guests a posting would bill: the larger of the guaranteed and the actual number; the estimate only when nothing else is known and the person allows it. */
export function billedPax(guaranteed: number | null, actual: number | null): { pax: number; basis: 'GUARANTEED' | 'ACTUAL' | 'NONE' } {
  if (guaranteed === null && actual === null) return { pax: 0, basis: 'NONE' }
  const g = guaranteed ?? 0
  const a = actual ?? 0
  return a > g ? { pax: a, basis: 'ACTUAL' } : { pax: g, basis: 'GUARANTEED' }
}

/** The net revenue of an estimate by the account of the ledger the charge codes post to (a code without an account keeps its own name); the order of the first appearance. */
export function revenueByAccount(codes: { charge_code_id: number; charge_code: string; gl_account_code: string | null; gl_account_name: string | null; net: string }[]): { key: string; label: string; net: number }[] {
  const out = new Map<string, { key: string; label: string; net: number }>()
  for (const c of codes) {
    const key = c.gl_account_code ?? `code:${c.charge_code_id}`
    const row = out.get(key) ?? { key, label: c.gl_account_code ? `${c.gl_account_code} ${c.gl_account_name ?? ''}`.trim() : c.charge_code, net: 0 }
    row.net += Number(c.net)
    out.set(key, row)
  }
  return [...out.values()]
}
