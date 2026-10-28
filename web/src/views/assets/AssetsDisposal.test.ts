import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { resetDepartments } from '@/composables/useDepartments'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import AssetDepreciationView from './AssetDepreciationView.vue'
import AssetDisposalsView from './AssetDisposalsView.vue'
import AssetRegisterView from './AssetRegisterView.vue'
import AssetView from './AssetView.vue'
import AssetWaitingView from './AssetWaitingView.vue'

/** PR 3 of the fixed assets: the changes of an asset, its disposal and the approval of it, the history tab, the status in the register, and the leftovers of the review of PR 2. */
let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))
vi.mock('@/utils/documents', async (orig) => ({ ...(await orig<typeof import('@/utils/documents')>()), downloadFile: vi.fn() }))

const categories = [
  { id: 2, code: 'FFE', name: 'FF&E', kind: 'FFE', cost_account_id: 11, cost_account_code: '1530', cost_account_name: 'FF&E', accumulated_account_id: 12, accumulated_account_code: '1592', expense_account_id: 13, expense_account_code: '7520', method: 'SL', life_months: 96, residual_pct: '0', fiscal_group: 'G2', is_depreciable: true, is_active: true, sort_order: 1, asset_count: 3, version: 2, department_id: 6 },
  { id: 3, code: 'IT', name: 'IT equipment', kind: 'FFE', cost_account_id: 21, cost_account_code: '1540', cost_account_name: 'IT', accumulated_account_id: 22, accumulated_account_code: '1593', expense_account_id: 23, expense_account_code: '7530', method: 'SL', life_months: 48, residual_pct: '0', fiscal_group: 'G1', is_depreciable: true, is_active: true, sort_order: 2, asset_count: 1, version: 1, department_id: null },
]
const asset = {
  id: 2, number: 'FA-000012', name: 'Deluxe chairs', category_id: 2, category_code: 'FFE', category_name: 'FF&E', category_kind: 'FFE', source: 'JOURNAL', status: 'ACTIVE', acquisition_date: '2026-03-01', in_service_date: '2026-03-01', start_rule: 'IN_SERVICE_MONTH', residual_value: '0',
  life_months: 96, method: 'SL', first_dep_month: '2026-03-01', fully_depreciated: false, unit_count: 1, disposal_pending: false, fiscal_group: 'NONE', fiscal_opening_estimated: false, version: 3, dep_months_done: 14, cost: '36000000', accum_depr: '13375000', carrying_value: '22625000', remaining_months: 82,
  location_id: 1, location_name: 'Lobby', department_id: 6, department_name: 'Rooms',
}
const detail = { ...asset, opening_import_number: null, source_link: null, ledger: [] }
const history = [
  { kind: 'MOVE', date: '2026-10-05', reason: 'new wing', details: { from_location: 'Lobby', to_location: 'Wing B', from_department: 'Rooms', to_department: 'Rooms' }, journal_id: null, journal_number: null, by: 'Maya', at: '2026-10-05T01:00:00Z' },
  { kind: 'LIFE', date: '2026-10-10', reason: 'longer life', details: { old: '96', new: '120' }, journal_id: null, journal_number: null, by: 'Maya', at: '2026-10-10T01:00:00Z' },
  { kind: 'DEPRECIATION', date: '2026-10-31', reason: null, details: { month: '2026-10-01', months: 1, amount: '375000' }, journal_id: 5, journal_number: 'JV-000412', by: null, at: '2026-11-01T01:00:00Z' },
]
const waitingLine = {
  line_id: 1, journal_id: 11, journal_number: 'JV-000011', journal_type: 'AP', journal_date: '2026-11-03', line_no: 1, source: 'BILL', document_number: 'BL-000231', account_id: 11, account_code: '1530', account_name: 'FF&E', description: 'Repair of chairs',
  cost: '4000000', category_id: 2, category_code: 'FFE', category_name: 'FF&E', category_kind: 'FFE', bill_number: 'BL-000231', supplier_name: 'PT Sejuk Abadi', vat_treatment: 'CREDITABLE', age_days: 3, below_threshold: false, dismissed: false,
}
const waitingPage = (threshold = '2500000') => ({ data: [waitingLine], total: 1, total_cost: '4000000', register_from: '2026-10-01', capitalization_threshold: threshold, counts: { waiting: 1, waiting_cost: '4000000', dismissed: 0, dismissed_cost: '0' } })
const accounts = [
  { id: 31, code: '1110', name: 'Cash', account_type: 'ASSET', statement_group: 'CASH' },
  { id: 32, code: '1230', name: 'Other receivables', account_type: 'ASSET', statement_group: 'RECEIVABLE' },
  { id: 33, code: '1530', name: 'FF&E', account_type: 'ASSET', statement_group: 'PPE' },
]
const preview = {
  asset_id: 2, asset_number: 'FA-000012', asset_name: 'Deluxe chairs', kind: 'SALE', reason_code: 'SOLD', disposal_date: '2026-11-06', units: 1, unit_count: 1, cost_removed: '36000000', accum_removed: '13375000', carrying_removed: '22625000',
  proceeds: '25000000', gain_loss: '2375000', approval_amount: '25000000', pkp_on_date: false,
  lines: [
    { account_id: 31, account_code: '1110', account_name: 'Cash', debit: '25000000', credit: '0', department_id: null },
    { account_id: 12, account_code: '1592', account_name: 'Accumulated FF&E', debit: '13375000', credit: '0', department_id: null },
    { account_id: 11, account_code: '1530', account_name: 'FF&E', debit: '0', credit: '36000000', department_id: null },
    { account_id: 41, account_code: '8110', account_name: 'Gain on disposal', debit: '0', credit: '2375000', department_id: 6 },
  ],
}
const requestRow = { id: 9, asset_id: 2, asset_number: 'FA-000012', asset_name: 'Deluxe chairs', kind: 'SALE', reason_code: 'SOLD', disposal_date: '2026-11-06', units: 1, proceeds: '25000000', approval_amount: '25000000', status: 'PENDING', requested_by: 4, requested_by_name: 'Maya', requested_at: '2026-11-06T02:00:00Z' }
const disposalRow = { id: 3, number: 'FAD-000003', asset_id: 2, asset_number: 'FA-000012', asset_name: 'Deluxe chairs', kind: 'SALE', reason_code: 'SOLD', disposal_date: '2026-11-06', units: 1, cost_removed: '36000000', accum_removed: '13375000', carrying_removed: '22625000', proceeds: '25000000', gain_loss: '2375000', journal_id: 5, journal_number: 'JV-000500', status: 'POSTED', created_at: '2026-11-06T02:00:00Z' }
const deprOverview = {
  register_from: '2026-10-01', next_month: '2026-10-01', business_date: '2026-11-06',
  months: [
    { month: '2026-11-01', status: 'NOT_RUN', total: '0', asset_count: 0, period_closed: false, is_next: false, runnable: false, reversible: false },
    { month: '2026-10-01', status: 'POSTED', total: '3041667', asset_count: 2, period_closed: false, is_next: false, runnable: false, reversible: true, journal_number: 'JV-000412' },
  ],
}
const group = (over: Record<string, unknown>) => ({ category_id: 2, category_code: 'FFE', category_name: 'FF&E', expense_account_id: 13, expense_account_code: '7520', expense_account_name: 'Depreciation FF&E', accumulated_account_id: 12, accumulated_account_code: '1592', accumulated_account_name: 'Accumulated depreciation FF&E', department_id: 6, department_name: 'Rooms', assets: 1, amount: '2125000', ...over })
const deprPreview = (groups: unknown[]) => ({ month: '2026-10-01', journal_date: '2026-10-31', status: 'POSTED', next_month: null, total: '3041667', asset_count: 2, catch_up_count: 0, blockers: [], can_run: false, groups, lines: [] })
const summary = { count: 1, cost: '36000000', accum_depr: '13375000', carrying_value: '22625000' }

