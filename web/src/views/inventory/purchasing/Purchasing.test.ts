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
import ApprovalChainsView from './ApprovalChainsView.vue'
import ApprovalInboxView from './ApprovalInboxView.vue'
import BillDraftsView from './BillDraftsView.vue'
import PurchasingView from './PurchasingView.vue'

let GET = vi.fn()
let PUT = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), PUT: (...a: unknown[]) => PUT(...a), POST: vi.fn(), PATCH: vi.fn() } }))

const prChain = {
  doc_type: 'PR', skip_level1_if_head: true, approver_may_edit: true, version: 3,
  levels: [
    { name: 'Head of department', approver_kind: 'DEPT_HEAD', permission: null, condition_kind: 'ALWAYS', threshold: null },
    { name: 'Cost control', approver_kind: 'PERMISSION', permission: 'inventory.cost_control', condition_kind: 'ALWAYS', threshold: null },
    { name: 'General manager', approver_kind: 'PERMISSION', permission: 'inventory.final_approve', condition_kind: 'TOTAL_ABOVE', threshold: '5000000' },
  ],
}
const srChain = { ...prChain, doc_type: 'SR', version: 1, levels: prChain.levels.slice(0, 2) }

const requests = [
  { id: 1, request_number: 'PR-000001', status: 'SUBMITTED', department_name: 'Kitchen', requester_id: 9, requester_name: 'Dewi', needed_on: '2026-10-20', line_count: 3, est_total: '1200000', stage: { done: 1, total: 3, name: 'Cost control' }, waiting_for_me: true, updated_at: '2026-10-17T01:00:00Z' },
  { id: 2, request_number: 'PR-000002', status: 'DRAFT', department_name: 'Bar', requester_id: 5, requester_name: 'Me', needed_on: null, line_count: 1, est_total: '90000', stage: null, waiting_for_me: false, updated_at: '2026-10-17T01:00:00Z' },
]
const drafts = [
  { id: 4, receipt_id: 8, receipt_number: 'GR-000008', supplier_id: 2, supplier_name: 'Sumber Segar', received_on: '2026-10-16', po_number: 'PO-000003', warehouse_name: 'Main store', receipt_value: '500000', remaining_value: '500000', status: 'OPEN' },
  { id: 5, receipt_id: 9, receipt_number: 'GR-000009', supplier_id: 3, supplier_name: 'Toko Lain', received_on: '2026-10-16', po_number: null, warehouse_name: 'Main store', receipt_value: '90000', remaining_value: '90000', status: 'OPEN' },
]

const blank = { template: '<div />' }
const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: blank }] })
const mounted: VueWrapper[] = []

function ok(path: string): unknown {
  if (path.endsWith('/approval-chains/{doc}')) return { data: prChain }
  if (path.endsWith('/approvals/inbox')) return { data: { data: [{ request_id: 1, request_number: 'PR-000001', est_total: '1200000', department_id: 3, department_name: 'Kitchen', requester_id: 9, requester_name: 'Dewi', submitted_at: '2026-10-16T02:00:00Z', step_id: 11, level_no: 2, step_name: 'Cost control' }] } }
  if (path.endsWith('/inventory/requests')) return { data: { data: requests } }
  if (path.endsWith('/inventory/bill-drafts')) return { data: { data: drafts } }
  if (path.endsWith('/inventory/settings')) return { data: { direct_receipt_limit: '500000', po_requires_pr: true } }
  return { data: { data: [] } }
}

async function setup(component: unknown, permissions: string[], route = '/inventory/purchasing') {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'm@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  usePropertyStore().current = { id: 7, currency_decimals: 0 } as never
  GET = vi.fn(async (path: string, opts?: { params?: { path?: { doc?: string } } }) => (path.endsWith('/approval-chains/{doc}') && opts?.params?.path?.doc === 'SR' ? { data: srChain } : ok(path)))
  PUT = vi.fn().mockResolvedValue({ data: { ...prChain, version: 4 } })
  await router.push(route)
  await router.isReady()
  const w = mount(component as never, { attachTo: document.body, global: { plugins: [pinia, router] } })
  mounted.push(w)
  await flushPromises()
  return w
}
const body = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)
const has = (sel: string) => document.body.querySelector(sel) !== null

beforeEach(() => setLocale('en'))
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
})

describe('purchasing', () => {
  it('lists the requests with their stage and a chip for the ones that wait for me', async () => {
    await setup(PurchasingView, ['inventory.view', 'inventory.request', 'inventory.approve'])
    expect(document.body.textContent).toContain('PR-000001')
    expect(document.body.textContent).toContain('PR-000002')
    expect(body('[data-testid=request-new]').text()).toContain('New request')
    const chips = [...document.body.querySelectorAll('button')].map((b) => b.textContent ?? '')
    const mine = [...document.body.querySelectorAll('button')].find((b) => (b.textContent ?? '').includes('Waiting for me (1)'))
    expect(chips.some((c) => c.includes('Waiting for me (1)'))).toBe(true)
    mine!.click()
    await flushPromises()
    expect(document.body.textContent).toContain('PR-000001')
    expect(document.body.textContent).not.toContain('PR-000002')
  })

  it('offers a new request only to who may make one', async () => {
    await setup(PurchasingView, ['inventory.view'])
    expect(has('[data-testid=request-new]')).toBe(false)
  })

  it('lists the requests that wait for the signed-in user in the inbox', async () => {
    await setup(ApprovalInboxView, ['inventory.approve', 'inventory.cost_control'])
    const row = body('[data-testid=inbox-PR-000001]').text()
    expect(row).toContain('Kitchen')
    expect(row).toContain('Dewi')
    expect(row).toContain('2 · Cost control')
  })
})

