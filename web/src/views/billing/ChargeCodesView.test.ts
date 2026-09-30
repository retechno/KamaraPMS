import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ChargeCodesView from './ChargeCodesView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
let PUT = vi.fn()
vi.mock('@/api/client', () => ({
  api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a), PUT: (...a: unknown[]) => PUT(...a) },
}))

const vat = { id: 1, code: 'VAT', name: 'VAT', rate: '11.0000', tax_on_service: true, is_active: true }
const city = { id: 2, code: 'CITY', name: 'City', rate: '1.0000', tax_on_service: false, is_active: true }
const old = { id: 3, code: 'OLD', name: 'Old', rate: '5.0000', tax_on_service: false, is_active: false }
const svc = { id: 7, code: 'SVC', name: 'Service', rate: '10.0000', is_active: true }
const room = {
  id: 10, code: 'ROOM', name: 'Room charge', charge_type: 'ROOM', price_mode: 'EXCLUSIVE', is_system: true, is_active: true,
  taxes: [{ tax_id: 1, code: 'VAT', name: 'VAT', rate: '11.0000', tax_on_service: true, sequence: 1 }],
  service_charges: [{ service_charge_id: 7, code: 'SVC', name: 'Service', rate: '10.0000', sequence: 1 }],
}
const spa = { id: 11, code: 'SPA', name: 'Spa', charge_type: 'SERVICE', price_mode: 'INCLUSIVE', default_unit_price: '250000.00', is_system: false, is_active: true, taxes: [], service_charges: [] }

function mountView(permissions: string[]) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/taxes')) return { data: { data: [vat, city, old] } }
    if (path.endsWith('/service-charges')) return { data: { data: [svc] } }
    return { data: { data: [room, spa] } }
  })
  POST = vi.fn().mockResolvedValue({ data: {} })
  PATCH = vi.fn().mockResolvedValue({ data: {} })
  PUT = vi.fn().mockResolvedValue({ data: room })
  return mount(ChargeCodesView, { global: { plugins: [pinia] } })
}

describe('ChargeCodesView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
    PATCH = vi.fn()
    PUT = vi.fn()
  })

  it('lists codes with their rules in calculation order', async () => {
    const w = mountView(['billing_config.manage'])
    await flushPromises()
    expect(w.get('[data-testid=code-ROOM] [data-testid=summary]').text()).toBe('SVC 10% → VAT 11%')
    expect(w.get('[data-testid=code-SPA] [data-testid=summary]').text()).toBe('none')
    expect(w.get('[data-testid=code-ROOM]').text()).toContain('system')
  })

  it('reorders, adds and removes rules and saves them with sequences 1..n', async () => {
    const w = mountView(['billing_config.manage'])
    await flushPromises()
    await w.get('[data-testid=code-ROOM] button').trigger('click')
    // Only active, unmapped taxes can be added.
    expect(w.findAll('select[name=add_tax] option').map((o) => o.text())).toEqual(['Add tax…', 'CITY · 1%'])
    await w.get('select[name=add_tax]').setValue(2)
    await w.get('[data-testid=add-tax]').trigger('click')
    expect(w.findAll('[data-testid^=tax-row-]')).toHaveLength(2)

    await w.get('button[aria-label="Move CITY · 1% up"]').trigger('click')
    expect(w.get('[data-testid=tax-row-0]').text()).toContain('CITY')
    await w.get('button[aria-label="Remove SVC · 10%"]').trigger('click')
    expect(w.find('[data-testid=service-row-0]').exists()).toBe(false)

    await w.get('[data-testid=save-rules]').trigger('click')
    await flushPromises()
    expect(PUT).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/charge-codes/{id}/rules', {
      params: { path: { propertyId: 7, id: 10 } },
      body: { taxes: [{ tax_id: 2, sequence: 1 }, { tax_id: 1, sequence: 2 }], service_charges: [] },
    })
    expect(w.find('[data-testid=rules-saved]').exists()).toBe(true)
  })

  it('offers the calculator only for existing codes', async () => {
    const w = mountView(['billing_config.manage'])
    await flushPromises()
    await w.get('button.btn-primary').trigger('click')
    expect(w.find('[data-testid=calculator]').exists()).toBe(false)
    await w.get('button[type=button]').trigger('click') // Close
    await w.get('[data-testid=code-SPA] button').trigger('click')
    expect(w.find('[data-testid=calculator]').exists()).toBe(true)
    expect((w.get('[data-testid=calculator] input[name=unit_price]').element as HTMLInputElement).value).toBe('250000')
  })

  it('shows a rule error such as an inactive tax', async () => {
    const w = mountView(['billing_config.manage'])
    await flushPromises()
    PUT.mockRejectedValue(new ApiError({ type: 't', title: 'Unprocessable Entity', status: 422, code: 'VALIDATION_FAILED', detail: 'the rules are invalid' }))
    await w.get('[data-testid=code-ROOM] button').trigger('click')
    await w.get('[data-testid=save-rules]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('VALIDATION_FAILED')
  })

  it('creates a charge code sending only the default price that was entered', async () => {
    const w = mountView(['billing_config.manage'])
    await flushPromises()
    await w.get('button.btn-primary').trigger('click')
    await w.get('input[name=code]').setValue('MASSAGE')
    await w.get('input[name=name]').setValue('Massage')
    await w.get('select[name=charge_type]').setValue('SERVICE')
    await w.get('select[name=price_mode]').setValue('INCLUSIVE')
    await w.get('[data-testid=code-form]').trigger('submit')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/charge-codes', {
      params: { path: { propertyId: 7 } },
      body: { code: 'MASSAGE', name: 'Massage', charge_type: 'SERVICE', price_mode: 'INCLUSIVE', default_unit_price: undefined, is_active: true },
    })
    expect(w.find('[data-testid=rules]').exists()).toBe(false) // rules are edited after the code exists
  })

  it('explains a locked price mode and keeps the system type fixed', async () => {
    const w = mountView(['billing_config.manage'])
    await flushPromises()
    PATCH.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'PRICE_MODE_LOCKED', detail: 'the price mode of a charge code in use cannot change' }))
    await w.get('[data-testid=code-ROOM] button').trigger('click')
    expect(w.get('select[name=charge_type]').attributes('disabled')).toBeDefined()
    await w.get('select[name=price_mode]').setValue('INCLUSIVE')
    await w.get('[data-testid=code-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('PRICE_MODE_LOCKED')
  })

  it('filters by type and is read-only without billing_config.manage', async () => {
    const w = mountView([])
    await flushPromises()
    await w.get('select[name=type_filter]').setValue('SERVICE')
    expect(w.find('[data-testid=code-ROOM]').exists()).toBe(false)
    expect(w.find('[data-testid=code-SPA]').exists()).toBe(true)
    expect(w.find('button.btn-primary').exists()).toBe(false)
    expect(w.find('[data-testid=code-SPA] button').exists()).toBe(false)
  })
})