let rules: unknown[] = []
function ok(path: string): unknown {
  if (path.endsWith('/assets/waiting')) return { data: waitingPage() }
  if (path.endsWith('/assets/depreciation')) return { data: deprOverview }
  if (path.endsWith('/preview')) return { data: deprPreview([group({}), group({ department_id: 7, department_name: 'F&B', amount: '916667' })]) }
  if (path.endsWith('/schedule')) return { data: { asset_id: 2, number: 'FA-000012', name: 'Deluxe chairs', depreciable: true, cost: '36000000', residual_value: '0', life_months: 96, method: 'SL', rows: [] } }
  if (path.endsWith('/history')) return { data: history }
  if (path.endsWith('/assets/categories')) return { data: { data: categories } }
  if (path.endsWith('/assets/locations')) return { data: { data: [{ id: 1, code: 'LOBBY', name: 'Lobby', kind: 'AREA', label_code: 'ABCDEFGHJKLM', is_active: true, asset_count: 2 }, { id: 2, code: 'WINGB', name: 'Wing B', kind: 'AREA', label_code: 'ABCDEFGHJKLN', is_active: true, asset_count: 0 }] } }
  if (path.endsWith('/assets/accounts')) return { data: { data: accounts } }
  if (path.endsWith('/approval-rules/in-force')) return { data: { data: rules } }
  if (path.endsWith('/disposal-requests')) return { data: [requestRow] }
  if (path.endsWith('/assets/disposals')) return { data: [disposalRow] }
  if (path.endsWith('/assets/{id}')) return { data: detail }
  if (path.endsWith('/assets')) return { data: { data: [asset], total: 1, summary } }
  if (path.endsWith('/departments')) return { data: { data: [{ id: 6, code: 'RM', name: 'Rooms', level: 1, is_active: true }, { id: 7, code: 'FB', name: 'F&B', level: 1, is_active: true }] } }
  return { data: { data: [] } }
}

