import type { Arrival } from '@/api/types'

/** What the front desk can do from a row of the arrivals list. Check-in is the existing check-in (its sheet); the rest go to the page or dialog that already does them. */
export type ArrivalAction = 'checkIn' | 'editGuest' | 'folio' | 'viewReservation' | 'editReservation' | 'payment'

export const PRIMARY_ARRIVAL_ACTIONS: ArrivalAction[] = ['checkIn', 'editGuest']
export const MORE_ARRIVAL_ACTIONS: ArrivalAction[] = ['viewReservation', 'editReservation', 'folio', 'payment']

/**
 * The actions this person may do on an arrival. The permissions are the ones the server enforces for each operation; this only decides what to offer.
 * Check-in is offered for a room that waits for it (CONFIRMED) and not when the server says the arrival is not on the business date (it would refuse: ARRIVAL_DATE_MISMATCH);
 * every other blocker is shown but left to the check-in, where the room is chosen and an unready room can be overridden with the right.
 */
export function arrivalActions(a: Arrival, can: (permission: string) => boolean): ArrivalAction[] {
  const waiting = a.status === 'CONFIRMED'
  const allowed: Record<ArrivalAction, boolean> = {
    checkIn: waiting && can('frontdesk.checkin') && !a.readiness.blockers.includes('NOT_BUSINESS_DATE'),
    editGuest: can('guest.write') && a.guest_id != null,
    folio: can('folio.read') && !!a.deposit,
    viewReservation: can('reservation.read'),
    editReservation: waiting && can('reservation.update'),
    payment: waiting && can('payment.post') && can('folio.read') && !!a.deposit,
  }
  return [...PRIMARY_ARRIVAL_ACTIONS, ...MORE_ARRIVAL_ACTIONS].filter((x) => allowed[x])
}

/** Where an action that is done on another page goes; check-in and edit guest are done in place. */
export function arrivalTarget(a: Arrival, action: ArrivalAction): string | undefined {
  switch (action) {
    case 'viewReservation':
    case 'editReservation':
      return `/reservations/${a.reservation_id}`
    case 'folio':
      return a.deposit ? `/folios/${a.deposit.folio_id}` : undefined
    case 'payment':
      return a.deposit ? `/folios/${a.deposit.folio_id}?tab=payment` : undefined
    default:
      return undefined
  }
}
