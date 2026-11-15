import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { i18n, setLocale } from '@/i18n'
import { usePlatformAuthStore } from '@/stores/platformAuth'
import PlatformLayout from './PlatformLayout.vue'

const Page = { template: '<h1>page</h1>' }

async function mountAt(path: string) {
  setActivePinia(createPinia())
  usePlatformAuthStore().operator = { id: 1, email: 'rina@kamarapms.example', full_name: 'Rina Wijaya', is_active: true, totp_enrolled: true, last_login_at: null, recovery_codes_left: 10 }
  const router = createRouter({
    history: createMemoryHistory(),
    routes: ['/platform/tenants', '/platform/tenants/:code', '/platform/plans', '/platform/plans/new', '/platform/invoices'].map((p) => ({ path: p, component: Page })),
  })
  await router.push(path)
  const w = mount(PlatformLayout, { global: { plugins: [router, i18n] } })
  await flushPromises()
  return w
}

describe('PlatformLayout', () => {
  beforeEach(async () => {
    await setLocale('en')
  })

  it('has the Platform pill, the three places of the console and the operator, and no hotel menu', async () => {
    const w = await mountAt('/platform/tenants')
    expect(w.get('[data-testid=platform-pill]').text()).toBe('Platform')
    expect(w.get('nav').attributes('aria-label')).toBe('Platform console')
    expect(w.findAll('nav a').map((a) => a.text())).toEqual(['Tenants', 'Plans', 'Invoices'])
    expect(w.get('[data-testid=platform-operator]').text()).toBe('Rina Wijaya · operator')
    expect(w.find('[data-testid=topbar]').exists()).toBe(false)
  })

  it('marks the place the person is in with aria-current, also for a page under it', async () => {
    const current = async (path: string) => (await mountAt(path)).findAll('nav a[aria-current=page]').map((a) => a.text())
    expect(await current('/platform/tenants')).toEqual(['Tenants'])
    expect(await current('/platform/tenants/ABC')).toEqual(['Tenants'])
    expect(await current('/platform/plans/new')).toEqual(['Plans'])
    expect(await current('/platform/invoices')).toEqual(['Invoices'])
  })

  it('tells the app to sign out', async () => {
    const w = await mountAt('/platform/plans')
    await w.get('[data-testid=platform-sign-out]').trigger('click')
    expect(w.emitted('signOut')).toHaveLength(1)
  })

  it('speaks Indonesian with the words of the glossary', async () => {
    await setLocale('id')
    const w = await mountAt('/platform/tenants')
    expect(w.findAll('nav a').map((a) => a.text())).toEqual(['Penyewa', 'Paket', 'Invoice'])
    expect(w.get('[data-testid=platform-sign-out]').text()).toBe('Keluar')
    await setLocale('en')
  })
})
