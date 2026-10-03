import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { openPdf } from '@/utils/documents'
import CityLedgerOverdueView from './CityLedgerOverdueView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PUT = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PUT: (...a: unknown[]) => PUT(...a) } }))
vi.mock('@/utils/documents', async (orig) => ({ ...(await orig<typeof import('@/utils/documents')>()), openPdf: vi.fn() }))

const overdue = {
  as_of: '2026-10-04',
  outstanding: '500000',
  interest: '1500',
  late_fee: { monthly_rate: '3', grace_days: 2 },
  companies: [
    {
      company_id: 1, code: 'ACME', name: 'Acme Corp', outstanding: '500000', interest: '1500', oldest_days_overdue: 20, next_level: 2,
      invoices: [
        { invoice_id: 51, invoice_number: 'CINV000001', invoice_date: '2026-09-01', due_date: '2026-09-14', days_overdue: 20, bucket: '1-30', total: '400000', outstanding: '400000', interest: '1200', last_reminder: { number: 'REM000001', date: '2026-09-25', level: 1 } },
        { invoice_id: 52, invoice_number: 'CINV000002', invoice_date: '2026-09-10', due_date: '2026-09-20', days_overdue: 14, bucket: '1-30', total: '100000', outstanding: '100000', interest: '300', last_reminder: null },
      ],
    },
  ],
}

function mountView(permissions = ['cityledger.read', 'cityledger.reminder']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: overdue })
  POST = vi.fn().mockResolvedValue({ data: { id: 91, number: 'REM000002' } })
  PUT = vi.fn().mockResolvedValue({ data: {} })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(CityLedgerOverdueView, { global: { plugins: [pinia, router] } })
}

describe('CityLedgerOverdueView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
    PUT = vi.fn()
    vi.mocked(openPdf).mockClear()
  })

  it('lists the overdue invoices by company with their interest and last reminder', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=overdue-summary]').text()).toContain('500,000')
    expect(w.get('[data-testid=overdue-ACME]').text()).toContain('Acme Corp')
    expect(w.get('[data-testid=invoice-CINV000001]').text()).toContain('REM000001 (1)')
    expect(w.get('[data-testid=invoice-CINV000002]').text()).toContain('None yet')
  })

  it('records a reminder at the next level for the invoices picked and prints it', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=remind-ACME]').trigger('click')
    expect((w.get('select[name=level]').element as HTMLSelectElement).value).toBe('2')
    await w.get('input[name=note]').setValue('please pay')
    await w.get('input[name="pick-CINV000002"]').setValue(false)
    await w.get('[data-testid=reminder-form]').find('form').trigger('submit')
    await flushPromises()
    const call = POST.mock.calls[0] as [string, { params: { path: unknown; header: Record<string, string> }; body: unknown }]
    expect(call[0]).toBe('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/reminders')
    expect(call[1].params.path).toEqual({ propertyId: 7, id: 1 })
    expect(call[1].params.header['Idempotency-Key']).toBeTruthy()
    expect(call[1].body).toEqual({ level: 2, note: 'please pay', invoice_ids: [51] })
    expect(openPdf).toHaveBeenCalledWith('/api/v1/properties/7/city-ledger/reminders/91/reminder.pdf')
    expect(w.get('[data-testid=notice]').text()).toContain('REM000002')
    expect(w.find('[data-testid=reminder-form]').exists()).toBe(false)
  })

  it('keeps the form and shows the code when the server refuses', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=remind-ACME]').trigger('click')
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'INVOICE_NOT_OVERDUE', detail: 'not overdue' }))
    await w.get('[data-testid=reminder-form]').find('form').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('INVOICE_NOT_OVERDUE')
    expect(w.find('[data-testid=reminder-form]').exists()).toBe(true)
  })

  it('saves the late fee', async () => {
    const w = mountView()
    await flushPromises()
    expect((w.get('input[name=monthly_rate]').element as HTMLInputElement).value).toBe('3')
    await w.get('input[name=monthly_rate]').setValue('2.5')
    await w.get('input[name=grace_days]').setValue('5')
    await w.get('[data-testid=late-fee] form').trigger('submit')
    await flushPromises()
    expect(PUT).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/city-ledger/settings/late-fee', { params: { path: { propertyId: 7 } }, body: { monthly_rate: '2.5', grace_days: 5 } })
  })

  it('hides the reminder and late fee controls without cityledger.reminder, and needs cityledger.read', async () => {
    const w = mountView(['cityledger.read'])
    await flushPromises()
    expect(w.find('[data-testid=remind-ACME]').exists()).toBe(false)
    expect(w.find('[data-testid=late-fee]').exists()).toBe(false)
    const denied = mountView([])
    await flushPromises()
    expect(denied.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })
})
