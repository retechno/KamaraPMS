import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { resetDepartments } from '@/composables/useDepartments'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import InventoryCategoriesView from './InventoryCategoriesView.vue'
import InventoryCategoryFormView from './InventoryCategoryFormView.vue'
import InventoryCostingView from './InventoryCostingView.vue'
import InventoryItemFormView from './InventoryItemFormView.vue'
import InventoryItemsView from './InventoryItemsView.vue'
import InventoryOpeningView from './InventoryOpeningView.vue'
import InventoryUnitsView from './InventoryUnitsView.vue'
import InventoryWarehouseFormView from './InventoryWarehouseFormView.vue'
import InventoryWarehousesView from './InventoryWarehousesView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PUT = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PUT: (...a: unknown[]) => PUT(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))

const units = [
  { id: 1, code: 'kg', name: 'Kilogram', is_active: true, in_use: true },
  { id: 2, code: 'g', name: 'Gram', is_active: true, in_use: true },
  { id: 3, code: 'botol', name: 'Botol', is_active: true, in_use: true },
  { id: 4, code: 'karton', name: 'Karton', is_active: true, in_use: false },
]
const categories = [
  { id: 1, code: 'MAKANAN', name: 'Makanan', kind: 'FOOD', inventory_account_id: 10, inventory_account_code: '1310', inventory_account_name: 'Food inventory', cogs_account_id: 20, cogs_account_code: '5210', issue_account_id: null, waste_account_id: null, variance_account_id: null, is_active: true, sort_order: 0, item_count: 2, version: 1 },
  { id: 2, code: 'MINUMAN', name: 'Minuman', kind: 'BEVERAGE', inventory_account_id: 11, inventory_account_code: '1320', inventory_account_name: 'Beverage inventory', cogs_account_id: null, issue_account_id: null, waste_account_id: null, variance_account_id: null, is_active: true, sort_order: 1, item_count: 1, version: 1 },
]
const stockUnit = { unit_id: 3, unit_code: 'botol', stock_num: 1, stock_den: 1, is_purchase: true, is_recipe: false, is_default_purchase: true, is_default_recipe: false }
const sirup = {
  id: 7, code: 'SIRUP', name: 'Sirup gula aren 650 ml', category_id: 2, category_code: 'MINUMAN', category_name: 'Minuman', category_kind: 'BEVERAGE', stock_unit_id: 3, stock_unit_code: 'botol',
  reference_cost: null, default_supplier_id: null, barcode: null, note: null, is_active: true, version: 2, qty_on_hand: '56.2', value_on_hand: '2764000', below_min: false, below_zero: false, has_stock: true,
  units: [stockUnit, { unit_id: 4, unit_code: 'karton', stock_num: 12, stock_den: 1, is_purchase: true, is_recipe: false, is_default_purchase: false, is_default_recipe: false }, { unit_id: 5, unit_code: 'ml', stock_num: 1, stock_den: 650, is_purchase: false, is_recipe: true, is_default_purchase: false, is_default_recipe: true }],
  stock: [
    { warehouse_id: 1, warehouse_code: 'UTAMA', warehouse_name: 'Gudang Utama', qty: '36', value: '1764000', average_cost: '49000', min_qty: '0' },
    { warehouse_id: 3, warehouse_code: 'BAR', warehouse_name: 'Gudang Bar', qty: '20.2', value: '1000000', average_cost: '49500', min_qty: '12' },
  ],
}
const es = {
  id: 9, code: 'ES', name: 'Es batu kristal 10 kg', category_id: 2, category_code: 'MINUMAN', category_name: 'Minuman', category_kind: 'BEVERAGE', stock_unit_id: 1, stock_unit_code: 'kg', reference_cost: '6000', default_supplier_id: null,
  barcode: null, note: null, is_active: true, version: 1, qty_on_hand: '-4', value_on_hand: '-24000', below_min: false, below_zero: true, has_stock: true, units: [{ ...stockUnit, unit_id: 1, unit_code: 'kg' }],
  stock: [{ warehouse_id: 3, warehouse_code: 'BAR', warehouse_name: 'Gudang Bar', qty: '-4', value: '-24000', average_cost: null, min_qty: '0' }],
}
const warehouses = [
  { id: 1, code: 'UTAMA', name: 'Gudang Utama', kind: 'MAIN', outlet_id: null, outlet_code: null, department_id: null, department_code: null, cost_class: 'OTHER', is_active: true, frozen: false, stock_value: '1764000', item_count: 1, version: 1 },
  { id: 3, code: 'BAR', name: 'Gudang Bar', kind: 'OUTLET', outlet_id: 5, outlet_code: 'BAR', department_id: 6, department_code: 'FB', cost_class: 'BEVERAGE', is_active: true, frozen: false, stock_value: '976000', item_count: 2, version: 4 },
]
const card = {
  item: sirup, warehouse_id: 3, warehouse: 'BAR', costing_method: 'AVERAGE', from: null, to: null, opening_qty: '0', opening_value: '0', closing_qty: '28.2', closing_value: '1393080', truncated: false, layers: [], shortfalls: [],
  lines: [
    { movement_id: 1, business_date: '2026-09-01', kind: 'OPENING', source_type: 'INV_OPENING', document_number: 'OPN-000001', qty: '20', value: '980000', unit_cost: '49000', cost_estimated: false, balance_qty: '20', balance_value: '980000', average_cost: '49000' },
    { movement_id: 2, business_date: '2026-09-02', kind: 'POS_SALE', source_type: 'POS', qty: '-2', value: '-98000', unit_cost: '49000', cost_estimated: false, balance_qty: '18', balance_value: '882000', average_cost: '49000' },
  ],
}

