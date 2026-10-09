import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ArrivalsView from './ArrivalsView.vue'
import DeparturesView from './DeparturesView.vue'
import InHouseView from './InHouseView.vue'

// What the browser check of the front desk lists found: a slow answer that replaced a newer one, a date input that did not say which date is asked, a credit called a balance due, a filter on the rows
// that are loaded, and a menu left open behind the dialog it opened.

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), PATCH: vi.fn() } }))

const arrival = (id: number, name: string, over: object = {}) => ({
  reservation_id: id, confirmation_number: `RES00000${id}`, reservation_room_id: id, reservation_version: 1, guest_id: 3, guest_name: name, room_type_id: 10, room_type_code: 'DLX', room_type_name: 'Deluxe',
  room_id: null, arrival_date: '2026-10-09', departure_date: '2026-10-10', adult_count: 2, child_count: 0, status: 'CONFIRMED', reservation_status: 'CONFIRMED',
  rate: { rate_plan_code: 'RO', rate_plan_name: 'Room only', amount: '800000', price_mode: 'EXCLUSIVE' }, company: null, deposit: null, readiness: { status: 'BLOCKED', blockers: ['ROOM_NOT_ASSIGNED'] }, ...over,
})
const folio = (over: object = {}) => ({ id: 80, folio_number: 'FOL000001', folio_type: 'GUEST', bill_to_company_id: null, status: 'OPEN', balance: '0', ...over })
const stay = (id: number, name: string, over: object = {}) => ({
  id, stay_number: `STY00000${id}`, version: 1, reservation_id: id, confirmation_number: `RES00000${id}`, guest: { id: 30 + id, name }, room: { id, number: `10${id}`, room_type_code: 'DLX', room_type_name: 'Deluxe' },
  company: null, billing: [], rate: { rate_plan_code: 'RO', rate_plan_name: 'Room only', amount: '800000', price_mode: 'EXCLUSIVE', is_override: false },
  stay: { arrival_date: '2026-10-08', departure_date: '2026-10-09', nights: 1, adults: 2, children: 0 },
  balance: { amount: '0', status: 'SETTLED', folios: [folio()] }, checkout: { status: 'READY', uncharged_nights: 0 }, ...over,
})

let mounted: VueWrapper | null = null

function setup(permissions = ['reservation.read', 'frontdesk.checkin', 'frontdesk.checkout', 'guest.write', 'frontdesk.rate_change']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-10-09' } as never
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return { pinia, router }
}

/** A request whose answer is given by hand, so that a test decides which of two requests answers first. */
function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((r) => { resolve = r })
  return { promise, resolve }
}

