import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { resetDepartments } from '@/composables/useDepartments'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ReportsIndexView from '../ReportsIndexView.vue'
import AmenityMonthsView from './AmenityMonthsView.vue'
import CountVariancesView from './CountVariancesView.vue'
import DailyCostView from './DailyCostView.vue'
import LowStockView from './LowStockView.vue'
import MealsView from './MealsView.vue'
import PendingApprovalsView from './PendingApprovalsView.vue'
import PriceVariancesView from './PriceVariancesView.vue'
import ReconciliationView from './ReconciliationView.vue'
import RecipeCostsView from './RecipeCostsView.vue'
import TransfersNoSrView from './TransfersNoSrView.vue'
import WasteView from './WasteView.vue'

let GET = vi.fn()
let POST = vi.fn()
const downloadCsv = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))
vi.mock('@/views/accounting/reportApi', () => ({ downloadCsv: (...a: unknown[]) => downloadCsv(...a), money: (v: string) => v }))

const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
const mounted: VueWrapper[] = []

async function setup(component: unknown, permissions: string[], get: (path: string, init?: unknown) => unknown) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'm@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  usePropertyStore().clock = { business_date: '2026-09-30' } as never
  resetDepartments()
  GET = vi.fn(async (path: string, init?: unknown) => get(path, init))
  POST = vi.fn().mockResolvedValue({ data: { id: 91 } })
  downloadCsv.mockReset()
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

const view = ['inventory.view']
const lookups = (path: string): unknown => (/warehouses|categories|departments/.test(path) ? { data: { data: [] } } : undefined)

describe('the index of the reports', () => {
  it('lists every report in five groups, each a link to its page', async () => {
    await setup(ReportsIndexView, view, () => ({ data: {} }))
    for (const g of ['stock', 'purchasing', 'fb', 'use', 'control']) expect(document.body.textContent).toContain({ stock: 'Stock', purchasing: 'Purchasing', fb: 'Food and beverage cost', use: 'Usage', control: 'Control' }[g])
    const to: Record<string, string> = {
      card: '/inventory/costing', value: '/inventory/reports/stock-value', low: '/inventory/reports/low-stock', neg: '/inventory/reports/negative-stock', grni: '/inventory/reports/received-not-invoiced',
      priceVar: '/inventory/reports/price-variances', daily: '/inventory/reports/daily-cost', outlet: '/inventory/reports/outlet-cost', recon: '/inventory/reports/fb-reconciliation',
      recipeCosts: '/inventory/reports/recipe-costs', usage: '/inventory/reports/usage', waste: '/inventory/reports/waste', meals: '/inventory/reports/meals', amenities: '/inventory/reports/amenities-by-month',
      countVar: '/inventory/reports/count-variances', trfNoSr: '/inventory/reports/transfers-without-requisition', pending: '/inventory/reports/pending-approvals', gap: '/inventory/pos-gap',
    }
    for (const [id, href] of Object.entries(to)) expect(body(`[data-testid=report-${id}]`).attributes('href')).toBe(href)
  })
})

describe('waste by reason', () => {
  const rep = {
    from: '2026-09-01', to: '2026-09-30', total: '95000', docs: 3,
    reasons: [{ reason: 'SPOILAGE', value: '45000', docs: 2 }, { reason: 'POS_VOID', value: '50000', docs: 1 }],
    rows: [
      { reason: 'SPOILAGE', warehouse_id: 1, warehouse_code: 'KIT', warehouse_name: 'Kitchen', department_id: 3, department_name: 'F&B', item_id: 4, item_code: 'BEEF', item_name: 'Beef', unit_code: 'KG', category_name: 'Meat', qty: '1.5', value: '45000', docs: 2 },
      { reason: 'POS_VOID', warehouse_id: 1, warehouse_code: 'KIT', warehouse_name: 'Kitchen', department_id: 3, department_name: 'F&B', item_id: 5, item_code: 'STEAK', item_name: 'Steak', unit_code: 'PCS', category_name: 'Dish', qty: '0', value: '50000', docs: 1 },
    ],
  }
  it('shows the total by reason, names the void of the POS, and sends the filters with the CSV', async () => {
    await setup(WasteView, view, (path) => lookups(path) ?? { data: rep })
    expect(body('[data-testid=reason-SPOILAGE]').text()).toContain('Spoilage')
    expect(body('[data-testid=reason-POS_VOID]').text()).toContain('POS void')
    expect(body('[data-testid=waste-total]').text()).toContain('95,000')
    expect(body('[data-testid=waste-table]').text()).toContain('Beef')
    await click('[data-testid=report-csv]')
    expect(downloadCsv).toHaveBeenCalledOnce()
  })
  it('says so when nothing was thrown away', async () => {
    await setup(WasteView, view, (path) => lookups(path) ?? { data: { ...rep, rows: [], reasons: [], total: '0', docs: 0 } })
    expect(has('[data-testid=waste-empty]')).toBe(true)
  })
})

