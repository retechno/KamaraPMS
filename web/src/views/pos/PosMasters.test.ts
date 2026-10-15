import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { resetDepartments } from '@/composables/useDepartments'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import PosMenuSetupView from './PosMenuSetupView.vue'
import PosOutletsView from './PosOutletsView.vue'
import PosStationsView from './PosStationsView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PUT = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PUT: (...a: unknown[]) => PUT(...a) } }))

const departments = [{ id: 5, code: 'FB', name: 'Food and beverage', level: 1, is_active: true }, { id: 6, code: 'REST', name: 'Restaurant', level: 2, is_active: true }]
const profile = { id: 3, code: 'FB', name: 'F&B', price_mode: 'EXCLUSIVE', is_active: true, rules: [{ rule_type: 'SERVICE', service_charge_id: 1, code: 'SVC', name: 'Service', rate: '10', sequence: 1, is_active: true }, { rule_type: 'TAX', tax_id: 2, code: 'PB1', name: 'PB1', rate: '10', sequence: 2, is_active: true }] }
const list = { id: 4, code: 'MAIN', name: 'Main', is_active: true, item_count: 12 }
const printer = { id: 9, name: 'Receipt', kind: 'RECEIPT', connection_info: '192.168.1.50:9100', paper_width_mm: 80, codepage: 'CP437', opens_drawer: true, is_active: true }
const outlet = {
  id: 1, code: 'RESTO', name: 'Restoran Teratai', outlet_type: 'RESTAURANT', order_mode: 'PRINT', department_id: 6, department_name: 'Restaurant', rule_profile_id: 3, rule_profile_name: 'F&B',
  price_list_id: 4, price_list_name: 'Main', drawer: 'LACI1', receipt_printer_id: 9, receipt_printer_name: 'Receipt', uses_tables: true, guest_count_required: false, rounding_unit: 0,
  allow_room_charge: true, allow_company_charge: false, allow_comp: true, comp_treatment: 'PAYMENT_COMP', max_discount_pct_no_approval: '0', block_night_audit_with_open_bills: false, is_active: true, sort_order: 0, version: 3,
}

const mounted: VueWrapper[] = []
function setup(component: unknown, permissions = ['pos.manage']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'm@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  resetDepartments()
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/departments')) return { data: { data: departments } }
    if (path.endsWith('/pos/outlets')) return { data: { data: [outlet] } }
    if (path.endsWith('/pos/rule-profiles')) return { data: { data: [profile] } }
    if (path.endsWith('/pos/price-lists')) return { data: { data: [list] } }
    if (path.endsWith('/pos/printers')) return { data: { data: [printer] } }
    if (path.endsWith('/pos/stations')) return { data: { data: [{ id: 2, code: 'KITCHEN', name: 'Main kitchen', printer_id: null, printer_name: null, kds_enabled: false, late_after_minutes: 15, is_active: true }] } }
    if (path.endsWith('/pos/setup-check')) return { data: { data: [{ code: 'PRODUCT_STATION_NO_OUTPUT', severity: 'ERROR', outlet: 'RESTO', product: 'NASGOR', station: 'KITCHEN' }] } }
    if (path.endsWith('/pos/categories')) return { data: { data: [{ id: 1, code: 'MAINS', name: 'Main dishes', parent_id: null, sort_order: 1, is_active: true, product_count: 7 }] } }
    if (path.endsWith('/taxes')) return { data: { data: [{ id: 2, code: 'PB1', name: 'PB1', rate: '10', is_active: true }] } }
    if (path.endsWith('/service-charges')) return { data: { data: [{ id: 1, code: 'SVC', name: 'Service', rate: '10', is_active: true }] } }
    return { data: { data: [] } }
  })
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

