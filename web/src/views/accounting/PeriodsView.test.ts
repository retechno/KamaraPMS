import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import PeriodsView from './PeriodsView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const period = (over: Record<string, unknown>) => ({
  period_start: '2026-09-01', period_end: '2026-09-30', status: 'OPEN', days: 30, posted_days: 30, closable: false, reopenable: false, closed_at: null, closed_by: null, reopened_at: null, ...over,
})
const periods = [
  period({ period_start: '2026-10-01', period_end: '2026-10-31', days: 31, posted_days: 4 }),
  period({ closable: true }),
  period({ period_start: '2026-08-01', period_end: '2026-08-31', status: 'CLOSED', days: 31, posted_days: 31, reopenable: true }),
]

function mountView(permissions = ['accounting.view', 'accounting.close']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: { data: periods } })
  POST = vi.fn().mockResolvedValue({ data: period({}) })
  return mount(PeriodsView, { global: { plugins: [pinia] } })
}

describe('PeriodsView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
    vi.restoreAllMocks()
  })

  it('shows how far each month is and what can be done with it', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=period-2026-10-01]').text()).toContain('4 of 31')
    expect(w.find('[data-testid=close-2026-10-01]').exists()).toBe(false)
    expect(w.find('[data-testid=close-2026-09-01]').exists()).toBe(true)
    expect(w.get('[data-testid=period-2026-08-01]').text()).toContain('Closed')
    expect(w.find('[data-testid=reopen-2026-08-01]').exists()).toBe(true)
  })

  it('closes a month after asking', async () => {
    const w = mountView()
    await flushPromises()
    vi.spyOn(window, 'confirm').mockReturnValueOnce(false)
    await w.get('[data-testid=close-2026-09-01]').trigger('click')
    expect(POST).not.toHaveBeenCalled()
    vi.spyOn(window, 'confirm').mockReturnValueOnce(true)
    await w.get('[data-testid=close-2026-09-01]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/accounting/periods/{start}/close')
    expect(POST.mock.calls[0]?.[1].params.path).toEqual({ propertyId: 7, start: '2026-09-01' })
    expect(w.get('[data-testid=notice]').text()).toContain('September 2026 is closed')
  })

  it('reopens with a reason and shows a refusal', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=reopen-2026-08-01]').trigger('click')
    expect((w.get('[data-testid=reopen-run]').element as HTMLButtonElement).disabled).toBe(true)
    await w.get('input[name=reason]').setValue('missing invoice')
    POST.mockRejectedValueOnce(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'PERIOD_NOT_LATEST', detail: 'only the latest closed period can be reopened' }))
    await w.get('[data-testid=reopen-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=period-error]').text()).toContain('PERIOD_NOT_LATEST')
    await w.get('[data-testid=reopen-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls.at(-1)?.[1].body).toEqual({ reason: 'missing invoice' })
    expect(w.get('[data-testid=notice]').text()).toContain('August 2026 is open again')
  })

  it('shows only what the role may do', async () => {
    const viewer = mountView(['accounting.view'])
    await flushPromises()
    expect(viewer.find('[data-testid=close-2026-09-01]').exists()).toBe(false)
    expect(viewer.find('[data-testid=reopen-2026-08-01]').exists()).toBe(false)
    const none = mountView([])
    await flushPromises()
    expect(none.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })
})
