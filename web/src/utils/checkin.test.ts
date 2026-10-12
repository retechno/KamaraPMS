import { describe, expect, it } from 'vitest'
import type { Arrival } from '@/api/types'
import { fromMinor, guestHints, isForeign, nextReadyArrival, nightsOf, proposedDeposit, toMinor } from './checkin'

describe('amounts in the smallest unit', () => {
  it('reads and writes plain amounts exactly', () => {
    expect(toMinor('1500000', 0)).toBe(1500000n)
    expect(toMinor('250.5', 2)).toBe(25050n)
    expect(toMinor('0.1', 2) + toMinor('0.2', 2)).toBe(30n)
    expect(toMinor('abc', 0)).toBe(0n)
    expect(toMinor(null, 0)).toBe(0n)
    expect(fromMinor(25050n, 2)).toBe('250.50')
    expect(fromMinor(7n, 2)).toBe('0.07')
    expect(fromMinor(1500000n, 0)).toBe('1500000')
  })
})

describe('proposedDeposit', () => {
  it('is the estimate of the server plus the incidental, less what is already held', () => {
    expect(proposedDeposit({ estimate: '2340000', incidental: '500000', paid: '0', decimals: 0 })).toBe('2840000')
    expect(proposedDeposit({ estimate: '2340000', incidental: '500000', paid: '1000000', decimals: 0 })).toBe('1840000')
  })
  it('is the estimate alone when the property proposes no incidental, and never below zero', () => {
    expect(proposedDeposit({ estimate: '2340000', incidental: '0', paid: '0', decimals: 0 })).toBe('2340000')
    expect(proposedDeposit({ estimate: '2340000', incidental: '0', paid: '9000000', decimals: 0 })).toBe('0')
  })
  it('keeps the decimals of the currency', () => {
    expect(proposedDeposit({ estimate: '100.25', incidental: '10', paid: '0.25', decimals: 2 })).toBe('110.00')
  })
  it('is only the incidental when the estimate could not be read', () => {
    expect(proposedDeposit({ estimate: null, incidental: '500000', paid: '0', decimals: 0 })).toBe('500000')
  })
})

describe('guest hints', () => {
  it('recommends the identity and the phone of a guest from Indonesia, or of unknown nationality', () => {
    expect(guestHints({ nationality: 'ID' })).toEqual(['identity', 'phone'])
    expect(guestHints({})).toEqual(['identity', 'phone'])
    expect(guestHints({ nationality: 'ID', id_type: 'KTP', id_number: '3171', phone: '+62 812' })).toEqual([])
    expect(guestHints({ id_number: '3171', phone: '+62 812' })).toEqual(['identity'])
  })
  it('stresses the passport of a foreign guest', () => {
    expect(isForeign('NL')).toBe(true)
    expect(isForeign('id')).toBe(false)
    expect(isForeign('')).toBe(false)
    expect(guestHints({ nationality: 'NL', phone: '+31 6' })).toEqual(['passport'])
    expect(guestHints({ nationality: 'NL', id_number: 'NX1234', phone: '+31 6' })).toEqual([])
  })
})

const arrival = (id: number, over: Partial<Arrival> = {}) => ({ reservation_room_id: id, status: 'CONFIRMED', readiness: { status: 'READY', blockers: [] }, ...over }) as Arrival

describe('nextReadyArrival', () => {
  it('is the first ready one that is not the current one nor done here', () => {
    const queue = [arrival(1), arrival(2, { readiness: { status: 'BLOCKED', blockers: ['ROOM_NOT_READY'] } }), arrival(3), arrival(4)]
    expect(nextReadyArrival(queue, 1, new Set())?.reservation_room_id).toBe(3)
    expect(nextReadyArrival(queue, 1, new Set([3]))?.reservation_room_id).toBe(4)
    expect(nextReadyArrival(queue, 1, new Set([3, 4]))).toBeNull()
    expect(nextReadyArrival([arrival(5, { status: 'CHECKED_IN' })], 1, new Set())).toBeNull()
  })
})

describe('nightsOf', () => {
  it('counts the nights between the dates', () => {
    expect(nightsOf({ arrival_date: '2026-10-10', departure_date: '2026-10-13' })).toBe(3)
    expect(nightsOf({ arrival_date: '2026-12-30', departure_date: '2027-01-02' })).toBe(3)
  })
})
