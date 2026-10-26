import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import CostLineChart from '@/components/inventory/CostLineChart.vue'
import OutletCostTargets from '@/components/inventory/OutletCostTargets.vue'
import { resetDepartments } from '@/composables/useDepartments'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import InventoryHomeView from '../InventoryHomeView.vue'
import DailyCostView from './DailyCostView.vue'
import GrniView from './GrniView.vue'
import ReconciliationView from './ReconciliationView.vue'
import StockValueView from './StockValueView.vue'

let GET = vi.fn()
let PUT = vi.fn()
let DELETE = vi.fn()
const downloadCsv = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), PUT: (...a: unknown[]) => PUT(...a), DELETE: (...a: unknown[]) => DELETE(...a) } }))
vi.mock('@/views/accounting/reportApi', () => ({ downloadCsv: (...a: unknown[]) => downloadCsv(...a), money: (v: string) => v }))

const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
const mounted: VueWrapper[] = []

async function setup(component: unknown, permissions: string[], get: (path: string, init?: unknown) => unknown, props: Record<string, unknown> = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'm@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  usePropertyStore().clock = { business_date: '2026-09-30' } as never
  resetDepartments()
  GET = vi.fn(async (path: string, init?: unknown) => get(path, init))
  PUT = vi.fn().mockResolvedValue({ data: {} })
  DELETE = vi.fn().mockResolvedValue({ data: {} })
  downloadCsv.mockReset()
  await router.push('/')
  const w = mount(component as never, { attachTo: document.body, props: props as never, global: { plugins: [pinia, router] } })
  mounted.push(w)
  await flushPromises()
  await flushPromises()
  return w
}
const body = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)
const has = (sel: string) => document.body.querySelector(sel) !== null
const all = (sel: string) => document.body.querySelectorAll(sel).length
const click = async (sel: string) => {
  ;(document.body.querySelector(sel) as HTMLElement).click()
  await flushPromises()
}
async function type(sel: string, value: string) {
  const el = document.body.querySelector(sel) as HTMLInputElement
  el.value = value
  el.dispatchEvent(new Event('input', { bubbles: true }))
  await flushPromises()
}

beforeEach(() => setLocale('en'))
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
})

const part = (revenue: string, cost: string, pct: string | null, target: string | null, above = false, mtd: string | null = pct) => ({ revenue, cost, pct, target_pct: target, above_target: above, mtd_pct: mtd, no_recipe_days: 0, no_recipe_data: false })
const day = (date: string, over: Record<string, unknown> = {}) => ({
  date, live: false, kinds: { SALE_COST_FOOD: '300000', SALE_COST_BEVERAGE: '50000', WASTE: '20000', STAFF_MEAL: '10000', ENTERTAINMENT: '5000', AMENITY: '40000', ENGINEERING: '0', OTHER_USE: '15000', COUNT_VARIANCE: '0', PRICE_VARIANCE: '-2000' },
  total: '438000', food: part('1000000', '300000', '30', '32'), beverage: part('250000', '50000', '20', '22'), fb: part('1250000', '350000', '28', null), banquet: part('0', '0', null, null), occupied_rooms: 20, amenity_cost_per_room: '2000',
  departments: { '0': '2000', '3': '436000' }, ...over,
})
const dailyCost = () => ({
  from: '2026-09-28', to: '2026-09-30', business_date: '2026-09-30', mtd_from: '2026-09-01', targets: [],
  days: [
    day('2026-09-28'),
    day('2026-09-29', { food: part('1000000', '350000', '35', '32', true, '32.5') }),
    day('2026-09-30', { live: true, food: part('800000', '240000', '30', '32') }),
  ],
  period: day('2026-09-28', { total: '1314000' }), mtd: day('2026-09-30', { total: '9000000', food: part('9000000', '2700000', '30', '32') }),
  departments: [{ id: 3, code: 'FB', name: 'Food and beverage' }],
})

