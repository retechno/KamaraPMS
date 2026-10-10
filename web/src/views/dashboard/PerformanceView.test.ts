import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { setLocale } from '@/i18n'
import PerformanceView from './PerformanceView.vue'

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
  return mount(PerformanceView, { global: { plugins: [pinia], stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
}

describe('PerformanceView', () => {
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
    expect(w.get('[data-testid=kpi-inhouse]').text()).toContain('owe 1,200,000') // in full: only the KPI figures are abbreviated
    expect(w.get('[data-testid=kpi-city-ledger]').text()).toContain('IDR 2.5M')
    expect(w.get('[data-testid=kpi-adr]').text()).toContain('IDR 1M')
    expect(w.get('[data-testid=kpi-revpar]').text()).toContain('IDR 667K')
    expect(w.get('[data-testid=kpi-revenue]').text()).toContain('IDR 6M')
    expect(w.get('[data-testid=housekeeping]').text()).toContain('Dirty')
    expect(w.get('[data-testid=payments]').text()).toContain('2,900,000')
    expect(w.get('[data-testid=payments]').text()).toContain('Cash') // the method in words, not CASH
    expect(w.get('[data-testid=payments]').text()).not.toContain('CASH')
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

  it('writes the month with separators and the percent with the decimal separator of the language', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=month-adr]').text()).toContain('1,000,000')
    expect(w.get('[data-testid=month-adr]').text()).toContain('800,000')
    expect(w.get('[data-testid=month-occupancy]').text()).toContain('60.00%')
    expect(w.get('[data-testid=month-occupancy]').text()).toContain('40.00%')
  })

  it('shows a dash for last month when it has no closed day, and titles the chart with the days it spans', async () => {
    const d = dashboard()
    GET.mockResolvedValue({ data: { ...d, previous_month: totals({ days: 0, adr: '0', occupancy_percent: '0.00', revpar: '0', room_revenue: '0' }) } })
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=month-adr]').text()).toContain('1,000,000')
    for (const k of ['occupancy', 'adr', 'revpar', 'revenue']) expect(w.get(`[data-testid=month-${k}] td:nth-child(3)`).text()).toBe('–')
    expect(w.get('[data-testid=trend]').text()).toContain('Occupancy, last 7 days')
  })

  it('does not call ADR, RevPAR and the revenue zero before night audit has posted the room charges', async () => {
    const d = dashboard()
    GET.mockResolvedValue({ data: { ...d, today: { ...d.today, rooms: { ...d.today.rooms, occupied: 6, sold: 0 }, adr: '0', revpar: '0', room_revenue: { net: '0', service: '0', tax: '0' } } } })
    const w = mountView()
    await flushPromises()
    for (const k of ['adr', 'revpar', 'revenue']) {
      expect(w.get(`[data-testid=kpi-${k}]`).text()).toContain('Available after night audit')
      expect(w.get(`[data-testid=kpi-${k}] [data-slot=kpi-value]`).text()).toBe('–')
    }
    expect(w.get('[data-testid=kpi-occupancy]').text()).toContain('66.67%') // occupancy is known all day
  })

  it('pads the trend to a week, with empty bars for the days before the first closed one', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=trend]').findAll('[data-testid=trend-day]')).toHaveLength(2)
    expect(w.get('[data-testid=trend]').findAll('[data-testid=trend-day-empty]')).toHaveLength(5)
    expect(w.get('[data-testid=trend]').text()).toContain('4 Oct') // the label is the day and month, not "10-04"
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
    // the person is told why, and is not left with an empty page or an error of the API
    expect(w.findAll('[data-slot=page-header]')).toHaveLength(1)
    expect(w.get('[data-testid=no-access]').text()).toContain('The Performance page is for managers')
    expect(w.get('[data-testid=no-access]').text()).toContain('View reports')
    expect(w.find('[data-testid=no-access] svg').exists()).toBe(true) // the lock
    expect(w.get('[data-testid=no-access] a[data-testid=empty-action]').text()).toBe('Back to Today')
    expect(w.get('[data-testid=no-access] a[data-testid=empty-action]').attributes('to')).toBe('/')
    expect(w.find('[data-testid=period]').exists()).toBe(false) // no controls for a page that is not there
  })

  it('says it in Indonesian too', async () => {
    setLocale('id')
    const w = mountView(['folio.read'])
    await flushPromises()
    expect(w.get('[data-testid=no-access]').text()).toContain('Halaman Kinerja khusus manajer')
    expect(w.get('[data-testid=no-access] a[data-testid=empty-action]').text()).toBe('Kembali ke Hari Ini')
    setLocale('en')
  })

  it('has a header with the badge for managers, the period, when it was updated and the link to the full report', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.findAll('[data-slot=page-header]')).toHaveLength(1)
    expect(w.get('h1').text()).toBe('Property performance')
    expect(w.get('[data-testid=managers-only]').text()).toContain('report.view')
    expect(w.get('[data-testid=updated]').text()).toMatch(/^Updated \d{2}:\d{2}$/)
    expect(w.get('[data-testid=full-report]').attributes('to')).toBe('/reports')
  })

  it('shows the four figures of today, each with its change against last month and the line of the closed days', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=main-kpis]').findAll('[data-slot=kpi-card]')).toHaveLength(4)
    expect(w.get('[data-testid=delta-adr]').text()).toContain('▲ 25.0%') // 1,000,000 against 800,000
    expect(w.get('[data-testid=delta-adr]').text()).toContain('vs last month')
    expect(w.get('[data-testid=delta-occupancy]').text()).toContain('▲') // 66.67 against 40
    expect(w.find('[data-testid=spark-occupancy]').exists()).toBe(true)
    expect(w.get('[data-testid=spark-occupancy] polyline').attributes('points')).toBeTruthy()
  })

  it('has no change to show when last month has no closed day, and no line from fewer than two days', async () => {
    const d = dashboard()
    GET.mockResolvedValue({ data: { ...d, previous_month: totals({ days: 0 }), trend: [d.trend[0]] } })
    const w = mountView()
    await flushPromises()
    expect(w.find('[data-testid=delta-adr]').exists()).toBe(false)
    expect(w.find('[data-testid=spark-adr]').exists()).toBe(false)
  })

  it('switches to the month so far: its figures and the change of the month against the month before', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=period-month]').trigger('click')
    expect(w.get('[data-testid=period-month]').attributes('aria-pressed')).toBe('true')
    expect(w.get('[data-testid=kpi-occupancy]').text()).toContain('60.00%')
    expect(w.get('[data-testid=kpi-occupancy]').text()).toContain('10 closed days')
    expect(w.get('[data-testid=kpi-adr]').text()).toContain('IDR 1M')
    expect(w.get('[data-testid=delta-adr]').text()).toContain('▲ 25.0%')
    await w.get('[data-testid=period-today]').trigger('click')
    expect(w.get('[data-testid=kpi-occupancy]').text()).toContain('66.67%')
    expect(GET).toHaveBeenCalledTimes(1) // the period is a view of what was read
  })

  it('says what the month has not got yet instead of a zero', async () => {
    GET.mockResolvedValue({ data: dashboard({ month_to_date: totals({ days: 0 }) }) })
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=period-month]').trigger('click')
    expect(w.get('[data-testid=kpi-adr] [data-slot=kpi-value]').text()).toBe('–')
    expect(w.get('[data-testid=kpi-adr]').text()).toContain('No day of this month is closed yet.')
    expect(w.find('[data-testid=delta-adr]').exists()).toBe(false)
  })

  it('writes the average and the highest day above the closed days', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=trend-summary]').text()).toBe('Average 65.00% · highest 80.00% on 10 Oct')
  })

  it('draws the nights ahead with the lines at 50% and 100%, and the nights at 90% or more in the strong colour', async () => {
    const d = dashboard()
    GET.mockResolvedValue({ data: { ...d, forecast: [...d.forecast, { date: '2026-10-13', rooms_sellable: 10, rooms_booked: 10, occupancy_percent: '100.00' }, { date: '2026-10-14', rooms_sellable: 10, rooms_booked: 9, occupancy_percent: '90.00' }] } })
    const w = mountView()
    await flushPromises()
    const chart = w.get('[data-testid=forecast]')
    expect(chart.find('[data-slot=line-100]').exists()).toBe(true)
    expect(chart.find('[data-slot=line-50]').exists()).toBe(true)
    expect(chart.findAll('[data-testid=forecast-bar]').map((b) => b.attributes('data-strong'))).toEqual(['false', 'false', 'true', 'true'])
  })
})