const blank = { template: '<div />' }
const router = createRouter({
  history: createMemoryHistory(),
  routes: [
    { path: '/inventory/items/new', component: blank }, { path: '/inventory/items/:id', component: blank },
    { path: '/inventory/warehouses/new', component: blank }, { path: '/inventory/warehouses/:id', component: blank },
    { path: '/inventory/categories/new', component: blank }, { path: '/inventory/categories/:id', component: blank },
    { path: '/inventory/openings/new', component: blank }, { path: '/inventory/openings/:id', component: blank },
    { path: '/:p(.*)*', component: blank },
  ],
})
const click = async (sel: string): Promise<void> => {
  ;(document.body.querySelector(sel) as HTMLElement).click()
  await flushPromises()
}

const queueList = {
  summary: { pending: 0, held: 1, failed: 1 },
  rows: [
    { id: 8, outlet_id: 1, outlet_code: 'R', outlet_name: 'Restoran', warehouse_id: 2, warehouse_name: 'Dapur', bill_line_id: 70, bill_number: 'R-0008', kind: 'SALE', product_id: 3, product_name: 'Es Sirup Vanila', quantity: '2', sale_class: 'BEVERAGE', sale_date: '2026-09-30', status: 'HELD', attempts: 0, error_code: null, error_message: null, created_at: '2026-09-30T10:00:00Z', applied_on: null },
    { id: 9, outlet_id: 1, outlet_code: 'R', outlet_name: 'Restoran', warehouse_id: 2, warehouse_name: 'Dapur', bill_line_id: 71, bill_number: 'R-0009', kind: 'SALE', product_id: 4, product_name: 'Nasi Goreng', quantity: '1', sale_class: 'FOOD', sale_date: '2026-09-30', status: 'FAILED', attempts: 1, error_code: 'ACCOUNT_KEY_MISSING', error_message: 'the account is not set', created_at: '2026-09-30T10:05:00Z', applied_on: null },
  ],
}

const mounted: VueWrapper[] = []