describe('the chart of the cost percentage', () => {
  const points = [
    { key: 'a', label: '1', value: 30, title: '1 Sep: 30%', above: false },
    { key: 'b', label: '2', value: 36, title: '2 Sep: 36%, above target', above: true },
    { key: 'c', label: '3', value: null, title: '3 Sep: no revenue', above: false },
    { key: 'd', label: '4', value: 31, title: '4 Sep: 31%', above: false },
  ]
  it('draws the line in runs, the target as a dashed line, a square for a day above the target, and names all of it in a legend', async () => {
    const w = mount(CostLineChart, { attachTo: document.body, props: { points, target: 32, seriesLabel: 'Food cost %' } })
    mounted.push(w)
    expect(all('[data-testid=cost-line]')).toBe(2) // the day with no revenue breaks the line
    expect(has('[data-testid=cost-target]')).toBe(true)
    expect(all('[data-testid=cost-above]')).toBe(1)
    const legend = body('[data-testid=cost-legend]').text()
    expect(legend).toContain('Food cost %')
    expect(legend).toContain('Target 32%')
    expect(legend).toContain('Above target')
    expect(document.body.querySelector('polyline')!.getAttribute('stroke-width')).toBe('2')
  })

  it('draws a day without recipe data as a cross on the axis, with no point and no line, and says so in the legend', async () => {
    const withGap = [
      { key: 'a', label: '1', value: 30, title: '1 Sep: 30%', above: false },
      { key: 'b', label: '2', value: null, title: '2 Sep: - (no recipe data)', above: false, noData: true },
      { key: 'c', label: '3', value: 31, title: '3 Sep: 31%', above: false },
    ]
    const w = mount(CostLineChart, { attachTo: document.body, props: { points: withGap, target: 32, seriesLabel: 'Food cost %' } })
    mounted.push(w)
    expect(all('[data-testid=cost-nodata]')).toBe(1)
    expect(all('[data-testid=cost-line]')).toBe(2)
    expect(all('circle')).toBe(2)
    expect(body('[data-testid=cost-nodata-legend]').text()).toContain('Day without recipe data')
  })

  it('shows the figures of a day in a tooltip on hover and on focus', async () => {
    const w = mount(CostLineChart, { attachTo: document.body, props: { points, target: 32, seriesLabel: 'Food cost %' } })
    mounted.push(w)
    expect(has('[data-testid=cost-tip]')).toBe(false)
    const hits = document.body.querySelectorAll('[data-testid=cost-hit]')
    hits[1]!.dispatchEvent(new Event('mouseenter'))
    await flushPromises()
    expect(body('[data-testid=cost-tip]').text()).toBe('2 Sep: 36%, above target')
    hits[0]!.dispatchEvent(new Event('focus'))
    await flushPromises()
    expect(body('[data-testid=cost-tip]').text()).toBe('1 Sep: 30%')
  })
})