const blank = { template: '<div />' }
const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: blank }] })
const mounted: VueWrapper[] = []
const READ = ['asset.read']
const MANAGE = ['asset.read', 'asset.manage']
const DISPOSE = ['asset.read', 'asset.manage', 'asset.dispose']

async function setup(component: unknown, permissions: string[], route: string, get?: (path: string) => unknown) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'm@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  usePropertyStore().current = { id: 7, currency_decimals: 0 } as never
  resetDepartments()
  GET = vi.fn(async (path: string) => (get ? get(path) : ok(path)))
  POST = vi.fn().mockResolvedValue({ data: undefined })
  PATCH = vi.fn().mockResolvedValue({ data: detail })
  await router.push(route)
  await router.isReady()
  const w = mount(component as never, { attachTo: document.body, global: { plugins: [pinia, router] } })
  mounted.push(w)
  await flushPromises()
  return w
}
const body = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)
const has = (sel: string) => document.body.querySelector(sel) !== null
async function type(sel: string, value: string): Promise<void> {
  const el = document.body.querySelector(sel) as HTMLInputElement
  el.value = value
  el.dispatchEvent(new Event('input', { bubbles: true }))
  await flushPromises()
}
async function choose(sel: string, value: string | number): Promise<void> {
  const el = document.body.querySelector(sel) as HTMLSelectElement
  el.value = String(value)
  el.dispatchEvent(new Event('change', { bubbles: true }))
  await flushPromises()
}
async function click(sel: string): Promise<void> {
  ;(document.body.querySelector(sel) as HTMLElement).click()
  await flushPromises()
}
const disabled = (sel: string): boolean => (document.body.querySelector(sel) as HTMLButtonElement).disabled
const callTo = (mock: ReturnType<typeof vi.fn>, suffix: string) => mock.mock.calls.find((c) => String(c[0]).endsWith(suffix))
/** The preview of the disposal comes after a short pause once the form is complete. */
function unmountAll(): void {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
}
const pause = async (): Promise<void> => {
  await new Promise((r) => setTimeout(r, 450))
  await flushPromises()
}

beforeEach(() => {
  setLocale('en')
  rules = []
})
afterEach(() => unmountAll())

