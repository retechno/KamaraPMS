import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { en } from '@/i18n/locales/en'
import { id } from '@/i18n/locales/id'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import BanquetCalendarView from './BanquetCalendarView.vue'
import BanquetCatalogView from './BanquetCatalogView.vue'
import BanquetEventsView from './BanquetEventsView.vue'
import BanquetEventView from './BanquetEventView.vue'
import BanquetRoomsView from './BanquetRoomsView.vue'
import BanquetSettingsView from './BanquetSettingsView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PUT = vi.fn()
let PATCH = vi.fn()
let DELETE = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PUT: (...a: unknown[]) => PUT(...a), PATCH: (...a: unknown[]) => PATCH(...a), DELETE: (...a: unknown[]) => DELETE(...a) } }))

const room = (n: number, code: string, name: string, over: Record<string, unknown> = {}) => ({
  id: n, code, name, area_m2: null, setup_minutes: 60, teardown_minutes: 30, is_combo: false, is_active: true, sort_order: n, version: 1, parts: [], setups: [{ setup_style: 'THEATER', capacity: 120 }], ...over,
})
const A = room(1, 'SA', 'Saraswati A')
const B = room(2, 'SB', 'Saraswati B')
const session = (n: number, over: Record<string, unknown> = {}) => ({
  session_id: n, event_id: 10 + n, event_number: `BQ-00000${n}`, event_name: `Event ${n}`, event_status: 'DEFINITE', event_type_name: 'Wedding', booker_name: 'PT Maju', sales_user_id: 5, option_until: null, option_overdue: false,
  session_name: 'Dinner', session_date: '2026-11-05', start_time: '18:00:00', end_time: '22:00:00', ends_next_day: false, room_id: 1, room_code: 'SA', room_name: 'Saraswati A', setup_minutes: 60, teardown_minutes: 30,
  setup_style: 'THEATER', pax_estimated: 100, rank: null, room_taken: false, ...over,
})

const estimate = (over: Record<string, unknown> = {}) => ({ lines: [], by_code: [], net: '0', service: '0', tax: '0', total: '0', ...over })
const money = (over: Record<string, unknown> = {}) => ({
  posting_mode: 'AUTO', posting_mode_own: false, estimate: estimate(), folio: null, deposit_received: '0', schedule: [], schedule_total: '0', overdue: false,
  policy: { base: '', tiers: [], current: null, days_left: 0 }, postings: [], cancelled_fee_item_id: null, groups: [], ...over,
})

const mounted: VueWrapper[] = []
async function setup(component: unknown, permissions: string[], routes = '/banquet') {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'm@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/banquet', component: { template: '<div />' } }, { path: '/banquet/events/:id', component: { template: '<div />' } }] })
  await router.push(routes)
  const w = mount(component as never, { attachTo: document.body, global: { plugins: [pinia, router] } })
  mounted.push(w)
  await flushPromises()
  return { w, router }
}
const body = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)
const has = (sel: string) => document.body.querySelector(sel) !== null

