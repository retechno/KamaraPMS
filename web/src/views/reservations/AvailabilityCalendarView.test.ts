import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import AvailabilityCalendarView from './AvailabilityCalendarView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const night = (date: string, sellable: number, held: number, blocked = 0) => ({
  date, sellable, held, blocked, available: sellable - held, occupancy_percent: sellable ? String(Math.round((held / sellable) * 100)) : '0',
  in_house: 0, arrivals: 0, reservations: held,
})
const calendar = {
  from: '2026-10-01', to: '2026-10-15',
  room_types: [
    { room_type_id: 10, code: 'DLX', name: 'Deluxe', rooms_total: 2, nights: [night('2026-10-01', 2, 1), night('2026-10-02', 2, 2), night('2026-10-03', 1, 2, 1)] },
  ],
  totals: [night('2026-10-01', 2, 1), night('2026-10-02', 2, 2), night('2026-10-03', 1, 2, 1)],
}

function mountView(permissions = ['reservation.read'], data: unknown = calendar) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-10-01' } as never
  GET = vi.fn().mockResolvedValue({ data })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(AvailabilityCalendarView, { global: { plugins: [pinia, router] } })
}

describe('AvailabilityCalendarView', () => {
  beforeEach(() => {
    GET = vi.fn()
    document.body.innerHTML = ''
    try {
      localStorage.clear()
    } catch {
      // not available
    }
  })

  it('loads 14 nights from the business date and shows what is left per room type and night', async () => {
    const w = mountView()
    await flushPromises()
    expect(GET.mock.calls[0]?.[1]).toMatchObject({ params: { path: { propertyId: 7 }, query: { from: '2026-10-01', to: '2026-10-15' } } })
    expect(w.get('[data-testid=cell-DLX-2026-10-01]').text()).toBe('1')
    expect(w.get('[data-testid=cell-DLX-2026-10-02]').text()).toBe('0') // sold out
    expect(w.get('[data-testid=cell-DLX-2026-10-02]').classes().join(' ')).toContain('bg-warning')
    expect(w.get('[data-testid=cell-DLX-2026-10-03]').text()).toBe('-1') // oversold
    expect(w.get('[data-testid=cell-DLX-2026-10-03]').classes().join(' ')).toContain('text-destructive')
    expect(w.get('[data-testid=cell-DLX-2026-10-01]').attributes('title')).toContain('1 held of 2 sellable')
    expect(w.get('[data-testid=totals-2026-10-01]').text()).toContain('1')
    expect(w.get('[data-testid=occupancy-2026-10-01]').text()).toBe('50%')
  })

  it('shows a row per bed type under each room type when asked', async () => {
    const withBeds = { ...calendar, room_types: [{ ...calendar.room_types[0], beds: [
      { bed_type_id: 3, code: 'KING', name: 'King', rooms_total: 1, nights: [night('2026-10-01', 1, 1), night('2026-10-02', 1, 0), night('2026-10-03', 1, 0)] },
      { bed_type_id: 4, code: 'TWIN', name: 'Twin', rooms_total: 1, nights: [night('2026-10-01', 1, 0), night('2026-10-02', 1, 0), night('2026-10-03', 0, 0, 1)] },
    ] }] }
    const w = mountView(['reservation.read'], withBeds)
    await flushPromises()
    expect(GET.mock.calls[0]?.[1].params.query.by_bed).toBeUndefined()
    expect(w.find('[data-testid=bed-note]').exists()).toBe(false)
    await w.get('input[name=by_bed]').setValue(true)
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1]).toMatchObject({ params: { query: { from: '2026-10-01', by_bed: true } } })
    expect(w.find('[data-testid=bed-note]').exists()).toBe(true)
    expect(w.get('[data-testid=row-DLX-KING]').text()).toContain('King')
    expect(w.get('[data-testid=cell-DLX-KING-2026-10-01]').text()).toBe('0')
    expect(w.get('[data-testid=cell-DLX-TWIN-2026-10-01]').text()).toBe('1')
    // the room type row and the totals are unchanged
    expect(w.get('[data-testid=cell-DLX-2026-10-01]').text()).toBe('1')
  })

  it('shows several metrics, chosen in a popover, as one row each under the room type', async () => {
    const w = mountView(['reservation.read'], { ...calendar, room_types: [{ ...calendar.room_types[0], nights: [{ ...night('2026-10-01', 2, 1), in_house: 1, arrivals: 2, reservations: 0 }, night('2026-10-02', 2, 2), night('2026-10-03', 1, 2, 1)] }] })
    await flushPromises()
    await w.get('[data-testid=metrics-trigger]').trigger('click')
    await flushPromises()
    const box = (name: string) => document.body.querySelector(`input[name=${name}]`) as HTMLInputElement
    box('metric_in_house').click()
    box('metric_arrivals').click()
    await flushPromises()
    expect(w.find('[data-testid=row-DLX-available]').exists()).toBe(true)
    expect(w.get('[data-testid=cell-DLX-in_house-2026-10-01]').text()).toBe('1')
    expect(w.get('[data-testid=cell-DLX-arrivals-2026-10-01]').text()).toBe('2')
    expect(w.find('[data-testid=row-DLX-held]').exists()).toBe(false)
    expect(localStorage.getItem('availability-calendar-metrics')).toBe('["available","in_house","arrivals"]')
    // the last metric cannot be switched off
    box('metric_available').click()
    box('metric_in_house').click()
    box('metric_arrivals').click()
    await flushPromises()
    // arrivals is the only one left: back to the compact view, with the arrivals in the type row
    expect(w.get('[data-testid=cell-DLX-2026-10-01]').text()).toBe('2')
  })

  it('moves the window by a week', async () => {
    const w = mountView()
    await flushPromises()
    await w.findAll('button').find((b) => b.text().includes('Week →'))?.trigger('click')
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1]).toMatchObject({ params: { query: { from: '2026-10-08', to: '2026-10-22' } } })
  })

  it('does not call the API without reservation.read', async () => {
    const w = mountView(['guest.read'])
    await flushPromises()
    expect(GET).not.toHaveBeenCalled()
    expect(w.find('[data-testid=no-access]').exists()).toBe(true)
  })

  it('says so when there are no room types', async () => {
    const w = mountView(['reservation.read'], { ...calendar, room_types: [], totals: [] })
    await flushPromises()
    expect(w.find('[data-testid=empty]').exists()).toBe(true)
  })
})
