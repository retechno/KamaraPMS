import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import TaxLiabilityView from './TaxLiabilityView.vue'
import TaxProfilesView from './TaxProfilesView.vue'
import TaxReturnsView from './TaxReturnsView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))
const openPdf = vi.fn()
vi.mock('@/utils/documents', async (orig) => ({ ...(await orig<typeof import('@/utils/documents')>()), openPdf: (...a: unknown[]) => openPdf(...a) }))

const profile = (over: Record<string, unknown> = {}) => ({
  id: 1, tax_id: 9, tax_code: 'PB1', tax_name: 'Hotel tax (PB1)', tax_rate: '10', gl_account_code: '2410', authority: 'Bapenda Badung', registration_number: 'P.2.01', due_day: 15, is_active: true,
  created_at: '2026-09-30T00:00:00Z', ...over,
})
const taxes = [{ id: 9, code: 'PB1', name: 'Hotel tax', rate: '10', is_active: true }, { id: 10, code: 'VAT', name: 'VAT', rate: '11', is_active: true }, { id: 11, code: 'OLD', name: 'Old', rate: '5', is_active: false }]
const period = (start: string, over: Record<string, unknown> = {}) => ({
  period_start: start, period_end: start.slice(0, 8) + '28', due_date: '2026-10-15', tax_amount: '220000', status: 'READY', return_id: null, paid: '0', outstanding: '0', overdue: false, ...over,
})
const periods = [period('2026-10-01', { status: 'OPEN', due_date: '2026-11-15', tax_amount: '0' }), period('2026-09-01'), period('2026-08-01', { status: 'FILED', return_id: 4, paid: '120000', outstanding: '100000', overdue: true })]
const worksheet = (over: Record<string, unknown> = {}) => ({
  profile: profile(), period_start: '2026-09-01', period_end: '2026-09-30', due_date: '2026-10-15', base_amount: '2200000', tax_amount: '220000', gl_collected: '220000', difference: '0', days: 1, posted_days: 1,
  ready: true, blockers: [], return: null,
  lines: [{ charge_code: 'ROOM', charge_name: 'Room', rate: '10', items: 1, base_amount: '1000000', tax_amount: '100000' }, { charge_code: 'MINIBAR', rate: '10', items: 3, base_amount: '1200000', tax_amount: '120000' }],
  ...over,
})
const filedReturn = (over: Record<string, unknown> = {}) => ({
  id: 4, return_number: 'TXR000001', tax_id: 9, tax_code: 'PB1', tax_name: 'PB1', period_start: '2026-08-01', period_end: '2026-08-31', due_date: '2026-09-15', base_amount: '2200000', tax_amount: '220000',
  status: 'FILED', filed_on: '2026-09-02', filing_reference: 'SPTPD-0826', filed_at: '2026-09-02T00:00:00Z', voided_at: null, paid: '120000', outstanding: '100000', payment_status: 'PARTIAL', overdue: true,
  lines: [], payments: [{ id: 21, payment_number: 'TXP000001', return_id: 4, payment_date: '2026-09-10', amount: '120000', penalty: '3000', payment_method: 'BANK_TRANSFER', reference_number: 'NTPN-1', status: 'POSTED' }], ...over,
})
const accounts = [{ id: 6, code: '7220', name: 'Other taxes', account_type: 'EXPENSE', is_postable: true, is_active: true }, { id: 7, code: '1130', name: 'Bank', account_type: 'ASSET', is_postable: true, is_active: true }]

function mountView(component: object, permissions = ['tax.view', 'tax.manage', 'tax.file', 'accounting.view'], path = '/') {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (p: string) => {
    if (p.endsWith('/tax/profiles')) return { data: { data: [profile()] } }
    if (p.endsWith('/taxes')) return { data: { data: taxes } }
    if (p.endsWith('/tax/periods')) return { data: { data: periods } }
    if (p.endsWith('/tax/worksheet')) return { data: worksheet() }
    if (p.endsWith('/tax/returns/{id}')) return { data: filedReturn() }
    if (p.endsWith('/tax/liability')) {
      return { data: { as_of: '2026-10-01', owed: '100000', taxes: [{ tax_id: 9, tax_code: 'PB1', tax_name: 'PB1', authority: 'Bapenda', account_code: '2410', collected: '440000', filed: '220000', unfiled: '220000', paid: '340000', owed: '100000', overdue_unfiled_months: 1, overdue_unpaid: '100000', returns_filed: 1 }], accounts: [{ account_code: '2410', books: '100000', owed: '100000', difference: '0' }] } }
    }
    return { data: { data: accounts } }
  })
  POST = vi.fn().mockResolvedValue({ data: filedReturn() })
  PATCH = vi.fn().mockResolvedValue({ data: profile() })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return router.push(path).then(async () => {
    await router.isReady()
    return mount(component, { global: { plugins: [pinia, router], stubs: { ApprovalDialog: { name: 'ApprovalDialog', emits: ['approve', 'cancel'], template: '<div data-testid="approval" />' } } } })
  })
}

