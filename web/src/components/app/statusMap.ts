import { BedDouble, Ban, CalendarClock, CircleDashed } from 'lucide-vue-next'
import type { Component } from 'vue'
import type { BadgeVariants } from '@/components/ui/badge'
import { STATUS_VARIANTS, statusFill, type StatusFill, type StatusVariant } from '@/components/ui/badge/statusFill'

/**
 * How a status looks, in one place. The colour of a status is its meaning, so a room that is dirty, a stay that is open
 * or a payment that was voided looks the same on every page. The label of a value is `status.<VALUE>` in the language
 * files; the domain only decides the colour, because OPEN means something different for a stay and for a work order.
 *
 * Teal is the colour of actions (buttons, links, the active menu) and no status has it. A status is a colour AND a weight (see `statusFill`): a light fill waits, a solid fill
 * is under way or ready, a stripe is not available. The colour is never alone: the label (and for occupancy an icon) is always shown with it.
 */
export type StatusDomain =
  | 'housekeeping' // a room's cleaning state
  | 'occupancy' // a room's occupancy today (an icon and a word, no colour)
  | 'reservation' // a reservation or a reserved room
  | 'stay' // a guest's stay
  | 'record' // something that is open and then closed: a folio, a business day, a period
  | 'payment' // a payment or a journal: posted or voided
  | 'work' // a task that is done by someone: maintenance, a lost item
  | 'reconciliation'
  | 'task' // a cleaning task
  | 'block' // a room block: out of order or out of service

type Variant = NonNullable<BadgeVariants['variant']>

const MAP: Record<StatusDomain, Record<string, Variant>> = {
  housekeeping: { DIRTY: 'dirty', CLEANING: 'cleaning', CLEAN: 'clean', INSPECTED: 'inspected', BLOCKED: 'ooo' },
  occupancy: { OCCUPIED: 'outline', RESERVED: 'outline', VACANT: 'outline', BLOCKED: 'outline' },
  reservation: {
    DRAFT: 'draft',
    CONFIRMED: 'booked',
    RESERVED: 'booked',
    IN_HOUSE: 'inhouse',
    CHECKED_IN: 'inhouse',
    CHECKED_OUT: 'closed',
    COMPLETED: 'closed',
    NO_SHOW: 'noshow',
    CANCELLED: 'cancelled',
  },
  stay: { OPEN: 'inhouse', CHECKED_OUT: 'closed', CANCELLED: 'cancelled' },
  record: { OPEN: 'success', CLOSED: 'closed' },
  payment: { POSTED: 'success', VOIDED: 'destructive' },
  work: { OPEN: 'warning', IN_PROGRESS: 'cleaning', RESOLVED: 'success', CANCELLED: 'cancelled' },
  reconciliation: { OPEN: 'warning', RECONCILED: 'success' },
  task: { PENDING: 'outline', IN_PROGRESS: 'cleaning', DONE: 'success', SKIPPED: 'warning' },
  block: { OOO: 'ooo', OOS: 'closed' },
}

/** The badge variant of a status; a status the map does not know is shown plainly. */
export function statusVariant(domain: StatusDomain, status: string): Variant {
  return MAP[domain][status] ?? 'outline'
}

/** Whether the map knows the status (an unknown one shows its raw text). */
export function knownStatus(domain: StatusDomain, status: string): boolean {
  return status in MAP[domain]
}

const NEUTRAL: StatusFill = { fill: 'border-border bg-card text-foreground', swatch: 'border-border bg-card', edge: 'border-l-border', dot: 'bg-border' }

const isStatusVariant = (v: string): v is StatusVariant => (STATUS_VARIANTS as readonly string[]).includes(v)

/**
 * The look of a status as classes, for what is not a badge: the background of a tile of a room (`fill`), the bar of a booking (`fill`), the swatch of a legend (`swatch`), the
 * colour bar of a tile with a neutral background (`edge`), a dot (`dot`). A status with no colour of its own (occupancy, a plain "success") gets the neutral look.
 */
export function statusSwatch(domain: StatusDomain, status: string): StatusFill {
  const v = statusVariant(domain, status)
  return isStatusVariant(v) ? statusFill[v] : NEUTRAL
}

export interface LegendEntry {
  status: string
  variant: Variant
  swatch: string
}

/**
 * The entries of a legend, taken from the map so that the legend and the thing it explains cannot disagree. Without `statuses` it is every status of the domain; two statuses with the
 * same look (CONFIRMED and RESERVED) are one entry, the first of them.
 */
export function statusLegend(domain: StatusDomain, statuses: readonly string[] = Object.keys(MAP[domain])): LegendEntry[] {
  const seen = new Set<Variant>()
  const out: LegendEntry[] = []
  for (const status of statuses) {
    const variant = statusVariant(domain, status)
    if (seen.has(variant)) continue
    seen.add(variant)
    out.push({ status, variant, swatch: statusSwatch(domain, status).swatch })
  }
  return out
}

/** Occupancy is told by an icon and a word, not by a colour. */
export const OCCUPANCY_ICON: Record<string, Component> = { OCCUPIED: BedDouble, RESERVED: CalendarClock, VACANT: CircleDashed, BLOCKED: Ban }
