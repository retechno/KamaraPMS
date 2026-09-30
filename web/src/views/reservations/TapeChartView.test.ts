import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
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
    await w.findAll('button').find((b) => b.text().includes('Week →'))?.trigger('click')
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1]).toMatchObject({ params: { query: { from: '2026-10-08', to: '2026-10-22' } } })
  })

  it('needs read permission', async () => {
    const w = mountView(['guest.read'])
    await flushPromises()
    expect(w.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })
})
