import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import BudgetsView from './BudgetsView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const budget = (over: Record<string, unknown>) => ({
  id: 1, year_start: '2026-01-01', year_end: '2026-12-31', year_label: 'FY2026', version: 1, name: 'Plan 2026', status: 'ACTIVE', copied_from_id: null,
  activated_at: null, activated_by: null, approved_by: null, archived_at: null, created_at: '', updated_at: '', account_count: 2,
  total_revenue: '2000000', total_expense: '300000', total_result: '1700000', ...over,
})
const budgets = [budget({ id: 3, version: 2, name: 'Revision', status: 'DRAFT' }), budget({})]
const fiscalYears = [
  { year_start: '2026-01-01', year_end: '2026-12-31', label: 'FY2026', status: 'OPEN' },
  { year_start: '2025-01-01', year_end: '2025-12-31', label: 'FY2025', status: 'CLOSED' },
]

function mountView(permissions = ['budget.view', 'budget.manage', 'accounting.view'], list: unknown[] = budgets) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockImplementation((path: string) => Promise.resolve({ data: { data: path.endsWith('/budgets') ? list : fiscalYears } }))
  POST = vi.fn().mockResolvedValue({ data: budget({ id: 9, status: 'DRAFT' }) })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  const push = vi.spyOn(router, 'push')
  return { w: mount(BudgetsView, { global: { plugins: [pinia, router] } }), push }
}

describe('BudgetsView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
  })

  it('lists the versions with their status and totals', async () => {
    const { w } = mountView()
    await flushPromises()
    const draft = w.get('[data-testid=budget-3]')
    expect(draft.text()).toContain('FY2026')
    expect(draft.text()).toContain('v2')
    expect(draft.get('[data-status]').attributes('data-status')).toBe('DRAFT')
    expect(draft.get('[data-testid=open-3]').text()).toBe('Edit')
    const active = w.get('[data-testid=budget-1]')
    expect(active.text()).toContain('2,000,000')
    expect(active.text()).toContain('1,700,000')
    expect(active.get('[data-testid=open-1]').attributes('href')).toBe('/budget/1')
    expect(active.get('[data-testid=open-1]').text()).toBe('View')
  })

  it('starts an empty draft of a fiscal year and opens it', async () => {
    const { w, push } = mountView()
    await flushPromises()
    await w.get('[data-testid=new-budget]').trigger('click')
    const options = w.findAll('select[name=year_start] option').map((o) => o.text())
    expect(options[0]).toContain('FY2028') // two years after the latest
    expect(options).toContain('FY2025 (2025-01-01)')
    expect((w.get('select[name=year_start]').element as HTMLSelectElement).value).toBe('2026-01-01') // the current year
    expect((w.get('[data-testid=create]').element as HTMLButtonElement).disabled).toBe(true) // a name is needed
    await w.get('input[name=name]').setValue('  Plan 2027  ')
    await w.get('select[name=year_start]').setValue('2027-01-01')
    await w.get('[data-testid=new-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/budgets')
    expect(POST.mock.calls[0]?.[1].body).toEqual({ name: 'Plan 2027', description: '', year_start: '2027-01-01' })
    expect(push).toHaveBeenCalledWith('/budget/9')
  })

  it('copies a version, which keeps its year', async () => {
    const { w, push } = mountView()
    await flushPromises()
    await w.get('[data-testid=new-budget]').trigger('click')
    await w.get('select[name=copy_from_id]').setValue('1')
    expect((w.get('select[name=year_start]').element as HTMLSelectElement).disabled).toBe(true)
    await w.get('input[name=name]').setValue('Revision')
    await w.get('[data-testid=new-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body).toEqual({ name: 'Revision', description: '', copy_from_id: 1 })
    expect(push).toHaveBeenCalledWith('/budget/9')
  })

  it('asks for a date when the role cannot read the fiscal years', async () => {
    const { w } = mountView(['budget.view', 'budget.manage'])
    await flushPromises()
    await w.get('[data-testid=new-budget]').trigger('click')
    expect(w.find('select[name=year_start]').exists()).toBe(false)
    expect(w.find('input[name=year_start][type=date]').exists()).toBe(true)
    expect(GET.mock.calls.some(([p]) => String(p).includes('fiscal-years'))).toBe(false)
  })

  it('shows a refusal of the server', async () => {
    const { w } = mountView()
    await flushPromises()
    await w.get('[data-testid=new-budget]').trigger('click')
    await w.get('input[name=name]').setValue('Plan')
    POST.mockRejectedValueOnce(new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'VALIDATION_FAILED', detail: 'the budget is invalid', errors: [{ field: 'year_start', code: 'OUT_OF_RANGE', message: 'too far' }] }))
    await w.get('[data-testid=new-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=budget-error]').text()).toContain('VALIDATION_FAILED')
    expect(w.get('[data-testid=budget-error]').text()).toContain('year_start')
  })

  it('shows only what the role may do', async () => {
    const { w: viewer } = mountView(['budget.view'])
    await flushPromises()
    expect(viewer.find('[data-testid=new-budget]').exists()).toBe(false)
    expect(viewer.get('[data-testid=open-3]').text()).toBe('View')
    const { w: none } = mountView([])
    await flushPromises()
    expect(none.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })

  it('has an empty state', async () => {
    const { w } = mountView(['budget.view', 'budget.manage', 'accounting.view'], [])
    await flushPromises()
    expect(w.find('[data-testid=empty]').exists()).toBe(true)
    expect(w.find('[data-testid=budgets]').exists()).toBe(false)
  })
})
