import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import AssetAuditView from './AssetAuditView.vue'
import AssetAuditsView from './AssetAuditsView.vue'
import AssetFiscalView from './AssetFiscalView.vue'
import AssetRecapView from './AssetRecapView.vue'
import AssetReportView from './AssetReportView.vue'

/** PR 4 of the fixed assets: the recap with the causes of its differences, the fiscal correction with its warning, and the stock-take on the phone. */
let GET = vi.fn()
let POST = vi.fn()
let PUT = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PUT: (...a: unknown[]) => PUT(...a) } }))

const READ = ['asset.read']
const MANAGE = ['asset.read', 'asset.manage']

async function setup(component: unknown, path: string, route: string, permissions = READ) {
  const pinia = createPinia()
  setActivePinia(pinia)
  setLocale('en')
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-11-06' } as never
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: route, component: { template: '<div />' } }, { path: '/:p(.*)*', component: { template: '<div />' } }] })
  void router.push(path)
  await router.isReady()
  const w = mount(component as never, { global: { plugins: [pinia, router] }, attachTo: document.body })
  await flushPromises()
  return w
}

const recapRow = (over: Record<string, unknown>) => ({
  category_id: 2, category_code: 'FFE', category_name: 'FF&E', cost_account_code: '1530', accumulated_account_code: '1592',
  cost_opening: '30000000', cost_additions: '6000000', cost_reclass: '0', cost_disposals: '0', cost_closing: '36000000',
  accum_opening: '10000000', accum_additions: '0', accum_depreciation: '3375000', accum_reclass: '0', accum_disposals: '0', accum_closing: '13375000', carrying_closing: '22625000',
  ledger_cost_opening: '30000000', ledger_cost_closing: '40000000', ledger_accum_opening: '10000000', ledger_accum_closing: '13375000',
  cost_difference: '4000000', accum_difference: '0', cost_causes: [{ code: 'WAITING_LIST', amount: '4000000', lines: 1 }], accum_causes: [], ...over,
})

describe('the recap by category', () => {
  beforeEach(() => {
    GET = vi.fn()
  })

  it('shows the difference with the ledger and its cause', async () => {
    const row = recapRow({})
    GET = vi.fn(async () => ({ data: { date_from: '2026-10-01', date_to: '2026-10-31', rows: [row], total: row, account_1590: '0' } }))
    const w = await setup(AssetRecapView, '/assets/reports/recap', '/assets/reports/recap')
    expect(w.get('[data-testid=recap-row-FFE]').text()).toContain('36,000,000')
    expect(w.get('[data-testid=recap-diff-FFE]').text()).toContain('4,000,000')
    expect(w.get('[data-testid=recap-cause-FFE-cost]').text()).toContain('On the waiting list')
    expect(w.find('[data-testid=cause-OTHER]').exists()).toBe(false)
  })

  it('hides the change-of-category columns unless a category changed, and matches the ledger in its own table', async () => {
    const row = recapRow({ accum_difference: '250000' })
    GET = vi.fn(async () => ({ data: { date_from: '2026-10-01', date_to: '2026-10-31', rows: [row], total: row, account_1590: '0' } }))
    const w = await setup(AssetRecapView, '/assets/reports/recap', '/assets/reports/recap')
    expect(w.find('[data-testid=recap-reclass-head]').exists()).toBe(false)
    expect(w.get('[data-testid=recap-table]').text()).not.toContain('Change of category')
    expect(w.get('[data-testid=recap-table]').text()).not.toContain('40,000,000')
    const ledger = w.get('[data-testid=recap-ledger-row-FFE]').text()
    expect(ledger).toContain('36,000,000')
    expect(ledger).toContain('40,000,000')
    expect(w.get('[data-testid=recap-diff-FFE]').text()).toContain('4,000,000')
    expect(w.get('[data-testid=recap-accum-diff-FFE]').text()).toContain('250,000')
    expect(w.get('[data-testid=recap-ledger-total]').text()).toContain('40,000,000')
  })

  it('shows the change-of-category columns when a category changed in the period', async () => {
    const row = recapRow({ cost_reclass: '5000000', accum_reclass: '1200000' })
    GET = vi.fn(async () => ({ data: { date_from: '2026-10-01', date_to: '2026-10-31', rows: [row], total: row, account_1590: '0' } }))
    const w = await setup(AssetRecapView, '/assets/reports/recap', '/assets/reports/recap')
    expect(w.find('[data-testid=recap-reclass-head]').exists()).toBe(true)
    expect(w.get('[data-testid=recap-row-FFE]').text()).toContain('5,000,000')
    expect(w.get('[data-testid=recap-row-FFE]').text()).toContain('1,200,000')
  })

  it('says so when the register and the ledger agree', async () => {
    const row = recapRow({ ledger_cost_closing: '36000000', cost_difference: '0', cost_causes: [] })
    GET = vi.fn(async () => ({ data: { date_from: '2026-10-01', date_to: '2026-10-31', rows: [row], total: row, account_1590: '0' } }))
    const w = await setup(AssetRecapView, '/assets/reports/recap', '/assets/reports/recap')
    expect(w.find('[data-testid=recap-nodiff]').exists()).toBe(true)
  })

  it('is refused for a person without any asset permission', async () => {
    const w = await setup(AssetRecapView, '/assets/reports/recap', '/assets/reports/recap', ['room.read'])
    expect(GET).not.toHaveBeenCalled()
    expect(w.text()).toContain('do not have access')
  })
})

