import type { TapeBooking, TapeChart, TapeRoom } from '@/api/types'
import { addDays } from '@/utils/dates'

/** What the person may do on the chart, from the permissions of the role at the property. */
export interface TapeRights {
  /** reservation.update: assign a room to a booking, change its dates. */
  update: boolean
  /** reservation.upgrade: assign a room of another type than the one booked. */
  upgrade: boolean
  /** frontdesk.room_move: move a stay that is in house. */
  move: boolean
}

/** The one segment of a stay that is in its room now: the only part of a stay that can be moved or extended. */
export const isOpenStay = (b: TapeBooking): boolean => b.status === 'CHECKED_IN' && b.open_segment
export const isConfirmed = (b: TapeBooking): boolean => b.status === 'CONFIRMED'

/** A booking can be moved to another room: assigned (or assigned for the first time) before check-in, or moved in house. */
export const canMove = (b: TapeBooking, r: TapeRights): boolean => (isConfirmed(b) ? r.update : isOpenStay(b) && r.move)
/** The dates can be changed: the arrival only before check-in, the departure before and during the stay. */
export const canChangeDates = (b: TapeBooking, r: TapeRights): boolean => (isConfirmed(b) || isOpenStay(b)) && r.update
/** The arrival can be changed (the left handle): only a booking that has not been checked in (there is no endpoint for the arrival of a stay). */
export const canChangeArrival = (b: TapeBooking, r: TapeRights): boolean => isConfirmed(b) && r.update

/** The nights a move covers: all of a booking to come, and a stay from the business date to its departure (at least one night), as the move of the server does. */
export function moveNights(b: TapeBooking, businessDate: string): { from: string; to: string } {
  if (isConfirmed(b)) return { from: b.arrival_date, to: b.departure_date }
  const from = b.arrival_date > businessDate ? b.arrival_date : businessDate
  return { from, to: b.departure_date > from ? b.departure_date : addDays(from, 1) }
}

export type TapeRefusal = 'same' | 'occupied' | 'blocked' | 'upgrade' | 'rights'
export type TapeVerdict = { ok: true; upgrade: boolean } | { ok: false; reason: TapeRefusal }

const overlaps = (aFrom: string, aTo: string, bFrom: string, bTo: string): boolean => aFrom < bTo && bFrom < aTo

/** What stands in the way in a room for nights [from, to): a booking of another line, or a block. The line itself (and the other segments of its stay) is not in the way. */
export function conflictIn(room: TapeRoom, from: string, to: string, lineId: number): 'occupied' | 'blocked' | null {
  if (room.bookings.some((x) => x.reservation_room_id !== lineId && overlaps(x.arrival_date, x.departure_date, from, to))) return 'occupied'
  if (room.blocks.some((x) => overlaps(x.start_date, x.end_date, from, to))) return 'blocked'
  return null
}

/**
 * Can this booking be dropped on this room? Judged from what the chart already holds (the other bookings and the blocks on the nights, the type booked against the type of the room, the rights of the
 * person), so the cells can be marked while the block is dragged. It is a hint: the server decides when the move is confirmed.
 */
export function dropVerdict(b: TapeBooking, room: TapeRoom, currentRoomId: number | null, businessDate: string, rights: TapeRights): TapeVerdict {
  if (!canMove(b, rights)) return { ok: false, reason: 'rights' }
  if (room.room_id === currentRoomId) return { ok: false, reason: 'same' }
  const { from, to } = moveNights(b, businessDate)
  const conflict = conflictIn(room, from, to, b.reservation_room_id)
  if (conflict) return { ok: false, reason: conflict }
  const upgrade = room.room_type_id !== b.room_type_id
  // Before check-in a room of another type is an upgrade of the booking and needs its own permission; the move of a stay in house is judged by the server.
  if (upgrade && isConfirmed(b) && !rights.upgrade) return { ok: false, reason: 'upgrade' }
  return { ok: true, upgrade }
}

export type DatesVerdict = { ok: true; nights: number } | { ok: false; reason: 'order' | 'past' | 'occupied' | 'blocked' | 'rights' }

export const nightsOf = (arrival: string, departure: string): number => Math.round((Date.parse(`${departure}T00:00:00Z`) - Date.parse(`${arrival}T00:00:00Z`)) / 86_400_000)