function ok(path: string): unknown {
  if (path.endsWith('/inventory/units')) return { data: { data: units } }
  if (path.endsWith('/inventory/categories')) return { data: { data: categories } }
  if (path.endsWith('/inventory/accounts')) return { data: { data: [{ id: 10, code: '1310', name: 'Food inventory' }, { id: 11, code: '1320', name: 'Beverage inventory' }] } }
  if (path.endsWith('/accounting/accounts')) return { data: { data: [{ id: 20, code: '5210', name: 'Cost of food sales', account_type: 'EXPENSE', is_postable: true, is_active: true }] } }
  if (path.endsWith('/inventory/warehouses')) return { data: { data: warehouses } }
  if (path.endsWith('/inventory/warehouses/{id}/levels')) return { data: { data: [{ item_id: 7, item_code: 'SIRUP', item_name: sirup.name, min_qty: '12', max_qty: null, reorder_qty: null }] } }
  if (path.endsWith('/inventory/items/{id}')) return { data: sirup }
  if (path.endsWith('/inventory/items')) return { data: { items: [sirup, es], total: 2, total_value: '2740000', by_category: { 1: 1, 2: 2 }, costing_method: 'AVERAGE', costing_locked: true, warehouses: [{ id: 1, code: 'UTAMA', name: 'Gudang Utama' }, { id: 3, code: 'BAR', name: 'Gudang Bar' }] } }
  if (path.endsWith('/inventory/stock-card')) return { data: card }
  if (path.endsWith('/inventory/settings')) return { data: { exists: true, costing_method: 'AVERAGE', costing_locked_on: '2026-09-01', price_tolerance_pct: '1', qty_tolerance_pct: '0', over_receipt_pct: '5', po_requires_pr: true, direct_receipt_limit: null, count_blocks_night_audit: true, count_blind: true, version: 3 } }
  if (path.endsWith('/inventory/setup-check')) return { data: { ok: true, costing_method: 'AVERAGE', costing_locked_on: '2026-09-01', warehouses: 2, categories: 2, items: 2, issues: [{ severity: 'WARNING', code: 'NO_ITEM', message: 'there is no item yet' }] } }
  if (path.endsWith('/inventory/consumption-queue')) return { data: queueList }
  if (path.endsWith('/pos/outlets')) return { data: { data: [{ id: 5, code: 'BAR', name: 'Bar' }] } }
  if (path.endsWith('/departments')) return { data: { data: [{ id: 6, code: 'FB', name: 'Food and beverage', level: 1, is_active: true }] } }
  return { data: { data: [] } }
}

async function setup(component: unknown, permissions = ['inventory.view', 'inventory.manage', 'inventory.opening'], route = '/inventory', get?: (path: string) => unknown) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'm@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  usePropertyStore().current = { id: 7, currency_decimals: 0 } as never
  resetDepartments()
  GET = vi.fn(async (path: string) => (get ? get(path) : ok(path)))
  POST = vi.fn().mockResolvedValue({ data: { id: 1, number: 'OPN-000001' } })
  PUT = vi.fn().mockResolvedValue({ data: sirup })
  PATCH = vi.fn().mockResolvedValue({ data: {} })
  await router.push(route)
  await router.isReady()
  const w = mount(component as never, { attachTo: document.body, global: { plugins: [pinia, router] } })
  mounted.push(w)
  await flushPromises()
  return w
}
const body = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)
const has = (sel: string) => document.body.querySelector(sel) !== null
async function type(sel: string, value: string): Promise<void> {
  const el = document.body.querySelector(sel) as HTMLInputElement
  el.value = value
  el.dispatchEvent(new Event('input', { bubbles: true }))
  await flushPromises()
}
async function choose(sel: string, value: string | number): Promise<void> {
  const el = document.body.querySelector(sel) as HTMLSelectElement
  el.value = String(value)
  el.dispatchEvent(new Event('change', { bubbles: true }))
  await flushPromises()
}

beforeEach(() => setLocale('en'))
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
})