describe('the actions on the asset page', () => {
  const at = (permissions = DISPOSE, get?: (path: string) => unknown) => setup(AssetView, permissions, '/assets/2', get)

  it('offers each change to who may make it, and nothing to who may only read', async () => {
    await at(READ)
    expect(has('[data-testid=asset-actions]')).toBe(false)
    unmountAll()
    await at(MANAGE)
    expect(has('[data-testid=act-move]')).toBe(true)
    expect(has('[data-testid=act-terms]')).toBe(true)
    expect(has('[data-testid=act-dispose]')).toBe(false) // asset.dispose is a permission of its own
    unmountAll()
    await at(DISPOSE)
    expect(has('[data-testid=act-dispose]')).toBe(true)
  })

  it('leaves the asset alone while its disposal waits for approval and says so', async () => {
    await at(DISPOSE, (path) => (path.endsWith('/assets/{id}') ? { data: { ...detail, disposal_pending: true } } : ok(path)))
    expect(has('[data-testid=asset-actions]')).toBe(false)
    expect(body('[data-testid=asset-disposal-pending]').text()).toBe('Waiting for approval')
    expect(body('[data-testid=disposal-pending-note]').text()).toContain('cannot be moved')
  })

  it('moves the asset to another place with a reason and an Idempotency-Key', async () => {
    await at()
    await click('[data-testid=act-move]')
    expect(disabled('[data-testid=move-confirm]')).toBe(true)
    await click('[data-testid=move-location-on]')
    await choose('[data-testid=move-location]', 2)
    await type('input[name=reason]', 'new wing')
    POST.mockResolvedValue({ data: { ...detail, location_id: 2, location_name: 'Wing B' } })
    await click('[data-testid=move-confirm]')
    const call = callTo(POST, '/assets/{id}/moves')
    expect(call?.[1].body).toEqual({ change_location: true, location_id: 2, change_department: false, department_id: null, reason: 'new wing' })
    expect(String(call?.[1].params.header['Idempotency-Key']).length).toBeGreaterThan(10)
    expect(has('[data-testid=move-dialog]')).toBe(false)
  })

  it('changes the life of the asset', async () => {
    await at()
    await click('[data-testid=act-terms]')
    await type('[data-testid=revise-life]', '120')
    await type('input[name=reason]', 'longer life')
    await click('[data-testid=revise-confirm]')
    expect(callTo(POST, '/assets/{id}/revisions')?.[1].body).toEqual({ kind: 'LIFE', reason: 'longer life', life_months: 120 })
  })

  it('moves the asset to another category and says what the journal does', async () => {
    await at()
    await click('[data-testid=act-category]')
    await choose('[data-testid=revise-category]', 3)
    expect(body('[data-testid=revise-journal-note]').text()).toContain('FF&E')
    expect(body('[data-testid=revise-journal-note]').text()).toContain('IT equipment')
    await type('input[name=reason]', 'it is a computer')
    await click('[data-testid=revise-confirm]')
    expect(callTo(POST, '/assets/{id}/revisions')?.[1].body).toEqual({ kind: 'CATEGORY', reason: 'it is a computer', category_id: 3 })
  })

  it('finishes a construction in progress with the date it was put in service', async () => {
    const cip = { ...detail, category_kind: 'CIP', category_name: 'Construction in progress', method: null, life_months: null, in_service_date: '2026-08-01' }
    await at(DISPOSE, (path) => (path.endsWith('/assets/{id}') ? { data: cip } : ok(path)))
    expect(body('[data-testid=act-category]').text()).toBe('Finished building')
    expect(has('[data-testid=act-terms]')).toBe(false)
    await click('[data-testid=act-category]')
    expect(has('[data-testid=revise-cip]')).toBe(true)
    await choose('[data-testid=revise-category]', 3)
    await type('input[name=in_service_date]', '2026-11-01')
    await type('input[name=reason]', 'opened')
    await click('[data-testid=revise-confirm]')
    expect(callTo(POST, '/assets/{id}/revisions')?.[1].body).toEqual({ kind: 'CATEGORY', reason: 'opened', category_id: 3, in_service_date: '2026-11-01', start_rule: 'IN_SERVICE_MONTH' })
  })

  it('sends "add value" to the waiting list with the asset', async () => {
    await at()
    expect(document.body.querySelector('[data-testid=act-addvalue]')?.getAttribute('href')).toBe('/assets/waiting?addTo=2')
  })

  it('shows what happened to the asset in the history tab', async () => {
    await at()
    await click('[data-testid=tab-history]')
    expect(body('[data-testid=history-0]').text()).toContain('Moved: place Lobby → Wing B')
    expect(body('[data-testid=history-0]').text()).toContain('new wing')
    expect(body('[data-testid=history-1]').text()).toContain('Life changed: 96 → 120')
    expect(body('[data-testid=history-2]').text()).toContain('Depreciation Oct 2026: 375,000')
    expect(body('[data-testid=history-2]').text()).toContain('JV-000412')
  })
})

