import { readFileSync, writeFileSync } from 'node:fs'
import type { ApiClient } from '../support/api'
import { USER_PASSWORD } from '../support/env'
import { addDays, Factories, type Guest } from '../support/factories'
import type { SeedState } from '../support/state'

/**
 * The hotel of the user guide's pictures: the world of setup (the property E2E with 50 rooms, the rate plan RO and a user per desk),
 * renamed and filled with Indonesian guests, so every picture shows a believable front desk. Built once at the start of the docs run
 * (docs-screenshots.spec.ts, test "dunia"), on the database pms_e2e that scripts/e2e.sh made new.
 *
 * Yesterday (the opening business day of setup) five guests check in; the night audit then closes yesterday, so the business date is today,
 * two guests leave today and the arrivals of today wait at the desk.
 */
export const DOCS_WORLD = new URL('../.state/docs-world.json', import.meta.url)

export interface Booking { id: number; confirmation: string; lineId: number; guest: Guest; room?: string }
export interface InHouse extends Booking { stayId: number; stayNumber: string; folioId: number; room: string }

export interface DocsWorld {
  propertyName: string
  businessDate: string
  /** Did the night audit of the start move the business date to today (false: the day of setup is still open and the top bar says the audit is late). */
  audited: boolean
  rudi: Guest // books a room through the screen (membuat-reservasi)
  arrivals: { budi: Booking; dewi: Booking; rizky: Booking }
  future: { fitri: Booking; hendra: Booking; yusuf: Booking }
  inHouse: { siti: InHouse; agus: InHouse; made: InHouse; putri: InHouse; lina: InHouse; andreas: InHouse }
}

export const PROPERTY_NAME = 'Hotel Kamara Ubud'

/** An example role and user of the guide: the front office manager who approves rate changes (no seeded role holds the approval permissions). */
export const MANAGER = { email: 'bagus.wicaksono@hotelkamara.example', fullName: 'Bagus Wicaksono', role: 'Manajer Front Office' }
const MANAGER_PERMISSIONS = [
  'reservation.read', 'reservation.update', 'guest.read', 'frontdesk.rate_change', 'folio.read', 'folio.adjust', 'correction.approve',
  'reservation.override_rate', 'reservation.override_rate_approve', 'reservation.complimentary', 'reservation.complimentary_approve',
  'reservation.restriction_approve', 'report.view',
]

const PEOPLE = {
  budi: ['Budi', 'Santoso', '+62 812 3456 7801'], dewi: ['Dewi', 'Lestari', '+62 813 2211 4402'], rizky: ['Rizky', 'Hidayat', '+62 857 1100 2203'],
  fitri: ['Fitri', 'Handayani', '+62 812 7788 9904'], hendra: ['Hendra', 'Gunawan', '+62 811 3344 5505'], yusuf: ['Yusuf', 'Maulana', '+62 878 6655 4406'],
  siti: ['Siti', 'Rahmawati', '+62 812 9900 1107'], agus: ['Agus', 'Pratama', '+62 813 5566 7708'], made: ['Made', 'Wirawan', '+62 819 2233 4409'],
  putri: ['Putri', 'Anggraini', '+62 812 4455 6610'], lina: ['Lina', 'Kusuma', '+62 821 7788 1111'], andreas: ['Andreas', 'Wijaya', '+62 811 9988 7712'],
  rudi: ['Rudi', 'Hartono', '+62 812 6677 8813'],
} as const
type Person = keyof typeof PEOPLE

export function readWorld(): DocsWorld {
  return JSON.parse(readFileSync(DOCS_WORLD, 'utf8')) as DocsWorld
}

