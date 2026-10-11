import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Reservation } from '@/api/types'
import { t } from '@/i18n'

/** The line to put a room on, and the version of its reservation (the version of the row the person is looking at). */
export interface AssignTarget {
  reservationId: number
  lineId: number
  version: number
}

/**
 * Assigns a room to a CONFIRMED line of a reservation (POST assign-room). A room that is already on the line is replaced: the server only asks that the line waits for its check-in and that
 * the room is free for the nights. `upgrade` is for a room of another type and is only sent by the page of the reservation: a list never sends it. The answer is the reservation, with the
 * version the next assignment of the same reservation has to ask for.
 */
export async function assignRoom(propertyId: number, target: AssignTarget, roomId: number, upgrade = false): Promise<Reservation> {
  const { data } = await api.POST('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/assign-room', {
    params: { path: { propertyId, id: target.reservationId, lineId: target.lineId } },
    body: { version: target.version, room_id: roomId, upgrade },
  })
  if (!data) throw new Error('no reservation')
  return data
}

/** What went wrong with an assignment, in words for the person, and whether what is on the screen (the rooms, the row) is out of date and has to be read again. */
export interface AssignFailure {
  kind: 'taken' | 'changed' | 'notWaiting' | 'other'
  message: string
  /** The rooms and the row are read again: someone else changed them. */
  reload: boolean
  code: string
}

const TAKEN = new Set(['ROOM_NOT_AVAILABLE', 'ROOM_TYPE_NOT_AVAILABLE', 'BED_NOT_AVAILABLE'])

export function assignFailure(e: unknown): AssignFailure {
  if (e instanceof ApiError) {
    if (TAKEN.has(e.code)) return { kind: 'taken', message: t('roomPicker.taken'), reload: true, code: e.code }
    if (e.code === 'VERSION_CONFLICT') return { kind: 'changed', message: t('roomPicker.changed'), reload: true, code: e.code }
    if (e.code === 'LINE_NOT_CONFIRMED') return { kind: 'notWaiting', message: t('roomPicker.notWaiting'), reload: true, code: e.code }
    return { kind: 'other', message: e.message, reload: e.status === 409, code: e.code }
  }
  return { kind: 'other', message: e instanceof Error ? e.message : String(e), reload: false, code: 'NETWORK_ERROR' }
}