describe('the disposal of an asset', () => {
  let result: unknown = { data: undefined }
  let previewBody: unknown = preview
  const open = async (permissions = DISPOSE) => {
    result = { data: undefined }
    previewBody = preview
    await setup(AssetView, permissions, '/assets/2')
    POST = vi.fn(async (path: string) => (String(path).endsWith('/dispose/preview') ? { data: previewBody } : result))
    await click('[data-testid=act-dispose]')
  }
  async function fillSale(): Promise<void> {
    await type('[data-testid=dispose-price]', '25000000')
    await choose('[data-testid=dispose-account]', 31)
    await pause()
  }

  it('groups the digits of the sale price while it is typed and sends the plain amount', async () => {
    await open()
    await type('[data-testid=dispose-price]', '25000000')
    expect((document.body.querySelector('[data-testid=dispose-price]') as HTMLInputElement).value).toBe('25,000,000')
  })

  it('offers only the accounts that may take the price: cash, bank and 1230', async () => {
    await open()
    const options = [...document.body.querySelectorAll('[data-testid=dispose-account] option')].map((o) => o.textContent?.trim())
    expect(options).toEqual(['–', '1110 · Cash', '1230 · Other receivables'])
    expect(has('[data-testid=dispose-preview]')).toBe(false)
    expect(disabled('[data-testid=dispose-confirm]')).toBe(true)
  })

  it('shows the journal, the gain and the note on VAT, then disposes of the asset at once when the rule is free', async () => {
    await open()
    await fillSale()
    expect(body('[data-testid=dispose-line-1110]').text()).toContain('25,000,000')
    expect(body('[data-testid=dispose-line-8110]').text()).toContain('2,375,000')
    expect(body('[data-testid=dispose-gain]').text()).toBe('2,375,000')
    expect(body('[data-testid=dispose-vat]').text()).toContain('tax consultant')
    expect(body('[data-testid=dispose-approval]').text()).toContain('not needed')
    expect(body('[data-testid=dispose-confirm]').text()).toBe('Approve now') // the person may approve their own: the button posts at once
    expect(has('[data-testid=dispose-approve-now]')).toBe(false)
    expect(has('[data-testid=dispose-approve-note]')).toBe(false)
    expect(callTo(POST, '/dispose/preview')?.[1].body).toMatchObject({ kind: 'SALE', reason_code: 'SOLD', proceeds: '25000000', proceeds_account_id: 31 })
    result = { data: { status: 'POSTED', disposal: { number: 'FAD-000004' }, request: null, replayed: false } }
    await click('[data-testid=dispose-confirm]')
    const call = callTo(POST, '/assets/{id}/dispose')
    expect(call?.[1].body).toMatchObject({ kind: 'SALE', proceeds: '25000000', proceeds_account_id: 31 })
    expect(String(call?.[1].params.header['Idempotency-Key']).length).toBeGreaterThan(10)
    expect(has('[data-testid=dispose-dialog]')).toBe(false)
  })

  it('makes a request when the rule asks for someone else, and says the amount it follows', async () => {
    rules = [{ kind: 'ASSET_DISPOSAL', rule: 'DISTINCT', threshold: '0' }]
    await open()
    await fillSale()
    expect(body('[data-testid=dispose-approval]').text()).toContain('someone else is needed (amount 25,000,000)')
    expect(body('[data-testid=dispose-confirm]').text()).toBe('Request disposal')
    expect(has('[data-testid=dispose-approve-now]')).toBe(true)
    expect(disabled('[data-testid=dispose-approve-now]')).toBe(true) // someone else must approve: the button to approve at once is off, with the reason
    expect(body('[data-testid=dispose-approve-note]').text()).toContain('needs the approval of someone else')
    result = { data: { status: 'PENDING', disposal: null, request: requestRow, replayed: false } }
    await click('[data-testid=dispose-confirm]')
    expect(callTo(POST, '/assets/{id}/dispose')).toBeDefined()
    expect(has('[data-testid=dispose-dialog]')).toBe(false)
  })

  it('lets the person approve their own disposal below the limit of DISTINCT_ABOVE, and asks for someone else above it', async () => {
    rules = [{ kind: 'ASSET_DISPOSAL', rule: 'DISTINCT_ABOVE', threshold: '30000000' }]
    await open()
    await fillSale()
    expect(body('[data-testid=dispose-approval]').text()).not.toContain('someone else is needed')
    expect(body('[data-testid=dispose-confirm]').text()).toBe('Approve now')
    expect(disabled('[data-testid=dispose-confirm]')).toBe(false)
    expect(has('[data-testid=dispose-approve-now]')).toBe(false)
  })

  it('asks for someone else above the limit of DISTINCT_ABOVE and turns the button to approve at once off', async () => {
    rules = [{ kind: 'ASSET_DISPOSAL', rule: 'DISTINCT_ABOVE', threshold: '10000000' }]
    await open()
    await fillSale()
    expect(body('[data-testid=dispose-confirm]').text()).toBe('Request disposal')
    expect(disabled('[data-testid=dispose-approve-now]')).toBe(true)
    expect(body('[data-testid=dispose-approve-note]').text()).toContain('needs the approval of someone else')
  })

  it('writes an asset off without a price, and asks for a note when the reason is "other"', async () => {
    await open()
    await click('[data-testid=dispose-writeoff]')
    expect(has('[data-testid=dispose-proceeds]')).toBe(false)
    await choose('[data-testid=dispose-reason]', 'OTHER')
    await pause()
    expect(has('[data-testid=dispose-preview]')).toBe(false) // an "other" needs a note
    await type('input[name=note]', 'scrapped after the flood')
    await pause()
    expect(callTo(POST, '/dispose/preview')?.[1].body).toMatchObject({ kind: 'WRITE_OFF', reason_code: 'OTHER', proceeds: null, proceeds_account_id: null, note: 'scrapped after the flood' })
  })

  it('says the VAT of the sale is not computed when the property is a taxable entrepreneur', async () => {
    await open()
    previewBody = { ...preview, pkp_on_date: true }
    await fillSale()
    expect(body('[data-testid=dispose-vat]').text()).toContain('VAT-registered')
  })
})

