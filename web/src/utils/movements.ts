import type { StatusVariant } from '@/utils/purchasing'

/** The variants of the Badge for the states of the movements (docs/architecture/35-inventory.md 5.4, 5.5, 5.9). */

export function requisitionVariant(status: string): StatusVariant {
  switch (status) {
    case 'SUBMITTED':
      return 'warning'
    case 'APPROVED':
    case 'FULFILLED':
      return 'success'
    case 'PARTIAL':
      return 'warning'
    case 'RETURNED':
    case 'REJECTED':
      return 'destructive'
    case 'CANCELLED':
      return 'outline'
    default:
      return 'secondary'
  }
}

export function postedVariant(status: string): StatusVariant {
  return status === 'POSTED' ? 'success' : 'outline'
}

export function countVariant(status: string): StatusVariant {
  switch (status) {
    case 'OPEN':
      return 'warning'
    case 'SUBMITTED':
      return 'secondary'
    case 'APPROVED':
      return 'success'
    default:
      return 'outline'
  }
}

export const PURPOSES = ['USE', 'WASTE', 'STAFF_MEAL', 'ENTERTAINMENT'] as const
export type Purpose = (typeof PURPOSES)[number]
export const WASTE_REASONS = ['SPOILAGE', 'EXPIRED', 'BREAKAGE', 'TASTING', 'OTHER'] as const

/** The hour a quantity was counted ("21.20"), from the instant the server stamped: the time of the property, not of the browser. */
export function hourOf(instant: string | null | undefined, formatTime: (v: string | null | undefined) => string): string {
  return instant ? formatTime(instant) : ''
}

/** The rule of an amenities line as the document says it: "1 × 78 kamar", "Suite · 1 × 12 tamu". */
export function ruleText(qtyPer: string, multiplier: number, basis: string, roomType: string | null, room: string, guest: string): string {
  const q = qtyPer.includes('.') ? qtyPer.replace(/\.?0+$/, '') : qtyPer
  const unit = basis === 'PER_GUEST' ? guest : room
  return `${roomType ? roomType + ' · ' : ''}${q} × ${multiplier} ${unit}`
}
