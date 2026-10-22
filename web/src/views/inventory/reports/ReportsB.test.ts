import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { resetDepartments } from '@/composables/useDepartments'
import { setLocale, t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ReportsIndexView from '../ReportsIndexView.vue'
import ItemMovementsView from './ItemMovementsView.vue'
import PriceHistoryView from './PriceHistoryView.vue'
import PurchasesView from './PurchasesView.vue'
import SlowStockView from './SlowStockView.vue'
import TopUsageView from './TopUsageView.vue'

// The reports of PR 6b (docs/architecture/35-inventory.md 22.5): the stock movement per item, the purchases per supplier and the price history, the slow and dead stock, the largest usage.

let GET = vi.fn()
const downloadCsv = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: vi.fn() } }))
vi.mock('@/views/accounting/reportApi', () => ({ downloadCsv: (...a: unknown[]) => downloadCsv(...a), money: (v: string) => v }))

const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
const mounted: VueWrapper[] = []

async function setup(component: unknown, get: (path: string, init?: unknown) => unknown, url = '/') {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'm@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: ['inventory.view'] }] } as never
  usePropertyStore().currentId = 7
  usePropertyStore().clock = { business_date: '2026-09-30' } as never
  resetDepartments()
  GET = vi.fn(async (path: string, init?: unknown) => get(path, init))
  downloadCsv.mockReset()
  await router.push(url)
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
const setValue = async (sel: string, value: string) => {
  const el = document.body.querySelector(sel) as HTMLSelectElement
  el.value = value
  el.dispatchEvent(new Event('change', { bubbles: true }))
  await flushPromises()
}
const lookups = (path: string): unknown => {
  if (/\/items$/.test(path)) return { data: { items: [{ id: 4, code: 'SIRUP', name: 'Syrup' }] } }
  if (/warehouses|categories|suppliers|departments/.test(path)) return { data: { data: [] } }
  return undefined
}
const lastQuery = (report: string): Record<string, unknown> => {
  const calls = GET.mock.calls.filter((c) => String(c[0]).endsWith(`/reports/${report}`))
  return ((calls[calls.length - 1]![1] as { params: { query: Record<string, unknown> } }).params.query)
}

beforeEach(() => setLocale('en'))
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
})

describe('the index lists the reports of 6b', () => {
  it('links the five new reports in their groups', async () => {
    await setup(ReportsIndexView, () => ({ data: {} }))
    const to: Record<string, string> = {
      movements: '/inventory/reports/item-movements', slowStock: '/inventory/reports/slow-stock', purchases: '/inventory/reports/purchases', priceHistory: '/inventory/reports/price-history',
      topUsage: '/inventory/reports/top-usage',
    }
    for (const [id, href] of Object.entries(to)) expect(body(`[data-testid=report-${id}]`).attributes('href')).toBe(href)
  })
})

