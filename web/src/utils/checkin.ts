import type { Arrival } from '@/api/types'

/** The four steps of the check-in sheet, in order. */
export const CHECKIN_STEPS = ['guest', 'room', 'deposit', 'done'] as const
export type CheckInStep = (typeof CHECKIN_STEPS)[number]

/** The methods a deposit can be taken by at check-in (the endpoint takes OTHER as well; the desk does not offer it here). */
export const DEPOSIT_METHODS = ['CASH', 'CARD', 'QRIS', 'BANK_TRANSFER'] as const
export type DepositMethod = (typeof DEPOSIT_METHODS)[number]

/** A plain amount string ("1500000", "250.50") to the smallest unit (BigInt), so sums are exact. A string that is not an amount is 0. */
export function toMinor(value: string | null | undefined, decimals: number): bigint {
  const s = (value ?? '').trim()
  const m = /^(-?)(\d{1,16})(?:\.(\d{1,4}))?$/.exec(s)
  if (!m) return 0n
  const frac = (m[3] ?? '').padEnd(decimals, '0').slice(0, decimals)
  const n = BigInt(m[2] + frac)
  return m[1] === '-' ? -n : n
}

export function fromMinor(minor: bigint, decimals: number): string {
  const neg = minor < 0n
  const digits = (neg ? -minor : minor).toString().padStart(decimals + 1, '0')
  const whole = digits.slice(0, digits.length - decimals)
  const frac = digits.slice(digits.length - decimals)
  return `${neg ? '-' : ''}${whole}${decimals > 0 ? `.${frac}` : ''}`
}

/**
 * What the desk proposes to take as a deposit: the estimate of the stay (the server's figure, with tax and service charge when it has them) plus the incidental deposit of the property,
 * less what the reservation already holds. Only a sum of figures the server gave: nothing here computes a tax. Never below zero.
 */
export function proposedDeposit(p: { estimate: string | null; incidental: string; paid: string; decimals: number }): string {
  const total = toMinor(p.estimate, p.decimals) + toMinor(p.incidental, p.decimals) - toMinor(p.paid, p.decimals)
  return fromMinor(total > 0n ? total : 0n, p.decimals)
}

/** Is this amount above zero? */
export const isPositive = (value: string, decimals: number): boolean => toMinor(value, decimals) > 0n

/** A guest of another country than Indonesia: the passport has to be reported to the immigration office (APOA). An unknown nationality is not assumed foreign. */
export const isForeign = (nationality: string | null | undefined): boolean => {
  const n = (nationality ?? '').trim().toUpperCase()
  return n !== '' && n !== 'ID'
}

export type GuestHint = 'identity' | 'phone' | 'passport'

/**
 * What the registration card would miss, as hints (none of them blocks the check-in): the identity and the phone are recommended; for a foreign guest the passport number is stressed ('passport', shown as a danger).
 * Whether the identity can become mandatory is a setting of the property for later.
 */
export function guestHints(g: { nationality?: string; id_type?: string; id_number?: string; phone?: string }): GuestHint[] {
  const out: GuestHint[] = []
  const hasId = !!g.id_number?.trim()
  if (isForeign(g.nationality)) {
    if (!hasId) out.push('passport')
  } else if (!hasId || !g.id_type?.trim()) {
    out.push('identity')
  }
  if (!g.phone?.trim()) out.push('phone')
  return out
}

/** The next arrival the desk can check in without opening anything else: confirmed, ready, not the one in the sheet and not one done in this sheet. */
export function nextReadyArrival(queue: readonly Arrival[], currentLineId: number, done: ReadonlySet<number>): Arrival | null {
  return queue.find((a) => a.status === 'CONFIRMED' && a.readiness.status === 'READY' && a.reservation_room_id !== currentLineId && !done.has(a.reservation_room_id)) ?? null
}

/** The nights of an arrival, from its dates (the estimate itself comes from the server). */
export function nightsOf(a: Pick<Arrival, 'arrival_date' | 'departure_date'>): number {
  const ms = Date.parse(`${a.departure_date}T00:00:00Z`) - Date.parse(`${a.arrival_date}T00:00:00Z`)
  return Math.max(0, Math.round(ms / 86_400_000))
}
