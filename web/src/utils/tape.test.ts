import { describe, expect, it } from 'vitest'
import type { TapeBooking, TapeRoom } from '@/api/types'
import { canChangeArrival, canChangeDates, canMove, columnsOf, conflictIn, countPerNight, dateChange, datesVerdict, dropVerdict, moveNights, nightsOf, packLanes } from './tape'

const rights = { update: true, upgrade: true, move: true }
const booking = (over: Partial<TapeBooking> = {}) => ({ reservation_id: 9, confirmation_number: 'RES9', reservation_room_id: 4, room_type_id: 10, stay_id: null, open_segment: false, status: 'CONFIRMED', arrival_date: '2026-10-05', departure_date: '2026-10-08', ...over }) as TapeBooking
const room = (id: number, type: number, bookings: TapeBooking[] = [], blocks: TapeRoom['blocks'] = []) => ({ room_id: id, room_number: String(id), room_type_id: type, room_type_code: 'T', bookings, blocks }) as TapeRoom
const stay = (over: Partial<TapeBooking> = {}) => booking({ status: 'CHECKED_IN', stay_id: 3, open_segment: true, arrival_date: '2026-10-03', departure_date: '2026-10-08', ...over })

describe('what the role may do with a booking', () => {
  it('moves a booking to come with reservation.update and a stay in house with frontdesk.room_move', () => {
    expect(canMove(booking(), rights)).toBe(true)
    expect(canMove(booking(), { ...rights, update: false })).toBe(false)
    expect(canMove(stay(), rights)).toBe(true)
    expect(canMove(stay(), { ...rights, move: false })).toBe(false)
    expect(canMove(stay({ open_segment: false }), rights)).toBe(false) // a closed segment is history
  })
  it('changes the departure of both and the arrival only before check-in', () => {
    expect(canChangeDates(booking(), rights)).toBe(true)
    expect(canChangeDates(stay(), rights)).toBe(true)
    expect(canChangeDates(stay({ open_segment: false }), rights)).toBe(false)
    expect(canChangeArrival(booking(), rights)).toBe(true)
    expect(canChangeArrival(stay(), rights)).toBe(false)
  })
})

describe('the nights of a move', () => {
  it('are all the nights of a booking to come, and a stay from the business date, at least one night', () => {
    expect(moveNights(booking(), '2026-10-01')).toEqual({ from: '2026-10-05', to: '2026-10-08' })
    expect(moveNights(stay(), '2026-10-05')).toEqual({ from: '2026-10-05', to: '2026-10-08' })
    expect(moveNights(stay({ departure_date: '2026-10-05' }), '2026-10-05')).toEqual({ from: '2026-10-05', to: '2026-10-06' })
  })
})

