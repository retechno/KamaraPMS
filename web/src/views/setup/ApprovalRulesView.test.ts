import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ApprovalRulesView from './ApprovalRulesView.vue'

let GET = vi.fn()
let PUT = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), PUT: (...a: unknown[]) => PUT(...a) } }))

const KINDS: [string, boolean, string][] = [
  ['FOLIO_ADJUSTMENT', true, 'correction.approve'], ['FOLIO_REVERSAL', true, 'correction.approve'], ['REFUND', true, 'correction.approve'], ['FOLIO_TRANSFER', true, 'correction.approve'],
  ['STAY_RATE', true, 'correction.approve'], ['RATE_OVERRIDE', true, 'reservation.override_rate_approve'], ['SALES_RESTRICTION', false, 'reservation.restriction_approve'],
  ['FREE_ROOM', true, 'reservation.complimentary_approve'], ['CITY_LEDGER_CREDIT', true, 'correction.approve'], ['CITY_LEDGER_VOID', true, 'correction.approve'], ['JOURNAL', true, 'correction.approve'],
  ['FISCAL_YEAR', false, 'correction.approve'], ['BANK', false, 'correction.approve'], ['BUDGET', false, 'budget.approve'], ['PAYABLES', true, 'correction.approve'],
  ['TAX_DOCUMENT', true, 'correction.approve'], ['TAX_SETTINGS', false, 'correction.approve'], ['SHIFT_VARIANCE', true, 'cashier.shift_approve'],
]
// approvers: BUDGET has one, SHIFT_VARIANCE nobody besides the administrators, the others three.
const approversOf = (k: string): number => (k === 'BUDGET' ? 1 : k === 'SHIFT_VARIANCE' ? 0 : 3)
function rules(over: Record<string, { rule: string; threshold: string | null }> = {}) {
  return KINDS.map(([kind, has_amount, permission]) => ({ kind, rule: over[kind]?.rule ?? 'FREE', threshold: over[kind]?.threshold ?? null, has_amount, permission, approvers: approversOf(kind), updated_at: null }))
}

function mountView(permissions = ['property.manage'], list = rules()) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.current = { id: 7, currency_decimals: 0 } as never
  GET = vi.fn().mockResolvedValue({ data: { data: list } })
  PUT = vi.fn().mockResolvedValue({ data: { data: list } })
  return mount(ApprovalRulesView, { global: { plugins: [pinia] } })
}

describe('ApprovalRulesView', () => {
  beforeEach(async () => {
    await setLocale('en')
    GET = vi.fn()
    PUT = vi.fn()
  })

  it('lists every kind with its rule, FREE by default', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.findAll('[data-testid^=rule-]').filter((e) => /^rule-[A-Z_]+$/.test(e.attributes('data-testid') ?? '')).length).toBe(18)
    expect((w.get('select[name=rule_REFUND]').element as HTMLSelectElement).value).toBe('FREE')
    expect(w.find('input[name=limit_REFUND]').exists()).toBe(false) // a limit only with the rule above a limit
    expect(w.get('[data-testid=save]').attributes('disabled')).toBeDefined() // nothing changed yet
  })

  it('offers "someone else above a limit" only to a kind that has an amount', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('select[name=rule_REFUND]').findAll('option').map((o) => o.attributes('value'))).toEqual(['FREE', 'DISTINCT', 'DISTINCT_ABOVE'])
    expect(w.get('select[name=rule_BANK]').findAll('option').map((o) => o.attributes('value'))).toEqual(['FREE', 'DISTINCT'])
    expect(w.get('[data-testid=rule-BANK]').text()).toContain('No amount')
  })

  it('saves only the kinds that were changed, a limit with the rule above a limit', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('select[name=rule_REFUND]').setValue('DISTINCT_ABOVE')
    await w.get('input[name=limit_REFUND]').setValue('500000')
    await w.get('select[name=rule_BANK]').setValue('DISTINCT')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(PUT).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/approval-rules', {
      params: { path: { propertyId: 7 } },
      body: { rules: [{ kind: 'REFUND', rule: 'DISTINCT_ABOVE', threshold: '500000' }, { kind: 'BANK', rule: 'DISTINCT', threshold: null }] },
    })
    expect(w.get('[data-testid=notice]').text()).toContain('saved')
  })

  it('asks for the limit before it sends', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('select[name=rule_REFUND]').setValue('DISTINCT_ABOVE')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(PUT).not.toHaveBeenCalled()
    expect(w.get('[data-testid=rule-REFUND]').text()).toContain('Give the limit')
  })

  it('warns when fewer than two people besides the administrators can approve a kind that asks for someone else', async () => {
    const w = mountView(['property.manage'], rules({ BUDGET: { rule: 'DISTINCT', threshold: null }, SHIFT_VARIANCE: { rule: 'DISTINCT_ABOVE', threshold: '5000' }, REFUND: { rule: 'DISTINCT', threshold: null } }))
    await flushPromises()
    expect(w.get('[data-testid=warning-BUDGET]').text()).toContain('Only one person can approve this')
    expect(w.get('[data-testid=warning-SHIFT_VARIANCE]').text()).toContain('Nobody besides the administrators')
    expect(w.find('[data-testid=warning-REFUND]').exists()).toBe(false) // three can
    expect(w.find('[data-testid=warning-BANK]').exists()).toBe(false) // a free kind needs no warning
    // the warning follows the choice, before it is saved
    await w.get('select[name=rule_BUDGET]').setValue('FREE')
    expect(w.find('[data-testid=warning-BUDGET]').exists()).toBe(false)
  })

  it('is read-only without property.manage', async () => {
    const w = mountView(['reservation.read'])
    await flushPromises()
    expect(w.find('[data-testid=read-only]').exists()).toBe(true)
    expect(w.find('[data-testid=save]').exists()).toBe(false)
    expect(w.get('select[name=rule_REFUND]').attributes('disabled')).toBeDefined()
  })

  it('speaks Indonesian', async () => {
    await setLocale('id')
    const w = mountView(['property.manage'], rules({ BUDGET: { rule: 'DISTINCT', threshold: null } }))
    await flushPromises()
    expect(w.text()).toContain('Aturan persetujuan')
    expect(w.get('[data-testid=rule-REFUND]').text()).toContain('Pengembalian dana')
    expect(w.get('[data-testid=warning-BUDGET]').text()).toContain('Hanya satu orang')
  })
})
