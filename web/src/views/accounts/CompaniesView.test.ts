import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import CompaniesView from './CompaniesView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))

const acme = { id: 1, code: 'ACME', name: 'Acme Corp', contact_name: 'Budi', email: 'ap@acme.test', credit_limit: '5000000', payment_terms_days: 45, is_active: true }
const free = { id: 2, code: 'FREE', name: 'Free Ltd', credit_limit: null, payment_terms_days: 30, is_active: true }
const none = { id: 3, code: 'NONE', name: 'No Credit', credit_limit: '0', payment_terms_days: 0, is_active: false }

function mountView(permissions = ['company.manage', 'reservation.read']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: { data: [acme, free, none] } })
  POST = vi.fn().mockResolvedValue({ data: {} })
  PATCH = vi.fn().mockResolvedValue({ data: {} })
  return mount(CompaniesView, { global: { plugins: [pinia] } })
}

describe('CompaniesView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
    PATCH = vi.fn()
  })

  it('lists companies with their credit terms', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=company-ACME]').text()).toContain('5000000')
    expect(w.get('[data-testid=company-ACME]').text()).toContain('45 days')
    expect(w.get('[data-testid=company-FREE]').text()).toContain('No limit')
    expect(w.get('[data-testid=company-NONE]').text()).toContain('No credit')
    expect(w.get('[data-testid=company-NONE]').text()).toContain('Inactive')
  })

  it('creates a company with a limit, or without one', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('button.btn-primary').trigger('click')
    await w.get('input[name=code]').setValue('NEW')
    await w.get('input[name=name]').setValue('New Co')
    await w.get('[data-testid=company-form]').trigger('submit')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/companies', expect.objectContaining({
      params: { path: { propertyId: 7 } }, body: expect.objectContaining({ code: 'NEW', name: 'New Co', credit_limit: '', payment_terms_days: 30, is_active: true }),
    }))
    await w.get('button.btn-primary').trigger('click')
    await w.get('input[name=code]').setValue('LIM')
    await w.get('input[name=name]').setValue('Limited')
    await w.get('input[name=unlimited]').setValue(false)
    await w.get('input[name=credit_limit]').setValue('250000')
    await w.get('[data-testid=company-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[1]?.[1].body.credit_limit).toBe('250000')
  })

  it('edits without changing the code, and explains a refused deactivation', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=company-ACME] button').trigger('click')
    expect((w.get('input[name=code]').element as HTMLInputElement).disabled).toBe(true)
    expect((w.get('input[name=credit_limit]').element as HTMLInputElement).value).toBe('5000000')
    PATCH.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'COMPANY_HAS_BALANCE', detail: 'the company still owes money' }))
    await w.get('input[name=is_active]').setValue(false)
    await w.get('[data-testid=company-form]').trigger('submit')
    await flushPromises()
    expect(PATCH.mock.calls[0]?.[1].params.path).toEqual({ propertyId: 7, id: 1 })
    expect(w.get('[data-testid=form-error]').text()).toContain('COMPANY_HAS_BALANCE')
    expect(w.get('[data-testid=form-error]').text()).toContain('City ledger')
  })

  it('is read-only without company.manage and hidden without any read permission', async () => {
    const reader = mountView(['reservation.read'])
    await flushPromises()
    expect(reader.find('button.btn-primary').exists()).toBe(false)
    expect(reader.find('[data-testid=read-only]').exists()).toBe(true)
    expect(reader.find('[data-testid=company-ACME] button').exists()).toBe(false)
    const none = mountView([])
    await flushPromises()
    expect(none.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })
})
