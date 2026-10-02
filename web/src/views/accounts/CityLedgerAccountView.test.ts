import { DOMWrapper, flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import CityLedgerAccountView from './CityLedgerAccountView.vue'

// The approval dialog is rendered in a portal on the body, not inside the wrapper.
const dlg = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)

let GET = vi.fn()
let POST = vi.fn()
let openPdf = vi.fn()
vi.mock('@/utils/documents', async (orig) => ({ ...(await orig<typeof import('@/utils/documents')>()), openPdf: (...a: unknown[]) => openPdf(...a) }))
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const account = {
  company_id: 1, code: 'ACME', name: 'Acme Corp', is_active: true, credit_limit: '1000000', payment_terms_days: 30, transferred: '300000', received: '100000',
  balance: '200000', available: '800000',
}
const aging = { as_of: '2026-09-30', total: '200000', buckets: [{ label: '0-30', amount: '200000' }, { label: '31-60', amount: '0' }, { label: '61-90', amount: '0' }, { label: '90+', amount: '0' }] }
const statement = {
  company: account, from: null, to: null, opening_balance: '0', total_debit: '300000', total_credit: '100000', closing_balance: '200000',
  lines: [
    { date: '2026-09-30', kind: 'TRANSFER', number: 'PAY000002', description: 'Folio FOL000001', status: 'POSTED', debit: '300000', credit: '0', balance: '300000', guest_name: 'Siti Nurhaliza' },
    { date: '2026-09-30', kind: 'RECEIPT', number: 'CLR000001', description: 'Receipt (BANK_TRANSFER)', status: 'POSTED', debit: '0', credit: '100000', balance: '200000' },
    { date: '2026-09-29', kind: 'RECEIPT', number: 'CLR000000', description: 'Receipt (CASH)', status: 'VOIDED', debit: '0', credit: '5', balance: '200000' },
  ],
}
const receipts = [
  { id: 31, receipt_number: 'CLR000001', company_id: 1, amount: '100000', payment_method: 'BANK_TRANSFER', business_date: '2026-09-30', status: 'POSTED' },
  { id: 30, receipt_number: 'CLR000000', company_id: 1, amount: '5', payment_method: 'CASH', business_date: '2026-09-29', status: 'VOIDED' },
]

const candidates = [
  { payment_id: 41, payment_number: 'PAY000041', business_date: '2026-09-29', folio_number: 'FOL000041', confirmation_number: 'RES1', guest_name: 'Siti', room_numbers: '101', checked_out_at: '2026-09-30T05:00:00Z', amount: '300000', stay_status: 'CHECKED_OUT', invoiceable: true },
  { payment_id: 42, payment_number: 'PAY000042', business_date: '2026-09-30', folio_number: 'FOL000042', confirmation_number: 'RES2', guest_name: 'Andi', room_numbers: '102', checked_out_at: null, amount: '50000', stay_status: 'OPEN', invoiceable: false },
  { payment_id: 43, payment_number: 'PAY000043', business_date: '2026-09-30', folio_number: 'FOL000043', confirmation_number: 'RES3', guest_name: 'Budi', room_numbers: '103', checked_out_at: '2026-09-30T06:00:00Z', amount: '200000', stay_status: 'CHECKED_OUT', invoiceable: true },
]
const invoices = [
  { id: 51, invoice_number: 'CINV000001', company_id: 1, invoice_date: '2026-09-30', due_date: '2026-10-30', total: '500000', paid: '100000', outstanding: '400000', payment_status: 'PARTIAL', status: 'ISSUED' },
  { id: 50, invoice_number: 'CINV000000', company_id: 1, invoice_date: '2026-09-20', due_date: '2026-10-20', total: '10', paid: '0', outstanding: '0', payment_status: 'VOID', status: 'VOIDED' },
  { id: 49, invoice_number: 'CINV-PAID', company_id: 1, invoice_date: '2026-09-10', due_date: '2026-10-10', total: '7', paid: '7', outstanding: '0', payment_status: 'PAID', status: 'ISSUED' },
]

function mountView(permissions = ['cityledger.read', 'cityledger.receive', 'cityledger.invoice']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'clerk@hotel.com', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-09-30' } as never
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/aging')) return { data: aging }
    if (path.endsWith('/statement')) return { data: statement }
    if (path.endsWith('/receipts')) return { data: { data: receipts } }
    if (path.endsWith('/invoice-candidates')) return { data: { data: candidates } }
    if (path.endsWith('/invoices')) return { data: { data: invoices } }
    return { data: account }
  })
  POST = vi.fn().mockResolvedValue({ data: {} })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(CityLedgerAccountView, { props: { id: '1' }, global: { plugins: [pinia, router] }, attachTo: document.body })
}