export async function buildWorld(api: ApiClient, seed: SeedState): Promise<DocsWorld> {
  const data = new Factories(api, seed)
  const P = data.P
  await api.patch(P, { name: PROPERTY_NAME, address: 'Jl. Raya Ubud No. 18', city: 'Gianyar', phone: '+62 361 975 000' })
  await ensureTaxes(api, P)
  await ensureManager(api, seed)

  const guests = {} as Record<Person, Guest>
  for (const [key, [first, last, phone]] of Object.entries(PEOPLE) as [Person, readonly [string, string, string]][]) {
    guests[key] = await data.guest({ first_name: first, last_name: last, phone, email: `${first}.${last}@contoh.example`.toLowerCase() })
  }

  // the room types in the order of pms-seed: STD, SUP, DLX, STE
  const type = (code: string) => seed.roomTypes.find((t) => t.code === code)?.id ?? seed.roomTypes[0]!.id
  const used = new Set<number>()

  /** The free room with the lowest number of the type (the same rooms every run), made clean. */
  async function room(typeId: number, arrival: string, departure: string): Promise<{ id: number; number: string }> {
    const free = (await api.get<{ data: { room_id: number; room_number: string; housekeeping_status: string }[] }>(`${P}/availability/rooms`, { room_type_id: typeId, arrival, departure })).data
      .filter((r) => !used.has(r.room_id))
      .sort((a, b) => a.room_number.localeCompare(b.room_number, 'en', { numeric: true }))
    const r = free[0]
    if (!r) throw new Error(`no free room of type ${typeId} for ${arrival}..${departure}`)
    used.add(r.room_id)
    await data.makeClean(r.room_id, r.housekeeping_status)
    return { id: r.room_id, number: r.room_number }
  }

  async function book(who: Person, o: { arrival: string; nights: number; typeCode: string; assign?: boolean; confirm?: boolean; adults?: number; source?: string }): Promise<Booking> {
    const departure = addDays(o.arrival, o.nights)
    const typeId = type(o.typeCode)
    const r = o.assign === false || o.confirm === false ? undefined : await room(typeId, o.arrival, departure)
    const res = await api.post<{ id: number; confirmation_number: string; rooms: { id: number }[] }>(`${P}/reservations`, {
      guest_id: guests[who].id, source: o.source ?? 'PHONE', confirm: o.confirm ?? true,
      rooms: [{ room_type_id: typeId, rate_plan_id: seed.ratePlanId, arrival_date: o.arrival, departure_date: departure, adult_count: o.adults ?? 2, ...(r ? { room_id: r.id } : {}) }],
    })
    return { id: res.id, confirmation: res.confirmation_number, lineId: res.rooms[0]!.id, guest: guests[who], room: r?.number }
  }

  async function checkIn(b: Booking, roomId: number): Promise<InHouse> {
    const full = await api.get<{ version: number }>(`${P}/reservations/${b.id}`)
    const out = await api.post<{ stay: { id: number; stay_number: string }; folio: { id: number }; stay_room: { room_number: string } }>(
      `${P}/reservations/${b.id}/rooms/${b.lineId}/check-in`, { version: full.version, guest_id: b.guest.id, adult_count: 2, room_id: roomId })
    return { ...b, stayId: out.stay.id, stayNumber: out.stay.stay_number, folioId: out.folio.id, room: out.stay_room.room_number }
  }

  async function stayFor(who: Person, arrival: string, nights: number, typeCode: string): Promise<InHouse> {
    const departure = addDays(arrival, nights)
    const r = await room(type(typeCode), arrival, departure)
    const res = await api.post<{ id: number; confirmation_number: string; rooms: { id: number }[] }>(`${P}/reservations`, {
      guest_id: guests[who].id, source: 'EMAIL', confirm: true,
      rooms: [{ room_type_id: type(typeCode), rate_plan_id: seed.ratePlanId, arrival_date: arrival, departure_date: departure, adult_count: 2, room_id: r.id }],
    })
    return checkIn({ id: res.id, confirmation: res.confirmation_number, lineId: res.rooms[0]!.id, guest: guests[who], room: r.number }, r.id)
  }

  // --- yesterday: the guests who are in house when the desk opens
  const yesterday = await data.businessDate()
  const siti = await stayFor('siti', yesterday, 3, 'SUP')
  const agus = await stayFor('agus', yesterday, 2, 'STD')
  const made = await stayFor('made', yesterday, 3, 'DLX')
  const putri = await stayFor('putri', yesterday, 1, 'STD')
  const lina = await stayFor('lina', yesterday, 1, 'SUP')
  await data.postCharge(putri.folioId, 'RESTAURANT', '185000', 'Makan malam di restoran')

  // --- the night audit closes yesterday: the business date becomes today
  let audited = false
  const preview = await api.get<{ can_run: boolean; blockers: unknown }>(`${P}/night-audit/preview`)
  if (preview.can_run) {
    await api.post(`${P}/night-audit/run`, { business_date: yesterday })
    audited = true
  } else {
    console.warn(`[docs] the night audit of the start cannot run, the pictures keep the day of setup open: ${JSON.stringify(preview.blockers).slice(0, 600)}`)
  }
  const today = await data.businessDate()
  // the rooms the audit marked dirty stay dirty: the room status pages show them

  // --- today
  const andreas = await stayFor('andreas', today, 2, 'STD')
  const budi = await book('budi', { arrival: today, nights: 2, typeCode: 'DLX' })
  const dewi = await book('dewi', { arrival: today, nights: 3, typeCode: 'SUP' })
  const rizky = await book('rizky', { arrival: today, nights: 1, typeCode: 'STD', assign: false })
  const fitri = await book('fitri', { arrival: addDays(today, 3), nights: 2, typeCode: 'DLX', assign: false })
  const hendra = await book('hendra', { arrival: addDays(today, 7), nights: 3, typeCode: 'SUP', confirm: false })
  const yusuf = await book('yusuf', { arrival: addDays(today, 5), nights: 2, typeCode: 'STD' })

  const world: DocsWorld = {
    propertyName: PROPERTY_NAME, businessDate: today, audited, rudi: guests.rudi,
    arrivals: { budi, dewi, rizky }, future: { fitri, hendra, yusuf },
    inHouse: { siti, agus, made, putri, lina, andreas },
  }
  writeFileSync(DOCS_WORLD, JSON.stringify(world, null, 2))
  return world
}

