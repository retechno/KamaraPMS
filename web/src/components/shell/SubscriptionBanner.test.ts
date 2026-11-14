import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { useSubscriptionStore } from '@/stores/subscription'
import SubscriptionBanner from './SubscriptionBanner.vue'

vi.mock('@/api/client', () => ({ api: { GET: vi.fn() } }))

const base = {
  plan_code: 'STD', plan_name: 'Standard', modules: ['CORE'], status: 'ACTIVE', access: 'FULL', started_on: '2026-01-01', paid_through: '2026-12-31', grace_ends_on: '2027-01-14', days_left: 40,
  limits: { max_properties: null, max_rooms: null, max_users: null }, usage: { properties: 1, rooms: 10, users: 2 }, is_trial_ending: false, billing_contact: null,
}

async function mountBanner(summary: object | null, admin = true) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 1, email: 'a@b.c', is_tenant_admin: admin }, tenant: { id: 1, code: 'ABC', name: 'ABC' }, properties: [] } as never
  useSubscriptionStore().summary = summary as never
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  const wrapper = mount(SubscriptionBanner, { global: { plugins: [pinia, router] } })
  await flushPromises()
  return wrapper
}

describe('SubscriptionBanner', () => {
  beforeEach(() => {
    setLocale('en')
    try {
      localStorage.clear()
    } catch {
      // nothing to clear
    }
  })

  it('says nothing before the summary is read, or while the subscription is in order', async () => {
    expect((await mountBanner(null)).find('[data-testid=subscription-banner]').exists()).toBe(false)
    expect((await mountBanner(base)).find('[data-testid=subscription-banner]').exists()).toBe(false)
  })

  it('warns everyone about a trial that ends within the week, and only admins get the link', async () => {
    const trial = { ...base, status: 'TRIAL', paid_through: '2026-11-20', days_left: 5, is_trial_ending: true }
    const admin = await mountBanner(trial, true)
    expect(admin.get('[data-testid=subscription-banner]').text()).toContain('The trial ends on')
    expect(admin.get('[data-testid=subscription-banner]').text()).toContain('5 days left')
    expect(admin.get('a').attributes('href')).toBe('/setup/subscription')
    expect(admin.get('.alert').classes()).toContain('info')
    const other = await mountBanner(trial, false)
    expect(other.get('[data-testid=subscription-banner]').text()).toContain('The trial ends on')
    expect(other.find('a').exists()).toBe(false)
  })

  it('puts the trial warning away for the rest of the day, and keeps the others', async () => {
    const trial = { ...base, status: 'TRIAL', days_left: 3, is_trial_ending: true }
    const wrapper = await mountBanner(trial)
    await wrapper.get('[data-testid=subscription-banner-dismiss]').trigger('click')
    expect(wrapper.find('[data-testid=subscription-banner]').exists()).toBe(false)
    expect((await mountBanner(trial)).find('[data-testid=subscription-banner]').exists()).toBe(false) // remembered
    expect((await mountBanner({ ...trial, days_left: 2 })).find('[data-testid=subscription-banner]').exists()).toBe(true) // tomorrow it is back

    const grace = await mountBanner({ ...base, access: 'GRACE', days_left: 9 })
    expect(grace.find('[data-testid=subscription-banner-dismiss]').exists()).toBe(false)
  })

  it('still works when the storage throws', async () => {
    const get = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    const set = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    const wrapper = await mountBanner({ ...base, status: 'TRIAL', days_left: 3, is_trial_ending: true })
    expect(wrapper.find('[data-testid=subscription-banner]').exists()).toBe(true)
    await wrapper.get('[data-testid=subscription-banner-dismiss]').trigger('click')
    expect(wrapper.find('[data-testid=subscription-banner]').exists()).toBe(false)
    get.mockRestore()
    set.mockRestore()
  })

  it('tells an admin in the grace period to renew, and the others to ask the admin', async () => {
    const grace = { ...base, access: 'GRACE', paid_through: '2026-11-10', grace_ends_on: '2026-11-24', days_left: 9 }
    const admin = await mountBanner(grace, true)
    const text = admin.get('[data-testid=subscription-banner]').text()
    expect(text).toContain('The subscription ended on')
    expect(text).toContain('9 days of grace left')
    expect(admin.get('.alert').attributes('role')).toBe('status')
    expect(admin.find('a').exists()).toBe(true)
    const other = await mountBanner(grace, false)
    expect(other.get('[data-testid=subscription-banner]').text()).toContain('must be renewed within 9 days. Ask your hotel administrator.')
    expect(other.find('a').exists()).toBe(false)
  })

  it('shows read only to everyone as an alert that stays', async () => {
    const ro = { ...base, access: 'READ_ONLY', days_left: 0 }
    const admin = await mountBanner(ro, true)
    expect(admin.get('[role=alert]').text()).toContain('The subscription has ended.')
    expect(admin.find('a').exists()).toBe(true)
    const other = await mountBanner(ro, false)
    expect(other.get('[role=alert]').text()).toContain('not changed until it is renewed')
    expect(other.find('a').exists()).toBe(false)
    expect(other.find('[data-testid=subscription-banner-dismiss]').exists()).toBe(false)
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    const wrapper = await mountBanner({ ...base, access: 'READ_ONLY', days_left: 0 })
    expect(wrapper.text()).toContain('Langganan telah berakhir')
  })
})
