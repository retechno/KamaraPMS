import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import CashierShiftView from './CashierShiftView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PUT = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PUT: (...a: unknown[]) => PUT(...a) } }))

const cash = { opening_float: '100000', payments: '400000', refunds: '0', receipts: '0', voided_after_close: '0', pay_ins: '0', pay_outs: '0', drops: '0', expected: '500000' }
const openShift = {
  id: 5, number: 'SHF000005', user_id: 9, user_name: 'Sari', drawer: 'MAIN', status: 'OPEN', opened_at: '2026-10-04T01:00:00Z', business_date_opened: '2026-10-04', opening_float: '100000',
  closed_at: null, business_date_closed: null, expected_cash: null, counted_cash: null, over_short: null, journal_id: null, approved_by: null, handed_over_to: null, cash, movements: [], counts: [],
}
const closedShift = { ...openShift, id: 4, number: 'SHF000004', status: 'CLOSED', expected_cash: '300000', counted_cash: '295000', over_short: '-5000', cash: undefined }

let current: unknown = null

function mountView(permissions = ['cashier.shift']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 9, email: 'sari@hotel.com', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/current')) return { data: { shift: current } }
    if (path.endsWith('/suggested-float')) return { data: { opening_float: '200000' } }
    if (path.endsWith('/settings')) return { data: { require_shift_for_cash: true, max_variance: '0', block_night_audit: true } }
    return { data: { data: [closedShift] } }
  })
  POST = vi.fn().mockResolvedValue({ data: {} })
  PUT = vi.fn().mockResolvedValue({ data: {} })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(CashierShiftView, { global: { plugins: [pinia, router] }, attachTo: document.body })
}

describe('CashierShiftView', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    current = null
    GET = vi.fn()
    POST = vi.fn()
    PUT = vi.fn()
  })

  it('opens a shift with the float the last one left', async () => {
    const w = mountView()
    await flushPromises()
    expect((w.get('input[name=opening_float]').element as HTMLInputElement).value).toBe('200000')
    await w.get('input[name=drawer]').setValue('front')
    await w.get('[data-testid=open-form] form').trigger('submit')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/cashier/shifts', { params: { path: { propertyId: 7 } }, body: { drawer: 'front', opening_float: '200000' } })
    expect(w.get('[data-testid=notice]').text()).toContain('The shift is open')
    expect(w.get('[data-testid=shift-SHF000004]').text()).toContain('-5,000')
  })

  it('shows the cash expected of the open shift and records a drop with its own key', async () => {
    current = openShift
    const w = mountView()
    await flushPromises()
    expect(w.find('[data-testid=open-form]').exists()).toBe(false)
    expect(w.get('[data-testid=expected]').text()).toContain('500,000')
    await w.get('select[name=kind]').setValue('DROP')
    await w.get('input[name=amount]').setValue('150000')
    await w.get('input[name=reason]').setValue('to the safe')
    await w.get('[data-testid=move-form] form').trigger('submit')
    await flushPromises()
    const call = POST.mock.calls[0] as [string, { params: { path: unknown; header: Record<string, string> }; body: unknown }]
    expect(call[0]).toBe('/api/v1/properties/{propertyId}/cashier/shifts/{id}/movements')
    expect(call[1].params.path).toEqual({ propertyId: 7, id: 5 })
    expect(call[1].params.header['Idempotency-Key']).toBeTruthy()
    expect(call[1].body).toEqual({ kind: 'DROP', amount: '150000', account_id: undefined, reason: 'to the safe' })
  })

  it('closes with the count and asks for an approval when the server says the difference needs one', async () => {
    current = openShift
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=start-close]').trigger('click')
    await w.get('input[name=counted_cash]').setValue('495000')
    expect(w.get('[data-testid=difference]').text()).toContain('Short by 5,000')
    POST.mockRejectedValueOnce(new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'APPROVAL_REQUIRED', detail: 'needs an approval' }))
    await w.get('[data-testid=close-form] input[name=reason]').setValue('a coin lost')
    await w.get('[data-testid=close-form] form').trigger('submit')
    await flushPromises()
    expect(document.body.textContent).toContain('Approve the difference')
    const inputs = document.body.querySelectorAll('input')
    const password = Array.from(inputs).find((i) => i.type === 'password') as HTMLInputElement
    password.value = 'secret'
    password.dispatchEvent(new Event('input'))
    await flushPromises()
    const approve = Array.from(document.body.querySelectorAll('button')).find((b) => b.type === 'submit' && b.closest('[role=dialog]')) as HTMLButtonElement
    approve.click()
    await flushPromises()
    const last = POST.mock.calls.at(-1) as [string, { body: { counted_cash: string; reason: string; approval: { email: string; password: string } } }]
    expect(last[0]).toBe('/api/v1/properties/{propertyId}/cashier/shifts/{id}/close')
    expect(last[1].body).toMatchObject({ counted_cash: '495000', reason: 'a coin lost', approval: { email: 'sari@hotel.com', password: 'secret' } })
    expect(w.get('[data-testid=notice]').text()).toContain('The shift is closed')
  })

  it('shows the settings only with cashier.settings and saves them', async () => {
    const w = mountView(['cashier.shift'])
    await flushPromises()
    expect(w.find('[data-testid=settings]').exists()).toBe(false)
    const admin = mountView(['cashier.shift', 'cashier.settings', 'cashier.shift_manage'])
    await flushPromises()
    await admin.get('input[name=max_variance]').setValue('1000')
    await admin.get('[data-testid=settings] form').trigger('submit')
    await flushPromises()
    expect(PUT).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/cashier/settings', { params: { path: { propertyId: 7 } }, body: { require_shift_for_cash: true, max_variance: '1000', block_night_audit: true } })
  })

  it('needs a cashier permission', async () => {
    const w = mountView([])
    await flushPromises()
    expect(w.get('[data-testid=no-access]').text()).toContain('cashier.shift')
    expect(GET).not.toHaveBeenCalled()
  })
})
