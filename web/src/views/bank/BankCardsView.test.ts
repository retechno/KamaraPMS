import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import BankCardsView from './BankCardsView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const expected = {
  as_of: '2026-10-04', account_key: 'CARD', gross: '1500000', expected_mdr: '30000', expected_vat: '2200', expected_deduction: '32200', expected_net: '1467800', late_count: 1, late_gross: '1000000',
  without_rate: 1, without_vat_rate: 1,
  lines: [
    { journal_line_id: 1, journal_date: '2026-09-30', journal_number: 'JV1', reference: 'PAY000001', amount: '1000000', mdr_rate: '2', expected_mdr: '20000', vat_rate: '11', expected_vat: '2200', expected_deduction: '22200', without_vat_rate: false, expected_net: '977800', expected_date: '2026-10-01', late: true },
    { journal_line_id: 2, journal_date: '2026-10-03', journal_number: 'JV2', reference: 'PAY000002', amount: '500000', mdr_rate: null, expected_mdr: '0', vat_rate: null, expected_vat: '0', expected_deduction: '0', without_vat_rate: false, expected_net: '500000', expected_date: null, late: false },
    { journal_line_id: 3, journal_date: '2026-10-03', journal_number: 'JV3', reference: 'PAY000003', amount: '200000', mdr_rate: '2', expected_mdr: '4000', vat_rate: null, expected_vat: '0', expected_deduction: '4000', without_vat_rate: true, expected_net: '196000', expected_date: '2026-10-05', late: false },
  ],
}
const rule = { id: 1, payment_method: 'CARD', mdr_rate: '2', vat_rate: '11', settlement_days: 1, effective_from: '2026-09-01', created_at: '2026-09-01T00:00:00Z' }
const settlement = {
  id: 3, bank_account_id: 1, account_key: 'CARD', journal_date: '2026-10-01', journal_number: 'JV9', gross: '1000000', net: '976000', fee: '24000', mdr_amount: '21800', vat_amount: '2200', vat_treatment: 'CREDITABLE',
  expected_mdr: '20000', expected_vat: '2200', proposed_vat: '2200', mdr_rate: '2', vat_rate: '11', payments_without_vat_rate: 0, mdr_variance: '1800', vat_variance: '0', payments: 1, created_at: '2026-10-01T00:00:00Z',
  status: 'POSTED', voided_at: null, void_reason: null, void_journal_number: null, statement_id: 4, statement_line_no: 1,
}
const voidedSettlement = { ...settlement, id: 2, journal_number: 'JV8', status: 'VOIDED', voided_at: '2026-10-02T00:00:00Z', void_reason: 'wrong batch', void_journal_number: 'JV10', statement_id: 0, statement_line_no: 0 }

function mountView(permissions = ['bank.view', 'bank.manage', 'bank.reconcile']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/expected')) return { data: expected }
    if (path.endsWith('/card-fee-rules')) return { data: { data: [rule] } }
    return { data: { data: [settlement, voidedSettlement] } }
  })
  POST = vi.fn().mockResolvedValue({ data: rule })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(BankCardsView, { global: { plugins: [pinia, router] } })
}

