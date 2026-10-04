import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import BudgetEditorView from './BudgetEditorView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PUT = vi.fn()
let PATCH = vi.fn()
let DELETE = vi.fn()
const downloadCsv = vi.fn()
vi.mock('@/api/client', () => ({
  api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PUT: (...a: unknown[]) => PUT(...a), PATCH: (...a: unknown[]) => PATCH(...a), DELETE: (...a: unknown[]) => DELETE(...a) },
}))
vi.mock('../accounting/reportApi', () => ({ downloadCsv: (...a: unknown[]) => downloadCsv(...a) }))

const months = Array.from({ length: 12 }, (_, i) => ({ number: i + 1, start: `2026-${String(i + 1).padStart(2, '0')}-01` }))
const zeros = (over: Record<number, string> = {}) => Array.from({ length: 12 }, (_, i) => over[i] ?? '0')
const accounts = [
  { id: 72, code: '4110', name: 'Room revenue', account_type: 'REVENUE', statement_group: 'REV_ROOMS', is_active: true },
  { id: 108, code: '5110', name: 'Salaries', account_type: 'EXPENSE', statement_group: 'EXP_ROOMS', is_active: true },
  { id: 133, code: '6110', name: 'Admin salaries', account_type: 'EXPENSE', statement_group: 'UND_AG', is_active: true },
]
const budget = (over: Record<string, unknown> = {}) => ({
  id: 5, year_start: '2026-01-01', year_end: '2026-12-31', year_label: 'FY2026', version: 1, name: 'Plan 2026', description: '', status: 'DRAFT', copied_from_id: null,
  activated_at: null, activated_by: null, approved_by: null, archived_at: null, created_at: '', updated_at: '', account_count: 2,
  total_revenue: '3000000', total_expense: '300000', total_result: '2700000', months, available_accounts: accounts,
  rows: [
    { account_id: 72, code: '4110', name: 'Room revenue', account_type: 'REVENUE', statement_group: 'REV_ROOMS', amounts: zeros({ 0: '1000000', 1: '1000000', 2: '1000000' }), total: '3000000' },
    { account_id: 108, code: '5110', name: 'Salaries', account_type: 'EXPENSE', statement_group: 'EXP_ROOMS', amounts: zeros({ 8: '300000' }), total: '300000' },
  ],
  ...over,
})

async function mountView(permissions = ['budget.view', 'budget.manage'], b: Record<string, unknown> = budget()) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: b })
  POST = vi.fn().mockResolvedValue({ data: b })
  PUT = vi.fn().mockResolvedValue({ data: b })
  PATCH = vi.fn().mockResolvedValue({ data: { ...b, name: 'Renamed' } })
  DELETE = vi.fn().mockResolvedValue({})
  downloadCsv.mockReset()
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/budget', component: { template: '<div />' } }, { path: '/budget/:id', component: { template: '<div />' } }] })
  await router.push('/budget/5')
  const push = vi.spyOn(router, 'push')
  const w = mount(BudgetEditorView, {
    global: { plugins: [pinia, router], stubs: { ApprovalDialog: { name: 'ApprovalDialog', emits: ['approve', 'cancel'], template: '<div data-testid="approval" />' } } },
  })
  await flushPromises()
  return { w, push }
}

const cell = (w: Awaited<ReturnType<typeof mountView>>['w'], code: string, month: number) => w.get(`input[name=a-${code}-${month}]`)

