import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { resetNavPins } from '@/composables/useNavPins'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import SidebarNav from './SidebarNav.vue'

let wrapper: VueWrapper | null = null

async function mountNav(opts: { path?: string; rail?: boolean; permissions?: string[]; admin?: boolean } = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = {
    user: { id: 1, email: 'a@b.c', is_tenant_admin: opts.admin ?? false },
    properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: opts.permissions ?? ['reservation.read', 'folio.read'] }],
  } as never
  usePropertyStore().currentId = 7
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  await router.push(opts.path ?? '/')
  wrapper = mount(SidebarNav, { props: { rail: opts.rail }, global: { plugins: [pinia, router] }, attachTo: document.body })
  await flushPromises()
  return { w: wrapper, router }
}

const texts = (w: VueWrapper, sel: string) => w.findAll(sel).map((e) => e.text())

describe('SidebarNav: pinned and recent pages', () => {
  beforeEach(() => {
    localStorage.clear()
    resetNavPins()
    setLocale('en')
  })
  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
    document.body.innerHTML = ''
  })

  it('puts the pinned pages on top: for a new person the dashboard, room status, tape chart and cashier', async () => {
    const { w } = await mountNav()
    const section = w.get('[data-testid=section-pinned]')
    expect(section.text()).toContain('Pinned')
    expect(texts(w, '[data-testid=section-pinned] a')).toEqual(['Dashboard', 'Room status', 'Tape chart', 'Cashier'])
    expect(w.get('[data-testid=all-menu]').text()).toBe('All menu')
    // the whole menu is still below
    expect(w.find('[data-testid=section-frontDesk]').exists()).toBe(true)
  })

  it('shows only the pinned pages the person may open', async () => {
    const { w } = await mountNav({ permissions: [] })
    expect(texts(w, '[data-testid=section-pinned] a')).toEqual(['Dashboard'])
  })

  it('leaves out of the pinned and recent pages those that are not in the menu or not for this person, without an error', async () => {
    localStorage.setItem('pms.nav.1', JSON.stringify({ pins: ['gone', 'users', 'guests'], recent: ['roles', 'gone', 'groups'] }))
    const { w } = await mountNav({ admin: false })
    expect(texts(w, '[data-testid=section-pinned] a')).toEqual(['Guests'])
    expect(texts(w, '[data-testid=section-recent] a')).toEqual(['Groups'])
  })

  it('has no pinned section when nothing is pinned, and takes a page off with its button', async () => {
    const { w } = await mountNav({ permissions: [] })
    expect(w.find('[data-testid=pinned-dashboard]').exists()).toBe(true)
    await w.get('[data-testid=unpin-dashboard]').trigger('click')
    expect(w.find('[data-testid=section-pinned]').exists()).toBe(false)
    expect(JSON.parse(localStorage.getItem('pms.nav.1')!).pins).toEqual([])
  })

  it('highlights the current page among the pinned ones as well', async () => {
    const { w } = await mountNav({ path: '/room-status' })
    expect(w.get('[data-testid=pinned-roomStatus]').attributes('aria-current')).toBe('page')
    expect(w.get('[data-testid=pinned-dashboard]').attributes('aria-current')).toBeUndefined()
  })

  it('lists the pages opened last under the pinned ones, and can hide the list', async () => {
    const { w } = await mountNav()
    expect(w.find('[data-testid=section-recent]').exists()).toBe(false) // nothing opened yet
    localStorage.setItem('pms.nav.1', JSON.stringify({ recent: ['guests', 'groups', 'reservations'] }))
    resetNavPins()
    const again = await mountNav()
    expect(texts(again.w, '[data-testid=section-recent] a')).toEqual(['Guests', 'Groups', 'Reservations'])
    const list = () => (again.w.get('[data-testid=section-recent] ul').element as HTMLElement).style.display
    expect(list()).not.toBe('none')
    await again.w.get('[data-testid=toggle-recent]').trigger('click')
    expect(list()).toBe('none')
    expect(again.w.get('[data-testid=toggle-recent]').attributes('aria-expanded')).toBe('false')
    expect(again.w.get('[data-testid=toggle-recent]').attributes('aria-label')).toBe('Show recently opened')
    await again.w.get('[data-testid=toggle-recent]').trigger('click')
    expect(list()).not.toBe('none')
  })

  it('speaks Indonesian', async () => {
    localStorage.setItem('pms.nav.1', JSON.stringify({ recent: ['guests'] }))
    setLocale('id')
    const { w } = await mountNav()
    expect(w.get('[data-testid=section-pinned]').text()).toContain('Disematkan')
    expect(w.get('[data-testid=section-recent]').text()).toContain('Terakhir dibuka')
    expect(w.get('[data-testid=all-menu]').text()).toBe('Semua menu')
    expect(w.get('[data-testid=unpin-dashboard]').attributes('aria-label')).toBe('Lepas sematan Dasbor')
  })

  it('tells the parent when a pinned page was chosen (the drawer closes)', async () => {
    const { w } = await mountNav()
    await w.get('[data-testid=pinned-roomStatus]').trigger('click')
    expect(w.emitted('navigate')).toHaveLength(1)
  })

  describe('the rail of a tablet', () => {
    it('has a button for the pinned and recent pages that opens the menu, then the sections as icons', async () => {
      const { w } = await mountNav({ rail: true })
      const button = w.get('[data-testid=rail-pins]')
      expect(button.attributes('aria-label')).toBe('Pinned and recent pages')
      await button.trigger('click')
      expect(w.emitted('openMenu')).toHaveLength(1)
      expect(w.find('[data-testid=rail-accounting]').exists()).toBe(true) // the finance sections have their icons
      expect(w.find('[data-testid=section-pinned]').exists()).toBe(false)
    })

    it('has no such button when there is nothing to open', async () => {
      localStorage.setItem('pms.nav.1', JSON.stringify({ pins: [] }))
      const { w } = await mountNav({ rail: true })
      expect(w.find('[data-testid=rail-pins]').exists()).toBe(false)
    })
  })
})
