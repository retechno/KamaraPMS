import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { resetDepartments } from '@/composables/useDepartments'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import PurchaseRequestView from './PurchaseRequestView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PUT: vi.fn(), PATCH: vi.fn() } }))

const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
const mounted: VueWrapper[] = []
const item = {
  id: 1, code: 'BERAS', name: 'Beras', stock_unit_id: 10, stock_unit_code: 'kg', is_active: true, default_supplier_id: null,
  units: [{ unit_id: 10, unit_code: 'kg', stock_num: 1, stock_den: 1, is_purchase: true, is_default_purchase: true }], stock: [],
}
const warehouses = [{ id: 1, code: 'MAIN', name: 'Main store', kind: 'MAIN', is_active: true }, { id: 2, code: 'BAR', name: 'Bar store', kind: 'DEPARTMENT', is_active: true }]
const body = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)

async function mountNew(): Promise<void> {
  await mountAt('/inventory/requests/new')
}

/** A draft of the signed-in requester (user 5) for the department 3, and what the check before a submission answers for it. */
const draft = {
  id: 9, request_number: 'PR-0009', status: 'DRAFT', requester_id: 5, requester_name: 'Mira', department_id: 3, department_code: 'FB', department_name: 'Food and beverage', warehouse_id: 1, warehouse_name: 'Main store',
  needed_on: null, est_total: '5000', cycle: 0, submitted_at: null, returned_note: null, note: null, version: 1, created_at: '2026-11-01T01:00:00Z', stage: null, steps: [], edits: [], decision: null, purchase_orders: [],
  lines: [{ line_no: 1, item_id: 1, item_code: 'BERAS', item_name: 'Beras', unit_id: 10, unit_code: 'kg', qty: '5', est_unit_price: '1000', amount: '5000', supplier_id: null, supplier_name: null, on_hand: '0', stock_unit_code: 'kg', po_number: null }],
}
const level = (n: number, over: Record<string, unknown> = {}) => ({ level_no: n, name: n === 1 ? 'Kepala departemen' : 'Cost control', approver_kind: n === 1 ? 'DEPT_HEAD' : 'PERMISSION', permission: n === 1 ? null : 'inventory.cost_control',
  applies: true, skip_reason: null, approvers: [{ id: 20 + n, name: `Approver ${n}` }], problem: null, ...over })
const checkOf = (problem: string | null, levels: unknown[]) => ({ can_submit: problem === null, problem, department_id: 3, department_name: 'Food and beverage', est_total: '5000', levels })
let check: unknown = checkOf(null, [level(1), level(2)])

async function mountAt(path: string, admin = false): Promise<void> {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'm@hotel.test', is_tenant_admin: admin }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: ['inventory.view', 'inventory.request'] }] } as never
  usePropertyStore().currentId = 7
  usePropertyStore().current = { id: 7, currency_decimals: 0 } as never
  resetDepartments()
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/inventory/requests/{id}/approval-check')) return { data: check }
    if (path.endsWith('/inventory/requests/{id}')) return { data: draft }
    if (path.endsWith('/inventory/warehouses')) return { data: { data: warehouses } }
    if (path.endsWith('/inventory/items/{id}')) return { data: item }
    if (path.endsWith('/inventory/items')) return { data: { items: [item] } }
    if (path.endsWith('/inventory/suppliers')) return { data: { data: [{ id: 2, name: 'Sumber' }] } }
    if (path.endsWith('/inventory/settings')) return { data: { po_requires_pr: false } }
    if (path.endsWith('/departments')) return { data: { data: [{ id: 3, code: 'FB', name: 'Food and beverage', level: 1, is_active: true }] } }
    return { data: { data: [] } }
  })
  POST = vi.fn().mockResolvedValue({ data: { id: 9 } })
  await router.push(path)
  await router.isReady()
  mounted.push(mount(PurchaseRequestView as never, { attachTo: document.body, global: { plugins: [pinia, router] } }))
  await flushPromises()
  await flushPromises()
  await flushPromises()
}