describe('the disposals and their approval', () => {
  const at = (permissions = DISPOSE) => setup(AssetDisposalsView, permissions, '/assets/disposals')

  it('lists the requests that wait and approves one', async () => {
    await at()
    expect(body('[data-testid=tab-pending]').text()).toContain('Waiting for approval (1)')
    expect(body('[data-testid=request-9]').text()).toContain('FA-000012')
    expect(body('[data-testid=request-9]').text()).toContain('25,000,000')
    expect(body('[data-testid=request-9-proceeds]').text()).toContain('25,000,000') // the price with the separator of thousands
    expect(body('[data-testid=request-9]').text()).not.toContain('2026-11-06T02:00:00Z') // the time is formatted like the rest of the app
    POST.mockResolvedValue({ data: { status: 'POSTED', disposal: { number: 'FAD-000004' }, request: null, replayed: false } })
    await click('[data-testid=approve-9]')
    expect(callTo(POST, '/disposal-requests/{id}/approve')?.[1].params.path).toEqual({ propertyId: 7, id: 9 })
  })

  it('refuses a request only with a reason', async () => {
    await at()
    await click('[data-testid=reject-9]')
    expect(disabled('[data-testid=reject-confirm]')).toBe(true)
    await type('input[name=note]', 'sold below value')
    await click('[data-testid=reject-confirm]')
    expect(callTo(POST, '/disposal-requests/{id}/reject')?.[1].body).toEqual({ note: 'sold below value' })
  })

  it('does not offer the decision to who may only read', async () => {
    await at(READ)
    expect(has('[data-testid=approve-9]')).toBe(false)
    expect(has('[data-testid=reject-9]')).toBe(false)
  })

  it('reverses a posted disposal with a reason and the approval of the corrections', async () => {
    await at()
    await click('[data-testid=tab-disposals]')
    expect(body('[data-testid=disposal-FAD-000003]').text()).toContain('2,375,000')
    await click('[data-testid=reverse-FAD-000003]')
    await type('input[name=reason]', 'entered twice')
    await click('[data-testid=reverse-next]')
    expect(has('[data-testid=reverse-dialog]')).toBe(false) // the dialog of approval takes its place
    expect(document.body.querySelector('input[type=password]')).not.toBeNull()
  })
})