describe('draft bills', () => {
  it('bills drafts of one supplier together: the pick of one supplier locks the drafts of the others', async () => {
    await setup(BillDraftsView, ['inventory.bill', 'payables.post'])
    const a = document.body.querySelector('[data-testid=pick-GR-000008]') as HTMLInputElement
    const b = document.body.querySelector('[data-testid=pick-GR-000009]') as HTMLInputElement
    expect(a.disabled).toBe(false)
    a.click()
    await flushPromises()
    expect(b.disabled).toBe(true)
    expect(body('[data-testid=bill-picked]').text()).toContain('(1)')
  })

  it('does not offer to bill to who may not', async () => {
    await setup(BillDraftsView, ['inventory.view'])
    expect((document.body.querySelector('[data-testid=pick-GR-000008]') as HTMLInputElement).disabled).toBe(true)
  })
})

describe('the approval chains', () => {
  it('shows the levels and saves the changes with the version it read', async () => {
    await setup(ApprovalChainsView, ['inventory.manage'])
    expect(has('[data-testid=chain-PR]')).toBe(true)
    expect(has('[data-testid=chain-SR]')).toBe(true)
    expect(has('[data-testid=level-PR-3]')).toBe(true)
    ;(document.body.querySelector('[data-testid=skip-PR]') as HTMLInputElement).click()
    await flushPromises()
    body('[data-testid=save-PR]').element.dispatchEvent(new Event('click', { bubbles: true }))
    await flushPromises()
    expect(PUT).toHaveBeenCalledTimes(1)
    const call = PUT.mock.calls[0]!
    expect(call[1].params.path.doc).toBe('PR')
    expect(call[1].body.version).toBe(3)
    expect(call[1].body.skip_level1_if_head).toBe(false)
    expect(call[1].body.levels).toHaveLength(3)
    expect(call[1].body.levels[2]).toMatchObject({ approver_kind: 'PERMISSION', permission: 'inventory.final_approve', condition_kind: 'TOTAL_ABOVE', threshold: '5000000' })
    expect(call[1].body.levels[0]).toMatchObject({ approver_kind: 'DEPT_HEAD', permission: null, threshold: null })
    expect(has('[data-testid=saved-PR]')).toBe(true)
  })

  it('removes a level', async () => {
    await setup(ApprovalChainsView, ['inventory.manage'])
    body('[data-testid=remove-level-PR-3]').element.dispatchEvent(new Event('click', { bubbles: true }))
    await flushPromises()
    expect(has('[data-testid=level-PR-3]')).toBe(false)
  })
})

describe('the texts of purchasing', () => {
  const dir = resolve(__dirname, '../../..')
  function files(d: string): string[] {
    return readdirSync(d, { withFileTypes: true }).flatMap((e) => (e.isDirectory() ? files(resolve(d, e.name)) : /\.(vue|ts)$/.test(e.name) && !e.name.endsWith('.test.ts') ? [resolve(d, e.name)] : []))
  }
  const sources = [...files(resolve(dir, 'views/inventory/purchasing')), ...files(resolve(dir, 'components/inventory')), resolve(dir, 'utils/purchasing.ts')]
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

  it('has the status words for every state of the documents', () => {
    const states: Record<string, string[]> = {
      requestStatus: ['DRAFT', 'SUBMITTED', 'APPROVED', 'RETURNED', 'CONVERTED', 'CANCELLED'],
      orderStatus: ['DRAFT', 'ORDERED', 'PARTIAL', 'RECEIVED', 'CLOSED', 'CANCELLED'],
      draftStatus: ['OPEN', 'PARTIAL', 'BILLED', 'DISCARDED'],
      receiptStatus: ['POSTED', 'REVERSED'],
    }
    const missing: string[] = []
    for (const [group, list] of Object.entries(states)) {
      for (const s of list) for (const [name, tree] of [['en', en], ['id', id]] as const) if (typeof lookup(tree, `inventory.${group}.${s}`) !== 'string') missing.push(`${name}: ${group}.${s}`)
    }
    for (const s of ['WAITING', 'ACTIVE', 'APPROVED', 'RETURNED', 'SKIPPED', 'NOT_REACHED']) for (const [name, tree] of [['en', en], ['id', id]] as const) if (typeof lookup(tree, `inventory.chain.state.${s}`) !== 'string') missing.push(`${name}: chain.state.${s}`)
    for (const k of ['APPROVAL_WAITING', 'APPROVED', 'RETURNED', 'CHAIN_STUCK']) for (const [name, tree] of [['en', en], ['id', id]] as const) if (typeof lookup(tree, `inventory.bell.kind.${k}`) !== 'string') missing.push(`${name}: bell.kind.${k}`)
    expect(missing).toEqual([])
  })
})
