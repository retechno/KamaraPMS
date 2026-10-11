import { describe, expect, it } from 'vitest'
import type { Arrival, CityLedgerAccount, HousekeepingBoardRoom } from '@/api/types'
import { arrivalChip, arrivalsBlockedBy, greetingFor, isReadyToSell, listWithMore, overCreditLimit, roomCounts, roomsOf } from './today'

const room = (over: Partial<HousekeepingBoardRoom> = {}): HousekeepingBoardRoom => ({ room_id: 1, room_number: '101', room_type_id: 1, room_type_code: 'STD', room_type_name: 'Standard', status: 'CLEAN', status_updated_at: '', occupancy: 'VACANT', allowed_next: [], priority: 'NORMAL', dnd: false, make_up_requested: false, ...over }) as HousekeepingBoardRoom
const arrival = (over: Partial<Arrival> = {}): Arrival => ({ reservation_room_id: 1, room_id: 5, room_number: '201', housekeeping_status: 'CLEAN', readiness: { status: 'READY', blockers: [] }, ...over }) as Arrival
const blocked = (blockers: string[], over: Partial<Arrival> = {}): Arrival => arrival({ readiness: { status: 'BLOCKED', blockers } as never, ...over })
const account = (over: Partial<CityLedgerAccount> = {}): CityLedgerAccount => ({ company_id: 1, code: 'ACME', name: 'Acme', credit_limit: '1000', available: '500', ...over }) as CityLedgerAccount

describe('rooms', () => {
  it('are ready to sell when they are empty, not blocked, and clean or inspected', () => {
    expect(isReadyToSell(room())).toBe(true)
    expect(isReadyToSell(room({ status: 'INSPECTED' }))).toBe(true)
    expect(isReadyToSell(room({ status: 'DIRTY' }))).toBe(false)
    expect(isReadyToSell(room({ status: 'CLEANING' }))).toBe(false)
    expect(isReadyToSell(room({ occupancy: 'OCCUPIED' }))).toBe(false)
    expect(isReadyToSell(room({ block: { type: 'OOO', end_date: '2026-10-20' } as never }))).toBe(false)
  })

  it('are counted as ready, dirty and being cleaned, and a blocked room is in none of the last two', () => {
    const rooms = [room(), room({ status: 'INSPECTED' }), room({ status: 'DIRTY' }), room({ status: 'DIRTY', occupancy: 'OCCUPIED' }), room({ status: 'CLEANING' }), room({ status: 'DIRTY', block: { type: 'OOO', end_date: 'x' } as never })]
    expect(roomCounts(rooms)).toEqual({ ready: 2, dirty: 2, cleaning: 1 })
  })
})

describe('arrivals', () => {
  it('are counted by what the server says blocks them, not by the housekeeping status of the room', () => {
    const list = [
      arrival({ reservation_room_id: 1 }),
      blocked(['ROOM_NOT_READY'], { reservation_room_id: 2, housekeeping_status: 'DIRTY' }),
      blocked(['ROOM_OCCUPIED'], { reservation_room_id: 3 }), // clean, and a guest is still in it
      blocked(['ROOM_NOT_ASSIGNED'], { reservation_room_id: 4, room_id: null, room_number: '', housekeeping_status: undefined }),
    ]
    expect(arrivalsBlockedBy(list, 'ROOM_NOT_READY').map((a) => a.reservation_room_id)).toEqual([2])
    expect(arrivalsBlockedBy(list, 'ROOM_OCCUPIED').map((a) => a.reservation_room_id)).toEqual([3])
    expect(arrivalsBlockedBy(list, 'ROOM_NOT_ASSIGNED').map((a) => a.reservation_room_id)).toEqual([4])
    expect(arrivalsBlockedBy(list, 'ROOM_NOT_READY', 'ROOM_OCCUPIED').map((a) => a.reservation_room_id)).toEqual([2, 3])
    expect(roomsOf(arrivalsBlockedBy(list, 'ROOM_NOT_READY', 'ROOM_OCCUPIED'))).toEqual(['201', '201'])
  })
})

