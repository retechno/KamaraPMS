import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import CreditNotesView from './CreditNotesView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const bill = {
  id: 3, bill_number: 'BILL000003', supplier_id: 1, supplier_code: 'PLN', supplier_name: 'Supplier PLN', supplier_invoice_number: 'INV-3', bill_date: '2026-09-30', due_date: '2026-10-30',
  total: '1110000', paid: '0', credited: '0', outstanding: '1110000', status: 'POSTED', payment_status: 'UNPAID', journal_id: 9, journal_number: 'JV000009', void_journal_id: null, voided_at: null,
  created_at: '2026-09-30T00:00:00Z',
  lines: [{ line_no: 1, account_id: 5, account_code: '6510', account_name: 'Electricity', description: 'September', amount: '1000000', vat_amount: '110000', vat_treatment: 'CREDITABLE' }],
}
const note = (over: Record<string, unknown> = {}) => ({
  id: 8, credit_number: 'SCN000001', supplier_id: 1, supplier_code: 'PLN', supplier_name: 'Supplier PLN', bill_id: 3, bill_number: 'BILL000003', supplier_invoice_number: 'INV-3',
  supplier_credit_number: 'CR-77', credit_date: '2026-10-01', reason: 'returned', total: '333000', applied: '0', unapplied: '333000', status: 'POSTED', journal_id: 12, journal_number: 'JV000012',
  void_journal_id: null, voided_at: null, created_at: '2026-10-01T00:00:00Z',
  lines: [{ line_no: 1, bill_line_no: 1, account_id: 5, account_code: '6510', account_name: 'Electricity', amount: '300000', vat_amount: '33000', vat_treatment: 'CREDITABLE', department_id: null }], allocations: [], ...over,
})

function mountView(permissions = ['payables.view', 'payables.post'], path = '/payables/credit-notes') {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'sari@hotel.com', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (p: string) => {
    if (p.endsWith('/credit-notes')) return { data: { data: [note(), note({ id: 9, credit_number: 'SCN000002', status: 'VOIDED', unapplied: '0', void_reason: 'mistake' })] } }
    if (p.endsWith('/credit-notes/{id}')) return { data: note() }
    if (p.endsWith('/open-bills')) return { data: { data: [{ bill_id: 4, bill_number: 'BILL000004', supplier_invoice_number: 'INV-4', bill_date: '2026-10-01', due_date: '2026-10-31', total: '500000', outstanding: '500000' }] } }
    if (p.endsWith('/bills/{id}')) return { data: bill }
    if (p.endsWith('/bills')) return { data: { data: [bill] } }
    if (p.endsWith('/suppliers')) return { data: { data: [{ id: 1, code: 'PLN', name: 'Supplier PLN', is_active: true, outstanding: '0', unapplied_credit: '0', payment_terms_days: 30, default_account_id: null, created_at: '2026-09-30T00:00:00Z' }] } }
    return { data: {} }
  })
  POST = vi.fn().mockResolvedValue({ data: note() })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  void router.push(path)
  return router.isReady().then(() => mount(CreditNotesView, { global: { plugins: [pinia, router] }, attachTo: document.body }))
}