describe('the outlets', () => {
  it('lists an outlet with its mode, department, profile, price list, drawer and complimentary treatment', async () => {
    setup(PosOutletsView)
    await flushPromises()
    const card = body('[data-testid=outlet-RESTO]').text()
    expect(card).toContain('Restoran Teratai')
    expect(card).toContain('Print tickets')
    expect(card).toContain('Restaurant')
    expect(card).toContain('LACI1')
    expect(card).toContain('Complimentary as a payment')
  })

  it('adds an outlet with the choices of the property and the complimentary treatment', async () => {
    setup(PosOutletsView)
    await flushPromises()
    await body('[data-testid=outlet-new]').trigger('click')
    await flushPromises()
    expect(has('[data-testid=outlet-dialog]')).toBe(true)
    expect(body('[data-testid=outlet-save]').attributes('disabled')).toBeDefined()
    await type('input[name=code]', 'rs')
    await type('input[name=name]', 'Room service')
    await body('[data-testid=comp-DISCOUNT_100]').setValue(true)
    expect(body('[data-testid=outlet-save]').attributes('disabled')).toBeUndefined()
    await body('[data-testid=outlet-save]').trigger('click')
    await flushPromises()
    const [path, opts] = POST.mock.calls[0] as [string, { body: Record<string, unknown> }]
    expect(path).toBe('/api/v1/properties/{propertyId}/pos/outlets')
    expect(opts.body).toMatchObject({ code: 'RS', name: 'Room service', comp_treatment: 'DISCOUNT_100', department_id: 6, rule_profile_id: 3, price_list_id: 4, order_mode: 'PRINT' })
  })

  it('edits an outlet with its version and keeps the code', async () => {
    setup(PosOutletsView)
    await flushPromises()
    await body('[data-testid=outlet-edit-RESTO]').trigger('click')
    await flushPromises()
    expect((document.body.querySelector('input[name=code]') as HTMLInputElement).disabled).toBe(true)
    await type('input[name=name]', 'Restoran Teratai 2')
    await body('[data-testid=outlet-save]').trigger('click')
    await flushPromises()
    const [path, opts] = PUT.mock.calls[0] as [string, { params: { path: { id: number } }; body: Record<string, unknown> }]
    expect(path).toBe('/api/v1/properties/{propertyId}/pos/outlets/{id}')
    expect(opts.params.path.id).toBe(1)
    expect(opts.body).toMatchObject({ name: 'Restoran Teratai 2', version: 3, comp_treatment: 'PAYMENT_COMP' })
  })

  it('shows the field errors of the server in the form', async () => {
    setup(PosOutletsView)
    await flushPromises()
    await body('[data-testid=outlet-new]').trigger('click')
    await type('input[name=code]', 'fol')
    await type('input[name=name]', 'X')
    POST.mockRejectedValue(new ApiError({ type: 'about:blank', title: 'x', status: 422, code: 'VALIDATION_FAILED', detail: 'invalid', errors: [{ field: 'code', code: 'RESERVED', message: 'this prefix is used by another document' }] }))
    await body('[data-testid=outlet-save]').trigger('click')
    await flushPromises()
    expect(body('[data-testid=outlet-dialog]').text()).toContain('this prefix is used by another document')
  })
})

describe('the stations and printers', () => {
  it('lists them with the configuration check, and adds a station', async () => {
    setup(PosStationsView)
    await flushPromises()
    expect(body('[data-testid=station-KITCHEN]').text()).toContain('Main kitchen')
    expect(body('[data-testid=setup-check]').text()).toContain('no printer or kitchen display')
    expect(body('[data-testid=printer-9]').text()).toContain('192.168.1.50:9100')
    await body('[data-testid=station-new]').trigger('click')
    await type('input[name=code]', 'bar')
    await type('input[name=name]', 'Bar')
    await body('[data-testid=station-save]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/pos/stations')
    expect((POST.mock.calls[0] as [string, { body: Record<string, unknown> }])[1].body).toMatchObject({ code: 'BAR', name: 'Bar', kds_enabled: false, late_after_minutes: 15 })
  })
})

describe('categories, price lists and profiles', () => {
  it('lists the three and builds a profile from the taxes and service charges of Setup, in order', async () => {
    setup(PosMenuSetupView)
    await flushPromises()
    expect(body('[data-testid=category-MAINS]').text()).toContain('7 products')
    expect(body('[data-testid=list-MAIN]').text()).toContain('12 prices')
    expect(body('[data-testid=profile-FB]').text()).toContain('Service 10% · PB1 10%')
    await body('[data-testid=profile-new]').trigger('click')
    await flushPromises()
    await type('input[name=code]', 'tax')
    await type('input[name=name]', 'Tax only')
    const select = document.body.querySelector('[data-testid=profile-add-rule]') as HTMLSelectElement
    select.value = 'T2'
    select.dispatchEvent(new Event('change', { bubbles: true }))
    await flushPromises()
    await body('[data-testid=profile-add]').trigger('click')
    await body('[data-testid=profile-save]').trigger('click')
    await flushPromises()
    const [path, opts] = POST.mock.calls[0] as [string, { body: Record<string, unknown> }]
    expect(path).toBe('/api/v1/properties/{propertyId}/pos/rule-profiles')
    expect(opts.body).toMatchObject({ code: 'TAX', name: 'Tax only', price_mode: 'EXCLUSIVE', rules: [{ tax_id: 2, service_charge_id: null }] })
  })
})
