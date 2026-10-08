import { describe, expect, it } from 'vitest'
import type { InHouseRow } from '@/api/types'
import { actionTarget, actionsFor, fullName, guestFolio } from './inHouse'

const row = (folios: object[] = [{ id: 80, folio_type: 'GUEST', status: 'OPEN' }]) =>
  ({ id: 5, reservation_id: 9, balance: { amount: '0', status: 'SETTLED', folios } }) as unknown as InHouseRow
const grant = (...p: string[]) => (permission: string) => p.includes(permission)

describe('in-house actions', () => {
  it('offers an action only with the permission the server asks for', () => {
    expect(actionsFor(row(), grant())).toEqual([])
    expect(actionsFor(row(), grant('reservation.read'))).toEqual(['view'])
    expect(actionsFor(row(), grant('guest.write', 'frontdesk.checkout'))).toEqual(['editGuest', 'checkOut'])
    expect(actionsFor(row(), grant('folio.read'))).toEqual(['folio'])
    expect(actionsFor(row(), grant('reservation.update'))).toEqual(['editStay', 'extend'])
    expect(actionsFor(row(), grant('frontdesk.room_move'))).toEqual(['moveRoom'])
  })

  it('needs an open folio and both permissions for a payment', () => {
    expect(actionsFor(row(), grant('payment.post'))).toEqual([])
    expect(actionsFor(row(), grant('payment.post', 'folio.read'))).toEqual(['folio', 'payment'])
    expect(actionsFor(row([{ id: 80, folio_type: 'GUEST', status: 'CLOSED' }]), grant('payment.post', 'folio.read'))).toEqual(['folio'])
    expect(actionsFor(row([]), grant('payment.post', 'folio.read'))).toEqual([])
  })

  it('goes to the guest folio, not a company folio', () => {
    const r = row([{ id: 81, folio_type: 'COMPANY', status: 'OPEN' }, { id: 80, folio_type: 'GUEST', status: 'OPEN' }])
    expect(guestFolio(r)?.id).toBe(80)
    expect(actionTarget(r, 'folio')).toBe('/folios/80')
    expect(actionTarget(r, 'payment')).toBe('/folios/80?tab=payment')
    expect(actionTarget(r, 'checkOut')).toBe('/stays/5?action=checkout')
    expect(actionTarget(r, 'moveRoom')).toBe('/stays/5?action=move')
    expect(actionTarget(r, 'extend')).toBe('/stays/5?action=extend')
    expect(actionTarget(r, 'editStay')).toBe('/reservations/9')
    expect(actionTarget(r, 'editGuest')).toBeUndefined() // done in place
  })

  it('writes a name as the server does', () => {
    expect(fullName({ first_name: 'Sari', last_name: 'Wijaya' })).toBe('Sari Wijaya')
    expect(fullName({ first_name: null, last_name: 'Wijaya' })).toBe('Wijaya')
  })
})
