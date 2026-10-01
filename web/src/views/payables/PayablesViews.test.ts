import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import AgingView from './AgingView.vue'
import BillsView from './BillsView.vue'
import PaymentsView from './PaymentsView.vue'
import SuppliersView from './SuppliersView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))

const supplier = (id: number, code: string, over: Record<string, unknown> = {}) => ({
  id, code, name: `Supplier ${code}`, payment_terms_days: 30, default_account_id: null, is_active: true, outstanding: '0', created_at: '2026-09-30T00:00:00Z', ...over,
})
const suppliers = [supplier(1, 'PLN', { default_account_id: 5, default_account_code: '6510', default_account_name: 'Electricity', outstanding: '1500000' }), supplier(2, 'OLD', { is_active: false })]
const accounts = [
  { id: 5, code: '6510', name: 'Electricity', account_type: 'EXPENSE', is_postable: true, is_active: true },
  { id: 6, code: '1420', name: 'Prepaid taxes', account_type: 'ASSET', is_postable: true, is_active: true },
  { id: 7, code: '4110', name: 'Room revenue', account_type: 'REVENUE', is_postable: true, is_active: true },
]
const bill = (id: number, number: string, over: Record<string, unknown> = {}) => ({
  id, bill_number: number, supplier_id: 1, supplier_code: 'PLN', supplier_name: 'Supplier PLN', supplier_invoice_number: `INV-${id}`, bill_date: '2026-09-30', due_date: '2026-10-30',
  total: '1000000', paid: '0', outstanding: '1000000', status: 'POSTED', payment_status: 'UNPAID', journal_id: 9, journal_number: 'JV000009', void_journal_id: null, voided_at: null,
  created_at: '2026-09-30T00:00:00Z', ...over,
})
const bills = [bill(1, 'BILL000001'), bill(2, 'BILL000002', { status: 'VOIDED', payment_status: 'VOIDED', outstanding: '0' })]
const payment = (id: number, number: string, over: Record<string, unknown> = {}) => ({
  id, payment_number: number, supplier_id: 1, supplier_code: 'PLN', supplier_name: 'Supplier PLN', payment_date: '2026-09-30', amount: '400000', payment_method: 'BANK_TRANSFER',
  status: 'POSTED', journal_id: 12, journal_number: 'JV000012', void_journal_id: null, voided_at: null, created_at: '2026-09-30T00:00:00Z', ...over,
})

function mountView(component: object, permissions = ['payables.view', 'payables.manage', 'payables.post', 'accounting.view'], query = '/') {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/open-bills')) return { data: { data: [{ bill_id: 1, bill_number: 'BILL000001', supplier_invoice_number: 'INV-1', bill_date: '2026-09-30', due_date: '2026-10-30', total: '1000000', outstanding: '600000' }, { bill_id: 3, bill_number: 'BILL000003', supplier_invoice_number: 'INV-3', bill_date: '2026-09-30', due_date: '2026-11-30', total: '500000', outstanding: '500000' }] } }
    if (path.endsWith('/suppliers')) return { data: { data: suppliers } }
    if (path.endsWith('/bills/{id}')) return { data: bill(1, 'BILL000001', { paid: '400000', lines: [{ line_no: 1, account_id: 5, account_code: '6510', account_name: 'Electricity', amount: '1000000' }] }) }
    if (path.endsWith('/bills')) return { data: { data: bills } }
    if (path.endsWith('/payments/{id}')) return { data: payment(1, 'SPAY000001', { allocations: [{ bill_id: 1, bill_number: 'BILL000001', supplier_invoice_number: 'INV-1', amount: '400000' }] }) }
    if (path.endsWith('/payments')) return { data: { data: [payment(1, 'SPAY000001')] } }
    if (path.endsWith('/aging')) {
      const z = { CURRENT: '0', DAYS_1_30: '0', DAYS_31_60: '0', DAYS_61_90: '0', DAYS_OVER_90: '0' }
      return { data: { as_of: '2026-10-02', total: '1500000', buckets: { ...z, CURRENT: '500000', DAYS_1_30: '1000000' }, suppliers: [{ supplier_id: 1, supplier_code: 'PLN', supplier_name: 'Supplier PLN', total: '1500000', buckets: { ...z, CURRENT: '500000', DAYS_1_30: '1000000' }, bills: [{ bill_id: 1, bill_number: 'BILL000001', supplier_invoice_number: 'INV-1', bill_date: '2026-09-30', due_date: '2026-09-30', days_overdue: 2, outstanding: '1000000' }] }] } }
    }
    return { data: { data: accounts } } // the chart
  })
  POST = vi.fn().mockResolvedValue({ data: { bill_number: 'BILL000004', payment_number: 'SPAY000002' } })
  PATCH = vi.fn().mockResolvedValue({ data: {} })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return router.push(query).then(async () => {
    await router.isReady()
    return mount(component, { global: { plugins: [pinia, router], stubs: { ApprovalDialog: { name: 'ApprovalDialog', emits: ['approve', 'cancel'], template: '<div data-testid="approval" />' } } } })
  })
}