describe('items and stock', () => {
  it('lists the items with a column for each warehouse, the markers, and the value of all the stock', async () => {
    await setup(InventoryItemsView)
    const row = body('[data-testid=item-SIRUP]').text()
    expect(row).toContain('Sirup gula aren 650 ml')
    expect(row).toContain('36') // Gudang Utama
    expect(row).toContain('20,2'.replace(',', '.')) // Gudang Bar, English separators
    expect(body('[data-testid=item-ES]').text()).toContain('Below zero')
    expect(body('[data-testid=item-ES]').text()).toContain('-4')
    expect(body('[data-testid=total-value]').text()).toBe('2,740,000')
    expect(body('[data-testid=method-badge]').text()).toContain('Moving average')
    expect(body('[data-testid=method-badge]').text()).toContain('locked')
    expect(document.body.textContent).toContain('Gudang Bar')
  })

  it('opens the panel of an item: its units, its stock per warehouse with the average and the minimum, and a short card', async () => {
    await setup(InventoryItemsView)
    expect(has('[data-testid=item-panel]')).toBe(false)
    await body('[data-testid=item-SIRUP]').trigger('click')
    await flushPromises()
    const units = body('[data-testid=panel-units]').text()
    expect(units).toContain('karton = 12 botol')
    expect(units).toContain('ml = 1 / 650 botol')
    const stock = body('[data-testid=panel-stock]').text()
    expect(stock).toContain('Gudang Bar')
    expect(stock).toContain('average 49,500')
    expect(stock).toContain('Minimum Gudang Bar 12')
    const cardText = body('[data-testid=panel-card]').text()
    expect(cardText).toContain('2 Sep')
    expect(cardText).not.toContain('09-02')
    expect(cardText).toContain('POS sale')
    expect(cardText).toContain('Opening stock OPN-000001')
    await body('[data-testid=panel-close]').trigger('click')
    expect(has('[data-testid=item-panel]')).toBe(false)
  })

  it('asks the server for the search, the category and the minimum', async () => {
    await setup(InventoryItemsView)
    GET.mockClear()
    await body('[data-testid=chip-MINUMAN]').trigger('click')
    await flushPromises()
    const call = GET.mock.calls.find((c) => String(c[0]).endsWith('/inventory/items'))!
    expect(call[1].params.query.category).toBe(2)
    GET.mockClear()
    await body('[data-testid=chip-below-min]').trigger('click')
    await flushPromises()
    expect(GET.mock.calls.find((c) => String(c[0]).endsWith('/inventory/items'))![1].params.query.below_min).toBe(true)
  })

  it('offers the new item and the opening stock only to who may', async () => {
    await setup(InventoryItemsView, ['inventory.view'])
    expect(has('[data-testid=item-new]')).toBe(false)
    expect(has('[data-testid=opening-new]')).toBe(false)
    document.body.innerHTML = ''
    await setup(InventoryItemsView)
    expect(has('[data-testid=item-new]')).toBe(true)
    expect(has('[data-testid=opening-new]')).toBe(true)
  })

  it('speaks Indonesian with its own separators', async () => {
    setLocale('id')
    await setup(InventoryItemsView)
    expect(body('[data-testid=total-value]').text()).toBe('2.740.000')
    expect(document.body.textContent).toContain('Barang & stok')
    expect(body('[data-testid=item-SIRUP]').text()).toContain('20,2')
  })
})

describe('an item', () => {
  it('is added with its unit, its category and the other units with their conversion', async () => {
    await setup(InventoryItemFormView, undefined, '/inventory/items/new')
    await type('input[name=code]', 'beras')
    await type('input[name=name]', 'Beras pandan')
    await choose('select[name=category_id]', 1)
    await choose('select[name=stock_unit_id]', 1)
    await body('[data-testid=unit-add]').trigger('click')
    await flushPromises()
    await choose('select[name=unit_0]', 2) // g
    await type('input[name=num_0]', '1')
    await type('input[name=den_0]', '1000')
    await body('[data-testid=item-form]').trigger('submit')
    await flushPromises()
    const [path, init] = POST.mock.calls[0]!
    expect(path).toBe('/api/v1/properties/{propertyId}/inventory/items')
    expect(init.body.code).toBe('BERAS')
    expect(init.body.stock_unit_id).toBe(1)
    expect(init.body.units).toEqual([
      { unit_id: 1, stock_num: 1, stock_den: 1, is_purchase: true, is_recipe: true, is_default_purchase: true, is_default_recipe: true },
      { unit_id: 2, stock_num: 1, stock_den: 1000, is_purchase: true, is_recipe: false, is_default_purchase: false, is_default_recipe: false },
    ])
  })

  it('keeps the stock unit and the category of an item that has stock, and puts the conversions as they are', async () => {
    await setup(InventoryItemFormView, undefined, '/inventory/items/7')
    expect((document.body.querySelector('select[name=stock_unit_id]') as HTMLSelectElement).disabled).toBe(true)
    expect((document.body.querySelector('select[name=category_id]') as HTMLSelectElement).disabled).toBe(true)
    expect((document.body.querySelector('input[name=code]') as HTMLInputElement).disabled).toBe(true)
    expect(has('[data-testid=unit-stock]')).toBe(true)
    expect(has('[data-testid=unit-row-1]')).toBe(true) // karton and ml
    await body('[data-testid=item-form]').trigger('submit')
    await flushPromises()
    expect(PATCH).toHaveBeenCalledTimes(1)
    expect(PUT.mock.calls[0]![0]).toBe('/api/v1/properties/{propertyId}/inventory/items/{id}/units')
    const units = PUT.mock.calls[0]![1].body.units
    expect(units).toContainEqual({ unit_id: 5, stock_num: 1, stock_den: 650, is_purchase: false, is_recipe: true, is_default_purchase: false, is_default_recipe: true })
  })

  it('shows the refusal of a field under the field', async () => {
    await setup(InventoryItemFormView, undefined, '/inventory/items/new')
    POST.mockRejectedValueOnce(new ApiError({ status: 422, code: 'VALIDATION_FAILED', title: 'invalid', errors: [{ field: 'code', code: 'INVALID_FORMAT', message: '1-30 characters' }] } as never))
    await type('input[name=code]', 'x')
    await type('input[name=name]', 'X')
    await choose('select[name=category_id]', 1)
    await choose('select[name=stock_unit_id]', 1)
    await body('[data-testid=item-form]').trigger('submit')
    await flushPromises()
    expect(has('[data-testid=item-error]')).toBe(true)
  })
})

