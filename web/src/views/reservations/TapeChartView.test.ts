import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import TapeChartView from './TapeChartView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const booking = { reservation_id: 9, confirmation_number: 'RES000009', reservation_room_id: 4, status: 'CONFIRMED', guest_name: 'Siti Nurhaliza', arrival_date: '2026-10-02', departure_date: '2026-10-04' }
const chart = {
  from: '2026-10-01', to: '2026-10-15',
  rooms: [
    { room_id: 1, room_number: '101', room_type_id: 10, room_type_code: 'DLX', bookings: [booking], blocks: [] },
    { room_id: 2, room_number: '102', room_type_id: 10, room_type_code: 'DLX', bookings: [], blocks: [{ id: 5, block_type: 'OOS', start_date: '2026-10-05', end_date: '2026-10-07' }] },
  ],
  unassigned: [{ room_type_id: 10, room_type_code: 'DLX', bookings: [{ ...booking, reservation_id: 10, reservation_room_id: 5, arrival_date: '2026-10-03', departure_date: '2026-10-04' }] }],
}

function mountView(permissions = ['reservation.read']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-10-01' } as never
  GET = vi.fn().mockResolvedValue({ data: chart })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(TapeChartView, { global: { plugins: [pinia, router] } })
}

describe('TapeChartView', () => {
  beforeEach(() => {
    GET = vi.fn()
  })

  it('loads 14 days from the business date and draws bookings, blocks and unassigned counts', async () => {
    const w = mountView()
    await flushPromises()
    expect(GET.mock.calls[0]?.[1]).toMatchObject({ params: { path: { propertyId: 7 }, query: { from: '2026-10-01', to: '2026-10-15' } } })
    // a booking covers its arrival night up to the night before departure
    expect(w.find('[data-testid=bar-101-2026-10-01]').exists()).toBe(false)
    expect(w.get('[data-testid=bar-101-2026-10-02]').attributes('href')).toBe('/reservations/9')
    expect(w.find('[data-testid=bar-101-2026-10-03]').exists()).toBe(true)
    expect(w.find('[data-testid=bar-101-2026-10-04]').exists()).toBe(false)
    expect(w.get('[data-testid=block-102-2026-10-05]').text()).toBe('OOS')
    expect(w.find('[data-testid=block-102-2026-10-07]').exists()).toBe(false)
    expect(w.get('[data-testid=count-DLX-2026-10-03]').text()).toBe('1')
    expect(w.find('[data-testid=count-DLX-2026-10-04]').exists()).toBe(false)
  })

  it('moves the window by a week', async () => {
    const w = mountView()
    await flushPromises()
    // the two buttons of the week have their arrows as icons, one to each side: ‹ Week and Week ›
    const [earlier, later] = w.findAll('button').filter((b) => b.text() === 'Week')
    expect(earlier?.find('svg.lucide-chevron-left').exists()).toBe(true)
    expect(later?.find('svg.lucide-chevron-right').exists()).toBe(true)
    expect(w.text()).not.toContain('→')
    await later?.trigger('click')
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1]).toMatchObject({ params: { query: { from: '2026-10-08', to: '2026-10-22' } } })
  })

  it('needs read permission', async () => {
    const w = mountView(['guest.read'])
    await flushPromises()
    expect(w.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })

  it('colours a checked-in stay differently, and shows a legend', async () => {
    const w = mountView()
    GET.mockResolvedValue({ data: { ...chart, rooms: [{ ...chart.rooms[0]!, bookings: [{ ...booking, status: 'CHECKED_IN' }] }, chart.rooms[1]] } })
    await w.findAll('button').find((b) => b.text().includes('Business date'))?.trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=bar-101-2026-10-02]').classes()).toContain('bg-status-inhouse') // a guest in the house: solid blue
    expect(w.get('[data-testid=legend]').text()).toContain('Checked in')
    expect(w.get('[data-testid=legend]').text()).toContain('Blocked (OOO / OOS)')
  })

  it('draws a booking to come lighter than a stay in the house, not only in another hue, and a block as the stripe', async () => {
    const w = mountView()
    await flushPromises()
    const confirmed = w.get('[data-testid=bar-101-2026-10-02]').classes()
    expect(confirmed).toEqual(expect.arrayContaining(['bg-status-booked-bg', 'text-status-booked', 'border-y']))
    expect(confirmed).not.toContain('bg-status-inhouse')
    expect(confirmed.some((c) => /(^|-)primary($|\/)|success/.test(c))).toBe(false) // no teal and no green: teal is for actions
    const block = w.get('[data-testid=block-102-2026-10-05]') // out of service here: the plain grey; out of order is the stripe (see the legend test)
    expect(block.classes()).toEqual(expect.arrayContaining(['bg-status-closed-bg', 'border-y']))
  })

  it('draws the legend from the same map as the bars', async () => {
    const w = mountView()
    await flushPromises()
    const legend = w.get('[data-testid=legend]')
    expect(legend.get('[data-legend=CONFIRMED] span').classes()).toEqual(expect.arrayContaining(['bg-status-booked-bg']))
    expect(legend.get('[data-legend=CHECKED_IN] span').classes()).toEqual(expect.arrayContaining(['bg-status-inhouse']))
    expect(legend.get('[data-legend=BLOCKED] span').classes()).toContain('status-hatch')
  })

  it('names the weekday over each date and shades the weekend', async () => {
    const w = mountView()
    await flushPromises()
    const heads = w.findAll('thead th').slice(1)
    expect(heads[0]!.text()).toContain('Thu') // 1 Oct 2026
    expect(heads[0]!.text()).toContain('01/10')
    expect(heads[0]!.classes()).toContain('bg-primary/10') // the business date
    expect(heads[2]!.text()).toContain('Sat')
    expect(heads[2]!.classes()).toContain('bg-muted/60')
    expect(heads[4]!.classes()).not.toContain('bg-muted/60') // Monday
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    const w = mountView()
    await flushPromises()
    expect(w.get('h1').text()).toBe('Bagan kamar')
    expect(w.get('[data-testid=unassigned-DLX]').text()).toContain('1 pemesanan')
    expect(w.get('table').text()).toContain('Belum ditetapkan')
    expect(w.get('select').findAll('option').map((o) => o.text())).toEqual(['14 hari', '28 hari'])
    setLocale('en')
  })
})
