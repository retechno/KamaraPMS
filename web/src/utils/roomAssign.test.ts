import { describe, expect, it } from 'vitest'
import type { Arrival, FreeRoom, ReservationSummary } from '@/api/types'
import { type AssignLine, freeRoomsKey, isUnassigned, lineKey, lineOfArrival, lineOfSummary, proposeAssignments } from './roomAssign'

const KING = 7
const TWIN = 8
const room = (id: number, status: string, bed: number | null = null): FreeRoom => ({ room_id: id, room_number: String(100 + id), housekeeping_status: status, bed_type_id: bed }) as FreeRoom
const line = (n: number, over: Partial<AssignLine> = {}): AssignLine => ({
  key: lineKey(n, n * 10), reservationId: n, lineId: n * 10, version: 1, confirmation: `RES00000${n}`, guest: `Guest ${n}`, typeId: 1, typeCode: 'DLX',
  arrival: '2026-10-10', departure: '2026-10-12', requestedBedTypeId: null, bedLocked: false, ...over,
})
const plan = (lines: AssignLine[], rooms: FreeRoom[], includeNotReady = false, requiresInspection = false) =>
  proposeAssignments(lines, new Map(lines.map((l) => [freeRoomsKey(l), rooms])), { includeNotReady, requiresInspection }).map((p) => p.room?.room_number ?? null)
const KEPT = { requestedBedTypeId: KING, bedLocked: true }

describe('proposeAssignments, ready rooms only', () => {
  it('proposes the first ready room to each line, and never the same room twice', () => {
    expect(plan([line(1), line(2), line(3)], [room(1, 'CLEAN'), room(2, 'INSPECTED'), room(3, 'CLEAN')])).toEqual(['101', '102', '103'])
  })

  it('leaves a line without a room when the ready rooms are used up, and does not propose a room that is not ready', () => {
    expect(plan([line(1), line(2)], [room(1, 'CLEAN'), room(2, 'DIRTY'), room(3, 'CLEANING')])).toEqual(['101', null])
  })

  it('takes the bed that was asked for first, and a room of another bed when none is left', () => {
    const rooms = [room(1, 'CLEAN', TWIN), room(2, 'CLEAN', KING)]
    expect(plan([line(1, { requestedBedTypeId: KING })], rooms)).toEqual(['102'])
    expect(plan([line(1, { requestedBedTypeId: KING }), line(2, { requestedBedTypeId: KING })], rooms)).toEqual(['102', '101'])
  })

  it('lets a line with a kept bed choose before the others, and gives it only a room with that bed, or none', () => {
    const rooms = [room(1, 'CLEAN', KING), room(2, 'CLEAN', TWIN)]
    // the first line has no preference: it would take 101, but the second keeps the king and chooses first
    expect(plan([line(1), line(2, KEPT)], rooms)).toEqual(['102', '101'])
    expect(plan([line(1, KEPT)], [room(2, 'CLEAN', TWIN)])).toEqual([null])
  })

  it('lets a line with a bed that was asked for choose before a line with none', () => {
    const rooms = [room(1, 'CLEAN', KING), room(2, 'CLEAN', TWIN)]
    expect(plan([line(1), line(2, { requestedBedTypeId: KING })], rooms)).toEqual(['102', '101'])
  })

  it('keeps the order of the lines in the answer', () => {
    const lines = [line(1), line(2, KEPT), line(3)]
    const out = proposeAssignments(lines, new Map(lines.map((l) => [freeRoomsKey(l), [room(1, 'CLEAN', KING), room(2, 'CLEAN'), room(3, 'CLEAN')]])), { includeNotReady: false, requiresInspection: false })
    expect(out.map((p) => p.line.key)).toEqual(lines.map((l) => l.key))
  })

  it('counts only an inspected room as ready when the property asks for an inspection', () => {
    expect(plan([line(1), line(2)], [room(1, 'CLEAN'), room(2, 'INSPECTED')], false, true)).toEqual(['102', null])
  })

  it('shares a room between two lines whose nights do not overlap, and not between two that do', () => {
    const a = line(1, { arrival: '2026-10-10', departure: '2026-10-12' })
    const b = line(2, { arrival: '2026-10-12', departure: '2026-10-14' }) // starts the day a ends: the room is free again
    const c = line(3, { arrival: '2026-10-11', departure: '2026-10-13' }) // overlaps both
    const free = new Map([[freeRoomsKey(a), [room(1, 'CLEAN')]], [freeRoomsKey(b), [room(1, 'CLEAN')]], [freeRoomsKey(c), [room(1, 'CLEAN')]]])
    const out = proposeAssignments([a, b, c], free, { includeNotReady: false, requiresInspection: false })
    expect(out.map((p) => p.room?.room_number ?? null)).toEqual(['101', '101', null])
  })

  it('takes the rooms that were read for each type: a line of another type gets a room of its own list', () => {
    const a = line(1, { typeId: 1 })
    const b = line(2, { typeId: 2, typeCode: 'STD' })
    const free = new Map([[freeRoomsKey(a), [room(1, 'CLEAN')]], [freeRoomsKey(b), [room(9, 'CLEAN')]]])
    expect(proposeAssignments([a, b], free, { includeNotReady: false, requiresInspection: false }).map((p) => p.room?.room_number)).toEqual(['101', '109'])
  })

  it('gives nothing to a line whose rooms were not read', () => {
    expect(proposeAssignments([line(1)], new Map(), { includeNotReady: true, requiresInspection: false })[0]?.room).toBeNull()
  })
})

