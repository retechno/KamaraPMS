import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import FolioView from './FolioView.vue'

let GET = vi.fn()
let POST = vi.fn()
let openPdf = vi.fn()
vi.mock('@/utils/documents', async (orig) => ({ ...(await orig<typeof import('@/utils/documents')>()), openPdf: (...a: unknown[]) => openPdf(...a) }))
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const item = (over: object = {}) => ({
  id: 1, folio_id: 3, transaction_type: 'CHARGE', business_date: '2026-09-30', service_date: '2026-09-30', transaction_at: '2026-09-30T13:00:00Z',
  description: 'Minibar', charge_code: 'MINIBAR', quantity: '1', unit_price: '100000', price_mode: 'EXCLUSIVE', base_amount: '100000', discount_amount: '0',
  net_amount: '100000', rounding_adjustment: '0', service_charge_total: '10000', tax_total: '12100', debit: '122100', credit: '0', payment_id: null,
  reversed_by_item_id: null, components: [
    { component_type: 'SERVICE_CHARGE', code: 'SVC', name: 'Service', rate: '10.0000', base_amount: '100000', amount: '10000', sequence: 1 },
    { component_type: 'TAX', code: 'VAT', name: 'VAT', rate: '11.0000', base_amount: '110000', amount: '12100', sequence: 1 },
  ], ...over,
})
const folio = (over: object = {}) => ({
  id: 3, folio_number: 'FOL000001', folio_type: 'GUEST', status: 'OPEN', reservation_id: 9, stay_id: null, opened_at: '2026-09-30T13:00:00Z', closed_at: null,
  version: 4, balance: '22100', totals: { debit: '122100', credit: '100000' },
  items: [item(), item({ id: 2, transaction_type: 'PAYMENT', description: 'Payment PAY000001 (CASH)', charge_code: undefined, debit: '0', credit: '100000', payment_id: 7, components: [] })], ...over,
})
const codes = [
  { id: 1, code: 'ROOM', name: 'Room', charge_type: 'ROOM', is_active: true },
  { id: 2, code: 'MINIBAR', name: 'Minibar', charge_type: 'FOOD_BEVERAGE', is_active: true },
]
const companies = [{ id: 21, code: 'ACME', name: 'Acme Corp', is_active: true }]
const ALL = ['folio.read', 'folio.post_charge', 'folio.adjust', 'folio.reverse', 'payment.post', 'payment.void', 'payment.refund']

function mountView(f: object = folio(), permissions = ALL, companiesError?: ApiError) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'clerk@hotel.com', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-09-30' } as never
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/companies') && companiesError) throw companiesError
    return { data: path.endsWith('/charge-codes') ? { data: codes } : path.endsWith('/companies') ? { data: companies } : f }
  })
  POST = vi.fn().mockResolvedValue({ data: {} })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(FolioView, { props: { id: '3' }, global: { plugins: [pinia, router] }, attachTo: document.body })
}