describe('stock movement per item', () => {
  const zero = { qty: '0', value: '0' }
  const buckets = {
    OPENING_STOCK: { qty: '40', value: '620000' }, RECEIPTS: zero, TRANSFERS_IN: zero, SALES_RETURNS: zero, POS_SALES: { qty: '3', value: '46500' }, USAGE: zero, WASTE: { qty: '2', value: '31000' },
    STAFF_MEALS: zero, ENTERTAINMENT: zero, TRANSFERS_OUT: zero, SUPPLIER_RETURNS: zero, COUNT_VARIANCE: zero, COST_ADJUST: zero,
  }
  const tot = Object.fromEntries(Object.keys(buckets).map((k) => [k, k === 'OPENING_STOCK' ? '620000' : k === 'POS_SALES' ? '46500' : k === 'WASTE' ? '31000' : '0']))
  const rep = {
    from: '2026-09-01', to: '2026-09-30',
    rows: [{ item_id: 3, item_code: 'BERAS', item_name: 'Rice', unit_code: 'kg', category_id: 1, category_name: 'Food', opening: zero, buckets, closing: { qty: '35', value: '542500' } }],
    categories: [{ category_id: 1, category_name: 'Food', opening: '0', buckets: tot, closing: '542500' }],
    total: { category_id: null, category_name: '', opening: '0', buckets: tot, closing: '542500' },
  }
  it('shows the opening, each kind that came in and went out, and the closing, with the totals by category', async () => {
    await setup(ItemMovementsView, (path) => lookups(path) ?? { data: rep })
    const row = body('[data-testid=move-BERAS]').text()
    expect(row).toContain('Rice')
    expect(body('[data-testid=move-BERAS-OPENING_STOCK]').text()).toContain('620,000')
    expect(body('[data-testid=move-BERAS-POS_SALES]').text()).toContain('46,500')
    expect(body('[data-testid=move-BERAS-WASTE]').text()).toContain('31,000')
    expect(body('[data-testid=move-BERAS-RECEIPTS]').text()).toBe('–')
    expect(body('[data-testid=move-BERAS-closing]').text()).toContain('542,500')
    expect(body('[data-testid=movecat-1]').text()).toContain('Food')
    expect(body('[data-testid=movecat-total]').text()).toContain('542,500')
    expect(document.body.textContent).toContain('Opening stock')
    expect(document.body.textContent).toContain('Returns to suppliers')
  })
  it('sends the filters with the file, and says so when nothing moved', async () => {
    await setup(ItemMovementsView, (path) => lookups(path) ?? { data: rep })
    await click('[data-testid=report-csv]')
    expect(downloadCsv).toHaveBeenCalledOnce()
    await setup(ItemMovementsView, (path) => lookups(path) ?? { data: { ...rep, rows: [], categories: [] } })
    expect(has('[data-testid=movements-empty]')).toBe(true)
  })
})

describe('purchases by supplier', () => {
  const rep = {
    from: '2026-09-01', to: '2026-09-30', value: '6920000', billed_value: '3060000', receipts: 3, bills: 1,
    suppliers: [
      { supplier_id: 2, supplier_name: 'Pasar', value: '3920000', share_pct: '56.65', receipts: 2, bills: 0, billed_value: '0', items: [
        { item_id: 4, item_code: 'SIRUP', item_name: 'Syrup', unit_code: 'botol', category_name: 'Beverage', qty: '60', value: '3120000', avg_price: '52000', receipts: 1, bills: 0, billed_value: '0' },
      ] },
      { supplier_id: 1, supplier_name: 'Supplier One', value: '3000000', share_pct: '43.35', receipts: 1, bills: 1, billed_value: '3060000', items: [
        { item_id: 4, item_code: 'SIRUP', item_name: 'Syrup', unit_code: 'botol', category_name: 'Beverage', qty: '60', value: '3000000', avg_price: '50000', receipts: 1, bills: 1, billed_value: '3060000' },
      ] },
    ],
  }
  it('shows each supplier with its share and its items, and links an item to its price history', async () => {
    await setup(PurchasesView, (path) => lookups(path) ?? { data: rep })
    expect(body('[data-testid=kpi-purchases-total]').text()).toContain('6,920,000')
    expect(body('[data-testid=purchase-supplier-2]').text()).toContain('Pasar')
    expect(body('[data-testid=purchase-supplier-2]').text()).toContain('56.65')
    expect(body('[data-testid=purchase-1-SIRUP]').text()).toContain('3,060,000')
    expect(body('[data-testid=purchase-2-SIRUP]').text()).toContain('52,000')
    const href = body('[data-testid=purchase-history-SIRUP]').attributes('href')
    expect(href).toContain('/inventory/reports/price-history')
    expect(href).toContain('item=4')
  })
  it('says so when nothing was received', async () => {
    await setup(PurchasesView, (path) => lookups(path) ?? { data: { ...rep, suppliers: [], value: '0' } })
    expect(has('[data-testid=purchases-empty]')).toBe(true)
  })
})