function api(sessions: unknown[] = [], extra: (path: string) => unknown = () => undefined) {
  GET = vi.fn(async (path: string) => {
    const x = extra(path)
    if (x !== undefined) return x
    if (path.endsWith('/banquet/today')) return { data: { date: '2026-11-05', sessions: [], overdue_options: [] } }
    if (path.endsWith('/banquet/calendar')) return { data: { from: '2026-11-05', to: '2026-11-05', rooms: [A, B], sessions } }
    if (path.endsWith('/banquet/event-types')) return { data: { data: [{ id: 1, code: 'WED', name: 'Wedding', is_active: true }] } }
    if (path.endsWith('/banquet/notices')) return { data: { data: [] } }
    if (path.endsWith('/banquet/rooms')) return { data: { data: [A, B] } }
    if (path.endsWith('/banquet/reasons')) return { data: { data: [{ id: 1, kind: 'LOST', code: 'PRICE', name: 'Price', is_active: true }, { id: 2, kind: 'CANCEL', code: 'ILL', name: 'Illness', is_active: true }] } }
    if (path.endsWith('/banquet/settings')) return { data: { tentative_conflict_mode: 'REJECT', operating_from: null, operating_to: null, posting_mode: 'AUTO', default_price_mode: 'EXCLUSIVE', max_discount_pct_no_approval: '0' } }
    if (path.endsWith('/events/{id}/money')) return { data: money() }
    if (path.endsWith('/events/{id}/lines')) return { data: { lines: [], estimate: estimate() } }
    if (path.endsWith('/companies')) return { data: { data: [{ id: 3, name: 'PT Maju' }] } }
    return { data: { data: [] } }
  })
  POST = vi.fn().mockResolvedValue({ data: {} })
  PUT = vi.fn().mockResolvedValue({ data: {} })
  PATCH = vi.fn().mockResolvedValue({ data: {} })
  DELETE = vi.fn().mockResolvedValue({ data: {} })
}

beforeEach(() => setLocale('en'))
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
})

describe('the calendar', () => {
  it('draws a confirmed session solid, the first option amber, a queued option grey with its place, with the set-up and tear-down around it', async () => {
    api([
      session(1),
      session(2, { event_status: 'TENTATIVE', rank: 1, room_id: 2, room_code: 'SB', start_time: '09:00:00', end_time: '11:00:00' }),
      session(3, { event_status: 'TENTATIVE', rank: 2, room_id: 2, room_code: 'SB', start_time: '10:00:00', end_time: '12:00:00' }),
    ])
    const { w } = await setup(BanquetCalendarView, ['banquet.manage'])
    expect(w.find('[data-testid=room-head-SA]').text()).toBe('Saraswati A')
    expect(w.find('[data-testid=block-1]').attributes('data-tone')).toBe('definite')
    expect(w.find('[data-testid=block-2]').attributes('data-tone')).toBe('first')
    expect(w.find('[data-testid=block-3]').attributes('data-tone')).toBe('queued')
    expect(w.find('[data-testid=block-3]').text()).toContain('#2')
    expect(w.find('[data-testid=setup-1]').exists()).toBe(true)
    expect(w.find('[data-testid=teardown-1]').exists()).toBe(true)
    // the block starts an hour before the session: 17:00 of a grid that runs 06:00 to 24:00
    expect(w.find('[data-testid=block-1]').attributes('style')).toContain('top: 61.1')
  })

  it('has a list of the day for a phone and the grid for a wide screen', async () => {
    api([session(1)])
    const { w } = await setup(BanquetCalendarView, ['banquet.read'])
    expect(w.find('[data-testid=calendar-agenda]').classes()).toContain('md:hidden')
    expect(w.find('[data-testid=calendar-day]').classes()).toEqual(expect.arrayContaining(['hidden', 'md:block']))
    expect(w.find('[data-testid=agenda-1]').text()).toContain('Saraswati A')
  })

  it('opens a new booking with the slot filled in when an empty slot is clicked', async () => {
    api([])
    const { w } = await setup(BanquetCalendarView, ['banquet.manage'])
    await w.find('[data-testid=room-col-SB]').trigger('click', { clientY: 0 })
    await flushPromises()
    expect(has('[data-testid=banquet-event-dialog]')).toBe(true)
    // the header is saved, then the first session opens with the room, the day and the time of the click
    POST.mockResolvedValueOnce({ data: { id: 11, number: 'BQ-000011', name: 'Wedding Sari', status: 'INQUIRY', sessions: [], version: 1, booker_name: 'PT Maju' } })
    const name = document.body.querySelector('input[name=name]') as HTMLInputElement
    name.value = 'Wedding Sari'
    name.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    ;(document.body.querySelector('select[name=booker_company_id]') as HTMLSelectElement).value = '3'
    ;(document.body.querySelector('select[name=booker_company_id]') as HTMLSelectElement).dispatchEvent(new Event('change', { bubbles: true }))
    await body('[data-testid=banquet-event-save]').trigger('click')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/banquet/events', expect.objectContaining({ body: expect.objectContaining({ name: 'Wedding Sari', booker_company_id: 3 }) }))
    expect(has('[data-testid=banquet-session-dialog]')).toBe(true)
    expect((document.body.querySelector('select[name=room_id]') as HTMLSelectElement).value).toBe('2')
    expect((document.body.querySelector('input[name=session_date]') as HTMLInputElement).value).toBe('2026-11-05')
    expect((document.body.querySelector('input[name=start_time]') as HTMLInputElement).value).toBe('06:00')
  })

  it('does not offer a booking to who may only read', async () => {
    api([])
    const { w } = await setup(BanquetCalendarView, ['banquet.read'])
    expect(w.find('[data-testid=banquet-new]').exists()).toBe(false)
    await w.find('[data-testid=room-col-SA]').trigger('click')
    await flushPromises()
    expect(has('[data-testid=banquet-event-dialog]')).toBe(false)
  })
})

