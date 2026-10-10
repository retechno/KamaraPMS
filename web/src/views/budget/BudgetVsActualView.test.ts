import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { resetDepartments } from '@/composables/useDepartments'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import BudgetVsActualView from './BudgetVsActualView.vue'

let GET = vi.fn()
const downloadCsv = vi.fn()
const openPdf = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))
vi.mock('../accounting/reportApi', () => ({ downloadCsv: (...a: unknown[]) => downloadCsv(...a) }))
vi.mock('@/utils/documents', () => ({ openPdf: (...a: unknown[]) => openPdf(...a) }))

const cell = (actual: string, budget: string, variance: string, percent: string | null, favourable: boolean | null) => ({ actual, budget, variance, variance_percent: percent, favourable })
const zero = cell('0', '0', '0', null, null)
const report = {
  year_start: '2026-01-01', year_end: '2026-12-31', year_label: 'FY2026', from: '2026-09-01', to: '2026-09-30',
  budget: { id: 5, name: 'Plan 2026', version: 2, status: 'ACTIVE' },
  lines: [
    { key: 'H_REVENUE', title: 'Operating revenue', kind: 'HEADING', period: zero, ytd: zero, accounts: [] },
    {
      key: 'REV_ROOMS', title: 'Rooms', kind: 'GROUP', period: cell('500000', '400000', '100000', '25', true), ytd: cell('500000', '500000', '0', '0', null),
      accounts: [
        { account_id: 72, code: '4110', name: 'Room revenue - transient', period: cell('300000', '200000', '100000', '50', true), ytd: cell('300000', '300000', '0', '0', null) },
        { account_id: 73, code: '4120', name: 'Room revenue - group', period: cell('200000', '200000', '0', '0', null), ytd: cell('200000', '200000', '0', '0', null) },
      ],
    },
    { key: 'TOTAL_REVENUE', title: 'Total revenue', kind: 'SUBTOTAL', period: cell('500000', '400000', '100000', '25', true), ytd: cell('500000', '500000', '0', '0', null), accounts: [] },
    {
      key: 'EXP_ROOMS', title: 'Rooms', kind: 'GROUP', period: cell('20000', '25000', '-5000', '-20', true), ytd: cell('20000', '25000', '-5000', '-20', true),
      accounts: [{ account_id: 108, code: '5110', name: 'Salaries', period: cell('20000', '25000', '-5000', '-20', true), ytd: cell('20000', '25000', '-5000', '-20', true) }],
    },
    {
      key: 'UND_AG', title: 'Administrative and general', kind: 'GROUP', period: cell('60000', '30000', '30000', '100', false), ytd: cell('60000', '30000', '30000', '100', false),
      accounts: [{ account_id: 133, code: '6110', name: 'Salaries', period: cell('60000', '30000', '30000', '100', false), ytd: cell('60000', '30000', '30000', '100', false) }],
    },
    { key: 'NET_INCOME', title: 'Net income', kind: 'TOTAL', period: cell('420000', '345000', '75000', '21.7', true), ytd: cell('420000', '445000', '-25000', '-5.6', false), accounts: [] },
  ],
}
const budgets = [
  { id: 5, year_start: '2026-01-01', year_label: 'FY2026', version: 2, name: 'Plan 2026', status: 'ACTIVE' },
  { id: 4, year_start: '2026-01-01', year_label: 'FY2026', version: 1, name: 'First', status: 'ARCHIVED' },
  { id: 9, year_start: '2027-01-01', year_label: 'FY2027', version: 1, name: 'Next', status: 'DRAFT' },
]

const metric = (key: string, unit: string, period: ReturnType<typeof cell>, ytd = period) => ({ key, unit, period, ytd })
const statistics = {
  year_start: '2026-01-01', year_end: '2026-12-31', year_label: 'FY2026', from: '2026-09-01', to: '2026-09-30', budget: report.budget, has_statistics: true, closed_days: 30,
  metrics: [
    metric('rooms_sold', 'NIGHTS', cell('210', '240', '-30', '-12.5', false)),
    metric('occupancy', 'PERCENT', cell('70', '80', '-10', '-12.5', false)),
    metric('adr', 'MONEY', cell('1000000', '900000', '100000', '11.1', true)),
  ],
  room_revenue_check: { period_money: '200', period_statistics: '210', ytd_money: '200', ytd_statistics: '210', agrees: false },
}

