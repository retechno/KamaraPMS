import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import GeneralLedgerView from './GeneralLedgerView.vue'
import ReconciliationView from './ReconciliationView.vue'
import StatementView from './StatementView.vue'
import TrialBalanceView from './TrialBalanceView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const download = vi.fn()
const openPdf = vi.fn()
vi.mock('@/utils/documents', async (orig) => ({ ...(await orig<typeof import('@/utils/documents')>()), openPdf: (...a: unknown[]) => openPdf(...a) }))
vi.mock('./reportApi', async (orig) => ({ ...(await orig<typeof import('./reportApi')>()), downloadCsv: (...a: unknown[]) => download(...a) }))

const acc = { id: 1, code: '1110', name: 'Cash on hand', account_type: 'ASSET', normal_side: 'DEBIT', is_postable: true, is_active: true }
const row = (code: string, over: Record<string, unknown> = {}) => ({
  account_id: 1, code, name: 'Cash on hand', account_type: 'ASSET', opening_debit: '0', opening_credit: '0', debit: '300000', credit: '60000', closing_debit: '240000', closing_credit: '0', ...over,
})

const answers: Record<string, unknown> = {
  '/trial-balance': {
    from: '2026-09-30', to: '2026-09-30', rows: [row('1110'), row('2310', { debit: '0', credit: '300000', closing_debit: '0', closing_credit: '300000' })],
    totals: row('', { debit: '300000', credit: '360000', closing_debit: '240000', closing_credit: '300000' }),
  },
  '/ledger': {
    from: '2026-09-30', to: '2026-09-30', account: acc, opening_balance: '0', total_debit: '300000', total_credit: '60000', closing_balance: '240000', truncated: false,
    lines: [{ journal_date: '2026-09-30', journal_id: 4, journal_number: 'JV000001', journal_type: 'DAY_CLOSE', description: 'Payments CASH', debit: '300000', credit: '0', balance: '300000' }],
  },
  '/income-statement': {
    from: '2026-09-01', to: '2026-09-30', net_income: '1000',
    lines: [
      { key: 'H_REVENUE', title: 'Operating revenue', kind: 'HEADING', amount: '0', accounts: [] },
      { key: 'REV_FB', title: 'Food and beverage', kind: 'GROUP', amount: '1100', accounts: [{ account_id: 1, code: '4210', name: 'Food revenue', amount: '100' }, { account_id: 2, code: '4230', name: 'Minibar revenue', amount: '1000' }] },
      { key: 'REV_ROOMS', title: 'Rooms', kind: 'GROUP', amount: '500', accounts: [{ account_id: 3, code: '4110', name: 'Room revenue', amount: '500' }] },
      { key: 'GOP', title: 'Gross operating profit', kind: 'TOTAL', amount: '1000', accounts: [] },
      { key: 'NET_INCOME', title: 'Net income', kind: 'TOTAL', amount: '1000', accounts: [] },
    ],
  },
  '/balance-sheet': { as_of: '2026-09-30', lines: [{ key: 'TOTAL_ASSETS', title: 'Total assets', kind: 'TOTAL', amount: '5', accounts: [] }], total_assets: '5', total_liabilities: '3', total_equity: '1', difference: '1' },
  '/reconciliation': {
    as_of: '2026-09-30', start_date: '2026-09-30', pending_days: 0, includes_open_day: false, reconciled: true,
    controls: [{ key: 'GUEST_LEDGER', title: 'Guest ledger', account: '1210 Guest ledger', ledger: '700', source: '700', difference: '0', basis: 'folios' }],
  },
}

function mountView(component: object, permissions = ['accounting.view'], routeName = '', over: Record<string, unknown> = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (path: string) => {
    const hit = Object.keys({ ...answers, ...over }).find((k) => path.endsWith(k))
    if (hit) return { data: { ...answers, ...over }[hit] }
    return { data: { data: [acc] } } // the list of accounts
  })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', name: routeName || 'x', component: { template: '<div />' } }] })
  return router.push('/').then(async () => {
    await router.isReady()
    return mount(component, { global: { plugins: [pinia, router] } })
  })
}

