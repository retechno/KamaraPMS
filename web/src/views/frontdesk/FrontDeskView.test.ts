import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ArrivalsView from './ArrivalsView.vue'
import FrontDeskView from './FrontDeskView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: vi.fn() } }))

const arrival = (id: number, guest: string, over: object = {}) => ({
  reservation_id: id, confirmation_number: `RES00000${id}`, reservation_room_id: id, reservation_version: 1, guest_id: 3, guest_name: guest,
  room_type_id: 10, room_type_code: 'DLX', room_type_name: 'Deluxe', room_id: null, arrival_date: '2026-09-30', departure_date: '2026-10-02', adult_count: 2, child_count: 0,
  status: 'CONFIRMED', reservation_status: 'CONFIRMED', rate: { rate_plan_code: 'BAR', rate_plan_name: 'Best', amount: '1000000', price_mode: 'EXCLUSIVE' }, company: null, deposit: null,
  readiness: { status: 'BLOCKED', blockers: ['ROOM_NOT_ASSIGNED'] }, ...over,
})
const inHouse = (id: number, number: string, guest: string, departure: string) => ({
  id, stay_number: number, version: 1, reservation_id: id, confirmation_number: `RES00000${id}`,
  guest: { id: 30 + id, name: guest }, room: { id: id, number: `10${id}`, room_type_code: 'DLX', room_type_name: 'Deluxe' }, company: null, billing: [],
  rate: { rate_plan_code: 'BAR', rate_plan_name: 'Best', amount: '1000000', price_mode: 'EXCLUSIVE', is_override: false },
  stay: { arrival_date: '2026-09-28', departure_date: departure, nights: 5, adults: 2, children: 1 },
  balance: { amount: '0', status: 'SETTLED', folios: [{ id: 80 + id, folio_number: `FOL00000${id}`, folio_type: 'GUEST', bill_to_company_id: null, status: 'OPEN', balance: '0' }] },
  checkout: { status: 'READY', uncharged_nights: 0 },
})

let mounted: VueWrapper | null = null

async function mountDesk(tab: 'arrivals' | 'in-house' | 'departures' = 'arrivals', permissions = ['reservation.read', 'frontdesk.checkin', 'reservation.create', 'frontdesk.checkout']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-09-30' } as never
  property.current = { require_room_inspection_for_checkin: false } as never
  GET = vi.fn(async (path: string, init?: { params?: { query?: { departure_until?: string } } }) => {
    if (path.endsWith('/arrivals')) return { data: { data: [arrival(1, 'Siti'), arrival(2, 'Budi', { room_id: 30, room_number: '301', housekeeping_status: 'DIRTY' })] } }
    if (path.endsWith('/stays/in-house') && init?.params?.query?.departure_until) return { data: { data: [inHouse(5, 'STY000005', 'Wayan', '2026-09-29')] } } // departures: overdue
    if (path.endsWith('/stays/in-house')) {
      return { data: { data: [inHouse(5, 'STY000005', 'Wayan', '2026-09-29'), inHouse(6, 'STY000006', 'Dewi', '2026-10-03'), inHouse(7, 'STY000007', 'Agus', '2026-10-04')], next_cursor: 'c2' } }
    }
    return { data: { data: [] } }
  })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  await router.push(`/${tab}`)
  mounted = mount(FrontDeskView, { props: { tab }, attachTo: document.body, global: { plugins: [pinia, router] } })
  await flushPromises()
  return { router }
}

