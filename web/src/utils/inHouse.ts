import type { InHouseRow } from '@/api/types'

/** What the front desk can do from a row of the in-house list. */
export type InHouseAction = 'view' | 'editGuest' | 'folio' | 'checkOut' | 'editStay' | 'editRate' | 'moveRoom' | 'extend' | 'payment'

/** The actions shown on the row itself; the others are in its menu. */
/** What the rate editor needs to know about the stay it edits; an in-house row is one, and so is a checked-in room of a reservation. */
export interface RateEditTarget {
  id: number
  guest: { name: string }
  room: { number: string; room_type_code: string }
  rate: { rate_plan_code: string }
}

export const PRIMARY_ACTIONS: InHouseAction[] = ['view', 'editGuest', 'folio', 'checkOut']
export const MORE_ACTIONS: InHouseAction[] = ['editStay', 'editRate', 'moveRoom', 'extend', 'payment']

type Folio = InHouseRow['balance']['folios'][number]

/** The guest folio of the stay (the one payments go to), else the first folio. */
export function guestFolio(row: InHouseRow): Folio | undefined {
  const folios = row.balance.folios
  return folios.find((f) => f.folio_type === 'GUEST') ?? folios[0]
}

/**
 * The actions this person may do on a stay that is in house. The permissions are the ones the server enforces on each operation; this only decides what to offer.
 * Every row is an OPEN stay, so no action depends on another status.
 */
export function actionsFor(row: InHouseRow, can: (permission: string) => boolean): InHouseAction[] {
  const folio = guestFolio(row)
  const allowed: Record<InHouseAction, boolean> = {
    view: can('reservation.read'),
    editGuest: can('guest.write'),
    folio: can('folio.read') && !!folio,
    checkOut: can('frontdesk.checkout'),
    editStay: can('reservation.update'),
    editRate: can('frontdesk.rate_change'),
    moveRoom: can('frontdesk.room_move'),
    extend: can('reservation.update'),
    payment: can('payment.post') && can('folio.read') && folio?.status === 'OPEN',
  }
  return [...PRIMARY_ACTIONS, ...MORE_ACTIONS].filter((a) => allowed[a])
}

/** Where an action that is done on another page goes; the actions done in place (editGuest) have none. */
export function actionTarget(row: InHouseRow, action: InHouseAction): string | undefined {
  const folio = guestFolio(row)
  switch (action) {
    case 'view':
      return `/stays/${row.id}`
    case 'folio':
      return folio ? `/folios/${folio.id}` : undefined
    case 'payment':
      return folio ? `/folios/${folio.id}?tab=payment` : undefined
    case 'checkOut':
      return `/stays/${row.id}?action=checkout`
    case 'moveRoom':
      return `/stays/${row.id}?action=move`
    case 'extend':
      return `/stays/${row.id}?action=extend`
    case 'editStay':
      return `/reservations/${row.reservation_id}`
    default:
      return undefined
  }
}

/** The name the list shows for a guest after an edit: first and last name, as the server writes it. */
export function fullName(g: { first_name?: string | null; last_name: string }): string {
  return [g.first_name, g.last_name].filter(Boolean).join(' ')
}
