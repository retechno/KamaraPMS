/** Small helpers shared by the reservation screens. */

/** A random key for one create request; a retry of the same submit reuses it. */
export function newIdempotencyKey(): string {
  try {
    return crypto.randomUUID()
  } catch {
    return `k-${Date.now()}-${Math.random().toString(36).slice(2)}`
  }
}

/** Nights between two "YYYY-MM-DD" dates (0 for an invalid or reversed range). */
export function nightsBetween(arrival: string, departure: string): number {
  const a = Date.parse(`${arrival}T00:00:00Z`)
  const d = Date.parse(`${departure}T00:00:00Z`)
  if (Number.isNaN(a) || Number.isNaN(d) || d <= a) return 0
  return Math.round((d - a) / 86_400_000)
}

const LABELS: Record<string, string> = {
  DRAFT: 'Draft',
  CONFIRMED: 'Confirmed',
  IN_HOUSE: 'In house',
  CHECKED_IN: 'Checked in',
  CHECKED_OUT: 'Checked out',
  COMPLETED: 'Checked out',
  NO_SHOW: 'No-show',
  CANCELLED: 'Cancelled',
}

export function statusLabel(status: string): string {
  return LABELS[status] ?? status
}

export function guestLabel(g?: { first_name?: string; last_name: string }): string {
  return g ? [g.first_name, g.last_name].filter(Boolean).join(' ') : ''
}
