import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { readdirSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { en } from '@/i18n/locales/en'
import { id } from '@/i18n/locales/id'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import AmenitiesPlanView from './AmenitiesPlanView.vue'
import AmenityRunView from './AmenityRunView.vue'
import CountView from './CountView.vue'
import IssueView from './IssueView.vue'
import MovementsView from './MovementsView.vue'
import TransferView from './TransferView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PUT = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PUT: (...a: unknown[]) => PUT(...a), PATCH: vi.fn() } }))
vi.mock('@/composables/useDepartments', () => ({ loadDepartments: async () => [{ id: 3, code: 'FB', name: 'Food and beverage', level: 0, is_active: true }], departmentLabel: (d: { code: string; name: string }) => `${d.code} · ${d.name}` }))
vi.mock('@/api/paging', () => ({ fetchAll: async () => [{ id: 11, code: 'STD', name: 'Standard', is_active: true }, { id: 12, code: 'STE', name: 'Suite', is_active: true }] }))

const blank = { template: '<div />' }
const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/inventory/amenities/runs/:date', component: blank }, { path: '/inventory/counts/:id', component: blank }, { path: '/:p(.*)*', component: blank }] })
const mounted: VueWrapper[] = []

const stockRow = (warehouse_id: number, qty: string) => ({ warehouse_id, warehouse_code: 'W', qty, value: '0' })
const items = [
  { id: 1, code: 'BERAS', name: 'Beras', stock_unit_id: 10, stock_unit_code: 'kg', is_active: true, units: [{ unit_id: 10, unit_code: 'kg', stock_num: 1, stock_den: 1 }], stock: [stockRow(1, '5'), stockRow(2, '-2')] },
  { id: 2, code: 'SIRUP', name: 'Sirup', stock_unit_id: 20, stock_unit_code: 'botol', is_active: true, units: [{ unit_id: 20, unit_code: 'botol', stock_num: 1, stock_den: 1 }, { unit_id: 21, unit_code: 'karton', stock_num: 12, stock_den: 1 }], stock: [stockRow(1, '40'), stockRow(2, '0')] },
]
const warehouses = [
  { id: 1, code: 'MAIN', name: 'Main store', kind: 'MAIN', is_active: true },
  { id: 2, code: 'BAR', name: 'Bar store', kind: 'DEPARTMENT', is_active: true },
]
const srLine = (line_no: number, item_id: number, name: string, left: string) => ({ line_no, item_id, item_code: name.toUpperCase(), item_name: name, unit_id: 10, unit_code: 'kg', qty: left, qty_stock: left, stock_unit_code: 'kg', qty_issued_stock: '0', qty_left_stock: left, on_hand: '5', est_value: '0', note: null })
const requisition = {
  id: 7, sr_number: 'SR-000007', status: 'APPROVED', requester_id: 9, requester_name: 'Wayan', department_id: 3, department_name: 'Food and beverage', to_warehouse_id: 2, to_warehouse_name: 'Bar store', from_warehouse_id: 1,
  from_warehouse_name: 'Main store', needed_on: null, est_total: '620000', cycle: 1, submitted_at: '2026-10-16T01:00:00Z', returned_note: null, rejected_note: null, note: null, version: 3, created_at: '2026-10-16T01:00:00Z',
  stage: { done: 2, total: 2, name: 'Final' }, lines: [srLine(1, 1, 'Beras', '8'), srLine(2, 2, 'Sirup', '12')], steps: [], edits: [], decision: null, transfers: [],
}

function reply(path: string): unknown {
  if (path.endsWith('/inventory/warehouses')) return { data: { data: warehouses } }
  if (path.endsWith('/inventory/items')) return { data: { items } }
  if (path.endsWith('/requisitions/{id}')) return { data: requisition }
  if (path.endsWith('/inventory/requisitions')) return { data: { data: [{ id: 7, sr_number: 'SR-000007', status: 'APPROVED', requester_id: 9, requester_name: 'Wayan', department_name: 'FB', to_warehouse_name: 'Bar store', from_warehouse_name: 'Main store', needed_on: null, est_total: '620000', line_count: 2, answered_by: '', submitted_at: null, created_at: '2026-10-16T01:00:00Z', stage: { done: 2, total: 2, name: 'Final' }, waiting_for_me: false }] } }
  if (path.endsWith('/inventory/transfers')) return { data: { data: [{ id: 3, trf_number: 'TRF-000003', status: 'POSTED', requisition_id: null, requisition_number: null, from_warehouse_name: 'Main store', to_warehouse_name: 'Bar store', business_date: '2026-10-16', line_count: 1, total_value: '1000', created_at: '2026-10-16T01:00:00Z' }] } }
  if (path.endsWith('/accounting/accounts')) return { data: { data: [{ id: 51, code: '5150', name: 'Guest supplies' }] } }
  return { data: { data: [] } }
}

async function setup(component: unknown, permissions: string[], route: string) {
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
  await flushPromises()
  return w
}
const body = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)
const has = (sel: string) => document.body.querySelector(sel) !== null
const click = async (sel: string) => {
  (body(sel).element as HTMLElement).click()
  await flushPromises()
}

