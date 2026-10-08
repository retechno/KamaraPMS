import { describe, expect, it } from 'vitest'
import type { Arrival } from '@/api/types'
import { arrivalActions, arrivalTarget } from './arrivals'

const arrival = (over: object = {}) =>
  ({ reservation_id: 9, reservation_room_id: 4, guest_id: 3, status: 'CONFIRMED', deposit: null, readiness: { status: 'READY', blockers: [] }, ...over }) as unknown as Arrival
const grant = (...p: string[]) => (permission: string) => p.includes(permission)

describe('arrival actions', () => {
  it('offers an action only with the permission the server asks for', () => {
    expect(arrivalActions(arrival(), grant())).toEqual([])
    expect(arrivalActions(arrival(), grant('frontdesk.checkin'))).toEqual(['checkIn'])
    expect(arrivalActions(arrival(), grant('guest.write'))).toEqual(['editGuest'])
    expect(arrivalActions(arrival(), grant('reservation.read'))).toEqual(['viewReservation'])
    expect(arrivalActions(arrival(), grant('reservation.update'))).toEqual(['editReservation'])
  })

  it('offers check-in only for a room that waits for it and is not blocked by the date', () => {
    const can = grant('frontdesk.checkin', 'reservation.update')
    expect(arrivalActions(arrival({ status: 'CHECKED_IN' }), can)).toEqual([])
    expect(arrivalActions(arrival({ status: 'CANCELLED' }), can)).toEqual([])
    expect(arrivalActions(arrival({ readiness: { status: 'BLOCKED', blockers: ['NOT_BUSINESS_DATE'] } }), can)).toEqual(['editReservation'])
    // another blocker is shown but left to the check-in, where the room is chosen
    expect(arrivalActions(arrival({ readiness: { status: 'BLOCKED', blockers: ['ROOM_NOT_ASSIGNED'] } }), can)).toEqual(['checkIn', 'editReservation'])
  })

  it('needs a guest to edit and a deposit folio for the folio and the payment', () => {
    expect(arrivalActions(arrival({ guest_id: null }), grant('guest.write'))).toEqual([])
    expect(arrivalActions(arrival(), grant('folio.read', 'payment.post'))).toEqual([])
    const dep = arrival({ deposit: { folio_id: 80, paid: '1' } })
    expect(arrivalActions(dep, grant('folio.read'))).toEqual(['folio'])
    expect(arrivalActions(dep, grant('folio.read', 'payment.post'))).toEqual(['folio', 'payment'])
    expect(arrivalTarget(dep, 'folio')).toBe('/folios/80')
    expect(arrivalTarget(dep, 'payment')).toBe('/folios/80?tab=payment')
    expect(arrivalTarget(arrival(), 'folio')).toBeUndefined()
    expect(arrivalTarget(dep, 'editReservation')).toBe('/reservations/9')
    expect(arrivalTarget(dep, 'checkIn')).toBeUndefined() // done in the sheet
  })
})
