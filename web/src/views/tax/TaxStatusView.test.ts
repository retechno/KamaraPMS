import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import TaxStatusView from './TaxStatusView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const row = (over: Record<string, unknown> = {}) => ({ id: 1, effective_from: '2000-01-01', is_pkp: false, input_vat_treatment: 'EXPENSE', created_at: '2026-09-30T00:00:00Z', ...over })
const view = (history = [row()]) => ({ current: history[history.length - 1], history })

function mountView(permissions = ['tax.view', 'tax.manage']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: view() })
  POST = vi.fn().mockResolvedValue({ data: view() })
  return mount(TaxStatusView, { global: { plugins: [pinia], stubs: { ApprovalDialog: { name: 'ApprovalDialog', emits: ['approve', 'cancel'], template: '<div data-testid="approval" />' } } } })
}

const problem = (code: string, errors: unknown[] = []) => new ApiError({ type: 't', title: 'x', status: 422, code, detail: 'it failed', errors } as never)

describe('PKP status', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
  })

  it('shows the status in force and the history', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=current-status]').text()).toBe('Not PKP')
    expect(w.get('[data-testid=current-treatment]').text()).toContain('Added to the cost of the purchase')
    expect(w.find('[data-testid=status-2000-01-01]').exists()).toBe(true)
  })

  it('a viewer reads it but cannot change it; without the permission nothing is read', async () => {
    const viewer = mountView(['tax.view'])
    await flushPromises()
    expect(viewer.find('[data-testid=change-status]').exists()).toBe(false)
    const nobody = mountView(['tax.file'])
    await flushPromises()
    expect(nobody.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })

  it('adds a change and offers input VAT claims only to a PKP hotel', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=change-status]').trigger('click')
    expect(w.findAll('select[name=input_vat_treatment] option').map((o) => o.text())).toEqual(['Added to the cost of the purchase', 'Kept apart, not claimed'])
    await w.get('input[name=is_pkp]').setValue(true)
    expect(w.findAll('select[name=input_vat_treatment] option').map((o) => o.text())).toEqual(['Claimed against the VAT collected', 'Added to the cost of the purchase', 'Kept apart, not claimed'])
    expect((w.get('select[name=input_vat_treatment]').element as HTMLSelectElement).value).toBe('CREDITABLE')
    await w.get('input[name=effective_from]').setValue('2026-10-01')
    await w.get('input[name=npwp]').setValue('01.234.567.8-901.000')
    await w.get('[data-testid=status-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].params.path).toEqual({ propertyId: 7 })
    expect(POST.mock.calls[0]?.[1].body).toMatchObject({ effective_from: '2026-10-01', is_pkp: true, npwp: '01.234.567.8-901.000', input_vat_treatment: 'CREDITABLE', approval: undefined })
    expect(w.get('[data-testid=notice]').text()).toBe('Saved.')
  })

  it('asks for an approval when the server says the change is backdated, and sends it', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=change-status]').trigger('click')
    await w.get('input[name=effective_from]').setValue('2026-09-01')
    POST.mockRejectedValueOnce(problem('APPROVAL_REQUIRED'))
    await w.get('[data-testid=status-form]').trigger('submit')
    await flushPromises()
    expect(w.find('[data-testid=approval]').exists()).toBe(true)
    w.findComponent({ name: 'ApprovalDialog' }).vm.$emit('approve', { email: 'a@b.c', password: 'pw' })
    await flushPromises()
    expect(POST.mock.calls.at(-1)?.[1].body.approval).toEqual({ email: 'a@b.c', password: 'pw' })
    expect(w.find('[data-testid=approval]').exists()).toBe(false)
    expect(w.find('[data-testid=notice]').exists()).toBe(true)
  })

  it('shows the field errors of the server', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=change-status]').trigger('click')
    await w.get('input[name=is_pkp]').setValue(true)
    await w.get('input[name=effective_from]').setValue('2026-10-01')
    POST.mockRejectedValueOnce(problem('VALIDATION_FAILED', [{ field: 'npwp', code: 'REQUIRED', message: 'the tax number (NPWP) of a PKP property' }]))
    await w.get('[data-testid=status-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=status-form]').text()).toContain('the tax number (NPWP) of a PKP property')
  })
})
