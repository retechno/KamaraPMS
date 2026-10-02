import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import AccountMappingView from './AccountMappingView.vue'

let GET = vi.fn()
let PUT = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), PUT: (...a: unknown[]) => PUT(...a) } }))

const acc = (id: number, code: string, name: string, type: string, over: Record<string, unknown> = {}) => ({
  id, code, name, account_type: type, normal_side: 'DEBIT', parent_id: null, is_postable: true, is_active: true, in_use: false, created_at: '2026-09-30T00:00:00Z', ...over,
})
const accounts = [
  acc(1, '1110', 'Cash on hand', 'ASSET'), acc(2, '1130', 'Bank', 'ASSET'), acc(3, '2310', 'Advance deposits', 'LIABILITY'), acc(4, '4110', 'Room revenue', 'REVENUE'),
  acc(5, '1100', 'Cash header', 'ASSET', { is_postable: false }), acc(6, '1199', 'Closed bank', 'ASSET', { is_active: false }),
]
const entry = (key: string, meaning: string, id: number) => ({ map_key: key, meaning, account_id: id, account_code: 'x', account_name: 'y', account_type: 'ASSET' })
const map = [entry('CASH', 'Cash payments', 1), entry('ADVANCE_DEPOSITS', 'Deposits held until the guest checks out', 3), entry('SUSPENSE', 'Anything that cannot be placed', 3)]
const issues = {
  checked: 13,
  issues: [
    { kind: 'CHARGE_CODE', id: 9, code: 'LAUNDRY', name: 'Laundry', problem: 'NO_CODE', posted_to: '2990 Suspense - unmapped items' },
    { kind: 'TAX', id: 4, code: 'VAT', name: 'VAT', gl_account_code: '9999', problem: 'UNKNOWN_ACCOUNT', posted_to: '2410 Hotel and restaurant tax payable' },
  ],
}

function mountView(permissions = ['accounting.view', 'accounting.manage'], report: object = issues) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/account-map')) return { data: { data: map } }
    if (path.endsWith('/unmapped')) return { data: report }
    return { data: { data: accounts } }
  })
  PUT = vi.fn().mockResolvedValue({ data: { data: map } })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(AccountMappingView, { global: { plugins: [pinia, router] } })
}

describe('AccountMappingView', () => {
  beforeEach(() => {
    GET = vi.fn()
    PUT = vi.fn()
  })

  it('offers each key only the accounts it can use', async () => {
    const w = mountView()
    await flushPromises()
    const cash = w.findAll('select[name=map_CASH] option').map((o) => o.text())
    expect(cash).toEqual(['1110 · Cash on hand', '1130 · Bank']) // assets that take postings and are active
    expect(w.findAll('select[name=map_ADVANCE_DEPOSITS] option').map((o) => o.text())).toEqual(['2310 · Advance deposits'])
    expect(w.findAll('select[name=map_SUSPENSE] option')).toHaveLength(4) // suspense may be anything that takes postings
    expect((w.get('[data-testid=save-map]').element as HTMLButtonElement).disabled).toBe(true)
  })

  it('saves only the keys that changed', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('select[name=map_CASH]').setValue(2)
    await w.get('[data-testid=save-map]').trigger('click')
    await flushPromises()
    expect(PUT.mock.calls[0]?.[1].body).toEqual({ entries: [{ map_key: 'CASH', account_id: 2 }] })
    expect(w.get('[data-testid=notice]').text()).toContain('Saved')
  })

  it('shows a refusal with its field errors', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('select[name=map_CASH]').setValue(2)
    PUT.mockRejectedValue(new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'VALIDATION_FAILED', detail: 'the mapping is invalid', errors: [{ field: 'entries[0].account_id', code: 'WRONG_TYPE', message: 'an account of type ASSET' }] } as never))
    await w.get('[data-testid=save-map]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=map-error]').text()).toContain('an account of type ASSET')
  })

  it('lists what cannot be placed and where it is posted instead', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=issue-CHARGE_CODE-LAUNDRY]').text()).toContain('has no account code')
    expect(w.get('[data-testid=issue-CHARGE_CODE-LAUNDRY]').text()).toContain('2990 Suspense')
    expect(w.get('[data-testid=issue-TAX-VAT]').text()).toContain('9999: names an account that is not in the chart')
    expect(w.get('[data-testid=issue-TAX-VAT] a').attributes('href')).toBe('/setup/taxes')
    const clean = mountView(['accounting.view'], { checked: 13, issues: [] })
    await flushPromises()
    expect(clean.get('[data-testid=all-mapped]').text()).toContain('All 13 active items')
  })

  it('is read-only without accounting.manage', async () => {
    const w = mountView(['accounting.view'])
    await flushPromises()
    expect(w.find('select[name=map_CASH]').exists()).toBe(false)
    expect(w.find('[data-testid=save-map]').exists()).toBe(false)
    expect(w.get('[data-testid=map-CASH]').text()).toContain('Cash payments')
    const none = mountView([])
    await flushPromises()
    expect(none.find('[data-testid=no-access]').exists()).toBe(true)
  })
})