beforeEach(() => {
  setLocale('en')
  check = checkOf(null, [level(1), level(2)])
})
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
})

describe('PurchaseRequestView, what is seen is what is sent', () => {
  it('does not save or submit the request while the needed date is half typed, and says why', async () => {
    await mountNew()
    ;(document.body.querySelector('[data-testid=line-add]') as HTMLElement).click()
    await flushPromises()
    await body('input[name=qty_0]').setValue('5')
    await body('select[name=department_id]').setValue(3)
    expect((body('[data-testid=request-submit]').element as HTMLButtonElement).disabled).toBe(false)
    await body('[data-testid=request-form] input[data-slot=date-input-text]').setValue('3/2')
    await body('[data-testid=request-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls.length).toBe(0)
    expect(document.body.textContent).toContain('Write as dd/mm/yyyy')
    ;(body('[data-testid=request-submit]').element as HTMLElement).click()
    await flushPromises()
    expect(POST.mock.calls.length).toBe(0)
  })
})

describe('PurchaseRequestView, the check before a submission', () => {
  const calls = () => GET.mock.calls.filter((c) => String(c[0]).endsWith('/approval-check'))

  it('reads the check of a saved draft and shows nothing while every level has someone', async () => {
    await mountAt('/inventory/requests/9')
    expect(calls().length).toBe(1)
    expect(calls()[0]![1]).toEqual({ params: { path: { propertyId: 7, id: 9 } } })
    expect(document.body.querySelector('[data-testid=chain-blocked]')).toBeNull()
    expect((body('[data-testid=request-submit]').element as HTMLButtonElement).disabled).toBe(false)
  })

  it('names the level that nobody can decide and what to ask for, and still lets the server decide', async () => {
    check = checkOf('NO_APPROVER', [level(1, { approvers: [], problem: 'NO_APPROVER' }), level(2)])
    await mountAt('/inventory/requests/9')
    const warn = body('[data-testid=chain-blocked]')
    expect(warn.exists()).toBe(true)
    expect(body('[data-testid=chain-blocked-1]').text()).toContain('Food and beverage')
    expect(body('[data-testid=chain-blocked-1]').text()).toContain('Kepala departemen')
    expect(document.body.querySelector('[data-testid=chain-blocked-2]')).toBeNull()
    expect(document.body.querySelector('[data-testid=chain-blocked-roles]')).toBeNull() // only an administrator sets up roles
    expect((body('[data-testid=request-submit]').element as HTMLButtonElement).disabled).toBe(false)
  })

  it('names the permission of a level of a permission, and tells when one person would have to approve twice', async () => {
    check = checkOf('NO_APPROVER', [level(1), level(2, { approvers: [], problem: 'NO_APPROVER' })])
    await mountAt('/inventory/requests/9', true)
    expect(body('[data-testid=chain-blocked-2]').text()).toContain('Cost control')
    expect(document.body.querySelector('[data-testid=chain-blocked-roles]')).not.toBeNull()
    while (mounted.length) mounted.pop()!.unmount()
    document.body.innerHTML = ''

    check = checkOf('NOT_DISTINCT', [level(1, { approvers: [{ id: 30, name: 'Rudi' }] }), level(2, { approvers: [{ id: 30, name: 'Rudi' }] })])
    await mountAt('/inventory/requests/9')
    expect(body('[data-testid=chain-blocked-distinct]').text()).toContain('1, 2')
  })

  it('hides the warning while another department is chosen on the form, and asks nothing of a new request', async () => {
    check = checkOf('NO_APPROVER', [level(1, { approvers: [], problem: 'NO_APPROVER' }), level(2)])
    await mountAt('/inventory/requests/9')
    expect(document.body.querySelector('[data-testid=chain-blocked]')).not.toBeNull()
    await body('select[name=department_id]').setValue(0)
    expect(document.body.querySelector('[data-testid=chain-blocked]')).toBeNull()
    while (mounted.length) mounted.pop()!.unmount()
    document.body.innerHTML = ''

    await mountNew()
    expect(calls().length).toBe(0)
  })
})
