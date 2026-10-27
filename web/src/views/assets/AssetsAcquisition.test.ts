import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { resetDepartments } from '@/composables/useDepartments'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import AssetDepreciationView from './AssetDepreciationView.vue'
import AssetOpeningView from './AssetOpeningView.vue'
import AssetRegisterView from './AssetRegisterView.vue'
import AssetView from './AssetView.vue'
import AssetWaitingView from './AssetWaitingView.vue'
import { splitCost } from './split'

/** PR 2 of the fixed assets: the waiting list, the Make-asset dialog, the monthly depreciation, the schedule tab, and the review changes of the opening balances screen. */
let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))
vi.mock('@/utils/documents', async (orig) => ({ ...(await orig<typeof import('@/utils/documents')>()), downloadFile: vi.fn() }))

const categories = [
  { id: 2, code: 'FFE', name: 'FF&E', kind: 'FFE', cost_account_id: 11, cost_account_code: '1530', cost_account_name: 'FF&E', accumulated_account_id: 12, accumulated_account_code: '1592', expense_account_id: 13, expense_account_code: '7520', method: 'SL', life_months: 96, residual_pct: '0', fiscal_group: 'G2', is_depreciable: true, is_active: true, sort_order: 1, asset_count: 3, version: 2, department_id: 6 },
]
const asset = {
  id: 2, number: 'FA-000012', name: 'Deluxe chairs', category_id: 2, category_code: 'FFE', category_name: 'FF&E', category_kind: 'FFE', source: 'JOURNAL', status: 'ACTIVE', acquisition_date: '2026-03-01', in_service_date: '2026-03-01', start_rule: 'IN_SERVICE_MONTH', residual_value: '0',
  life_months: 96, method: 'SL', first_dep_month: '2026-03-01', fully_depreciated: false, unit_count: 1, fiscal_group: 'NONE', fiscal_opening_estimated: false, version: 3, dep_months_done: 14, cost: '36000000', accum_depr: '13375000', carrying_value: '22625000', remaining_months: 82,
}
const detail = { ...asset, opening_import_number: null, source_link: { journal_line_id: 1, unit_no: 1, units: 1, cost: '36000000', journal_id: 11, journal_number: 'JV-000011', journal_date: '2026-03-01', journal_type: 'AP', bill_number: 'BL-000188', bill_line_no: 2 }, ledger: [] }

const waitingLine = (over: Record<string, unknown>) => ({
  line_id: 1, journal_id: 11, journal_number: 'JV-000011', journal_type: 'AP', journal_date: '2026-11-03', line_no: 1, source: 'BILL', document_number: 'BL-000231', account_id: 11, account_code: '1530', account_name: 'FF&E', description: 'Split AC 1 PK (4 units)',
  cost: '22000000', category_id: 2, category_code: 'FFE', category_name: 'FF&E', category_kind: 'FFE', bill_number: 'BL-000231', supplier_name: 'PT Sejuk Abadi', vat_treatment: 'CREDITABLE', age_days: 3, below_threshold: false, dismissed: false, ...over,
})
const waitingPage = {
  data: [
    waitingLine({}),
    waitingLine({ line_id: 2, source: 'MANUAL', document_number: 'JV-000340', bill_number: null, supplier_name: null, vat_treatment: null, description: 'Garden lamp', cost: '1800000', below_threshold: true }),
  ],
  total: 2, total_cost: '23800000', register_from: '2026-10-01', capitalization_threshold: '2500000', counts: { waiting: 2, waiting_cost: '23800000', dismissed: 1, dismissed_cost: '500000' },
}
const deprOverview = {
  register_from: '2026-10-01', next_month: '2026-10-01', business_date: '2026-11-06',
  months: [
    { month: '2026-10-01', status: 'NOT_RUN', total: '0', asset_count: 0, period_closed: false, is_next: true, runnable: true, reversible: false },
    { month: '2026-11-01', status: 'NOT_RUN', total: '0', asset_count: 0, period_closed: false, is_next: false, runnable: false, reversible: false },
  ],
}
const group = (over: Record<string, unknown>) => ({ category_id: 2, category_code: 'FFE', category_name: 'FF&E', expense_account_id: 13, expense_account_code: '7520', accumulated_account_id: 12, accumulated_account_code: '1592', department_id: 6, department_name: 'Rooms', assets: 1, amount: '2125000', ...over })
const line = (n: number, dept: number, months: number, catchUp: boolean) => ({ asset_id: n, asset_number: `FA-0000${n}`, asset_name: `Asset ${n}`, category_id: 2, category_code: 'FFE', category_name: 'FF&E', expense_account_id: 13, expense_account_code: '7520', department_id: dept, months, from_month: '2026-10-01', catch_up: catchUp, amount: '1', accum_after: '0', carrying_after: '0', months_done_after: months })
const deprPreview = {
  month: '2026-10-01', journal_date: '2026-10-31', status: 'NOT_RUN', next_month: '2026-10-01', total: '3041667', asset_count: 2, catch_up_count: 1, blockers: [], can_run: true,
  groups: [group({}), group({ department_id: 7, department_name: 'F&B', amount: '916667' })], lines: [line(12, 6, 1, false), line(31, 7, 2, true)],
}
const schedule = {
  asset_id: 2, number: 'FA-000012', name: 'Deluxe chairs', depreciable: true, cost: '36000000', residual_value: '0', life_months: 96, method: 'SL',
  rows: [
    { month: '2026-09-01', kind: 'OPENING', months: 13, amount: '13000000', accum_after: '13000000', carrying_after: '23000000', months_done: 13 },
    { month: '2026-10-01', kind: 'POSTED', months: 1, amount: '375000', accum_after: '13375000', carrying_after: '22625000', months_done: 14, journal_number: 'JV-000412' },
    { month: '2026-11-01', kind: 'PROJECTED', months: 1, amount: '375000', accum_after: '13750000', carrying_after: '22250000', months_done: 15 },
    { month: '2027-01-01', kind: 'PROJECTED', months: 1, amount: '375000', accum_after: '14125000', carrying_after: '21875000', months_done: 16 },
  ],
}
const summary = { count: 1, cost: '36000000', accum_depr: '13375000', carrying_value: '22625000' }