describe('BankCardsView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
  })

  it('shows what the acquirer should still pay, what is late and what has no rate', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=totals]').text()).toContain('1,467,800')
    expect(w.get('[data-testid=late]').text()).toContain('1,000,000')
    expect(w.get('[data-testid=without-rate]').text()).toContain('1 payments')
    expect(w.get('[data-testid=line-PAY000001]').text()).toContain('Late')
    expect(w.get('[data-testid=line-PAY000002]').text()).not.toContain('Late')
    await w.get('select[name=account_key]').setValue('OTHER_PAYMENT')
    await flushPromises()
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/expected') && c[1].params.query.account_key === 'OTHER_PAYMENT')).toBe(true)
  })

  it('shows the VAT expected on the fee and marks the payments that have no VAT rate', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=expected-vat]').text()).toBe('2,200')
    expect(w.get('[data-testid=line-PAY000001]').text()).toContain('2,200')
    expect(w.get('[data-testid=line-PAY000001]').text()).toContain('(11%)')
    expect(w.get('[data-testid=line-PAY000001]').text()).toContain('977,800')
    expect(w.get('[data-testid=no-vat-rate-PAY000003]').text()).toBe('no rate')
    expect(w.find('[data-testid=no-vat-rate-PAY000002]').exists()).toBe(false) // no rate at all: told by the other count
    expect(w.get('[data-testid=without-vat-rate]').text()).toContain('1 payments have no VAT rate')
    expect(w.get('[data-testid=rule-list]').text()).toContain('VAT 11%')
  })

  it('lists the settlements with the fee that was not expected', async () => {
    const w = mountView()
    await flushPromises()
    const row = w.get('[data-testid=settlement-JV9]').text()
    expect(row).toContain('24,000')
    expect(row).toContain('20,000')
    expect(row).toContain('21,800')
    expect(row).toContain('2,200')
    expect(row).toContain('1,800')
    expect(w.get('[data-testid=treatment-JV9]').text()).toBe('claimed')
  })

  it('adds a fee rule', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=rule-list]').text()).toContain('2%')
    await w.get('input[name=mdr_rate]').setValue('2.5')
    await w.get('input[name=effective_from]').setValue('2026-10-15')
    await w.get('[data-testid=rule-form]').trigger('submit')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/bank/card-fee-rules', {
      params: { path: { propertyId: 7 } },
      body: { payment_method: 'CARD', mdr_rate: '2.5', vat_rate: undefined, settlement_days: 1, effective_from: '2026-10-15' },
    })
    expect(w.get('[data-testid=notice]').text()).toContain('The rule is added')
  })

  it('adds a fee rule for QRIS and settles it as its own clearing account', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('select[name=rule_method]').findAll('option').map((o) => o.text())).toEqual(['Cards', 'E-wallets', 'QRIS'])
    expect(w.get('select[name=account_key]').findAll('option').map((o) => o.text())).toEqual(['Cards', 'E-wallets', 'QRIS'])
    await w.get('select[name=rule_method]').setValue('QRIS')
    await w.get('input[name=mdr_rate]').setValue('0.7')
    await w.get('input[name=vat_rate]').setValue('11')
    await w.get('input[name=effective_from]').setValue('2026-10-15')
    await w.get('[data-testid=rule-form]').trigger('submit')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/bank/card-fee-rules', {
      params: { path: { propertyId: 7 } },
      body: { payment_method: 'QRIS', mdr_rate: '0.7', vat_rate: '11', settlement_days: 1, effective_from: '2026-10-15' },
    })
    GET.mockClear()
    await w.get('select[name=account_key]').setValue('QRIS')
    await flushPromises()
    expect(GET.mock.calls.some((c) => JSON.stringify(c[1]).includes('"account_key":"QRIS"'))).toBe(true)
  })

  it('adds a fee rule with the VAT the acquirer charges on its commission', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('input[name=mdr_rate]').setValue('0.7')
    await w.get('input[name=vat_rate]').setValue('11')
    await w.get('input[name=effective_from]').setValue('2026-10-15')
    await w.get('[data-testid=rule-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body).toEqual({ payment_method: 'CARD', mdr_rate: '0.7', vat_rate: '11', settlement_days: 1, effective_from: '2026-10-15' })
  })

  it('voids a settlement with a reason and an approval for its gross', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=voided-JV8]').text()).toBe('Voided')
    expect(w.get('[data-testid=settlement-JV8]').text()).toContain('JV10 · wrong batch')
    expect(w.find('[data-testid=void-JV8]').exists()).toBe(false)
    await w.get('[data-testid=void-JV9]').trigger('click')
    expect(w.get('[data-testid=void-form]').text()).toContain('1,000,000')
    expect((w.get('[data-testid=void-ask]').element as HTMLButtonElement).disabled).toBe(true)
    await w.get('input[name=reason]').setValue('  wrong batch  ')
    await w.get('[data-testid=void-form]').trigger('submit')
    const dialog = w.findComponent({ name: 'ApprovalDialog' })
    expect(dialog.props('kind')).toBe('BANK_SETTLEMENT_VOID')
    expect(dialog.props('amount')).toBe('1000000')
    POST = vi.fn().mockResolvedValue({ data: { ...settlement, status: 'VOIDED' } })
    dialog.vm.$emit('approve', { email: 'a@b.c', password: 'pw' })
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/bank/card-settlements/{id}/void', {
      params: { path: { propertyId: 7, id: 3 } },
      body: { reason: 'wrong batch', approval: { email: 'a@b.c', password: 'pw' } },
    })
    expect(w.get('[data-testid=notice]').text()).toContain('Settlement JV9 is voided')
    expect(w.findComponent({ name: 'ApprovalDialog' }).exists()).toBe(false)
  })

  it('says what reopening the month means when the void is in a closed month, and points to a manual journal', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=void-JV9]').trigger('click')
    await w.get('input[name=reason]').setValue('wrong')
    await w.get('[data-testid=void-form]').trigger('submit')
    const { ApiError } = await import('@/api/problem')
    POST = vi.fn().mockRejectedValue(new ApiError({ status: 409, code: 'PERIOD_CLOSED', title: 'closed', detail: 'the books of 2026-09 are closed', context: { period: '2026-09-01', month: '2026-09' } } as never))
    w.findComponent({ name: 'ApprovalDialog' }).vm.$emit('approve', { email: 'a@b.c', password: 'pw' })
    await flushPromises()
    const alert = w.get('[data-testid=void-period-closed]').text()
    expect(alert).toContain('The books of Sep 2026 are closed')
    expect(alert).toContain("changes the figures of that month's reports")
    expect(alert).toContain('accountant')
    expect(alert).toContain('Manual journals')
    expect(w.get('[data-testid=void-period-closed] a').attributes('href')).toBe('/accounting/journals')
    expect(w.findComponent({ name: 'ApprovalDialog' }).exists()).toBe(false)
  })

  it('offers the void only with bank.reconcile', async () => {
    const w = mountView(['bank.view', 'bank.manage'])
    await flushPromises()
    expect(w.find('[data-testid=void-JV9]').exists()).toBe(false)
    expect(w.get('[data-testid=settlement-JV9]').text()).toContain('Posted')
  })

  it('hides the rule form without bank.manage and needs bank.view', async () => {
    const w = mountView(['bank.view'])
    await flushPromises()
    expect(w.find('[data-testid=rule-form]').exists()).toBe(false)
    const denied = mountView([])
    await flushPromises()
    expect(denied.get('[data-testid=no-access]').text()).toContain('bank.view')
    expect(GET).not.toHaveBeenCalled()
  })
})

describe('BankCardsView, what is seen is what is sent', () => {
  it('does not add a rule while the date is half typed, and says why', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('input[name=mdr_rate]').setValue('2.5')
    await w.get('[data-testid=rule-form] input[data-slot=date-input-text]').setValue('3/2')
    const before = POST.mock.calls.length
    await w.get('[data-testid=rule-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls.length).toBe(before)
    expect(w.text()).toContain('Write as dd/mm/yyyy')
  })
})