const tentative = {
  id: 20, number: 'BQ-000020', name: 'Gala', status: 'TENTATIVE', event_type_id: 1, event_type_name: 'Wedding', booker_guest_id: null, booker_company_id: 3, booker_name: 'PT Maju', contact_name: null, contact_phone: null, contact_email: null,
  sales_user_id: 5, sales_name: 'Dewi', option_until: '2026-11-10', option_overdue: false, reason_id: null, reason_name: null, reason_note: null, booking_group_id: null, booking_group_name: null, notes: null,
  inquiry_at: '2026-10-20T01:00:00Z', definite_at: null, closed_at: null, version: 3,
  sessions: [
    { id: 31, session_no: 1, name: 'Dinner', session_date: '2026-11-05', start_time: '18:00:00', end_time: '22:00:00', ends_next_day: false, room_id: 1, room_code: 'SA', room_name: 'Saraswati A', setup_style: 'THEATER', pax_estimated: 100, pax_guaranteed: null, pax_actual: null, status: 'ACTIVE', notes: null, rank: 1, room_taken: false, hold_from: '2026-11-05T17:00', hold_to: '2026-11-05T22:30' },
    { id: 32, session_no: 2, name: 'Lunch', session_date: '2026-11-06', start_time: '12:00:00', end_time: '14:00:00', ends_next_day: false, room_id: 2, room_code: 'SB', room_name: 'Saraswati B', setup_style: null, pax_estimated: 50, pax_guaranteed: null, pax_actual: null, status: 'ACTIVE', notes: null, rank: 2, room_taken: false, hold_from: '2026-11-06T11:00', hold_to: '2026-11-06T14:30' },
  ],
}

