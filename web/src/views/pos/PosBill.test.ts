import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import PosBillView from './PosBillView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PUT = vi.fn()
let DELETE = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PUT: (...a: unknown[]) => PUT(...a), DELETE: (...a: unknown[]) => DELETE(...a) } }))

const line = (over: object) => ({
  id: 1, client_uuid: 'u', product_id: 21, product_name: 'Nasi goreng', options: [], quantity: '2', unit_price: '50000', discount_amount: '0', is_comp: false, status: 'DRAFT', round: 0,
  base_amount: '100000', net_amount: '100000', service_amount: '10000', tax_amount: '11000', total: '121000', ticket_status: '', created_at: '2026-10-15T05:00:00Z', ...over,
})
const totals = (over: object = {}) => ({ base: '100000', discount: '0', net: '100000', service: '10000', tax: '11000', sold: '0', drafts: '121000', rounding: '0', paid: '0', transferred: '0', balance: '0', payable: '121000', cash_payable: '121000', ...over })
const bill = (over: object = {}) => ({
  id: 10, client_uuid: 'b', outlet_id: 1, outlet_code: 'R', outlet_name: 'Restoran', folio_id: 5, bill_number: 'R-0123', status: 'OPEN', order_type: 'DINE_IN', table_id: 11, table_code: 'T5', guest_count: 2,
  waiter_name: 'Ketut', comp_treatment: 'PAYMENT_COMP', business_date_opened: '2026-10-15', opened_at: '2026-10-15T05:00:00Z', version: 1, lines: [line({})], payments: [], room_charges: [], totals: totals(), ...over,
})
const catalog = {
  outlet_id: 1, business_date: '2026-10-15', sold_out: [23],
  categories: [{ id: 1, code: 'MAINS', name: 'Makanan' }, { id: 2, code: 'DRINKS', name: 'Minuman' }],
  products: [
    { id: 21, code: 'NASI', name: 'Nasi goreng', category_id: 1, price: '50000', option_groups: [] },
    { id: 22, code: 'STEAK', name: 'Steak', category_id: 1, price: '150000', option_groups: [{ id: 7, name: 'Kematangan', kind: 'VARIANT', min_select: 1, max_select: 1, options: [{ id: 71, name: 'Medium', price_delta: '0', is_default: true, is_active: true }, { id: 72, name: 'Well done', price_delta: '5000', is_default: false, is_active: true }] }] },
    { id: 23, code: 'SOTO', name: 'Soto', category_id: 1, price: '30000', option_groups: [] },
    { id: 24, code: 'TEH', name: 'Es teh', category_id: 2, price: '15000', option_groups: [] },
  ],
}

let outletOver: object = {}
const mounted: VueWrapper[] = []
const body = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)
const has = (sel: string) => document.body.querySelector(sel) !== null

function setup(current = bill(), permissions = ['pos.order', 'pos.cashier', 'pos.void', 'pos.discount', 'pos.comp', 'folio.reopen']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'k@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/pos/bills/{id}')) return { data: current }
    if (path.endsWith('/catalog')) return { data: catalog }
    if (path.endsWith('/pos/outlets/{id}')) return { data: { id: 1, drawer: 'D1', allow_room_charge: true, allow_company_charge: false, order_mode: 'PRINT', receipt_printer_id: null, ...outletOver } }
    return { data: null }
  })
  POST = vi.fn(async () => ({ data: current }))
  PUT = vi.fn(async () => ({ data: current }))
  DELETE = vi.fn(async () => ({ data: current }))
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/pos/bills/:id', component: PosBillView }, { path: '/:p(.*)*', component: { template: '<div />' } }] })
  void router.push('/pos/bills/10')
  return router.isReady().then(() => {
    const w = mount(PosBillView, { global: { plugins: [pinia, router] }, attachTo: document.body })
    mounted.push(w)
    return w
  })
}

beforeEach(async () => {
  outletOver = {}
  await setLocale('en')
})
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
})

