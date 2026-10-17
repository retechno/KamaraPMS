import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { resetDepartments } from '@/composables/useDepartments'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { findingText } from '@/utils/findings'
import InventoryPosGapView from '@/views/inventory/InventoryPosGapView.vue'
import PosMenuSetupView from '@/views/pos/PosMenuSetupView.vue'
import PosOutletsView from '@/views/pos/PosOutletsView.vue'
import ProductRecipe from './ProductRecipe.vue'

let GET = vi.fn()
let PUT = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), PUT: (...a: unknown[]) => PUT(...a), POST: (...a: unknown[]) => POST(...a) } }))

const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
const mounted: VueWrapper[] = []

const line = (id: number, item_id: number, code: string, name: string, unit_id: number, unit: string, qty: string, stockQty: string, unitCost: string, cost: string, extra: object = {}) => ({
  id, item_id, item_code: code, item_name: name, unit_id, unit_code: unit, quantity: qty, note: null, stock_unit_code: unit === 'g' ? 'kg' : unit, stock_qty: stockQty, unit_cost: unitCost, cost_basis: 'AVERAGE', cost,
  replaced_qty: '0', replaced_cost: '0', replaces_label: null, ...extra,
})
const items = [
  { id: 1, code: 'BERAS', name: 'Beras', stock_unit_id: 10, stock_unit_code: 'kg', units: [{ unit_id: 10, unit_code: 'kg', stock_num: 1, stock_den: 1, is_recipe: false, is_default_recipe: false }, { unit_id: 11, unit_code: 'g', stock_num: 1, stock_den: 1000, is_recipe: true, is_default_recipe: true }], stock: [] },
  { id: 2, code: 'TELUR', name: 'Telur', stock_unit_id: 12, stock_unit_code: 'pcs', units: [{ unit_id: 12, unit_code: 'pcs', stock_num: 1, stock_den: 1, is_recipe: true, is_default_recipe: true }], stock: [] },
]
const recipe = (over: object = {}) => ({
  product_id: 10, product_code: 'NASGOR', product_name: 'Nasi Goreng', inventory_active: true, editable: true, outlet_id: 1, outlet_name: 'Restoran', warehouse_id: 3, warehouse_name: 'Dapur', sale_class: 'FOOD',
  outlets: [{ id: 1, code: 'R', name: 'Restoran', warehouse_id: 3, warehouse_name: 'Dapur' }, { id: 2, code: 'B', name: 'Bar', warehouse_id: null, warehouse_name: null }],
  base: [line(1, 1, 'BERAS', 'Beras', 11, 'g', '200', '0.2', '17360', '3472')],
  options: [
    { option_id: 20, name: 'Jumbo', group_id: 5, group_name: 'Porsi', kind: 'VARIANT', is_active: true, price_delta: '20000', extra_cost: '1736', lines: [line(2, 1, 'BERAS', 'Beras', 11, 'g', '300', '0.3', '17360', '5208', { replaced_qty: '200', replaced_cost: '3472', replaces_label: 'Beras' })] },
    { option_id: 21, name: 'Telur ceplok', group_id: 6, group_name: 'Tambahan', kind: 'MODIFIER', is_active: true, price_delta: '8000', extra_cost: '2727', lines: [line(3, 2, 'TELUR', 'Telur', 12, 'pcs', '1', '1', '2500', '2500')] },
    { option_id: 22, name: 'Level pedas', group_id: 5, group_name: 'Porsi', kind: 'VARIANT', is_active: true, price_delta: '0', extra_cost: '0', lines: [] },
  ],
  base_cost: '3472', net_price: '85000', margin: '81528', food_cost_pct: '4.1', cost_basis_label: 'AVERAGE', currency_decimals: 0, ...over,
})

async function setup(component: unknown, permissions: string[], get: (path: string) => unknown, props: Record<string, unknown> = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'm@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  usePropertyStore().clock = { business_date: '2026-09-30' } as never
  resetDepartments()
  GET = vi.fn(async (path: string) => get(path))
  PUT = vi.fn().mockResolvedValue({ data: {} })
  POST = vi.fn().mockResolvedValue({ data: {} })
  await router.push('/')
  const w = mount(component as never, { attachTo: document.body, props: props as never, global: { plugins: [pinia, router] } })
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
async function type(sel: string, value: string) {
  const el = document.body.querySelector(sel) as HTMLInputElement
  el.value = value
  el.dispatchEvent(new Event('input', { bubbles: true }))
  await flushPromises()
}
const recipeApi = (r = recipe()) => (path: string) => {
  if (path.endsWith('/recipe')) return { data: r }
  if (path.endsWith('/inventory/items')) return { data: { items } }
  return { data: { data: [] } }
}

beforeEach(() => setLocale('en'))
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
})

