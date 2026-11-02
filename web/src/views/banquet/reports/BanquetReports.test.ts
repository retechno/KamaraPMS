import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { useSubscriptionStore } from '@/stores/subscription'
import BanquetSettingsView from '../BanquetSettingsView.vue'
import BanquetForecastView from './BanquetForecastView.vue'
import BanquetGuestsReportView from './BanquetGuestsReportView.vue'
import BanquetLostReportView from './BanquetLostReportView.vue'
import BanquetReportsView from './BanquetReportsView.vue'
import BanquetRoomsReportView from './BanquetRoomsReportView.vue'
import BanquetSalesReportView from './BanquetSalesReportView.vue'
import BanquetScheduleView from './BanquetScheduleView.vue'

let GET = vi.fn()
let PUT = vi.fn()
const downloadCsv = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), PUT: (...a: unknown[]) => PUT(...a), POST: vi.fn(), PATCH: vi.fn(), DELETE: vi.fn() } }))
vi.mock('@/views/accounting/reportApi', () => ({ downloadCsv: (...a: unknown[]) => downloadCsv(...a), money: (v: string) => v }))

const mounted: VueWrapper[] = []
async function setup(component: unknown, permissions: string[], get: (path: string) => unknown, modules: string[] | null = null) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'm@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  usePropertyStore().clock = { business_date: '2026-11-10' } as never
  useSubscriptionStore().modules = modules
  GET = vi.fn(async (path: string) => get(path))
  PUT = vi.fn().mockResolvedValue({ data: {} })
  downloadCsv.mockReset()
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  await router.push('/')
  const w = mount(component as never, { attachTo: document.body, global: { plugins: [pinia, router] } })
  mounted.push(w)
  await flushPromises()
  await flushPromises()
  return w
}
const body = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)
const has = (sel: string) => document.body.querySelector(sel) !== null
const click = async (sel: string) => {
  ;(document.body.querySelector(sel) as HTMLElement).click()
  await flushPromises()
}

beforeEach(() => setLocale('en'))
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
})

const read = ['banquet.read']
const group = (key: string, name: string, over: Record<string, unknown> = {}) => ({ key, name, definite: '0', tentative: '0', definite_events: 0, tentative_events: 0, ...over })

describe('the index of the reports', () => {
  it('links the six reports and the report of the sessions not posted', async () => {
    await setup(BanquetReportsView, read, () => ({ data: {} }))
    const to: Record<string, string> = { forecast: '/banquet/reports/forecast', rooms: '/banquet/reports/rooms', guests: '/banquet/reports/guests', sales: '/banquet/reports/sales', lost: '/banquet/reports/lost', schedule: '/banquet/reports/schedule', unposted: '/banquet/unposted' }
    for (const [id, href] of Object.entries(to)) expect(body(`[data-testid=report-card-${id}]`).attributes('href')).toBe(href)
  })
})

describe('the forecast', () => {
  const forecast = (path: string) =>
    path.endsWith('/reports/forecast')
      ? {
          data: {
            from: '2026-11-01', to: '2027-04-30', with_tax: false, definite: '9000000', tentative: '4000000', tentative_overdue: 2, unposted: 3, unposted_oldest_days: 5, conversion_pct: '66.7',
            months: [group('2026-11', '', { definite: '9000000', tentative: '4000000', definite_events: 2, tentative_events: 1 })],
            by_type: [group('WED', 'Wedding', { definite: '9000000', definite_events: 2 })],
            by_sales: [group('5', 'Dewi', { definite: '9000000', tentative: '4000000', definite_events: 2, tentative_events: 1 })],
          },
        }
      : { data: {} }

  it('asks six months from the first of the month of the business date, and shows definite and tentative on rows of their own', async () => {
    await setup(BanquetForecastView, read, forecast)
    expect(GET).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/banquet/reports/forecast', { params: { path: { propertyId: 7 }, query: { from: '2026-11-01', to: '2027-04-30', with_tax: false } } })
    expect(body('[data-testid=forecast-months-2026-11-definite]').text()).toContain('9,000,000')
    // the month is written as a month, not as the ISO key (which stays the key of the row and of the CSV)
    expect(body('[data-testid=forecast-months-2026-11-definite]').text()).toContain('Nov 2026')
    expect(body('[data-testid=forecast-months-2026-11-definite]').text()).not.toContain('2026-11')
    expect(body('[data-testid=forecast-months-2026-11-tentative]').text()).toContain('4,000,000')
    expect(body('[data-testid=forecast-types-WED-definite]').text()).toContain('Wedding')
    expect(body('[data-testid=forecast-sales-5-definite]').text()).toContain('Dewi')
    expect(body('[data-testid=kpi-conversion]').text()).toContain('66.7')
    expect(body('[data-testid=kpi-overdue]').text()).toBe('2')
    expect(body('[data-testid=kpi-unposted] a').attributes('href')).toBe('/banquet/unposted')
  })

  it('asks again with service and tax when the toggle is on, and the file takes the same choice', async () => {
    await setup(BanquetForecastView, read, forecast)
    await body('[data-testid=forecast-tax]').setValue(true)
    await flushPromises()
    expect(GET).toHaveBeenLastCalledWith('/api/v1/properties/{propertyId}/banquet/reports/forecast', { params: { path: { propertyId: 7 }, query: { from: '2026-11-01', to: '2027-04-30', with_tax: true } } })
    await click('[data-testid=report-csv]')
    expect(downloadCsv).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/banquet/reports/forecast', { path: { propertyId: 7 }, query: { from: '2026-11-01', to: '2027-04-30', with_tax: true } }, 'banquet-forecast.csv')
  })

  it('says so when nothing is booked', async () => {
    await setup(BanquetForecastView, read, () => ({ data: { from: '2026-11-01', to: '2027-04-30', with_tax: false, definite: '0', tentative: '0', tentative_overdue: 0, unposted: 0, unposted_oldest_days: 0, conversion_pct: null, months: [], by_type: [], by_sales: [] } }))
    expect(has('[data-testid=forecast-empty]')).toBe(true)
    expect(body('[data-testid=kpi-conversion]').text()).toBe('–')
  })
})

