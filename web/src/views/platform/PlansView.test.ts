import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import PlansView from './PlansView.vue'

let GET = vi.fn()
vi.mock('@/api/platformClient', () => ({ platformApi: { GET: (...a: unknown[]) => GET(...a) } }))

const plan = (o: Record<string, unknown>) => ({
  id: 1,
  code: 'BASIC',
  name: 'Basic',
  modules: ['HOUSEKEEPING'],
  limits: { max_properties: 1, max_rooms: 30, max_users: 5 },
  price: '1500000.000',
  currency_code: 'IDR',
  billing_period_months: 1,
  trial_days: 14,
  is_active: true,
  is_default: true,
  tenant_count: 3,
  ...o,
})
const PLANS = [
  plan({}),
  plan({ id: 2, code: 'PRO', name: 'Pro', modules: ['HOUSEKEEPING', 'ACCOUNTING', 'POS'], limits: { max_properties: 3, max_rooms: 120, max_users: 30 }, price: '7500000', billing_period_months: 3, trial_days: 0, is_default: false, tenant_count: 1 }),
  plan({ id: 3, code: 'LEGACY', name: 'Legacy', modules: ['HOUSEKEEPING', 'ACCOUNTING', 'POS', 'INVENTORY', 'BANQUET'], limits: { max_properties: null, max_rooms: null, max_users: null }, price: '0', trial_days: 0, is_active: false, is_default: false, tenant_count: 0 }),
]

async function mountView() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/platform/plans', component: PlansView }, { path: '/platform/plans/new', component: { template: '<div />' } }] })
  await router.push('/platform/plans')
  const wrapper = mount(PlansView, { global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}

describe('PlansView', () => {
  beforeEach(() => {
    GET = vi.fn(async () => ({ data: { items: PLANS } }))
    setLocale('en')
  })

  it('shows a skeleton while it loads', async () => {
    GET = vi.fn(() => new Promise(() => {}))
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: PlansView }, { path: '/platform/plans/new', component: { template: '<div />' } }] })
    await router.push('/')
    const w = mount(PlansView, { global: { plugins: [router] } })
    expect(w.find('[data-testid=plans-loading]').exists()).toBe(true)
    expect(w.get('h1').text()).toBe('Plans')
  })

  it('lists a card per plan with price, period, limits, modules, trial and the tenants link', async () => {
    const w = await mountView()
    expect(GET).toHaveBeenCalledWith('/api/v1/platform/plans')
    const basic = w.get('[data-testid=plan-BASIC]')
    expect(basic.get('h2').text()).toBe('Basic')
    expect(basic.get('[data-testid=plan-price]').text()).toBe('IDR 1,500,000')
    expect(basic.text()).toContain('/ month')
    expect(basic.get('[data-testid=limit-rooms]').text()).toContain('30')
    expect(basic.text()).toContain('Core')
    expect(basic.text()).toContain('Housekeeping')
    expect(basic.get('[data-testid=plan-trial]').text()).toBe('Trial 14 days')
    expect(basic.get('[data-testid=badge-default]').text()).toBe('Default')
    expect(basic.find('[data-testid=badge-not-for-sale]').exists()).toBe(false)
    expect(basic.get('[data-testid=plan-tenants]').text()).toBe('3 tenants')
    expect(basic.get('[data-testid=plan-tenants]').attributes('href')).toBe('/platform/tenants?plan=BASIC')
    expect(basic.get('[data-testid=plan-edit]').attributes('href')).toBe('/platform/plans/BASIC')

    const pro = w.get('[data-testid=plan-PRO]')
    expect(pro.text()).toContain('/ 3 months')
    expect(pro.get('[data-testid=plan-trial]').text()).toBe('No trial')
    expect(pro.get('[data-testid=plan-tenants]').text()).toBe('1 tenant')
    expect(pro.find('[data-testid=badge-default]').exists()).toBe(false)
  })

  it('writes an unlimited limit as a word, shows "All modules" and "Not for sale"', async () => {
    const w = await mountView()
    const legacy = w.get('[data-testid=plan-LEGACY]')
    for (const k of ['properties', 'rooms', 'users']) expect(legacy.get(`[data-testid=limit-${k}]`).text()).toContain('Unlimited')
    expect(legacy.text()).toContain('All modules')
    expect(legacy.get('[data-testid=badge-not-for-sale]').text()).toBe('Not for sale')
    expect(legacy.get('[data-testid=plan-trial]').text()).toBe('Tenants from before subscriptions')
  })

  it('has the New plan link in the header', async () => {
    const w = await mountView()
    expect(w.get('[data-testid=new-plan]').attributes('href')).toBe('/platform/plans/new')
  })

  it('shows the empty state when there are no plans', async () => {
    GET = vi.fn(async () => ({ data: { items: [] } }))
    const w = await mountView()
    expect(w.text()).toContain('There are no plans yet')
    expect(w.find('[data-testid=plans-list]').exists()).toBe(false)
  })

  it('shows the error of the server in place of the list', async () => {
    GET = vi.fn(async () => {
      throw new ApiError({ type: 't', title: 'x', status: 500, code: 'INTERNAL', detail: 'The plans could not be read.' })
    })
    const w = await mountView()
    expect(w.get('[role=alert]').text()).toContain('The plans could not be read.')
    expect(w.find('[data-testid=plans-list]').exists()).toBe(false)
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    const w = await mountView()
    expect(w.get('h1').text()).toBe('Paket')
    expect(w.get('[data-testid=plan-LEGACY]').get('[data-testid=badge-not-for-sale]').text()).toBe('Tidak dijual')
    expect(w.get('[data-testid=plan-BASIC] [data-testid=plan-edit]').text()).toBe('Ubah')
  })
})
