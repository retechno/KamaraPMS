import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import PosWaiterFloorView from './PosWaiterFloorView.vue'
import PosWaiterOrderView from './PosWaiterOrderView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PUT = vi.fn()
let DELETE = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PUT: (...a: unknown[]) => PUT(...a), DELETE: (...a: unknown[]) => DELETE(...a) } }))
type Opts = { channels: () => string[]; onEvent: (e: { id: number; channel: string; type: string; entity_id: number; at: string }) => void; onReload?: () => void }
let stream: Opts | null = null
vi.mock('@/composables/usePosEvents', async (orig) => ({
  ...(await orig<typeof import('@/composables/usePosEvents')>()),
  usePosEvents: (o: Opts) => {
    stream = o
    return { state: { value: 'live' }, stop: () => undefined }
  },
}))

const outlet = { id: 1, code: 'R', name: 'Restoran Teratai', is_active: true }
const table = (id: number, code: string, status: string, bills: object[] = []) => ({ id, code, capacity: 4, shape: 'SQUARE', x: 1, y: 1, w: 2, h: 2, is_active: true, status, bills, holds: [] })
const floor = {
  business_date: '2026-10-15', outlet, now: '2026-10-15T05:34:00Z', holds: [],
  areas: [{ id: 1, name: 'Indoor', grid_w: 40, grid_h: 24, layout_version: 1, marks: [], tables: [
    table(11, 'T5', 'OCCUPIED', [{ id: 10, bill_number: 'R-0123', table_code: 'T5', total: '453750', guest_count: 2, minutes: 34 }]),
    table(12, 'T6', 'FREE'),
  ] }],
  open_bills: [{ id: 10, bill_number: 'R-0123', table_code: 'T5', order_type: 'DINE_IN', total: '453750', guest_count: 2, minutes: 34, customer_name: '' }],
}
const line = (over: object) => ({
  id: 1, client_uuid: 'u', product_id: 22, product_name: 'Jus Alpukat', options: [], quantity: '2', unit_price: '45000', discount_amount: '0', is_comp: false, status: 'SENT', round: 1,
  base_amount: '90000', net_amount: '90000', service_amount: '9000', tax_amount: '9900', total: '108900', ticket_status: 'COOKING', created_at: '2026-10-15T05:00:00Z', ...over,
})
const totals = { base: '90000', discount: '0', net: '90000', service: '9000', tax: '9900', sold: '108900', drafts: '0', rounding: '0', paid: '0', transferred: '0', balance: '108900', payable: '108900', cash_payable: '108900' }
const bill = (lines: object[] = [line({})], over: object = {}) => ({
  id: 10, client_uuid: 'b', outlet_id: 1, outlet_code: 'R', outlet_name: 'Restoran Teratai', folio_id: 5, bill_number: 'R-0123', status: 'OPEN', order_type: 'DINE_IN', table_id: 11, table_code: 'T5',
  guest_count: 2, waiter_name: 'Ketut', comp_treatment: 'PAYMENT_COMP', business_date_opened: '2026-10-15', opened_at: '2026-10-15T05:00:00Z', version: 1, lines, payments: [], room_charges: [], totals, ...over,
})
const catalog = {
  outlet_id: 1, business_date: '2026-10-15', sold_out: [25],
  categories: [{ id: 1, code: 'DRINKS', name: 'Minuman' }, { id: 2, code: 'MAINS', name: 'Makanan utama' }],
  products: [
    { id: 21, code: 'TEH', name: 'Es Teh Manis', category_id: 1, price: '25000', option_groups: [] },
    { id: 22, code: 'JUS', name: 'Jus Alpukat', category_id: 1, price: '45000', option_groups: [] },
    { id: 25, code: 'SMOOTH', name: 'Smoothie Mangga', category_id: 1, price: '48000', option_groups: [] },
    { id: 23, code: 'STEAK', name: 'Steak', category_id: 2, price: '150000', option_groups: [{ id: 7, name: 'Kematangan', kind: 'VARIANT', min_select: 1, max_select: 1, options: [{ id: 71, name: 'Medium', price_delta: '0', is_default: true, is_active: true }, { id: 72, name: 'Well done', price_delta: '5000', is_default: false, is_active: true }] }] },
  ],
}

const mounted: VueWrapper[] = []
const body = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)
const has = (sel: string) => document.body.querySelector(sel) !== null

async function mountAt(component: object, path: string, current = bill(), permissions = ['pos.order']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'w@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (p: string) => {
    if (p.endsWith('/pos/outlets')) return { data: { data: [outlet] } }
    if (p.endsWith('/floor')) return { data: floor }
    if (p.endsWith('/pos/bills/{id}')) return { data: current }
    if (p.endsWith('/catalog')) return { data: catalog }
    return { data: null }
  })
  POST = vi.fn(async (p: string) => (p.endsWith('/bills') ? { data: { id: 99, bill_number: 'R-0200' } } : { data: current }))
  PUT = vi.fn(async () => ({ data: current }))
  DELETE = vi.fn(async () => ({ data: current }))
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/pos/waiter/bills/:id', component }, { path: '/:p(.*)*', component: { template: '<div />' } }] })
  const push = vi.spyOn(router, 'push')
  void router.push(path)
  await router.isReady()
  const w = mount(component, { global: { plugins: [pinia, router] }, attachTo: document.body })
  mounted.push(w)
  await flushPromises()
  return { w, push }
}

