import { uiStatus } from '@/utils/reservationStatus'

/**
 * What the front desk can do with a reservation, by its status. This is the list of the screen, not a permission: every action is checked again by the server, and one is offered only
 * when the status allows it, the person holds the permission the server asks for, and the thing it needs exists (a stay to open, a folio to go to). VOID is not here: the backend has none.
 */
export type ReservationAction = 'view' | 'edit' | 'confirm' | 'checkIn' | 'payment' | 'cancel' | 'noShow' | 'viewStay' | 'editGuest' | 'editRate' | 'folio' | 'checkOut'

/** What the actions depend on, the same whether the reservation comes from the list or from the detail. */
export interface ActionSubject {
  displayStatus: string
  /** The rooms that are not cancelled, with the date each arrives (when known). */
  rooms: { status: string; arrivalDate?: string }[]
  /** A stay of the reservation is open (a room is checked in and has its stay). */
  hasStay: boolean
  /** There is a folio to go to: the deposit folio before check-in, the folios of the stay after. */
  hasFolio: boolean
  /** The guest of the reservation is known (there is somebody to edit). */
  hasGuest: boolean
}

interface Context {
  can: (permission: string) => boolean
  /** The business date: check-in is only for a room that arrives on it, and a no-show only from it. */
  businessDate: string
}

/** The actions for one reservation, in the order they are shown. */
export function reservationActions(r: ActionSubject, c: Context): ReservationAction[] {
  const status = uiStatus(r.displayStatus)
  const can = c.can
  const waiting = r.rooms.filter((l) => l.status === 'CONFIRMED')
  const out: ReservationAction[] = []
  const add = (a: ReservationAction, ok: boolean) => {
    if (ok) out.push(a)
  }
  add('view', can('reservation.read'))
  switch (status) {
    case 'DRAFT':
      add('edit', can('reservation.update'))
      add('confirm', can('reservation.create'))
      add('cancel', can('reservation.cancel'))
      break
    case 'RESERVED':
      add('edit', can('reservation.update'))
      add('checkIn', can('frontdesk.checkin') && waiting.some((l) => !l.arrivalDate || l.arrivalDate === c.businessDate))
      add('payment', can('payment.post'))
      add('cancel', can('reservation.cancel'))
      add('noShow', can('nightaudit.no_show') && waiting.some((l) => !!l.arrivalDate && l.arrivalDate <= c.businessDate))
      break
    case 'CHECKED_IN':
      add('viewStay', can('reservation.read') && r.hasStay)
      add('editGuest', can('guest.write') && r.hasGuest)
      add('editRate', can('frontdesk.rate_change') && r.hasStay)
      add('folio', can('folio.read') && r.hasFolio)
      add('payment', can('payment.post') && can('folio.read') && r.hasFolio)
      add('checkOut', can('frontdesk.checkout') && r.hasStay)
      break
    case 'CHECKED_OUT':
      add('folio', can('folio.read') && r.hasFolio)
      break
    default: // CANCELLED, NO_SHOW: read only
      break
  }
  return out
}
