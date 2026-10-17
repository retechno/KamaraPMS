import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ApprovalChain from '@/components/inventory/ApprovalChain.vue'
import BillDraftView from './BillDraftView.vue'
import ReceiptView from './ReceiptView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PUT: vi.fn(), PATCH: vi.fn() } }))

const blank = { template: '<div />' }
const router = createRouter({
  history: createMemoryHistory(),
  routes: [
    { path: '/inventory/receipts/:id', component: blank },
    { path: '/inventory/bill-drafts/:id', component: blank },
    { path: '/:p(.*)*', component: blank },
  ],
})
const mounted: VueWrapper[] = []

// 12 cartons of 24 bottles: 288 bottles in; 288 billed; none returned
const receipt = {
  id: 8, receipt_number: 'GR-000008', status: 'POSTED', supplier_name: 'Sumber', warehouse_name: 'Main', received_on: '2026-10-16', po_id: 3, po_number: 'PO-000003', delivery_note: null, note: null, draft_id: 4, draft_status: 'OPEN',
  reverse_reason: null, reversible: true, total_value: '720000', matches: [], returns: [],
  lines: [{ line_no: 1, item_name: 'Syrup', item_code: 'SIRUP', unit_code: 'karton', stock_unit_code: 'botol', qty: '12', qty_stock: '288', unit_price: '60000', value: '720000', qty_billed_stock: '288', qty_returned_stock: '24' }],
}

async function setup(component: unknown, route: string, permissions: string[]) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'm@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  usePropertyStore().current = { id: 7, currency_decimals: 0 } as never
  await router.push(route)
  await router.isReady()
  const w = mount(component as never, { attachTo: document.body, global: { plugins: [pinia, router] } })
  mounted.push(w)
  await flushPromises()
  return w
}
const body = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)
const has = (sel: string) => document.body.querySelector(sel) !== null

beforeEach(() => {
  setLocale('en')
  POST = vi.fn().mockResolvedValue({ data: { ...receipt, status: 'REVERSED' } })
})
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
})

describe('a posted receipt', () => {
  beforeEach(() => {
    GET = vi.fn(async () => ({ data: receipt }))
  })

  it('shows the quantity, what was billed and what was returned in one unit, with the stock unit beside it', async () => {
    await setup(ReceiptView, '/inventory/receipts/8', ['inventory.view', 'inventory.receive'])
    expect(body('[data-testid=line-received]').text()).toBe('12 karton (288 botol)')
    expect(body('[data-testid=line-billed]').text()).toBe('12 karton (288 botol)')
    expect(body('[data-testid=line-returned]').text()).toBe('1 karton (24 botol)')
  })

  it('shows a dash where nothing was returned', async () => {
    GET = vi.fn(async () => ({ data: { ...receipt, lines: [{ ...receipt.lines[0]!, qty_returned_stock: '0' }] } }))
    await setup(ReceiptView, '/inventory/receipts/8', ['inventory.view'])
    expect(body('[data-testid=line-returned]').text()).toBe('–')
  })

  it('asks the reason in a dialog after the click, not beside the buttons', async () => {
    await setup(ReceiptView, '/inventory/receipts/8', ['inventory.view', 'inventory.receive'])
    expect(has('[name=reason]')).toBe(false)
    body('[data-testid=receipt-reverse]').element.dispatchEvent(new Event('click', { bubbles: true }))
    await flushPromises()
    expect(has('[data-testid=reverse-dialog]')).toBe(true)
    expect((body('[data-testid=reverse-confirm]').element as HTMLButtonElement).disabled).toBe(true)
    const input = body('[data-testid=reverse-reason]')
    await input.setValue('Wrong delivery')
    expect((body('[data-testid=reverse-confirm]').element as HTMLButtonElement).disabled).toBe(false)
    body('[data-testid=reverse-confirm]').element.dispatchEvent(new Event('click', { bubbles: true }))
    await flushPromises()
    expect(POST).toHaveBeenCalledTimes(1)
    expect(POST.mock.calls[0]![1].body).toEqual({ reason: 'Wrong delivery' })
  })
})

describe('the approval chain card', () => {
  const step = (level_no: number, status: string, extra: Record<string, unknown> = {}) => ({ id: level_no, cycle: 1, level_no, name: `L${level_no}`, status, approver_kind: 'DEPT_HEAD', permission: null, condition_kind: 'ALWAYS', threshold: null, applies: true, skip_reason: null, approvers: [], ...extra })
  const request = (steps: unknown[]) => ({ status: 'SUBMITTED', cycle: 1, requester_name: 'Dewi', submitted_at: '2026-10-16T01:00:00Z', steps, edits: [] })

  it('says how many of the levels apply when the last one is not needed', async () => {
    const w = mount(ApprovalChain, { props: { request: request([step(1, 'APPROVED'), step(2, 'ACTIVE'), step(3, 'SKIPPED', { condition_kind: 'TOTAL_ABOVE', threshold: '5000000', skip_reason: 'CONDITION' })]) as never } })
    expect(w.get('h2').text()).toBe('Approval chain (2 of 3 levels apply)')
    w.unmount()
  })

  it('counts every level when all apply', async () => {
    const w = mount(ApprovalChain, { props: { request: request([step(1, 'APPROVED'), step(2, 'ACTIVE')]) as never } })
    expect(w.get('h2').text()).toBe('Approval chain (2 levels)')
    w.unmount()
  })
})

describe('the split of a price difference', () => {
  const draft = {
    id: 4, receipt_id: 8, receipt_number: 'GR-000008', supplier_id: 2, supplier_name: 'Sumber', status: 'OPEN', matches: [],
    lines: [{ line_no: 1, item_name: 'Syrup', item_code: 'SIRUP', unit_code: 'karton', stock_unit_code: 'botol', qty: '12', billable_qty: '12', unit_price: '60000' }],
  }
  const previewLine = (inventory_part: string, ppv_part: string) => ({ receipt_id: 8, receipt_line_no: 1, item_name: 'Syrup', billed_qty_stock: '288', on_hand_qty_stock: '768', price_variance: '1000', variance: '1000', inventory_part, ppv_part })
  const preview = (l: unknown) => ({ lines: [l], receipt_total: '720000', stock_invoice_net: '721000', extra_total: '0', vat_total: '0', bill_total: '721000', variance: '1000', approval_amount: '1000', tolerance_amount: '5000', approval_required: false, inventory_part: '1000', ppv_part: '0', vat_treatment: 'CAPITALISE' })

  async function split(l: unknown): Promise<string> {
    GET = vi.fn(async (path: string) => (path.includes('bill-drafts') ? { data: draft } : { data: { data: [] } }))
    POST = vi.fn().mockResolvedValue({ data: preview(l) })
    vi.useFakeTimers({ shouldAdvanceTime: true })
    await setup(BillDraftView, '/inventory/bill-drafts/4', ['inventory.bill', 'payables.post', 'inventory.view'])
    await body('[name=invoice_number]').setValue('INV-1')
    await body('[name=bill_date]').setValue('2026-10-16')
    await vi.advanceTimersByTimeAsync(400)
    await flushPromises()
    vi.useRealTimers()
    return body('[data-testid=split-line-1]').text()
  }

  it('writes the quantities with their unit and says where the whole difference goes', async () => {
    expect(await split(previewLine('1000', '0'))).toBe('Syrup: 288 botol billed · 768 botol still in the warehouse → the whole difference goes into the value of stock')
  })

  it('says when part of the difference goes to the purchase price variance', async () => {
    expect(await split(previewLine('400', '600'))).toContain('part goes into the value of stock, the rest into purchase price variance')
  })
})
