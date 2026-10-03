import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import TaxInvoicesView from './TaxInvoicesView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PUT = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PUT: (...a: unknown[]) => PUT(...a) } }))
const openPdf = vi.fn()
const downloadExport = vi.fn()
vi.mock('@/utils/documents', async (orig) => ({ ...(await orig<typeof import('@/utils/documents')>()), openPdf: (...a: unknown[]) => openPdf(...a), downloadExport: (...a: unknown[]) => downloadExport(...a) }))

const invoice = (over: Record<string, unknown> = {}) => ({
  id: 1, invoice_ref: 'TXI000001', status: 'ISSUED', issue_date: '2026-09-30', source_type: 'CITY_LEDGER_INVOICE', city_ledger_invoice_id: 5, city_ledger_invoice_number: 'CINV000005',
  folio_id: null, seller: { name: 'Hotel Bali', npwp: '01.234.567.8-901.000', pkp_number: 'PKP-77' }, buyer: { name: 'ACME Ltd', npwp: '023456789012000', address: 'Jl. Mawar 2' },
  taxable_base: '110000', vat_amount: '11000', replaces_invoice_id: null, voided_at: null, created_at: '2026-09-30T00:00:00Z',
  lines: [{ line_no: 1, charge_code: 'RESTAURANT', description: 'Restaurant', base_amount: '110000', rate: '10', vat_amount: '11000' }], ...over,
})
const preview = (over: Record<string, unknown> = {}) => ({
  source_type: 'CITY_LEDGER_INVOICE', source_ref: 'CINV000005', issue_date: '2026-09-30', seller: { name: 'Hotel Bali', npwp: '01.234.567.8-901.000' },
  buyer: { name: 'ACME Ltd', npwp: '023456789012000', address: 'Jl. Mawar 2' }, lines: [{ line_no: 1, charge_code: 'RESTAURANT', description: 'Restaurant', base_amount: '110000', rate: '10', vat_amount: '11000' }],
  taxable_base: '110000', vat_amount: '11000', blockers: [], ready: true, ...over,
})

function mountView(permissions = ['tax.view', 'tax.invoice'], path = '/tax/invoices', previewOver: Record<string, unknown> = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (p: string) => {
    if (p.endsWith('/invoices/preview')) return { data: preview(previewOver) }
    if (p.endsWith('/invoices/coverage')) return { data: { from: '2026-09-01', to: '2026-09-30', vat_collected: '16000', vat_invoiced: '11000', difference: '5000', uncovered: [{ folio_id: 9, folio_number: 'FOL000009', status: 'CLOSED', vat: '5000' }] } }
    if (p.endsWith('/invoices/exports')) return { data: { data: [{ id: 3, format: 'CSV', period_start: '2026-09-01', period_end: '2026-09-30', invoice_count: 2, file_name: 'tax-invoices-2026-09-01-2026-09-30.csv', sha256: 'abcdef0123456789'.repeat(4), created_at: '2026-09-30T00:00:00Z' }] } }
    if (p.endsWith('/invoices/{id}')) return { data: invoice() }
    return { data: { data: [invoice({ lines: undefined }), invoice({ id: 2, invoice_ref: 'TXI000002', status: 'VOIDED', void_reason: 'wrong buyer', djp_number: '010.000-26.1' })] } }
  })
  POST = vi.fn().mockResolvedValue({ data: invoice() })
  PUT = vi.fn().mockResolvedValue({ data: invoice({ djp_number: '010.000-26.12345678' }) })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return router.push(path).then(async () => {
    await router.isReady()
    return mount(TaxInvoicesView, { global: { plugins: [pinia, router], stubs: { ApprovalDialog: { name: 'ApprovalDialog', emits: ['approve', 'cancel'], template: '<div data-testid="approval" />' } } } })
  })
}

const problem = (code: string) => new ApiError({ type: 't', title: 'x', status: 409, code, detail: 'it failed' } as never)