describe('purchase price history', () => {
  const rep = {
    from: '2026-09-01', to: '2026-09-30', item_id: 4, item_code: 'SIRUP', item_name: 'Syrup', unit_code: 'botol',
    min_price: '50000', max_price: '52000', last_price: '52000', avg_price: '51000',
    rows: [
      { receipt_id: 1, receipt_number: 'RCV-000001', received_on: '2026-09-10', supplier_id: 1, supplier_name: 'Supplier One', purchase_unit_code: 'karton', qty_purchase: '5', unit_price: '600000', price_per_stock: '50000', change_pct: null },
      { receipt_id: 2, receipt_number: 'RCV-000002', received_on: '2026-09-20', supplier_id: 2, supplier_name: 'Pasar', purchase_unit_code: 'karton', qty_purchase: '5', unit_price: '624000', price_per_stock: '52000', change_pct: '4' },
    ],
  }
  it('asks for an item first, and does not call the report without one', async () => {
    await setup(PriceHistoryView, (path) => lookups(path) ?? { data: rep })
    expect(has('[data-testid=pricehistory-pick]')).toBe(true)
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/reports/price-history'))).toBe(false)
  })
  it('shows the prices of the item from the link, the lowest, the highest and the change from the receipt before', async () => {
    await setup(PriceHistoryView, (path) => lookups(path) ?? { data: rep }, '/inventory/reports/price-history?item=4')
    expect(lastQuery('price-history').item_id).toBe(4)
    expect(body('[data-testid=kpi-price-min]').text()).toContain('50,000')
    expect(body('[data-testid=kpi-price-max]').text()).toContain('52,000')
    expect(body('[data-testid=kpi-price-avg]').text()).toContain('51,000')
    expect(body('[data-testid=price-RCV-000001]').text()).toContain('600,000')
    expect(body('[data-testid=price-RCV-000002]').text()).toContain('+4')
    await click('[data-testid=report-csv]')
    expect(downloadCsv).toHaveBeenCalledOnce()
  })
})

describe('slow and dead stock', () => {
  const rep = {
    as_of: '2026-09-30', days: 90, costing_method: 'FIFO', age_basis: 'OLDEST_LAYER', total: '330000', never_out: 1,
    rows: [
      { warehouse_id: 1, warehouse_code: 'MAIN', warehouse_name: 'Main', item_id: 1, item_code: 'SOAP', item_name: 'Soap', unit_code: 'pcs', category_name: 'Supplies', qty: '100', value: '296000', last_out_on: null, days_since_out: null, never_out: true, age_days: 200 },
      { warehouse_id: 1, warehouse_code: 'MAIN', warehouse_name: 'Main', item_id: 2, item_code: 'SYRUP', item_name: 'Syrup', unit_code: 'btl', category_name: 'Beverage', qty: '2', value: '34000', last_out_on: '2026-05-01', days_since_out: 152, never_out: false, age_days: 160 },
    ],
  }
  it('lists the items that were never used first, then the longest unused, with the age basis of the costing method', async () => {
    await setup(SlowStockView, (path) => lookups(path) ?? { data: rep })
    expect(lastQuery('slow-stock').days).toBe(90)
    expect(body('[data-testid=slow-MAIN-SOAP]').text()).toContain('Never used')
    expect(body('[data-testid=slow-MAIN-SOAP]').text()).toContain('200 days')
    expect(body('[data-testid=slow-MAIN-SYRUP]').text()).toContain('2026-05-01')
    expect(body('[data-testid=slow-MAIN-SYRUP]').text()).toContain('152')
    expect(body('[data-testid=kpi-slow-total]').text()).toContain('330,000')
    expect(body('[data-testid=slow-basis]').text()).toContain('oldest layer')
  })
  it('asks again with the window that was chosen, and sends it with the file', async () => {
    await setup(SlowStockView, (path) => lookups(path) ?? { data: rep })
    await setValue('select[name=days]', '30')
    expect(lastQuery('slow-stock').days).toBe(30)
    await click('[data-testid=report-csv]')
    expect(downloadCsv).toHaveBeenCalledOnce()
    expect(JSON.stringify(downloadCsv.mock.calls[0])).toContain('"days":30')
  })
})