describe('proposeAssignments, with the rooms that are not ready', () => {
  const rooms = [room(1, 'DIRTY', KING), room(2, 'CLEAN'), room(3, 'CLEANING'), room(4, 'INSPECTED', KING)]

  it('proposes the ready rooms first and then the rooms that are dirty or being cleaned', () => {
    expect(plan([line(1), line(2), line(3), line(4)], rooms, true)).toEqual(['102', '104', '101', '103'])
  })

  it('takes the bed that was asked for among the ready rooms, and a ready room of another bed before a room that is not ready (ready first, the bed second)', () => {
    expect(plan([line(1, { requestedBedTypeId: KING }), line(2, { requestedBedTypeId: KING })], rooms, true)).toEqual(['104', '102'])
    // when no ready room is left, the bed decides again among the rooms that are not ready
    expect(plan([line(1, { requestedBedTypeId: KING }), line(2, { requestedBedTypeId: KING }), line(3, { requestedBedTypeId: KING }), line(4, { requestedBedTypeId: KING })], rooms, true)).toEqual(['104', '102', '101', '103'])
  })

  it('still gives a kept bed only a room with that bed, ready first', () => {
    expect(plan([line(1, KEPT), line(2, KEPT), line(3, KEPT)], rooms, true)).toEqual(['104', '101', null])
  })

  it('has no candidate other than the rooms the server lists: a room that a guest still occupies is not one of them', () => {
    expect(plan([line(1)], [], true)).toEqual([null])
  })
})

describe('lines of a list', () => {
  const arrival = {
    reservation_id: 41, reservation_room_id: 90, reservation_version: 5, confirmation_number: 'RES000041', guest_name: 'Ana', room_type_id: 3, room_type_code: 'DLX',
    arrival_date: '2026-10-10', departure_date: '2026-10-12', requested_bed_type_id: 7, bed_locked: true,
  } as Arrival

  it('is made of an arrival', () => {
    expect(lineOfArrival(arrival)).toEqual({
      key: '41:90', reservationId: 41, lineId: 90, version: 5, confirmation: 'RES000041', guest: 'Ana', typeId: 3, typeCode: 'DLX', arrival: '2026-10-10', departure: '2026-10-12', requestedBedTypeId: 7, bedLocked: true,
    })
  })

  it('is made of a room of a reservation of the list, with the version of the reservation', () => {
    const r = { id: 41, version: 6, confirmation_number: 'RES000041', guest_name: 'Ana', rooms: [] } as unknown as ReservationSummary
    const l = { id: 90, status: 'CONFIRMED', room_id: null, room_type_id: 3, room_type_code: 'DLX', arrival_date: '2026-10-10', departure_date: '2026-10-12', requested_bed_type_id: null, bed_locked: false } as ReservationSummary['rooms'][number]
    expect(lineOfSummary(r, l)).toMatchObject({ key: '41:90', version: 6, typeId: 3, requestedBedTypeId: null, bedLocked: false })
  })

  it('has to wait for check-in and have no room to be offered a room', () => {
    expect(isUnassigned({ status: 'CONFIRMED', room_id: null })).toBe(true)
    expect(isUnassigned({ status: 'CONFIRMED', room_id: 5 })).toBe(false)
    expect(isUnassigned({ status: 'CHECKED_IN', room_id: null })).toBe(false)
    expect(isUnassigned({ status: 'CANCELLED', room_id: null })).toBe(false)
  })

  it('shares one question between lines of the same type and nights', () => {
    expect(freeRoomsKey(line(1))).toBe(freeRoomsKey(line(2)))
    expect(freeRoomsKey(line(1))).not.toBe(freeRoomsKey(line(2, { departure: '2026-10-13' })))
  })
})