describe('tax views', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
    PATCH = vi.fn()
    openPdf.mockReset()
  })

  it('sets up how a tax is filed, offering only taxes that are not set up', async () => {
    const w = await mountView(TaxProfilesView)
    await flushPromises()
    expect(w.get('[data-testid=profile-PB1]').text()).toContain('Bapenda Badung')
    await w.get('[data-testid=new-profile]').trigger('click')
    expect(w.findAll('select[name=tax_id] option').map((o) => o.text())).toEqual(['Choose a tax', 'VAT · VAT (11%)']) // not the one set up, not the inactive one
    await w.get('select[name=tax_id]').setValue(10)
    await w.get('input[name=authority]').setValue('KPP Pratama')
    await w.get('input[name=due_day]').setValue('20')
    await w.get('[data-testid=profile-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body).toEqual({ tax_id: 10, authority: 'KPP Pratama', registration_number: undefined, due_day: 20, is_active: true })
    await w.get('[data-testid=edit-PB1]').trigger('click')
    PATCH.mockRejectedValue(new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'VALIDATION_FAILED', detail: 'invalid', errors: [{ field: 'due_day', code: 'OUT_OF_RANGE', message: 'a day between 1 and 28' }] } as never))
    await w.get('input[name=due_day]').setValue('31')
    await w.get('[data-testid=profile-form]').trigger('submit')
    await flushPromises()
    expect(PATCH.mock.calls[0]?.[1].params.path).toEqual({ propertyId: 7, id: 1 })
    expect(w.get('[data-testid=profile-form]').text()).toContain('a day between 1 and 28')
  })

  it('shows the months of a tax and the worksheet of the one opened, with the check against the books', async () => {
    const w = await mountView(TaxReturnsView, undefined, '/tax/returns?tax=9')
    await flushPromises()
    expect(w.get('[data-testid=period-2026-10-01]').text()).toContain('Not over')
    expect(w.get('[data-testid=period-2026-09-01]').text()).toContain('Ready to file')
    expect(w.get('[data-testid=period-2026-08-01]').text()).toContain('overdue')
    await w.get('[data-testid=period-2026-09-01]').trigger('click')
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1].params.query).toEqual({ tax_id: 9, period: '2026-09-01' })
    expect(w.get('[data-testid=lines]').text()).toContain('MINIBAR')
    expect(w.get('[data-testid=totals]').text()).toContain('220,000')
    expect(w.get('[data-testid=books-check]').text()).toContain('they agree')
    await w.get('[data-testid=pdf]').trigger('click')
    expect(openPdf).toHaveBeenCalledWith('/api/v1/properties/7/tax/worksheet.pdf?tax_id=9&period=2026-09-01')
  })

  it('files a ready month with an idempotency key, and says why a month cannot be filed', async () => {
    const w = await mountView(TaxReturnsView, undefined, '/tax/returns?tax=9')
    await flushPromises()
    await w.get('[data-testid=period-2026-09-01]').trigger('click')
    await flushPromises()
    await w.get('input[name=reference]').setValue('SPTPD-0926')
    await w.get('[data-testid=file-form]').trigger('submit')
    await flushPromises()
    const [path, init] = POST.mock.calls[0] as [string, { params: { header: Record<string, string> }; body: unknown }]
    expect(path).toBe('/api/v1/properties/{propertyId}/tax/returns')
    expect(init.params.header['Idempotency-Key']).toBeTruthy()
    expect(init.body).toEqual({ tax_id: 9, period_start: '2026-09-01', filed_on: null, filing_reference: 'SPTPD-0926', notes: undefined })
    expect(w.get('[data-testid=notice]').text()).toContain('filed')
    // a month that is not ready shows its blockers and no form
    GET.mockImplementation(async (p: string) => {
      if (p.endsWith('/tax/periods')) return { data: { data: periods } }
      if (p.endsWith('/tax/profiles')) return { data: { data: [profile()] } }
      if (p.endsWith('/tax/worksheet')) return { data: worksheet({ ready: false, blockers: ['The month is not over yet.'] }) }
      return { data: { data: accounts } }
    })
    await w.get('[data-testid=period-2026-10-01]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=blockers]').text()).toContain('not over yet')
    expect(w.find('[data-testid=file-form]').exists()).toBe(false)
  })

  it('records a payment with a penalty against a filed return, and voids a payment or the return with an approval', async () => {
    const w = await mountView(TaxReturnsView, undefined, '/tax/returns?tax=9')
    await flushPromises()
    GET.mockImplementation(async (p: string) => {
      if (p.endsWith('/tax/periods')) return { data: { data: periods } }
      if (p.endsWith('/tax/profiles')) return { data: { data: [profile()] } }
      if (p.endsWith('/tax/worksheet')) return { data: worksheet({ return: filedReturn() }) }
      if (p.endsWith('/tax/returns/{id}')) return { data: filedReturn() }
      return { data: { data: accounts } }
    })
    await w.get('[data-testid=period-2026-08-01]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=filed-header]').text()).toContain('TXR000001')
    expect(w.get('[data-testid=filed-header]').text()).toContain('owed 100,000')
    expect(w.find('[data-testid=void-return]').exists()).toBe(false) // it has payments
    await w.get('[data-testid=pdf]').trigger('click')
    expect(openPdf).toHaveBeenCalledWith('/api/v1/properties/7/tax/returns/4/return.pdf')
    await w.get('[data-testid=pay-open]').trigger('click')
    expect((w.get('input[name=amount]').element as HTMLInputElement).value).toBe('100000') // what is owed
    await w.get('input[name=payment_date]').setValue('2026-10-01')
    await w.get('input[name=penalty]').setValue('5000')
    expect(w.findAll('select[name=penalty_account] option').map((o) => o.text())).toEqual(['Choose an account', '7220 · Other taxes']) // an expense account
    await w.get('select[name=penalty_account]').setValue(6)
    await w.get('[data-testid=pay-form]').trigger('submit')
    await flushPromises()
    const [path, init] = POST.mock.calls.at(-1) as [string, { params: { path: unknown; header: Record<string, string> }; body: unknown }]
    expect(path).toBe('/api/v1/properties/{propertyId}/tax/returns/{id}/payments')
    expect(init.params.path).toEqual({ propertyId: 7, id: 4 })
    expect(init.params.header['Idempotency-Key']).toBeTruthy()
    expect(init.body).toEqual({ payment_date: '2026-10-01', amount: '100000', penalty: '5000', penalty_account_id: 6, payment_method: 'BANK_TRANSFER', reference_number: undefined, remarks: undefined })
    // voiding a payment needs a reason, then the approval
    await w.get('[data-testid=void-payment-TXP000001]').trigger('click')
    expect((w.get('[data-testid=void-ask]').element as HTMLButtonElement).disabled).toBe(true)
    await w.get('input[name=reason]').setValue('paid twice')
    await w.get('[data-testid=void-ask]').trigger('click')
    w.findComponent({ name: 'ApprovalDialog' }).vm.$emit('approve', { email: 'a@b.c', password: 'pw' })
    await flushPromises()
    expect(POST.mock.calls.at(-1)?.[0]).toBe('/api/v1/properties/{propertyId}/tax/payments/{id}/void')
    expect(POST.mock.calls.at(-1)?.[1].params.path).toEqual({ propertyId: 7, id: 21 })
    expect(POST.mock.calls.at(-1)?.[1].body).toEqual({ reason: 'paid twice', approval: { email: 'a@b.c', password: 'pw' } })
  })

  it('shows what is owed, what is overdue and how the books compare', async () => {
    const w = await mountView(TaxLiabilityView)
    await flushPromises()
    expect(w.get('[data-testid=owed-total]').text()).toContain('100,000')
    expect(w.get('[data-testid=tax-PB1]').text()).toContain('440,000')
    expect(w.get('[data-testid=overdue-unfiled]').text()).toContain('1 month(s) not filed')
    expect(w.get('[data-testid=overdue-unpaid]').text()).toContain('100,000 unpaid')
    expect(w.get('[data-testid=account-2410]').text()).toContain('–') // no difference
    await w.get('input[name=as_of]').setValue('2026-09-30')
    await w.get('[data-testid=apply]').trigger('submit')
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1].params.query).toEqual({ as_of: '2026-09-30' })
  })

  it('shows only what the role may do', async () => {
    const viewer = ['tax.view']
    const p = await mountView(TaxProfilesView, viewer)
    await flushPromises()
    expect(p.find('[data-testid=new-profile]').exists()).toBe(false)
    expect(p.find('[data-testid=edit-PB1]').exists()).toBe(false)
    const r = await mountView(TaxReturnsView, viewer, '/tax/returns?tax=9')
    await flushPromises()
    await r.get('[data-testid=period-2026-09-01]').trigger('click')
    await flushPromises()
    expect(r.find('[data-testid=file-form]').exists()).toBe(false)
    for (const component of [TaxProfilesView, TaxReturnsView, TaxLiabilityView]) {
      const none = await mountView(component, [])
      await flushPromises()
      expect(none.find('[data-testid=no-access]').exists()).toBe(true)
      expect(GET).not.toHaveBeenCalled()
    }
  })
})