describe('the other reports', () => {
  it('rooms: hours, occupancy and revenue of each room, and the total', async () => {
    await setup(BanquetRoomsReportView, read, () => ({
      data: {
        from: '2026-11-01', to: '2026-11-10', operating_from: '07:00:00', operating_to: '23:00:00', days: 10, revenue: '5000000',
        rooms: [
          { room_id: 1, code: 'SA', name: 'Saraswati A', is_combo: false, sessions: 3, hours: '12', locked_hours: '0', available_hours: '160', occupancy_pct: '7.5', posted_sessions: 2, revenue: '5000000', revenue_per_hour: '416666.667' },
          { room_id: 2, code: 'SAB', name: 'Saraswati A+B', is_combo: true, sessions: 1, hours: '4', locked_hours: '6.5', available_hours: '160', occupancy_pct: '2.5', posted_sessions: 0, revenue: '0', revenue_per_hour: null },
        ],
      },
    }))
    expect(body('[data-testid=room-SA]').text()).toContain('7.5')
    // the hours another room took are in a column of their own, and are not held hours or occupancy
    expect(body('[data-testid=room-SA-locked]').text()).toBe('–')
    expect(body('[data-testid=room-SAB-locked]').text()).toBe('6.5')
    expect(body('[data-testid=room-SAB]').text()).toContain('2.5')
    expect(body('[data-testid=rooms-table]').text()).toContain('Locked by another room')
    expect(body('[data-testid=room-SA]').text()).toContain('5,000,000')
    expect(body('[data-testid=rooms-total]').text()).toContain('5,000,000')
    expect(body('[data-testid=rooms-hours]').text()).toContain('10 days')
  })

  it('guests: the difference is signed and the totals count the sessions with both numbers', async () => {
    await setup(BanquetGuestsReportView, read, () => ({
      data: {
        from: '2026-11-01', to: '2026-11-10', guaranteed: 200, actual: 190, difference: -10, compared_count: 2, over: 1, under: 1,
        rows: [
          { session_id: 1, event_id: 11, event_number: 'BQ-1', event_name: 'Wedding', session_no: 1, session_name: 'Dinner', session_date: '2026-11-05', room_name: 'A', pax_estimated: 100, pax_guaranteed: 100, pax_actual: 90, difference: -10 },
          { session_id: 2, event_id: 12, event_number: 'BQ-2', event_name: 'Gala', session_no: 1, session_name: 'Dinner', session_date: '2026-11-06', room_name: null, pax_estimated: 100, pax_guaranteed: 100, pax_actual: 100, difference: 0 },
        ],
      },
    }))
    expect(body('[data-testid=guests-row-1]').text()).toContain('-10')
    expect(body('[data-testid=guests-total]').text()).toContain('Total of 2 sessions')
  })

  it('sales: the conversion of each person and of the team', async () => {
    const row = { sales_user_id: 5, sales_name: 'Dewi', created: 4, definite: 2, lost: 1, definite_value: '9000000', lost_value: '1000000', conversion_pct: '66.7' }
    await setup(BanquetSalesReportView, read, () => ({ data: { from: '2026-11-01', to: '2026-11-10', rows: [row], total: { ...row, sales_user_id: 0, sales_name: '' } } }))
    expect(body('[data-testid=sales-row-5]').text()).toContain('66.7')
    expect(body('[data-testid=sales-total]').text()).toContain('9,000,000')
  })

  it('lost: groups by reason, a lost event apart from a cancelled one', async () => {
    await setup(BanquetLostReportView, read, () => ({
      data: {
        from: '2026-11-01', to: '2026-11-10', lost: 1, cancelled: 1, value: '3000000',
        groups: [
          { status: 'LOST', reason_code: 'PRICE', reason_name: 'Price', events: 1, value: '1000000' },
          { status: 'CANCELLED', reason_code: 'ILL', reason_name: 'Illness', events: 1, value: '2000000' },
        ],
        rows: [{ event_id: 11, event_number: 'BQ-1', event_name: 'Wedding', status: 'LOST', sales_name: 'Dewi', value: '1000000', reason_code: 'PRICE', reason_name: 'Price', reason_note: 'Went elsewhere', closed_on: '2026-11-03' }],
      },
    }))
    expect(body('[data-testid=lost-group-LOST-PRICE]').text()).toContain('1,000,000')
    expect(body('[data-testid=lost-group-CANCELLED-ILL]').text()).toContain('Cancelled')
    expect(body('[data-testid=lost-row-11]').text()).toContain('Went elsewhere')
  })

  it('schedule: asks the day, prints and downloads for that day', async () => {
    await setup(BanquetScheduleView, read, () => ({
      data: {
        day: '2026-11-10',
        rows: [{ session_id: 1, event_id: 11, event_number: 'BQ-1', event_name: 'Wedding', status: 'DEFINITE', session_no: 1, session_name: 'Dinner', start_time: '18:00:00', end_time: '22:00:00', ends_next_day: false, room_name: 'A', setup_style: 'THEATER', pax_estimated: 100, pax_guaranteed: 90, pax_actual: null, contact_name: 'Ibu Sari', contact_phone: '0812', sales_name: 'Dewi', beo_revision: 2, beo_unread: 1 }],
      },
    }))
    expect(GET).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/banquet/reports/schedule', { params: { path: { propertyId: 7 }, query: { day: '2026-11-10' } } })
    const row = body('[data-testid=schedule-row-1]').text()
    expect(row).toContain('18:00:00 – 22:00:00')
    expect(row).toContain('Ibu Sari')
    expect(row).toContain('Rev 2, 1 unread')
    await click('[data-testid=report-csv]')
    expect(downloadCsv).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/banquet/reports/schedule', { path: { propertyId: 7 }, query: { day: '2026-11-10' } }, 'banquet-schedule-2026-11-10.csv')
  })

  it('schedule: says so when the day has no session', async () => {
    await setup(BanquetScheduleView, read, () => ({ data: { day: '2026-11-10', rows: [] } }))
    expect(has('[data-testid=schedule-empty]')).toBe(true)
  })
})