describe('a warehouse', () => {
  it('names the outlet for a warehouse of an outlet, and keeps the kind of one that has stock', async () => {
    await setup(InventoryWarehouseFormView, undefined, '/inventory/warehouses/3')
    expect((document.body.querySelector('input[name=code]') as HTMLInputElement).value).toBe('BAR')
    expect((document.body.querySelector('select[name=outlet_id]') as HTMLSelectElement).value).toBe('5')
    expect((document.body.querySelector('select[name=kind]') as HTMLSelectElement).disabled).toBe(true)
    expect(has('[data-testid=locked]')).toBe(true)
    expect(has('[data-testid=levels]')).toBe(true)
  })

  it('saves the minimum levels as a whole', async () => {
    await setup(InventoryWarehouseFormView, undefined, '/inventory/warehouses/3')
    await type('input[name=min_0]', '15')
    await body('[data-testid=levels-save]').trigger('click')
    await flushPromises()
    expect(PUT.mock.calls[0]![0]).toBe('/api/v1/properties/{propertyId}/inventory/warehouses/{id}/levels')
    expect(PUT.mock.calls[0]![1].body.levels).toEqual([{ item_id: 7, min_qty: '15', max_qty: null, reorder_qty: null }])
  })

  it('is added as the main store of the property', async () => {
    await setup(InventoryWarehouseFormView, undefined, '/inventory/warehouses/new')
    expect(has('[data-testid=levels]')).toBe(false)
    expect(has('select[name=outlet_id]')).toBe(false)
    await type('input[name=code]', 'utama')
    await type('input[name=name]', 'Gudang Utama')
    await body('[data-testid=warehouse-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]![1].body).toMatchObject({ code: 'UTAMA', kind: 'MAIN', outlet_id: null, cost_class: 'OTHER' })
  })
})

describe('a category', () => {
  it('chooses its inventory account among the inventory accounts and the cost accounts among the expenses', async () => {
    await setup(InventoryCategoryFormView, undefined, '/inventory/categories/new')
    const inv = Array.from((document.body.querySelector('select[name=inventory_account_id]') as HTMLSelectElement).options).map((o) => o.textContent)
    expect(inv.join('|')).toContain('1310 · Food inventory')
    const cogs = Array.from((document.body.querySelector('select[name=cogs_account_id]') as HTMLSelectElement).options).map((o) => o.textContent)
    expect(cogs.join('|')).toContain('5210 · Cost of food sales')
    await type('input[name=code]', 'lain')
    await type('input[name=name]', 'Lain-lain')
    await body('[data-testid=category-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]![1].body).toMatchObject({ code: 'LAIN', kind: 'FOOD', inventory_account_id: 10, cogs_account_id: null })
  })

  it('keeps the account of a category whose items have moved', async () => {
    await setup(InventoryCategoryFormView, undefined, '/inventory/categories/1')
    expect(has('[data-testid=locked]')).toBe(true)
    expect((document.body.querySelector('select[name=inventory_account_id]') as HTMLSelectElement).disabled).toBe(true)
  })
})

describe('the opening stock', () => {
  async function fill(): Promise<void> {
    await body('[data-testid=line-add]').trigger('click')
    await flushPromises()
  }

  it('works out the value of a line from the unit it is counted in and posts the document once', async () => {
    await setup(InventoryOpeningView, undefined, '/inventory/openings/new')
    expect(body('[data-testid=opening-post]').attributes('disabled')).toBeDefined()
    await fill()
    await type('input[name=qty_0]', '36')
    await type('input[name=cost_0]', '49000')
    expect(body('[data-testid=line-value-0]').text()).toBe('1,764,000')
    expect(body('[data-testid=opening-total]').text()).toBe('1,764,000')
    expect(body('[data-testid=opening-post]').attributes('disabled')).toBeUndefined()
    await body('[data-testid=opening-form]').trigger('submit')
    await flushPromises()
    const init = POST.mock.calls[0]![1]
    expect(init.body.warehouse_id).toBe(1)
    expect(init.body.lines).toEqual([{ item_id: 7, unit_id: 3, qty: '36', unit_cost: '49000' }])
    expect(init.body.client_uuid).toMatch(/^[0-9a-f-]{36}$/)
  })

  it('shows the quantity in stock units when the line is counted in another unit', async () => {
    await setup(InventoryOpeningView, undefined, '/inventory/openings/new')
    await fill()
    await choose('select[name=unit_0]', 4) // karton
    await type('input[name=qty_0]', '5')
    await type('input[name=cost_0]', '600000')
    expect(body('[data-testid=line-0]').text()).toContain('In stock units: 60 botol')
    expect(body('[data-testid=line-value-0]').text()).toBe('3,000,000')
  })

  it('refuses a quantity of zero, and the cost field takes the digits and separators of the currency', async () => {
    await setup(InventoryOpeningView, undefined, '/inventory/openings/new')
    await fill()
    await type('input[name=qty_0]', '0')
    await type('input[name=cost_0]', '10')
    expect(body('[data-testid=opening-post]').attributes('disabled')).toBeDefined()
    await type('input[name=qty_0]', '1')
    expect(body('[data-testid=opening-post]').attributes('disabled')).toBeUndefined()
    await type('input[name=cost_0]', '1234567')
    expect((body('input[name=cost_0]').element as HTMLInputElement).value).toBe('1,234,567') // grouped as typed; IDR has no decimals
  })

  it('shows a posted opening as a read only document', async () => {
    await setup(InventoryOpeningView, undefined, '/inventory/openings/1')
    expect(has('[data-testid=opening-form]')).toBe(false)
  })
})

describe('costing', () => {
  it('shows the four steps, the lock of the method with its date, and the setup check', async () => {
    await setup(InventoryCostingView)
    expect(has('[data-testid=steps]')).toBe(true)
    expect(body('[data-testid=locked]').text()).toContain('Locked since the first stock movement')
    expect((document.body.querySelector('[data-testid=method-FIFO]') as HTMLInputElement).disabled).toBe(true)
    expect(has('[data-testid=method-save]')).toBe(false)
    expect(body('[data-testid=issue-NO_ITEM]').text()).toContain('There is no item yet.') // the sentence of the language of the page, by the code
  })

  it('writes a finding in Indonesian by its code and its params, not with the English sentence of the server', async () => {
    setLocale('id')
    await setup(InventoryCostingView, undefined, '/inventory', (path) => (path.endsWith('/inventory/setup-check')
      ? { data: { ok: false, costing_method: 'AVERAGE', costing_locked_on: null, warehouses: 1, categories: 1, items: 1, issues: [{ severity: 'ERROR', code: 'INVENTORY_QUEUE_FAILED', message: '2 POS sale(s) could not be deducted', params: { n: '2' } }] } }
      : ok(path)))
    const text = body('[data-testid=issue-INVENTORY_QUEUE_FAILED]').text()
    expect(text).toContain('2 penjualan POS gagal dipotong dari stok')
    expect(text).not.toContain('could not be deducted')
  })

  it('lists what the POS queue holds and lets the manager retry a failed row or cancel it', async () => {
    await setup(InventoryCostingView)
    expect(body('[data-testid=queue-summary]').text()).toContain('Failed 1')
    expect(body('[data-testid=queue-row-9]').text()).toContain('Nasi Goreng')
    expect(body('[data-testid=queue-row-9]').text()).toContain('An account that this needs is not set in this property.')
    expect(has('[data-testid=queue-retry-8]')).toBe(false) // a held row is not tried again by hand, only cancelled
    expect(has('[data-testid=queue-cancel-8]')).toBe(true)
    await click('[data-testid=queue-retry-9]')
    expect(POST.mock.calls[0]![0]).toContain('/consumption-queue/{id}/retry')
    expect(POST.mock.calls[0]![1].params.path.id).toBe(9)
    await click('[data-testid=queue-cancel-9]')
    expect(POST.mock.calls[1]![0]).toContain('/consumption-queue/{id}/cancel')
  })

  it('shows the stock card of an item in a warehouse with its running balance', async () => {
    await setup(InventoryCostingView, undefined, '/inventory/costing?warehouse=3&item=7')
    const table = body('[data-testid=card-table]').text()
    expect(table).toContain('Opening stock OPN-000001')
    expect(table).toContain('POS sale')
    expect(table).toContain('980,000')
    expect(table).toContain('882,000')
    expect(has('[data-testid=layers]')).toBe(false) // average: no layers
  })

  it('writes the cost moved to waste without a quantity or a value, since the balance does not change', async () => {
    const wasteCard = { ...card, lines: [...card.lines, { movement_id: 3, business_date: '2026-09-02', kind: 'COGS_TO_WASTE', source_type: 'POS_QUEUE', qty: '0', value: '0', unit_cost: '0', cost_estimated: false, balance_qty: '18', balance_value: '882000', average_cost: '49000' }] }
    await setup(InventoryCostingView, undefined, '/inventory/costing?warehouse=3&item=7', (path) => (path.endsWith('/inventory/stock-card') ? { data: wasteCard } : ok(path)))
    const row = body('[data-testid=move-3]')
    const cells = row.findAll('td').map((c) => c.text())
    expect(cells[2]).toBe('–')
    expect(cells[3]).toBe('–')
    expect(body('[data-testid=cost-to-waste]').text()).toBe('from cost of sales to waste expense')
    expect(cells[4]).toBe('18') // the balance stays
  })

  it('lets the manager choose the method while it is free', async () => {
    GET = vi.fn()
    await setup(InventoryCostingView)
    // a property with no movement yet
    document.body.innerHTML = ''
    const free = ok
    GET = vi.fn(async (path: string) => (path.endsWith('/inventory/settings') ? { data: { exists: true, costing_method: 'AVERAGE', costing_locked_on: null, price_tolerance_pct: '1', qty_tolerance_pct: '0', over_receipt_pct: '5', po_requires_pr: true, direct_receipt_limit: null, count_blocks_night_audit: true, count_blind: true, version: 1 } } : free(path)))
    const pinia = createPinia()
    setActivePinia(pinia)
    useAuthStore().me = { user: { id: 5, email: 'm@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: ['inventory.manage'] }] } as never
    usePropertyStore().currentId = 7
    PUT = vi.fn().mockResolvedValue({ data: { costing_method: 'FIFO', costing_locked_on: null, version: 2 } })
    const w = mount(InventoryCostingView as never, { attachTo: document.body, global: { plugins: [pinia, router] } })
    mounted.push(w)
    await flushPromises()
    await choose('[data-testid=method-FIFO]', 'FIFO')
    const radio = document.body.querySelector('[data-testid=method-FIFO]') as HTMLInputElement
    radio.checked = true
    radio.dispatchEvent(new Event('change', { bubbles: true }))
    await flushPromises()
    await body('[data-testid=method-save]').trigger('click')
    await flushPromises()
    expect(PUT.mock.calls[0]![1].body.costing_method).toBe('FIFO')
    expect(PUT.mock.calls[0]![1].body.version).toBe(1)
  })
})

describe('the lists of the masters', () => {
  it.each([
    [InventoryCategoriesView, 'category-', '/inventory/categories', categories[0]!],
    [InventoryWarehousesView, 'warehouse-', '/inventory/warehouses', warehouses[0]!],
    [InventoryUnitsView, 'unit-', '/inventory/units', units[0]!],
  ])('open the page of a row when it is clicked and have a link in the code for the keyboard', async (view, prefix, path, first) => {
    await setup(view, undefined, path)
    const row = body(`[data-testid=${prefix}${first.code}]`)
    expect(row.find('a').exists()).toBe(true)
    await row.find('td:nth-child(2)').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe(`${path}/${first.id}`)
  })

  it('leave the page closed to who cannot manage the masters', async () => {
    await setup(InventoryCategoriesView, ['inventory.view'], '/inventory/categories')
    expect(body(`[data-testid=category-${categories[0]!.code}]`).find('a').exists()).toBe(false)
    await body(`[data-testid=category-${categories[0]!.code}]`).find('td:nth-child(2)').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/inventory/categories')
  })
})
