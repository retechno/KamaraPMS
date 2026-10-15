import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import PosFloorView from './PosFloorView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const outlet = { id: 1, code: 'R', name: 'Restoran Teratai', drawer: 'DRAWER1', allow_room_charge: true }
const bill = (over: object) => ({ id: 10, bill_number: 'R-0123', status: 'OPEN', order_type: 'DINE_IN', table_id: 11, table_code: 'T5', guest_count: 2, target_room: '', customer_name: '', waiter_name: 'Ketut',
  opened_at: '2026-10-15T05:00:00Z', minutes: 34, bill_requested_at: null, closed_at: null, total: '453750', draft_lines: 0, sent_lines: 3, version: 2, ...over })
const table = (id: number, code: string, status: string, bills: object[] = [], holds: object[] = []) => ({ id, area_id: 5, code, capacity: 4, shape: 'SQUARE', x: (id - 10) * 6, y: 2, w: 3, h: 3, is_active: true, status, bills, holds })
const floor = (over: object = {}) => ({
  business_date: '2026-10-15', outlet, now: '2026-10-15T05:34:00Z', holds: [{ id: 1, table_id: 13, table_code: 'T7', from_time: '19:00', name: 'Kel. Santoso', party_size: 6, business_date: '2026-10-15', status: 'HELD' }],
  open_bills: [bill({}), bill({ id: 11, bill_number: 'B-0004', table_id: null, table_code: '', order_type: 'TAKEAWAY', customer_name: 'Pak Made', total: '90000', minutes: 5, guest_count: null })],
  areas: [{ id: 5, name: 'Indoor', grid_w: 40, grid_h: 24, layout_version: 1, marks: [{ kind: 'WINDOW', label: 'Jendela', x: 0, y: 0, w: 10, h: 1 }],
    tables: [table(11, 'T5', 'AWAITING_PAYMENT', [bill({ bill_requested_at: '2026-10-15T05:30:00Z' })]), table(12, 'T6', 'FREE'), table(13, 'T7', 'HELD', [], [{ id: 1, from_time: '19:00', name: 'Kel. Santoso' }])] }],
  ...over,
})

const mounted: VueWrapper[] = []
const body = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)
function setup(permissions = ['pos.order', 'pos.cashier', 'pos.report'], data: object = floor()) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'k@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/pos/outlets')) return { data: { data: [outlet] } }
    if (path.endsWith('/floor')) return { data }
    return { data: null }
  })
  POST = vi.fn(async (path: string) => (path.endsWith('/bills') ? { data: { id: 99, bill_number: 'R-0200' } } : { data: null }))
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  const push = vi.spyOn(router, 'push')
  const w = mount(PosFloorView, { global: { plugins: [pinia, router] }, attachTo: document.body })
  mounted.push(w)
  return { w, push }
}

beforeEach(async () => {
  localStorage.clear()
  await setLocale('en')
})
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
})

describe('the cashier floor plan', () => {
  it('draws every table with its state, its total and its minutes', async () => {
    const { w } = setup()
    await flushPromises()
    const t5 = w.get('[data-testid=table-T5]')
    expect(t5.attributes('data-status')).toBe('AWAITING_PAYMENT')
    expect(t5.text()).toContain('453,750')
    expect(t5.text()).toContain('34 min')
    expect(t5.text()).toContain('2 guests')
    expect(w.get('[data-testid=table-T6]').attributes('data-status')).toBe('FREE')
    expect(w.get('[data-testid=table-T7]').attributes('data-status')).toBe('HELD')
    expect(w.get('[data-testid=table-T7]').text()).toContain('19:00')
  })

  it('lists the open bills and searches them by table, number or name', async () => {
    const { w } = setup()
    await flushPromises()
    expect(w.findAll('[data-testid^=bill-][href]')).toHaveLength(2)
    await w.get('[data-testid=bill-search]').setValue('made')
    expect(w.findAll('[data-testid^=bill-][href]')).toHaveLength(1)
    expect(w.get('[data-testid=bill-11]').text()).toContain('B-0004')
  })

  it('opens the bill of a table that has one, and asks for the guests of one that has none', async () => {
    const { w, push } = setup()
    await flushPromises()
    await w.get('[data-testid=table-T5]').trigger('click')
    expect(push).toHaveBeenCalledWith('/pos/bills/10')
    await w.get('[data-testid=table-T6]').trigger('click')
    await flushPromises()
    await body('[data-testid=guest-count]').setValue('3')
    await body('form').trigger('submit')
    await flushPromises()
    const call = POST.mock.calls.find((c) => String(c[0]).endsWith('/bills'))!
    expect(call[1].body).toMatchObject({ order_type: 'DINE_IN', table_id: 12, guest_count: 3 })
    expect(call[1].body.client_uuid).toMatch(/\S{8}/)
    expect(push).toHaveBeenCalledWith('/pos/bills/99')
  })

  it('opens a bill without a table for a takeaway', async () => {
    const { w, push } = setup()
    await flushPromises()
    await w.get('[data-testid=new-takeaway]').trigger('click')
    await body('[data-testid=customer]').setValue('Bu Sari')
    await body('[data-testid=takeaway-dialog] form').trigger('submit')
    await flushPromises()
    const call = POST.mock.calls.find((c) => String(c[0]).endsWith('/bills'))!
    expect(call[1].body).toMatchObject({ order_type: 'TAKEAWAY', customer_name: 'Bu Sari' })
    expect(push).toHaveBeenCalledWith('/pos/bills/99')
  })

  it('offers the drawer to a cashier only, and the report to who may read it', async () => {
    const waiter = setup(['pos.order'])
    await flushPromises()
    expect(waiter.w.find('[data-testid=open-drawer]').exists()).toBe(false)
    expect(waiter.w.find('[data-testid=to-report]').exists()).toBe(false)
    const cashier = setup()
    await flushPromises()
    expect(cashier.w.find('[data-testid=open-drawer]').exists()).toBe(true)
    expect(cashier.w.find('[data-testid=to-report]').exists()).toBe(true)
  })

  it('says so when the outlet has no table', async () => {
    const { w } = setup(['pos.order'], floor({ areas: [] }))
    await flushPromises()
    expect(w.find('[data-testid=no-tables]').exists()).toBe(true)
  })

  it('speaks Indonesian', async () => {
    await setLocale('id')
    const { w } = setup()
    await flushPromises()
    expect(w.get('[data-testid=table-T5]').text()).toContain('34 mnt')
    expect(w.get('[data-testid=legend]').text()).toContain('Bill diminta')
  })
})