describe('an event', () => {
  async function open(ev: unknown, permissions: string[]) {
    api([], (path) => (path.endsWith('/banquet/events/{id}') ? { data: ev } : undefined))
    return setup(BanquetEventView, permissions, '/banquet/events/20')
  }

  it('shows the sessions tab first, with the place of each session in the queue and the room held with its buffers', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/banquet/events/:id', component: { template: '<div />' } }] })
    await router.push('/banquet/events/20')
    api([], (path) => (path.endsWith('/banquet/events/{id}') ? { data: tentative } : undefined))
    const pinia = createPinia()
    setActivePinia(pinia)
    useAuthStore().me = { user: { id: 5, email: 'm@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: ['banquet.manage'] }] } as never
    usePropertyStore().currentId = 7
    const w = mount(BanquetEventView, { attachTo: document.body, global: { plugins: [pinia, router] } })
    mounted.push(w)
    await flushPromises()
    expect(w.find('[data-testid=session-list]').exists()).toBe(true)
    expect(w.find('[data-testid=rank-31]').text()).toBe('First option')
    expect(w.find('[data-testid=rank-32]').text()).toBe('Place 2')
    expect(w.find('[data-testid=session-31] [data-testid=hold-range]').text()).toMatch(/^Room held .*17:00–22:30$/)
    expect(w.find('[data-testid=session-31] [data-testid=hold-range]').text()).not.toMatch(/T\d\d:\d\d|2026-/)
    await w.find('[data-testid=tab-details]').trigger('mousedown')
    await w.find('[data-testid=tab-details]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid=event-details]').exists()).toBe(true)
  })

  it('offers Confirm only to who may confirm, and refuses it while an option is in front', async () => {
    const { w } = await open(tentative, ['banquet.manage'])
    expect(w.find('[data-testid=event-confirm]').exists()).toBe(false)
    expect(w.find('[data-testid=event-lost]').exists()).toBe(true)
    w.unmount()
    mounted.pop()
    document.body.innerHTML = ''
    const second = await open(tentative, ['banquet.manage', 'banquet.confirm'])
    await second.w.find('[data-testid=event-confirm]').trigger('click')
    await flushPromises()
    expect(has('[data-testid=banquet-confirm-dialog]')).toBe(true)
    expect(has('[data-testid=confirm-blocked]')).toBe(true)
    expect((document.body.querySelector('[data-testid=confirm-event]') as HTMLButtonElement).disabled).toBe(true)
    expect(body('[data-testid=banquet-confirm-dialog]').text()).not.toMatch(/Rp|IDR|price|deposit/i)
  })

  it('confirms when every session is the first option', async () => {
    const first = { ...tentative, sessions: tentative.sessions.map((s) => ({ ...s, rank: 1 })) }
    POST = vi.fn()
    const { w } = await open(first, ['banquet.confirm'])
    POST.mockResolvedValue({ data: { ...first, status: 'DEFINITE' } })
    await w.find('[data-testid=event-confirm]').trigger('click')
    await flushPromises()
    expect(has('[data-testid=confirm-effect]')).toBe(true)
    await body('[data-testid=confirm-event]').trigger('click')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/banquet/events/{id}/confirm', { params: { path: { propertyId: 7, id: 20 } }, body: { policy_template_id: null } })
    expect(w.find('[data-testid=event-status]').text()).toBe('Definite')
  })

  it('moves a tentative session after showing the place it gives up and the place it gets', async () => {
    const { w } = await open(tentative, ['banquet.manage'])
    POST.mockResolvedValue({ data: { mode: 'QUEUE', available: false, conflicts: [{ event_id: 9, event_number: 'BQ-000009', event_name: 'Other', event_status: 'TENTATIVE', session_id: 90, session_name: 'Dinner', hold_kind: 'OPTION', room_code: 'SB', rank: 1, same_event: false }], rank_in_new_slot: 2, rank_given_up: 1, capacity: 120, capacity_exceeds: false, setup_offered: true, hold_from: '2026-11-05T17:00', hold_to: '2026-11-05T22:30' } })
    await w.find('[data-testid=session-edit-31]').trigger('click')
    await flushPromises()
    expect(has('[data-testid=banquet-session-dialog]')).toBe(true)
    const select = document.body.querySelector('select[name=room_id]') as HTMLSelectElement
    select.value = '2'
    select.dispatchEvent(new Event('change', { bubbles: true }))
    await new Promise((r) => setTimeout(r, 400))
    await flushPromises()
    expect(document.body.querySelector('[data-testid=check-ranks]')?.textContent).toBe('Gives up place 1, takes place 2 in the new queue.')
    // the room is named in full and the option has its place: not the code of the master, not a bare "option"
    const conflict = document.body.querySelector('[data-testid=session-check] ul')?.textContent ?? ''
    expect(conflict).toContain('option #1')
    expect(conflict).toContain('Saraswati B')
    expect(conflict).not.toMatch(/\bSB\b/)
    // a worse place needs the person's yes
    expect((document.body.querySelector('[data-testid=banquet-session-save]') as HTMLButtonElement).disabled).toBe(true)
    const accept = document.body.querySelector('[data-testid=accept-worse]') as HTMLInputElement
    accept.click()
    await flushPromises()
    expect((document.body.querySelector('[data-testid=banquet-session-save]') as HTMLButtonElement).disabled).toBe(false)
  })

  it('refuses a room that is taken when the setting refuses', async () => {
    const { w } = await open(tentative, ['banquet.manage'])
    POST.mockResolvedValue({ data: { mode: 'REJECT', available: false, conflicts: [{ event_id: 9, event_number: 'BQ-000009', event_name: 'Other', event_status: 'DEFINITE', session_id: 90, session_name: 'Dinner', hold_kind: 'DEFINITE', room_code: 'SB', same_event: false }], rank_in_new_slot: 2, rank_given_up: 1, capacity: 120, capacity_exceeds: false, setup_offered: true, hold_from: '17:00', hold_to: '22:30' } })
    await w.find('[data-testid=session-edit-31]').trigger('click')
    await flushPromises()
    const select = document.body.querySelector('select[name=room_id]') as HTMLSelectElement
    select.value = '2'
    select.dispatchEvent(new Event('change', { bubbles: true }))
    await new Promise((r) => setTimeout(r, 400))
    await flushPromises()
    expect(document.body.querySelector('[data-testid=check-conflict]')?.textContent).toContain('taken')
    expect((document.body.querySelector('[data-testid=banquet-session-save]') as HTMLButtonElement).disabled).toBe(true)
  })
})