describe('BudgetEditorView', () => {
  beforeEach(() => vi.restoreAllMocks())

  it('shows the grid of a draft with the totals of the rows, the months and the year', async () => {
    const { w } = await mountView()
    expect(GET.mock.calls[0]?.[1].params.path).toEqual({ propertyId: 7, id: 5 })
    expect(w.get('h1').text()).toContain('FY2026 v1 · Plan 2026')
    expect(w.get('[data-testid=status]').attributes('data-status')).toBe('DRAFT')
    expect((cell(w, '4110', 1).element as HTMLInputElement).value).toBe('1000000')
    expect(w.get('[data-testid=total-4110]').text()).toBe('3,000,000')
    expect(w.get('[data-testid=sum-REVENUE]').text()).toContain('3,000,000')
    expect(w.get('[data-testid=sum-EXPENSE]').text()).toContain('300,000')
    expect(w.get('[data-testid=sum-RESULT]').text()).toContain('2,700,000')
    expect(w.findAll('thead th').map((th) => th.text())).toContain('Sep')
  })

  it('adds up what is typed, exactly, and saves the whole grid', async () => {
    const { w } = await mountView()
    expect(w.find('[data-testid=unsaved]').exists()).toBe(false)
    expect((w.get('[data-testid=save]').element as HTMLButtonElement).disabled).toBe(true)
    await cell(w, '4110', 4).setValue('0.1')
    await cell(w, '4110', 5).setValue('0.2')
    expect(w.get('[data-testid=total-4110]').text()).toBe('3,000,000.3')
    expect(w.find('[data-testid=unsaved]').exists()).toBe(true)
    PUT.mockResolvedValueOnce({ data: budget({ name: 'Saved' }) })
    await w.get('[data-testid=save]').trigger('click')
    await flushPromises()
    const [path, init] = PUT.mock.calls[0] as [string, { params: { path: unknown }; body: { rows: { account_id: number; amounts: string[] }[] } }]
    expect(path).toBe('/api/v1/properties/{propertyId}/budgets/{id}/grid')
    expect(init.params.path).toEqual({ propertyId: 7, id: 5 })
    expect(init.body.rows).toHaveLength(2)
    expect(init.body.rows[0]?.account_id).toBe(72)
    expect(init.body.rows[0]?.amounts[3]).toBe('0.1')
    expect(w.get('[data-testid=notice]').text()).toContain('saved')
    expect(w.find('[data-testid=unsaved]').exists()).toBe(false)
  })

  it('refuses to save an amount that is not one', async () => {
    const { w } = await mountView()
    await cell(w, '4110', 1).setValue('1,000')
    expect(w.get('[data-testid=invalid]').text()).toContain('1 figures')
    expect(cell(w, '4110', 1).attributes('aria-invalid')).toBe('true')
    expect((w.get('[data-testid=save]').element as HTMLButtonElement).disabled).toBe(true)
    expect((w.get('[data-testid=activate]').element as HTMLButtonElement).disabled).toBe(true)
  })

  it('adds an account that is not in the grid yet, and takes one out', async () => {
    const { w } = await mountView()
    const options = w.findAll('select[name=add_account] option').map((o) => o.text())
    expect(options).toContain('6110 Admin salaries')
    expect(options.some((o) => o.includes('4110'))).toBe(false)
    await w.get('select[name=add_account]').setValue('133')
    await w.get('[data-testid=add-row]').trigger('click')
    expect(w.find('[data-testid=row-6110]').exists()).toBe(true)
    expect((cell(w, '6110', 1).element as HTMLInputElement).value).toBe('')
    expect(w.get('[data-testid=total-6110]').text()).toBe('0')
    await cell(w, '6110', 2).setValue('50')
    await w.get('[data-testid=remove-5110]').trigger('click')
    expect(w.find('[data-testid=row-5110]').exists()).toBe(false)
    expect(w.get('[data-testid=sum-EXPENSE]').text()).toContain('50')
  })

  it('spreads a year over the months, after saving what is typed', async () => {
    const { w } = await mountView()
    await cell(w, '5110', 1).setValue('10')
    await w.get('[data-testid=spread-5110]').trigger('click')
    expect((w.get('input[name=spread_total]').element as HTMLInputElement).value).toBe('300010')
    await w.get('input[name=spread_total]').setValue('1200')
    await w.get('select[name=spread_method]').setValue('LAST_YEAR')
    POST.mockResolvedValueOnce({ data: budget({ name: 'Spread' }) })
    await w.get('[data-testid=spread-form]').trigger('submit')
    await flushPromises()
    expect(PUT).toHaveBeenCalledTimes(1) // the unsaved figure goes first
    const [path, init] = POST.mock.calls[0] as [string, { body: unknown }]
    expect(path).toBe('/api/v1/properties/{propertyId}/budgets/{id}/spread')
    expect(init.body).toEqual({ account_id: 108, total: '1200', method: 'LAST_YEAR' })
    expect(w.find('[data-testid=spread-form]').exists()).toBe(false)
    expect(w.get('[data-testid=notice]').text()).toContain('5110 Salaries')
  })

  it('keeps the form open and shows why a spread was refused', async () => {
    const { w } = await mountView()
    await w.get('[data-testid=spread-4110]').trigger('click')
    await w.get('input[name=spread_total]').setValue('1200')
    POST.mockRejectedValueOnce(new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'VALIDATION_FAILED', detail: 'the spread is invalid', errors: [{ field: 'method', code: 'NO_PATTERN', message: 'none' }] }))
    await w.get('[data-testid=spread-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=budget-error]').text()).toContain('method')
    expect(w.find('[data-testid=spread-form]').exists()).toBe(true)
  })

  it('fills from the actuals of last year', async () => {
    const { w } = await mountView()
    await w.get('[data-testid=fill-open]').trigger('click')
    await w.get('input[name=percent_change]').setValue('10')
    await w.get('input[name=replace]').setValue(true)
    await w.get('[data-testid=fill-form]').trigger('submit')
    await flushPromises()
    const [path, init] = POST.mock.calls[0] as [string, { body: unknown }]
    expect(path).toBe('/api/v1/properties/{propertyId}/budgets/{id}/fill-from-actuals')
    expect(init.body).toEqual({ percent_change: '10', replace: true })
    expect(w.find('[data-testid=fill-form]').exists()).toBe(false)
  })

  it('imports a CSV after checking it', async () => {
    const { w } = await mountView()
    await w.get('[data-testid=import-open]').trigger('click')
    expect((w.get('[data-testid=import-apply]').element as HTMLButtonElement).disabled).toBe(true)
    await w.get('textarea[name=csv]').setValue('code,m1\n4110,1')
    POST.mockResolvedValueOnce({ data: { dry_run: true, accounts: 1 } })
    await w.get('[data-testid=import-check]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body).toEqual({ csv: 'code,m1\n4110,1', dry_run: true })
    expect(w.get('[data-testid=import-checked]').text()).toContain('1 accounts')
    POST.mockResolvedValueOnce({ data: { dry_run: false, accounts: 1 } })
    await w.get('[data-testid=import-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[1]?.[1].body).toEqual({ csv: 'code,m1\n4110,1', dry_run: false })
    expect(w.get('[data-testid=notice]').text()).toContain('Imported 1 accounts')
    expect(w.find('[data-testid=import-form]').exists()).toBe(false)
  })

  it('does not apply a file that failed the check', async () => {
    const { w } = await mountView()
    await w.get('[data-testid=import-open]').trigger('click')
    await w.get('textarea[name=csv]').setValue('bad')
    POST.mockRejectedValueOnce(new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'VALIDATION_FAILED', detail: 'the file is invalid', errors: [{ field: 'csv', code: 'MISSING_COLUMN', message: 'needs code' }] }))
    await w.get('[data-testid=import-check]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=budget-error]').text()).toContain('csv')
    expect((w.get('[data-testid=import-apply]').element as HTMLButtonElement).disabled).toBe(true)
  })

  it('exports the grid', async () => {
    const { w } = await mountView()
    await w.get('[data-testid=export]').trigger('click')
    await flushPromises()
    expect(downloadCsv).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/budgets/{id}/export', { path: { propertyId: 7, id: 5 } }, 'budget-FY2026-v1.csv')
  })

  it('renames the draft', async () => {
    const { w } = await mountView()
    await w.get('input[name=name]').setValue('Renamed')
    await w.get('[data-testid=details-form]').trigger('submit')
    await flushPromises()
    expect(PATCH.mock.calls[0]?.[1].body).toEqual({ name: 'Renamed', description: '' })
    expect(w.get('[data-testid=notice]').text()).toContain('name was saved')
  })

  it('deletes the draft after asking', async () => {
    const { w, push } = await mountView()
    vi.spyOn(window, 'confirm').mockReturnValueOnce(false)
    await w.get('[data-testid=delete]').trigger('click')
    await flushPromises()
    expect(DELETE).not.toHaveBeenCalled()
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    await w.get('[data-testid=delete]').trigger('click')
    await flushPromises()
    expect(DELETE.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/budgets/{id}')
    expect(push).toHaveBeenCalledWith('/budget')
  })

  it('makes the draft active with an approval', async () => {
    const { w } = await mountView()
    expect(w.find('[data-testid=approval]').exists()).toBe(false)
    await w.get('[data-testid=activate]').trigger('click')
    await flushPromises()
    const dialog = w.findComponent({ name: 'ApprovalDialog' })
    expect(dialog.exists()).toBe(true)
    POST.mockResolvedValueOnce({ data: budget({ status: 'ACTIVE', approved_by: 3 }) })
    dialog.vm.$emit('approve', { email: 'boss@b.c', password: 'pw' })
    await flushPromises()
    const [path, init] = POST.mock.calls[0] as [string, { body: unknown }]
    expect(path).toBe('/api/v1/properties/{propertyId}/budgets/{id}/activate')
    expect(init.body).toEqual({ approval: { email: 'boss@b.c', password: 'pw' } })
    expect(w.find('[data-testid=approval]').exists()).toBe(false)
    expect(w.get('[data-testid=notice]').text()).toContain('FY2026 version 1 is now the active budget')
    expect(w.get('[data-testid=status]').attributes('data-status')).toBe('ACTIVE')
    expect(w.find('input[name=a-4110-1]').exists()).toBe(false) // now fixed
  })

  it('shows a refused approval in the dialog and keeps it open', async () => {
    const { w } = await mountView()
    await w.get('[data-testid=activate]').trigger('click')
    await flushPromises()
    POST.mockRejectedValueOnce(new ApiError({ type: 't', title: 'Unauthorized', status: 401, code: 'APPROVAL_INVALID_CREDENTIALS', detail: 'wrong' }))
    w.findComponent({ name: 'ApprovalDialog' }).vm.$emit('approve', { email: 'boss@b.c', password: 'nope' })
    await flushPromises()
    expect(w.find('[data-testid=approval]').exists()).toBe(true)
    expect(w.find('[data-testid=budget-error]').exists()).toBe(false)
  })

  it('reads an active budget and revises it by copying', async () => {
    const { w, push } = await mountView(['budget.view', 'budget.manage'], budget({ status: 'ACTIVE', approved_by: 3 }))
    expect(w.find('input[name=a-4110-1]').exists()).toBe(false)
    expect(w.find('[data-testid=activate]').exists()).toBe(false)
    expect(w.find('[data-testid=delete]').exists()).toBe(false)
    expect(w.find('[data-testid=active-note]').exists()).toBe(true)
    expect(w.get('[data-testid=total-4110]').text()).toBe('3,000,000')
    POST.mockResolvedValueOnce({ data: budget({ id: 8, version: 2, status: 'DRAFT' }) })
    await w.get('[data-testid=revise]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body).toEqual({ name: 'Plan 2026 (revision)', copy_from_id: 5 })
    expect(push).toHaveBeenCalledWith('/budget/8')
  })

  it('reads an archived budget', async () => {
    const { w } = await mountView(['budget.view'], budget({ status: 'ARCHIVED', approved_by: 3 }))
    expect(w.find('[data-testid=archived-note]').exists()).toBe(true)
    expect(w.find('[data-testid=revise]').exists()).toBe(false)
    expect(w.find('input[name=a-4110-1]').exists()).toBe(false)
  })

  it('shows only what the role may do', async () => {
    const { w: viewer } = await mountView(['budget.view'])
    expect(viewer.find('input[name=a-4110-1]').exists()).toBe(false)
    expect(viewer.find('[data-testid=save]').exists()).toBe(false)
    expect(viewer.find('[data-testid=add-account]').exists()).toBe(false)
    const { w: none } = await mountView([])
    expect(none.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })

  it('shows a budget that is not found', async () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    useAuthStore().me = { user: { id: 5, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: ['budget.view'] }] } as never
    usePropertyStore().currentId = 7
    GET = vi.fn().mockRejectedValue(new ApiError({ type: 't', title: 'Not found', status: 404, code: 'BUDGET_NOT_FOUND', detail: 'no' }))
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/budget/:id', component: { template: '<div />' } }] })
    await router.push('/budget/5')
    const w = mount(BudgetEditorView, { global: { plugins: [pinia, router] } })
    await flushPromises()
    expect(w.get('[data-testid=budget-error]').text()).toContain('BUDGET_NOT_FOUND')
  })
})