/** The nights that are added (or taken away) by new dates, as two windows: what the booking gains and what it loses. */
export function dateChange(b: TapeBooking, arrival: string, departure: string): { added: { from: string; to: string }[]; removed: { from: string; to: string }[] } {
  const added: { from: string; to: string }[] = []
  const removed: { from: string; to: string }[] = []
  if (arrival < b.arrival_date) added.push({ from: arrival, to: b.arrival_date })
  if (arrival > b.arrival_date) removed.push({ from: b.arrival_date, to: arrival < b.departure_date ? arrival : b.departure_date })
  if (departure > b.departure_date) added.push({ from: b.departure_date, to: departure })
  if (departure < b.departure_date) removed.push({ from: departure > b.arrival_date ? departure : b.arrival_date, to: b.departure_date })
  return { added, removed }
}

/** Are the new dates possible for this booking in its room, as far as the chart can tell? The room only has to be free for the nights that are added. */
export function datesVerdict(b: TapeBooking, room: TapeRoom | null, arrival: string, departure: string, businessDate: string, rights: TapeRights): DatesVerdict {
  if (!canChangeDates(b, rights)) return { ok: false, reason: 'rights' }
  if (departure <= arrival) return { ok: false, reason: 'order' }
  if (isOpenStay(b)) {
    if (arrival !== b.arrival_date || departure <= businessDate) return { ok: false, reason: 'past' }
  } else if (arrival !== b.arrival_date && !canChangeArrival(b, rights)) {
    return { ok: false, reason: 'rights' }
  }
  if (room) {
    for (const w of dateChange(b, arrival, departure).added) {
      const c = conflictIn(room, w.from, w.to, b.reservation_room_id)
      if (c) return { ok: false, reason: c }
    }
  }
  return { ok: true, nights: nightsOf(arrival, departure) }
}

/** The room a booking sits in on the chart (null for a booking that has no room yet). */
export function roomOf(chart: TapeChart, b: TapeBooking): TapeRoom | null {
  return chart.rooms.find((r) => r.bookings.some((x) => x === b || (x.reservation_room_id === b.reservation_room_id && x.arrival_date === b.arrival_date))) ?? null
}

/**
 * Packs bookings into as few lanes (rows) as possible, none of them overlapping: sorted by arrival, then departure, then line id (so the result never depends on the order of the input), each goes
 * into the first lane that is free on its arrival. A night is [arrival, departure), so a booking may start on the day another leaves. Greedy by arrival is optimal for intervals: the number of lanes
 * is the most bookings that share one night.
 */
export function packLanes<T extends { arrival_date: string; departure_date: string; reservation_room_id: number }>(items: readonly T[]): T[][] {
  const sorted = [...items].sort((a, b) => a.arrival_date.localeCompare(b.arrival_date) || a.departure_date.localeCompare(b.departure_date) || a.reservation_room_id - b.reservation_room_id)
  const lanes: T[][] = []
  const ends: string[] = [] // the departure of the last booking of each lane
  for (const item of sorted) {
    const lane = ends.findIndex((end) => end <= item.arrival_date)
    if (lane === -1) {
      lanes.push([item])
      ends.push(item.departure_date)
    } else {
      lanes[lane]!.push(item)
      ends[lane] = item.departure_date
    }
  }
  return lanes
}

/** How many of the bookings hold each night of `dates` (a night is [arrival, departure)). */
export const countPerNight = (bookings: readonly { arrival_date: string; departure_date: string }[], dates: readonly string[]): number[] =>
  dates.map((d) => bookings.filter((b) => b.arrival_date <= d && d < b.departure_date).length)

/** The columns a bar covers on a window that starts on `windowStart` and has `days` days: 0-based first column, and how many; null when it is outside. Clipped at the edges. */
export function columnsOf(arrival: string, departure: string, windowStart: string, days: number): { start: number; span: number; clippedLeft: boolean; clippedRight: boolean } | null {
  const first = nightsOf(windowStart, arrival)
  const last = nightsOf(windowStart, departure) // exclusive
  const start = Math.max(0, first)
  const end = Math.min(days, last)
  if (end <= start) return null
  return { start, span: end - start, clippedLeft: first < 0, clippedRight: last > days }
}
