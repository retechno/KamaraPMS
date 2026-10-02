import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import AppShell from './AppShell.vue'

vi.mock('@/api/client', () => ({ api: { GET: vi.fn() } }))

let mounted: VueWrapper | null = null
const original = window.innerWidth

function setWidth(w: number) {
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: w })
}

async function mountShell(width: number, clockOver: object = {}) {
  setWidth(width)
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 1, email: 'a@b.c', full_name: 'Rina', is_tenant_admin: false }, properties: [] } as never
  const property = usePropertyStore()
  property.loaded = true
  property.properties = [{ id: 7, code: 'BALI', name: 'Bali' }] as never
  property.currentId = 7
  property.clock = {
    business_date: '2026-09-30', property_local_time: '2026-09-30T20:00:00+07:00', server_time: 'x', timezone: 'Asia/Jakarta',
    night_audit_allowed: false, night_audit_overdue: false, ...clockOver,
  } as never
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  await router.push('/arrivals')
  mounted = mount(AppShell, { slots: { default: '<p data-testid="page">The page</p>' }, attachTo: document.body, global: { plugins: [pinia, router] } })
  await flushPromises()
  return { router }
}

describe('AppShell', () => {
  beforeEach(() => {
    localStorage.clear()
    setLocale('en')
  })
  afterEach(() => {
    mounted?.unmount()
    mounted = null
    document.body.innerHTML = ''
    setWidth(original)
  })

  it('frames the page with the sidebar and the top bar', async () => {
    await mountShell(1280)
    expect(mounted!.get('[data-testid=sidebar]').attributes('data-mode')).toBe('full')
    expect(mounted!.find('[data-testid=topbar]').exists()).toBe(true)
    expect(mounted!.get('main [data-testid=page]').text()).toBe('The page')
    expect(mounted!.get('[data-testid=nav-arrivals]').attributes('aria-current')).toBe('page')
  })

  it('collapses the sidebar to the rail and back, remembering the choice', async () => {
    await mountShell(1280)
    await mounted!.get('[data-testid=toggle-sidebar]').trigger('click')
    expect(mounted!.get('[data-testid=sidebar]').attributes('data-mode')).toBe('rail')
    expect(localStorage.getItem('pms.sidebar.collapsed')).toBe('1')
    mounted!.unmount()
    await mountShell(1280)
    expect(mounted!.get('[data-testid=sidebar]').attributes('data-mode')).toBe('rail')
    await mounted!.get('[data-testid=toggle-sidebar]').trigger('click')
    expect(mounted!.get('[data-testid=sidebar]').attributes('data-mode')).toBe('full')
  })

  it('is a rail on a tablet, with the full menu in a drawer', async () => {
    await mountShell(900)
    expect(mounted!.get('[data-testid=sidebar]').attributes('data-mode')).toBe('rail')
    expect(document.body.querySelector('[data-testid=drawer]')).toBeNull()
    await mounted!.get('[data-testid=open-menu]').trigger('click')
    await flushPromises()
    const drawer = document.body.querySelector('[data-testid=drawer]')!
    expect(drawer.textContent).toContain('Arrivals')
  })

  it('has no sidebar on a phone, only the drawer; choosing a page closes it', async () => {
    const { router } = await mountShell(600)
    expect(mounted!.find('[data-testid=sidebar]').exists()).toBe(false)
    await mounted!.get('[data-testid=open-menu]').trigger('click')
    await flushPromises()
    document.body.querySelector<HTMLAnchorElement>('[data-testid=nav-guests]')!.click()
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/guests')
    expect(document.body.querySelector('[data-testid=drawer]')).toBeNull()
  })

  it('opens the finder with Ctrl+K and again with Cmd+K closes it', async () => {
    await mountShell(1280)
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true }))
    await flushPromises()
    expect(document.body.querySelector('[data-testid=command-palette]')).not.toBeNull()
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'K', metaKey: true }))
    await flushPromises()
    expect(document.body.querySelector('[data-testid=command-palette]')).toBeNull()
  })

  it('opens the finder from the top bar', async () => {
    await mountShell(1280)
    await mounted!.get('[data-testid=open-search]').trigger('click')
    await flushPromises()
    expect(document.body.querySelector('[data-testid=palette-input]')).not.toBeNull()
  })

  it('warns when night audit is overdue, in the language of the person', async () => {
    await mountShell(1280, { night_audit_overdue: true })
    expect(mounted!.get('[data-testid=overdue-banner]').text()).toContain('Night audit is overdue: the business date is still 30 Sep 2026')
    setLocale('id')
    await flushPromises()
    expect(mounted!.get('[data-testid=overdue-banner]').text()).toContain('Night audit terlambat')
  })

  it('has a skip link to the content', async () => {
    await mountShell(1280)
    expect(mounted!.get('a[href="#main"]').text()).toBe('Skip to content')
    expect(mounted!.find('main#main').exists()).toBe(true)
  })

  it('asks the parent to sign out', async () => {
    await mountShell(1280)
    await mounted!.get('[data-testid=sign-out]').trigger('click')
    expect(mounted!.emitted('signOut')).toHaveLength(1)
  })
})
