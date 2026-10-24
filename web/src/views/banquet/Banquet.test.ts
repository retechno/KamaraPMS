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
    if (path.endsWith('/banquet/settings')) return { data: { tentative_conflict_mode: 'REJECT', operating_from: null, operating_to: null } }
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
    { id: 31, session_no: 1, name: 'Dinner', session_date: '2026-11-05', start_time: '18:00:00', end_time: '22:00:00', ends_next_day: false, room_id: 1, room_code: 'SA', room_name: 'Saraswati A', setup_style: 'THEATER', pax_estimated: 100, pax_guaranteed: null, pax_actual: null, status: 'ACTIVE', notes: null, rank: 1, room_taken: false, hold_from: '17:00', hold_to: '22:30' },
    { id: 32, session_no: 2, name: 'Lunch', session_date: '2026-11-06', start_time: '12:00:00', end_time: '14:00:00', ends_next_day: false, room_id: 2, room_code: 'SB', room_name: 'Saraswati B', setup_style: null, pax_estimated: 50, pax_guaranteed: null, pax_actual: null, status: 'ACTIVE', notes: null, rank: 2, room_taken: false, hold_from: '11:00', hold_to: '14:30' },
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
    expect(w.find('[data-testid=session-31] [data-testid=hold-range]').text()).toBe('Room held 17:00 – 22:30')
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
    expect(document.body.textContent).not.toMatch(/Rp|IDR|price|deposit/i)
  })

  it('confirms when every session is the first option, and shows no money', async () => {
    const first = { ...tentative, sessions: tentative.sessions.map((s) => ({ ...s, rank: 1 })) }
    POST = vi.fn()
    const { w } = await open(first, ['banquet.confirm'])
    POST.mockResolvedValue({ data: { ...first, status: 'DEFINITE' } })
    await w.find('[data-testid=event-confirm]').trigger('click')
    await flushPromises()
    expect(has('[data-testid=confirm-effect]')).toBe(true)
    await body('[data-testid=confirm-event]').trigger('click')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/banquet/events/{id}/confirm', { params: { path: { propertyId: 7, id: 20 } } })
    expect(w.find('[data-testid=event-status]').text()).toBe('Definite')
  })

  it('moves a tentative session after showing the place it gives up and the place it gets', async () => {
    const { w } = await open(tentative, ['banquet.manage'])
    POST.mockResolvedValue({ data: { mode: 'QUEUE', available: false, conflicts: [{ event_id: 9, event_number: 'BQ-000009', event_name: 'Other', event_status: 'TENTATIVE', session_id: 90, session_name: 'Dinner', hold_kind: 'OPTION', room_code: 'SB', same_event: false }], rank_in_new_slot: 2, rank_given_up: 1, capacity: 120, capacity_exceeds: false, setup_offered: true, hold_from: '17:00', hold_to: '22:30' } })
    await w.find('[data-testid=session-edit-31]').trigger('click')
    await flushPromises()
    expect(has('[data-testid=banquet-session-dialog]')).toBe(true)
    const select = document.body.querySelector('select[name=room_id]') as HTMLSelectElement
    select.value = '2'
    select.dispatchEvent(new Event('change', { bubbles: true }))
    await new Promise((r) => setTimeout(r, 400))
    await flushPromises()
    expect(document.body.querySelector('[data-testid=check-ranks]')?.textContent).toBe('Gives up place 1, takes place 2 in the new queue.')
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