describe('FrontDeskView', () => {
  beforeEach(() => setLocale('en'))
  afterEach(() => {
    mounted?.unmount()
    mounted = null
    document.body.innerHTML = ''
  })

  it('has the page title, the business date and the three tabs with their counts', async () => {
    await mountDesk()
    expect(mounted!.get('h1').text()).toBe('Front desk')
    expect(mounted!.get('[data-testid=front-desk-date]').text()).toBe('30 Sep 2026')
    expect(mounted!.get('[data-testid=tab-arrivals]').text()).toContain('Arrivals')
    expect(mounted!.get('[data-testid=count-arrivals]').text()).toBe('2')
    expect(mounted!.get('[data-testid=count-in-house]').text()).toBe('3+') // more than the first page
    expect(mounted!.get('[data-testid=count-departures]').text()).toBe('1')
  })

  it('shows the tab of its route and only that one', async () => {
    await mountDesk('in-house')
    expect(mounted!.get('[data-testid=tab-in-house]').attributes('data-state')).toBe('active')
    expect(mounted!.get('[data-testid=panel-arrivals]').classes()).toContain('hidden')
    expect(mounted!.get('[data-testid=panel-in-house]').classes()).not.toContain('hidden')
    expect(mounted!.get('[data-testid=panel-in-house]').text()).toContain('Dewi')
  })

  it('goes to the address of a tab when it is chosen', async () => {
    const { router } = await mountDesk()
    const tab = mounted!.get('[data-testid=tab-departures]')
    await tab.trigger('mousedown', { button: 0 })
    await tab.trigger('focus')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/departures')
  })

  it('offers walk-in and a new reservation to those who may', async () => {
    await mountDesk()
    expect(mounted!.get('[data-testid=walk-in]').attributes('href')).toBe('/walk-in')
    expect(mounted!.get('[data-testid=new-reservation]').attributes('href')).toBe('/reservations/new')
    mounted!.unmount()
    await mountDesk('arrivals', ['reservation.read'])
    expect(mounted!.find('[data-testid=walk-in]').exists()).toBe(false)
    expect(mounted!.find('[data-testid=new-reservation]').exists()).toBe(false)
  })

  it('lists the arrivals with the housekeeping state of an assigned room, and opens check-in in a sheet', async () => {
    await mountDesk()
    const row = mounted!.get('[data-testid=arrival-2]')
    expect(row.text()).toContain('301')
    expect(row.text()).toContain('Dirty')
    await mounted!.get('[data-testid=open-1]').trigger('click')
    await flushPromises()
    const sheet = document.body.querySelector('[data-testid=checkin-sheet]')!
    expect(sheet.textContent).toContain('Check in Siti · DLX')
    expect(sheet.textContent).toContain('RES000001')
    expect(sheet.querySelector('form[data-testid=checkin-1]')).not.toBeNull()
  })

  it('marks an overdue departure and offers check-out to those who may', async () => {
    await mountDesk('departures')
    expect(mounted!.get('[data-testid=overdue]').text()).toBe('overdue')
    expect(mounted!.get('[data-testid=checkOut-STY000005]').text()).toBe('Check out')
    mounted!.unmount()
    await mountDesk('departures', ['reservation.read'])
    expect(mounted!.find('[data-testid=checkOut-STY000005]').exists()).toBe(false)
  })

  it('reads the other lists again after a check-in', async () => {
    await mountDesk('arrivals')
    const reads = () => GET.mock.calls.filter((c) => String(c[0]).endsWith('/stays/in-house')).length
    const before = reads()
    mounted!.findComponent(ArrivalsView).vm.$emit('changed')
    await flushPromises()
    expect(reads()).toBe(before + 2) // the in-house and the departures lists
  })

  it('sorts a list by a column', async () => {
    await mountDesk('in-house')
    const guests = () => mounted!.findAll('[data-testid=panel-in-house] tbody tr').map((r) => r.findAll('td')[1]!.get('button').text())
    expect(guests()).toEqual(['Wayan', 'Dewi', 'Agus'])
    await mounted!.get('[data-testid=panel-in-house] [data-testid=sort-guest_name]').trigger('click')
    expect(guests()).toEqual(['Agus', 'Dewi', 'Wayan'])
  })

  it('needs read permission', async () => {
    await mountDesk('arrivals', ['guest.read'])
    expect(mounted!.findAll('[data-testid=no-access]')).toHaveLength(3)
    expect(mounted!.find('[data-testid=count-arrivals]').exists()).toBe(false)
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    await mountDesk()
    expect(mounted!.get('[data-testid=tab-departures]').text()).toContain('Keberangkatan')
    expect(mounted!.get('[data-testid=open-1]').text()).toBe('Check-in')
    expect(mounted!.get('[data-testid=panel-arrivals] thead').text()).toContain('Siap check-in')
  })
})