describe('the warehouse of the banquet', () => {
  const settings = (path: string, warehouse: number | null = 3) => {
    if (path.endsWith('/banquet/settings')) return { data: { tentative_conflict_mode: 'REJECT', operating_from: null, operating_to: null, posting_mode: 'AUTO', default_price_mode: 'EXCLUSIVE', max_discount_pct_no_approval: '0', warehouse_id: warehouse, warehouse_name: warehouse ? 'Main kitchen' : null } }
    if (path.endsWith('/inventory/warehouses')) return { data: { data: [{ id: 3, code: 'KIT', name: 'Main kitchen', is_active: true }, { id: 4, code: 'BAR', name: 'Bar store', is_active: true }] } }
    return { data: { data: [] } }
  }

  it('is not offered when the plan has no Inventory', async () => {
    await setup(BanquetSettingsView, ['banquet.setup'], settings, ['BANQUET'])
    expect(has('[data-testid=stock-section]')).toBe(false)
    expect(GET).not.toHaveBeenCalledWith('/api/v1/properties/{propertyId}/inventory/warehouses', expect.anything())
  })

  it('shows the warehouse that is set and saves another one on its own', async () => {
    await setup(BanquetSettingsView, ['banquet.setup', 'inventory.view'], settings, ['BANQUET', 'INVENTORY'])
    const select = body('[data-testid=banquet-warehouse]')
    expect((select.element as HTMLSelectElement).value).toBe('3')
    await select.setValue(4)
    await click('[data-testid=warehouse-save]')
    expect(PUT).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/banquet/settings', { params: { path: { propertyId: 7 } }, body: { tentative_conflict_mode: 'REJECT', warehouse_id: 4 } })
  })

  it('says that no stock moves when there is no warehouse', async () => {
    await setup(BanquetSettingsView, ['banquet.setup'], (p) => settings(p, null), ['BANQUET', 'INVENTORY'])
    expect(has('[data-testid=warehouse-off]')).toBe(true)
  })
})

describe('the schedule report of the banquet, what is seen is what is sent', () => {
  it('does not load while the day is half typed, and says why', async () => {
    const w = await setup(BanquetScheduleView, ['banquet.read'], () => ({ data: { day: '2026-11-10', rows: [] } }))
    const before = GET.mock.calls.length
    await w.get('[data-testid=report-period] input[data-slot=date-input-text]').setValue('3/2')
    await w.get('[data-testid=report-period]').trigger('submit')
    await flushPromises()
    expect(GET.mock.calls.length).toBe(before)
    expect(w.text()).toContain('Write as dd/mm/yyyy')
  })
})