const cellOf = (a: string, b: string, v: string, fav: boolean | null) => cell(a, b, v, null, fav)
const pair = (a: string, b: string, v: string, fav: boolean | null) => ({ period: cellOf(a, b, v, fav), ytd: cellOf(a, b, v, fav) })
const deptNode = (over: Record<string, unknown>) => ({ id: 1, parent_id: null, code: 'ROOMS', name: 'Rooms', level: 1, is_active: true, revenue: pair('600', '500', '100', true), expense: pair('80', '100', '-20', true), profit: pair('520', '400', '120', true), children: [], ...over })
const byDepartment = {
  year_start: '2026-01-01', year_end: '2026-12-31', year_label: 'FY2026', from: '2026-09-01', to: '2026-09-30', budget: report.budget,
  departments: [
    deptNode({}),
    deptNode({ id: 2, code: 'FB', name: 'Food and beverage', revenue: pair('800', '750', '50', true), expense: pair('150', '100', '50', false), profit: pair('650', '650', '0', null), children: [deptNode({ id: 9, parent_id: 2, code: 'REST', name: 'Restaurant', level: 2, revenue: pair('600', '500', '100', true), expense: pair('150', '100', '50', false), profit: pair('450', '400', '50', true) })] }),
  ],
  unassigned: deptNode({ id: 0, code: '', name: '', revenue: pair('100', '0', '100', true), expense: pair('0', '0', '0', null), profit: pair('100', '0', '100', true) }),
  totals: deptNode({ id: 0, code: '', name: '', revenue: pair('1500', '1250', '250', true), expense: pair('230', '200', '30', false), profit: pair('1270', '1050', '220', true) }),
}

function mountView(permissions = ['budget.view'], stats: Record<string, unknown> = statistics, dept: Record<string, unknown> = byDepartment) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockImplementation((path: string) => Promise.resolve({ data: path.endsWith('/vs-actual') ? report : path.endsWith('/statistics-vs-actual') ? stats : path.endsWith('/department-vs-actual') ? dept : path.endsWith('/departments') ? { data: [{ id: 2, parent_id: null, code: 'FB', name: 'Food and beverage', sort_order: 20, is_active: true, level: 1, child_count: 1, in_use: true }] } : { data: budgets } }))
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(BudgetVsActualView, { global: { plugins: [pinia, router] } })
}

const reportCalls = () => GET.mock.calls.filter(([p]) => String(p).endsWith('/vs-actual'))