describe('CityLedgerAccountView', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    GET = vi.fn()
    POST = vi.fn()
    openPdf = vi.fn().mockResolvedValue(undefined)
    document.body.innerHTML = ''
  })

  it('shows the balance, the aging and the statement with voided lines struck', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=balance]').text()).toBe('200,000')
    expect(w.get('[data-testid=aging]').text()).toContain('0-30')
    expect(w.get('[data-testid=aging]').text()).toContain('200,000')
    expect(w.get('[data-testid=line-PAY000002]').text()).toContain('Siti Nurhaliza')
    expect(w.get('[data-testid=line-CLR000000]').text()).toContain('(voided)')
    expect(w.get('[data-testid=line-CLR000000]').classes()).toContain('voided')
  })

  it('records a receipt with an Idempotency-Key and shows a refusal', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('input[name=amount]').setValue('50000')
    await w.get('input[name=reference]').setValue('TRX-9')
    await w.get('[data-testid=receipt-form]').trigger('submit')
    await flushPromises()
    const [path, opts] = POST.mock.calls[0] as [string, { params: { header: Record<string, string> }; body: unknown }]
    expect(path).toBe('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/receipts')
    expect(opts.params.header['Idempotency-Key']).toBeTruthy()
    expect(opts.body).toEqual({ amount: '50000', payment_method: 'BANK_TRANSFER', reference_number: 'TRX-9', remarks: undefined })
    expect(w.get('[data-testid=notice]').text()).toContain('Receipt recorded')

    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'RECEIPT_EXCEEDS_BALANCE', detail: 'more than the company owes' }))
    await w.get('input[name=amount]').setValue('999999')
    await w.get('[data-testid=receipt-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('RECEIPT_EXCEEDS_BALANCE')
    expect(POST.mock.calls[1]?.[1].params.header['Idempotency-Key']).not.toBe(opts.params.header['Idempotency-Key']) // a new attempt after an answer
  })

  it('voids only a posted receipt of today, with a reason and an approval', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.find('[data-testid=void-CLR000000]').exists()).toBe(false) // already voided
    expect(w.find('[data-testid=void-PAY000002]').exists()).toBe(false) // a transfer is voided on its folio
    await w.get('[data-testid=void-CLR000001]').trigger('click')
    expect((w.get('[data-testid=void-continue]').element as HTMLButtonElement).disabled).toBe(true)
    await w.get('input[name=void_reason]').setValue('bounced')
    await w.get('[data-testid=void-continue]').trigger('click')
    await dlg('input[name=approval_password]').setValue('secret')
    await dlg('[data-testid=approval-dialog]').trigger('submit')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/city-ledger/receipts/{id}/void', {
      params: { path: { propertyId: 7, id: 31 } }, body: { reason: 'bounced', approval: { email: 'clerk@hotel.com', password: 'secret' } },
    })
    expect(w.get('[data-testid=notice]').text()).toContain('Receipt voided')
  })

  it('prints the statement for the chosen period', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('input[name=from]').setValue('2026-09-01')
    await w.get('input[name=to]').setValue('2026-09-30')
    await w.get('[data-testid=print-statement]').trigger('click')
    expect(openPdf).toHaveBeenCalledWith('/api/v1/properties/7/companies/1/statement.pdf?from=2026-09-01&to=2026-09-30')
    await w.get('[data-testid=statement] form').trigger('submit')
    await flushPromises()
    const calls = GET.mock.calls.filter(([p]) => (p as string).endsWith('/statement'))
    expect(calls.at(-1)?.[1].params.query).toEqual({ from: '2026-09-01', to: '2026-09-30' })
  })

  it('lists what can be invoiced, with guests still in house disabled', async () => {
    const w = mountView()
    await flushPromises()
    expect((w.get('[data-testid=candidate-PAY000041] input').element as HTMLInputElement).disabled).toBe(false)
    expect((w.get('[data-testid=candidate-PAY000042] input').element as HTMLInputElement).disabled).toBe(true)
    expect(w.get('[data-testid=candidate-PAY000042]').text()).toContain('in house')
    expect((w.get('[data-testid=create-invoice]').element as HTMLButtonElement).disabled).toBe(true)
  })

  it('combines the picked transfers into one invoice with an Idempotency-Key', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=pick-all]').setValue(true)
    expect(w.get('[data-testid=create-invoice]').text()).toContain('2 selected, 500,000')
    await w.get('input[name=invoice_notes]').setValue('September')
    await w.get('[data-testid=invoice-builder] form').trigger('submit')
    await flushPromises()
    const [path, opts] = POST.mock.calls[0] as [string, { params: { header: Record<string, string> }; body: unknown }]
    expect(path).toBe('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/invoices')
    expect(opts.params.header['Idempotency-Key']).toBeTruthy()
    expect(opts.body).toEqual({ payment_ids: [41, 43], notes: 'September' })
    expect(w.get('[data-testid=notice]').text()).toContain('issued')
  })

  it('shows a refused invoice', async () => {
    const w = mountView()
    await flushPromises()
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'TRANSFER_NOT_AVAILABLE', detail: 'already on an invoice' }))
    await w.get('[data-testid=candidate-PAY000041] input').setValue(true)
    await w.get('[data-testid=invoice-builder] form').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('TRANSFER_NOT_AVAILABLE')
  })

  it('prints an invoice and voids an issued one with a reason and an approval', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=print-CINV000001]').trigger('click')
    expect(openPdf).toHaveBeenCalledWith('/api/v1/properties/7/city-ledger/invoices/51/invoice.pdf')
    expect(w.find('[data-testid=void-CINV000000]').exists()).toBe(false) // already voided
    await w.get('[data-testid=void-CINV000001]').trigger('click')
    await w.get('input[name=void_reason]').setValue('wrong company')
    await w.get('[data-testid=void-continue]').trigger('click')
    await dlg('input[name=approval_password]').setValue('secret')
    await dlg('[data-testid=approval-dialog]').trigger('submit')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/city-ledger/invoices/{id}/void', {
      params: { path: { propertyId: 7, id: 51 } }, body: { reason: 'wrong company', approval: { email: 'clerk@hotel.com', password: 'secret' } },
    })
    expect(w.get('[data-testid=notice]').text()).toContain('Invoice voided')
  })

  it('shows what each invoice has been paid', async () => {
    const w = mountView()
    await flushPromises()
    const row = w.get('[data-testid=invoice-CINV000001]').text()
    expect(row).toContain('100,000')
    expect(row).toContain('400,000')
    expect(row).toContain('Part paid')
    expect(w.get('[data-testid=invoice-CINV-PAID]').text()).toContain('Paid')
    expect(w.find('[data-testid=pay-CINV-PAID]').exists()).toBe(false) // nothing outstanding
    expect(w.find('[data-testid=pay-CINV000000]').exists()).toBe(false) // voided
  })

  it('pays an invoice with a receipt allocated to it', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=pay-CINV000001]').trigger('click')
    expect((w.get('input[name=pay_amount]').element as HTMLInputElement).value).toBe('400000') // the outstanding amount
    await w.get('input[name=pay_amount]').setValue('150000')
    await w.get('input[name=pay_reference]').setValue('TRX-5')
    await w.get('[data-testid=pay-form]').trigger('submit')
    await flushPromises()
    const [path, opts] = POST.mock.calls[0] as [string, { params: { header: Record<string, string> }; body: unknown }]
    expect(path).toBe('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/receipts')
    expect(opts.params.header['Idempotency-Key']).toBeTruthy()
    expect(opts.body).toEqual({ amount: '150000', payment_method: 'BANK_TRANSFER', reference_number: 'TRX-5', allocations: [{ invoice_id: 51, amount: '150000' }] })
    expect(w.get('[data-testid=notice]').text()).toContain('CINV000001')
    expect(w.find('[data-testid=pay-form]').exists()).toBe(false)
  })

  it('shows a refused payment and keeps the form open', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=pay-CINV000001]').trigger('click')
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'ALLOCATION_EXCEEDS_INVOICE', detail: 'more than the invoice still owes' }))
    await w.get('[data-testid=pay-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('ALLOCATION_EXCEEDS_INVOICE')
    expect(w.find('[data-testid=pay-form]').exists()).toBe(true)
  })

  it('needs cityledger.receive to pay an invoice', async () => {
    const w = mountView(['cityledger.read'])
    await flushPromises()
    expect(w.find('[data-testid=pay-CINV000001]').exists()).toBe(false)
  })

  it('offers no invoicing without cityledger.invoice', async () => {
    const w = mountView(['cityledger.read'])
    await flushPromises()
    expect(w.find('[data-testid=invoice-builder]').exists()).toBe(false)
    expect(w.find('[data-testid=void-CINV000001]').exists()).toBe(false)
    expect(w.find('[data-testid=print-CINV000001]').exists()).toBe(true)
  })

  it('hides the receipt form without cityledger.receive, and everything without cityledger.read', async () => {
    const reader = mountView(['cityledger.read'])
    await flushPromises()
    expect(reader.find('[data-testid=receipt-form]').exists()).toBe(false)
    expect(reader.find('[data-testid=void-CLR000001]').exists()).toBe(false)
    const none = mountView([])
    await flushPromises()
    expect(none.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })
})