describe('count variances', () => {
  const rep = {
    from: '2026-09-01', to: '2026-09-30', gain: '10000', loss: '40000', net: '-30000',
    counts: [{ id: 12, number: 'CNT-0012', warehouse_id: 1, warehouse_code: 'KIT', warehouse_name: 'Kitchen', scope: 'FULL', started_on: '2026-09-29', approved_on: '2026-09-30', approver_name: 'Dewi', gain: '10000', loss: '40000', net: '-30000', lines: 20, lines_with_variance: 2 }],
    lines: [{ count_id: 12, count_number: 'CNT-0012', approved_on: '2026-09-30', warehouse_id: 1, warehouse_code: 'KIT', item_id: 4, item_code: 'BEEF', item_name: 'Beef', unit_code: 'KG', category_name: 'Meat', book_qty: '10', counted_qty: '9', variance_qty: '-1', variance_value: '-40000' }],
    items: [{ warehouse_id: 1, warehouse_code: 'KIT', item_id: 4, item_code: 'BEEF', item_name: 'Beef', unit_code: 'KG', counts: 1, variance_qty: '-1', variance_value: '-40000' }],
  }
  it('lists the counts with a link to each, then the lines and the items on their own tabs', async () => {
    await setup(CountVariancesView, view, (path) => lookups(path) ?? { data: rep })
    expect(body('[data-testid=countvar-totals]').text()).toContain('-30,000')
    expect(body('[data-testid=count-CNT-0012] a').attributes('href')).toBe('/inventory/counts/12')
    await click('[data-testid=tab-LINES]')
    expect(body('[data-testid=countvar-lines]').text()).toContain('Beef')
    expect(body('[data-testid=countvar-lines]').text()).toContain('-40,000')
    await click('[data-testid=tab-ITEMS]')
    expect(has('[data-testid=countvar-items]')).toBe(true)
  })
})

describe('purchase price variance', () => {
  const item = { item_id: 4, item_code: 'BEEF', item_name: 'Beef', unit_code: 'KG', qty: '10', receipt_value: '1000000', bill_cost: '1100000', receipt_unit_price: '100000', bill_unit_price: '110000', variance: '100000', pct: '10', bills: 1, lines_above_tolerance: 1, lines: 1 }
  const rep = {
    from: '2026-09-01', to: '2026-09-30', tolerance_pct: '5', bills: 2, bills_above_tolerance: 1, receipt_value: '1500000', bill_cost: '1600000', variance: '100000', pct: '6.67',
    suppliers: [{ supplier_id: 8, supplier_name: 'Meat Co', bills: 2, bills_above_tolerance: 1, lines: 2, lines_above_tolerance: 1, receipt_value: '1500000', bill_cost: '1600000', variance: '100000', pct: '6.67', items: [item] }],
  }
  it('shows the variance against the value received and how many bills went above the tolerance, and opens the items of a supplier', async () => {
    await setup(PriceVariancesView, view, () => ({ data: rep }))
    expect(body('[data-testid=kpi-variance]').text()).toContain('+100,000')
    expect(body('[data-testid=kpi-variance]').text()).toContain('6.67%')
    expect(body('[data-testid=kpi-above]').text()).toContain('1')
    expect(body('[data-testid=kpi-above]').text()).toContain('5%')
    expect(body('[data-testid=supplier-8]').text()).toContain('Meat Co')
    await click('[data-testid=supplier-toggle-8]')
    expect(body('[data-testid=supplier-8]').text()).toContain('Beef')
    expect(body('[data-testid=supplier-8]').text()).toContain('110,000')
  })
})