describe('the masters', () => {
  it('lists a combined room with its parts and the capacity of each set-up, and saves the parts and set-ups with the room', async () => {
    const ball = room(3, 'BALL', 'Ballroom', { is_combo: true, parts: [{ room_id: 1, code: 'SA', name: 'Saraswati A' }, { room_id: 2, code: 'SB', name: 'Saraswati B' }], setups: [{ setup_style: 'THEATER', capacity: 300 }] })
    api([], (path) => (path.endsWith('/banquet/rooms') ? { data: { data: [A, B, ball] } } : undefined))
    const { w } = await setup(BanquetRoomsView, ['banquet.setup'])
    expect(w.find('[data-testid=room-BALL] [data-testid=room-parts]').text()).toBe('Made of: Saraswati A + Saraswati B')
    expect(w.find('[data-testid=room-BALL] [data-testid=room-setups]').text()).toBe('Theatre 300')
    PATCH.mockResolvedValue({ data: { ...ball } })
    await w.find('[data-testid=room-edit-BALL]').trigger('click')
    await flushPromises()
    await body('[data-testid=room-save]').trigger('submit')
    await flushPromises()
    expect(PATCH).toHaveBeenCalled()
    expect(PUT).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/banquet/rooms/{id}/parts', expect.objectContaining({ body: { room_ids: [1, 2] } }))
    expect(PUT).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/banquet/rooms/{id}/setups', expect.objectContaining({ body: { setups: [{ setup_style: 'THEATER', capacity: 300 }] } }))
  })

  it('does not offer to add a room to who may only read', async () => {
    api()
    const { w } = await setup(BanquetRoomsView, ['banquet.read'])
    expect(w.find('[data-testid=room-new]').exists()).toBe(false)
    expect(w.find('[data-testid=room-edit-SA]').exists()).toBe(false)
  })

  it('saves the conflict mode of tentative bookings', async () => {
    api()
    const { w } = await setup(BanquetSettingsView, ['banquet.setup'])
    const queue = w.find('[data-testid=mode-QUEUE]')
    await queue.setValue(true)
    await w.find('[data-testid=conflict-section] form').trigger('submit')
    await flushPromises()
    expect(PUT).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/banquet/settings', expect.objectContaining({ body: expect.objectContaining({ tentative_conflict_mode: 'QUEUE' }) }))
  })
})

describe('the texts', () => {
  it('has the same banquet keys in English and in Indonesian', () => {
    const keys = (o: Record<string, unknown>, p = ''): string[] => Object.entries(o).flatMap(([k, v]) => (typeof v === 'object' && v !== null ? keys(v as Record<string, unknown>, `${p}${k}.`) : [`${p}${k}`]))
    expect(keys(id.banquet as never).sort()).toEqual(keys(en.banquet as never).sort())
    expect(Object.keys(en.banquet.status)).toHaveLength(6)
  })
})