describe('CreditNotesView', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    GET = vi.fn()
    POST = vi.fn()
  })

  it('lists the credit notes with what is not applied, and shows the lines of one on demand', async () => {
    const w = await mountView()
    await flushPromises()
    expect(w.get('[data-testid=credit-SCN000001]').text()).toContain('CR-77')
    expect(w.get('[data-testid=credit-SCN000001]').text()).toContain('333,000')
    expect(w.get('[data-testid=credit-SCN000002]').text()).toContain('Voided')
    await w.get('[data-testid=credit-SCN000001]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=credit-detail]').text()).toContain('6510 · Electricity')
    expect(w.get('[data-testid=credit-detail]').text()).toContain('claimed')
  })

  it('enters a credit note against a bill: the lines of the bill, an amount and the VAT of each', async () => {
    const w = await mountView(['payables.view', 'payables.post'], '/payables/credit-notes?bill=3')
    await flushPromises()
    expect(w.get('[data-testid=bill-owes]').text()).toContain('BILL000003 still owes 1,110,000')
    expect(w.get('[data-testid=credit-line-0]').text()).toContain('6510 · Electricity')
    await w.get('input[name=supplier_credit_number]').setValue('CR-77')
    await w.get('input[name=credit_date]').setValue('2026-10-01')
    await w.get('input[name=reason]').setValue('returned')
    expect((w.get('[data-testid=credit-post]').element as HTMLButtonElement).disabled).toBe(true) // no amount yet
    await w.get('input[name=amount_0]').setValue('300000')
    await w.get('input[name=vat_0]').setValue('33000')
    expect(w.get('[data-testid=credit-total]').text()).toBe('333,000')
    await w.get('[data-testid=credit-form]').trigger('submit')
    await flushPromises()
    const [path, init] = POST.mock.calls[0] as [string, { params: { header: Record<string, string> }; body: unknown }]
    expect(path).toBe('/api/v1/properties/{propertyId}/payables/credit-notes')
    expect(init.params.header['Idempotency-Key']).toBeTruthy()
    expect(init.body).toEqual({ bill_id: 3, supplier_credit_number: 'CR-77', credit_date: '2026-10-01', reason: 'returned', lines: [{ bill_line_no: 1, amount: '300000', vat_amount: '33000' }] })
    expect(w.get('[data-testid=notice]').text()).toContain('SCN000001')
  })

  it('shows why a credit note was refused beside the line', async () => {
    const w = await mountView(['payables.view', 'payables.post'], '/payables/credit-notes?bill=3')
    await flushPromises()
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'VALIDATION_FAILED', detail: 'invalid', errors: [{ field: 'lines[0].amount', code: 'EXCEEDS_BILL_LINE', message: 'more than the 100 the bill line has left to credit' }] }))
    await w.get('input[name=supplier_credit_number]').setValue('CR-1')
    await w.get('input[name=credit_date]').setValue('2026-10-01')
    await w.get('input[name=reason]').setValue('x')
    await w.get('input[name=amount_0]').setValue('999999999')
    await w.get('[data-testid=credit-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=credit-line-0]').text()).toContain('more than the 100')
  })

  it('applies what is left of a credit note to an open bill', async () => {
    const w = await mountView()
    await flushPromises()
    await w.get('[data-testid=credit-SCN000001]').trigger('click')
    await flushPromises()
    await w.get('[data-testid=start-apply]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=open-BILL000004]').text()).toContain('500,000')
    expect((w.get('[data-testid=apply-submit]').element as HTMLButtonElement).disabled).toBe(true)
    await w.get('input[name=apply_4]').setValue('200000')
    await w.get('[data-testid=apply-form]').trigger('submit')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/payables/credit-notes/{id}/apply', { params: { path: { propertyId: 7, id: 8 } }, body: { allocations: [{ bill_id: 4, amount: '200000' }] } })
    expect(w.get('[data-testid=notice]').text()).toContain('SCN000001 was applied')
  })

  it('voids a credit note with a reason and an approval', async () => {
    const w = await mountView()
    await flushPromises()
    await w.get('[data-testid=credit-SCN000001]').trigger('click')
    await flushPromises()
    await w.get('[data-testid=void]').trigger('click')
    await w.get('input[name=void_reason]').setValue('withdrawn')
    await w.get('[data-testid=void-ask]').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('Approve void')
  })

  it('needs payables.view, and only payables.post enters or changes anything', async () => {
    const none = await mountView([])
    await flushPromises()
    expect(none.get('[data-testid=no-access]').text()).toContain('payables.view')
    const viewer = await mountView(['payables.view'])
    await flushPromises()
    expect(viewer.find('[data-testid=new-credit]').exists()).toBe(false)
    await viewer.get('[data-testid=credit-SCN000001]').trigger('click')
    await flushPromises()
    expect(viewer.find('[data-testid=start-apply]').exists()).toBe(false)
    expect(viewer.find('[data-testid=void]').exists()).toBe(false)
  })
})