async function ensureManager(api: ApiClient, seed: SeedState): Promise<void> {
  const roles = (await api.get<{ data: { id: number; name: string }[] }>('/roles')).data
  const role = roles.find((r) => r.name === MANAGER.role) ?? (await api.post<{ id: number }>('/roles', { name: MANAGER.role, description: 'Menyetujui perubahan tarif dan koreksi front office', permissions: MANAGER_PERMISSIONS }))
  const users = await api.all<{ email: string }>('/users')
  if (!users.some((u) => u.email.toLowerCase() === MANAGER.email)) {
    await api.post('/users', { email: MANAGER.email, full_name: MANAGER.fullName, password: USER_PASSWORD, grants: [{ property_id: seed.propertyId, role_id: role.id }] })
  }
}

/**
 * The taxes most hotels in Indonesia charge, when the guide's setup pictures did not make them: the hotel tax PB1 (PBJT) 10% on top of a service charge
 * of 10%, on the room (ROOM, not ROOM_EXEMPT) and on the restaurant and the minibar. A new property has neither (its charge codes are made without tax).
 */
export async function ensureTaxes(api: ApiClient, P: string): Promise<void> {
  const taxes = (await api.get<{ data: { id: number; code: string }[] }>(`${P}/taxes`)).data
  const services = (await api.get<{ data: { id: number; code: string }[] }>(`${P}/service-charges`)).data
  const pb1 = { name: 'Pajak hotel dan restoran (PB1)', rate: '10', tax_on_service: true, tax_kind: 'LOCAL', gl_account_code: '2410' }
  const found = taxes.find((t) => t.code === 'PB1')
  // the demo POS data may have made PB1 already (as a restaurant tax without an account): the pictures show the hotel's own
  if (found) await api.patch(`${P}/taxes/${found.id}`, { ...pb1, is_active: true })
  const tax = found ?? (await api.post<{ id: number }>(`${P}/taxes`, { code: 'PB1', ...pb1 }))
  const svc = services.find((s) => s.code === 'SC') ?? (await api.post<{ id: number }>(`${P}/service-charges`, { code: 'SC', name: 'Biaya layanan', rate: '10', gl_account_code: '2430' }))
  const codes = (await api.get<{ data: { id: number; code: string; charge_type: string }[] }>(`${P}/charge-codes`, { active: true })).data
  for (const c of codes.filter((c) => ['ROOM', 'RESTAURANT', 'MINIBAR'].includes(c.code))) {
    const cur = await api.get<{ rules?: { taxes?: unknown[]; service_charges?: unknown[] }; taxes?: unknown[]; service_charges?: unknown[] }>(`${P}/charge-codes/${c.id}`)
    const has = (cur.rules?.taxes ?? cur.taxes ?? []).length > 0
    if (!has) await api.put(`${P}/charge-codes/${c.id}/rules`, { taxes: [{ tax_id: tax.id, sequence: 2 }], service_charges: [{ service_charge_id: svc.id, sequence: 1 }] })
  }
}
