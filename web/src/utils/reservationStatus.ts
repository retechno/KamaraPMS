/**
 * The status of a reservation as the reservation screens name it. The API keeps its own values (the header is DRAFT, CONFIRMED or CANCELLED; the status staff see, `display_status`, is derived from
 * the header and the rooms), and this only names them for the screen: CONFIRMED is "reserved" and IN_HOUSE is "checked in". It is the reservation's lifecycle and nothing else: the stay, the
 * folio and the occupancy of a room have their own. There is no VOID: the backend has no such status, so the screens offer none.
 */
export type ReservationUiStatus = 'DRAFT' | 'RESERVED' | 'CHECKED_IN' | 'CHECKED_OUT' | 'CANCELLED' | 'NO_SHOW'

const UI: Record<string, ReservationUiStatus> = {
  DRAFT: 'DRAFT',
  CONFIRMED: 'RESERVED',
  IN_HOUSE: 'CHECKED_IN',
  CHECKED_OUT: 'CHECKED_OUT',
  CANCELLED: 'CANCELLED',
  NO_SHOW: 'NO_SHOW',
}

/** The name of the status staff see (a `display_status`) on the reservation screens. A value the screen does not know is shown as it is. */
export function uiStatus(displayStatus: string): ReservationUiStatus | string {
  return UI[displayStatus] ?? displayStatus
}

/** The statuses of the filter, in the order of the lifecycle, with the `display_status` the API is asked for. */
export const STATUS_FILTER: { ui: ReservationUiStatus; api: 'DRAFT' | 'CONFIRMED' | 'IN_HOUSE' | 'CHECKED_OUT' | 'CANCELLED' | 'NO_SHOW' }[] = [
  { ui: 'DRAFT', api: 'DRAFT' },
  { ui: 'RESERVED', api: 'CONFIRMED' },
  { ui: 'CHECKED_IN', api: 'IN_HOUSE' },
  { ui: 'CHECKED_OUT', api: 'CHECKED_OUT' },
  { ui: 'CANCELLED', api: 'CANCELLED' },
  { ui: 'NO_SHOW', api: 'NO_SHOW' },
]

/** The `display_status` the API is asked for, from the status of the filter ('' is all). */
export function apiStatusOf(ui: string): string {
  return STATUS_FILTER.find((s) => s.ui === ui)?.api ?? ''
}

/** The name of the status of a room of a reservation (a line), the same words as the reservation: CONFIRMED is "reserved", COMPLETED is "checked out". */
export function lineUiStatus(lineStatus: string): string {
  if (lineStatus === 'CONFIRMED') return 'RESERVED'
  if (lineStatus === 'COMPLETED') return 'CHECKED_OUT'
  return lineStatus
}
