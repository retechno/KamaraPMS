import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { NAV_PERMISSIONS } from '@/test/navAccess'
import SidebarNav from './SidebarNav.vue'

async function mountNav(path = '/', admin = false, rail = false) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 1, email: 'a@b.c', is_tenant_admin: admin }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: NAV_PERMISSIONS }] } as never
  usePropertyStore().currentId = 7
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
    expect(w.get('[data-testid=nav-guestsToday]').text()).toBe('Tamu hari ini')
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
  describe('Guests today', () => {
    it('is one item, active (with its section open) on arrivals, in-house and departures', async () => {
      for (const path of ['/arrivals', '/in-house', '/departures']) {
        const { w } = await mountNav(path)
        expect(w.find('[data-testid=nav-arrivals]').exists(), path).toBe(false)
        expect(w.find('[data-testid=nav-inHouse]').exists(), path).toBe(false)
        expect(w.find('[data-testid=nav-departures]').exists(), path).toBe(false)
        expect(w.get('[data-testid=nav-guestsToday]').attributes('aria-current'), path).toBe('page')
        expect(w.get('[data-testid=nav-guestsToday]').text()).toBe('Guests today')
        expect(w.get('[data-testid=toggle-frontDesk]').attributes('aria-expanded'), path).toBe('true')
      }
    })

    it('shows one pin for a person who pinned the old items, and does not list it as a recent page', async () => {
      localStorage.setItem('pms.nav.1', JSON.stringify({ pins: ['arrivals', 'inHouse', 'departures', 'dashboard'], recent: ['departures'] }))
      const { w } = await mountNav('/guests')
      expect(w.findAll('[data-testid^=pinned-]').map((e) => e.attributes('data-testid'))).toEqual(['pinned-guestsToday', 'pinned-dashboard'])
      expect(w.find('[data-testid=section-recent]').exists()).toBe(false) // it is pinned, so it is not "recent"
    })
  })

  describe('the sections that are closed until needed', () => {
    const shown = (w: Awaited<ReturnType<typeof mountNav>>['w'], id: string) => (w.get(`[data-testid=section-${id}] ul`).element as HTMLElement).style.display !== 'none'
    const backOffice: [string, string][] = [['accounting', '/accounting/accounts'], ['tax', '/tax/status'], ['bank', '/bank/accounts'], ['budget', '/budget'], ['assets', '/assets'], ['payables', '/payables/bills']]

    it('are closed on the first page, all six, and open when the page is inside them', async () => {
      const home = await mountNav('/')
      for (const [id] of backOffice) expect(shown(home.w, id), id).toBe(false)
      for (const [id, path] of backOffice) {
        const { w } = await mountNav(path)
        expect(shown(w, id), `${id} on ${path}`).toBe(true)
        for (const [other] of backOffice.filter(([o]) => o !== id)) expect(shown(w, other), `${other} on ${path}`).toBe(false)
      }
    })

    it('follow what the person chose: open stays open, closed stays closed', async () => {
      localStorage.setItem('pms.nav.open', JSON.stringify({ accounting: true, billing: false }))
      const { w } = await mountNav('/')
      expect(shown(w, 'accounting')).toBe(true) // a back-office section the person opened
      expect(shown(w, 'billing')).toBe(false) // a daily section the person closed
      expect(shown(w, 'tax')).toBe(false)
    })
  })

  describe('the last pages', () => {
    const order = (w: Awaited<ReturnType<typeof mountNav>>['w']) => w.findAll('[data-testid=sidebar-nav] > section').map((s) => s.attributes('data-testid'))

    it('are below every section, and the sections do not move whether there are any or not', async () => {
      const without = await mountNav('/')
      localStorage.setItem('pms.nav.1', JSON.stringify({ recent: ['guests', 'groups', 'reservations'] }))
      const withRecent = await mountNav('/')
      expect(withRecent.w.find('[data-testid=section-recent]').exists()).toBe(true)
      const sectionsOnly = (list: (string | undefined)[]) => list.filter((x) => x !== 'section-recent')
      expect(sectionsOnly(order(withRecent.w))).toEqual(order(without.w))
      expect(order(withRecent.w).at(-1)).toBe('section-recent')
      expect(order(withRecent.w).indexOf('section-recent')).toBeGreaterThan(order(withRecent.w).indexOf('section-setup'))
    })

    it('are hidden and shown with the button they had, and a pinned page stays at the top', async () => {
      localStorage.setItem('pms.nav.1', JSON.stringify({ pins: ['dashboard'], recent: ['guests', 'groups'] }))
      const { w } = await mountNav('/reservations')
      expect(order(w)[0]).toBe('section-pinned')
      expect((w.get('[data-testid=section-recent] ul').element as HTMLElement).style.display).not.toBe('none')
      await w.get('[data-testid=toggle-recent]').trigger('click')
      expect((w.get('[data-testid=section-recent] ul').element as HTMLElement).style.display).toBe('none')
    })
  })
})
