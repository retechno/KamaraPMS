import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { currentLocale, setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import TopBar from './TopBar.vue'

vi.mock('@/api/client', () => ({ api: { GET: vi.fn() } }))

const clock = (over: object = {}) => ({
  business_date: '2026-09-30', property_local_time: '2026-09-30T20:15:00+07:00', server_time: '2026-09-30T13:15:00Z', timezone: 'Asia/Jakarta',
  night_audit_allowed: false, night_audit_overdue: false, ...over,
})

const mounted: VueWrapper[] = []
const body = <T extends Element = HTMLElement>(sel: string): T | null => document.body.querySelector<T>(sel)

async function mountBar(opts: { path?: string; mode?: 'full' | 'rail' | 'drawer'; collapsed?: boolean; canCollapse?: boolean; clock?: object | null; properties?: object[] } = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 1, email: 'rina@hotel.test', full_name: 'Rina Front', is_tenant_admin: false }, properties: [] } as never
  const property = usePropertyStore()
  property.loaded = true
  property.properties = (opts.properties ?? [{ id: 7, code: 'BALI', name: 'Bali' }, { id: 8, code: 'LOMBOK', name: 'Lombok' }]) as never
  property.currentId = 7
  property.clock = (opts.clock === undefined ? clock() : opts.clock) as never
  const select = vi.spyOn(property, 'select').mockResolvedValue()
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' }, meta: { title: 'Stay' } }] })
  await router.push(opts.path ?? '/arrivals')
  const w = mount(TopBar, {
    attachTo: document.body,
    props: { mode: opts.mode ?? 'full', collapsed: opts.collapsed ?? false, canCollapse: opts.canCollapse ?? (opts.mode ?? 'full') === 'full' },
    global: { plugins: [pinia, router] },
  })
  mounted.push(w)
  await flushPromises()
  return { w, select }
}

/** reka-ui opens a menu on a press of its button and on Enter; jsdom cannot make a pointer event with a button, so the tests press Enter. */
async function openUserMenu(w: VueWrapper): Promise<void> {
  await w.get('[data-testid=user-menu]').trigger('keydown', { key: 'Enter' })
  await flushPromises()
}