describe('tax invoices', () => {
  beforeEach(() => {
    openPdf.mockReset()
    downloadExport.mockReset()
  })

  it('lists the invoices, with void ones struck through, and opens one with its lines', async () => {
    const w = await mountView()
    await flushPromises()
    expect(w.get('[data-testid=invoice-TXI000001]').text()).toContain('ACME Ltd')
    expect(w.get('[data-testid=invoice-TXI000002]').classes()).toContain('voided')
    await w.get('[data-testid=invoice-TXI000001]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=invoice-lines]').text()).toContain('RESTAURANT')
    await w.get('[data-testid=pdf]').trigger('click')
    expect(openPdf).toHaveBeenCalledWith('/api/v1/properties/7/tax/invoices/1/invoice.pdf')
  })

  it('previews the invoice of a city ledger invoice and issues it with an idempotency key', async () => {
    const w = await mountView(undefined, '/tax/invoices?source_type=CITY_LEDGER_INVOICE&id=5')
    await flushPromises()
    expect(GET.mock.calls.find((c) => String(c[0]).endsWith('/invoices/preview'))?.[1].params.query).toMatchObject({ source_type: 'CITY_LEDGER_INVOICE', id: 5 })
    expect(w.get('[data-testid=preview-totals]').text()).toContain('11,000')
    expect((w.get('input[name=buyer_name]').element as HTMLInputElement).value).toBe('ACME Ltd')
    expect((w.get('input[name=buyer_name]').element as HTMLInputElement).disabled).toBe(true) // the company is the buyer
    await w.get('[data-testid=issue-post]').trigger('submit')
    await flushPromises()
    const [path, init] = POST.mock.calls[0] as [string, { params: { header: Record<string, string> }; body: unknown }]
    expect(path).toBe('/api/v1/properties/{propertyId}/tax/invoices')
    expect(init.params.header['Idempotency-Key']).toBeTruthy()
    expect(init.body).toMatchObject({ source_type: 'CITY_LEDGER_INVOICE', city_ledger_invoice_id: 5 })
    expect(w.get('[data-testid=notice]').text()).toContain('TXI000001')
    expect(w.find('[data-testid=issue]').exists()).toBe(false)
  })

  it('says what stops an invoice and does not let it be issued', async () => {
    const w = await mountView(undefined, '/tax/invoices?source_type=FOLIO&id=9', { source_type: 'FOLIO', source_ref: 'FOL000009', buyer: { name: '', npwp: '' } })
    await flushPromises()
    expect((w.get('input[name=buyer_name]').element as HTMLInputElement).disabled).toBe(false) // the buyer of a folio is typed
    GET.mockImplementation(async (p: string) => (p.endsWith('/invoices/preview')
      ? { data: preview({ source_type: 'FOLIO', source_ref: 'FOL000009', ready: false, blockers: [{ code: 'BUYER_NPWP_INVALID', message: 'x' }, { code: 'NOT_PKP', message: 'x' }] }) }
      : { data: { data: [] } }))
    await w.get('input[name=buyer_name]').setValue('Siti')
    await w.get('input[name=buyer_name]').trigger('change')
    await flushPromises()
    expect(w.get('[data-testid=blockers]').text()).toContain('15 or 16 digits')
    expect(w.get('[data-testid=blockers]').text()).toContain('not PKP')
    expect((w.get('[data-testid=issue-post]').element as HTMLButtonElement).disabled).toBe(true)
  })

  it('records the official number of an issued invoice once', async () => {
    const w = await mountView()
    await flushPromises()
    await w.get('[data-testid=invoice-TXI000001]').trigger('click')
    await flushPromises()
    expect((w.get('[data-testid=save-number]').element as HTMLButtonElement).disabled).toBe(true)
    await w.get('input[name=djp_number]').setValue('010.000-26.12345678')
    await w.get('form [data-testid=save-number]').trigger('submit')
    await flushPromises()
    expect(PUT.mock.calls[0]?.[1]).toMatchObject({ params: { path: { propertyId: 7, id: 1 } }, body: { number: '010.000-26.12345678' } })
    expect(w.get('[data-testid=notice]').text()).toContain('official number')
  })

  it('voids an invoice with a reason and an approval', async () => {
    const w = await mountView()
    await flushPromises()
    await w.get('[data-testid=invoice-TXI000001]').trigger('click')
    await flushPromises()
    await w.get('[data-testid=void]').trigger('click')
    await w.get('input[name=reason]').setValue('wrong buyer')
    await w.get('form [data-testid=void-ask]').trigger('submit')
    expect(w.find('[data-testid=approval]').exists()).toBe(true)
    POST.mockRejectedValueOnce(problem('APPROVAL_INVALID_CREDENTIALS'))
    w.findComponent({ name: 'ApprovalDialog' }).vm.$emit('approve', { email: 'a@b.c', password: 'bad' })
    await flushPromises()
    expect(w.find('[data-testid=approval]').exists()).toBe(true) // still asking
    w.findComponent({ name: 'ApprovalDialog' }).vm.$emit('approve', { email: 'a@b.c', password: 'pw' })
    await flushPromises()
    expect(POST.mock.calls.at(-1)?.[1].body).toEqual({ reason: 'wrong buyer', approval: { email: 'a@b.c', password: 'pw' } })
    expect(w.get('[data-testid=notice]').text()).toContain('TXI000001')
  })

  it('offers a replacement of a void invoice', async () => {
    const w = await mountView()
    await flushPromises()
    GET.mockImplementation(async (p: string) => (p.endsWith('/invoices/{id}') ? { data: invoice({ id: 2, status: 'VOIDED', void_reason: 'wrong buyer' }) } : p.endsWith('/invoices/exports') ? { data: { data: [] } } : { data: { data: [invoice({ id: 2, status: 'VOIDED', lines: undefined })] } }))
    await w.get('[data-testid=apply]').trigger('click')
    await w.get('form').trigger('submit')
    await flushPromises()
    await w.get('[data-testid=invoice-TXI000001]').trigger('click')
    await flushPromises()
    const link = w.get('[data-testid=replace]')
    expect(link.attributes('href')).toContain('source_type=CITY_LEDGER_INVOICE')
    expect(link.attributes('href')).toContain('replaces=2')
  })

  it('exports a range and checks the coverage of the VAT collected', async () => {
    const w = await mountView()
    await flushPromises()
    expect(w.get('[data-testid=exports]').text()).toContain('tax-invoices-2026-09-01-2026-09-30.csv')
    downloadExport.mockResolvedValue({ invoices: '2', sha256: 'x' })
    await w.get('input[name=range_from]').setValue('2026-09-01')
    await w.get('input[name=range_to]').setValue('2026-09-30')
    await w.get('[data-testid=export]').trigger('click')
    await flushPromises()
    expect(downloadExport).toHaveBeenCalledWith('/api/v1/properties/7/tax/invoices/exports', { from: '2026-09-01', to: '2026-09-30' })
    expect(w.get('[data-testid=notice]').text()).toContain('2')
    await w.get('[data-testid=coverage-check]').trigger('click')
    await w.get('[data-testid=range-card] form').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=coverage-difference]').text()).toBe('5,000')
    expect(w.get('[data-testid=uncovered]').text()).toContain('FOL000009')
  })

  it('a viewer reads but cannot issue, export or void; without the permission nothing is read', async () => {
    const viewer = await mountView(['tax.view'], '/tax/invoices?source_type=CITY_LEDGER_INVOICE&id=5')
    await flushPromises()
    expect(viewer.find('[data-testid=issue]').exists()).toBe(false)
    expect(viewer.find('[data-testid=export]').exists()).toBe(false)
    await viewer.get('[data-testid=invoice-TXI000001]').trigger('click')
    await flushPromises()
    expect(viewer.find('[data-testid=void]').exists()).toBe(false)
    const nobody = await mountView(['tax.file'])
    await flushPromises()
    expect(nobody.find('[data-testid=no-access]').exists()).toBe(true)
  })
})
