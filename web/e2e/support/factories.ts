import { randomInt } from 'node:crypto'
import type { ApiClient } from './api'
import type { SeedState } from './state'

/** A short unique suffix, so data made by parallel tests never collides and a row can be found by its name. */
export function uid(): string {
  return `${Date.now().toString(36).slice(-4)}${randomInt(36 ** 3).toString(36).padStart(3, '0')}`.toUpperCase()
}

export function addDays(iso: string, n: number): string {
  const d = new Date(`${iso}T00:00:00Z`)
  d.setUTCDate(d.getUTCDate() + n)
  return d.toISOString().slice(0, 10)
}

/** dd/mm/yyyy, the way DateInput is typed. */
export function displayDate(iso: string): string {
  const [y, m, d] = iso.split('-')
  return `${d}/${m}/${y}`
}

export interface Guest { id: number; code: string; first_name: string; last_name: string }
export interface Reservation { id: number; confirmation_number: string; version: number; guest_id: number; arrival_date: string; departure_date: string; rooms: { id: number; room_id: number | null; room_number?: string; room_type_id: number }[] }
export interface CheckedIn { reservation: Reservation; stay: { id: number; stay_number: string; version: number }; folio: { id: number; folio_number: string }; room: { id: number; number: string }; guest: Guest }

/**
 * Makes the data a test starts from, through the API, as the administrator. Each call makes new rows with unique names;
 * nothing here is shared between tests, so the specs run in parallel.
 */
export class Factories {
  readonly P: string
  constructor(readonly api: ApiClient, readonly seed: SeedState) {
    this.P = `/properties/${seed.propertyId}`
  }

  async businessDate(): Promise<string> {
    return (await this.api.get<{ business_date: string }>(`${this.P}/business-date`)).business_date
  }

  async guest(over: Partial<{ first_name: string; last_name: string; email: string; phone: string }> = {}): Promise<Guest> {
    const u = uid()
    return this.api.post<Guest>('/guests', {
      origin_property_id: this.seed.propertyId, first_name: over.first_name ?? 'Tamu', last_name: over.last_name ?? `E2E ${u}`,
      email: over.email ?? `tamu.${u.toLowerCase()}@e2e.example`, phone: over.phone, country_code: 'ID',
    })
  }

  async chargeCode(code: string): Promise<{ id: number; code: string; name: string; default_unit_price?: string }> {
    const all = (await this.api.get<{ data: { id: number; code: string; name: string; charge_type: string; default_unit_price?: string }[] }>(`${this.P}/charge-codes`, { active: true })).data
    const found = all.find((c) => c.code === code) ?? all.find((c) => c.charge_type === code)
    if (!found) throw new Error(`no charge code ${code} (have ${all.map((c) => c.code).join(', ')})`)
    return found
  }

  /** A free room of the type for the nights, made clean (ready for a check-in). Picked at random so parallel tests rarely meet. */
  async freeRoom(roomTypeId: number, arrival: string, departure: string): Promise<{ id: number; number: string }> {
    const free = (await this.api.get<{ data: { room_id: number; room_number: string; housekeeping_status: string }[] }>(`${this.P}/availability/rooms`, { room_type_id: roomTypeId, arrival, departure })).data
    if (!free.length) throw new Error(`no free room of type ${roomTypeId} for ${arrival}..${departure}`)
    const r = free[randomInt(free.length)]!
    await this.makeClean(r.room_id, r.housekeeping_status)
    return { id: r.room_id, number: r.room_number }
  }

  async makeClean(roomId: number, status?: string): Promise<void> {
    if (status === 'CLEAN' || status === 'INSPECTED') return
    // DIRTY→CLEAN and CLEANING→CLEAN are both legal (rooms/model.go); an unknown status is tried and may already be clean
    await this.api.soft('POST', `${this.P}/rooms/${roomId}/housekeeping`, { status: 'CLEAN' })
  }

  async makeDirty(roomId: number): Promise<void> {
    await this.api.post(`${this.P}/rooms/${roomId}/housekeeping`, { status: 'DIRTY' })
  }

  /**
   * A dirty room of a room type of its own, which no other test books (they pick rooms of the seeded types): for the housekeeping tests,
   * whose room must not be checked in by a parallel test between two steps.
   */
  async isolatedRoom(): Promise<{ id: number; number: string }> {
    const u = uid()
    const type = await this.api.post<{ id: number }>(`${this.P}/room-types`, { code: `H${u}`.slice(0, 8), name: `Uji HK ${u}`, max_adult: 2, max_child: 0, max_occupancy: 2, base_occupancy: 2 })
    const bed = (await this.api.get<{ data: { id: number }[] }>(`${this.P}/bed-types`, { active: true })).data[0]!
    const number = `H${u.slice(-4)}`
    const room = await this.api.post<{ id: number }>(`${this.P}/rooms`, { room_number: number, room_type_id: type.id, bed_type_id: bed.id, floor: '9', initial_housekeeping_status: 'DIRTY' })
    return { id: room.id, number }
  }

  /** The room type with the most free rooms for the nights (so a test does not fail on a full type). */
  async roomTypeWithSpace(arrival: string, departure: string): Promise<number> {
    let best = this.seed.roomTypes[0]!.id
    let most = -1
    for (const t of this.seed.roomTypes) {
      const n = (await this.api.get<{ data: unknown[] }>(`${this.P}/availability/rooms`, { room_type_id: t.id, arrival, departure })).data.length
      if (n > most) { most = n; best = t.id }
    }
    return best
  }