describe('the daily cost', () => {
  const api = (path: string) => (path.endsWith('/daily-cost') ? { data: dailyCost() } : { data: {} })

  it('shows every cost of each day with the total, marks the running day, and ends with the period and the month to date', async () => {
    await setup(DailyCostView, ['inventory.view'], api)
    expect(GET.mock.calls[0]![1].params.query).toEqual({ from: '2026-09-01', to: '2026-09-30' })
    const row = body('[data-testid=daily-2026-09-28]').text()
    expect(row).toContain('350,000') // the cost of food and beverage sales: 300,000 + 50,000
    expect(row).toContain('15,000') // staff meals 10,000 and entertainment 5,000
    expect(row).toContain('28%')
    expect(row).toContain('438,000')
    expect(body('[data-testid=daily-2026-09-30]').text()).toContain('running')
    expect(body('[data-testid=daily-2026-09-28]').text()).not.toContain('running')
    expect(has('[data-testid=daily-period]')).toBe(true)
    expect(body('[data-testid=daily-mtd]').text()).toContain('9,000,000')
    expect(body('[data-testid=kpi-food]').text()).toContain('30%')
    expect(body('[data-testid=kpi-food]').text()).toContain('2.0 points below')
  })

  it('shows a day with revenue and no cost from recipes as a dash with a note, not as 0%, and counts the days left out of the month to date', async () => {
    const gap = { ...part('1385000', '0', null, '32'), no_recipe_days: 1, no_recipe_data: true }
    const data = dailyCost()
    data.days[1] = day('2026-09-29', { food: gap, fb: { ...part('1635000', '50000', '20', null), no_recipe_days: 1 } })
    data.mtd = day('2026-09-30', { total: '9000000', food: { ...part('9000000', '2700000', '30', '32'), no_recipe_days: 1 } })
    await setup(DailyCostView, ['inventory.view'], (path) => (path.endsWith('/daily-cost') ? { data } : { data: {} }))
    expect(body('[data-testid=daily-2026-09-29]').text()).toContain('no recipe data')
    expect(body('[data-testid=kpi-food]').text()).toContain('1 day(s) without recipe data not counted')
    await click('[data-testid=tab-FB]')
    const row = body('[data-testid=fb-2026-09-29]').text()
    expect(row).toContain('1,385,000')
    expect(row).toContain('no recipe data')
    expect(row).toContain('1,385,000––no recipe data') // the cost and the percentage are dashes, not 0%
    expect(has('[data-testid=cost-nodata]')).toBe(true)
    expect(has('[data-testid=cost-nodata-legend]')).toBe(true)
  })

  it('has three views over the same rows: all costs, food and beverage only with the chart and the days above the target, and by department', async () => {
    await setup(DailyCostView, ['inventory.view'], api)
    expect(has('[data-testid=daily-all]')).toBe(true)
    await click('[data-testid=tab-FB]')
    expect(has('[data-testid=daily-fb]')).toBe(true)
    expect(has('[data-testid=cost-chart]')).toBe(true)
    expect(body('[data-testid=above-count]').text()).toContain('1 days above target')
    expect(body('[data-testid=above-food-2026-09-29]').text()).toContain('▲') // a symbol and not only a colour
    expect(has('[data-testid=above-food-2026-09-28]')).toBe(false)
    expect(body('[data-testid=fb-2026-09-29]').text()).toContain('35%')
    expect(all('[data-testid=cost-above]')).toBe(1)
    await click('[data-testid=chart-BEVERAGE]')
    expect(body('[data-testid=cost-legend]').text()).toContain('Beverage cost %')
    await click('[data-testid=tab-DEPT]')
    expect(body('[data-testid=daily-dept]').text()).toContain('Food and beverage')
    expect(body('[data-testid=dept-2026-09-28]').text()).toContain('436,000')
    expect(body('[data-testid=dept-2026-09-28]').text()).toContain('438,000')
  })

  it('downloads the same period as a CSV file and reloads with another period', async () => {
    await setup(DailyCostView, ['inventory.view'], api)
    await type('[name=from]', '2026-09-10')
    await click('[data-testid=report-show]')
    expect(GET.mock.calls.at(-1)![1].params.query).toEqual({ from: '2026-09-10', to: '2026-09-30' })
    await click('[data-testid=report-csv]')
    expect(downloadCsv).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/inventory/reports/daily-cost', { path: { propertyId: 7 }, query: { from: '2026-09-10', to: '2026-09-30' } }, 'daily-cost.csv')
  })
})

