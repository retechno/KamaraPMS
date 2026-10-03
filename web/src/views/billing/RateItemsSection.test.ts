import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import RateItemsSection from './RateItemsSection.vue'

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))

const vat = { id: 1, code: 'VAT', name: 'VAT', rate: '11.0000', tax_on_service: true, gl_account_code: '2.1.05', is_active: true }
const svc = { id: 2, code: 'SVC', name: 'Service', rate: '10.0000', gl_account_code: null, is_active: true }

function mountSection(kind: 'tax' | 'service', permissions: string[] = ['billing_config.manage']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: { data: [kind === 'tax' ? vat : svc] } })
  POST = vi.fn().mockResolvedValue({ data: {} })
  PATCH = vi.fn().mockResolvedValue({ data: {} })
  return mount(RateItemsSection, { props: { kind }, global: { plugins: [pinia] } })
}

describe('RateItemsSection', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
    PATCH = vi.fn()
  })

  it('loads taxes and shows rates as percentages', async () => {
    const w = mountSection('tax')
    await flushPromises()
    expect(GET.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/taxes')
    expect(w.get('[data-testid=tax-VAT]').text()).toContain('11%')
    expect(w.get('[data-testid=tax-VAT]').text()).toContain('Yes')
  })

  it('creates a tax with its rate as a string and the tax-on-service flag', async () => {
    const w = mountSection('tax')
    await flushPromises()
    await w.get('[data-testid=new-item]').trigger('click')
    await w.get('input[name=code]').setValue('CITY')
    await w.get('input[name=name]').setValue('City tax')
    await w.get('input[name=rate]').setValue('1.5')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/taxes', {
      params: { path: { propertyId: 7 } },
      body: { code: 'CITY', name: 'City tax', rate: '1.5', tax_on_service: false, tax_kind: 'LOCAL', is_active: true },
    })
  })

  it('service charges have no tax-on-service option', async () => {
    const w = mountSection('service')
    await flushPromises()
    expect(GET.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/service-charges')
    await w.get('[data-testid=new-item]').trigger('click')
    expect(w.find('input[name=tax_on_service]').exists()).toBe(false)
    await w.get('input[name=code]').setValue('SVC2')
    await w.get('input[name=name]').setValue('Service')
    await w.get('input[name=rate]').setValue('5')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/service-charges', {
      params: { path: { propertyId: 7 } },
      body: { code: 'SVC2', name: 'Service', rate: '5', is_active: true },
    })
  })

  it('warns how many in-house stays a rate change reaches', async () => {
    const w = mountSection('tax')
    await flushPromises()
    PATCH.mockResolvedValue({ data: { ...vat, rate: '12.0000', affected_open_stays: 3 } })
    await w.get('[data-testid=tax-VAT] button').trigger('click')
    await w.get('input[name=rate]').setValue('12')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(PATCH).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/taxes/{id}', {
      params: { path: { propertyId: 7, id: 1 } },
      body: { name: 'VAT', rate: '12', tax_on_service: true, tax_kind: 'LOCAL', gl_account_code: '2.1.05', is_active: true },
    })
    expect(w.get('[data-testid=notice]').text()).toContain('3 in-house stay(s)')
  })

  it('shows the account code and sends it on create, edit and clear', async () => {
    const w = mountSection('tax')
    await flushPromises()
    expect(w.get('[data-testid=tax-VAT] [data-testid=account]').text()).toBe('2.1.05')
    await w.get('[data-testid=tax-VAT] button').trigger('click')
    expect((w.get('input[name=gl_account_code]').element as HTMLInputElement).value).toBe('2.1.05')
    await w.get('input[name=gl_account_code]').setValue('')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(PATCH.mock.calls[0]?.[1]).toMatchObject({ body: { gl_account_code: '' } }) // an empty string clears the mapping
    const svcSection = mountSection('service')
    await flushPromises()
    expect(svcSection.get('[data-testid=service-SVC] [data-testid=account]').text()).toBe('—')
    await svcSection.get('button').trigger('click')
    await svcSection.get('input[name=code]').setValue('SVC2')
    await svcSection.get('input[name=name]').setValue('Service 2')
    await svcSection.get('input[name=rate]').setValue('5')
    await svcSection.get('input[name=gl_account_code]').setValue('2-2100')
    await svcSection.get('form').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1]).toMatchObject({ body: { code: 'SVC2', gl_account_code: '2-2100' } })
  })

  it('shows TAX_IN_USE when deactivating a mapped tax', async () => {
    const w = mountSection('tax')
    await flushPromises()
    PATCH.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'TAX_IN_USE', detail: 'the tax is still mapped to charge codes' }))
    await w.get('[data-testid=tax-VAT] button').trigger('click')
    await w.get('input[name=is_active]').setValue(false)
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('TAX_IN_USE')
  })

  it('shows field errors next to the rate', async () => {
    const w = mountSection('tax')
    await flushPromises()
    POST.mockRejectedValue(
      new ApiError({
        type: 't', title: 'Unprocessable Entity', status: 422, code: 'VALIDATION_FAILED', detail: 'the tax is invalid',
        errors: [{ field: 'rate', code: 'INVALID_RATE', message: 'a percentage from 0 to 100' }],
      }),
    )
    await w.get('[data-testid=new-item]').trigger('click')
    await w.get('input[name=rate]').setValue('150')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(w.get('input[name=rate]').attributes('aria-invalid')).toBe('true')
    expect(w.text()).toContain('a percentage from 0 to 100')
  })

  it('is read-only without billing_config.manage', async () => {
    const w = mountSection('tax', [])
    await flushPromises()
    expect(w.find('[data-testid=new-item]').exists()).toBe(false)
    expect(w.find('[data-testid=tax-VAT] button').exists()).toBe(false)
  })
})