function ok(path: string): unknown {
  if (path.endsWith('/assets/waiting')) return { data: waitingPage }
  if (path.endsWith('/assets/depreciation')) return { data: deprOverview }
  if (path.endsWith('/preview')) return { data: deprPreview }
  if (path.endsWith('/schedule')) return { data: schedule }
  if (path.endsWith('/assets/categories')) return { data: { data: categories } }
  if (path.endsWith('/assets/locations')) return { data: { data: [{ id: 1, code: 'LOBBY', name: 'Lobby', kind: 'AREA', label_code: 'ABCDEFGHJKLM', is_active: true, asset_count: 2 }] } }
  if (path.endsWith('/assets/opening/imports')) return { data: { data: [] } }
  if (path.endsWith('/assets/{id}')) return { data: detail }
  if (path.endsWith('/assets')) return { data: { data: [asset], total: 1, summary } }
  if (path.endsWith('/departments')) return { data: { data: [{ id: 6, code: 'RM', name: 'Rooms', level: 1, is_active: true }, { id: 7, code: 'FB', name: 'F&B', level: 1, is_active: true }] } }
  return { data: { data: [] } }
}

const blank = { template: '<div />' }
const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: blank }] })
const mounted: VueWrapper[] = []
const ALL = ['asset.read']
const MANAGE = ['asset.read', 'asset.manage', 'asset.setup']
const DEPR = ['asset.read', 'asset.depreciate']

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

beforeEach(() => setLocale('en'))
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
})

describe('the cost divided into units', () => {
  it('gives every unit but the last the rounded share and the last the rest', () => {
    expect(splitCost('100', 3, 0)).toEqual(['33', '33', '34'])
    expect(splitCost('22000000', 4, 0)).toEqual(['5500000', '5500000', '5500000', '5500000'])
    expect(splitCost('10.00', 3, 2)).toEqual(['3.33', '3.33', '3.34'])
    expect(splitCost('101', 2, 0)).toEqual(['50', '51']) // the share is rounded down, as the server does
    expect(splitCost('18000000', 7, 0)).toEqual(['2571428', '2571428', '2571428', '2571428', '2571428', '2571428', '2571432'])
  })
})