describe('the recipe of a product', () => {
  it('shows the base recipe with the cost of each line, the variants that replace, the add-ons, the cost, the margin and the food cost %', async () => {
    await setup(ProductRecipe, ['pos.manage', 'inventory.view'], recipeApi(), { productId: 10 })
    expect(body('[data-testid=recipe-line-BERAS]').text()).toContain('3,472')
    expect(body('[data-testid=recipe-option-20]').text()).toContain('Variant')
    expect(body('[data-testid=recipe-option-20]').text()).toContain('replaces 200 g of Beras')
    expect(body('[data-testid=recipe-extra-20]').text()).toContain('+1,736')
    expect(body('[data-testid=recipe-option-21]').text()).toContain('Add-on')
    expect(body('[data-testid=recipe-option-22]').text()).toContain('No stock ingredient')
    expect(body('[data-testid=recipe-cost]').text()).toBe('3,472')
    expect(body('[data-testid=recipe-price]').text()).toBe('85,000')
    expect(body('[data-testid=recipe-margin]').text()).toBe('81,528')
    expect(body('[data-testid=recipe-foodcost]').text()).toBe('4.1%')
    expect((body('[data-testid=recipe-outlet]').element as HTMLSelectElement).textContent).toContain('Dapur')
  })

  it('saves the whole recipe, base and options, with the units of the items, and shows the cost of a new line only once it is saved', async () => {
    await setup(ProductRecipe, ['pos.manage', 'inventory.view'], recipeApi(), { productId: 10 })
    expect((body('[data-testid=recipe-save]').element as HTMLButtonElement).disabled).toBe(true) // nothing changed
    await click('[data-testid=recipe-add-base]')
    await type('[name=quantity_3]', '1') // the new rows come after the three that were saved
    expect(body('[data-testid=recipe-cost]').text()).toBe('—') // the figures are the server's: they wait for the save
    await click('[data-testid=recipe-add-option-22]')
    await type('[name=quantity_4]', '5')
    await click('[data-testid=recipe-save]')
    expect(PUT).toHaveBeenCalledTimes(1)
    const sent = PUT.mock.calls[0]![1].body.lines
    expect(sent).toEqual([
      { option_id: null, item_id: 1, unit_id: 11, quantity: '200' },
      { option_id: 20, item_id: 1, unit_id: 11, quantity: '300' },
      { option_id: 21, item_id: 2, unit_id: 12, quantity: '1' },
      { option_id: null, item_id: 2, unit_id: 12, quantity: '1' },
      { option_id: 22, item_id: 1, unit_id: 11, quantity: '5' },
    ])
    expect(body('[data-testid=recipe-notice]').text()).toContain('saved')
  })

  it('refuses a quantity that is not above zero before it reaches the server', async () => {
    await setup(ProductRecipe, ['pos.manage', 'inventory.view'], recipeApi(), { productId: 10 })
    await type('[name=quantity_0]', '0')
    expect((body('[data-testid=recipe-save]').element as HTMLButtonElement).disabled).toBe(true)
  })

  it('is read only when the module is not in the plan, and says why', async () => {
    await setup(ProductRecipe, ['pos.manage'], recipeApi(recipe({ inventory_active: false, editable: false })), { productId: 10 })
    expect(has('[data-testid=recipe-readonly]')).toBe(true)
    expect(has('[data-testid=recipe-save]')).toBe(false)
    expect(has('[data-testid=recipe-add-base]')).toBe(false)
    expect(body('[data-testid=recipe-line-BERAS]').text()).toContain('Beras')
  })

  it('reads the cost at the warehouse of another outlet, and warns of an outlet with no warehouse', async () => {
    const w = await setup(ProductRecipe, ['pos.manage', 'inventory.view'], (path) => (path.endsWith('/recipe') ? { data: recipe() } : recipeApi()(path)), { productId: 10 })
    const select = document.body.querySelector('[data-testid=recipe-outlet]') as HTMLSelectElement
    select.value = '2'
    select.dispatchEvent(new Event('change', { bubbles: true }))
    await flushPromises()
    const call = GET.mock.calls.filter((c) => String(c[0]).endsWith('/recipe')).at(-1)!
    expect(call[1].params.query).toEqual({ outlet_id: 2 })
    w.unmount()
    await setup(ProductRecipe, ['pos.manage', 'inventory.view'], recipeApi(recipe({ warehouse_id: null, warehouse_name: null, cost_basis_label: 'REFERENCE' })), { productId: 10 })
    expect(has('[data-testid=recipe-no-warehouse]')).toBe(true)
  })
})