describe('BudgetVsActualView', () => {
  beforeEach(() => {
    resetDepartments()
    GET = vi.fn()
    downloadCsv.mockReset()
    openPdf.mockReset()
    setLocale('en')
  })

  it('shows the report like the income statement, for the period and the year to date', async () => {
    const w = mountView()
    await flushPromises()
    expect(reportCalls()[0]?.[1].params).toEqual({ path: { propertyId: 7 }, query: { year_start: undefined, from: undefined, to: undefined, budget_id: undefined } })
    expect(w.get('[data-testid=range]').text()).toContain('FY2026 · Plan 2026 (v2, Active)')
    expect(w.get('[data-testid=range]').text()).toContain('Sep 2026 – Sep 2026')
    expect(w.get('[data-testid=line-H_REVENUE]').text()).toBe('Operating revenue')
    const rooms = w.get('[data-testid=line-REV_ROOMS]').text()
    expect(rooms).toContain('500,000')
    expect(rooms).toContain('400,000')
    expect(rooms).toContain('100,000')
    expect(rooms).toContain('25%')
    expect(w.get('[data-testid=account-4110]').text()).toContain('Room revenue - transient')
    expect(w.get('[data-testid=account-4120]').text()).toContain('200,000')
    expect(w.get('[data-testid=line-TOTAL_REVENUE]').text()).toContain('Total revenue')
    expect(w.get('[data-testid=line-NET_INCOME]').text()).toContain('420,000')
  })

  it('tells good from bad in words, not only in colour', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=variance-TOTAL_REVENUE]').text()).toContain('fav.')
    expect(w.get('[data-testid=variance-TOTAL_REVENUE]').classes()).toContain('text-success-text')
    expect(w.get('[data-testid=account-5110]').text()).toContain('fav.') // an expense below the budget
    expect(w.get('[data-testid=account-6110]').text()).toContain('unfav.') // an expense above it
    expect(w.get('[data-testid=account-6110]').html()).toContain('text-destructive')
    expect(w.get('[data-testid=variance-NET_INCOME]').text()).toContain('fav.')
    expect(w.get('[data-testid=line-NET_INCOME]').text()).toContain('unfav.') // the year to date is below the plan
    expect(w.get('[data-testid=account-4120]').text()).not.toContain('fav.') // on budget: neither
  })

  it('asks for another year, months and version', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.findAll('select[name=year_start] option').map((o) => o.text())).toEqual(['Current fiscal year', 'FY2026', 'FY2027'])
    expect(w.findAll('select[name=budget_id] option').map((o) => o.text())).toHaveLength(4) // the active one and the three versions
    await w.get('select[name=year_start]').setValue('2026-01-01')
    expect(w.findAll('select[name=budget_id] option').map((o) => o.text())).toHaveLength(3) // the two versions of that year
    await w.get('input[name=from]').setValue('2026-08')
    await w.get('input[name=to]').setValue('2026-09')
    await w.get('select[name=budget_id]').setValue('4')
    await w.get('[data-testid=filters]').trigger('submit')
    await flushPromises()
    expect(reportCalls().at(-1)?.[1].params.query).toEqual({ year_start: '2026-01-01', from: '2026-08-01', to: '2026-09-01', budget_id: 4 })
  })

  it('keeps the filters in the export and the PDF', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('input[name=from]').setValue('2026-08')
    await w.get('[data-testid=export]').trigger('click')
    expect(downloadCsv).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/budgets/vs-actual', { path: { propertyId: 7 }, query: { year_start: undefined, from: '2026-08-01', to: undefined, budget_id: undefined } }, 'budget-vs-actual.csv')
    await w.get('[data-testid=pdf]').trigger('click')
    expect(openPdf).toHaveBeenCalledWith('/api/v1/properties/7/budgets/vs-actual.pdf?from=2026-08-01')
  })

  it('says what is missing when the year has no active budget', async () => {
    const w = mountView()
    GET.mockImplementation((path: string) => path.endsWith('/vs-actual')
      ? Promise.reject(new ApiError({ type: 't', title: 'Not found', status: 404, code: 'NO_ACTIVE_BUDGET', detail: 'no active budget' }))
      : Promise.resolve({ data: { data: budgets } }))
    await w.get('[data-testid=filters]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=report-error]').text()).toContain('NO_ACTIVE_BUDGET')
    expect(w.find('[data-testid=empty]').exists()).toBe(true)
    expect(w.find('[data-testid=pdf]').exists()).toBe(false)
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    const w = mountView()
    await flushPromises()
    expect(w.get('h1').text()).toBe('Anggaran terhadap realisasi')
    expect(w.get('[data-testid=range]').text()).toContain('Aktif')
    expect(w.get('[data-testid=account-6110]').text()).toContain('rugi')
    expect(w.get('[data-testid=line-REV_ROOMS]').text()).toContain('500.000')
  })

  it('needs the budget.view permission', async () => {
    const w = mountView([])
    await flushPromises()
    expect(w.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })

  it('sets the statistics against the closed days, with the check of the room revenue', async () => {
    const w = mountView()
    await flushPromises()
    expect(GET.mock.calls.some(([p]) => String(p).endsWith('/statistics-vs-actual'))).toBe(true)
    expect(w.get('[data-testid=statistics]').text()).toContain('30 closed days')
    expect(w.get('[data-testid=metric-rooms_sold]').text()).toContain('210')
    expect(w.get('[data-testid=metric-rooms_sold]').text()).toContain('unfav.')
    expect(w.get('[data-testid=metric-occupancy]').text()).toContain('70%')
    expect(w.get('[data-testid=metric-adr]').text()).toContain('1,000,000')
    expect(w.get('[data-testid=metric-adr]').text()).toContain('fav.')
    expect(w.get('[data-testid=check-differs]').text()).toContain('(200)')
    expect(w.get('[data-testid=check-differs]').text()).toContain('(210)')
    const agreeing = mountView(['budget.view'], { ...statistics, room_revenue_check: { ...statistics.room_revenue_check, agrees: true } })
    await flushPromises()
    expect(agreeing.find('[data-testid=check-agrees]').exists()).toBe(true)
  })

  it('says when the budget has no statistics', async () => {
    const w = mountView(['budget.view'], { ...statistics, has_statistics: false, metrics: [] })
    await flushPromises()
    expect(w.find('[data-testid=stats-none]').exists()).toBe(true)
    expect(w.find('[data-testid=stats-report]').exists()).toBe(false)
  })

  it('shows the departments under an account that has them', async () => {
    const withDepts = { ...report, lines: report.lines.map((l) => (l.key === 'REV_ROOMS' ? { ...l, accounts: l.accounts.map((a) => (a.code === '4110' ? { ...a, departments: [
      { department_id: null, department_code: '', department_name: '', period: cell('100000', '0', '100000', null, true), ytd: cell('100000', '0', '100000', null, true) },
      { department_id: 9, department_code: 'REST', department_name: 'Restaurant', period: cell('200000', '200000', '0', null, null), ytd: cell('200000', '300000', '-100000', null, false) },
    ] } : a)) } : l)) }
    const pinia = createPinia()
    setActivePinia(pinia)
    useAuthStore().me = { user: { id: 5, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: ['budget.view'] }] } as never
    usePropertyStore().currentId = 7
    GET = vi.fn(async (path: string) => (path.endsWith('/vs-actual') ? { data: withDepts } : path.endsWith('/budgets') ? { data: { data: budgets } } : path.endsWith('/departments') ? { data: { data: [] } } : { data: {} }))
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
    const w = mount(BudgetVsActualView, { global: { plugins: [pinia, router] } })
    await flushPromises()
    expect(w.get('[data-testid=account-4110-dept-none]').text()).toContain('No department')
    expect(w.get('[data-testid=account-4110-dept-none]').text()).toContain('100,000')
    expect(w.get('[data-testid=account-4110-dept-REST]').text()).toContain('REST · Restaurant')
    expect(w.get('[data-testid=account-4110-dept-REST]').text()).toContain('unfav.') // the year to date is below the plan
    expect(w.find('[data-testid=account-4120-dept-none]').exists()).toBe(false) // an account with no department has no sub-rows
  })

  it('sets the budget against the actual by department, with sub-departments, the unassigned and the totals', async () => {
    const w = mountView()
    await flushPromises()
    expect(GET.mock.calls.some(([p]) => String(p).endsWith('/department-vs-actual'))).toBe(true)
    const fb = w.get('[data-testid=dept-FB]')
    expect(fb.text()).toContain('800')
    expect(fb.text()).toContain('650')
    expect(w.get('[data-testid=dept-REST]').text()).toContain('Restaurant')
    expect(w.get('[data-testid=dept-REST]').text()).toContain('450')
    expect(w.get('[data-testid=dept-unassigned]').text()).toContain('No department')
    expect(w.get('[data-testid=dept-totals]').text()).toContain('1,500')
    expect(fb.text()).toContain('unfav.') // the expense above the plan
    const basis = w.get('select[name=basis]')
    await basis.setValue('ytd')
    expect(w.get('[data-testid=dept-ROOMS]').text()).toContain('600') // the year to date cells of the pair
  })

  it('narrows the report by department to one department', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.findAll('select[name=department_id] option').map((o) => o.text())).toEqual(['All departments', 'FB · Food and beverage'])
    await w.get('select[name=department_id]').setValue('2')
    await w.get('[data-testid=filters]').trigger('submit')
    await flushPromises()
    const call = GET.mock.calls.filter(([p]) => String(p).endsWith('/department-vs-actual')).at(-1)
    expect(call?.[1].params.query).toMatchObject({ department_id: 2 })
    // the money report is not narrowed
    expect(reportCalls().at(-1)?.[1].params.query.department_id).toBeUndefined()
  })
})