describe('the waiting list', () => {
  const at = (permissions = MANAGE, get?: (path: string) => unknown) => setup(AssetWaitingView, permissions, '/assets/waiting', get)

  it('lists the lines with their source and document, and flags the one below the threshold', async () => {
    await at()
    expect(body('[data-testid=tab-waiting]').text()).toContain('Waiting · 2')
    expect(body('[data-testid=tab-dismissed]').text()).toContain('Dismissed · 1')
    expect(body('[data-testid=line-1]').text()).toContain('Bill')
    expect(body('[data-testid=line-1]').text()).toContain('BL-000231 · PT Sejuk Abadi')
    expect(body('[data-testid=line-1]').text()).toContain('Input VAT is credited')
    expect(body('[data-testid=line-2]').text()).toContain('Manual')
    expect(body('[data-testid=line-2]').text()).toContain('JV-000340')
    expect(body('[data-testid=line-2] [data-testid=below-threshold]').text()).toContain('2,500,000')
    expect(has('[data-testid=line-1] [data-testid=below-threshold]')).toBe(false)
    expect(body('[data-testid=waiting-total]').text()).toContain('23,800,000')
  })

  it('asks the server for a source when it is chosen, and for the dismissed lines on the second tab', async () => {
    await at()
    await choose('[data-testid=filter-source]', 'BANK')
    expect(GET.mock.calls.at(-1)?.[1].params.query).toMatchObject({ source: 'BANK', dismissed: false })
    await click('[data-testid=tab-dismissed]')
    expect(GET.mock.calls.at(-1)?.[1].params.query).toMatchObject({ dismissed: true })
  })

  it('does not offer the actions to who may only read', async () => {
    await at(ALL)
    expect(has('[data-testid=make-1]')).toBe(false)
    expect(has('[data-testid=dismiss-1]')).toBe(false)
  })

  it('opens the dialog with the threshold warning for a small line, and no warning for a large one', async () => {
    await at()
    await click('[data-testid=make-2]')
    expect(has('[data-testid=make-threshold]')).toBe(true)
    expect(body('[data-testid=make-threshold]').text()).toContain('2,500,000')
  })

  it('splits the cost over several units with the rest on the last, and posts the request with an Idempotency-Key', async () => {
    await at(MANAGE, (path) => (path.endsWith('/assets/waiting') ? { data: { ...waitingPage, data: [waitingLine({ cost: '100000000' })] } } : ok(path)))
    await click('[data-testid=make-1]')
    expect(has('[data-testid=make-threshold]')).toBe(false)
    await type('input[name=units]', '3')
    expect(body('[data-testid=make-unit-1]').text()).toContain('33,333,333')
    expect(body('[data-testid=make-unit-3]').text()).toContain('33,333,334')
    expect(body('[data-testid=make-unit-1]').text()).toContain('347,222') // 33,333,333 over 96 months
    POST.mockResolvedValue({ data: { assets: [{ id: 1, number: 'FA-000001', name: 'x', cost: '33333333' }], warnings: [], replayed: false } })
    await click('[data-testid=make-confirm]')
    const call = callTo(POST, '/assets/from-line')
    expect(call?.[1].body).toMatchObject({ journal_line_id: 1, units: 3, category_id: 2, start_rule: 'IN_SERVICE_MONTH', in_service_date: '2026-11-03' })
    expect(String(call?.[1].params.header['Idempotency-Key']).length).toBeGreaterThan(10)
    expect(has('[data-testid=make-dialog]')).toBe(false) // done: the dialog closes and the list is loaded again
  })

  it('lets the start rule be the month after', async () => {
    await at()
    await click('[data-testid=make-1]')
    await click('input[name=start_rule][value=NEXT_MONTH]')
    await click('[data-testid=make-confirm]')
    expect(callTo(POST, '/assets/from-line')?.[1].body.start_rule).toBe('NEXT_MONTH')
  })

  it('dismisses a line with a reason', async () => {
    await at()
    await click('[data-testid=dismiss-1]')
    expect(disabled('[data-testid=dismiss-confirm]')).toBe(true)
    await type('input[name=reason]', 'a gift, not bought')
    await click('[data-testid=dismiss-confirm]')
    expect(callTo(POST, '/waiting/dismiss')?.[1].body).toEqual({ journal_line_id: 1, reason: 'a gift, not bought' })
  })

  it('restores a dismissed line', async () => {
    await at(MANAGE, (path) => (path.endsWith('/assets/waiting') ? { data: { ...waitingPage, data: [waitingLine({ dismissed: true, dismissed_reason: 'a gift' })] } } : ok(path)))
    await click('[data-testid=restore-1]')
    expect(callTo(POST, '/waiting/restore')?.[1].body).toEqual({ journal_line_id: 1 })
  })
})

