import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import FiscalYearsView from './FiscalYearsView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const year = (over: Record<string, unknown>) => ({
  year_start: '2025-10-01', year_end: '2026-09-30', label: 'FY2026', status: 'OPEN', months: 12, closed_months: 12, net_income: '1840000', closable: true, reopenable: false,
  closing_journal_id: null, closed_at: null, reopened_at: null, ...over,
})
const years = [
  year({ year_start: '2026-10-01', year_end: '2027-09-30', label: 'FY2027', closed_months: 0, net_income: '0', closable: false }),
  year({}),
  year({ year_start: '2024-10-01', year_end: '2025-09-30', label: 'FY2025', status: 'CLOSED', closable: false, reopenable: true, closing_journal_number: 'JV000009' }),
]

function mountView(permissions = ['accounting.view', 'accounting.close']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: { data: years } })
  POST = vi.fn().mockResolvedValue({ data: year({}) })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(FiscalYearsView, { global: { plugins: [pinia, router], stubs: { ApprovalDialog: { name: 'ApprovalDialog', emits: ['approve', 'cancel'], template: '<div data-testid="approval" />' } } } })
}

describe('FiscalYearsView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
    vi.restoreAllMocks()
  })

  it('shows the years with their result and what can be done', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=year-FY2026]').text()).toContain('12 of 12')
    expect(w.get('[data-testid=year-FY2026]').text()).toContain('1,840,000')
    expect(w.find('[data-testid=close-FY2026]').exists()).toBe(true)
    expect(w.find('[data-testid=close-FY2027]').exists()).toBe(false)
    expect(w.get('[data-testid=year-FY2025]').text()).toContain('journal JV000009')
    expect(w.find('[data-testid=reopen-FY2025]').exists()).toBe(true)
  })

  it('closes a year after asking, and shows a refusal', async () => {
    const w = mountView()
    await flushPromises()
    vi.spyOn(window, 'confirm').mockReturnValueOnce(false)
    await w.get('[data-testid=close-FY2026]').trigger('click')
    expect(POST).not.toHaveBeenCalled()
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    POST.mockRejectedValueOnce(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'FISCAL_YEAR_NOT_READY', detail: 'a month is still open' }))
    await w.get('[data-testid=close-FY2026]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=year-error]').text()).toContain('FISCAL_YEAR_NOT_READY')
    await w.get('[data-testid=close-FY2026]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls.at(-1)?.[0]).toBe('/api/v1/properties/{propertyId}/accounting/fiscal-years/{start}/close')
    expect(POST.mock.calls.at(-1)?.[1].params.path).toEqual({ propertyId: 7, start: '2025-10-01' })
    expect(w.get('[data-testid=notice]').text()).toContain('FY2026 is closed')
  })

  it('reopens with a reason and an approval', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=reopen-FY2025]').trigger('click')
    expect((w.get('[data-testid=reopen-ask]').element as HTMLButtonElement).disabled).toBe(true)
    await w.get('input[name=reason]').setValue('audit adjustment')
    await w.get('[data-testid=reopen-form]').trigger('submit')
    const dialog = w.findComponent({ name: 'ApprovalDialog' })
    expect(dialog.exists()).toBe(true)
    dialog.vm.$emit('approve', { email: 'a@b.c', password: 'pw' })
    await flushPromises()
    const [path, init] = POST.mock.calls.at(-1) as [string, { params: { path: unknown }; body: unknown }]
    expect(path).toBe('/api/v1/properties/{propertyId}/accounting/fiscal-years/{start}/reopen')
    expect(init.params.path).toEqual({ propertyId: 7, start: '2024-10-01' })
    expect(init.body).toEqual({ reason: 'audit adjustment', approval: { email: 'a@b.c', password: 'pw' } })
    expect(w.get('[data-testid=notice]').text()).toContain('FY2025 is open again')
  })

  it('shows only what the role may do', async () => {
    const viewer = mountView(['accounting.view'])
    await flushPromises()
    expect(viewer.find('[data-testid=close-FY2026]').exists()).toBe(false)
    expect(viewer.find('[data-testid=reopen-FY2025]').exists()).toBe(false)
    const none = mountView([])
    await flushPromises()
    expect(none.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })
})
