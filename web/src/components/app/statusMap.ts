import type { BadgeVariants } from '@/components/ui/badge'

/**
 * How a status looks, in one place. The colour of a status is its meaning, so a room that is dirty, a stay that is open
 * or a payment that was voided looks the same on every page. The label of a value is `status.<VALUE>` in the language
 * files; the domain only decides the colour, because OPEN means something different for a stay and for a work order.
 */
export type StatusDomain =
  | 'housekeeping' // a room's cleaning state
  | 'occupancy' // a room's occupancy today
  | 'reservation' // a reservation or a reserved room
  | 'stay' // a guest's stay
  | 'record' // something that is open and then closed: a folio, a business day, a period
  | 'payment' // a payment or a journal: posted or voided
  | 'work' // a task that is done by someone: maintenance, a lost item
  | 'reconciliation'
  | 'task' // a cleaning task

type Variant = NonNullable<BadgeVariants['variant']>

const MAP: Record<StatusDomain, Record<string, Variant>> = {
  housekeeping: { DIRTY: 'warning', CLEANING: 'secondary', CLEAN: 'success', INSPECTED: 'default', BLOCKED: 'destructive' },
  occupancy: { OCCUPIED: 'default', RESERVED: 'secondary', VACANT: 'outline', BLOCKED: 'destructive' },
  reservation: {
    DRAFT: 'outline',
    CONFIRMED: 'secondary',
    RESERVED: 'secondary',
    IN_HOUSE: 'default',
    CHECKED_IN: 'default',
    CHECKED_OUT: 'outline',
    COMPLETED: 'outline',
    NO_SHOW: 'warning',
    CANCELLED: 'destructive',
  },
  stay: { OPEN: 'default', CHECKED_OUT: 'outline', CANCELLED: 'destructive' },
  record: { OPEN: 'success', CLOSED: 'outline' },
  payment: { POSTED: 'success', VOIDED: 'destructive' },
  work: { OPEN: 'warning', IN_PROGRESS: 'secondary', RESOLVED: 'success', CANCELLED: 'destructive' },
  reconciliation: { OPEN: 'warning', RECONCILED: 'success' },
  task: { PENDING: 'outline', IN_PROGRESS: 'secondary', DONE: 'success', SKIPPED: 'warning' },
}

/** The badge variant of a status; a status the map does not know is shown plainly. */
export function statusVariant(domain: StatusDomain, status: string): Variant {
  return MAP[domain][status] ?? 'outline'
}

/** Whether the map knows the status (an unknown one shows its raw text). */
export function knownStatus(domain: StatusDomain, status: string): boolean {
  return status in MAP[domain]
}