const BALL = room(3, 'BALL', 'Ballroom Saraswati', { is_combo: true, parts: [{ room_id: 1, code: 'SA', name: 'Saraswati A' }, { room_id: 2, code: 'SB', name: 'Saraswati B' }] })
const lock = (sessionId: number, roomId: number, over: Record<string, unknown> = {}) => ({
  room_id: roomId, session_id: sessionId, event_id: 10 + sessionId, event_number: 'BQ-000001', event_name: 'Gala', event_status: 'DEFINITE', hold_kind: 'DEFINITE', via_room_id: 3, via_room_code: 'BALL', via_room_name: 'Ballroom',
  hold_from: '2026-11-05T17:00', hold_to: '2026-11-05T23:30', ...over,
})
function withLocks(sessions: unknown[], locks: unknown[]) {
  api(sessions, (path) => (path.endsWith('/banquet/calendar') ? { data: { from: '2026-11-05', to: '2026-11-05', rooms: [A, B, BALL], sessions, locks } } : undefined))
}
// the gala is booked in the ballroom (holds A and B), a meeting in A holds the ballroom
const gala = session(1, { room_id: 3, room_code: 'BALL', room_name: 'Ballroom Saraswati', start_time: '18:00:00', end_time: '23:00:00' })
const meeting = session(2, { event_status: 'TENTATIVE', rank: 1, room_id: 1, start_time: '09:00:00', end_time: '11:00:00' })
const galaLocks = [lock(1, 1), lock(1, 2)]
const meetingLock = lock(2, 3, { event_number: 'BQ-000002', event_name: 'Meeting', event_status: 'TENTATIVE', hold_kind: 'OPTION', via_room_id: 1, via_room_code: 'SA', via_room_name: 'Saraswati A', hold_from: '2026-11-05T08:00', hold_to: '2026-11-05T11:30' })