describe('TopBar', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.removeAttribute('data-theme')
    setLocale('en')
  })
  afterEach(() => {
    mounted.splice(0).forEach((w) => w.unmount())
    document.body.innerHTML = ''
    vi.restoreAllMocks()
  })

  it('shows where the person is: the section and the page', async () => {
    const { w } = await mountBar({ path: '/arrivals' })
    expect(w.get('[data-testid=breadcrumb]').text()).toBe('Front desk/Arrivals')
  })

  it('shows the route title for a page outside the menu', async () => {
    const { w } = await mountBar({ path: '/stays/4' })
    expect(w.get('[data-testid=breadcrumb]').text()).toBe('Stay')
  })

  it('is one row: no wrapping, a fixed height', async () => {
    const { w } = await mountBar()
    const classes = w.get('[data-testid=topbar]').classes()
    expect(classes).toContain('h-14')
    expect(classes).not.toContain('flex-wrap')
  })

  describe('the property', () => {
    it('is chosen in a select that shows the code first, cuts a long name with an ellipsis and holds the whole name in its title', async () => {
      const { w, select } = await mountBar({ properties: [{ id: 7, code: 'BALI', name: 'Kamara Ubud Boutique Resort and Spa (review UI)' }, { id: 8, code: 'LOMBOK', name: 'Lombok' }] })
      const sel = w.get('[data-testid=property-switcher]')
      expect((sel.element as HTMLSelectElement).value).toBe('7')
      expect(sel.attributes('title')).toBe('BALI · Kamara Ubud Boutique Resort and Spa (review UI)')
      expect(sel.classes()).toEqual(expect.arrayContaining(['text-ellipsis', 'overflow-hidden', 'whitespace-nowrap']))
      expect(sel.findAll('option').map((o) => o.text())[0]).toBe('BALI · Kamara Ubud Boutique Resort and Spa (review UI)') // the code is the first thing in it
      await sel.setValue('8')
      expect(select).toHaveBeenCalledWith(8)
    })

    it('invites to create one when there is none', async () => {
      const { w } = await mountBar({ properties: [], clock: null })
      expect(w.find('[data-testid=property-switcher]').exists()).toBe(false)
      expect(w.text()).toContain('No property yet')
    })
  })

  describe('the business day', () => {
    it('is one chip that links to night audit, with the date and the local time when the day has not turned yet', async () => {
      const { w } = await mountBar()
      const chip = w.get('[data-testid=business-date]')
      expect(chip.attributes('href')).toBe('/night-audit')
      expect(chip.text()).toContain('Business date')
      expect(chip.text()).toContain('30 Sep 2026')
      const late = await mountBar({ clock: clock({ property_local_time: '2026-10-01T01:10:00+07:00' }) })
      expect(late.w.get('[data-testid=business-date]').text()).toContain('local')
    })

    it('says whether night audit is ready, overdue or later, inside the chip', async () => {
      const later = (await mountBar()).w.get('[data-testid=night-audit-status]')
      expect(later.text()).toBe('Night audit later')
      expect(later.attributes('data-state')).toBe('later')
      const ready = (await mountBar({ clock: clock({ night_audit_allowed: true }) })).w.get('[data-testid=night-audit-status]')
      expect(ready.text()).toBe('Night audit ready')
      const overdue = await mountBar({ clock: clock({ night_audit_allowed: true, night_audit_overdue: true }) })
      expect(overdue.w.get('[data-testid=night-audit-status]').text()).toBe('Night audit overdue')
      expect(overdue.w.get('[data-testid=business-date]').classes()).toContain('text-status-noshow') // the colour of a warning
    })

    it('keeps "overdue" in words at every width, and lets "ready" and "later" shrink to a dot on a narrow bar', async () => {
      for (const mode of ['full', 'rail', 'drawer'] as const) {
        const overdue = (await mountBar({ mode, clock: clock({ night_audit_overdue: true }) })).w.get('[data-testid=night-audit-status]')
        expect(overdue.classes(), `overdue in ${mode}`).not.toContain('sr-only')
        expect(overdue.classes(), `overdue in ${mode}`).not.toContain('hidden')
        const ready = (await mountBar({ mode, clock: clock({ night_audit_allowed: true }) })).w.get('[data-testid=night-audit-status]')
        expect(ready.classes(), `ready in ${mode}`).toContain('sr-only') // seen only by a screen reader below the wide layout
        expect(ready.classes(), `ready in ${mode}`).toContain('xl:not-sr-only')
      }
    })

    it('is short on a phone: the day without its year, and "Overdue" in words when night audit is late', async () => {
      const { w } = await mountBar({ mode: 'drawer' })
      expect(w.get('[data-testid=business-date]').text()).toContain('30 Sep')
      expect(w.get('[data-testid=business-date]').text()).not.toContain('2026')
      expect(w.get('[data-testid=business-date]').text()).not.toContain('Business date')
      const late = await mountBar({ mode: 'drawer', clock: clock({ night_audit_overdue: true }) })
      expect(late.w.get('[data-testid=night-audit-status]').text()).toBe('Overdue')
    })
  })

  describe('the menu of the person', () => {
    it('is closed until it is opened, and shows the name, the e-mail and the account', async () => {
      const { w } = await mountBar()
      expect(body('[data-testid=user-name]')).toBeNull()
      expect(w.get('[data-testid=user-menu]').text()).toBe('RF') // the initials
      await openUserMenu(w)
      expect(body('[data-testid=user-name]')?.textContent).toBe('Rina Front')
      expect(body('[data-testid=user-email]')?.textContent).toBe('rina@hotel.test')
      expect(body('[data-testid=account-link]')?.getAttribute('href')).toBe('/account')
    })

    it('switches the language and remembers it', async () => {
      const { w } = await mountBar()
      await openUserMenu(w)
      expect(body('[data-testid=language-en]')?.getAttribute('aria-checked')).toBe('true')
      body('[data-testid=language-id]')!.click()
      await flushPromises()
      expect(currentLocale()).toBe('id')
      expect(localStorage.getItem('pms.locale')).toBe('id')
      expect(w.get('[data-testid=breadcrumb]').text()).toBe('Front Desk/Kedatangan')
      expect(w.get('[data-testid=business-date]').text()).toContain('Tanggal bisnis')
    })

    it('chooses the theme: light, dark or the system', async () => {
      const { w } = await mountBar()
      await openUserMenu(w)
      expect(body('[data-testid=menu-theme-system]')?.getAttribute('aria-checked')).toBe('true')
      body('[data-testid=menu-theme-dark]')!.click()
      await flushPromises()
      expect(document.documentElement.getAttribute('data-theme')).toBe('dark')
      expect(localStorage.getItem('pms.theme')).toBe('dark')
    })

    it('signs out', async () => {
      const { w } = await mountBar()
      await openUserMenu(w)
      body('[data-testid=sign-out]')!.click()
      await flushPromises()
      expect(w.emitted('signOut')).toHaveLength(1)
    })
  })

  describe('the finder', () => {
    it('is a wide field that opens the quick finder, and tells the shortcut', async () => {
      const { w } = await mountBar()
      const finder = w.get('[data-testid=open-search]')
      expect(finder.text()).toContain('Search pages, guests, reservations')
      expect(w.get('[data-testid=search-shortcut]').text()).toBe('Ctrl K')
      await finder.trigger('click')
      expect(w.emitted('search')).toHaveLength(1)
    })

    it('tells ⌘K on a Mac', async () => {
      vi.spyOn(navigator, 'platform', 'get').mockReturnValue('MacIntel')
      const { w } = await mountBar()
      expect(w.get('[data-testid=search-shortcut]').text()).toBe('⌘K')
    })

    it('is an icon on a phone', async () => {
      const { w } = await mountBar({ mode: 'drawer' })
      expect(w.get('[data-testid=open-search]').text()).toBe('')
      expect(w.find('[data-testid=search-shortcut]').exists()).toBe(false)
      await w.get('[data-testid=open-search]').trigger('click')
      expect(w.emitted('search')).toHaveLength(1)
    })
  })

  describe('a phone', () => {
    it('shows the menu button and the title of the page, and leaves the property to the drawer', async () => {
      const { w } = await mountBar({ mode: 'drawer', path: '/arrivals' })
      expect(w.get('[data-testid=page-title]').text()).toBe('Arrivals')
      expect(w.find('[data-testid=breadcrumb]').exists()).toBe(false)
      expect(w.find('[data-testid=property-switcher]').exists()).toBe(false)
      await w.get('[data-testid=open-menu]').trigger('click')
      expect(w.emitted('menu')).toHaveLength(1)
    })
  })

  it('offers the menu button on a tablet and a phone, the collapse toggle on a desktop', async () => {
    const phone = await mountBar({ mode: 'drawer' })
    expect(phone.w.find('[data-testid=toggle-sidebar]').exists()).toBe(false)
    expect((await mountBar({ mode: 'rail' })).w.find('[data-testid=open-menu]').exists()).toBe(true)

    // a wide window collapsed to the rail still offers the toggle, to open it again
    const collapsedDesk = await mountBar({ mode: 'rail', collapsed: true, canCollapse: true })
    expect(collapsedDesk.w.find('[data-testid=open-menu]').exists()).toBe(false)
    expect(collapsedDesk.w.find('[data-testid=toggle-sidebar]').exists()).toBe(true)

    const desk = await mountBar({ mode: 'full' })
    expect(desk.w.find('[data-testid=open-menu]').exists()).toBe(false)
    await desk.w.get('[data-testid=toggle-sidebar]').trigger('click')
    expect(desk.w.emitted('toggleSidebar')).toHaveLength(1)
  })
})