describe('report views', () => {
  beforeEach(() => {
    GET = vi.fn()
    download.mockReset()
    openPdf.mockReset()
  })

  it('shows the trial balance with its totals and exports it', async () => {
    const w = await mountView(TrialBalanceView)
    await flushPromises()
    expect(w.get('[data-testid=row-1110]').text()).toContain('240000')
    expect(w.get('[data-testid=totals]').text()).toContain('360000')
    await w.get('input[name=from]').setValue('2026-09-01')
    await w.get('[data-testid=apply]').trigger('submit')
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1].params.query).toEqual({ from: '2026-09-01', to: undefined })
    await w.get('[data-testid=export]').trigger('click')
    expect(download.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/accounting/trial-balance')
    expect(download.mock.calls[0]?.[2]).toBe('trial-balance-2026-09-30.csv')
    await w.get('[data-testid=pdf]').trigger('click')
    expect(openPdf).toHaveBeenCalledWith('/api/v1/properties/7/accounting/trial-balance.pdf?from=2026-09-01')
  })

  it('shows what the server refuses in a range', async () => {
    const w = await mountView(TrialBalanceView)
    await flushPromises()
    GET.mockRejectedValue(new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'VALIDATION_FAILED', detail: 'the range is invalid', errors: [{ field: 'to', code: 'BEFORE_FROM', message: 'the end is before the start' }] } as never))
    await w.get('[data-testid=apply]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=report-error]').text()).toContain('the end is before the start')
  })

  it('shows the ledger of the chosen account with running balances', async () => {
    const w = await mountView(GeneralLedgerView)
    await flushPromises()
    expect(w.find('[data-testid=ledger]').exists()).toBe(false) // nothing until an account is chosen
    await w.get('select[name=account]').setValue(1)
    await w.get('[data-testid=apply]').trigger('submit')
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1].params.path).toEqual({ propertyId: 7, id: 1 })
    expect(w.get('[data-testid=entry-0]').text()).toContain('JV000001')
    expect(w.get('[data-testid=closing]').text()).toContain('240000')
    await w.get('[data-testid=export]').trigger('click')
    expect(download.mock.calls[0]?.[2]).toBe('ledger-1110.csv')
    await w.get('[data-testid=pdf]').trigger('click')
    expect(openPdf).toHaveBeenCalledWith('/api/v1/properties/7/accounting/accounts/1/ledger.pdf')
  })

  it('opens the ledger of the account and the month a journal line sent it to', async () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: ['accounting.view'] }] } as never
    usePropertyStore().currentId = 7
    GET = vi.fn(async (path: string) => (path.endsWith('/ledger') ? { data: answers['/ledger'] } : { data: { data: [acc] } }))
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
    await router.push('/accounting/ledger?account=1&from=2026-09-01&to=2026-09-30')
    const w = mount(GeneralLedgerView, { global: { plugins: [pinia, router] } })
    await flushPromises()
    expect((w.get('select[name=account]').element as HTMLSelectElement).value).toBe('1')
    expect((w.get('input[name=from]').element as HTMLInputElement).value).toBe('2026-09-01')
    expect(GET.mock.calls.at(-1)?.[1].params.query).toEqual({ from: '2026-09-01', to: '2026-09-30' })
    expect(w.find('[data-testid=ledger]').exists()).toBe(true)
  })

  it('lays the income statement out by group and total', async () => {
    const w = await mountView(StatementView, ['accounting.view'], 'accounting-income-statement')
    await flushPromises()
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/income-statement'))).toBe(true)
    expect(w.get('[data-testid=line-GOP]').text()).toContain('Gross operating profit')
    expect(w.text()).toContain('Minibar revenue') // a group with several accounts lists them
    expect(w.text()).not.toContain('Room revenue') // a group with one shows its total only
    expect(w.get('[data-testid=line-REV_ROOMS]').text()).toContain('500')
    await w.get('input[name=to]').setValue('2026-09-15')
    await w.get('[data-testid=apply]').trigger('submit')
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1].params.query).toEqual({ from: undefined, to: '2026-09-15' })
    await w.get('[data-testid=pdf]').trigger('click')
    expect(openPdf).toHaveBeenCalledWith('/api/v1/properties/7/accounting/income-statement.pdf?to=2026-09-15')
  })

  it('warns when the balance sheet does not balance', async () => {
    const w = await mountView(StatementView, ['accounting.view'], 'accounting-balance-sheet')
    await flushPromises()
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/balance-sheet'))).toBe(true)
    expect(w.get('[data-testid=imbalance]').text()).toContain('out of balance by 1')
    await w.get('input[name=as_of]').setValue('2026-09-30')
    await w.get('[data-testid=apply]').trigger('submit')
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1].params.query).toEqual({ as_of: '2026-09-30' })
    await w.get('[data-testid=pdf]').trigger('click')
    expect(openPdf).toHaveBeenCalledWith('/api/v1/properties/7/accounting/balance-sheet.pdf?as_of=2026-09-30')
  })

  it('reports whether the books agree with the folios', async () => {
    const w = await mountView(ReconciliationView)
    await flushPromises()
    expect(w.find('[data-testid=reconciled]').exists()).toBe(true)
    expect(w.get('[data-testid=control-GUEST_LEDGER]').text()).toContain('700')
    const bad = await mountView(ReconciliationView, ['accounting.view'], '', {
      '/reconciliation': { ...(answers['/reconciliation'] as object), reconciled: false, pending_days: 2, includes_open_day: true },
    })
    await flushPromises()
    expect(bad.find('[data-testid=not-reconciled]').exists()).toBe(true)
    expect(bad.get('[data-testid=pending]').text()).toContain('2 closed business day')
    expect(bad.find('[data-testid=open-day]').exists()).toBe(true)
  })

  it('needs accounting.view', async () => {
    for (const component of [TrialBalanceView, GeneralLedgerView, ReconciliationView]) {
      const w = await mountView(component, [])
      await flushPromises()
      expect(w.find('[data-testid=no-access]').exists()).toBe(true)
      expect(GET).not.toHaveBeenCalled()
    }
  })
})