describe('the rooms that another booking locks', () => {
  it('shows a combined room\'s booking on its parts, and a part\'s booking on the combined room, hatched with the room it is booked in (day)', async () => {
    withLocks([gala, meeting], [...galaLocks, meetingLock])
    const { w } = await setup(BanquetCalendarView, ['banquet.manage'])
    expect(w.find('[data-testid=lock-1-SA]').text()).toBe('Locked: Ballroom · BQ-000001')
    expect(w.find('[data-testid=lock-1-SB]').text()).toBe('Locked: Ballroom · BQ-000001')
    expect(w.find('[data-testid=room-col-BALL] [data-testid=lock-2-BALL]').text()).toBe('Option: Saraswati A · BQ-000002')
    // from the set-up to the tear-down: 17:00 to 23:30 on a grid of 06:00 to 24:00 starts at 61.1 %
    expect(w.find('[data-testid=lock-1-SA]').attributes('style')).toContain('top: 61.1')
    expect(w.find('[data-testid=lock-1-SA]').attributes('style')).toContain('repeating-linear-gradient')
    // the booking itself is in its own column and not locked by itself
    expect(w.find('[data-testid=room-col-BALL] [data-testid=block-1]').exists()).toBe(true)
    expect(w.find('[data-testid=room-col-BALL] [data-testid=lock-1-BALL]').exists()).toBe(false)
    expect(w.find('[data-testid=legend-lock]').text()).toBe('Locked by a booking in another room')
  })

  it('shows them in the week and in the list of the phone', async () => {
    withLocks([gala, meeting], [...galaLocks, meetingLock])
    const { w } = await setup(BanquetCalendarView, ['banquet.manage'])
    expect(w.find('[data-testid=agenda-lock-1-1]').text()).toContain('Saraswati A')
    expect(w.find('[data-testid=agenda-lock-1-1]').text()).toContain('Locked: Ballroom · BQ-000001')
    expect(w.find('[data-testid=agenda-lock-1-1]').text()).toContain('17:00–23:30')
    await w.find('[data-testid=view-week]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid=week-row-SA] [data-testid=chip-lock-1-SA]').text()).toBe('Locked: Ballroom · BQ-000001')
    expect(w.find('[data-testid=week-row-SB] [data-testid=chip-lock-1-SB]').exists()).toBe(true)
    expect(w.find('[data-testid=week-row-BALL] [data-testid=chip-lock-2-BALL]').text()).toBe('Option: Saraswati A · BQ-000002')
  })

  it('cuts a hold that runs past midnight at the end of the day and shows the rest on the next', async () => {
    const late = lock(1, 1, { hold_from: '2026-11-05T21:00', hold_to: '2026-11-06T01:30' })
    withLocks([], [late])
    const { w } = await setup(BanquetCalendarView, ['banquet.read'])
    // 21:00 to 24:00 on the day of the calendar
    expect(w.find('[data-testid=lock-1-SA]').attributes('style')).toContain('top: 83.3')
    expect(w.find('[data-testid=lock-1-SA]').attributes('style')).toContain('height: 16.6')
    await w.find('[data-testid=view-week]').trigger('click')
    await flushPromises()
    expect(w.findAll('[data-testid=week-row-SA] [data-testid=chip-lock-1-SA]')).toHaveLength(2)
  })

  it('is written in Indonesian with the terms of the design: Definit and Inquiry', async () => {
    setLocale('id')
    withLocks([gala, meeting], [...galaLocks, meetingLock])
    const { w } = await setup(BanquetCalendarView, ['banquet.manage'])
    expect(w.find('[data-testid=lock-1-SA]').text()).toBe('Terkunci: Ballroom · BQ-000001')
    expect(w.find('[data-testid=calendar-legend]').text()).toContain('Definit')
    expect(w.find('[data-testid=calendar-legend]').text()).toContain('Ruang diambil acara definit')
    expect(id.banquet.status.DEFINITE).toBe('Definit')
    expect(id.banquet.status.INQUIRY).toBe('Inquiry')
    expect(JSON.stringify(id.banquet)).not.toMatch(/\bpasti\b|penjajakan/i)
    expect(JSON.stringify((id.emptyState as Record<string, unknown>).banquetEvents)).not.toMatch(/penjajakan/i)
    expect(id.banquet.hold.optionRank).toBe('opsi #{n}')
  })
})

describe('sessions that overlap in a room', () => {
  it('sit side by side, and every block says when, however short', async () => {
    api([
      session(2, { event_status: 'TENTATIVE', rank: 1, room_id: 2, room_code: 'SB', start_time: '09:00:00', end_time: '11:00:00' }),
      session(3, { event_status: 'TENTATIVE', rank: 2, room_id: 2, room_code: 'SB', start_time: '10:00:00', end_time: '10:30:00' }),
      session(1, { room_id: 2, room_code: 'SB', start_time: '15:00:00', end_time: '16:00:00' }),
    ])
    const { w } = await setup(BanquetCalendarView, ['banquet.manage'])
    expect(w.find('[data-testid=block-2]').attributes('data-lanes')).toBe('2')
    expect(w.find('[data-testid=block-3]').attributes('data-lanes')).toBe('2')
    expect(w.find('[data-testid=block-1]').attributes('data-lanes')).toBe('1')
    const left = (id: number): string => /left: ([^;]+);/.exec(w.find(`[data-testid=block-${id}]`).attributes('style') ?? '')?.[1] ?? ''
    expect(left(2)).toContain('0%')
    expect(left(3)).toContain('50%')
    expect(w.find('[data-testid=block-3]').attributes('style')).toContain('width: calc(50% - 0.5rem)')
    expect(w.find('[data-testid=block-1]').attributes('style')).toContain('width: calc(100% - 0.5rem)')
    // the label begins with the hours, so a block cut to one line still shows them
    expect(w.find('[data-testid=block-3]').text()).toMatch(/^10:00–10:30 · Event 3/)
    expect(w.find('[data-testid=block-2]').text()).toMatch(/^09:00–11:00 · Event 2/)
  })
})

