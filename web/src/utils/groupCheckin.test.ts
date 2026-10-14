import { describe, expect, it } from 'vitest'
import type { Arrival, GroupRouting } from '@/api/types'
import { chunk, depositProposal, identityHint, roomGoesToMaster, rowSelectable } from './groupCheckin'

const arrival = (over: object = {}): Arrival => ({
  reservation_id: 41, confirmation_number: 'RES000001', reservation_room_id: 1, reservation_version: 2, guest_id: 5, guest_name: 'Ayu', room_type_id: 3, room_type_code: 'DLX', room_type_name: 'Deluxe', room_id: 9,
  arrival_date: '2026-09-30', departure_date: '2026-10-01', adult_count: 2, child_count: 0, status: 'CONFIRMED', reservation_status: 'CONFIRMED', rate: { rate_plan_code: 'BAR', rate_plan_name: 'Best', amount: '900000', price_mode: 'EXCLUSIVE' },
  company: null, deposit: null, readiness: { status: 'READY', blockers: [] }, guest_has_id: true, guest_has_phone: true, guest_is_placeholder: false, estimate_total: '1000000', ...over,
}) as Arrival

const routing = (instructions: { scope: string; charge_code_id: number | null }[], over: object = {}): GroupRouting => ({
  group_id: 4, master: { id: 30, folio_number: 'FOL000030', status: 'OPEN', balance: '0', version: 1, bill_to_company_id: null, opened_at: '', closed_at: null },
  instructions, lines: [{ reservation_room_id: 1, reservation_id: 41, confirmation_number: 'RES000001', status: 'CONFIRMED', excluded: false }, { reservation_room_id: 2, reservation_id: 42, confirmation_number: 'RES000002', status: 'CONFIRMED', excluded: true }], ...over,
}) as GroupRouting

const o = { incidental: '200000', decimals: 0 }

describe('the deposit proposed for a room of a group', () => {
  it('asks nothing when everything goes to the master, nor when a company pays', () => {
    expect(depositProposal(arrival(), routing([{ scope: 'ALL', charge_code_id: null }]), o)).toEqual({ basis: 'MASTER_ALL', amount: null })
    expect(depositProposal(arrival({ company: { id: 8, name: 'Acme' } }), routing([]), o)).toEqual({ basis: 'COMPANY', amount: null })
  })

  it('asks the incidental deposit alone when the room goes to the master', () => {
    expect(depositProposal(arrival(), routing([{ scope: 'ROOM', charge_code_id: null }]), o)).toEqual({ basis: 'INCIDENTAL', amount: '200000' })
    // what the reservation already holds is taken off
    expect(depositProposal(arrival({ deposit: { folio_id: 3, paid: '50000' } }), routing([{ scope: 'ROOM', charge_code_id: null }]), o).amount).toBe('150000')
  })

  it('follows the rule of the panel when the room is charged on its own folio: the estimate and the incidental, less what is held', () => {
    // a room that is left out of the routing
    const left = arrival({ reservation_room_id: 2 })
    expect(depositProposal(left, routing([{ scope: 'ALL', charge_code_id: null }]), o)).toEqual({ basis: 'PANEL', amount: '1200000' })
    // no instruction for the room (only a charge code), or no master at all
    expect(depositProposal(arrival(), routing([{ scope: 'CHARGE_CODE', charge_code_id: 2 }]), o)).toEqual({ basis: 'PANEL', amount: '1200000' })
    expect(depositProposal(arrival(), routing([{ scope: 'ROOM', charge_code_id: null }], { master: null }), o).basis).toBe('PANEL')
    expect(depositProposal(arrival({ deposit: { folio_id: 3, paid: '300000' } }), null, o).amount).toBe('900000')
    // the estimate could not be read: the incidental deposit alone
    expect(depositProposal(arrival({ estimate_total: undefined }), null, o).amount).toBe('200000')
  })

  it('does not count a closed master as a place for the room', () => {
    const closed = routing([{ scope: 'ROOM', charge_code_id: null }])
    closed.master = { ...closed.master!, status: 'CLOSED' }
    expect(roomGoesToMaster(arrival(), closed)).toBe(false)
  })
})

describe('the rows of a group check-in', () => {
  it('can be ticked when ready, or when only the room is not ready and the person may override that', () => {
    expect(rowSelectable(arrival(), false)).toBe(true)
    const notReady = arrival({ readiness: { status: 'BLOCKED', blockers: ['ROOM_NOT_READY'] } })
    expect(rowSelectable(notReady, false)).toBe(false)
    expect(rowSelectable(notReady, true)).toBe(true)
    expect(rowSelectable(arrival({ readiness: { status: 'BLOCKED', blockers: ['ROOM_NOT_READY', 'GUEST_MISSING'] } }), true)).toBe(false)
    expect(rowSelectable(arrival({ readiness: { status: 'BLOCKED', blockers: ['ROOM_NOT_ASSIGNED'] } }), true)).toBe(false)
    expect(rowSelectable(arrival({ status: 'CHECKED_IN' }), true)).toBe(false)
  })

  it('goes in parts of 20, in order', () => {
    const parts = chunk(Array.from({ length: 45 }, (_, i) => i), 20)
    expect(parts.map((p) => p.length)).toEqual([20, 20, 5])
    expect(parts[1]![0]).toBe(20)
    expect(chunk([], 20)).toEqual([])
  })

  it('warns about a passport of a foreign guest and about a guest with no ID, never of a placeholder', () => {
    expect(identityHint(arrival({ guest_nationality: 'FR', guest_has_id: false }))).toBe('passport')
    expect(identityHint(arrival({ guest_nationality: 'FR', guest_has_id: true }))).toBeNull()
    expect(identityHint(arrival({ guest_nationality: 'ID', guest_has_id: false }))).toBe('identity')
    expect(identityHint(arrival({ guest_nationality: 'ID', guest_has_id: true, guest_id_type: 'KTP' }))).toBeNull()
    expect(identityHint(arrival({ guest_has_id: false, guest_is_placeholder: true }))).toBeNull()
  })
})
