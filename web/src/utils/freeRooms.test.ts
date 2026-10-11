import { describe, expect, it } from 'vitest'
import type { FreeRoom } from '@/api/types'
import { setLocale } from '@/i18n'
import { hasRequestedBed, isRoomReady, roomOptionLabel, roomRank, sortFreeRooms } from './freeRooms'

const room = (n: number, status: string, bed: number | null = null, over: Partial<FreeRoom> = {}): FreeRoom => ({ room_id: n, room_number: String(100 + n), housekeeping_status: status, bed_type_id: bed, ...over }) as FreeRoom
const numbers = (rooms: FreeRoom[]) => rooms.map((r) => r.room_number)

describe('isRoomReady', () => {
  it('is clean or inspected, and only inspected when the property asks for an inspection', () => {
    for (const s of ['CLEAN', 'INSPECTED']) expect(isRoomReady(s, false)).toBe(true)
    for (const s of ['DIRTY', 'CLEANING']) expect(isRoomReady(s, false)).toBe(false)
    expect(isRoomReady('CLEAN', true)).toBe(false)
    expect(isRoomReady('INSPECTED', true)).toBe(true)
  })
})

describe('sortFreeRooms', () => {
  const KING = 7
  const rooms = [room(1, 'DIRTY', KING), room(2, 'CLEAN', null), room(3, 'CLEANING', null), room(4, 'INSPECTED', KING), room(5, 'CLEAN', KING), room(6, 'DIRTY', null)]

  it('puts the rooms that are ready first, and among them the bed that was asked for', () => {
    // ready with the bed (104, 105 in the order of the server), ready without (102), not ready with the bed (101), not ready without (103, 106)
    expect(numbers(sortFreeRooms(rooms, { requestedBedTypeId: KING, requiresInspection: false }))).toEqual(['104', '105', '102', '101', '103', '106'])
  })

  it('keeps the order of the server when no bed was asked for: the rooms that are ready first, then the others', () => {
    expect(numbers(sortFreeRooms(rooms, { requestedBedTypeId: null, requiresInspection: false }))).toEqual(['102', '104', '105', '101', '103', '106'])
  })

  it('counts only an inspected room as ready when the property asks for an inspection', () => {
    expect(numbers(sortFreeRooms(rooms, { requestedBedTypeId: KING, requiresInspection: true }))).toEqual(['104', '101', '105', '102', '103', '106'])
  })

  it('puts the room that is on the line before all, whatever its state', () => {
    expect(numbers(sortFreeRooms(rooms, { requestedBedTypeId: KING, currentRoomId: 6, requiresInspection: false }))[0]).toBe('106')
  })

  it('does not change the list it is given', () => {
    const copy = [...rooms]
    sortFreeRooms(rooms, { requestedBedTypeId: KING, requiresInspection: false })
    expect(rooms).toEqual(copy)
  })

  it('ranks a room: current 0, ready+bed 1, ready 2, not ready+bed 3, not ready 4', () => {
    const o = { requestedBedTypeId: KING, currentRoomId: 9, requiresInspection: false }
    expect([room(9, 'DIRTY'), room(1, 'CLEAN', KING), room(2, 'CLEAN'), room(3, 'DIRTY', KING), room(4, 'DIRTY')].map((r) => roomRank(r, o))).toEqual([0, 1, 2, 3, 4])
  })
})

describe('roomOptionLabel', () => {
  it('says number, status and bed, marks the bed that was asked for, and notes a room that is not ready', () => {
    setLocale('en')
    const o = { requestedBedTypeId: 7, requiresInspection: false }
    expect(roomOptionLabel(room(1, 'CLEAN', 7, { bed_type_name: 'King' }), o)).toBe('101 · Clean · King ✓ matches the request')
    expect(roomOptionLabel(room(2, 'DIRTY', 8, { bed_type_name: 'Twin' }), o)).toContain('Twin')
    expect(roomOptionLabel(room(2, 'DIRTY', 8, { bed_type_name: 'Twin' }), o)).not.toContain('✓')
    expect(roomOptionLabel(room(2, 'DIRTY', 8, { bed_type_name: 'Twin' }), o)).toMatch(/not ready/i)
    expect(hasRequestedBed(room(1, 'CLEAN', 7), 7)).toBe(true)
    expect(hasRequestedBed(room(1, 'CLEAN', 7), null)).toBe(false)
  })
})