describe('the stock settings of an outlet and of a category', () => {
  const outlet = {
    id: 1, code: 'RESTO', name: 'Restoran Teratai', outlet_type: 'RESTAURANT', order_mode: 'PRINT', department_id: 6, department_name: 'Restaurant', rule_profile_id: 3, rule_profile_name: 'F&B', price_list_id: 4, price_list_name: 'Main',
    drawer: 'LACI1', receipt_printer_id: null, receipt_printer_name: null, uses_tables: true, guest_count_required: false, rounding_unit: 0, allow_room_charge: true, allow_company_charge: false, allow_comp: true,
    comp_treatment: 'PAYMENT_COMP', max_discount_pct_no_approval: '0', block_night_audit_with_open_bills: false, is_active: true, sort_order: 1, version: 2, warehouse_id: 3, warehouse_name: 'Dapur', void_waste_after_minutes: 0, receipt_copies: 1,
  }
  const api = (path: string) => {
    if (path.endsWith('/departments')) return { data: { data: [{ id: 6, code: 'REST', name: 'Restaurant', level: 2, is_active: true }] } }
    if (path.endsWith('/pos/outlets')) return { data: { data: [outlet] } }
    if (path.endsWith('/pos/rule-profiles')) return { data: { data: [{ id: 3, code: 'FB', name: 'F&B', price_mode: 'EXCLUSIVE', is_active: true, rules: [] }] } }
    if (path.endsWith('/pos/price-lists')) return { data: { data: [{ id: 4, code: 'MAIN', name: 'Main', is_active: true, item_count: 1 }] } }
    if (path.endsWith('/inventory/warehouses')) return { data: { data: [{ id: 3, code: 'DAPUR', name: 'Dapur', kind: 'OUTLET', is_active: true }, { id: 4, code: 'BAR', name: 'Bar', kind: 'OUTLET', is_active: true }] } }
    if (path.endsWith('/pos/categories')) return { data: { data: [{ id: 1, code: 'MAINS', name: 'Mains', parent_id: null, sort_order: 1, is_active: true, product_count: 2, fb_class: 'FOOD' }] } }
    return { data: { data: [] } }
  }

  it('chooses the warehouse of an outlet and the minutes after which a void is waste, only for an outlet that prints tickets', async () => {
    await setup(PosOutletsView, ['pos.manage', 'inventory.view'], api)
    await click('[data-testid=outlet-RESTO] button, [data-testid=outlet-edit-RESTO]')
    expect(has('[data-testid=outlet-stock]')).toBe(true)
    expect((body('[data-testid=outlet-warehouse]').element as HTMLSelectElement).value).toBe('3')
    await type('[data-testid=outlet-void-waste]', '20')
    const select = document.body.querySelector('[data-testid=outlet-warehouse]') as HTMLSelectElement
    select.value = '4'
    select.dispatchEvent(new Event('change', { bubbles: true }))
    await flushPromises()
    await click('[data-testid=outlet-save]')
    expect(PUT.mock.calls[0]![1].body).toMatchObject({ warehouse_id: 4, void_waste_after_minutes: 20 })
  })

  it('leaves the stock part out for a person who may not read the stock', async () => {
    await setup(PosOutletsView, ['pos.manage'], (path) => {
      if (path.endsWith('/inventory/warehouses')) throw new Error('403')
      return api(path)
    })
    await click('[data-testid=outlet-RESTO] button, [data-testid=outlet-edit-RESTO]')
    // the outlet has a warehouse already, so the field stays to show it
    expect(has('[data-testid=outlet-stock]')).toBe(true)
  })

  it('gives a category its sale class', async () => {
    await setup(PosMenuSetupView, ['pos.manage'], api)
    await click('[data-testid=category-MAINS] button')
    expect((body('[data-testid=category-fb-class]').element as HTMLSelectElement).value).toBe('FOOD')
    const select = document.body.querySelector('[data-testid=category-fb-class]') as HTMLSelectElement
    select.value = 'BEVERAGE'
    select.dispatchEvent(new Event('change', { bubbles: true }))
    await flushPromises()
    await click('[data-testid=category-save]')
    expect(PUT.mock.calls[0]![1].body).toMatchObject({ fb_class: 'BEVERAGE' })
  })
})