describe('the largest usage', () => {
  const rep = {
    from: '2026-09-01', to: '2026-09-30', limit: 20, kinds: ['POS_SALE', 'ISSUE', 'WASTE', 'STAFF_MEAL', 'ENTERTAINMENT'], items: 3, shown: '227500', total: '242300',
    rows: [
      { rank: 1, item_id: 4, item_code: 'SIRUP', item_name: 'Syrup', unit_code: 'botol', category_name: 'Beverage', qty: '3', value: '150000', share_pct: '61.91', cumulative_pct: '61.91' },
      { rank: 2, item_id: 3, item_code: 'BERAS', item_name: 'Rice', unit_code: 'kg', category_name: 'Food', qty: '5', value: '77500', share_pct: '31.99', cumulative_pct: '93.9' },
    ],
  }
  it('ranks the items by value with their share and the running share', async () => {
    await setup(TopUsageView, (path) => lookups(path) ?? { data: rep })
    expect(body('[data-testid=top-SIRUP]').text()).toContain('150,000')
    expect(body('[data-testid=top-SIRUP]').text()).toContain('61.91')
    expect(body('[data-testid=top-BERAS]').text()).toContain('93.9')
    expect(body('[data-testid=top-total]').text()).toContain('227,500')
    expect(lastQuery('top-usage').kinds).toBe('POS_SALE,ISSUE,WASTE,STAFF_MEAL,ENTERTAINMENT')
    expect(lastQuery('top-usage').limit).toBe(20)
  })
  it('asks again with the kinds and the size of the list that were chosen', async () => {
    await setup(TopUsageView, (path) => lookups(path) ?? { data: rep })
    await click('[data-testid=top-kind-ISSUE]')
    await click('[data-testid=top-kind-STAFF_MEAL]')
    await click('[data-testid=report-show]')
    expect(lastQuery('top-usage').kinds).toBe('POS_SALE,WASTE,ENTERTAINMENT')
    await setValue('select[name=limit]', '50')
    expect(lastQuery('top-usage').limit).toBe(50)
    await click('[data-testid=report-csv]')
    expect(JSON.stringify(downloadCsv.mock.calls[0])).toContain('"limit":50')
  })
})

describe('the texts of the reports of 6b', () => {
  it('has an English and an Indonesian name for every kind of movement and of usage', async () => {
    const buckets = ['OPENING_STOCK', 'RECEIPTS', 'TRANSFERS_IN', 'SALES_RETURNS', 'POS_SALES', 'USAGE', 'WASTE', 'STAFF_MEALS', 'ENTERTAINMENT', 'TRANSFERS_OUT', 'SUPPLIER_RETURNS', 'COUNT_VARIANCE', 'COST_ADJUST']
    const kinds = ['POS_SALE', 'ISSUE', 'WASTE', 'STAFF_MEAL', 'ENTERTAINMENT', 'TRANSFER_OUT', 'SUPPLIER_RETURN']
    for (const locale of ['en', 'id'] as const) {
      await setLocale(locale)
      for (const b of buckets) expect(t(`inventory.reports.movements.bucket.${b}`), `${locale} ${b}`).not.toBe(`inventory.reports.movements.bucket.${b}`)
      for (const k of kinds) expect(t(`inventory.reports.topUsage.kind.${k}`), `${locale} ${k}`).not.toBe(`inventory.reports.topUsage.kind.${k}`)
      for (const b of ['LAST_ENTRY', 'OLDEST_LAYER']) expect(t(`inventory.reports.slowStock.basis${b}`)).not.toContain('inventory.reports')
    }
  })
})