describe('the monthly depreciation', () => {
  const at = (permissions = DEPR, get?: (path: string) => unknown) => setup(AssetDepreciationView, permissions, '/assets/depreciation', get)

  it('lists the months and previews the next one: the journal by category and department, balanced', async () => {
    await at()
    expect(body('[data-testid=month-2026-10]').text()).toContain('Ready')
    expect(body('[data-testid=month-2026-11]').text()).toBe('Nov 2026–') // a month after the next one waits for it
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/preview') && c[1].params.path.month === '2026-10')).toBe(true)
    expect(body('[data-testid=depr-journal]').text()).toContain('Rooms')
    expect(body('[data-testid=depr-journal]').text()).toContain('F&B')
    expect(body('[data-testid=credit-1592]').text()).toContain('3,041,667')
    expect(body('[data-testid=depr-total]').text()).toBe('3,041,667')
    expect(body('[data-testid=depr-counts]').text()).toContain('1 catching up')
    expect(has('[data-testid=depr-clear]')).toBe(true)
  })

  it('runs the month with an Idempotency-Key', async () => {
    await at()
    POST.mockResolvedValue({ data: { run: { journal_number: 'JV-000412' }, lines: [], groups: [], replayed: false } })
    await click('[data-testid=depr-run]')
    const call = callTo(POST, '/depreciation/{month}/run')
    expect(call?.[1].params.path.month).toBe('2026-10')
    expect(String(call?.[1].params.header['Idempotency-Key']).length).toBeGreaterThan(10)
  })

  it('words the blockers and does not let the run happen', async () => {
    const blocked = {
      ...deprPreview, can_run: false,
      blockers: [
        { code: 'DEPARTMENT_REQUIRED', message: 'x', params: { asset: 'FA-000075', account: '7520' } },
        { code: 'ACCOUNT_INACTIVE', message: 'x', params: { account: '7550', category: 'IT' } },
        { code: 'DEPRECIATION_MONTH_INVALID', message: 'x', params: { reason: 'NOT_NEXT', month: '2026-11', next: '2026-10' } },
      ],
    }
    await at(DEPR, (path) => (path.endsWith('/preview') ? { data: blocked } : ok(path)))
    expect(body('[data-testid=blocker-DEPARTMENT_REQUIRED]').text()).toContain('FA-000075 needs a department')
    expect(body('[data-testid=blocker-ACCOUNT_INACTIVE]').text()).toContain('Account 7550')
    expect(body('[data-testid=blocker-DEPRECIATION_MONTH_INVALID]').text()).toContain('Oct 2026 comes first')
    expect(disabled('[data-testid=depr-run]')).toBe(true)
  })

  it('reverses the latest run with a reason', async () => {
    const posted = { ...deprOverview, months: [{ ...deprOverview.months[0], status: 'POSTED', total: '3041667', journal_number: 'JV-000412', reversible: true, runnable: false }] }
    await at(DEPR, (path) => (path.endsWith('/assets/depreciation') ? { data: posted } : ok(path)))
    expect(body('[data-testid=depr-posted]').text()).toContain('JV-000412')
    await click('[data-testid=depr-reverse]')
    await type('input[name=reason]', 'wrong department')
    await click('[data-testid=reverse-confirm]')
    expect(callTo(POST, '/depreciation/{month}/reverse')?.[1].body).toEqual({ reason: 'wrong department' })
  })

  it('needs the permission to depreciate', async () => {
    await at(ALL)
    expect(has('[data-testid=no-depreciate]')).toBe(true)
    expect(has('[data-testid=depr-months]')).toBe(false)
  })
})