describe('transfers without a requisition', () => {
  const row = (over: Record<string, unknown>) => ({ transfer_id: 3, number: 'TRF-0003', business_date: '2026-09-12', status: 'POSTED', from_warehouse: 'MAIN', to_warehouse: 'KIT', created_by_name: 'Agus', note: null, item_id: 4, item_code: 'BEEF', item_name: 'Beef', unit_code: 'KG', qty: '2', value: '80000', ...over })
  it('lists the transfers with a link, marks a reversed one, and sends the warehouse filters with the CSV', async () => {
    await setup(TransfersNoSrView, view, (path) => lookups(path) ?? { data: { from: '2026-09-01', to: '2026-09-30', docs: 1, total: '80000', rows: [row({}), row({ transfer_id: 4, number: 'TRF-0004', status: 'REVERSED' })] } })
    expect(body('[data-testid=trfnosr-table] a').attributes('href')).toBe('/inventory/transfers/3')
    expect(body('[data-testid=trfnosr-table]').text()).toContain('Reversed')
    expect(body('[data-testid=trfnosr-total]').text()).toContain('80,000')
    expect(body('[data-testid=trfnosr-total]').text()).toContain('1 transfer')
  })
})

describe('amenities by month', () => {
  const month = (m: string, over: Record<string, unknown>) => ({ month: m, nights: 600, occupied_rooms: 600, guests: 900, comp_rooms: 0, house_use_rooms: 0, cost: '1200000', cost_per_room: '2000', cost_per_guest: '1333', prev_cost: '1000000', prev_cost_per_room: '2500', change_pct: '20', items: [{ item_id: 6, item_code: 'SOAP', item_name: 'Soap', unit_code: 'PCS', qty: '400', cost: '1200000', prev_cost: '1000000' }], ...over })
  it('shows the cost per occupied room and the change against the month before, and opens the items of a month', async () => {
    const w = await setup(AmenityMonthsView, view, () => ({ data: { from: '2026-04-01', to: '2026-09-30', months: [month('2026-09-01', {}), month('2026-08-01', { change_pct: null, prev_cost: null })] } }))
    expect(body('[data-testid=month-2026-09]').text()).toContain('1,200,000')
    expect(body('[data-testid=month-2026-09]').text()).toContain('+20%')
    expect(body('[data-testid=month-2026-08]').text()).toContain('–')
    await click('[data-testid=month-toggle-2026-09]')
    expect(body('[data-testid=amenity-table]').text()).toContain('Soap')
    expect(GET.mock.calls[0]![1].params.query.from).toBe('2026-04-01')
    w.unmount()
    mounted.pop()
  })
})

describe('documents waiting for approval', () => {
  const doc = (over: Record<string, unknown>) => ({ doc_type: 'PR', doc_id: 31, number: 'PR-0031', department_name: 'Kitchen', requester_name: 'Sari', level_no: 2, step_name: 'Cost control', awaiting: 'Budi', submitted_at: '2026-09-20T02:00:00Z', entered_at: '2026-09-28T02:00:00Z', age_hours: 22, age_days: 0, value: '2500000', ...over })
  it('links a request and a requisition to their pages and counts the age in hours under two days, in days after', async () => {
    await setup(PendingApprovalsView, view, () => ({ data: { total: '3300000', rows: [doc({}), doc({ doc_type: 'SR', doc_id: 8, number: 'SR-0008', age_hours: 120, age_days: 5, value: '800000' })] } }))
    expect(body('[data-testid="pending-PR-0031"] a').attributes('href')).toBe('/inventory/requests/31')
    expect(body('[data-testid="pending-SR-0008"] a').attributes('href')).toBe('/inventory/requisitions/8')
    expect(body('[data-testid="pending-PR-0031"] [data-testid=pending-age]').text()).toBe('22 h')
    expect(body('[data-testid="pending-SR-0008"] [data-testid=pending-age]').text()).toBe('5 days')
    expect(body('[data-testid=pending-total]').text()).toContain('3,300,000')
  })
  it('says so when nothing waits', async () => {
    await setup(PendingApprovalsView, view, () => ({ data: { total: '0', rows: [] } }))
    expect(has('[data-testid=pending-empty]')).toBe(true)
  })
})

