import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { resetDepartments } from '@/composables/useDepartments'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import DepartmentReportView from './DepartmentReportView.vue'

let GET = vi.fn()
const downloadCsv = vi.fn()
const openPdf = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))
vi.mock('./reportApi', async (orig) => ({ ...(await orig<typeof import('./reportApi')>()), downloadCsv: (...a: unknown[]) => downloadCsv(...a) }))
vi.mock('@/utils/documents', async (orig) => ({ ...(await orig<typeof import('@/utils/documents')>()), openPdf: (...a: unknown[]) => openPdf(...a) }))

const node = (over: Record<string, unknown>) => ({
  id: 1, parent_id: null, code: 'ROOMS', name: 'Rooms', level: 1, is_active: true, revenue: '600000', expense: '80000', profit: '520000', own_revenue: '600000', own_expense: '80000',
  accounts: [
    { account_id: 1, code: '4110', name: 'Room revenue', account_type: 'REVENUE', amount: '600000' },
    { account_id: 2, code: '5110', name: 'Salaries', account_type: 'EXPENSE', amount: '80000' },
  ],
  children: [], ...over,
})
const report = {
  from: '2026-09-01', to: '2026-09-30',
  departments: [
    node({}),
    node({
      id: 2, code: 'FB', name: 'Food and beverage', revenue: '400000', expense: '120000', profit: '280000', own_revenue: '0', own_expense: '0', accounts: [],
      children: [node({ id: 9, parent_id: 2, code: 'REST', name: 'Restaurant', level: 2, revenue: '300000', expense: '120000', profit: '180000', own_revenue: '300000', own_expense: '120000', accounts: [{ account_id: 3, code: '4210', name: 'Food revenue', account_type: 'REVENUE', amount: '300000' }] })],
    }),
    node({ id: 3, code: 'AG', name: 'Administrative and general', revenue: '0', expense: '50000', profit: '-50000', own_revenue: '0', own_expense: '50000', accounts: [] }),
  ],
  unassigned: node({ id: 0, code: '', name: '', revenue: '0', expense: '10000', profit: '-10000', own_revenue: '0', own_expense: '10000', accounts: [{ account_id: 4, code: '6160', name: 'Office supplies', account_type: 'EXPENSE', amount: '10000' }] }),
  totals: { revenue: '1000000', expense: '260000', profit: '740000' },
}
const departments = [
  { id: 1, parent_id: null, code: 'ROOMS', name: 'Rooms', sort_order: 10, is_active: true, level: 1, child_count: 0, in_use: true },
  { id: 2, parent_id: null, code: 'FB', name: 'Food and beverage', sort_order: 20, is_active: true, level: 1, child_count: 1, in_use: true },
]

function mountView(permissions = ['accounting.view'], answer: Record<string, unknown> = report) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (path: string) => (path.endsWith('/departments') ? { data: { data: departments } } : { data: answer }))
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(DepartmentReportView, { global: { plugins: [pinia, router] } })
}

const reportCalls = () => GET.mock.calls.filter(([p]) => String(p).endsWith('/department-report'))

describe('DepartmentReportView', () => {
  beforeEach(() => {
    resetDepartments()
    downloadCsv.mockReset()
    openPdf.mockReset()
  })

  it('shows each department with its sub-departments, the unassigned and the totals', async () => {
    const w = mountView()
    await flushPromises()
    expect(reportCalls()[0]?.[1].params).toEqual({ path: { propertyId: 7 }, query: { from: undefined, to: undefined, department_id: undefined } })
    expect(w.get('[data-testid=dept-ROOMS]').text()).toContain('600,000')
    expect(w.get('[data-testid=dept-ROOMS]').text()).toContain('520,000')
    expect(w.get('[data-testid=dept-FB]').text()).toContain('400,000')
    expect(w.get('[data-testid=dept-REST]').text()).toContain('Restaurant')
    expect(w.get('[data-testid=dept-REST]').text()).toContain('180,000')
    expect(w.get('[data-testid=dept-AG]').html()).toContain('text-destructive') // a loss
    expect(w.get('[data-testid=dept-unassigned]').text()).toContain('No department')
    expect(w.get('[data-testid=dept-unassigned]').text()).toContain('10,000')
    expect(w.get('[data-testid=totals]').text()).toContain('1,000,000')
    expect(w.get('[data-testid=totals]').text()).toContain('740,000')
  })

  it('opens a row to the accounts posted to it', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.find('[data-testid=account-ROOMS-4110]').exists()).toBe(false)
    await w.get('[data-testid=toggle-ROOMS]').trigger('click')
    expect(w.get('[data-testid=account-ROOMS-4110]').text()).toContain('Room revenue')
    expect(w.get('[data-testid=account-ROOMS-5110]').text()).toContain('80,000')
    await w.get('[data-testid=dept-REST]').trigger('click') // the whole row opens it
    expect(w.get('[data-testid=account-REST-4210]').text()).toContain('300,000')
    await w.get('[data-testid=toggle-unassigned]').trigger('click')
    expect(w.get('[data-testid=account-unassigned-6160]').text()).toContain('Office supplies')
    await w.get('[data-testid=toggle-ROOMS]').trigger('click')
    expect(w.find('[data-testid=account-ROOMS-4110]').exists()).toBe(false)
  })

  it('asks for a range and one department, and keeps them in the PDF and the CSV', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.findAll('select[name=department_id] option').map((o) => o.text())).toEqual(['All departments', 'ROOMS · Rooms', 'FB · Food and beverage'])
    await w.get('input[name=from]').setValue('2026-09-01')
    await w.get('input[name=to]').setValue('2026-09-15')
    await w.get('select[name=department_id]').setValue('2')
    await w.get('[data-testid=filters]').trigger('submit')
    await flushPromises()
    expect(reportCalls().at(-1)?.[1].params.query).toEqual({ from: '2026-09-01', to: '2026-09-15', department_id: 2 })
    await w.get('[data-testid=pdf]').trigger('click')
    expect(openPdf).toHaveBeenCalledWith('/api/v1/properties/7/accounting/department-report.pdf?from=2026-09-01&to=2026-09-15&department_id=2')
    await w.get('[data-testid=export]').trigger('click')
    expect(downloadCsv).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/accounting/department-report', { path: { propertyId: 7 }, query: { from: '2026-09-01', to: '2026-09-15', department_id: 2 } }, 'department-report.csv')
  })

  it('leaves out the unassigned line when there is none, and shows a refusal', async () => {
    const w = mountView(['accounting.view'], { ...report, unassigned: node({ id: 0, code: '', name: '', revenue: '0', expense: '0', profit: '0', accounts: [] }) })
    await flushPromises()
    expect(w.find('[data-testid=dept-unassigned]').exists()).toBe(false)
    GET.mockImplementation(async (path: string) => {
      if (path.endsWith('/departments')) return { data: { data: departments } }
      throw new ApiError({ type: 't', title: 'Not found', status: 404, code: 'DEPARTMENT_NOT_FOUND', detail: 'no such department' })
    })
    await w.get('[data-testid=filters]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=report-error]').text()).toContain('DEPARTMENT_NOT_FOUND')
    expect(w.find('[data-testid=pdf]').exists()).toBe(false)
  })

  it('needs accounting.view', async () => {
    const w = mountView([])
    await flushPromises()
    expect(w.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })
})