beforeEach(() => {
  setLocale('en')
  GET = vi.fn(async (path: string) => reply(path))
  POST = vi.fn().mockResolvedValue({ data: { id: 99 } })
  PUT = vi.fn().mockResolvedValue({ data: {} })
})
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
})

describe('requisitions and transfers', () => {
  it('lists the requisitions with their stage and the transfers, and offers what the permissions allow', async () => {
    await setup(MovementsView, ['inventory.view', 'inventory.request', 'inventory.move'], '/inventory/requisitions')
    expect(body('[data-testid=requisition-SR-000007]').text()).toContain('Wayan')
    expect(body('[data-testid=requisition-SR-000007]').text()).toContain('Ready to send')
    expect(has('[data-testid=requisition-new]')).toBe(true)
    expect(has('[data-testid=transfer-free]')).toBe(true)
    await click('[data-testid=tab-transfers]')
    expect(document.body.textContent).toContain('TRF-000003')
  })

  it('offers no new document to who may only view', async () => {
    await setup(MovementsView, ['inventory.view'], '/inventory/requisitions')
    expect(has('[data-testid=requisition-new]')).toBe(false)
    expect(has('[data-testid=transfer-free]')).toBe(false)
  })

  it('answers a requisition with the stock of the source: the quantity is what is left or what the source holds, and a line that asks for more is refused before the server', async () => {
    await setup(TransferView, ['inventory.move', 'inventory.view'], '/inventory/transfers/new?sr=7')
    // beras: 8 left, 5 in the source -> 5; sirup: 12 left, 40 in the source -> 12
    expect((document.body.querySelector('[name=qty_0]') as HTMLInputElement).value).toBe('5')
    expect((document.body.querySelector('[name=qty_1]') as HTMLInputElement).value).toBe('12')
    expect(body('[data-testid=to-stock-0]').text()).toBe('-2')
    const first = document.body.querySelector('[name=qty_0]') as HTMLInputElement
    first.value = '6'
    first.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    expect(has('[data-testid=too-much-0]')).toBe(true)
    expect((body('[data-testid=transfer-post]').element as HTMLButtonElement).disabled).toBe(true)
    first.value = '4'
    first.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    await click('[data-testid=transfer-post]')
    expect(POST).toHaveBeenCalledTimes(1)
    const call = POST.mock.calls[0]!
    expect(call[1].body.requisition_id).toBe(7)
    expect(call[1].body.lines).toEqual([{ item_id: 1, qty: '4' }, { item_id: 2, qty: '12' }])
  })
})