describe('the waiting list adds value to an asset', () => {
  const at = (route = '/assets/waiting') => setup(AssetWaitingView, MANAGE, route, (path) => (path.endsWith('/assets') ? { data: { data: [asset], total: 1, summary } } : ok(path)))

  it('posts the line as an addition to the chosen asset, with no journal', async () => {
    await at()
    await click('[data-testid=add-1]')
    expect(body('[data-testid=addvalue-source]').text()).toContain('BL-000231')
    expect(disabled('[data-testid=addvalue-confirm]')).toBe(true)
    await choose('[data-testid=addvalue-asset]', 2)
    expect(body('[data-testid=addvalue-effect]').text()).toContain('40,000,000') // 36,000,000 and the line of 4,000,000
    await type('input[name=reason]', 'new covers')
    await click('[data-testid=addvalue-confirm]')
    const call = callTo(POST, '/assets/{id}/revisions')
    expect(call?.[1].params.path).toEqual({ propertyId: 7, id: 2 })
    expect(call?.[1].body).toEqual({ kind: 'CAPITALIZE', reason: 'new covers', journal_line_id: 1 })
  })

  it('has the asset chosen when the asset page sent the person here', async () => {
    await at('/assets/waiting?addTo=2')
    await click('[data-testid=add-1]')
    expect((document.body.querySelector('[data-testid=addvalue-asset]') as HTMLSelectElement).value).toBe('2')
  })
})

describe('the register shows the disposals that wait', () => {
  it('flags the asset and counts the requests on the link', async () => {
    await setup(AssetRegisterView, DISPOSE, '/assets', (path) => (path.endsWith('/assets') ? { data: { data: [{ ...asset, disposal_pending: true }], total: 1, summary } } : ok(path)))
    expect(body('[data-testid=asset-FA-000012]').text()).toContain('Waiting for approval')
    expect(body('[data-testid=go-disposals]').text()).toBe('Disposals (1 waiting)')
  })
})

describe('what the review of PR 2 asked for in the depreciation', () => {
  const at = (get?: (path: string) => unknown) => setup(AssetDepreciationView, ['asset.read', 'asset.depreciate'], '/assets/depreciation', get)

  it('names the accounts, labels the total "Total" and lists the months oldest first', async () => {
    await at()
    expect(body('[data-testid=debit-7520]').text()).toContain('7520 · Depreciation FF&E')
    expect(body('[data-testid=credit-1592]').text()).toContain('1592 · Accumulated depreciation FF&E')
    expect(body('[data-testid=depr-journal]').text()).toContain('Total')
    expect(body('[data-testid=depr-journal]').text()).not.toContain('harga')
    const months = [...document.body.querySelectorAll('[data-testid^=month-]')].map((e) => e.getAttribute('data-testid'))
    expect(months).toEqual(['month-2026-10', 'month-2026-11'])
  })

  it('says what was checked, and that the departments are filled only when every line has one', async () => {
    await at()
    expect(body('[data-testid=depr-clear]').text()).toContain('Nothing blocks the run. Checked: months in order, every account is active and open to posting, every line has a department.')
    unmountAll()
    await at((path) => (path.endsWith('/preview') ? { data: deprPreview([group({}), group({ department_id: null, department_name: null, amount: '916667' })]) } : ok(path)))
    const text = body('[data-testid=depr-clear]').text()
    expect(text).toContain('a department wherever the account asks for one')
    expect(text).not.toContain('every line has a department')
  })

  it('lets the reversal be confirmed in the normal colour', async () => {
    await at()
    await click('[data-testid=depr-reverse]')
    expect(document.body.querySelector('[data-testid=reverse-confirm]')?.className).not.toContain('destructive')
  })
})

describe('the capitalisation threshold of the Make-asset dialog', () => {
  it('shows no "above the threshold" badge when the threshold is zero', async () => {
    await setup(AssetWaitingView, MANAGE, '/assets/waiting', (path) => (path.endsWith('/assets/waiting') ? { data: waitingPage('0') } : ok(path)))
    await click('[data-testid=make-1]')
    expect(has('[data-testid=make-above]')).toBe(false)
    expect(has('[data-testid=make-threshold]')).toBe(false)
  })

  it('shows the badge when there is a threshold and the line is above it', async () => {
    await setup(AssetWaitingView, MANAGE, '/assets/waiting')
    await click('[data-testid=make-1]')
    expect(has('[data-testid=make-above]')).toBe(true)
  })
})