describe('the report of the POS sales without a deduction', () => {
  it('lists the lines with the date, the outlet and the bill, and says so when there are none', async () => {
    const rows = [{ bill_line_id: 71, bill_number: 'R-0009', outlet_id: 1, outlet_code: 'R', outlet_name: 'Restoran', product_id: 4, product_name: 'Nasi Goreng', quantity: '2', sent_on: '2026-09-20', voided: false }]
    await setup(InventoryPosGapView, ['inventory.view'], (path) => (path.endsWith('/pos-without-deduction') ? { data: { data: rows } } : { data: { data: [] } }))
    expect(body('[data-testid=gap-row-71]').text()).toContain('Nasi Goreng')
    expect(GET.mock.calls.find((c) => String(c[0]).endsWith('/pos-without-deduction'))![1].params.query).toMatchObject({ from: '2026-09-01', to: '2026-09-30' })
    await setup(InventoryPosGapView, ['inventory.view'], () => ({ data: { data: [] } }))
    expect(has('[data-testid=gap-empty]')).toBe(true)
  })
})

describe('the findings of the audit', () => {
  it('are written in the language of the page by their code and params, with the numbers in its separators', () => {
    const f = { code: 'INVENTORY_BALANCE_DRIFT', message: 'english', params: { part: 'balance', item: '5', warehouse: '2', qty: '10', value: '170000', ledger_qty: '9', ledger_value: '153000' } }
    expect(findingText(f)).toContain('holds 10 (value 170,000) but the movements say 9 (value 153,000)')
    setLocale('id')
    expect(findingText({ code: 'POS_OPEN_BILLS', message: 'english', params: { n: '4', outlet: 'Bar Kolam' } }, 'warning')).toBe('4 bill Bar Kolam masih terbuka dengan item terjual: penjualannya jadi pendapatan hari ini, pembayarannya belum diterima.')
    expect(findingText({ code: 'DEPARTMENT_HEAD_INACTIVE', message: 'english', params: { name: 'Dewi', role: 'head', department: 'FB' } })).toContain('adalah kepala departemen FB')
    expect(findingText({ code: 'AMENITY_PLAN_INVALID', message: 'english', params: { cause: 'ITEM_INACTIVE', item: 'SAMPO' } })).toBe('Paket amenities tidak bisa jalan malam ini: barang SAMPO nonaktif')
    // a warning reads differently from a blocker where the text says so
    const open = { code: 'INVENTORY_COUNT_OPEN', message: 'english', params: { number: 'CNT-1', warehouse: 'Bar' } }
    expect(findingText(open, 'blocker')).toContain('Setujui atau batalkan')
    expect(findingText(open, 'warning')).toContain('menunggu sampai opname selesai')
    // a code that this release does not know shows the sentence of the server
    expect(findingText({ code: 'SOMETHING_NEW', message: 'what the server said' })).toBe('what the server said')
  })

  it('have a text in both languages for every code the backend sends', async () => {
    const { en } = await import('@/i18n/locales/en')
    const { id } = await import('@/i18n/locales/id')
    const codes = ['INVENTORY_BALANCE_DRIFT', 'INVENTORY_GL_MISMATCH', 'INVENTORY_NEGATIVE_STOCK', 'INVENTORY_RECEIPT_UNBILLED', 'INVENTORY_QUEUE_FAILED', 'INVENTORY_COUNT_OPEN', 'AMENITY_PLAN_INVALID', 'AMENITY_HELD', 'AMENITY_STOCK_SHORT',
      'NO_CATEGORY', 'NO_WAREHOUSE', 'NO_ITEM', 'ACCOUNT_MAP_INCOMPLETE', 'DEPARTMENT_HEAD_INACTIVE', 'APPROVAL_CHAIN_EMPTY', 'POS_OPEN_BILLS', 'POS_DRAFT_LINES']
    for (const [name, tree] of [['en', en], ['id', id]] as const) {
      const f = tree.findings as Record<string, unknown>
      expect(codes.filter((c) => typeof f[c] !== 'string'), name).toEqual([])
    }
  })
})