describe('issues', () => {
  it('asks the covers of a staff meal and the host of an entertainment before it can be posted', async () => {
    await setup(IssueView, ['inventory.move', 'inventory.view'], '/inventory/issues/new?purpose=STAFF_MEAL')
    await click('[data-testid=line-add]')
    const qty = document.body.querySelector('[name=qty_0]') as HTMLInputElement
    qty.value = '2'
    qty.dispatchEvent(new Event('input', { bubbles: true }))
    const dept = document.body.querySelector('[name=department_id]') as HTMLSelectElement
    dept.value = '3'
    dept.dispatchEvent(new Event('change', { bubbles: true }))
    await flushPromises()
    expect((body('[data-testid=issue-post]').element as HTMLButtonElement).disabled).toBe(true) // no covers
    await body('[data-testid=covers]').setValue('25')
    expect((body('[data-testid=issue-post]').element as HTMLButtonElement).disabled).toBe(false)
    await click('[data-testid=issue-post]')
    const sent = POST.mock.calls[0]![1].body
    expect(sent).toMatchObject({ purpose: 'STAFF_MEAL', covers: 25, department_id: 3, reason_code: null, beneficiary: null })
  })

  it('asks the host of an entertainment too', async () => {
    await setup(IssueView, ['inventory.move', 'inventory.view'], '/inventory/issues/new?purpose=ENTERTAINMENT')
    await click('[data-testid=line-add]')
    const qty = document.body.querySelector('[name=qty_0]') as HTMLInputElement
    qty.value = '2'
    qty.dispatchEvent(new Event('input', { bubbles: true }))
    const dept = document.body.querySelector('[name=department_id]') as HTMLSelectElement
    dept.value = '3'
    dept.dispatchEvent(new Event('change', { bubbles: true }))
    await body('[data-testid=covers]').setValue('4')
    expect((body('[data-testid=issue-post]').element as HTMLButtonElement).disabled).toBe(true)
    await body('[data-testid=beneficiary]').setValue('Bapak Gubernur')
    expect((body('[data-testid=issue-post]').element as HTMLButtonElement).disabled).toBe(false)
  })

  it('does not let a line take more than the warehouse holds', async () => {
    await setup(IssueView, ['inventory.move'], '/inventory/issues/new')
    await click('[data-testid=line-add]')
    const qty = document.body.querySelector('[name=qty_0]') as HTMLInputElement
    qty.value = '6' // beras: 5 in the first warehouse
    qty.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    expect(has('[data-testid=too-much-0]')).toBe(true)
  })
})

describe('the count sheet', () => {
  const count = (status: string, extra: Record<string, unknown> = {}) => ({
    id: 5, cnt_number: 'CNT-000005', status, warehouse_id: 2, warehouse_name: 'Bar store', scope: 'ALL', category_name: null, started_at: '2026-10-16T14:00:00Z', started_by_name: 'Wayan', business_date_started: '2026-10-16',
    submitted_at: null, submitted_by_name: null, decided_at: null, decided_by_name: null, total_variance_value: null, note: null, version: 1, blind: true, preview: false, held_documents: [],
    lines: [
      { item_id: 1, item_code: 'BERAS', item_name: 'Beras', category_name: 'Food', stock_unit_code: 'kg', book_qty: null, counted_qty: null, counted_at: null, entered_unit_id: null, entered_qty: null, expected_qty: null, held_before_qty: null, variance_qty: null, variance_value: null, note: null },
      { item_id: 2, item_code: 'SIRUP', item_name: 'Sirup', category_name: 'Beverage', stock_unit_code: 'botol', book_qty: null, counted_qty: '3', counted_at: '2026-10-16T14:20:00Z', entered_unit_id: null, entered_qty: null, expected_qty: null, held_before_qty: null, variance_qty: null, variance_value: null, note: null },
    ], ...extra,
  })

  it('is blind, tells the hour of each item, saves a quantity when the box is left and submits', async () => {
    GET = vi.fn(async (path: string) => (path.endsWith('/counts/{id}') ? { data: count('OPEN') } : reply(path)))
    PUT = vi.fn().mockResolvedValue({ data: count('OPEN', { lines: count('OPEN').lines.map((l) => (l.item_id === 1 ? { ...l, counted_qty: '4', counted_at: '2026-10-16T14:30:00Z' } : l)) }) })
    POST = vi.fn().mockResolvedValue({ data: count('SUBMITTED', { blind: false, preview: true }) })
    await setup(CountView, ['inventory.count', 'inventory.view'], '/inventory/counts/5')
    expect(has('[data-testid=freeze-banner]')).toBe(true)
    expect(body('[data-testid=progress]').text()).toContain('1 of 2')
    expect(document.body.textContent).not.toContain('Book:') // blind
    expect(has('[data-testid=item-BERAS]')).toBe(true) // not counted yet
    expect((document.body.querySelector('[name=unit_BERAS]') as HTMLSelectElement).value).toBe('10') // the stock unit is chosen once the items are loaded
    expect(has('[data-testid=item-SIRUP]')).toBe(false)
    const input = document.body.querySelector('[name=count_BERAS]') as HTMLInputElement
    input.value = '4'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    input.dispatchEvent(new Event('blur', { bubbles: true }))
    await flushPromises()
    expect(PUT).toHaveBeenCalledTimes(1)
    expect(PUT.mock.calls[0]![1].body.lines).toEqual([{ item_id: 1, qty: '4', unit_id: null }])
    expect(body('[data-testid=progress]').text()).toContain('2 of 2')
    await click('[data-testid=count-submit]')
    expect(POST.mock.calls[0]![0]).toContain('/counts/{id}/submit')
  })

  it('shows the variance of a submitted count to the approver, who approves it', async () => {
    const lines = [{ ...count('SUBMITTED').lines[1]!, book_qty: '4', expected_qty: '4', held_before_qty: '0', variance_qty: '-1', variance_value: '-49500' }, { ...count('SUBMITTED').lines[0]!, book_qty: '10' }]
    GET = vi.fn(async (path: string) => (path.endsWith('/counts/{id}') ? { data: count('SUBMITTED', { blind: false, preview: true, submitted_by_name: 'Wayan', lines }) } : reply(path)))
    POST = vi.fn().mockResolvedValue({ data: count('APPROVED', { blind: false, decided_by_name: 'Made', decided_at: '2026-10-16T15:00:00Z', lines }) })
    await setup(CountView, ['inventory.approve', 'inventory.view'], '/inventory/counts/5')
    expect(has('[data-testid=count-review]')).toBe(true)
    expect(body('[data-testid=variance-SIRUP]').text()).toBe('-1')
    expect(body('[data-testid=count-total]').text()).toContain('49')
    await click('[data-testid=count-approve]')
    expect(POST.mock.calls[0]![0]).toContain('/counts/{id}/approve')
  })
})