describe('recipe costs', () => {
  const row = (over: Record<string, unknown>) => ({ outlet_id: 2, outlet_code: 'REST', outlet_name: 'Restaurant', product_id: 1, product_code: 'NASI', product_name: 'Nasi goreng', category_name: 'Main', class: 'FOOD', has_recipe: true, cost: '20000', net_price: '50000', margin: '30000', cost_pct: '40', target_pct: '32', above_target: true, ...over })
  it('shows the cost percentage against the target, flags a product with no recipe, and filters by outlet', async () => {
    await setup(RecipeCostsView, view, () => ({ data: { currency_decimals: 0, without_recipe: 1, rows: [row({}), row({ outlet_id: 3, outlet_code: 'BAR', product_id: 9, product_code: 'MOJ', product_name: 'Mojito', has_recipe: false, cost: null, margin: null, cost_pct: null, target_pct: null, above_target: false })] } }))
    expect(body('[data-testid=recipecost-REST-NASI]').text()).toContain('40%')
    expect(body('[data-testid=recipecost-REST-NASI]').text()).toContain('32%')
    expect(body('[data-testid=recipecost-BAR-MOJ]').text()).toContain('No recipe')
    expect(body('[data-testid=recipecost-missing]').text()).toContain('1 product')
    const sel = document.body.querySelector('select[name=outlet_id]') as HTMLSelectElement
    sel.value = '3'
    sel.dispatchEvent(new Event('change', { bubbles: true }))
    await flushPromises()
    expect(has('[data-testid=recipecost-REST-NASI]')).toBe(false)
    expect(has('[data-testid=recipecost-BAR-MOJ]')).toBe(true)
  })
})

describe('staff meals and entertainment', () => {
  it('shows the value, the documents and the cost of a portion by department', async () => {
    await setup(MealsView, view, (path) => lookups(path) ?? ({ data: {
      from: '2026-09-01', to: '2026-09-30',
      rows: [{ department_id: 3, department_name: 'F&B', purpose: 'STAFF_MEAL', docs: 2, covers: 40, value: '200000', cost_per_cover: '5000' }, { department_id: 3, department_name: 'F&B', purpose: 'ENTERTAINMENT', docs: 1, covers: 0, value: '90000', cost_per_cover: null }],
      totals: { STAFF_MEAL: { purpose: 'STAFF_MEAL', docs: 2, covers: 40, value: '200000', cost_per_cover: '5000' }, ENTERTAINMENT: { purpose: 'ENTERTAINMENT', docs: 1, covers: 0, value: '90000', cost_per_cover: null } },
    } }))
    expect(body('[data-testid=total-STAFF_MEAL]').text()).toContain('200,000')
    expect(body('[data-testid=total-STAFF_MEAL]').text()).toContain('5,000')
    expect(body('[data-testid=meals-table]').text()).toContain('Entertainment')
  })
})

