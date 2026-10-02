import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ManagerDashboard from './ManagerDashboard.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...args: unknown[]) => GET(...args) } }))

const totals = (over: object = {}) => ({
  days: 10, available_room_nights: 100, occupied_room_nights: 60, room_nights_sold: 60, room_revenue: '60000000', occupancy_percent: '60.00', adr: '1000000', revpar: '600000', ...over,
})

const dashboard = (over: object = {}) => ({
  business_date: '2026-10-11',
  today: {
    rooms: { total: 10, out_of_order: 1, out_of_service: 0, sellable: 9, occupied: 6, sold: 6 }, occupancy_percent: '66.67', adr: '1000000', revpar: '666667',
    room_revenue: { net: '6000000', service: '0', tax: '0' }, city_ledger: { transferred: '0', received: '0', outstanding: '2500000' },
    payments_by_method: [{ method: 'CASH', payments: '3000000', refunds: '100000', net: '2900000' }],
  },
  movements: { arrivals_expected: 2, arrivals_checked_in: 1, departures_expected: 3, departures_checked_out: 4, in_house: 6, in_house_balance: '1200000' },
  rooms: { clean: 3, dirty: 2, cleaning: 1, inspected: 3 },
  trend: [
    { business_date: '2026-10-09', occupancy_percent: '50.00', adr: '1', revpar: '1' },
    { business_date: '2026-10-10', occupancy_percent: '80.00', adr: '1', revpar: '1' },
  ],
  month_to_date: totals(), previous_month: totals({ adr: '800000', occupancy_percent: '40.00' }),
  forecast: [
    { date: '2026-10-11', rooms_sellable: 9, rooms_booked: 6, occupancy_percent: '66.67' },
    { date: '2026-10-12', rooms_sellable: 9, rooms_booked: 3, occupancy_percent: '33.33' },
  ],
  ...over,
})

function mountView(permissions = ['report.view']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'm@hotel.com', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  return mount(ManagerDashboard, { global: { plugins: [pinia], stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
}

describe('ManagerDashboard', () => {
  beforeEach(() => {
    GET = vi.fn().mockResolvedValue({ data: dashboard() })
  })

  it('shows the figures of the open day and the work left', async () => {
    const w = mountView()
    await flushPromises()
    expect(GET).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/dashboard', { params: { path: { propertyId: 7 } } })
    expect(w.get('[data-testid=kpi-occupancy]').text()).toContain('66.67%')
    expect(w.get('[data-testid=kpi-occupancy]').text()).toContain('6 of 9 rooms')
    expect(w.get('[data-testid=kpi-arrivals]').text()).toContain('2')
    expect(w.get('[data-testid=kpi-departures]').text()).toContain('3')
    expect(w.get('[data-testid=kpi-inhouse]').text()).toContain('owe 1200000')
    expect(w.get('[data-testid=kpi-city-ledger]').text()).toContain('2500000')
    expect(w.get('[data-testid=housekeeping]').text()).toContain('Dirty')
    expect(w.get('[data-testid=payments]').text()).toContain('2900000')
  })

  it('compares the month with the one before and draws the days', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=month-adr]').text()).toContain('▲ 25.0%')
    expect(w.get('[data-testid=month-occupancy]').text()).toContain('▲ 50.0%')
    expect(w.get('[data-testid=trend]').findAll('[data-testid=trend-day]')).toHaveLength(2)
    expect(w.get('[data-testid=forecast]').findAll('[data-testid=forecast-day]')).toHaveLength(2)
    expect(w.get('[data-testid=trend-bar]').attributes('style')).toContain('height: 50%')
  })

  it('says so when nothing is closed yet', async () => {
    GET.mockResolvedValue({ data: dashboard({ trend: [], month_to_date: totals({ days: 0 }) }) })
    const w = mountView()
    await flushPromises()
    expect(w.find('[data-testid=trend-empty]').exists()).toBe(true)
    expect(w.find('[data-testid=month-empty]').exists()).toBe(true)
  })

  it('shows an error and offers a refresh', async () => {
    GET.mockRejectedValueOnce(new ApiError({ type: 't', title: 'x', status: 500, code: 'INTERNAL', detail: 'boom' }))
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=dash-error]').text()).toContain('INTERNAL')
    await w.get('[data-testid=dash-refresh]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid=dash-error]').exists()).toBe(false)
    expect(GET).toHaveBeenCalledTimes(2)
  })

  it('stays out of the way without report.view', async () => {
    const w = mountView(['folio.read'])
    await flushPromises()
    expect(GET).not.toHaveBeenCalled()
    expect(w.find('[data-testid=manager-dashboard]').exists()).toBe(false)
  })
})
