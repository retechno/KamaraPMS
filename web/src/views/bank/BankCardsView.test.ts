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
  as_of: '2026-10-04', account_key: 'CARD', gross: '1500000', expected_fee: '30000', expected_net: '1470000', late_count: 1, late_gross: '1000000', without_rate: 1,
  lines: [
    { journal_line_id: 1, journal_date: '2026-09-30', journal_number: 'JV1', reference: 'PAY000001', amount: '1000000', mdr_rate: '2', expected_fee: '20000', expected_net: '980000', expected_date: '2026-10-01', late: true },
    { journal_line_id: 2, journal_date: '2026-10-03', journal_number: 'JV2', reference: 'PAY000002', amount: '500000', mdr_rate: null, expected_fee: '0', expected_net: '500000', expected_date: null, late: false },
  ],
}
const rule = { id: 1, payment_method: 'CARD', mdr_rate: '2', settlement_days: 1, effective_from: '2026-09-01', created_at: '2026-09-01T00:00:00Z' }
const settlement = { id: 3, bank_account_id: 1, account_key: 'CARD', journal_date: '2026-10-01', journal_number: 'JV9', gross: '1000000', net: '976000', fee: '24000', expected_fee: '20000', fee_variance: '4000', payments: 1, created_at: '2026-10-01T00:00:00Z' }

function mountView(permissions = ['bank.view', 'bank.manage']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/expected')) return { data: expected }
    if (path.endsWith('/card-fee-rules')) return { data: { data: [rule] } }
    return { data: { data: [settlement] } }
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
    expect(w.get('[data-testid=totals]').text()).toContain('1,470,000')
    expect(w.get('[data-testid=late]').text()).toContain('1,000,000')
    expect(w.get('[data-testid=without-rate]').text()).toContain('1 payments')
    expect(w.get('[data-testid=line-PAY000001]').text()).toContain('Late')
    expect(w.get('[data-testid=line-PAY000002]').text()).not.toContain('Late')
    await w.get('select[name=account_key]').setValue('OTHER_PAYMENT')
    await flushPromises()
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/expected') && c[1].params.query.account_key === 'OTHER_PAYMENT')).toBe(true)
  })

  it('lists the settlements with the fee that was not expected', async () => {
    const w = mountView()
    await flushPromises()
    const row = w.get('[data-testid=settlement-JV9]').text()
    expect(row).toContain('24,000')
    expect(row).toContain('20,000')
    expect(row).toContain('4,000')
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
      body: { payment_method: 'CARD', mdr_rate: '2.5', settlement_days: 1, effective_from: '2026-10-15' },
    })
    expect(w.get('[data-testid=notice]').text()).toContain('The rule is added')
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
