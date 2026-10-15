import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import PosFloorPlanView from './PosFloorPlanView.vue'
import PosProductsView from './PosProductsView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PUT = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PUT: (...a: unknown[]) => PUT(...a) } }))

const outlets = [
  { id: 1, code: 'RESTO', name: 'Restoran Teratai', price_list_id: 4, is_active: true, rule_profile_id: 3 },
  { id: 2, code: 'RS', name: 'Room service', price_list_id: 5, is_active: true, rule_profile_id: 3 },
  { id: 3, code: 'BAR', name: 'Bar Kolam', price_list_id: 4, is_active: true, rule_profile_id: 3 },
]
const product = {
  id: 10, code: 'NASGOR', name: 'Nasi Goreng Kampung', short_name: 'Nasgor', category_id: 1, category_name: 'Main dishes', charge_code_id: 20, charge_code: 'FOOD', charge_code_name: 'Food',
  rule_profile_id: null, station_id: 2, is_active: true, version: 4,
  option_groups: [
    { id: 1, name: 'Portion', kind: 'VARIANT', min_select: 1, max_select: 1, sort_order: 0, options: [{ id: 1, name: 'Regular', price_delta: '0', is_default: true, max_qty: 1, sort_order: 0, is_active: true }, { id: 2, name: 'Jumbo', price_delta: '20000.000', is_default: false, max_qty: 1, sort_order: 1, is_active: true }] },
    { id: 2, name: 'Extras', kind: 'MODIFIER', min_select: 0, max_select: 3, sort_order: 1, options: [{ id: 3, name: 'Fried egg', price_delta: '8000.000', is_default: false, max_qty: 2, sort_order: 0, is_active: true }] },
  ],
  prices: [{ price_list_id: 4, product_id: 10, price: '85000.000', is_sold: true }, { price_list_id: 5, product_id: 10, price: '95000.000', is_sold: true }],
}

const mounted: VueWrapper[] = []
function setup(component: unknown, get: (path: string) => unknown) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'm@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: ['pos.manage'] }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.current = { currency_decimals: 0 } as never
  GET = vi.fn(async (path: string) => get(path))
  POST = vi.fn().mockResolvedValue({ data: {} })
  PUT = vi.fn().mockResolvedValue({ data: {} })
  const w = mount(component as never, { attachTo: document.body, global: { plugins: [pinia] } })
  mounted.push(w)
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

beforeEach(() => setLocale('en'))
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
})

const productsGet = (path: string) => {
  if (path.endsWith('/pos/products/{id}')) return { data: product }
  if (path.endsWith('/pos/products')) return { data: { data: [{ ...product, option_groups: undefined, prices: undefined }] } }
  if (path.endsWith('/pos/categories')) return { data: { data: [{ id: 1, code: 'MAINS', name: 'Main dishes', parent_id: null, sort_order: 1, is_active: true, product_count: 1 }] } }
  if (path.endsWith('/pos/outlets')) return { data: { data: outlets } }
  if (path.endsWith('/pos/stations')) return { data: { data: [{ id: 2, code: 'KITCHEN', name: 'Main kitchen', is_active: true }] } }
  if (path.endsWith('/pos/rule-profiles')) return { data: { data: [{ id: 3, code: 'FB', name: 'F&B', price_mode: 'EXCLUSIVE', is_active: true, rules: [{ name: 'Service', rate: '10' }, { name: 'PB1', rate: '10' }] }] } }
  if (path.endsWith('/charge-codes')) return { data: { data: [{ id: 20, code: 'FOOD', name: 'Food', charge_type: 'FOOD_BEVERAGE', is_active: true }, { id: 21, code: 'ROOM', name: 'Room', charge_type: 'ROOM', is_active: true }] } }
  return { data: { data: [] } }
}