describe('the other reports', () => {
  const column = (over: Record<string, unknown> = {}) => ({
    opening: '1000000', opening_posted: '0', purchases: '500000', transfer_in: '20000', transfer_out: '10000', closing: '1100000', goods_used: '410000', staff_meal: '10000', entertainment: '0', net_cost: '400000', revenue: '1250000',
    actual_pct: '32', theory: '350000', theory_pct: '28', difference: '50000', difference_points: '4', waste: '20000', count_variance: '30000', no_recipe_estimate: '10000', other_outflows: '0', target_pct: '30', ...over,
  })
  const gl = (status: string, diff = '0') => ({ status, gl_through: '2026-09-29', lines: [{ key: 'INV_COGS_FOOD', stock: '350000', gl: '350000', difference: '0' }, { key: 'INV_WASTE', stock: '20000', gl: String(20000 - Number(diff)), difference: diff }], stock_total: '370000', gl_total: '370000', difference: diff })

  it('reconciles the food and beverage cost line by line and says on what the closing stock rests', async () => {
    await setup(ReconciliationView, ['inventory.view'], () => ({ data: { from: '2026-09-01', to: '2026-09-30', food: column(), beverage: column({ goods_used: '90000' }), total: column(), closing_basis: 'BOOK', gl: gl('MATCH') } }))
    expect(body('[data-testid=closing-basis]').text()).toBe('Estimated from the book stock')
    expect(body('[data-testid=recon-closing]').text()).toContain('estimated from the book stock')
    expect(body('[data-testid=recon-goodsUsed-food]').text()).toBe('410,000')
    expect(body('[data-testid=recon-goodsUsed-beverage]').text()).toBe('90,000')
    expect(body('[data-testid=recon-difference-food]').text()).toContain('+4%')
    expect(body('[data-testid=gl-status]').text()).toContain('Match')
    expect(body('[data-testid=gl-INV_WASTE]').text()).toContain('Waste')
  })

  it('puts the opening stock posted in the period on its own line, under the opening stock', async () => {
    await setup(ReconciliationView, ['inventory.view'], () => ({ data: { from: '2026-09-01', to: '2026-09-30', food: column({ opening_posted: '10507131' }), beverage: column(), total: column({ opening_posted: '10507131' }), closing_basis: 'BOOK', gl: gl('MATCH') } }))
    expect(body('[data-testid=recon-openingPosted-food]').text()).toBe('10,507,131')
    expect(body('[data-testid=recon-openingPosted]').text()).toContain('Opening stock posted in the period (OPN)')
  })

  it('shows a difference with the general ledger in words and in a symbol, and a period that is not journaled yet as such', async () => {
    await setup(ReconciliationView, ['inventory.view'], () => ({ data: { from: '2026-09-01', to: '2026-09-30', food: column(), beverage: column(), total: column(), closing_basis: 'COUNT', gl: gl('DIFFERENCE', '1500') } }))
    expect(body('[data-testid=gl-status]').text()).toContain('✕')
    expect(body('[data-testid=gl-status]').text()).toContain('Difference')
    expect(body('[data-testid=gl-INV_WASTE]').text()).toContain('1,500')
    await setup(ReconciliationView, ['inventory.view'], () => ({ data: { from: '2026-09-01', to: '2026-09-30', food: column(), beverage: column(), total: column(), closing_basis: 'COUNT', gl: { status: 'PENDING', gl_through: null, lines: [], stock_total: '0', gl_total: '0', difference: '0' } } }))
    expect(has('[data-testid=gl-pending]')).toBe(true)
  })

  it('shows the value of the stock by warehouse and beside the accounts, with the status', async () => {
    const report = {
      as_of: '2026-09-30', live: true, gl_through: '2026-09-29', status: 'DIFFERENCE', total: '3000000',
      warehouses: [{ warehouse_id: 1, code: 'MAIN', name: 'Main store', total: '3000000', categories: [{ category_id: 1, category_code: 'FOOD', category_name: 'Food', account_id: 9, items: 12, value: '3000000' }] }],
      categories: [{ category_id: 1, category_code: 'FOOD', category_name: 'Food', account_id: 9, items: 12, value: '3000000' }],
      accounts: [{ account_id: 9, code: '1310', name: 'Inventory food', stock: '3000000', gl: '2900000', difference: '100000' }],
    }
    await setup(StockValueView, ['inventory.view'], () => ({ data: report }))
    expect(body('[data-testid=value-status]').text()).toContain('Difference')
    expect(body('[data-testid=account-1310]').text()).toContain('100,000')
    expect(body('[data-testid=value-MAIN-FOOD]').text()).toContain('3,000,000')
    expect(body('[data-testid=value-total]').text()).toContain('3,000,000')
    expect(GET.mock.calls[0]![1].params.query).toEqual({ as_of: '2026-09-30' })
  })

  it('lists the receipts nobody has billed by age and marks the old ones', async () => {
    const report = {
      as_of: '2026-09-30', total: '900000', ledger_balance: '900000',
      buckets: [{ bucket: '0-30', count: 1, value: '300000' }, { bucket: '31-60', count: 0, value: '0' }, { bucket: '61-90', count: 1, value: '600000' }, { bucket: '90+', count: 0, value: '0' }],
      rows: [
        { draft_id: 1, receipt_id: 11, receipt_number: 'GRN-000011', received_on: '2026-09-20', supplier_id: 1, supplier_name: 'Sumber Jaya', warehouse_name: 'Main', status: 'OPEN', age_days: 10, bucket: '0-30', value: '300000' },
        { draft_id: 2, receipt_id: 12, receipt_number: 'GRN-000012', received_on: '2026-07-15', supplier_id: 1, supplier_name: 'Sumber Jaya', warehouse_name: 'Main', status: 'PARTIAL', age_days: 77, bucket: '61-90', value: '600000' },
      ],
    }
    await setup(GrniView, ['inventory.view'], () => ({ data: report }))
    expect(body('[data-testid=bucket-61-90]').text()).toContain('600,000')
    expect(body('[data-testid=grni-GRN-000012]').text()).toContain('▲')
    expect(body('[data-testid=grni-GRN-000011]').text()).not.toContain('▲')
    expect(body('[data-testid=grni-total]').text()).toBe('900,000')
  })
})

