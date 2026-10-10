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

/** The arrivals of today whose room is assigned and is not clean yet (dirty or being cleaned), by room number. */
export const arrivalsWithUnreadyRoom = (arrivals: Arrival[]): Arrival[] =>
  arrivals.filter((a) => !!a.room_id && (a.housekeeping_status === 'DIRTY' || a.housekeeping_status === 'CLEANING'))

/** The arrivals of today that still have no room. */
export const arrivalsWithoutRoom = (arrivals: Arrival[]): Arrival[] => arrivals.filter((a) => !a.room_id)

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