describe('the amenities', () => {
  const run = {
    business_date: '2026-09-30', iss_number: 'AMN-20260930', rooms: 4, guests: 8, comp_rooms: 1, house_use_rooms: 0, total_value: '44890', status: 'POSTED', plan_version: 2, held_lines: 0, issue_id: 12,
    created_at: '2026-09-30T16:00:00Z', reversal_number: null, cost_per_room: '11222', reversible: true, held_by_count: null,
    types: [{ room_type_id: 11, room_type_name: 'Standard', rooms: 3, guests: 6 }, { room_type_id: 12, room_type_name: 'Suite', rooms: 1, guests: 2 }],
    lines: [
      { line_no: 1, item_id: 1, item_code: 'SOAP', item_name: 'Soap 25 g', stock_unit_code: 'pcs', room_type_id: null, room_type_name: null, basis: 'PER_ROOM', qty_per: '1.000000', multiplier: 4, qty_stock: '4', stock_before: '3', value: '11840', status: 'APPLIED', applied_on: '2026-09-30' },
      { line_no: 2, item_id: 2, item_code: 'SLIPPER', item_name: 'Slippers', stock_unit_code: 'pcs', room_type_id: 12, room_type_name: 'Suite', basis: 'PER_GUEST', qty_per: '1', multiplier: 2, qty_stock: '2', stock_before: '10', value: '13000', status: 'APPLIED', applied_on: '2026-09-30' },
    ],
    room_list: [{ room_id: 1, room_number: '101', room_type_name: 'Standard', guests: 2, occupancy_kind: 'PAID' }],
    short: [{ line_no: 1, item_id: 1, item_code: 'SOAP', item_name: 'Soap 25 g', stock_unit_code: 'pcs', room_type_id: null, room_type_name: null, basis: 'PER_ROOM', qty_per: '1', multiplier: 4, qty_stock: '4', stock_before: '3', value: '11840', status: 'APPLIED', applied_on: null }],
    journal: [{ debit_code: '5150', debit_name: 'Guest supplies', credit_code: '1330', credit_name: 'Operating supplies', department_name: 'Rooms', amount: '44890' }],
  }

  it('shows the document of a night with the rule of each line, the shortage and the journal, and reverses it with a reason', async () => {
    GET = vi.fn(async (path: string) => (path.includes('/amenities/runs/') ? { data: run } : reply(path)))
    POST = vi.fn().mockResolvedValue({ data: { ...run, status: 'REVERSED', reversal_number: 'ISS-000009', reversible: false } })
    await setup(AmenityRunView, ['inventory.view', 'inventory.manage'], '/inventory/amenities/runs/2026-09-30')
    expect(body('[data-testid=run-rooms]').text()).toBe('4')
    expect(body('[data-testid=run-line-SOAP]').text()).toContain('1 × 4 rooms')
    expect(body('[data-testid=run-line-SLIPPER]').text()).toContain('Suite · 1 × 2 guests')
    expect(body('[data-testid=run-short-count]').text()).toBe('1')
    expect(body('[data-testid=run-journal]').text()).toContain('5150')
    await click('[data-testid=run-reverse]')
    expect((body('[data-testid=reverse-confirm]').element as HTMLButtonElement).disabled).toBe(true)
    await body('[data-testid=reverse-reason]').setValue('three rooms were empty')
    await click('[data-testid=reverse-confirm]')
    expect(POST.mock.calls[0]![1].body).toEqual({ reason: 'three rooms were empty' })
    expect(has('[data-testid=reversal-note]')).toBe(true)
  })

  it('does not offer the reversal to who may not manage', async () => {
    GET = vi.fn(async (path: string) => (path.includes('/amenities/runs/') ? { data: run } : reply(path)))
    await setup(AmenityRunView, ['inventory.view'], '/inventory/amenities/runs/2026-09-30')
    expect(has('[data-testid=run-reverse]')).toBe(false)
  })

  it('holds the plan as lines for the base package and for a room type, and saves a new version', async () => {
    const plan = {
      id: 3, version_no: 2, effective_from: '2026-09-30', is_enabled: true, warehouse_id: 2, warehouse_name: 'Bar store', department_id: 3, department_name: 'FB', expense_account_id: 51, expense_account_code: '5150', expense_account_name: 'Guest supplies',
      created_at: '2026-09-30T01:00:00Z', applies_from_next_close: true, versions: [{ id: 3, version_no: 2, effective_from: '2026-09-30', is_enabled: true, line_count: 2, created_at: '2026-09-30T01:00:00Z', created_by_name: 'A' }],
      cost_per_room: [{ room_type_id: 11, room_type_name: 'Standard', guests: 2, cost: '23680' }, { room_type_id: 12, room_type_name: 'Suite', guests: 2, cost: '40880' }],
      lines: [
        { line_no: 1, room_type_id: null, room_type_name: null, item_id: 1, item_code: 'BERAS', item_name: 'Beras', unit_id: 10, unit_code: 'kg', stock_unit_code: 'kg', qty: '1.000000', basis: 'PER_ROOM' },
        { line_no: 2, room_type_id: 12, room_type_name: 'Suite', item_id: 2, item_code: 'SIRUP', item_name: 'Sirup', unit_id: 20, unit_code: 'botol', stock_unit_code: 'botol', qty: '2', basis: 'PER_GUEST' },
      ],
    }
    GET = vi.fn(async (path: string) => {
      if (path.endsWith('/amenities/plan')) return { data: plan }
      if (path.endsWith('/amenities/preview')) return { data: { business_date: '2026-09-30', enabled: true, rooms: 4, guests: 8, comp_rooms: 0, house_use_rooms: 0, value: '44890', short: [{ item_id: 1, item_code: 'BERAS', item_name: 'Beras', stock_unit_code: 'kg', need: '4', on_hand: '3', short: '1' }], held: null, held_by_count: null, invalid: [], types: [], lines: [] } }
      if (path.endsWith('/amenities/runs')) return { data: { data: [] } }
      return reply(path)
    })
    PUT = vi.fn().mockResolvedValue({ data: { ...plan, version_no: 3 } })
    await setup(AmenitiesPlanView, ['inventory.view', 'inventory.manage'], '/inventory/amenities')
    expect(body('[data-testid=plan-version]').text()).toContain('2')
    expect(has('[data-testid=plan-line-0]')).toBe(true)
    expect(has('[data-testid=plan-line-1]')).toBe(false) // the suite's extra is on its own tab
    expect(body('[data-testid=plan-cost]').text()).toContain('23,680')
    expect(body('[data-testid=preview-short]').text()).toContain('Beras')
    await click('[data-testid=tab-type-STE]')
    expect(has('[data-testid=plan-line-1]')).toBe(true)
    await click('[data-testid=plan-save]')
    expect(PUT).toHaveBeenCalledTimes(1)
    const sent = PUT.mock.calls[0]![1].body
    expect(sent.is_enabled).toBe(true)
    expect(sent.lines).toEqual([
      { room_type_id: null, item_id: 1, unit_id: 10, qty: '1', basis: 'PER_ROOM' },
      { room_type_id: 12, item_id: 2, unit_id: 20, qty: '2', basis: 'PER_GUEST' },
    ])
  })
})

