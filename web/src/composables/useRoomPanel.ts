import { shallowRef } from 'vue'
import { api } from '@/api/client'
import type { HousekeepingBoardRoom } from '@/api/types'

/**
 * The room panel is opened from many places (a room number in any table, the quick finder, the room status board). They share one state: which room is open. The shell shows it (RoomPanelHost).
 * `seed` is what the caller already knows about the room (a row of the room board): the panel draws it at once and fills in the rest when its summary arrives.
 */
export interface RoomPanelTarget {
  roomId: number
  number: string
  seed: HousekeepingBoardRoom | null
}

const target = shallowRef<RoomPanelTarget | null>(null)

export function openRoomPanel(roomId: number, number = '', seed: HousekeepingBoardRoom | null = null): void {
  target.value = { roomId, number, seed }
}

export function closeRoomPanel(): void {
  target.value = null
}

export function useRoomPanel() {
  return { target, open: openRoomPanel, close: closeRoomPanel }
}

// ---- the rooms of the property by number: what turns "305" into a room

const FRESH_MS = 60_000
let cache: { propertyId: number; at: number; rooms: HousekeepingBoardRoom[] } | null = null
let inflight: { propertyId: number; promise: Promise<HousekeepingBoardRoom[]> } | null = null

/**
 * The rooms of a property as the room board lists them, kept for a minute: a room number is looked up in it (a table that has only the number, the quick finder). A failed load gives no rooms.
 */
export async function roomDirectory(propertyId: number | null): Promise<HousekeepingBoardRoom[]> {
  if (propertyId === null) return []
  if (cache && cache.propertyId === propertyId && Date.now() - cache.at < FRESH_MS) return cache.rooms
  if (inflight && inflight.propertyId === propertyId) return inflight.promise
  const promise = (async () => {
    try {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/housekeeping', { params: { path: { propertyId } } })
      const rooms = data?.data ?? []
      cache = { propertyId, at: Date.now(), rooms }
      return rooms
    } catch {
      return []
    } finally {
      inflight = null
    }
  })()
  inflight = { propertyId, promise }
  return promise
}

/** The room with this number (as typed, without case or spaces), or null. */
export async function findRoomByNumber(propertyId: number | null, number: string): Promise<HousekeepingBoardRoom | null> {
  const wanted = number.trim().toLowerCase()
  if (!wanted) return null
  return (await roomDirectory(propertyId)).find((r) => r.room_number.toLowerCase() === wanted) ?? null
}

/** Forgets the rooms that were kept (another property, or a test). */
export function forgetRoomDirectory(): void {
  cache = null
  inflight = null
}
