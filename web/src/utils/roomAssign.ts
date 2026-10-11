import type { Arrival, FreeRoom, ReservationSummary } from '@/api/types'
import { isRoomReady, sortFreeRooms } from '@/utils/freeRooms'

/**
 * Putting a room on each room of a reservation that has none, in one go. A room of a reservation (a "line") to be assigned, whichever list it comes from, and the proposal for a set of them:
 * a room that is free for the nights, ready (or, when asked, not ready yet: empty and dirty or being cleaned), with the bed the guest asked for when there is one, and never a room that
 * another proposal of the same set holds for nights that overlap. Nothing here saves anything: the proposal is shown, and only what the person confirms is assigned.
 *
 * A room that a guest still occupies is never a candidate: the server does not list it as free for nights that start before the guest has left (a stay holds its room through tonight), so
 * the proposal cannot offer it, with or without the rooms that are not ready.
 */
export interface AssignLine {
  /** `${reservationId}:${lineId}`. */
  key: string
  reservationId: number
  lineId: number
  /** The version of the reservation as the row showed it. */
  version: number
  confirmation: string
  guest: string
  /** The booked type, which is the type of the rooms to choose from (never another one). */
  typeId: number
  typeCode: string
  arrival: string
  departure: string
  requestedBedTypeId: number | null
  /** The room must have that bed. */
  bedLocked: boolean
}

export interface Proposal {
  line: AssignLine
  /** The room proposed, null when none fits. */
  room: FreeRoom | null
}

export interface ProposeOptions {
  /** Also propose a room that is not ready (dirty or being cleaned), after the ones that are. */
  includeNotReady: boolean
  /** The property asks for an inspection before a check-in: then only INSPECTED is ready. */
  requiresInspection: boolean
}

export const lineKey = (reservationId: number, lineId: number): string => `${reservationId}:${lineId}`

/** What has to be asked of the server for a line: the free rooms of its type for its nights. Lines with the same question share one answer. */
export const freeRoomsKey = (l: Pick<AssignLine, 'typeId' | 'arrival' | 'departure'>): string => `${l.typeId}|${l.arrival}|${l.departure}`

export function lineOfArrival(a: Arrival): AssignLine {
  return {
    key: lineKey(a.reservation_id, a.reservation_room_id),
    reservationId: a.reservation_id,
    lineId: a.reservation_room_id,
    version: a.reservation_version,
    confirmation: a.confirmation_number,
    guest: a.guest_name ?? '',
    typeId: a.room_type_id,
    typeCode: a.room_type_code,
    arrival: a.arrival_date,
    departure: a.departure_date,
    requestedBedTypeId: a.requested_bed_type_id ?? null,
    bedLocked: !!a.bed_locked,
  }
}

type SummaryRoom = ReservationSummary['rooms'][number]

export function lineOfSummary(r: ReservationSummary, l: SummaryRoom): AssignLine {
  return {
    key: lineKey(r.id, l.id),
    reservationId: r.id,
    lineId: l.id,
    version: r.version,
    confirmation: r.confirmation_number,
    guest: r.guest_name ?? '',
    typeId: l.room_type_id,
    typeCode: l.room_type_code,
    arrival: l.arrival_date,
    departure: l.departure_date,
    requestedBedTypeId: l.requested_bed_type_id ?? null,
    bedLocked: l.bed_locked,
  }
}

/** A room of a reservation that waits for check-in and has no room: what a list offers to assign. */
export const isUnassigned = (l: Pick<SummaryRoom, 'status' | 'room_id'>): boolean => l.status === 'CONFIRMED' && l.room_id == null

const overlaps = (a: { arrival: string; departure: string }, b: { arrival: string; departure: string }): boolean => a.arrival < b.departure && b.arrival < a.departure

/** How much a line asks of its room: a kept bed first, then a bed that was asked for, then the others. */
function constraint(l: AssignLine): number {
  if (l.bedLocked) return 0
  return l.requestedBedTypeId ? 1 : 2
}

/**
 * The proposal for a set of lines. The lines with most constraints choose first (the order of the list otherwise), each takes the best room that is still free for its nights, and the answer
 * is in the order of the lines it was given. `free` holds the answer of the server for each question (`freeRoomsKey`).
 */
export function proposeAssignments(lines: readonly AssignLine[], free: ReadonlyMap<string, readonly FreeRoom[]>, options: ProposeOptions): Proposal[] {
  const taken = new Map<number, { arrival: string; departure: string }[]>() // the nights a room is already proposed for
  const order = lines.map((line, index) => ({ line, index })).sort((a, b) => constraint(a.line) - constraint(b.line) || a.index - b.index)
  const chosen = new Map<string, FreeRoom | null>()
  for (const { line } of order) {
    const rooms = (free.get(freeRoomsKey(line)) ?? []).filter((r) => {
      if (taken.get(r.room_id)?.some((n) => overlaps(n, line))) return false // never the same room twice for nights that overlap
      if (line.bedLocked && !(line.requestedBedTypeId && r.bed_type_id === line.requestedBedTypeId)) return false // a kept bed: only a room with it
      return options.includeNotReady || isRoomReady(r.housekeeping_status, options.requiresInspection)
    })
    const best = sortFreeRooms(rooms, { requestedBedTypeId: line.requestedBedTypeId, requiresInspection: options.requiresInspection })[0] ?? null
    chosen.set(line.key, best)
    if (best) taken.set(best.room_id, [...(taken.get(best.room_id) ?? []), { arrival: line.arrival, departure: line.departure }])
  }
  return lines.map((line) => ({ line, room: chosen.get(line.key) ?? null }))
}