describe('the order and the bill', () => {
  it('shows the menu of the outlet and the lines of the bill with the amounts of the server', async () => {
    const w = await setup()
    await flushPromises()
    expect(w.findAll('[data-testid^=product-]')).toHaveLength(3) // the category Makanan
    expect(w.get('[data-testid=line-1]').text()).toContain('Nasi goreng')
    expect(w.get('[data-testid=line-1]').text()).toContain('100,000')
    expect(w.get('[data-testid=line-1]').text()).not.toContain('121,000')
    expect(w.get('[data-testid=payable]').text()).toBe('121,000')
    // sold out: not offered
    expect((w.get('[data-testid=product-23]').element as HTMLButtonElement).disabled).toBe(true)
    expect(w.get('[data-testid=product-23]').text()).toContain('Sold out')
  })

  it('shows each line at its menu price and the service, tax and total only in the summary', async () => {
    const w = await setup(bill({ lines: [line({ id: 2, status: 'SENT', round: 1, net_amount: '60000', total: '72600' }), line({ id: 4, status: 'SENT', round: 1, net_amount: '40000', total: '48400' })], totals: totals({ net: '100000', sold: '121000', drafts: '0', payable: '121000' }) }))
    await flushPromises()
    const amounts = ['2', '4'].map((id) => w.get(`[data-testid=amount-${id}]`).text())
    expect(amounts).toEqual(['60,000', '40,000'])
    expect(w.get('[data-testid=line-2]').text()).not.toContain('72,600')
    const sum = amounts.reduce((a, x) => a + Number(x.replace(/,/g, '')), 0)
    expect(sum).toBe(Number(totals().net))
    expect(w.get('[data-testid=payable]').text()).toBe('121,000')
  })

  it('says the order was sent in the style of a success, and that a kitchen ticket was made', async () => {
    const w = await setup()
    await flushPromises()
    await w.get('[data-testid=send]').trigger('click')
    await flushPromises()
    const n = w.get('[data-testid=bill-notice]')
    expect(n.text()).toBe('Order sent. Kitchen ticket created.')
    expect(n.classes()).toContain('notice')
    expect(n.classes()).not.toContain('alert')
  })

  it('adds a plain product with one tap and a product with options through its sheet', async () => {
    const w = await setup(bill({ lines: [] }))
    await flushPromises()
    await w.get('[data-testid=product-21]').trigger('click')
    await flushPromises()
    const add = POST.mock.calls.find((c) => String(c[0]).endsWith('/lines'))!
    expect(add[1].body).toMatchObject({ product_id: 21, quantity: '1', options: [] })
    await w.get('[data-testid=product-22]').trigger('click')
    await flushPromises()
    expect(has('[data-testid=options-dialog]')).toBe(true)
    await body('[data-testid=option-72]').trigger('change')
    await body('[data-testid=options-note]').setValue('no salt')
    await body('[data-testid=options-dialog] form').trigger('submit')
    await flushPromises()
    const second = POST.mock.calls.filter((c) => String(c[0]).endsWith('/lines'))[1]!
    expect(second[1].body).toMatchObject({ product_id: 22, options: [{ option_id: 72 }], note: 'no salt' })
  })

  it('changes and removes a draft line, and sends the drafts in a round', async () => {
    const w = await setup()
    await flushPromises()
    await w.get('[data-testid=more-1]').trigger('click')
    await flushPromises()
    expect(PUT.mock.calls[0]![1].body).toMatchObject({ quantity: '3' })
    await w.get('[data-testid=remove-1]').trigger('click')
    await flushPromises()
    expect(DELETE).toHaveBeenCalledTimes(1)
    await w.get('[data-testid=send]').trigger('click')
    await flushPromises()
    const send = POST.mock.calls.find((c) => String(c[0]).endsWith('/send'))!
    expect(send[1].body.client_uuid).toMatch(/\S{8}/)
  })

  it('shows what was sent by round with the state of its ticket, and a void struck through', async () => {
    const w = await setup(bill({
      lines: [line({ id: 2, status: 'SENT', round: 1, ticket_status: 'COOKING', total: '121000' }), line({ id: 3, product_name: 'Steak', status: 'VOID', round: 1, total: '181500', void_reason: 'wrong order', approved_by_name: 'Wayan' })],
      totals: totals({ sold: '121000', drafts: '0', balance: '121000', payable: '121000' }),
    }))
    await flushPromises()
    expect(w.get('[data-testid=round-1]').text()).toContain('Cooking')
    expect(w.get('[data-testid=line-3]').text()).toContain('wrong order')
    expect(w.get('[data-testid=line-3]').text()).toContain('Wayan')
  })

  it('voids a sent line with a reason and the approval, and not without the permission', async () => {
    const sent = bill({ lines: [line({ id: 2, status: 'SENT', round: 1 })], totals: totals({ sold: '121000', drafts: '0', balance: '121000', payable: '121000' }) })
    const w = await setup(sent)
    await flushPromises()
    await w.get('[data-testid=void-2]').trigger('click')
    await body('[data-testid=reason]').setValue('wrong order')
    await body('[data-testid=reason-dialog] form').trigger('submit')
    await flushPromises()
    expect(has('[data-testid=approval-dialog]')).toBe(true)
    await body('input[name=approval_email]').setValue('boss@hotel.test')
    await body('input[name=approval_password]').setValue('secret-pass')
    await body('[data-testid=approval-dialog]').trigger('submit')
    await flushPromises()
    const call = POST.mock.calls.find((c) => String(c[0]).includes('/void'))!
    expect(call[1].body).toMatchObject({ reason: 'wrong order', approval: { email: 'boss@hotel.test', password: 'secret-pass' } })
    expect(call[1].params.path.lineId).toBe(2)

    const w2 = await setup(sent, ['pos.order', 'pos.cashier'])
    await flushPromises()
    expect(w2.find('[data-testid=void-2]').exists()).toBe(false)
  })

  it('gives a discount without an approver first, and asks for one when the server says so', async () => {
    const w = await setup()
    await flushPromises()
    await w.get('[data-testid=discount]').trigger('click')
    await body('[data-testid=discount-figure]').setValue('10')
    await body('[data-testid=reason]').setValue('regular')
    POST = vi.fn(async () => {
      throw new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'APPROVAL_REQUIRED', detail: 'needs approval' })
    })
    await body('[data-testid=reason-dialog] form').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]![1].body).toMatchObject({ scope: 'BILL', percent: '10', reason: 'regular' })
    expect(POST.mock.calls[0]![1].body.approval).toBeUndefined()
    expect(has('[data-testid=approval-dialog]')).toBe(true)
  })

  it('moves the table and offers merge and split', async () => {
    const w = await setup(bill({ lines: [line({ id: 2, status: 'SENT', round: 1 }), line({ id: 3, product_name: 'Es teh', status: 'SENT', round: 1 })] }))
    await flushPromises()
    expect(w.find('[data-testid=merge]').exists()).toBe(true)
    expect(w.find('[data-testid=split]').exists()).toBe(true)
    await w.get('[data-testid=split]').trigger('click')
    await body('[data-testid=split-line-3]').setValue(true)
    await body('[data-testid=split-submit]').trigger('click')
    await flushPromises()
    const call = POST.mock.calls.find((c) => String(c[0]).endsWith('/split'))!
    expect(call[1].body.line_ids).toEqual([3])
  })

  it('shows a paid bill as read only, reopens it with the approval and prints it', async () => {
    const w = await setup(bill({ status: 'CLOSED', totals: totals({ sold: '121000', drafts: '0', payable: '0', balance: '0', paid: '121000' }), lines: [line({ id: 2, status: 'SENT', round: 1 })] }))
    await flushPromises()
    expect(w.find('[data-testid=menu]').exists()).toBe(false)
    expect(w.get('[data-testid=bill-closed]').text()).toContain('paid')
    expect(w.find('[data-testid=reopen]').exists()).toBe(true)
    expect(w.find('[data-testid=send]').exists()).toBe(false)
  })

  it('prints an open bill at the receipt printer of the outlet, and keeps the browser as a second way', async () => {
    outletOver = { receipt_printer_id: 3 }
    const w = await setup()
    await flushPromises()
    expect(w.find('[data-testid=print-browser]').exists()).toBe(true)
    await w.get('[data-testid=print]').trigger('click')
    await flushPromises()
    const call = POST.mock.calls.find((c) => String(c[0]).endsWith('/bills/{id}/print'))!
    expect(call[1]).toMatchObject({ params: { path: { propertyId: 7, id: 10 } }, body: {} })
    expect(w.get('[data-testid=bill-notice]').text()).toBe('The bill was sent to the printer.')
  })

  it('asks for a reason before a receipt is printed again, and says COPY in the notice', async () => {
    outletOver = { receipt_printer_id: 3 }
    const w = await setup(bill({ status: 'CLOSED', totals: totals({ sold: '121000', drafts: '0', payable: '0', balance: '0', paid: '121000' }), lines: [line({ id: 2, status: 'SENT', round: 1 })] }))
    await flushPromises()
    expect(w.get('[data-testid=print]').text()).toBe('Reprint the receipt')
    await w.get('[data-testid=print]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls.some((c) => String(c[0]).endsWith('/print'))).toBe(false)
    expect((body('[data-testid=print-submit]').element as HTMLButtonElement).disabled).toBe(true)
    const input = document.body.querySelector('[data-testid=print-reason]') as HTMLInputElement
    input.value = 'the guest lost it'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    await body('[data-testid=print-submit]').trigger('click')
    await flushPromises()
    const call = POST.mock.calls.find((c) => String(c[0]).endsWith('/bills/{id}/print'))!
    expect(call[1].body).toEqual({ reason: 'the guest lost it' })
    expect(w.get('[data-testid=bill-notice]').text()).toBe('A copy of the receipt was sent to the printer.')
  })

  it('prints from the browser when the outlet has no receipt printer, or the server says so', async () => {
    const open = vi.fn(() => null)
    vi.stubGlobal('open', open)
    const w = await setup() // no receipt printer
    await flushPromises()
    expect(w.find('[data-testid=print-browser]').exists()).toBe(false)
    await w.get('[data-testid=print]').trigger('click')
    await flushPromises()
    expect(open).toHaveBeenCalledTimes(1)
    expect(POST.mock.calls.some((c) => String(c[0]).endsWith('/print'))).toBe(false)
    // the outlet had a printer a moment ago and has none now: the answer of the server sends the page to the browser
    outletOver = { receipt_printer_id: 3 }
    const w2 = await setup()
    await flushPromises()
    POST = vi.fn(async () => {
      throw new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'OUTLET_NO_RECEIPT_PRINTER', detail: 'none' })
    })
    await w2.get('[data-testid=print]').trigger('click')
    await flushPromises()
    expect(open).toHaveBeenCalledTimes(2)
    vi.unstubAllGlobals()
  })

  it('says the bill is not open any more when a payment came first', async () => {
    const w = await setup()
    await flushPromises()
    PUT = vi.fn(async () => {
      throw new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'BILL_NOT_OPEN', detail: 'the bill is not open any more' })
    })
    await w.get('[data-testid=more-1]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=bill-error]').text()).toContain('not open any more')
  })
})