describe('the asset page and the register', () => {
  it('shows the schedule by year, then by month with the run that booked each month', async () => {
    await setup(AssetView, MANAGE, '/assets/2')
    expect(body('[data-testid=schedule-year-2026]').text()).toContain('2 months')
    expect(body('[data-testid=schedule-year-2026]').text()).toContain('13,750,000')
    expect(body('[data-testid=schedule-year-2027]').text()).toContain('projected')
    await click('[data-testid=schedule-toggle]')
    expect(body('[data-testid=schedule-month-2026-10]').text()).toContain('JV-000412')
    expect(body('[data-testid=schedule-month-2026-11]').text()).toContain('Projected')
  })

  it('says so when the asset is not depreciated', async () => {
    await setup(AssetView, MANAGE, '/assets/2', (path) => (path.endsWith('/schedule') ? { data: { ...schedule, depreciable: false, rows: [] } } : ok(path)))
    expect(has('[data-testid=schedule-none]')).toBe(true)
  })

  it('names the bill line the asset was made from', async () => {
    await setup(AssetView, MANAGE, '/assets/2')
    expect(body('[data-testid=asset-source]').text()).toContain('BL-000188')
    expect(body('[data-testid=asset-source]').text()).toContain('line 2')
  })

  it('locks the start of the depreciation once a month has been posted', async () => {
    await setup(AssetView, MANAGE, '/assets/2')
    await click('[data-testid=asset-edit]')
    expect(has('[data-testid=edit-start]')).toBe(false) // dep_months_done is 14
  })

  it('lets the start be changed while no month has been depreciated', async () => {
    await setup(AssetView, MANAGE, '/assets/2', (path) => (path.endsWith('/assets/{id}') ? { data: { ...detail, dep_months_done: 0 } } : ok(path)))
    await click('[data-testid=asset-edit]')
    expect(has('[data-testid=edit-start]')).toBe(true)
    await choose('select[name=start_rule]', 'NEXT_MONTH')
    await click('[data-testid=edit-save]')
    expect(PATCH.mock.calls[0]?.[1].body).toMatchObject({ start_rule: 'NEXT_MONTH' })
    expect(PATCH.mock.calls[0]?.[1].body.in_service_date).toBeUndefined()
  })

  it('shows the waiting list count and the depreciation tile on the register', async () => {
    await setup(AssetRegisterView, DEPR, '/assets')
    expect(body('[data-testid=go-waiting]').text()).toContain('Waiting list (2)')
    expect(body('[data-testid=tile-depreciation]').text()).toContain('Oct 2026')
    expect(body('[data-testid=tile-depreciation]').text()).toContain('Ready to run')
  })

  it('hides the depreciation tile from who may not depreciate', async () => {
    await setup(AssetRegisterView, ALL, '/assets')
    expect(has('[data-testid=tile-depreciation]')).toBe(false)
    expect(has('[data-testid=go-waiting]')).toBe(true)
  })
})

describe('the opening balances, review of the first screens', () => {
  const finding = (n: number) => ({ row_no: n, field: 'fiscal_accumulated_depreciation', code: 'FISCAL_ACCUMULATED_ESTIMATED', message: 'computed' })
  const result = (over: Record<string, unknown> = {}) => ({
    dry_run: true, valid: true, replayed: false, mode: 'WITH_JOURNAL', as_of: '2026-09-30', journal_date: '2026-09-30', register_from: '2026-10-01',
    rows: [1, 2].map((n) => ({ row_no: n, name: `Chair ${n}`, category_code: 'FFE', cost: '1000000', accum: '100000', months_done: 4, fiscal_accum: '500000', fully_depreciated: false, errors: [], warnings: [finding(n)] })),
    totals: [], total_cost: '2000000', total_accum: '200000', net_to_equity: '1800000', journal_preview: [], gl_check: [], errors: [], warnings: [finding(1), finding(2)], ...over,
  })
  const check = async (res: unknown) => {
    await setup(AssetOpeningView, MANAGE, '/assets/opening')
    POST.mockResolvedValue({ data: res })
    await type('input[name=as_of]', '2026-09-30')
    await type('[data-testid=csv-text]', 'name\nx')
    await click('[data-testid=dry-run]')
  }

  it('sums a kind of finding once with its row count, and keeps the sentence of each row in the table', async () => {
    await check(result())
    const items = document.body.querySelectorAll('[data-testid=result-warnings] li')
    expect(items.length).toBe(1)
    expect(items[0]?.textContent).toContain('2 row(s)')
    expect(items[0]?.textContent).toContain('rate of the group')
    expect(body('[data-testid=result-row-1]').text()).toContain('rate of the group')
    expect(body('[data-testid=result-row-2]').text()).toContain('rate of the group')
  })

  it('shows the name of the category in the table, not its code', async () => {
    await check(result())
    expect(body('[data-testid=result-category-1]').text()).toBe('FF&E')
  })

  it('colours the banner of an invalid file as an error, not as a success', async () => {
    await check(result({ valid: false, errors: [{ row_no: 1, field: 'cost', code: 'INVALID_VALUE', message: 'bad' }] }))
    const banner = document.body.querySelector('[data-testid=result-invalid]') as HTMLElement
    expect(banner.className).toContain('alert')
    expect(banner.className).not.toContain('notice')
  })
})