  /**
   * A reservation. Defaults: arrival on the business date, 2 nights, a guest of its own, confirmed, a free clean room assigned
   * (assignRoom: false leaves the line without a room, for the tests of room assignment).
   */
  async reservation(o: { arrival?: string; nights?: number; roomTypeId?: number; assignRoom?: boolean; confirm?: boolean; guest?: Guest; companyId?: number; groupId?: number; source?: string; adults?: number } = {}): Promise<Reservation & { guest: Guest; room?: { id: number; number: string } }> {
    const arrival = o.arrival ?? (await this.businessDate())
    const departure = addDays(arrival, o.nights ?? 2)
    const roomTypeId = o.roomTypeId ?? (await this.roomTypeWithSpace(arrival, departure))
    const guest = o.guest ?? (await this.guest())
    const room = o.assignRoom === false || o.confirm === false ? undefined : await this.freeRoom(roomTypeId, arrival, departure)
    const res = await this.api.post<Reservation>(`${this.P}/reservations`, {
      guest_id: guest.id, source: o.source ?? 'PHONE', confirm: o.confirm ?? true, company_id: o.companyId, booking_group_id: o.groupId,
      rooms: [{ room_type_id: roomTypeId, rate_plan_id: this.seed.ratePlanId, arrival_date: arrival, departure_date: departure, adult_count: o.adults ?? 2, ...(room ? { room_id: room.id } : {}) }],
    })
    return { ...res, guest, room }
  }

  /** A guest in house: a reservation arriving today, checked in through the API. */
  async inHouse(o: { nights?: number; roomTypeId?: number; companyId?: number } = {}): Promise<CheckedIn> {
    const res = await this.reservation({ nights: o.nights ?? 2, roomTypeId: o.roomTypeId, companyId: o.companyId })
    const full = await this.api.get<Reservation>(`${this.P}/reservations/${res.id}`)
    const line = full.rooms[0]!
    const out = await this.api.post<{ stay: CheckedIn['stay']; folio: CheckedIn['folio']; stay_room: { room_id: number; room_number: string } }>(
      `${this.P}/reservations/${res.id}/rooms/${line.id}/check-in`,
      { version: full.version, guest_id: res.guest.id, adult_count: 2, room_id: res.room!.id },
    )
    return { reservation: full, stay: out.stay, folio: out.folio, room: { id: out.stay_room.room_id, number: out.stay_room.room_number }, guest: res.guest }
  }

  async postCharge(folioId: number, chargeCode: string, amount: string, description?: string): Promise<{ item: { id: number } }> {
    const cc = await this.chargeCode(chargeCode)
    return this.api.post(`${this.P}/folios/${folioId}/charges`, { charge_code_id: cc.id, quantity: '1', unit_price: amount, description })
  }

  /** A payment that needs no cashier shift (only cash does). */
  async pay(folioId: number, amount: string, method: 'CARD' | 'BANK_TRANSFER' | 'OTHER' | 'QRIS' = 'CARD'): Promise<{ payment: { id: number; payment_number: string } }> {
    return this.api.post(`${this.P}/folios/${folioId}/payments`, { amount, payment_method: method, reference_number: `E2E-${uid()}` })
  }

  async folio(folioId: number): Promise<{ id: number; folio_number: string; status: string; balance: string; items?: unknown[] }> {
    return this.api.get(`${this.P}/folios/${folioId}`)
  }

  async stay(stayId: number): Promise<{ stay: { id: number; status: string; version: number; departure_date: string }; folios: { id: number; balance?: string }[]; segments: { room_number: string }[] }> {
    return this.api.get(`${this.P}/stays/${stayId}`)
  }

  /** Settles the folio of a stay so it can check out (pays what is due by card). */
  async settle(folioId: number): Promise<void> {
    const f = await this.folio(folioId)
    if (Number(f.balance) > 0) await this.pay(folioId, f.balance)
  }

  async company(o: Partial<{ code: string; name: string; credit_limit: string }> = {}): Promise<{ id: number; code: string; name: string }> {
    const u = uid()
    return this.api.post(`${this.P}/companies`, { code: o.code ?? `C${u}`.slice(0, 10), name: o.name ?? `PT Uji ${u}`, contact_name: 'Ibu Uji', email: `ar.${u.toLowerCase()}@e2e.example`, credit_limit: o.credit_limit ?? '50000000', payment_terms_days: 30 })
  }

  /** Opens a cashier shift for this API user on a drawer of its own (one shift per drawer: parallel tests use different drawers). */
  async openShift(api: ApiClient = this.api, drawer = `E2E-${uid()}`): Promise<{ id: number; drawer: string }> {
    const cur = await api.soft<{ id?: number; drawer?: string; shift?: { id: number; drawer: string } }>('GET', `${this.P}/cashier/shifts/current`)
    const open = cur?.shift ?? (cur?.id ? (cur as { id: number; drawer: string }) : null)
    if (open) return open
    return api.post(`${this.P}/cashier/shifts`, { drawer, opening_float: '1000000' })
  }

  async closeShift(api: ApiClient = this.api): Promise<void> {
    const cur = await api.soft<{ id?: number; shift?: { id: number } }>('GET', `${this.P}/cashier/shifts/current`)
    const id = cur?.shift?.id ?? cur?.id
    if (!id) return
    const sh = await api.get<{ cash: { expected: string } }>(`${this.P}/cashier/shifts/${id}`)
    await api.post(`${this.P}/cashier/shifts/${id}/close`, { counted_cash: sh.cash.expected })
  }
}