describe('FolioView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
    openPdf = vi.fn().mockResolvedValue(undefined)
    document.body.innerHTML = ''
  })

  it('shows the ledger, the balance and the components of an item', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=balance]').text()).toBe('22100')
    expect(w.get('[data-testid=item-1]').text()).toContain('122100')
    expect(w.find('[data-testid=detail-1]').exists()).toBe(false)
    await w.get('[data-testid=toggle-1]').trigger('click')
    expect(w.get('[data-testid=detail-1]').text()).toContain('VAT 11.0000% on 110000 = 12100')
  })

  it('offers only what is allowed: reverse a same-day charge, void or refund a payment', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.find('[data-testid=reverse-1]').exists()).toBe(true)
    expect(w.find('[data-testid=reverse-2]').exists()).toBe(false) // a payment is voided, not reversed
    expect(w.find('[data-testid=void-2]').exists()).toBe(true)
    expect(w.find('[data-testid=refund-2]').exists()).toBe(true)

    const old = mountView(folio({ items: [item({ business_date: '2026-09-29' }), item({ id: 5, reversed_by_item_id: 6 })] }))
    await flushPromises()
    expect(old.find('[data-testid=reverse-1]').exists()).toBe(false) // an earlier day needs an adjustment
    expect(old.find('[data-testid=reverse-5]').exists()).toBe(false) // already reversed

    const reader = mountView(folio(), ['folio.read'])
    await flushPromises()
    expect(reader.find('[data-testid=reverse-1]').exists()).toBe(false)
    expect(reader.find('[data-testid=charge-form]').exists()).toBe(false)
    const closed = mountView(folio({ status: 'CLOSED' }))
    await flushPromises()
    expect(closed.find('[data-testid=charge-form]').exists()).toBe(false)
    expect(closed.find('[data-testid=void-2]').exists()).toBe(false)
  })

  it('posts a charge with an Idempotency-Key and no ROOM codes offered', async () => {
    const w = mountView()
    await flushPromises()
    const options = w.get('select[name=charge_code]').findAll('option').map((o) => o.text())
    expect(options).toEqual(['MINIBAR · Minibar'])
    await w.get('input[name=quantity]').setValue('2')
    await w.get('input[name=unit_price]').setValue('50000')
    await w.get('form[data-testid=charge-form]').trigger('submit')
    await flushPromises()
    const [path, init] = POST.mock.calls[0] as [string, { params: { header: Record<string, string> }; body: object }]
    expect(path).toBe('/api/v1/properties/{propertyId}/folios/{id}/charges')
    expect(init.params.header['Idempotency-Key']).toBeTruthy()
    expect(init.body).toMatchObject({ charge_code_id: 2, quantity: '2', unit_price: '50000' })
    expect(w.get('[data-testid=notice]').text()).toBe('Charge posted.')
  })

  it('takes a payment', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('input[name=amount]').setValue('50000')
    await w.get('select[name=payment_method]').setValue('CARD')
    await w.get('form[data-testid=payment-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1]).toMatchObject({ body: { amount: '50000', payment_method: 'CARD' } })
  })

  it('reverses an item only after a reason and an approval', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=reverse-1]').trigger('click')
    expect(w.find('[data-testid=approval-dialog]').exists()).toBe(false) // the reason comes first
    expect(w.get('form[data-testid=correction-form] button[type=submit]').attributes('disabled')).toBeDefined()
    await w.get('input[name=reason]').setValue('posted twice')
    await w.get('form[data-testid=correction-form]').trigger('submit')
    expect(w.find('[data-testid=approval-dialog]').exists()).toBe(true)
    expect(POST).not.toHaveBeenCalled()

    // a wrong password keeps the dialog open with the server's message
    POST.mockRejectedValueOnce(new ApiError({ type: 't', title: 'Unauthorized', status: 401, code: 'APPROVAL_INVALID_CREDENTIALS', detail: 'the approver email or password is incorrect' }))
    await w.get('input[name=approval_password]').setValue('wrong')
    await w.get('[data-testid=approval-dialog]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=approval-error]').text()).toContain('APPROVAL_INVALID_CREDENTIALS')
    expect(w.find('[data-testid=approval-dialog]').exists()).toBe(true)

    POST.mockResolvedValue({ data: {} })
    await w.get('input[name=approval_password]').setValue('right')
    await w.get('[data-testid=approval-dialog]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls.at(-1)).toEqual(['/api/v1/properties/{propertyId}/folio-items/{id}/reverse', {
      params: { path: { propertyId: 7, id: 1 } }, body: { reason: 'posted twice', approval: { email: 'clerk@hotel.com', password: 'right' } },
    }])
    expect(w.find('[data-testid=approval-dialog]').exists()).toBe(false)
    expect(w.get('[data-testid=notice]').text()).toBe('Item reversed.')
  })

  it('refunds with the amount, a reason and an approval', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=refund-2]').trigger('click')
    expect((w.get('input[name=refund_amount]').element as HTMLInputElement).value).toBe('100000')
    await w.get('input[name=refund_amount]').setValue('30000')
    await w.get('select[name=refund_method]').setValue('CASH')
    await w.get('input[name=refund_reference]').setValue('KW-1')
    await w.get('input[name=reason]').setValue('goodwill')
    await w.get('form[data-testid=correction-form]').trigger('submit')
    await w.get('input[name=approval_password]').setValue('pw')
    await w.get('[data-testid=approval-dialog]').trigger('submit')
    await flushPromises()
    const [path, init] = POST.mock.calls.at(-1) as [string, { params: { path: { id: number }; header: Record<string, string> }; body: object }]
    expect(path).toBe('/api/v1/properties/{propertyId}/payments/{id}/refunds')
    expect(init.params.path.id).toBe(7) // the payment, not the ledger item
    expect(init.params.header['Idempotency-Key']).toBeTruthy()
    expect(init.body).toMatchObject({ amount: '30000', reason: 'goodwill', payment_method: 'CASH', reference_number: 'KW-1' })
  })

  it('offers only the refund methods the property allows, cash by default', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=refund-2]').trigger('click')
    const select = () => w.get('select[name=refund_method]').findAll('option').map((o) => o.text())
    expect(select()).toEqual(['Cash'])
    usePropertyStore().current = { id: 7, refund_methods: ['CASH', 'BANK_TRANSFER'] } as never
    await flushPromises()
    expect(select()).toEqual(['Cash', 'Bank transfer'])
  })

  it('posts an adjustment through the approval dialog', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('input[name=adjust_amount]').setValue('-50000')
    await w.get('input[name=adjust_reason]').setValue('spilled')
    await w.get('form[data-testid=adjust-form]').trigger('submit')
    expect(w.find('[data-testid=approval-dialog]').exists()).toBe(true)
    await w.get('input[name=approval_password]').setValue('pw')
    await w.get('[data-testid=approval-dialog]').trigger('submit')
    await flushPromises()
    const [path, init] = POST.mock.calls.at(-1) as [string, { body: object }]
    expect(path).toBe('/api/v1/properties/{propertyId}/folios/{id}/adjustments')
    expect(init.body).toMatchObject({ charge_code_id: 1, amount: '-50000', reason: 'spilled', approval: { email: 'clerk@hotel.com', password: 'pw' } })
    expect(w.get('[data-testid=notice]').text()).toBe('Adjustment posted.')
  })

  it('shows field errors of a charge', async () => {
    const w = mountView()
    await flushPromises()
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'VALIDATION_FAILED', detail: 'the charge is invalid', errors: [{ field: 'quantity', code: 'INVALID_VALUE', message: 'a positive number' }] }))
    await w.get('form[data-testid=charge-form]').trigger('submit')
    await flushPromises()
    expect(w.get('form[data-testid=charge-form] [role=alert]').text()).toBe('a positive number')
  })

  it('closes a folio without a stay', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=close]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]).toEqual(['/api/v1/properties/{propertyId}/folios/{id}/close', { params: { path: { propertyId: 7, id: 3 } }, body: { version: 4 } }])
    const linked = mountView(folio({ stay_id: 12 }))
    await flushPromises()
    expect(linked.find('[data-testid=close]').exists()).toBe(false)
  })

  it('prints the bill of an open folio and the invoice of a closed one, and a receipt per payment', async () => {
    const w = mountView(folio(), [...ALL, 'reservation.read'])
    await flushPromises()
    expect(w.get('[data-testid=print-invoice]').text()).toBe('Print bill')
    await w.get('[data-testid=print-invoice]').trigger('click')
    expect(openPdf).toHaveBeenLastCalledWith('/api/v1/properties/7/folios/3/invoice.pdf')
    expect(w.find('[data-testid=receipt-1]').exists()).toBe(false) // a charge has no receipt
    await w.get('[data-testid=receipt-2]').trigger('click')
    expect(openPdf.mock.calls.at(-1)?.[0]).toMatch(/^\/api\/v1\/properties\/7\/payments\/\d+\/receipt\.pdf$/)
    const closed = mountView(folio({ status: 'CLOSED' }), [...ALL, 'reservation.read'])
    await flushPromises()
    expect(closed.get('[data-testid=print-invoice]').text()).toBe('Print invoice')
  })

  it('needs reservation.read to print, and shows a refusal', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.find('[data-testid=print-invoice]').exists()).toBe(false) // the document names the reservation and the guest
    const p = mountView(folio(), [...ALL, 'reservation.read'])
    await flushPromises()
    openPdf.mockRejectedValue(new ApiError({ type: 't', title: 'Forbidden', status: 403, code: 'PERMISSION_DENIED', detail: 'no' }))
    await p.get('[data-testid=print-invoice]').trigger('click')
    await flushPromises()
    expect(p.get('[data-testid=form-error]').text()).toContain('PERMISSION_DENIED')
  })

  it('transfers part of the balance to a company with an Idempotency-Key', async () => {
    const w = mountView(folio(), [...ALL, 'cityledger.transfer'])
    await flushPromises()
    expect(w.findAll('select[name=transfer_company] option').map((o) => o.text())).toEqual(['ACME · Acme Corp'])
    await w.get('input[name=transfer_amount]').setValue('15000')
    await w.get('input[name=transfer_reference]').setValue('PO-7')
    await w.get('[data-testid=transfer-form]').trigger('submit')
    await flushPromises()
    const [path, opts] = POST.mock.calls[0] as [string, { params: { header: Record<string, string> }; body: unknown }]
    expect(path).toBe('/api/v1/properties/{propertyId}/folios/{id}/city-ledger-transfers')
    expect(opts.params.header['Idempotency-Key']).toBeTruthy()
    expect(opts.body).toEqual({ company_id: 21, amount: '15000', reference_number: 'PO-7' })
    expect(w.get('[data-testid=notice]').text()).toContain('city ledger')
  })

  it('shows a refused transfer and hides the form from other roles and closed folios', async () => {
    const w = mountView(folio(), [...ALL, 'cityledger.transfer'])
    await flushPromises()
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'CREDIT_LIMIT_EXCEEDED', detail: 'the transfer would take the company above its credit limit' }))
    await w.get('input[name=transfer_amount]').setValue('15000')
    await w.get('[data-testid=transfer-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('CREDIT_LIMIT_EXCEEDED')
    expect(mountView().find('[data-testid=transfer-form]').exists()).toBe(false)
    const closed = mountView(folio({ status: 'CLOSED' }), [...ALL, 'cityledger.transfer'])
    await flushPromises()
    expect(closed.find('[data-testid=transfer-form]').exists()).toBe(false)
  })

  it('explains when the role cannot pick a company, and offers no refund of a transfer', async () => {
    const denied = mountView(folio(), [...ALL, 'cityledger.transfer'], new ApiError({ type: 't', title: 'Forbidden', status: 403, code: 'PERMISSION_DENIED', detail: 'no' }))
    await flushPromises()
    expect(denied.get('[data-testid=transfer-denied]').text()).toContain('cityledger.read')
    const transferred = mountView(folio({ items: [item({ id: 2, transaction_type: 'PAYMENT', description: 'Payment PAY000002 (CITY_LEDGER)', debit: '0', credit: '5000', payment_id: 8, components: [] })] }))
    await flushPromises()
    expect(transferred.find('[data-testid=refund-2]').exists()).toBe(false) // the company's receipt settles it
    expect(transferred.find('[data-testid=void-2]').exists()).toBe(true)
  })

  it('needs folio.read', async () => {
    const w = mountView(folio(), ['guest.read'])
    await flushPromises()
    expect(w.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })

  const tabsOf = (w: ReturnType<typeof mountView>) => w.findAll('[data-testid=post-tabs] button').map((b) => b.text())
  const shown = (w: ReturnType<typeof mountView>, form: string) => !w.get(`[data-testid=${form}]`).element.closest('.hidden')

  it('has one tab for each posting form the role may use, the first one open', async () => {
    const w = mountView()
    await flushPromises()
    expect(tabsOf(w)).toEqual(['Charge', 'Payment', 'Adjustment'])
    expect(shown(w, 'charge-form')).toBe(true)
    expect(shown(w, 'payment-form')).toBe(false)
    const all = mountView(folio(), [...ALL, 'cityledger.transfer'])
    await flushPromises()
    expect(tabsOf(all)).toEqual(['Charge', 'Payment', 'Transfer', 'Adjustment'])
    const cashier = mountView(folio(), ['folio.read', 'payment.post'])
    await flushPromises()
    expect(tabsOf(cashier)).toEqual(['Payment'])
    expect(shown(cashier, 'payment-form')).toBe(true)
    const reader = mountView(folio(), ['folio.read'])
    await flushPromises()
    expect(reader.find('[data-testid=post-tabs]').exists()).toBe(false)
  })

  it('switches between the forms and keeps what was typed in the others', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('input[name=quantity]').setValue('3')
    const tab = w.get('[data-testid=tab-payment]')
    await tab.trigger('mousedown', { button: 0 })
    await tab.trigger('focus')
    await flushPromises()
    expect(shown(w, 'payment-form')).toBe(true)
    expect(shown(w, 'charge-form')).toBe(false)
    expect((w.get('input[name=quantity]').element as HTMLInputElement).value).toBe('3')
  })

  it('shows the status, the totals and the figures of the open ledger', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('h1').text()).toBe('Folio FOL000001')
    expect(w.get('[data-testid=folio-status]').text()).toBe('Open')
    expect(w.get('[data-testid=debit]').text()).toBe('122100')
    expect(w.get('[data-testid=credit]').text()).toBe('100000')
    const closed = mountView(folio({ status: 'CLOSED' }))
    await flushPromises()
    expect(closed.get('[data-testid=folio-status]').text()).toBe('Closed')
  })

  it('strikes a reversed item through and shows the detail of an item under it', async () => {
    const w = mountView(folio({ items: [item({ id: 5, reversed_by_item_id: 6, reason: 'posted twice' })] }))
    await flushPromises()
    expect(w.get('[data-testid=item-5]').classes()).toContain('line-through')
    await w.get('[data-testid=toggle-5]').trigger('click')
    const detail = w.get('[data-testid=detail-5]').text()
    expect(detail).toContain('1 x 100000 (exclusive) · net 100000 · posted twice')
    await w.get('[data-testid=toggle-5]').trigger('click')
    expect(w.find('[data-testid=detail-5]').exists()).toBe(false)
  })

  it('speaks Indonesian: the page, the approval dialog and the notice', async () => {
    setLocale('id')
    const w = mountView()
    await flushPromises()
    expect(w.find('[data-testid=print-invoice]').exists()).toBe(false) // no reservation.read
    expect(tabsOf(w)).toEqual(['Biaya', 'Pembayaran', 'Penyesuaian'])
    await w.get('[data-testid=reverse-1]').trigger('click')
    await w.get('input[name=reason]').setValue('dobel')
    await w.get('form[data-testid=correction-form]').trigger('submit')
    expect(w.get('[data-testid=approval-dialog]').text()).toContain('Setujui reversal')
    expect(w.get('[data-testid=approval-dialog]').text()).toContain('Kata sandi penyetuju')
    await w.get('input[name=approval_password]').setValue('pw')
    await w.get('[data-testid=approval-dialog]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=notice]').text()).toBe('Item di-reverse.')
    setLocale('en')
  })
})