describe('the fiscal correction', () => {
  it('always carries the warning that the rates are to be confirmed by the tax consultant', async () => {
    const totals = { commercial: '3375000', fiscal: '4500000', depreciation_net: '-1125000', positive_correction: '0', negative_correction: '1125000', disposal_correction: '0', net_correction: '-1125000', partial_year_assets: 0, estimated_fiscal_assets: 0 }
    const rows = [{ asset_id: 2, number: 'FA-000012', name: 'Deluxe chairs', category_code: 'FFE', fiscal_group: 'G2', fiscal_method: 'SL', commercial: '3375000', fiscal: '4500000', difference: '-1125000', partial_year: false, fiscal_estimated: false, from_opening: false, disposed_in_year: false }]
    GET = vi.fn(async () => ({ data: { tax_year: 2026, year_start: '2026-01-01', year_end: '2026-12-31', rows, disposals: [], totals, rates: [{ code: 'G2', life_years: 8, sl_rate: '12.5%', db_rate: '25%' }] } }))
    const w = await setup(AssetFiscalView, '/assets/reports/fiscal', '/assets/reports/fiscal')
    expect(w.get('[data-testid=fiscal-banner]').text()).toContain('tax consultant')
    expect(w.get('[data-testid=fiscal-row-FA-000012]').text()).toContain('−1,125,000')
    expect(w.findAll('[data-testid=rate-confirm]').length).toBe(1)
    expect(w.get('[data-testid=fiscal-net]').text()).toContain('−1,125,000')
  })

  it('names the group with its percent, shows the months compared and the banner of a register that started mid-year', async () => {
    const totals = { commercial: '10000000', fiscal: '10000000', depreciation_net: '0', positive_correction: '0', negative_correction: '0', disposal_correction: '0', net_correction: '0', partial_year_assets: 1, estimated_fiscal_assets: 0 }
    const rows = [
      { asset_id: 2, number: 'FA-000020', name: 'Building', category_code: 'BLD', fiscal_group: 'BLD_PERMANENT', fiscal_rate_percent: '5.000', fiscal_method: 'SL', commercial: '10000000', fiscal: '10000000', difference: '0', partial_year: true, fiscal_estimated: false, from_opening: true, disposed_in_year: false, from_month: '2026-10-01', to_month: '2026-10-01', months: 1, until_last_run: true },
      { asset_id: 3, number: 'FA-000021', name: 'Chairs', category_code: 'FFE', fiscal_group: 'G2', fiscal_rate_percent: '12.500', fiscal_method: 'SL', commercial: '0', fiscal: '0', difference: '0', partial_year: false, fiscal_estimated: false, from_opening: false, disposed_in_year: false, from_month: '2026-10-01', to_month: '2026-10-01', months: 1, until_last_run: true },
    ]
    GET = vi.fn(async () => ({ data: { tax_year: 2026, year_start: '2026-01-01', year_end: '2026-12-31', register_from: '2026-10-01', year_starts_before_register: true, last_run_month: '2026-10-01', year_open: true, rows, disposals: [], totals, rates: [] } }))
    setLocale('id')
    const w = await setup(AssetFiscalView, '/assets/reports/fiscal', '/assets/reports/fiscal')
    setLocale('id')
    await flushPromises()
    const text = w.text()
    expect(text).toContain('Bangunan permanen · 5%')
    expect(text).toContain('Kelompok 2 · 12,5%')
    expect(text).not.toContain('BLD_PERMANENT')
    expect(w.get('[data-testid=fiscal-register-banner]').text()).toContain('hanya memuat')
    expect(w.get('[data-testid=fiscal-range-FA-000020]').text()).toMatch(/Okt/)
    expect(w.find('[data-testid=fiscal-open]').exists()).toBe(true)
    setLocale('en')
  })
})