describe('dropVerdict', () => {
  const free = room(2, 10)
  it('accepts a free room of the booked type', () => {
    expect(dropVerdict(booking(), free, 1, '2026-10-01', rights)).toEqual({ ok: true, upgrade: false })
  })
  it('refuses the room it is in, a booking in the way (not the line itself) and a block', () => {
    expect(dropVerdict(booking(), room(1, 10), 1, '2026-10-01', rights)).toEqual({ ok: false, reason: 'same' })
    expect(dropVerdict(booking(), room(2, 10, [booking({ reservation_room_id: 7, arrival_date: '2026-10-07', departure_date: '2026-10-09' })]), 1, '2026-10-01', rights)).toEqual({ ok: false, reason: 'occupied' })
    expect(dropVerdict(booking(), room(2, 10, [booking({ reservation_room_id: 7, arrival_date: '2026-10-08', departure_date: '2026-10-09' })]), 1, '2026-10-01', rights).ok).toBe(true) // it leaves on the night the other arrives
    expect(dropVerdict(booking(), room(2, 10, [], [{ id: 1, block_type: 'OOO', start_date: '2026-10-06', end_date: '2026-10-07' }]), 1, '2026-10-01', rights)).toEqual({ ok: false, reason: 'blocked' })
  })
  it('marks another type as an upgrade, and refuses it without reservation.upgrade before check-in', () => {
    expect(dropVerdict(booking(), room(2, 11), 1, '2026-10-01', rights)).toEqual({ ok: true, upgrade: true })
    expect(dropVerdict(booking(), room(2, 11), 1, '2026-10-01', { ...rights, upgrade: false })).toEqual({ ok: false, reason: 'upgrade' })
    // the move of a stay in house is judged by the server: the mark says upgrade, the permission of the booking is not asked
    expect(dropVerdict(stay(), room(2, 11), 1, '2026-10-05', { ...rights, upgrade: false })).toEqual({ ok: true, upgrade: true })
  })
  it('judges an upgraded line by the type it was booked in, not the type of its room', () => {
    expect(dropVerdict(booking({ room_type_id: 10 }), room(2, 10), 1, '2026-10-01', { ...rights, upgrade: false })).toEqual({ ok: true, upgrade: false })
  })
  it('does not look at the nights of a stay that are past', () => {
    const past = room(2, 10, [booking({ reservation_room_id: 7, arrival_date: '2026-10-03', departure_date: '2026-10-05' })])
    expect(dropVerdict(stay(), past, 1, '2026-10-05', rights).ok).toBe(true)
  })
})

describe('dates', () => {
  it('names the nights that are gained and lost', () => {
    const b = booking()
    expect(dateChange(b, '2026-10-05', '2026-10-10')).toEqual({ added: [{ from: '2026-10-08', to: '2026-10-10' }], removed: [] })
    expect(dateChange(b, '2026-10-05', '2026-10-06')).toEqual({ added: [], removed: [{ from: '2026-10-06', to: '2026-10-08' }] })
    expect(dateChange(b, '2026-10-04', '2026-10-08')).toEqual({ added: [{ from: '2026-10-04', to: '2026-10-05' }], removed: [] })
    expect(dateChange(b, '2026-10-06', '2026-10-08')).toEqual({ added: [], removed: [{ from: '2026-10-05', to: '2026-10-06' }] })
  })
  it('accepts a longer stay when the room is free for the added nights, and refuses it when it is not', () => {
    const own = booking()
    const free = room(1, 10, [own])
    expect(datesVerdict(own, free, '2026-10-05', '2026-10-10', '2026-10-01', rights)).toEqual({ ok: true, nights: 5 })
    const busy = room(1, 10, [own, booking({ reservation_room_id: 7, arrival_date: '2026-10-09', departure_date: '2026-10-12' })])
    expect(datesVerdict(own, busy, '2026-10-05', '2026-10-10', '2026-10-01', rights)).toEqual({ ok: false, reason: 'occupied' })
    expect(datesVerdict(own, busy, '2026-10-05', '2026-10-09', '2026-10-01', rights).ok).toBe(true)
    const blocked = room(1, 10, [own], [{ id: 1, block_type: 'OOS', start_date: '2026-10-08', end_date: '2026-10-09' }])
    expect(datesVerdict(own, blocked, '2026-10-05', '2026-10-10', '2026-10-01', rights)).toEqual({ ok: false, reason: 'blocked' })
  })
  it('needs at least one night, keeps the arrival of a stay, and a departure after the business date', () => {
    const s = stay()
    expect(datesVerdict(booking(), null, '2026-10-05', '2026-10-05', '2026-10-01', rights)).toEqual({ ok: false, reason: 'order' })
    expect(datesVerdict(s, null, '2026-10-04', '2026-10-08', '2026-10-05', rights)).toEqual({ ok: false, reason: 'past' })
    expect(datesVerdict(s, null, '2026-10-03', '2026-10-05', '2026-10-05', rights)).toEqual({ ok: false, reason: 'past' })
    expect(datesVerdict(s, null, '2026-10-03', '2026-10-06', '2026-10-05', rights)).toEqual({ ok: true, nights: 3 })
  })
  it('does not know a room for a booking without one: the server decides', () => {
    expect(datesVerdict(booking(), null, '2026-10-05', '2026-10-12', '2026-10-01', rights).ok).toBe(true)
  })
})