beforeEach(async () => {
  localStorage.clear()
  stream = null
  await setLocale('id')
})
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
})

describe('the waiter: the tables on a phone', () => {
  it('draws the tables with their state in words and the total of an open bill', async () => {
    const { w } = await mountAt(PosWaiterFloorView, '/pos/waiter')
    expect(w.get('[data-testid=table-T5]').attributes('data-status')).toBe('OCCUPIED')
    expect(w.get('[data-testid=table-T5]').text()).toContain('453.750')
    expect(w.get('[data-testid=table-T6]').text()).toContain('Kosong')
    expect(w.get('[data-testid=waiter-bills]').text()).toContain('R-0123')
    expect(stream?.channels()).toEqual(['outlet:1'])
  })

  it('continues the bill of a table that has one', async () => {
    const { w, push } = await mountAt(PosWaiterFloorView, '/pos/waiter')
    await w.get('[data-testid=table-T5]').trigger('click')
    expect(push).toHaveBeenCalledWith('/pos/waiter/bills/10')
  })

  it('asks the number of guests before it opens a free table, and goes to its order', async () => {
    const { w, push } = await mountAt(PosWaiterFloorView, '/pos/waiter')
    await w.get('[data-testid=table-T6]').trigger('click')
    expect(has('[data-testid=guests-dialog]')).toBe(true)
    await body('[data-testid=guests-more]').trigger('click')
    expect(document.body.querySelector('[data-testid=guests-count]')?.textContent).toBe('3')
    await body('[data-testid=guests-open]').trigger('click')
    await flushPromises()
    const call = POST.mock.calls.find((c) => String(c[0]).endsWith('/bills'))!
    expect(call[1].body).toMatchObject({ order_type: 'DINE_IN', table_id: 12, guest_count: 3 })
    expect(push).toHaveBeenCalledWith('/pos/waiter/bills/99')
  })

  it('reads the floor again when the stream says a table changed', async () => {
    vi.useFakeTimers()
    try {
      await mountAt(PosWaiterFloorView, '/pos/waiter')
      const n = () => GET.mock.calls.filter((c) => String(c[0]).endsWith('/floor')).length
      expect(n()).toBe(1)
      stream?.onEvent({ id: 5, channel: 'outlet:1', type: 'table.updated', entity_id: 12, at: '' })
      await vi.advanceTimersByTimeAsync(300)
      await flushPromises()
      expect(n()).toBe(2)
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('the waiter: the order on a phone', () => {
  const at = '/pos/waiter/bills/10'

  it('shows the table, the guests and the bill in the header, and what was ordered already', async () => {
    const { w } = await mountAt(PosWaiterOrderView, at)
    expect(w.get('[data-testid=waiter-title]').text()).toContain('Meja T5 · 2 tamu')
    expect(w.get('[data-testid=waiter-title]').text()).toContain('R-0123 · Restoran Teratai')
    expect(w.get('[data-testid=product-22] [data-testid=ordered]').text()).toContain('sudah dipesan 2')
    expect(w.get('[data-testid=product-21]').text()).not.toContain('sudah dipesan')
  })

  it('has "Tambah" that becomes "− n +" and sends the product once', async () => {
    const { w } = await mountAt(PosWaiterOrderView, at, bill([]))
    expect(w.get('[data-testid=add-21]').text()).toBe('Tambah')
    await w.get('[data-testid=add-21]').trigger('click')
    await flushPromises()
    const add = POST.mock.calls.find((c) => String(c[0]).endsWith('/lines'))!
    expect(add[1].body).toMatchObject({ product_id: 21, quantity: '1', options: [] })
    expect(add[1].body.client_uuid).toMatch(/\S{8}/)
  })

  it('shows the stepper for a product that has a draft, and changes its quantity', async () => {
    const draft = line({ id: 5, product_id: 21, product_name: 'Es Teh Manis', status: 'DRAFT', round: 0, quantity: '2', ticket_status: '' })
    const { w } = await mountAt(PosWaiterOrderView, at, bill([draft]))
    expect(w.find('[data-testid=add-21]').exists()).toBe(false)
    expect(w.get('[data-testid=qty-21]').text()).toBe('2')
    await w.get('[data-testid=more-21]').trigger('click')
    await flushPromises()
    expect(PUT.mock.calls[0]![1].body).toMatchObject({ quantity: '3' })
    await w.get('[data-testid=less-21]').trigger('click')
    await flushPromises()
    expect(PUT.mock.calls[1]![1].body).toMatchObject({ quantity: '1' })
  })

  it('removes the draft when it goes below one', async () => {
    const draft = line({ id: 5, product_id: 21, product_name: 'Es Teh Manis', status: 'DRAFT', round: 0, quantity: '1', ticket_status: '' })
    const { w } = await mountAt(PosWaiterOrderView, at, bill([draft]))
    await w.get('[data-testid=less-21]').trigger('click')
    await flushPromises()
    expect(DELETE).toHaveBeenCalledTimes(1)
  })

  it('cannot choose a product that is sold out today', async () => {
    const { w } = await mountAt(PosWaiterOrderView, at, bill([]))
    const row = w.get('[data-testid=product-25]')
    expect(row.attributes('data-sold-out')).toBe('true')
    expect(row.text()).toContain('Habis')
    expect(row.find('[data-testid=add-25]').exists()).toBe(false)
    expect(POST).not.toHaveBeenCalled()
  })

  it('opens the sheet of a product with options: variant, and a note for the kitchen', async () => {
    const { w } = await mountAt(PosWaiterOrderView, at, bill([]))
    await w.get('[data-testid=cat-2]').trigger('click')
    await w.get('[data-testid=add-23]').trigger('click')
    expect(has('[data-testid=options-dialog]')).toBe(true)
    await body('[data-testid=option-72]').trigger('change')
    await body('[data-testid=options-note]').setValue('tanpa garam')
    await body('[data-testid=options-dialog] form').trigger('submit')
    await flushPromises()
    const add = POST.mock.calls.find((c) => String(c[0]).endsWith('/lines'))!
    expect(add[1].body).toMatchObject({ product_id: 23, options: [{ option_id: 72 }], note: 'tanpa garam' })
  })

  it('sends the drafts to the kitchen and the bar, and says so in the style of a success', async () => {
    const draft = line({ id: 5, product_id: 21, product_name: 'Es Teh Manis', status: 'DRAFT', round: 0, quantity: '1', ticket_status: '' })
    const { w } = await mountAt(PosWaiterOrderView, at, bill([draft]))
    expect(w.get('[data-testid=draft-summary]').text()).toBe('1 item draf · bill 108.900')
    await w.get('[data-testid=waiter-send]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls.find((c) => String(c[0]).endsWith('/send'))).toBeTruthy()
    expect(w.get('[data-testid=waiter-notice]').classes()).toContain('notice')
  })

  it('has nothing to send without drafts, and no payment, void or discount at all', async () => {
    const { w } = await mountAt(PosWaiterOrderView, at)
    expect((w.get('[data-testid=waiter-send]').element as HTMLButtonElement).disabled).toBe(true)
    const text = w.text()
    for (const word of ['Bayar', 'Void', 'Diskon', 'Komplimen']) expect(text).not.toContain(word)
  })

  it('lets the waiter look at the bill: lines with the status of the ticket and the totals', async () => {
    const { w } = await mountAt(PosWaiterOrderView, at)
    await w.get('[data-testid=view-bill]').trigger('click')
    const dlg = document.body.querySelector('[data-testid=bill-dialog]')!.textContent!
    expect(dlg).toContain('2 × Jus Alpukat')
    expect(dlg).toContain('Dimasak')
    expect(dlg).toContain('108.900')
  })

  it('shows the other waiter\'s line when the stream says the bill changed', async () => {
    vi.useFakeTimers()
    try {
      await mountAt(PosWaiterOrderView, at)
      const n = () => GET.mock.calls.filter((c) => String(c[0]).endsWith('/pos/bills/{id}')).length
      expect(n()).toBe(1)
      stream?.onEvent({ id: 3, channel: 'outlet:1', type: 'bill.updated', entity_id: 10, at: '' })
      stream?.onEvent({ id: 4, channel: 'outlet:1', type: 'bill.updated', entity_id: 10, at: '' })
      await vi.advanceTimersByTimeAsync(300)
      await flushPromises()
      expect(n()).toBe(2)
      // the sold-out list is read again when a product is marked
      const c = () => GET.mock.calls.filter((x) => String(x[0]).endsWith('/catalog')).length
      const before = c()
      stream?.onEvent({ id: 5, channel: 'outlet:1', type: 'product.sold_out', entity_id: 21, at: '' })
      await flushPromises()
      expect(c()).toBe(before + 1)
    } finally {
      vi.useRealTimers()
    }
  })

  it('says the bill is not open any more when a payment came first, and disables the buttons', async () => {
    const { w } = await mountAt(PosWaiterOrderView, at, bill([], { status: 'CLOSED' }))
    expect(w.get('[data-testid=waiter-closed]').text()).toContain('tidak terbuka')
    expect((w.get('[data-testid=add-21]').element as HTMLButtonElement).disabled).toBe(true)
  })

  it('shows the refusal of the server', async () => {
    const { w } = await mountAt(PosWaiterOrderView, at, bill([]))
    POST = vi.fn(async () => {
      throw new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'BILL_NOT_OPEN', detail: 'the bill is not open any more' })
    })
    await w.get('[data-testid=add-21]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=waiter-error]').text()).toContain('tidak terbuka')
  })
})
