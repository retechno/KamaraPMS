import { describe, expect, it } from 'vitest'
import { reservationActions, type ActionSubject } from './reservationActions'
import { apiStatusOf, lineUiStatus, STATUS_FILTER, uiStatus } from './reservationStatus'

const subject = (over: Partial<ActionSubject> = {}): ActionSubject => ({
  displayStatus: 'CONFIRMED', rooms: [{ status: 'CONFIRMED', arrivalDate: '2026-10-02' }], hasStay: false, hasFolio: false, hasGuest: true, ...over,
})
const all = () => true
const grant = (...p: string[]) => (permission: string) => p.includes(permission)
const ctx = (can: (p: string) => boolean) => ({ can, businessDate: '2026-10-02' })

describe('reservation status names', () => {
  it('names the status staff see without changing what the API says', () => {
    expect(['DRAFT', 'CONFIRMED', 'IN_HOUSE', 'CHECKED_OUT', 'CANCELLED', 'NO_SHOW'].map(uiStatus)).toEqual(['DRAFT', 'RESERVED', 'CHECKED_IN', 'CHECKED_OUT', 'CANCELLED', 'NO_SHOW'])
    expect(uiStatus('SOMETHING_NEW')).toBe('SOMETHING_NEW') // an unknown value is shown as it is, never made into another status
  })

  it('maps each status of the filter to a status of the API, and has no void', () => {
    expect(STATUS_FILTER.map((s) => apiStatusOf(s.ui))).toEqual(['DRAFT', 'CONFIRMED', 'IN_HOUSE', 'CHECKED_OUT', 'CANCELLED', 'NO_SHOW'])
    expect(apiStatusOf('')).toBe('')
    expect(apiStatusOf('VOID')).toBe('')
    expect(STATUS_FILTER.some((s) => s.ui === ('VOID' as never))).toBe(false)
  })
})

describe('room status names', () => {
  it('names the status of a room in the words of the reservation', () => {
    expect(['CONFIRMED', 'COMPLETED', 'CHECKED_IN', 'CANCELLED', 'NO_SHOW', 'DRAFT'].map(lineUiStatus)).toEqual(['RESERVED', 'CHECKED_OUT', 'CHECKED_IN', 'CANCELLED', 'NO_SHOW', 'DRAFT'])
  })
})

describe('reservation actions by status', () => {
  it('a draft can be edited, confirmed and cancelled, and cannot check in or take a no-show', () => {
    expect(reservationActions(subject({ displayStatus: 'DRAFT', rooms: [{ status: 'DRAFT' }] }), ctx(all))).toEqual(['view', 'edit', 'confirm', 'cancel'])
  })

  it('a reserved reservation can check in on its arrival date, take a payment, be cancelled or marked no-show', () => {
    expect(reservationActions(subject(), ctx(all))).toEqual(['view', 'edit', 'checkIn', 'payment', 'cancel', 'noShow'])
    const later = subject({ rooms: [{ status: 'CONFIRMED', arrivalDate: '2026-10-09' }] })
    expect(reservationActions(later, ctx(all))).toEqual(['view', 'edit', 'payment', 'cancel']) // not on the business date: no check-in, no no-show
    const past = subject({ rooms: [{ status: 'CONFIRMED', arrivalDate: '2026-10-01' }] })
    expect(reservationActions(past, ctx(all))).toContain('noShow')
  })

  it('a checked-in reservation works on its stay, its guest, its rate and its folio', () => {
    const inHouse = subject({ displayStatus: 'IN_HOUSE', rooms: [{ status: 'CHECKED_IN' }], hasStay: true, hasFolio: true })
    expect(reservationActions(inHouse, ctx(all))).toEqual(['view', 'viewStay', 'editGuest', 'editRate', 'folio', 'payment', 'checkOut'])
    // the things an action needs must exist
    expect(reservationActions({ ...inHouse, hasStay: false, hasFolio: false }, ctx(all))).toEqual(['view', 'editGuest'])
  })

  it('a checked-out reservation is looked at, with its folio when there is one', () => {
    expect(reservationActions(subject({ displayStatus: 'CHECKED_OUT', rooms: [{ status: 'COMPLETED' }], hasFolio: true }), ctx(all))).toEqual(['view', 'folio'])
    expect(reservationActions(subject({ displayStatus: 'CHECKED_OUT', rooms: [{ status: 'COMPLETED' }] }), ctx(all))).toEqual(['view'])
  })

  it('a cancelled or no-show reservation is read only', () => {
    expect(reservationActions(subject({ displayStatus: 'CANCELLED', rooms: [{ status: 'CANCELLED' }] }), ctx(all))).toEqual(['view'])
    expect(reservationActions(subject({ displayStatus: 'NO_SHOW', rooms: [{ status: 'NO_SHOW' }] }), ctx(all))).toEqual(['view'])
  })

  it('offers an action only with the permission the server asks for', () => {
    expect(reservationActions(subject(), ctx(grant()))).toEqual([])
    expect(reservationActions(subject(), ctx(grant('reservation.read')))).toEqual(['view'])
    expect(reservationActions(subject(), ctx(grant('reservation.read', 'frontdesk.checkin')))).toEqual(['view', 'checkIn'])
    expect(reservationActions(subject({ displayStatus: 'DRAFT', rooms: [{ status: 'DRAFT' }] }), ctx(grant('reservation.create')))).toEqual(['confirm'])
  })
})