describe('the texts of the movements', () => {
  const dir = resolve(__dirname, '../../..')
  function files(d: string): string[] {
    return readdirSync(d, { withFileTypes: true }).flatMap((e) => (e.isDirectory() ? files(resolve(d, e.name)) : /\.(vue|ts)$/.test(e.name) && !e.name.endsWith('.test.ts') ? [resolve(d, e.name)] : []))
  }
  const sources = [...files(resolve(dir, 'views/inventory/movements')), resolve(dir, 'utils/movements.ts')]
  const lookup = (tree: unknown, key: string): unknown => key.split('.').reduce<unknown>((o, k) => (o && typeof o === 'object' ? (o as Record<string, unknown>)[k] : undefined), tree)

  it('has every literal key that the views use, in English and Indonesian', () => {
    const missing: string[] = []
    for (const f of sources) {
      for (const m of readFileSync(f, 'utf8').matchAll(/\bt\(\s*'([a-zA-Z0-9_.]+)'/g)) {
        for (const [name, tree] of [['en', en], ['id', id]] as const) if (typeof lookup(tree, m[1]!) !== 'string') missing.push(`${name}: ${m[1]}`)
      }
    }
    expect([...new Set(missing)]).toEqual([])
  })

  it('has the words of every state, purpose, reason and kind the views build a key from', () => {
    const groups: Record<string, string[]> = {
      'inventory.requisitionStatus': ['DRAFT', 'SUBMITTED', 'RETURNED', 'APPROVED', 'PARTIAL', 'FULFILLED', 'REJECTED', 'CANCELLED'],
      'inventory.postedStatus': ['POSTED', 'REVERSED'],
      'inventory.countStatus': ['OPEN', 'SUBMITTED', 'APPROVED', 'CANCELLED'],
      'inventory.issues.purpose': ['USE', 'WASTE', 'STAFF_MEAL', 'ENTERTAINMENT'],
      'inventory.issue.newTitle': ['USE', 'WASTE', 'STAFF_MEAL', 'ENTERTAINMENT'],
      'inventory.issue.hint': ['USE', 'WASTE', 'STAFF_MEAL', 'ENTERTAINMENT'],
      'inventory.issue.rule': ['USE', 'WASTE', 'STAFF_MEAL', 'ENTERTAINMENT'],
      'inventory.issue.post': ['USE', 'WASTE', 'STAFF_MEAL', 'ENTERTAINMENT'],
      'inventory.issue.reasons': ['SPOILAGE', 'EXPIRED', 'BREAKAGE', 'TASTING', 'OTHER'],
      'inventory.counts.scopeName': ['ALL', 'CATEGORY', 'LIST'],
      'inventory.amenityRun.kind': ['PAID', 'COMPLIMENTARY', 'HOUSE_USE'],
      'inventory.inbox.doc': ['PR', 'SR'],
      'inventory.movements.tab': ['requisitions', 'transfers'],
    }
    const missing: string[] = []
    for (const [group, list] of Object.entries(groups)) for (const k of list) for (const [name, tree] of [['en', en], ['id', id]] as const) if (typeof lookup(tree, `${group}.${k}`) !== 'string') missing.push(`${name}: ${group}.${k}`)
    expect(missing).toEqual([])
  })
})