describe('the daily cost, opened on a day', () => {
  const day = {
    date: '2026-09-29', live: false, kinds: { SALE_COST_FOOD: '300000', WASTE: '20000' }, total: '320000', occupied_rooms: 0, amenity_cost_per_room: null, departments: {},
    food: { revenue: '0', cost: '0', pct: null, target_pct: null, above_target: false, mtd_pct: null, no_recipe_days: 0, no_recipe_data: false },
    beverage: { revenue: '0', cost: '0', pct: null, target_pct: null, above_target: false, mtd_pct: null, no_recipe_days: 0, no_recipe_data: false },
    fb: { revenue: '0', cost: '0', pct: null, target_pct: null, above_target: false, mtd_pct: null, no_recipe_days: 0, no_recipe_data: false },
  }
  const costDay = {
    date: '2026-09-29', total: '320000',
    kinds: [
      { cost_kind: 'SALE_COST_FOOD', total: '300000', docs: [{ cost_kind: 'SALE_COST_FOOD', source_type: 'POS_QUEUE', source_id: 5, link_id: 77, number: 'BILL-77', department_name: '', outlet_name: 'Restaurant', lines: 3, amount: '300000' }] },
      { cost_kind: 'WASTE', total: '20000', docs: [{ cost_kind: 'WASTE', source_type: 'INV_ISSUE', source_id: 14, link_id: 14, number: 'ISS-0014', department_name: 'F&B', outlet_name: '', lines: 1, amount: '20000' }] },
      { cost_kind: 'AMENITY', total: '0', docs: [] },
    ],
  }
  it('opens the documents of the date that was clicked, per kind, each with a link, and closes again', async () => {
    await setup(DailyCostView, view, (path, init) => {
      if (path.endsWith('/cost-day')) return { data: costDay, init }
      return { data: { from: '2026-09-29', to: '2026-09-29', business_date: '2026-09-30', mtd_from: '2026-09-01', targets: [], days: [day], period: day, mtd: day, departments: [] } }
    })
    await click('[data-testid="daily-open-2026-09-29"]')
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/cost-day') && c[1].params.query.date === '2026-09-29')).toBe(true)
    expect(body('[data-testid=cost-day-SALE_COST_FOOD] a').attributes('href')).toBe('/pos/bills/77')
    expect(body('[data-testid=cost-day-WASTE] a').attributes('href')).toBe('/inventory/issues/14')
    expect(has('[data-testid=cost-day-AMENITY]')).toBe(false)
    await click('[data-testid=cost-day-close]')
    expect(has('[data-testid=cost-day]')).toBe(false)
  })
})

describe('the reconciliation, on the days with recipe data', () => {
  it('says how many days have no recipe data', async () => {
    const col = { theory: '0', theory_revenue: '0', no_recipe_days: 0 }
    await setup(ReconciliationView, view, () => ({ data: { from: '2026-09-01', to: '2026-09-30', closing_basis: 'BOOK', food: col, beverage: col, total: { ...col, no_recipe_days: 4 }, gl: { status: 'MATCH', gl_through: '2026-09-29', lines: [], stock_total: '0', gl_total: '0', difference: '0' } } }))
    expect(body('[data-testid=recon-no-recipe-days]').text()).toBe('4 day(s) without recipe data')
  })
})

describe('items below the minimum, and the purchase request made from them', () => {
  const row = (wh: number, whCode: string, item: number, code: string) => ({ warehouse_id: wh, warehouse_code: whCode, item_id: item, item_code: code, item_name: code, unit_code: 'KG', qty: '2', min_qty: '10', max_qty: '30', reorder_qty: '0', missing: '8', suggest_qty: '28', value: '100000' })
  const rows = [row(1, 'KIT', 4, 'BEEF'), row(1, 'KIT', 5, 'RICE'), row(2, 'BAR', 6, 'LIME')]
  const get = () => ({ data: { data: rows } })
  it('creates a draft request for the chosen items of one warehouse and opens it', async () => {
    await setup(LowStockView, ['inventory.view', 'inventory.request'], get)
    expect(body('[data-testid=low-KIT-BEEF]').text()).toContain('28')
    await click('[data-testid=low-check-KIT-BEEF]')
    await click('[data-testid=low-check-KIT-RICE]')
    await click('[data-testid=low-make-pr]')
    expect(POST).toHaveBeenCalledOnce()
    const arg = POST.mock.calls[0]![1]
    expect(arg.body.warehouse_id).toBe(1)
    expect(arg.body.item_ids).toEqual([4, 5])
    expect(typeof arg.body.client_uuid).toBe('string')
    expect(router.currentRoute.value.fullPath).toBe('/inventory/requests/91')
  })
  it('refuses items of two warehouses in one request', async () => {
    await setup(LowStockView, ['inventory.view', 'inventory.request'], get)
    await click('[data-testid=low-check-KIT-BEEF]')
    await click('[data-testid=low-check-BAR-LIME]')
    await click('[data-testid=low-make-pr]')
    expect(POST).not.toHaveBeenCalled()
    expect(body('[data-testid=low-warning]').text()).toContain('one warehouse')
  })
  it('does not offer the request without the right to make one', async () => {
    await setup(LowStockView, view, get)
    expect(has('[data-testid=low-make-pr]')).toBe(false)
    expect(has('[data-testid=low-select-all]')).toBe(false)
  })
})