describe('columns', () => {
  it('place a bar on the window, clipped at the edges', () => {
    expect(columnsOf('2026-10-05', '2026-10-08', '2026-10-01', 14)).toEqual({ start: 4, span: 3, clippedLeft: false, clippedRight: false })
    expect(columnsOf('2026-09-28', '2026-10-03', '2026-10-01', 14)).toEqual({ start: 0, span: 2, clippedLeft: true, clippedRight: false })
    expect(columnsOf('2026-10-13', '2026-10-20', '2026-10-01', 14)).toEqual({ start: 12, span: 2, clippedLeft: false, clippedRight: true })
    expect(columnsOf('2026-10-20', '2026-10-22', '2026-10-01', 14)).toBeNull()
    expect(nightsOf('2026-12-30', '2027-01-02')).toBe(3)
  })
  it('conflictIn ignores the line itself', () => {
    const own = booking()
    expect(conflictIn(room(1, 10, [own]), '2026-10-05', '2026-10-08', own.reservation_room_id)).toBeNull()
  })
})

describe('packing the bookings that have no room into lanes', () => {
  const b = (id: number, from: string, to: string) => booking({ reservation_room_id: id, arrival_date: from, departure_date: to })
  const ids = (lanes: TapeBooking[][]) => lanes.map((l) => l.map((x) => x.reservation_room_id))

  it('puts none of them on top of another and uses as many lanes as the most that share a night', () => {
    const items = [b(1, '2026-10-03', '2026-10-05'), b(2, '2026-10-04', '2026-10-06'), b(3, '2026-10-05', '2026-10-07'), b(4, '2026-10-06', '2026-10-08'), b(5, '2026-10-08', '2026-10-09')]
    const lanes = packLanes(items)
    expect(ids(lanes)).toEqual([[1, 3, 5], [2, 4]])
    for (const lane of lanes) lane.slice(1).forEach((x, i) => expect(x.arrival_date >= lane[i]!.departure_date).toBe(true))
    expect(lanes.length).toBe(Math.max(...['2026-10-03', '2026-10-04', '2026-10-05', '2026-10-06', '2026-10-08'].map((d) => countPerNight(items, [d])[0]!)))
  })
  it('lets a booking start on the day another leaves', () => {
    expect(ids(packLanes([b(1, '2026-10-01', '2026-10-03'), b(2, '2026-10-03', '2026-10-05')]))).toEqual([[1, 2]])
  })
  it('gives the same lanes whatever the order of the input', () => {
    const items = [b(7, '2026-10-02', '2026-10-06'), b(3, '2026-10-02', '2026-10-04'), b(5, '2026-10-03', '2026-10-05'), b(1, '2026-10-02', '2026-10-04')]
    const a = ids(packLanes(items))
    expect(ids(packLanes([...items].reverse()))).toEqual(a)
    expect(ids(packLanes([items[2]!, items[0]!, items[3]!, items[1]!]))).toEqual(a)
  })
  it('is empty for nothing and does not change its input', () => {
    expect(packLanes([])).toEqual([])
    const items = [b(2, '2026-10-05', '2026-10-06'), b(1, '2026-10-01', '2026-10-02')]
    packLanes(items)
    expect(items.map((x) => x.reservation_room_id)).toEqual([2, 1])
  })
})

describe('the bookings of each night', () => {
  it('counts a booking on its nights [arrival, departure) only', () => {
    expect(countPerNight([booking({ arrival_date: '2026-10-02', departure_date: '2026-10-04' }), booking({ arrival_date: '2026-10-03', departure_date: '2026-10-04' })], ['2026-10-01', '2026-10-02', '2026-10-03', '2026-10-04'])).toEqual([0, 1, 2, 0])
  })
})
