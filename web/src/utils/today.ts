import type { Arrival, CityLedgerAccount, HousekeepingBoardRoom } from '@/api/types'

/**
 * The rules of the "Today" page, apart from the screen: what counts as a room ready to sell, which arrivals need attention, how the greeting follows the hour. They read what the
 * lists of the front desk already answer; nothing here is a number of money.
 */

/** A room that can be sold now: empty, not blocked, and clean or inspected. */
export const isReadyToSell = (r: HousekeepingBoardRoom): boolean => r.occupancy === 'VACANT' && !r.block && (r.status === 'CLEAN' || r.status === 'INSPECTED')

export interface RoomCounts {
  ready: number
  dirty: number
  cleaning: number
}

export function roomCounts(rooms: HousekeepingBoardRoom[]): RoomCounts {
  return {
    ready: rooms.filter(isReadyToSell).length,
    // a blocked room is not waiting for housekeeping: it is out of the count
    dirty: rooms.filter((r) => !r.block && r.status === 'DIRTY').length,
    cleaning: rooms.filter((r) => !r.block && r.status === 'CLEANING').length,
  }
}

type Blocker = Arrival['readiness']['blockers'][number]

/** What the server says holds an arrival back from check-in (`readiness.blockers`): the arrivals that have at least one of these. */
export const arrivalsBlockedBy = (arrivals: Arrival[], ...blockers: Blocker[]): Arrival[] => arrivals.filter((a) => a.readiness.blockers.some((b) => blockers.includes(b)))

/** The rooms (numbers) of arrivals, for a line of a few. */
export const roomsOf = (arrivals: Arrival[]): string[] => arrivals.map((a) => a.room_number ?? '')

/**
 * How an arrival is shown in the column: the chip is the first blocker that matches, in this order, and when nothing blocks it is the housekeeping status of the room (clean or
 * inspected). `status` is the housekeeping status for the chips that are one. The check-in button is the main one only for a ready arrival.
 */
export type ArrivalChip =
  | { kind: 'noRoom' }
  | { kind: 'waitingCheckOut' }
  | { kind: 'blocked' }
  | { kind: 'housekeeping'; status: string }
  | { kind: 'guestMissing' }
  | { kind: 'none' }

export function arrivalChip(a: Arrival): ArrivalChip {
  const has = (...blockers: Blocker[]): boolean => a.readiness.blockers.some((b) => blockers.includes(b))
  if (has('ROOM_NOT_ASSIGNED')) return { kind: 'noRoom' }
  if (has('ROOM_OCCUPIED')) return { kind: 'waitingCheckOut' }
  if (has('ROOM_BLOCKED', 'ROOM_NOT_AVAILABLE')) return { kind: 'blocked' }
  if (has('ROOM_NOT_READY')) return a.housekeeping_status ? { kind: 'housekeeping', status: a.housekeeping_status } : { kind: 'none' }
  if (has('GUEST_MISSING')) return { kind: 'guestMissing' }
  if (a.readiness.status === 'READY' && a.housekeeping_status) return { kind: 'housekeeping', status: a.housekeeping_status }
  // a line that has no room by the numbers, though the server named no blocker (it never does): still told as without room
  if (!a.room_id) return { kind: 'noRoom' }
  return a.housekeeping_status ? { kind: 'housekeeping', status: a.housekeeping_status } : { kind: 'none' }
}

/** A company account over its limit: it has one and what is left of it is below zero. */
export const overCreditLimit = (accounts: CityLedgerAccount[]): CityLedgerAccount[] => accounts.filter((a) => a.credit_limit !== null && a.available !== null && Number(a.available) < 0)

export type Greeting = 'morning' | 'afternoon' | 'evening' | 'night'

/** The greeting for an hour of the property's day (0 to 23). */
export function greetingFor(hour: number): Greeting {
  if (hour >= 4 && hour < 11) return 'morning'
  if (hour >= 11 && hour < 15) return 'afternoon'
  if (hour >= 15 && hour < 19) return 'evening'
  return 'night'
}

/** The rooms or codes of a list as a short sentence part: "201, 204, 305 +2". */
export function listWithMore(items: string[], shown = 5): string {
  const unique = [...new Set(items.filter(Boolean))]
  return unique.length <= shown ? unique.join(', ') : `${unique.slice(0, shown).join(', ')} +${unique.length - shown}`
}