describe('the product master', () => {
  it('lists the products by category and opens one with its prices per outlet, variants, add-ons and tax', async () => {
    setup(PosProductsView, productsGet)
    await flushPromises()
    expect(body('[data-testid=product-list]').text()).toContain('Main dishes')
    expect(GET.mock.calls.find((c) => String(c[0]).endsWith('/pos/products'))?.[1]).toMatchObject({ params: { query: { outlet_id: 1 } } })
    await body('[data-testid=product-NASGOR]').trigger('click')
    await flushPromises()
    expect((document.body.querySelector('input[name=name]') as HTMLInputElement).value).toBe('Nasi Goreng Kampung')
    expect((document.body.querySelector('input[name=code]') as HTMLInputElement).disabled).toBe(true)
    // the price of each outlet, and a shared price list is shown once
    expect((document.body.querySelector('input[name=price_1]') as HTMLInputElement).value).toBe('85,000')
    expect((document.body.querySelector('input[name=price_2]') as HTMLInputElement).value).toBe('95,000')
    expect((document.body.querySelector('input[name=price_3]') as HTMLInputElement).disabled).toBe(true)
    expect(body('[data-testid=price-row-3]').text()).toContain('same price list')
    expect(body('[data-testid=product-variants] [data-testid=group-0]').text()).toContain('Variant: pick one')
    expect(body('[data-testid=product-modifiers] [data-testid=group-1]').text()).toContain('Add-ons')
    expect((document.body.querySelector('input[name=option_name_0_1]') as HTMLInputElement).value).toBe('Jumbo')
    expect((document.body.querySelector('input[name=option_price_0_1]') as HTMLInputElement).value).toBe('20,000')
    expect(body('[data-testid=tax-follow]').element).toBeTruthy()
    expect(body('[data-testid=product-tax]').text()).toContain('Service 10%, PB1 10%')
    // the room code is not offered
    expect(document.body.querySelector('select[name=charge_code_id]')?.textContent).not.toContain('ROOM')
  })

  it('saves the whole form with its version, the option groups and one price for each price list', async () => {
    setup(PosProductsView, productsGet)
    await flushPromises()
    await body('[data-testid=product-NASGOR]').trigger('click')
    await flushPromises()
    await type('input[name=price_1]', '90000')
    await body('[data-testid=tax-own]').setValue(true)
    ;(document.body.querySelector('select[name=rule_profile_id]') as HTMLSelectElement).value = '3'
    document.body.querySelector('select[name=rule_profile_id]')!.dispatchEvent(new Event('change', { bubbles: true }))
    await flushPromises()
    await body('[data-testid=product-save]').trigger('submit')
    await flushPromises()
    const [path, opts] = PUT.mock.calls[0] as [string, { params: { path: { id: number } }; body: Record<string, unknown> & { prices: unknown[]; option_groups: { kind: string; min_select: number; options: unknown[] }[] } }]
    expect(path).toBe('/api/v1/properties/{propertyId}/pos/products/{id}')
    expect(opts.params.path.id).toBe(10)
    expect(opts.body).toMatchObject({ version: 4, name: 'Nasi Goreng Kampung', charge_code_id: 20, station_id: 2, rule_profile_id: 3 })
    expect(opts.body.prices).toEqual([{ price_list_id: 4, price: '90000', is_sold: true }, { price_list_id: 5, price: '95000', is_sold: true }])
    expect(opts.body.option_groups.map((g) => g.kind)).toEqual(['VARIANT', 'MODIFIER'])
    expect(opts.body.option_groups[0]).toMatchObject({ min_select: 1, max_select: 1 })
    expect(opts.body.option_groups[0]!.options).toHaveLength(2)
  })

  it('shows what the server refuses in the option groups', async () => {
    setup(PosProductsView, productsGet)
    await flushPromises()
    await body('[data-testid=product-NASGOR]').trigger('click')
    await flushPromises()
    PUT.mockRejectedValue(new ApiError({ type: 'about:blank', title: 'x', status: 422, code: 'VALIDATION_FAILED', detail: 'invalid',
      errors: [{ field: 'option_groups[1].options[0].price_delta', code: 'INVALID_VALUE', message: 'an add-on is priced at zero or more' }] }))
    await body('[data-testid=product-save]').trigger('submit')
    await flushPromises()
    expect(body('[data-testid=group-1]').text()).toContain('an add-on is priced at zero or more')
  })

  it('creates a product with a code, and adds a variant group and an add-on group', async () => {
    setup(PosProductsView, productsGet)
    await flushPromises()
    await body('[data-testid=product-new]').trigger('click')
    await flushPromises()
    expect(body('[data-testid=product-save]').attributes('disabled')).toBeDefined()
    await type('input[name=code]', 'SOTO')
    await type('input[name=name]', 'Soto Ayam')
    ;(document.body.querySelector('select[name=charge_code_id]') as HTMLSelectElement).value = '20'
    document.body.querySelector('select[name=charge_code_id]')!.dispatchEvent(new Event('change', { bubbles: true }))
    await body('[data-testid=add-variant]').trigger('click')
    await body('[data-testid=add-modifier]').trigger('click')
    await flushPromises()
    expect(has('[data-testid=group-0]') && has('[data-testid=group-1]')).toBe(true)
    await body('[data-testid=product-save]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/pos/products')
    expect((POST.mock.calls[0] as [string, { body: Record<string, unknown> }])[1].body).toMatchObject({ code: 'SOTO', name: 'Soto Ayam', charge_code_id: 20, category_id: 1 })
  })

  it('checks a CSV first and lists the rows that are not accepted', async () => {
    setup(PosProductsView, productsGet)
    await flushPromises()
    await body('[data-testid=csv-open]').trigger('click')
    await flushPromises()
    await type('[data-testid=csv-text]', 'code,name,category,charge_code\nA,Nasi,NOPE,FOOD\n')
    POST.mockResolvedValue({ data: { dry_run: true, rows: 1, created: 0, updated: 0, errors: [{ row: 2, field: 'category', code: 'NOT_FOUND', detail: 'no such active category: NOPE' }] } })
    await body('[data-testid=csv-check]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1]).toMatchObject({ params: { query: { dry_run: true } }, body: { csv: 'code,name,category,charge_code\nA,Nasi,NOPE,FOOD\n' } })
    expect(body('[data-testid=csv-result]').text()).toContain('1 rows are not accepted')
    expect(body('[data-testid=csv-result]').text()).toContain('Row 2: category')
  })
})

describe('the floor plan editor', () => {
  const area = { id: 8, outlet_id: 1, name: 'Indoors', sort_order: 1, grid_w: 40, grid_h: 24, layout_version: 3, is_active: true,
    tables: [{ id: 1, area_id: 8, code: '1', capacity: 2, shape: 'SQUARE', x: 2, y: 2, w: 4, h: 4, is_active: true }, { id: 2, area_id: 8, code: '2', capacity: 4, shape: 'ROUND', x: 8, y: 2, w: 5, h: 5, is_active: false }],
    marks: [{ id: 1, kind: 'DOOR', label: 'Entrance', x: 0, y: 20, w: 6, h: 2 }] }
  const floorGet = (path: string) => {
    if (path.endsWith('/floor-plan')) return { data: { outlet_id: 1, areas: [area] } }
    if (path.endsWith('/pos/outlets')) return { data: { data: [outlets[0]] } }
    return { data: { data: [] } }
  }

  it('draws the active tables and the marks of the first area', async () => {
    setup(PosFloorPlanView, floorGet)
    await flushPromises()
    expect(has('[data-testid=table-0]')).toBe(true)
    expect(has('[data-testid=table-1]')).toBe(false) // a table that is off is not drawn
    expect(body('[data-testid=mark-0]').text()).toBe('Entrance')
    expect(body('[data-testid=floor-side]').text()).toContain('1 tables, 2 seats')
  })

  it('adds a table in a free place and saves the whole plan with the version that was loaded', async () => {
    setup(PosFloorPlanView, floorGet)
    await flushPromises()
    await body('[data-testid=table-add]').trigger('click')
    await flushPromises()
    expect((document.body.querySelector('input[name=table_code]') as HTMLInputElement).value).toBe('2')
    await type('input[name=table_capacity]', '6')
    await body('[data-testid=floor-save]').trigger('click')
    await flushPromises()
    const [path, opts] = PUT.mock.calls[0] as [string, { params: { path: { id: number } }; body: { layout_version: number; tables: { id?: number; code: string; capacity: number }[] } }]
    expect(path).toBe('/api/v1/properties/{propertyId}/pos/areas/{id}/layout')
    expect(opts.params.path.id).toBe(8)
    expect(opts.body.layout_version).toBe(3)
    expect(opts.body.tables).toHaveLength(2)
    expect(opts.body.tables[0]).toMatchObject({ id: 1, code: '1' })
    expect(opts.body.tables[1]).toMatchObject({ code: '2', capacity: 6 })
  })

  it('says so when somebody else saved the plan, and loads it again', async () => {
    setup(PosFloorPlanView, floorGet)
    await flushPromises()
    await body('[data-testid=table-add]').trigger('click')
    PUT.mockRejectedValue(new ApiError({ type: 'about:blank', title: 'x', status: 409, code: 'VERSION_CONFLICT', detail: 'changed' }))
    await body('[data-testid=floor-save]').trigger('click')
    await flushPromises()
    expect(has('[data-testid=floor-conflict]')).toBe(true)
    const before = GET.mock.calls.filter((c) => String(c[0]).endsWith('/floor-plan')).length
    await body('[data-testid=floor-reload]').trigger('click')
    await flushPromises()
    expect(GET.mock.calls.filter((c) => String(c[0]).endsWith('/floor-plan')).length).toBe(before + 1)
    expect(has('[data-testid=floor-conflict]')).toBe(false)
  })

  it('removes the selected table from the plan', async () => {
    setup(PosFloorPlanView, floorGet)
    await flushPromises()
    await body('[data-testid=table-0]').trigger('pointerdown')
    await body('[data-testid=floor-remove]').trigger('click')
    expect(has('[data-testid=table-0]')).toBe(false)
  })
})