describe('the chip of an arrival', () => {
  it('is the housekeeping status of the room when nothing blocks the arrival', () => {
    expect(arrivalChip(arrival())).toEqual({ kind: 'housekeeping', status: 'CLEAN' })
    expect(arrivalChip(arrival({ housekeeping_status: 'INSPECTED' }))).toEqual({ kind: 'housekeeping', status: 'INSPECTED' })
  })

  it('is "no room" for a room that is not assigned', () => {
    expect(arrivalChip(blocked(['ROOM_NOT_ASSIGNED'], { room_id: null, housekeeping_status: undefined }))).toEqual({ kind: 'noRoom' })
  })

  it('is "waiting for check-out" for a room that is still occupied, even if the room is clean', () => {
    expect(arrivalChip(blocked(['ROOM_OCCUPIED'], { housekeeping_status: 'CLEAN' }))).toEqual({ kind: 'waitingCheckOut' })
  })

  it('is "blocked" for a room that is blocked or not available', () => {
    expect(arrivalChip(blocked(['ROOM_BLOCKED']))).toEqual({ kind: 'blocked' })
    expect(arrivalChip(blocked(['ROOM_NOT_AVAILABLE']))).toEqual({ kind: 'blocked' })
  })

  it('is the housekeeping status (dirty, being cleaned) for a room that is not ready', () => {
    expect(arrivalChip(blocked(['ROOM_NOT_READY'], { housekeeping_status: 'DIRTY' }))).toEqual({ kind: 'housekeeping', status: 'DIRTY' })
    expect(arrivalChip(blocked(['ROOM_NOT_READY'], { housekeeping_status: 'CLEANING' }))).toEqual({ kind: 'housekeeping', status: 'CLEANING' })
  })

  it('is "guest incomplete" when only the guest is missing', () => {
    expect(arrivalChip(blocked(['GUEST_MISSING']))).toEqual({ kind: 'guestMissing' })
  })

  it('takes the first blocker that matches when there are several: no room, then occupied, then blocked, then not ready, then guest', () => {
    expect(arrivalChip(blocked(['GUEST_MISSING', 'ROOM_NOT_READY', 'ROOM_OCCUPIED', 'ROOM_NOT_ASSIGNED']))).toEqual({ kind: 'noRoom' })
    expect(arrivalChip(blocked(['GUEST_MISSING', 'ROOM_NOT_READY', 'ROOM_OCCUPIED']))).toEqual({ kind: 'waitingCheckOut' })
    expect(arrivalChip(blocked(['GUEST_MISSING', 'ROOM_NOT_READY', 'ROOM_BLOCKED']))).toEqual({ kind: 'blocked' })
    expect(arrivalChip(blocked(['GUEST_MISSING', 'ROOM_NOT_READY'], { housekeeping_status: 'DIRTY' }))).toEqual({ kind: 'housekeeping', status: 'DIRTY' })
  })
})

describe('company accounts', () => {
  it('are over the limit only when they have one and nothing of it is left', () => {
    const list = [account(), account({ company_id: 2, available: '-1' }), account({ company_id: 3, credit_limit: null, available: null }), account({ company_id: 4, available: '0' })]
    expect(overCreditLimit(list).map((a) => a.company_id)).toEqual([2])
  })
})

describe('the greeting', () => {
  it('follows the hour of the property', () => {
    expect([5, 10, 11, 14, 15, 18, 19, 23, 0, 3].map(greetingFor)).toEqual(['morning', 'morning', 'afternoon', 'afternoon', 'evening', 'evening', 'night', 'night', 'night', 'night'])
  })
})

describe('listWithMore', () => {
  it('lists a few and counts the rest, without repeating a code', () => {
    expect(listWithMore(['201', '204'])).toBe('201, 204')
    expect(listWithMore(['DLX', 'FAM', 'DLX'])).toBe('DLX, FAM')
    expect(listWithMore(['1', '2', '3', '4', '5', '6', '7'])).toBe('1, 2, 3, 4, 5 +2')
    expect(listWithMore([])).toBe('')
  })
})
