import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import StatementView from './StatementView.vue'
import TrialBalanceView from './TrialBalanceView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const YEAR = { year_start: '2026-01-01', year_end: '2026-12-31', label: 'FY2026', status: 'OPEN', months: 12, closed_months: 8, net_income: '0', closable: false, reopenable: false }
const row = { account_id: 1, code: '1110', name: 'Cash', account_type: 'ASSET', opening_debit: '0', opening_credit: '0', debit: '1', credit: '0', closing_debit: '1', closing_credit: '0' }
const trial = { from: '2026-09-01', to: '2026-09-30', rows: [row], totals: row }
const income = { from: '2026-09-01', to: '2026-09-30', net_income: '0', lines: [] }
const balance = { as_of: '2026-09-30', lines: [], total_assets: '0', total_liabilities: '0', total_equity: '0', difference: '0' }

async function setup(years: unknown[] = [YEAR], page = '/accounting/trial-balance') {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: ['accounting.view'] }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-10-10' } as never
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/fiscal-years')) return { data: { data: years } }
    if (path.endsWith('/trial-balance')) return { data: trial }
    if (path.endsWith('/income-statement')) return { data: income }
    if (path.endsWith('/balance-sheet')) return { data: balance }
    return { data: {} }
  })
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/accounting/trial-balance', name: 'accounting-trial-balance', component: { template: '<div />' } },
      { path: '/accounting/income-statement', name: 'accounting-income-statement', component: { template: '<div />' } },
      { path: '/accounting/balance-sheet', name: 'accounting-balance-sheet', component: { template: '<div />' } },
    ],
  })
  await router.push(page)
  return { plugins: [pinia, router] }
}

const lastQuery = (suffix: string) => GET.mock.calls.filter((c) => String(c[0]).endsWith(suffix)).at(-1)?.[1].params.query

describe('the date filter of the financial reports', () => {
  beforeEach(() => setLocale('en'))

  it('is filled with the period the report shows, so the inputs say what is on the screen', async () => {
    const w = mount(TrialBalanceView, { global: await setup() })
    await flushPromises()
    expect((w.get('input[name=from]').element as HTMLInputElement).value).toBe('2026-09-01')
    expect((w.get('input[name=to]').element as HTMLInputElement).value).toBe('2026-09-30')
    expect(w.get('[data-testid=range]').text()).toContain('1 Sep 2026')
  })

  it('leaves what the person typed alone when the report comes back', async () => {
    const w = mount(TrialBalanceView, { global: await setup() })
    await flushPromises()
    await w.get('input[name=from]').setValue('2026-09-05')
    await w.get('input[name=to]').setValue('')
    await w.get('[data-testid=apply]').trigger('submit')
    await flushPromises()
    expect(lastQuery('/trial-balance')).toEqual({ from: '2026-09-05', to: undefined })
    // the report named its own end, and the empty end took it
    expect((w.get('input[name=to]').element as HTMLInputElement).value).toBe('2026-09-30')
  })

  it('offers this month, last month and the fiscal year to date, counted from the business date', async () => {
    const w = mount(TrialBalanceView, { global: await setup() })
    await flushPromises()
    await w.get('[data-testid=pick-month]').trigger('click')
    await flushPromises()
    expect(lastQuery('/trial-balance')).toEqual({ from: '2026-10-01', to: '2026-10-10' })
    expect((w.get('input[name=from]').element as HTMLInputElement).value).toBe('2026-10-01')
    await w.get('[data-testid=pick-last-month]').trigger('click')
    await flushPromises()
    expect(lastQuery('/trial-balance')).toEqual({ from: '2026-09-01', to: '2026-09-30' })
    await w.get('[data-testid=pick-fiscal-year]').trigger('click')
    await flushPromises()
    expect(lastQuery('/trial-balance')).toEqual({ from: '2026-01-01', to: '2026-10-10' })
  })

  it('hides "fiscal year to date" when no fiscal year is set up, and keeps the other two', async () => {
    const w = mount(TrialBalanceView, { global: await setup([]) })
    await flushPromises()
    expect(w.find('[data-testid=pick-fiscal-year]').exists()).toBe(false)
    expect(w.find('[data-testid=pick-month]').exists()).toBe(true)
    expect(w.find('[data-testid=pick-last-month]').exists()).toBe(true)
  })

  it('hides it as well when the fiscal years cannot be read, and when none covers the business date', async () => {
    const w = mount(TrialBalanceView, { global: await setup([{ ...YEAR, year_start: '2024-01-01', year_end: '2024-12-31' }]) })
    await flushPromises()
    expect(w.find('[data-testid=pick-fiscal-year]').exists()).toBe(false)
    const global = await setup()
    GET = vi.fn(async (path: string) => {
      if (path.endsWith('/fiscal-years')) throw new Error('boom')
      return { data: trial }
    })
    const failed = mount(TrialBalanceView, { global })
    await flushPromises()
    expect(failed.find('[data-testid=pick-fiscal-year]').exists()).toBe(false)
    expect(failed.find('[data-testid=pick-month]').exists()).toBe(true)
  })

  it('fills the income statement the same way, and the balance sheet with its date', async () => {
    const g = await setup([YEAR], '/accounting/income-statement')
    const w = mount(StatementView, { global: g })
    await flushPromises()
    expect((w.get('input[name=from]').element as HTMLInputElement).value).toBe('2026-09-01')
    expect((w.get('input[name=to]').element as HTMLInputElement).value).toBe('2026-09-30')
    expect(w.find('[data-testid=pick-month]').exists()).toBe(true)
    const b = mount(StatementView, { global: await setup([YEAR], '/accounting/balance-sheet') })
    await flushPromises()
    expect((b.get('input[name=as_of]').element as HTMLInputElement).value).toBe('2026-09-30')
    expect(b.find('[data-testid=period-picks]').exists()).toBe(false) // a balance sheet is as of one date
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    const w = mount(TrialBalanceView, { global: await setup() })
    await flushPromises()
    expect(w.get('[data-testid=period-picks]').attributes('aria-label')).toBe('Periode cepat')
    expect(w.get('[data-testid=pick-month]').text()).toBe('Bulan ini')
    expect(w.get('[data-testid=pick-last-month]').text()).toBe('Bulan lalu')
    expect(w.get('[data-testid=pick-fiscal-year]').text()).toBe('Tahun buku berjalan')
  })
})