const auditBase = {
  id: 5, number: 'FAO-000005', location_id: 1, location_code: 'LOBBY', location_name: 'Lobby', status: 'IN_PROGRESS', started_at: '2026-11-06T01:00:00Z',
  counts: { expected: 2, found: 0, moved: 0, unexpected: 0, damaged: 0, missing: 0 }, lines: [] as unknown[],
  unscanned: [{ asset_id: 2, number: 'FA-000012', name: 'Deluxe chairs', disposal_pending: false }, { asset_id: 3, number: 'FA-000013', name: 'Reception desk', disposal_pending: false }],
}

describe('the stock-take on the phone', () => {
  let detail: Record<string, unknown>
  beforeEach(() => {
    detail = JSON.parse(JSON.stringify(auditBase))
    GET = vi.fn(async () => ({ data: detail }))
    PUT = vi.fn(async () => {
      detail = { ...detail, counts: { ...auditBase.counts, found: 1 }, lines: [{ asset_id: 2, asset_number: 'FA-000012', asset_name: 'Deluxe chairs', result: 'FOUND', scanned_at: '2026-11-06T02:00:00Z' }], unscanned: [auditBase.unscanned[1]] }
      return { data: { line: (detail.lines as unknown[])[0] } }
    })
    POST = vi.fn(async () => ({ data: { missing: 1, proposals: 1 } }))
  })

  it('scans a typed code and shows the result and the progress', async () => {
    const w = await setup(AssetAuditView, '/assets/audits/5', '/assets/audits/:id', MANAGE)
    expect(w.get('[data-testid=audit-progress]').text()).toBe('0 of 2 found')
    await w.get('[data-testid=audit-code]').setValue('FA:ABCDEFGHJKLM')
    await w.get('[data-testid=audit-manual-form]').trigger('submit')
    await flushPromises()
    expect(PUT.mock.calls[0]![1].body.code).toBe('FA:ABCDEFGHJKLM')
    expect(w.get('[data-testid=audit-last]').text()).toContain('Found')
    expect(w.get('[data-testid=audit-progress]').text()).toBe('1 of 2 found')
    expect(w.find('[data-testid=audit-unscanned-FA-000012]').exists()).toBe(false)
    expect((w.get('[data-testid=audit-code]').element as HTMLInputElement).value).toBe('')
  })

  it('needs the place first for a stock-take of the whole property', async () => {
    detail.location_id = null
    detail.location_name = null
    const w = await setup(AssetAuditView, '/assets/audits/5', '/assets/audits/:id', MANAGE)
    await w.get('[data-testid=audit-code]').setValue('FL:ABCDEFGHJKLM')
    await w.get('[data-testid=audit-manual-form]').trigger('submit')
    await flushPromises()
    expect(PUT).not.toHaveBeenCalled()
    expect(w.get('[data-testid=audit-place]').text()).toContain('FL:ABCDEFGHJKLM')
    await w.get('[data-testid=audit-code]').setValue('FA:ABCDEFGHJKLM')
    await w.get('[data-testid=audit-manual-form]').trigger('submit')
    await flushPromises()
    expect(PUT.mock.calls[0]![1].body).toMatchObject({ code: 'FA:ABCDEFGHJKLM', location_code: 'FL:ABCDEFGHJKLM' })
  })

  it('marks an unscanned asset as damaged', async () => {
    const w = await setup(AssetAuditView, '/assets/audits/5', '/assets/audits/:id', MANAGE)
    await w.get('[data-testid=audit-damaged-FA-000013]').trigger('click')
    await flushPromises()
    const input = document.body.querySelector('[data-testid=audit-damaged-note]') as HTMLInputElement
    input.value = 'cracked top'
    input.dispatchEvent(new Event('input'))
    ;(document.body.querySelector('[data-testid=audit-damaged-confirm]') as HTMLElement).click()
    await flushPromises()
    expect(PUT.mock.calls[0]![1].body).toMatchObject({ asset_id: 3, damaged: true, note: 'cracked top' })
  })

  it('closes into proposals and posts nothing itself', async () => {
    const w = await setup(AssetAuditView, '/assets/audits/5', '/assets/audits/:id', MANAGE)
    await w.get('[data-testid=audit-close]').trigger('click')
    await flushPromises()
    ;(document.body.querySelector('[data-testid=audit-close-confirm]') as HTMLElement).click()
    await flushPromises()
    expect(POST.mock.calls[0]![0]).toContain('/audits/{id}/close')
    detail.status = 'CLOSED'
    expect(w.get('[data-testid=audit-closed]').text()).toContain('1 missing, 1 proposals made.')
  })

  it('tells that the camera cannot be used and keeps the typed code', async () => {
    const w = await setup(AssetAuditView, '/assets/audits/5', '/assets/audits/:id', MANAGE)
    await w.get('[data-testid=audit-camera]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=qr-problem]').text()).toContain('Type the code')
    expect(w.find('[data-testid=audit-code]').exists()).toBe(true)
  })

  it('does not offer to scan to a person who can only read', async () => {
    const w = await setup(AssetAuditView, '/assets/audits/5', '/assets/audits/:id', READ)
    expect(w.find('[data-testid=audit-code]').exists()).toBe(false)
    expect(w.find('[data-testid=audit-close]').exists()).toBe(false)
  })
})

