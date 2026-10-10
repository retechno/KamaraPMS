import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import SidebarNav from './SidebarNav.vue'

async function mountNav(path = '/', admin = false, rail = false) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 1, email: 'a@b.c', is_tenant_admin: admin }, properties: [] } as never
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  await router.push(path)
  const w = mount(SidebarNav, { props: { rail }, global: { plugins: [pinia, router] } })
  await flushPromises()
  return { w, router }
}

describe('SidebarNav', () => {
  beforeEach(() => {
    localStorage.clear()
    setLocale('en')
  })

  it('shows the sections and the page the person is on', async () => {
    const { w } = await mountNav('/reservations/tape')
    expect(w.get('[data-testid=section-frontDesk]').text()).toContain('Front desk')
    expect(w.get('[data-testid=nav-tapeChart]').attributes('aria-current')).toBe('page')
    expect(w.get('[data-testid=nav-reservations]').attributes('aria-current')).toBeUndefined()
    expect(w.get('[data-testid=nav-tapeChart]').text()).toBe('Tape chart')
  })

  it('opens the daily sections and keeps the finance sections and setup closed until needed', async () => {
    const { w } = await mountNav('/')
    const shown = (id: string) => (w.get(`[data-testid=section-${id}] ul`).element as HTMLElement).style.display !== 'none'
    expect(shown('frontDesk')).toBe(true)
    expect(shown('billing')).toBe(true)
    for (const id of ['accounting', 'payables', 'tax', 'bank', 'budget', 'setup']) expect(shown(id), id).toBe(false)
  })

  it('opens the section of the page it arrives on, and only that one of the finance sections', async () => {
    const { w, router } = await mountNav('/')
    await router.push('/tax/returns')
    await flushPromises()
    expect((w.get('[data-testid=section-tax] ul').element as HTMLElement).style.display).not.toBe('none')
    expect(w.get('[data-testid=section-tax]').text()).toContain('Tax')
    expect((w.get('[data-testid=section-accounting] ul').element as HTMLElement).style.display).toBe('none')
    expect(w.get('[data-testid=nav-taxReturns]').attributes('aria-current')).toBe('page')
  })

  it('collapses and expands a section and remembers it', async () => {
    const { w } = await mountNav('/')
    const toggle = w.get('[data-testid=toggle-billing]')
    expect(toggle.attributes('aria-expanded')).toBe('true')
    await toggle.trigger('click')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    expect(JSON.parse(localStorage.getItem('pms.nav.open')!)).toMatchObject({ billing: false })
    const again = await mountNav('/')
    expect(again.w.get('[data-testid=toggle-billing]').attributes('aria-expanded')).toBe('false')
  })

  it('hides the pages for administrators from everyone else', async () => {
    const user = await mountNav('/setup/room-types', false)
    expect(user.w.find('[data-testid=nav-users]').exists()).toBe(false)
    expect(user.w.find('[data-testid=nav-roomTypes]').exists()).toBe(true)
    const admin = await mountNav('/setup/room-types', true)
    expect(admin.w.find('[data-testid=nav-users]').exists()).toBe(true)
  })

  it('follows the language', async () => {
    const { w } = await mountNav('/')
    setLocale('id')
    await flushPromises()
    expect(w.get('[data-testid=nav-arrivals]').text()).toBe('Kedatangan')
    expect(w.get('[data-testid=section-rooms]').text()).toContain('Kamar')
  })

  it('is a rail of section icons on a tablet', async () => {
    const { w } = await mountNav('/room-status', false, true)
    expect(w.find('[data-testid=section-rooms]').exists()).toBe(false)
    const rooms = w.get('[data-testid=rail-rooms]')
    expect(rooms.attributes('title')).toBe('Rooms')
    expect(rooms.attributes('href')).toBe('/room-status')
    expect(rooms.classes().join(' ')).toContain('text-primary')
  })

  it('tells the parent when a page was chosen (the drawer closes)', async () => {
    const { w } = await mountNav('/')
    await w.get('[data-testid=nav-guests]').trigger('click')
    expect(w.emitted('navigate')).toHaveLength(1)
  })
})
