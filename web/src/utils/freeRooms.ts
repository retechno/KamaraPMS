import type { FreeRoom } from '@/api/types'
import { t } from '@/i18n'
import { statusText } from '@/utils/status'

/**
 * The free rooms of a type for a period, as a person chooses from them. One order for every place that offers them (the check-in, the reservation page, the picker of a list):
 * the room that is already on the line first, then the rooms that are ready (a room that is not ready cannot be checked into without an override), among them the rooms with the
 * bed the guest asked for first, then the rooms that are not ready, in the same way. The sort is stable, so the rooms keep the order the server gave them (floor and number) otherwise.
 */
export interface RoomOrder {
  /** The bed the guest asked for (null: none). */
  requestedBedTypeId?: number | null
  /** The room that is on the line now. */
  currentRoomId?: number | null
  /** The property asks for an inspection before a check-in: then only INSPECTED is ready. */
  requiresInspection: boolean
  /** The guest comes after the business date: the housekeeping status of today says nothing about that night, so it does not order the rooms. */
  ignoreReadiness?: boolean
}

/** An arrival after the business date (the housekeeping status of today is not relevant to it). */
export const isAfterBusinessDate = (arrival: string, businessDate: string): boolean => businessDate !== '' && arrival > businessDate

type RoomLike = Pick<FreeRoom, 'room_id' | 'housekeeping_status'> & { bed_type_id?: number | null }

/** A room that a guest can be checked into as it is: clean or inspected (inspected only, when the property asks for an inspection). */
export const isRoomReady = (status: string, requiresInspection: boolean): boolean => (requiresInspection ? status === 'INSPECTED' : status === 'CLEAN' || status === 'INSPECTED')

export function roomRank(r: RoomLike, order: RoomOrder): number {
  if (order.currentRoomId != null && r.room_id === order.currentRoomId) return 0
  const wanted = order.requestedBedTypeId ?? null
  const bed = wanted !== null && r.bed_type_id === wanted ? 0 : 1
  return (order.ignoreReadiness || isRoomReady(r.housekeeping_status, order.requiresInspection) ? 1 : 3) + bed
}

export function sortFreeRooms<T extends RoomLike>(rooms: readonly T[], order: RoomOrder): T[] {
  return [...rooms].sort((a, b) => roomRank(a, order) - roomRank(b, order))
}

/** Whether a room has the bed that was asked for. */
export const hasRequestedBed = (r: Pick<FreeRoom, 'bed_type_id'>, requestedBedTypeId: number | null | undefined): boolean => !!requestedBedTypeId && r.bed_type_id === requestedBedTypeId

/** A free room in one line of text, for a list that is a select: number, housekeeping status, bed (marked when it is the bed asked for), and a note when it is not ready. */
export function roomOptionLabel(r: FreeRoom, order: Pick<RoomOrder, 'requestedBedTypeId' | 'requiresInspection'>): string {
  const bed = r.bed_type_name ? ` · ${r.bed_type_name}` : ''
  const mark = hasRequestedBed(r, order.requestedBedTypeId) ? ` ✓ ${t('bedTypes.matches')}` : ''
  const note = isRoomReady(r.housekeeping_status, order.requiresInspection) ? '' : ` ${t('frontDesk.checkIn.notReadyTag')}`
  return `${r.room_number} · ${statusText(r.housekeeping_status)}${bed}${mark}${note}`
}