describe('the inventory page', () => {
  const dash = {
    business_date: '2026-09-30', mtd_from: '2026-09-01', stock_value: '12500000', food: part('9000000', '2700000', '30', '32'), beverage: part('3000000', '720000', '24', '22', true), fb: part('12000000', '3420000', '28.5', null),
    negative_items: 2, negative_value: '150000', below_min: 5, waiting_approvals: 3,
  }
  it('shows the figures that say whether the stock is in order and the list of the reports', async () => {
    await setup(InventoryHomeView, ['inventory.view'], () => ({ data: dash }))
    expect(body('[data-testid=dash-value]').text()).toContain('12,500,000')
    expect(body('[data-testid=dash-food]').text()).toContain('30%')
    expect(body('[data-testid=dash-food]').text()).toContain('2.0 points below')
    expect(body('[data-testid=dash-beverage]').text()).toContain('2.0 points above')
    expect(body('[data-testid=dash-negative]').text()).toContain('150,000')
    expect(body('[data-testid=dash-below]').text()).toContain('5')
    expect(body('[data-testid=dash-waiting]').text()).toContain('3')
    for (const id of ['daily', 'outlet', 'recon', 'card', 'value', 'low', 'neg', 'grni', 'usage', 'gap']) expect(has(`[data-testid=report-${id}]`)).toBe(true)
    expect(body('[data-testid=report-daily]').attributes('href')).toBe('/inventory/reports/daily-cost')
  })
})

describe('the target of an outlet', () => {
  const targets = (own: object[] = []) => (path: string) => (path.endsWith('/cost-targets') ? { data: { data: [{ source: 'POS', outlet_id: null, class: 'FOOD', target_pct: '30' }, ...own] } } : { data: {} })

  it('shows the target of the property when the outlet has none, and saves its own', async () => {
    await setup(OutletCostTargets, ['inventory.view', 'inventory.manage'], targets(), { outletId: 4 })
    expect(body('[data-testid=outlet-targets]').text()).toContain('Property: 30%')
    expect((body('[data-testid=target-FOOD]').element as HTMLInputElement).placeholder).toBe('30')
    expect((body('[data-testid=targets-save]').element as HTMLButtonElement).disabled).toBe(true)
    await type('[data-testid=target-FOOD]', '28.5')
    await click('[data-testid=targets-save]')
    expect(PUT).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/inventory/cost-targets', { params: { path: { propertyId: 7 } }, body: { outlet_id: 4, class: 'FOOD', target_pct: '28.5' } })
    expect(DELETE).not.toHaveBeenCalled()
  })

  it('removes the own target when the field is emptied, and refuses a value outside 1 to 100', async () => {
    await setup(OutletCostTargets, ['inventory.view', 'inventory.manage'], targets([{ source: 'POS', outlet_id: 4, class: 'BEVERAGE', target_pct: '22' }]), { outletId: 4 })
    expect((body('[data-testid=target-BEVERAGE]').element as HTMLInputElement).value).toBe('22')
    await type('[data-testid=target-BEVERAGE]', '120')
    expect(has('[data-testid=target-invalid]')).toBe(true)
    expect((body('[data-testid=targets-save]').element as HTMLButtonElement).disabled).toBe(true)
    await type('[data-testid=target-BEVERAGE]', '')
    await click('[data-testid=targets-save]')
    expect(DELETE).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/inventory/cost-targets', { params: { path: { propertyId: 7 }, query: { outlet_id: 4, class: 'BEVERAGE' } } })
  })

  it('is read only without the right to manage the inventory, and shows nothing without the right to read it', async () => {
    await setup(OutletCostTargets, ['inventory.view'], targets(), { outletId: 4 })
    expect((body('[data-testid=target-FOOD]').element as HTMLInputElement).disabled).toBe(true)
    expect(has('[data-testid=targets-save]')).toBe(false)
    mounted.pop()!.unmount()
    document.body.innerHTML = ''
    await setup(OutletCostTargets, ['pos.manage'], () => { throw new Error('403') }, { outletId: 4 })
    expect(has('[data-testid=outlet-targets]')).toBe(false)
  })
})
