import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
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

async function mountBar(opts: { path?: string; mode?: 'full' | 'rail' | 'drawer'; collapsed?: boolean; canCollapse?: boolean; clock?: object | null; properties?: object[] } = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 1, email: 'a@b.c', full_name: 'Rina Front', is_tenant_admin: false }, properties: [] } as never
  const property = usePropertyStore()
  property.loaded = true
  property.properties = (opts.properties ?? [{ id: 7, code: 'BALI', name: 'Bali' }, { id: 8, code: 'LOMBOK', name: 'Lombok' }]) as never
  property.currentId = 7
  property.clock = (opts.clock === undefined ? clock() : opts.clock) as never
  const select = vi.spyOn(property, 'select').mockResolvedValue()
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' }, meta: { title: 'Stay' } }] })
  await router.push(opts.path ?? '/arrivals')
  const w = mount(TopBar, { props: { mode: opts.mode ?? 'full', collapsed: opts.collapsed ?? false, canCollapse: opts.canCollapse ?? (opts.mode ?? 'full') === 'full' }, global: { plugins: [pinia, router] } })
  await flushPromises()
  return { w, select }
}

describe('TopBar', () => {
  beforeEach(() => {
    localStorage.clear()
    setLocale('en')
  })

  it('shows where the person is: the section and the page', async () => {
    const { w } = await mountBar({ path: '/arrivals' })
    expect(w.get('[data-testid=breadcrumb]').text()).toBe('Front desk/Arrivals')
  })

  it('shows the route title for a page outside the menu', async () => {
    const { w } = await mountBar({ path: '/stays/4' })
    expect(w.get('[data-testid=breadcrumb]').text()).toBe('Stay')
  })

  it('switches the property', async () => {
    const { w, select } = await mountBar()
    const sel = w.get('[data-testid=property-switcher]')
    expect((sel.element as HTMLSelectElement).value).toBe('7')
    await sel.setValue('8')
    expect(select).toHaveBeenCalledWith(8)
  })

  it('shows the business date and the local time when the day has not turned yet', async () => {
    const { w } = await mountBar()
    const chip = w.get('[data-testid=business-date]')
    expect(chip.text()).toContain('Business date')
    expect(chip.text()).toContain('30 Sep 2026')
    const late = await mountBar({ clock: clock({ property_local_time: '2026-10-01T01:10:00+07:00' }) })
    expect(late.w.get('[data-testid=business-date]').text()).toContain('local')
  })

  it('says whether night audit is ready, overdue or later', async () => {
    expect((await mountBar()).w.get('[data-testid=night-audit-status]').text()).toBe('Night audit later')
    expect((await mountBar({ clock: clock({ night_audit_allowed: true }) })).w.get('[data-testid=night-audit-status]').text()).toBe('Night audit ready')
    const overdue = await mountBar({ clock: clock({ night_audit_allowed: true, night_audit_overdue: true }) })
    expect(overdue.w.get('[data-testid=night-audit-status]').text()).toBe('Night audit overdue')
    expect(overdue.w.get('[data-testid=night-audit-status]').attributes('href')).toBe('/night-audit')
  })

  it('invites to create a property when there is none', async () => {
    const { w } = await mountBar({ properties: [], clock: null })
    expect(w.find('[data-testid=property-switcher]').exists()).toBe(false)
    expect(w.text()).toContain('No property yet')
  })

  it('switches the language and remembers it', async () => {
    const { w } = await mountBar()
    await w.get('[data-testid=language-switcher]').setValue('id')
    expect(currentLocale()).toBe('id')
    expect(localStorage.getItem('pms.locale')).toBe('id')
    expect(w.get('[data-testid=breadcrumb]').text()).toBe('Front desk/Kedatangan')
    expect(w.get('[data-testid=business-date]').text()).toContain('Tanggal bisnis')
  })

  it('shows the person and signs out', async () => {
    const { w } = await mountBar()
    expect(w.get('[data-testid=user-name]').text()).toBe('Rina Front')
    expect(w.get('[data-testid=user-name]').attributes('href')).toBe('/account')
    await w.get('[data-testid=sign-out]').trigger('click')
    expect(w.emitted('signOut')).toHaveLength(1)
  })

  it('opens the finder', async () => {
    const { w } = await mountBar()
    await w.get('[data-testid=open-search]').trigger('click')
    expect(w.emitted('search')).toHaveLength(1)
  })

  it('offers the menu button on a tablet and a phone, the collapse toggle on a desktop', async () => {
    const phone = await mountBar({ mode: 'drawer' })
    expect(phone.w.find('[data-testid=toggle-sidebar]').exists()).toBe(false)
    await phone.w.get('[data-testid=open-menu]').trigger('click')
    expect(phone.w.emitted('menu')).toHaveLength(1)
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