describe('the lists', () => {
  it('shows the price mode beside the price of an item and of a package', async () => {
    const item = (n: number, code: string, mode: string) => ({ id: n, code, name: `Item ${code}`, kind: 'MENU', unit_basis: 'PER_PAX', charge_code_id: n, charge_code: `FB${n}`, charge_code_name: 'F&B', price_mode: mode, list_price: '150000', fb_class: 'FOOD', pos_product_id: null, description: null, is_active: true })
    const comp = (n: number, mode: string) => ({ item_id: n, item_code: `I${n}`, item_name: `Item ${n}`, item_kind: 'MENU', price_mode: mode, qty_per_pax: '1', sort_order: n })
    const pkg = (n: number, code: string, mode: string | null, comps: unknown[]) => ({ id: n, code, name: `Package ${code}`, price_per_pax: '363000', price_mode: mode, description: null, is_active: true, components: comps })
    api([], (path) => {
      if (path.endsWith('/banquet/items')) return { data: { data: [item(1, 'LUNCH', 'INCLUSIVE'), item(2, 'SOUND', 'EXCLUSIVE')] } }
      if (path.endsWith('/banquet/packages')) return { data: { data: [pkg(1, 'NETT', 'INCLUSIVE', [comp(1, 'INCLUSIVE')]), pkg(2, 'PLUS', 'EXCLUSIVE', [comp(2, 'EXCLUSIVE')]), pkg(3, 'BOTH', 'MIXED', [comp(1, 'INCLUSIVE'), comp(2, 'EXCLUSIVE')]), pkg(4, 'EMPTY', null, [])] } }
      return undefined
    })
    const { w } = await setup(BanquetCatalogView, ['banquet.read'])
    expect(w.find('[data-testid=item-price-LUNCH]').text()).toContain('Nett')
    expect(w.find('[data-testid=item-price-SOUND]').text()).toContain('++')
    await w.find('[data-testid=tab-packages]').trigger('mousedown')
    await w.find('[data-testid=tab-packages]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid=package-price-NETT]').text()).toContain('Nett')
    expect(w.find('[data-testid=package-price-PLUS]').text()).toContain('++')
    expect(w.find('[data-testid=package-price-BOTH]').text()).toContain('Mixed')
    expect(w.find('[data-testid=package-price-EMPTY]').text()).not.toMatch(/Nett|\+\+|Mixed/)
  })

  it('has a guests column in the list of events: the guaranteed number where there is one, else the estimate', async () => {
    const row = (n: number, over: Record<string, unknown>) => ({ id: n, number: `BQ-00000${n}`, status: 'DEFINITE', name: `Event ${n}`, event_type_id: 1, event_type_name: 'Wedding', booker_name: 'PT Maju', sales_user_id: 5, sales_name: 'Dewi', option_until: null, option_overdue: false, first_date: '2026-11-05', last_date: '2026-11-05', sessions: 1, pax_estimated: 100, pax: 100, pax_guaranteed: false, version: 1, ...over })
    api([], (path) => (path.endsWith('/banquet/events') ? { data: { data: [row(1, { pax: 160, pax_guaranteed: true, sessions: 2 }), row(2, { pax: 90 }), row(3, { sessions: 0, pax: 0, first_date: null, last_date: null })] } } : undefined))
    const { w } = await setup(BanquetEventsView, ['banquet.read'])
    expect(w.text()).toContain('Guests')
    expect(w.find('[data-testid=pax-BQ-000001]').text()).toBe('160 (guaranteed)')
    expect(w.find('[data-testid=pax-BQ-000002]').text()).toBe('90 (estimate)')
    expect(w.find('[data-testid=pax-BQ-000003]').exists()).toBe(false)
  })
})
