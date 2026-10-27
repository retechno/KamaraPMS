import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { resetDepartments } from '@/composables/useDepartments'
import { i18n, setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import AssetLabelsView from './AssetLabelsView.vue'
import AssetOpeningView from './AssetOpeningView.vue'
import AssetRegisterView from './AssetRegisterView.vue'
import AssetSettingsView from './AssetSettingsView.vue'
import AssetView from './AssetView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PUT = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PUT: (...a: unknown[]) => PUT(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))
const openPdf = vi.fn()
vi.mock('@/utils/documents', async (orig) => ({ ...(await orig<typeof import('@/utils/documents')>()), openPdf: (...a: unknown[]) => openPdf(...a), downloadFile: vi.fn() }))

const categories = [
  { id: 1, code: 'LAND', name: 'Land', kind: 'LAND', cost_account_id: 10, cost_account_code: '1510', cost_account_name: 'Land', residual_pct: '0', fiscal_group: 'NONE', is_depreciable: false, is_active: true, sort_order: 0, asset_count: 1, version: 1 },
  { id: 2, code: 'FFE', name: 'FF&E', kind: 'FFE', cost_account_id: 11, cost_account_code: '1530', cost_account_name: 'FF&E', accumulated_account_id: 12, accumulated_account_code: '1592', expense_account_id: 13, expense_account_code: '7520', method: 'SL', life_months: 96, residual_pct: '0', fiscal_group: 'G2', fiscal_method: 'SL', is_depreciable: true, is_active: true, sort_order: 1, asset_count: 3, version: 2 },
  { id: 3, code: 'CIP', name: 'Construction in progress', kind: 'CIP', cost_account_id: 14, cost_account_code: '1560', cost_account_name: 'CIP', residual_pct: '0', fiscal_group: 'NONE', is_depreciable: false, is_active: true, sort_order: 2, asset_count: 1, version: 1 },
]
const locations = [
  { id: 1, code: 'LOBBY', name: 'Lobby', kind: 'AREA', label_code: 'ABCDEFGHJKLM', is_active: true, asset_count: 2 },
  { id: 2, code: 'R201', name: 'Room 201', kind: 'ROOM', room_id: 4, label_code: 'NOPQRSTUVWXY', is_active: true, asset_count: 0, reference_label: '201' },
]
const base = {
  category_id: 2, category_code: 'FFE', category_name: 'FF&E', category_kind: 'FFE', source: 'JOURNAL', status: 'ACTIVE', acquisition_date: '2026-03-01', in_service_date: '2026-03-01', start_rule: 'IN_SERVICE_MONTH', residual_value: '0', life_months: 96, method: 'SL',
  first_dep_month: '2026-03-01', fully_depreciated: false, unit_count: 1, fiscal_group: 'G2', fiscal_method: 'SL', fiscal_opening_estimated: false, version: 3, dep_months_done: 0,
}
const assets = [
  { ...base, id: 1, number: 'FA-000001', name: 'Hotel land', category_id: 1, category_code: 'LAND', category_name: 'Land', category_kind: 'LAND', cost: '5000000000', accum_depr: '0', carrying_value: '5000000000', life_months: null, method: null, location_name: 'Lobby', department_name: 'Admin' },
  { ...base, id: 2, number: 'FA-000012', name: 'Deluxe chairs', cost: '36000000', accum_depr: '13000000', carrying_value: '23000000', remaining_months: 23, location_name: 'Room 201', department_name: 'Rooms', serial_no: 'SN-9' },
  { ...base, id: 3, number: 'FA-000090', name: 'Pickup L300', source: 'OPENING', cost: '150000000', accum_depr: '150000000', carrying_value: '0', fully_depreciated: true, remaining_months: 0 },
  { ...base, id: 4, number: 'FA-000005', name: 'Annex building', category_id: 3, category_code: 'CIP', category_name: 'Construction in progress', category_kind: 'CIP', cost: '1200000000', accum_depr: '0', carrying_value: '1200000000', life_months: null, method: null },
]
const detail = {
  ...assets[1], opening_import_number: null, supplier_name: 'PT Astra', warranty_until: '2028-02-14', fiscal_accum_opening: '36000000', fiscal_opening_as_of: '2026-09-30', fiscal_months_done_opening: 48, fiscal_opening_estimated: true, dep_months_done: 13,
  ledger: [{ id: 5, kind: 'OPEN', event_date: '2026-09-30', cost_delta: '36000000', accum_delta: '13000000', months_delta: 13, source_type: 'OPENING', source_id: 1, created_at: '2026-10-01T00:00:00Z' }],
}
const settings = { register_from: null, capitalization_threshold: '2500000', start_rule: 'IN_SERVICE_MONTH', oe_policy: 'EXPENSE', oe_default_months: 24, oe_policy_changed_on: null, tax_year_start_month: null, started: false, version: 4 }
const fiscalGroups = [{ code: 'G1', life_years: 4, sl_rate: '25.000', db_rate: '50.000' }, { code: 'BLD_PERMANENT', life_years: 20, sl_rate: '5.000', db_rate: null }]
const dry = {
  dry_run: true, valid: true, replayed: false, mode: 'WITH_JOURNAL', as_of: '2026-09-30', journal_date: '2026-09-30', register_from: '2026-10-01',
  rows: [{ row_no: 1, name: 'Wooden wardrobe', category_code: 'FFE', cost: '36000000', accum: '30375000', months_done: 81, fiscal_accum: '36000000', fully_depreciated: false, warnings: [{ row_no: 1, field: 'fiscal_accumulated_depreciation', code: 'FISCAL_ACCUMULATED_ESTIMATED', message: 'computed' }], errors: [] }],
  totals: [{ category_code: 'FFE', category_name: 'FF&E', count: 1, cost: '36000000', accum: '30375000', net: '5625000' }], total_cost: '36000000', total_accum: '30375000', net_to_equity: '5625000',
  journal_preview: [
    { account_code: '1530', account_name: 'FF&E', debit: '36000000', credit: '0' }, { account_code: '1592', account_name: 'Accumulated depreciation FF&E', debit: '0', credit: '30375000' }, { account_code: '3900', account_name: 'Opening balance equity', debit: '0', credit: '5625000' },
  ],
  gl_check: [], errors: [], warnings: [],
}

function ok(path: string): unknown {
  if (path.endsWith('/assets/categories')) return { data: { data: categories } }
  if (path.endsWith('/assets/locations')) return { data: { data: locations } }
  if (path.endsWith('/assets/fiscal-groups')) return { data: { data: fiscalGroups } }
  if (path.endsWith('/assets/settings')) return { data: settings }
  if (path.endsWith('/assets/setup-check')) return { data: { as_of: '2026-10-26', issues: [{ code: 'NO_LOCATION', severity: 'WARNING', message: 'one asset has no location' }], reconciliation: [{ category_id: 2, category_code: 'FFE', cost_account_code: '1530', register_cost: '36000000', ledger_cost: '36000000', cost_difference: '0', register_accum: '13000000', ledger_accum: '12000000', accum_difference: '1000000' }] } }
  if (path.endsWith('/assets/accounts')) return { data: { data: [{ id: 10, code: '1510', name: 'Land', account_type: 'ASSET' }, { id: 11, code: '1530', name: 'FF&E', account_type: 'ASSET' }] } }
  if (path.endsWith('/assets/opening/imports')) return { data: { data: [{ id: 1, number: 'FAI-000001', as_of: '2026-09-30', mode: 'WITH_JOURNAL', journal_number: 'JV-000100', asset_count: 2, total_cost: '250000000', total_accum: '110000000' }] } }
  if (path.endsWith('/assets/{id}')) return { data: detail }
  if (path.endsWith('/assets')) return { data: { data: assets, total: 4, summary: { count: 4, cost: '6386000000', accum_depr: '163000000', carrying_value: '6223000000' } } }
  if (path.endsWith('/departments')) return { data: { data: [{ id: 6, code: 'RM', name: 'Rooms', level: 1, is_active: true }] } }
  if (path.endsWith('/banquet/rooms')) return { data: { data: [{ id: 3, code: 'BALLROOM', name: 'Ballroom' }] } }
  if (path.endsWith('/rooms')) return { data: { data: [{ id: 4, room_number: '201' }] } }
  if (path.endsWith('/inventory/warehouses')) return { data: { data: [{ id: 8, code: 'MAIN', name: 'Main store' }] } }
  return { data: { data: [] } }
}

const blank = { template: '<div />' }
const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/assets/:id', component: blank }, { path: '/:p(.*)*', component: blank }] })
const mounted: VueWrapper[] = []
const ALL = ['asset.read']
const SETUP = ['asset.read', 'asset.manage', 'asset.setup']

async function setup(component: unknown, permissions = SETUP, route = '/assets', get?: (path: string) => unknown) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'm@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  usePropertyStore().current = { id: 7, currency_decimals: 0 } as never
  resetDepartments()
  GET = vi.fn(async (path: string) => (get ? get(path) : ok(path)))
  POST = vi.fn(async (path: string) => ({ data: path.endsWith('/cancel') ? detail : dry }))
  PUT = vi.fn().mockResolvedValue({ data: settings })
  PATCH = vi.fn().mockResolvedValue({ data: detail })
  openPdf.mockReset()
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

function reset(): void {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
}
beforeEach(() => setLocale('en'))
afterEach(reset)

describe('the register', () => {
  it('lists the assets with the sums, and shows land, construction and a fully depreciated asset without a life left', async () => {
    await setup(AssetRegisterView)
    expect(body('[data-testid=asset-tiles]').text()).toContain('6,386,000,000')
    expect(body('[data-testid=asset-tiles]').text()).toContain('4 assets')
    expect(body('[data-testid="asset-FA-000012"]').text()).toContain('Deluxe chairs')
    expect(body('[data-testid="asset-FA-000012"]').text()).toContain('23 mo')
    expect(body('[data-testid="asset-FA-000001"]').text()).toContain('not depreciated')
    expect(body('[data-testid="asset-FA-000005"]').text()).toContain('not in use yet')
    expect(body('[data-testid="asset-FA-000090"]').text()).toContain('fully depreciated')
    expect(body('[data-testid="asset-FA-000090"]').text()).toContain('opening balance')
  })

  it('asks the server for the active assets, then for a category when a chip is chosen', async () => {
    await setup(AssetRegisterView)
    const first = GET.mock.calls.find((c) => c[0] === '/api/v1/properties/{propertyId}/assets')!
    expect(first[1].params.query).toMatchObject({ status: 'ACTIVE', offset: 0 })
    await click('[data-testid=chip-FFE]')
    const last = GET.mock.calls.filter((c) => c[0] === '/api/v1/properties/{propertyId}/assets').at(-1)!
    expect(last[1].params.query).toMatchObject({ category_id: 2, status: 'ACTIVE' })
  })

  it('sends the chosen assets to the labels page', async () => {
    await setup(AssetRegisterView)
    await click('[data-testid="select-FA-000012"]')
    await click('[data-testid="select-FA-000090"]')
    expect(body('[data-testid=print-labels]').text()).toContain('(2)')
    await click('[data-testid=print-labels]')
    expect(router.currentRoute.value.path).toBe('/assets/labels')
    expect(router.currentRoute.value.query.ids).toBe('2,3')
  })

  it('offers the opening balances only to who sets the assets up, and nothing to who holds no asset permission', async () => {
    await setup(AssetRegisterView, ALL)
    expect(has('[data-testid=go-opening]')).toBe(false)
    expect(has('[data-testid=asset-table]')).toBe(true)
    reset()
    await setup(AssetRegisterView, SETUP)
    expect(has('[data-testid=go-opening]')).toBe(true)
    reset()
    await setup(AssetRegisterView, ['reservation.read'])
    expect(has('[data-testid=no-access]')).toBe(true)
    expect(GET.mock.calls.filter((c) => String(c[0]).includes('/assets')).length).toBe(0)
  })

  it('counts the assets exactly in the footer, and shows the date in service as a date of the page', async () => {
    await setup(AssetRegisterView)
    expect(body('[data-testid=table-count]').text()).toBe('4 assets')
    expect(body('[data-testid="asset-FA-000012"]').text()).toMatch(/\d{1,2} \w{3} 20\d\d/)
    expect(body('[data-testid="asset-FA-000012"]').text()).not.toMatch(/20\d\d-\d\d-\d\d/)
  })

  it('speaks Indonesian with its own separators', async () => {
    setLocale('id')
    await setup(AssetRegisterView)
    expect(body('[data-testid=table-count]').text()).toBe('4 aset')
    expect(document.body.textContent).toContain('Register aset tetap')
    expect(body('[data-testid=asset-tiles]').text()).toContain('6.386.000.000')
    expect(body('[data-testid="asset-FA-000090"]').text()).toContain('habis disusutkan')
  })
})

describe('an asset', () => {
  const at = (permissions = SETUP) => setup(AssetView, permissions, '/assets/2')

  it('shows the data, the values, the fiscal book with the computed marker, and the history', async () => {
    await at()
    expect(document.body.textContent).toContain('Deluxe chairs')
    expect(body('[data-testid=value-cost]').text()).toBe('36,000,000')
    expect(body('[data-testid=value-accum]').text()).toBe('13,000,000')
    expect(body('[data-testid=value-carrying]').text()).toBe('23,000,000')
    expect(body('[data-testid=asset-fiscal]').text()).toContain('36,000,000')
    expect(has('[data-testid=fiscal-estimated]')).toBe(true)
    expect(document.body.textContent).toContain('Mar 2026')
    await click('[data-testid=tab-history]')
    expect(body('[data-testid=ledger-5]').text()).toContain('Brought in (opening balance)')
  })

  it('changes the details with the version it read, and refuses nothing silently', async () => {
    await at()
    await click('[data-testid=asset-edit]')
    await type('[data-testid=edit-dialog] input[name=name]', 'Deluxe chairs (set)')
    await click('[data-testid=edit-save]')
    const call = PATCH.mock.calls.find((c) => c[0] === '/api/v1/properties/{propertyId}/assets/{id}')!
    expect(call[1].body).toMatchObject({ name: 'Deluxe chairs (set)', version: 3 })
    expect(call[1].params.path).toEqual({ propertyId: 7, id: 2 })
  })

  it('cancels with a reason', async () => {
    await at()
    await click('[data-testid=asset-cancel]')
    expect((document.body.querySelector('[data-testid=cancel-confirm]') as HTMLButtonElement).disabled).toBe(true)
    await type('[data-testid=cancel-dialog] input[name=reason]', 'bought twice')
    await click('[data-testid=cancel-confirm]')
    const call = POST.mock.calls.find((c) => c[0] === '/api/v1/properties/{propertyId}/assets/{id}/cancel')!
    expect(call[1].body).toEqual({ reason: 'bought twice' })
  })

  it('shows no change or cancel to who only reads', async () => {
    await at(ALL)
    expect(has('[data-testid=asset-edit]')).toBe(false)
    expect(has('[data-testid=asset-cancel]')).toBe(false)
    expect(has('[data-testid=asset-label]')).toBe(true)
  })
})

describe('the opening balances', () => {
  async function fill(): Promise<void> {
    await type('input[name=as_of]', '2026-09-30')
    await type('[data-testid=csv-text]', 'name,category,in_service_date,cost\nWooden wardrobe,FFE,2020-01-10,36000000')
  }

  it('checks the file first: the rows, the warning about the computed fiscal accumulation, the totals and the journal against 3900', async () => {
    await setup(AssetOpeningView)
    await fill()
    await click('[data-testid=dry-run]')
    const call = POST.mock.calls.find((c) => c[0] === '/api/v1/properties/{propertyId}/assets/opening/import')!
    expect(call[1].params.query).toEqual({ dry_run: true })
    expect(call[1].body).toMatchObject({ as_of: '2026-09-30', post_journal: true, dry_run: true })
    expect(body('[data-testid=result-row-1]').text()).toContain('Wooden wardrobe')
    expect(body('[data-testid=result-row-1]').text()).toContain('30,375,000')
    expect(body('[data-testid=result-row-1]').text()).toContain('fiscal accumulation was computed')
    expect(body('[data-testid=result-journal]').text()).toContain('3900')
    expect(body('[data-testid=result-journal]').text()).toContain('5,625,000')
  })

  it('words the findings in the language of the page from their code, field and figures', async () => {
    setLocale('id')
    await setup(AssetOpeningView)
    POST = vi.fn().mockResolvedValue({ data: { ...dry, valid: false, mode: 'REGISTER_ONLY', journal_preview: [],
      errors: [{ row_no: 0, field: 'as_of', code: 'AS_OF_MISMATCH', message: 'the register starts in 2026-10', params: { month: '2026-10' } }, { row_no: 0, code: 'OPENING_GL_MISMATCH', message: 'the sums differ' }],
      rows: [{ ...dry.rows[0], errors: [{ row_no: 1, field: 'name', code: 'INVALID_VALUE', message: '1 to 150 characters', params: { max: '150' } }], warnings: [] }] } })
    await fill()
    await click('input[name=mode][value=REGISTER_ONLY]')
    await click('[data-testid=dry-run]')
    const errors = body('[data-testid=result-errors]').text()
    expect(errors).toContain('Register dimulai pada Okt 2026')
    expect(errors).toContain('Jumlah dalam berkas tidak sama dengan saldo akun buku besar')
    expect(errors).not.toContain('the register starts')
    expect(body('[data-testid=result-row-1]').text()).toContain('Nama terdiri dari 1 sampai 150 karakter.')
  })

  it('imports only after a dry run that is valid, with a key, and tells where the journal goes', async () => {
    await setup(AssetOpeningView)
    expect(has('[data-testid=confirm-import]')).toBe(false)
    await fill()
    await click('[data-testid=dry-run]')
    POST.mockResolvedValueOnce({ data: { ...dry, dry_run: false, number: 'FAI-000002', rows: dry.rows } })
    await click('[data-testid=confirm-import]')
    const real = POST.mock.calls.filter((c) => c[0] === '/api/v1/properties/{propertyId}/assets/opening/import').at(-1)!
    expect(real[1].params.query).toEqual({ dry_run: false })
    expect(real[1].params.header['Idempotency-Key']).toMatch(/^[0-9a-f-]{36}$/)
    expect(real[1].body.dry_run).toBe(false)
    expect(router.currentRoute.value.path).toBe('/assets')
  })

  it('does not offer the import when the rows have errors', async () => {
    await setup(AssetOpeningView)
    POST = vi.fn().mockResolvedValue({ data: { ...dry, valid: false, errors: [{ row_no: 1, code: 'X', message: 'row 1 is wrong' }], rows: [{ ...dry.rows[0], warnings: [], errors: [{ row_no: 1, code: 'COST_INVALID', message: 'cost is not a number' }] }] } })
    await fill()
    await click('[data-testid=dry-run]')
    expect(has('[data-testid=result-invalid]')).toBe(true)
    expect((document.body.querySelector('[data-testid=confirm-import]') as HTMLButtonElement).disabled).toBe(true)
    expect(body('[data-testid=result-row-1]').text()).toContain('cost is not a number')
  })

  it('in the mode "register only" asks for no journal and shows the comparison with the ledger', async () => {
    await setup(AssetOpeningView)
    POST = vi.fn().mockResolvedValue({ data: { ...dry, mode: 'REGISTER_ONLY', journal_preview: [], gl_check: [{ category_code: 'FFE', cost_account_code: '1530', register_cost: '36000000', ledger_cost: '30000000', cost_difference: '6000000', register_accum: '0', ledger_accum: '0', accum_difference: '0' }] } })
    await fill()
    await click('input[name=mode][value=REGISTER_ONLY]')
    await click('[data-testid=dry-run]')
    expect(POST.mock.calls[0]![1].body.post_journal).toBe(false)
    expect(body('[data-testid=result-glcheck]').text()).toContain('6,000,000')
    expect(has('[data-testid=result-journal]')).toBe(false)
  })

  it('adds one old asset through the same checks, with the fiscal fields', async () => {
    await setup(AssetOpeningView)
    await click('[data-testid=toggle-single]')
    await type('input[name=as_of]', '2026-09-30')
    await type('input[name=name]', 'Pickup L300')
    await choose('select[name=category]', 'FFE')
    await type('input[name=cost]', '150000000')
    await type('input[name=in_service_date]', '2017-03-01')
    await type('input[name=fiscal_accumulated_depreciation]', '150000000')
    await click('[data-testid=dry-run]')
    const call = POST.mock.calls.find((c) => c[0] === '/api/v1/properties/{propertyId}/assets/opening/single')!
    expect(call[1].body.row).toMatchObject({ name: 'Pickup L300', category: 'FFE', cost: '150000000', fiscal_accumulated_depreciation: '150000000' })
    expect(call[1].body.dry_run).toBe(true)
  })

  it('lists the imports made, and gives the page to who sets up only', async () => {
    await setup(AssetOpeningView)
    expect(body('[data-testid="import-FAI-000001"]').text()).toContain('JV-000100')
    reset()
    await setup(AssetOpeningView, ALL)
    expect(has('[data-testid=no-setup]')).toBe(true)
    expect(has('[data-testid=opening-form]')).toBe(false)
  })
})

describe('categories and settings', () => {
  it('lists the categories with their accounts, and the check of the register against the ledger', async () => {
    await setup(AssetSettingsView)
    expect(body('[data-testid=category-FFE]').text()).toContain('1592')
    expect(body('[data-testid=category-FFE]').text()).toContain('Straight line · 96 mo')
    expect(body('[data-testid=category-LAND]').text()).toContain('not depreciated')
    expect(body('[data-testid=check-issues]').text()).toContain('one asset has no location')
    expect(body('[data-testid=check-reconciliation]').text()).toContain('12,000,000')
  })

  it('saves the settings with the version', async () => {
    await setup(AssetSettingsView)
    await type('input[name=capitalization_threshold]', '3000000')
    await click('input[name=oe_policy][value=BATCH]')
    await click('[data-testid=settings-save]')
    const call = PUT.mock.calls.find((c) => c[0] === '/api/v1/properties/{propertyId}/assets/settings')!
    expect(call[1].body).toMatchObject({ capitalization_threshold: '3000000', oe_policy: 'BATCH', start_rule: 'IN_SERVICE_MONTH', version: 4 })
  })

  it('adds a category with its accounts, and leaves out the accounts of a kind that is not depreciated', async () => {
    await setup(AssetSettingsView)
    await click('[data-testid=category-new]')
    await type('[data-testid=category-dialog] input[name=code]', 'FURN')
    await type('[data-testid=category-dialog] input[name=name]', 'Furniture')
    await choose('[data-testid=category-dialog] select[name=cost_account_id]', 11)
    await click('[data-testid=category-save]')
    const call = POST.mock.calls.find((c) => c[0] === '/api/v1/properties/{propertyId}/assets/categories')!
    expect(call[1].body).toMatchObject({ code: 'FURN', name: 'Furniture', cost_account_id: 11, kind: 'FFE' })
    reset()
    await setup(AssetSettingsView)
    await click('[data-testid=category-edit-LAND]')
    expect(has('[data-testid=category-dialog] select[name=accumulated_account_id]')).toBe(false)
  })

  it('makes the location of a meeting room, and a free area', async () => {
    await setup(AssetSettingsView)
    await choose('[data-testid=ensure-form] select[name=kind]', 'BANQUET_ROOM')
    await choose('[data-testid=ensure-form] select[name=ref_id]', 3)
    await click('[data-testid=ensure-submit]')
    expect(POST.mock.calls.find((c) => c[0] === '/api/v1/properties/{propertyId}/assets/locations/ensure')![1].body).toEqual({ kind: 'BANQUET_ROOM', ref_id: 3 })
    await click('[data-testid=location-new]')
    await type('[data-testid=location-dialog] input[name=code]', 'POOL')
    await type('[data-testid=location-dialog] input[name=name]', 'Pool deck')
    await click('[data-testid=location-save]')
    expect(POST.mock.calls.find((c) => c[0] === '/api/v1/properties/{propertyId}/assets/locations')![1].body).toEqual({ code: 'POOL', name: 'Pool deck' })
  })

  it('changes a fiscal rate', async () => {
    await setup(AssetSettingsView)
    await type('[data-testid=fiscal-G1] input:nth-of-type(1)', '5')
    await click('[data-testid=fiscal-save-G1]')
    const call = PUT.mock.calls.find((c) => c[0] === '/api/v1/properties/{propertyId}/assets/fiscal-groups/{code}')!
    expect(call[1].params.path).toEqual({ propertyId: 7, code: 'G1' })
    expect(call[1].body).toMatchObject({ life_years: 5, sl_rate: '25.000', db_rate: '50.000' })
  })

  it('shows who only reads the same page without the buttons', async () => {
    await setup(AssetSettingsView, ALL)
    for (const id of ['category-new', 'location-new', 'settings-save', 'ensure-form']) expect(has(`[data-testid=${id}]`), id).toBe(false)
    expect(has('[data-testid=categories-table]')).toBe(true)
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/assets/accounts'))).toBe(false)
  })
})

describe('the labels', () => {
  it('prints the selected assets in the chosen format', async () => {
    await setup(AssetLabelsView, ALL, '/assets/labels?ids=2,3')
    await click('input[name=layout][value=ROLL_50x30]')
    await click('[data-testid=labels-print]')
    expect(openPdf).toHaveBeenCalledWith('/api/v1/properties/7/assets/labels.pdf?layout=ROLL_50x30&ids=2%2C3')
  })

  it('prints the labels of the places for the stock-take, and a location only once one is chosen', async () => {
    await setup(AssetLabelsView, ALL, '/assets/labels')
    await choose('[data-testid=labels-what]', 'places')
    await click('[data-testid=labels-print]')
    expect(openPdf).toHaveBeenCalledWith('/api/v1/properties/7/assets/labels.pdf?layout=A4_24&kind=place')
    await choose('[data-testid=labels-what]', 'location')
    expect((document.body.querySelector('[data-testid=labels-print]') as HTMLButtonElement).disabled).toBe(true)
    await choose('[data-testid=labels-location]', 2)
    expect((document.body.querySelector('[data-testid=labels-print]') as HTMLButtonElement).disabled).toBe(false)
  })
})

describe('the texts', () => {
  const keys = (o: unknown, prefix = ''): string[] => Object.entries(o as Record<string, unknown>).flatMap(([k, v]) => (v && typeof v === 'object' ? keys(v, `${prefix}${k}.`) : [`${prefix}${k}`]))

  it('are the same keys in English and in Indonesian', async () => {
    const { id } = await import('@/i18n/locales/id')
    const { en } = await import('@/i18n/locales/en')
    expect(keys(id.assets).sort()).toEqual(keys(en.assets).sort())
    expect(keys(id.assets).length).toBeGreaterThan(150)
    expect(i18n.global.te('assets.register.title')).toBe(true)
  })
})