describe('front desk lists', () => {
  beforeEach(() => {
    setLocale('en')
  })
  afterEach(() => {
    mounted?.unmount()
    mounted = null
    document.body.innerHTML = ''
  })

  it('arrivals: a slow answer to an older filter does not replace the answer to the newer one', async () => {
    const { pinia, router } = setup()
    const slow = deferred<{ data: { data: object[] } }>()
    GET = vi.fn((path: string, init?: { params?: { query?: Record<string, string> } }) => {
      if (!path.endsWith('/arrivals')) return Promise.resolve({ data: { data: [] } })
      const q = init?.params?.query ?? {}
      if (q.status === 'CANCELLED') return Promise.resolve({ data: { data: [arrival(2, 'Cancelled Guest', { status: 'CANCELLED' })] } })
      if (q.q === 'a') return slow.promise // the first request, asked for a search, answers last
      return Promise.resolve({ data: { data: [arrival(1, 'Siti Nurhaliza')] } })
    })
    mounted = mount(ArrivalsView, { attachTo: document.body, global: { plugins: [pinia, router] } })
    await flushPromises()
    await mounted.get('input[name=q]').setValue('a') // the older request, still on its way
    await mounted.get('select[name=status]').setValue('CANCELLED') // the newer one
    await flushPromises()
    expect(mounted.findAll('tbody tr[data-testid^=arrival-]').map((r) => r.text())).toEqual([expect.stringContaining('Cancelled Guest')])
    slow.resolve({ data: { data: [arrival(3, 'Stale One'), arrival(4, 'Stale Two')] } })
    await flushPromises()
    expect(mounted.findAll('tbody tr[data-testid^=arrival-]')).toHaveLength(1)
    expect(mounted.text()).not.toContain('Stale')
  })

  it('arrivals: the date input shows the business date, and choosing it again is the default (no date is sent, and the count of the tab is kept)', async () => {
    const { pinia, router } = setup()
    GET = vi.fn(async (path: string) => (path.endsWith('/arrivals') ? { data: { data: [arrival(1, 'Siti Nurhaliza')] } } : { data: { data: [] } }))
    mounted = mount(ArrivalsView, { attachTo: document.body, global: { plugins: [pinia, router] } })
    await flushPromises()
    const input = mounted.get('input[name=date]')
    expect((input.element as HTMLInputElement).value).toBe('2026-10-09')
    await input.setValue('2026-10-10')
    await flushPromises()
    expect(GET.mock.calls.filter((c) => String(c[0]).endsWith('/arrivals')).at(-1)?.[1]).toMatchObject({ params: { query: { date: '2026-10-10' } } })
    await input.setValue('2026-10-09')
    await flushPromises()
    expect(GET.mock.calls.filter((c) => String(c[0]).endsWith('/arrivals')).at(-1)?.[1]).toMatchObject({ params: { query: { date: undefined } } })
    expect(mounted.emitted('loaded')?.at(-1)).toEqual([1])
  })

  it('arrivals: names the status of a room like the reservation: reserved, not confirmed', async () => {
    const { pinia, router } = setup()
    GET = vi.fn(async (path: string) => (path.endsWith('/arrivals') ? { data: { data: [arrival(1, 'Siti Nurhaliza')] } } : { data: { data: [] } }))
    mounted = mount(ArrivalsView, { attachTo: document.body, global: { plugins: [pinia, router] } })
    await flushPromises()
    expect(mounted.get('[data-testid=arrival-1] [data-slot=status-badge]').text()).toBe('Reserved')
  })

  it('departures: a slow answer to an older search does not replace the answer to the newer one', async () => {
    const { pinia, router } = setup()
    const slow = deferred<{ data: { data: object[] } }>()
    GET = vi.fn((path: string, init?: { params?: { query?: Record<string, string> } }) => {
      if (!path.endsWith('/stays/in-house')) return Promise.resolve({ data: { data: [] } })
      const q = init?.params?.query?.q
      if (q === 'Siti') return slow.promise
      if (q === 'Budi') return Promise.resolve({ data: { data: [stay(2, 'Budi Santoso')] } })
      return Promise.resolve({ data: { data: [stay(1, 'Siti Nurhaliza'), stay(2, 'Budi Santoso')] } })
    })
    mounted = mount(DeparturesView, { attachTo: document.body, global: { plugins: [pinia, router] } })
    await flushPromises()
    await mounted.get('input[name=q]').setValue('Siti')
    await mounted.get('input[name=q]').setValue('Budi')
    await flushPromises()
    slow.resolve({ data: { data: [stay(1, 'Siti Nurhaliza')] } }) // the older answer comes last
    await flushPromises()
    expect(mounted.findAll('tbody tr[data-testid^=stay-]').map((r) => r.text())).toEqual([expect.stringContaining('Budi Santoso')])
  })

  it('departures: the date input shows the business date', async () => {
    const { pinia, router } = setup()
    GET = vi.fn(async () => ({ data: { data: [stay(1, 'Siti Nurhaliza')] } }))
    mounted = mount(DeparturesView, { attachTo: document.body, global: { plugins: [pinia, router] } })
    await flushPromises()
    expect((mounted.get('input[name=departure_date]').element as HTMLInputElement).value).toBe('2026-10-09')
  })

  it('departures: a guest folio that holds the guest money is a credit to settle, not a balance due; a guest who owes is still a balance due', async () => {
    const { pinia, router } = setup()
    GET = vi.fn(async () => ({ data: { data: [
      stay(1, 'Credit Guest', { balance: { amount: '-700000', status: 'CREDIT', folios: [folio({ balance: '-700000' })] }, checkout: { status: 'BALANCE_DUE', uncharged_nights: 0 } }),
      stay(2, 'Owing Guest', { balance: { amount: '800000', status: 'OUTSTANDING', folios: [folio({ id: 81, balance: '800000' })] }, checkout: { status: 'BALANCE_DUE', uncharged_nights: 0 } }),
    ] } }))
    mounted = mount(DeparturesView, { attachTo: document.body, global: { plugins: [pinia, router] } })
    await flushPromises()
    const badge = (n: string) => mounted!.get(`[data-testid=checkout-status-${n}]`)
    expect(badge('STY000001').text()).toBe('Credit to settle')
    expect(badge('STY000002').text()).toBe('Balance due')
  })

  it('departures: has no filter on the rows that are loaded (it has a search that asks the server); in-house keeps its filters', async () => {
    const { pinia, router } = setup()
    GET = vi.fn(async () => ({ data: { data: [stay(1, 'Siti Nurhaliza')] } }))
    mounted = mount(DeparturesView, { attachTo: document.body, global: { plugins: [pinia, router] } })
    await flushPromises()
    expect(mounted.find('[data-testid=table-filters]').exists()).toBe(false)
    mounted.unmount()
    mounted = mount(InHouseView, { attachTo: document.body, global: { plugins: [pinia, router] } })
    await flushPromises()
    expect(mounted.find('[data-testid=table-filters]').exists()).toBe(true)
  })

  it('in-house: the menu of a row closes when an action opens a dialog', async () => {
    const { pinia, router } = setup()
    GET = vi.fn(async (path: string) => (path.endsWith('/stays/in-house') ? { data: { data: [stay(1, 'Siti Nurhaliza')] } } : { data: { stay: { id: 1, version: 1 }, nightly_rates: [] } }))
    mounted = mount(InHouseView, { attachTo: document.body, global: { plugins: [pinia, router] } })
    await flushPromises()
    await mounted.get('[data-testid=more-STY000001]').trigger('click')
    await flushPromises()
    expect(document.body.querySelector('[data-testid=menu-STY000001]')).not.toBeNull()
    ;(document.body.querySelector('[data-testid=editRate-STY000001]') as HTMLElement).click()
    await flushPromises()
    expect(document.body.querySelector('[data-testid=edit-rate-dialog]')).not.toBeNull()
    expect(document.body.querySelector('[data-testid=menu-STY000001]')).toBeNull()
  })
})