describe('payables views', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
    PATCH = vi.fn()
  })

  it('lists suppliers with what is owed and hides the inactive ones', async () => {
    const w = await mountView(SuppliersView)
    await flushPromises()
    expect(w.get('[data-testid=supplier-PLN]').text()).toContain('6510 - Electricity')
    expect(w.get('[data-testid=owed]').text()).toBe('1500000')
    expect(w.find('[data-testid=supplier-OLD]').exists()).toBe(false)
    await w.get('input[name=inactive]').setValue(true)
    expect(w.find('[data-testid=supplier-OLD]').exists()).toBe(true)
  })

  it('creates and edits a supplier and shows a refusal', async () => {
    const w = await mountView(SuppliersView)
    await flushPromises()
    await w.get('[data-testid=new-supplier]').trigger('click')
    await w.get('input[name=code]').setValue('PAM')
    await w.get('input[name=name]').setValue('PAM Water')
    await w.get('input[name=payment_terms_days]').setValue('14')
    await w.get('select[name=default_account_id]').setValue(5)
    await w.get('[data-testid=supplier-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body).toMatchObject({ code: 'PAM', name: 'PAM Water', payment_terms_days: 14, default_account_id: 5, is_active: true })
    expect(w.get('[data-testid=notice]').text()).toContain('saved')
    await w.get('[data-testid=edit-PLN]').trigger('click')
    expect((w.get('input[name=code]').element as HTMLInputElement).disabled).toBe(true)
    PATCH.mockRejectedValue(new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'VALIDATION_FAILED', detail: 'the supplier is invalid', errors: [{ field: 'email', code: 'INVALID_EMAIL', message: 'not an e-mail address' }] } as never))
    await w.get('[data-testid=supplier-form]').trigger('submit')
    await flushPromises()
    expect(PATCH.mock.calls[0]?.[1].params.path).toEqual({ propertyId: 7, id: 1 })
    expect(w.get('[data-testid=supplier-form]').text()).toContain('not an e-mail address')
  })

  it('enters a bill with its default account, only when the total is above zero', async () => {
    const w = await mountView(BillsView)
    await flushPromises()
    expect(w.get('[data-testid=bill-BILL000002]').classes()).toContain('voided')
    await w.get('[data-testid=new-bill]').trigger('click')
    expect(w.findAll('select[name=account_0] option').map((o) => o.text())).toEqual(['Choose an account', '6510 · Electricity', '1420 · Prepaid taxes']) // no revenue accounts
    await w.get('select[name=supplier_id]').setValue(1)
    expect((w.get('select[name=account_0]').element as HTMLSelectElement).value).toBe('5') // the supplier's usual account
    await w.get('input[name=invoice]').setValue('INV-77')
    await w.get('input[name=bill_date]').setValue('2026-09-30')
    expect((w.get('[data-testid=bill-post]').element as HTMLButtonElement).disabled).toBe(true)
    await w.get('input[name=amount_0]').setValue('1000000')
    await w.get('[data-testid=add-line]').trigger('click')
    await w.get('select[name=account_1]').setValue(6)
    await w.get('input[name=amount_1]').setValue('110000')
    expect(w.get('[data-testid=bill-total]').text()).toBe('1110000')
    await w.get('[data-testid=bill-form]').trigger('submit')
    await flushPromises()
    const [path, init] = POST.mock.calls[0] as [string, { params: { header: Record<string, string> }; body: unknown }]
    expect(path).toBe('/api/v1/properties/{propertyId}/payables/bills')
    expect(init.params.header['Idempotency-Key']).toBeTruthy()
    expect(init.body).toEqual({
      supplier_id: 1, supplier_invoice_number: 'INV-77', bill_date: '2026-09-30', due_date: null, description: undefined,
      lines: [{ account_id: 5, description: undefined, amount: '1000000' }, { account_id: 6, description: undefined, amount: '110000' }],
    })
    expect(w.get('[data-testid=notice]').text()).toContain('BILL000004')
  })

  it('shows the lines of a bill and voids it with a reason and an approval', async () => {
    const w = await mountView(BillsView)
    await flushPromises()
    await w.get('[data-testid=bill-BILL000001]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=bill-detail]').text()).toContain('6510 · Electricity')
    await w.get('[data-testid=void]').trigger('click')
    expect((w.get('[data-testid=void-ask]').element as HTMLButtonElement).disabled).toBe(true)
    await w.get('input[name=reason]').setValue('duplicate')
    await w.get('[data-testid=void-ask]').trigger('submit')
    const dialog = w.findComponent({ name: 'ApprovalDialog' })
    dialog.vm.$emit('approve', { email: 'a@b.c', password: 'pw' })
    await flushPromises()
    const [path, init] = POST.mock.calls.at(-1) as [string, { params: { path: unknown }; body: unknown }]
    expect(path).toBe('/api/v1/properties/{propertyId}/payables/bills/{id}/void')
    expect(init.params.path).toEqual({ propertyId: 7, id: 1 })
    expect(init.body).toEqual({ reason: 'duplicate', approval: { email: 'a@b.c', password: 'pw' } })
  })

  it('filters bills on the server, starting from the supplier in the address', async () => {
    const w = await mountView(BillsView, undefined, '/payables/bills?supplier=1')
    await flushPromises()
    const first = GET.mock.calls.find((c) => String(c[0]).endsWith('/bills'))
    expect(first?.[1].params.query.supplier_id).toBe(1)
    await w.get('input[name=open_only]').setValue(true)
    await w.get('[data-testid=apply]').trigger('submit')
    await flushPromises()
    expect(GET.mock.calls.filter((c) => String(c[0]).endsWith('/bills')).at(-1)?.[1].params.query.open_only).toBe(true)
  })

  it('pays a supplier by settling open bills, never more than is owed', async () => {
    const w = await mountView(PaymentsView)
    await flushPromises()
    await w.get('[data-testid=new-payment]').trigger('click')
    await w.get('select[name=supplier_id]').setValue(1)
    await flushPromises()
    expect(w.findAll('[data-testid=open-bills] tbody tr')).toHaveLength(2)
    await w.get('input[name=payment_date]').setValue('2026-09-30')
    await w.get('input[name=pay_BILL000001]').setValue('700000') // owed 600000
    expect((w.get('[data-testid=payment-post]').element as HTMLButtonElement).disabled).toBe(true)
    await w.get('[data-testid=pay-all]').trigger('click')
    expect(w.get('[data-testid=payment-total]').text()).toBe('1100000')
    await w.get('input[name=pay_BILL000003]').setValue('')
    await w.get('select[name=method]').setValue('CASH')
    await w.get('[data-testid=payment-form]').trigger('submit')
    await flushPromises()
    const [path, init] = POST.mock.calls[0] as [string, { params: { header: Record<string, string> }; body: unknown }]
    expect(path).toBe('/api/v1/properties/{propertyId}/payables/payments')
    expect(init.params.header['Idempotency-Key']).toBeTruthy()
    expect(init.body).toEqual({ supplier_id: 1, payment_date: '2026-09-30', payment_method: 'CASH', reference_number: undefined, remarks: undefined, allocations: [{ bill_id: 1, amount: '600000' }] })
    expect(w.get('[data-testid=notice]').text()).toContain('SPAY000002')
  })

  it('shows a payment with what it settled and voids it with an approval', async () => {
    const w = await mountView(PaymentsView)
    await flushPromises()
    await w.get('[data-testid=payment-SPAY000001]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=payment-detail]').text()).toContain('BILL000001')
    await w.get('[data-testid=void]').trigger('click')
    await w.get('input[name=reason]').setValue('paid twice')
    await w.get('[data-testid=void-ask]').trigger('submit')
    w.findComponent({ name: 'ApprovalDialog' }).vm.$emit('approve', { email: 'a@b.c', password: 'pw' })
    await flushPromises()
    expect(POST.mock.calls.at(-1)?.[0]).toBe('/api/v1/properties/{propertyId}/payables/payments/{id}/void')
    expect(POST.mock.calls.at(-1)?.[1].body).toEqual({ reason: 'paid twice', approval: { email: 'a@b.c', password: 'pw' } })
  })

  it('shows the aging by bucket with the bills of a supplier on demand', async () => {
    const w = await mountView(AgingView)
    await flushPromises()
    expect(w.get('[data-testid=supplier-PLN]').text()).toContain('1000000')
    expect(w.get('[data-testid=totals]').text()).toContain('1500000')
    expect(w.find('[data-testid=bills-PLN]').exists()).toBe(false)
    await w.get('[data-testid=supplier-PLN]').trigger('click')
    expect(w.get('[data-testid=bills-PLN]').text()).toContain('BILL000001')
    await w.get('input[name=as_of]').setValue('2026-10-01')
    await w.get('[data-testid=apply]').trigger('submit')
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1].params.query).toEqual({ as_of: '2026-10-01' })
  })

  it('shows only what the role may do', async () => {
    const viewer = ['payables.view']
    const s = await mountView(SuppliersView, viewer)
    await flushPromises()
    expect(s.find('[data-testid=new-supplier]').exists()).toBe(false)
    expect(s.find('[data-testid=edit-PLN]').exists()).toBe(false)
    const b = await mountView(BillsView, viewer)
    await flushPromises()
    expect(b.find('[data-testid=new-bill]').exists()).toBe(false)
    await b.get('[data-testid=bill-BILL000001]').trigger('click')
    await flushPromises()
    expect(b.find('[data-testid=void]').exists()).toBe(false)
    const p = await mountView(PaymentsView, viewer)
    await flushPromises()
    expect(p.find('[data-testid=new-payment]').exists()).toBe(false)
    for (const component of [SuppliersView, BillsView, PaymentsView, AgingView]) {
      const none = await mountView(component, [])
      await flushPromises()
      expect(none.find('[data-testid=no-access]').exists()).toBe(true)
      expect(GET).not.toHaveBeenCalled()
    }
  })
})