describe('the schedule report', () => {
  it('keeps the button off until a category is chosen and asks nothing before', async () => {
    GET = vi.fn(async (path: string) => ({ data: path.endsWith('/assets/categories') ? { data: [{ id: 2, code: 'FFE', name: 'Furniture' }] } : { data: [] } }))
    const w = await setup(AssetReportView, '/assets/reports/schedule', '/assets/reports/:kind')
    expect(w.get('[data-testid=report-run]').attributes('disabled')).toBeDefined()
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/reports/schedule'))).toBe(false)
    await w.get('[data-testid=f-category]').setValue(2)
    expect(w.get('[data-testid=report-run]').attributes('disabled')).toBeUndefined()
  })
})

describe('the list of stock-takes and proposals', () => {
  it('lets whoever manages the assets decide a move but not a write-off', async () => {
    const proposal = (id: number, kind: string) => ({ id, status: 'PENDING', kind, audit_number: 'FAO-000005', asset_id: id, asset_number: `FA-00001${id}`, asset_name: 'Chair', line_result: kind === 'MOVE' ? 'MOVED' : 'MISSING', to_location_name: 'Lobby', carrying_value: '1000000' })
    GET = vi.fn(async (path: string) => {
      if (path.endsWith('/assets/proposals')) return { data: { data: [proposal(1, 'MOVE'), proposal(2, 'WRITE_OFF')] } }
      return { data: { data: [] } }
    })
    const w = await setup(AssetAuditsView, '/assets/audits', '/assets/audits', MANAGE)
    await w.get('[data-testid=tab-proposals]').trigger('click')
    expect(w.find('[data-testid=proposal-apply-1]').exists()).toBe(true)
    expect(w.find('[data-testid=proposal-apply-2]').exists()).toBe(false)
    expect(w.get('[data-testid=proposal-2]').text()).toContain('Needs another permission')
    expect(w.get('[data-testid=proposal-apply-1]').text()).toBe('Apply')
    expect(w.get('[data-testid=proposal-1]').text()).not.toBe('Apply')
  })
})
