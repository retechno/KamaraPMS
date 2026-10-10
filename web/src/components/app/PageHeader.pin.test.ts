import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { resetNavPins } from '@/composables/useNavPins'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import PageHeader from './PageHeader.vue'

let wrapper: VueWrapper | null = null

async function mountHeader(path: string, permissions: string[] = ['reservation.read']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 1, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  await router.push(path)
  wrapper = mount(PageHeader, { props: { title: 'A page' }, global: { plugins: [pinia, router] } })
  await flushPromises()
  return wrapper
}

describe('PageHeader: the pin', () => {
  beforeEach(() => {
    localStorage.clear()
    resetNavPins()
    setLocale('en')
  })
  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
  })

  it('pins the page to the menu and takes it off again', async () => {
    const w = await mountHeader('/guests')
    const pin = w.get('[data-testid=pin-page]')
    expect(pin.attributes('aria-pressed')).toBe('false')
    expect(pin.attributes('aria-label')).toBe('Pin this page to the menu')
    await pin.trigger('click')
    expect(w.get('[data-testid=pin-page]').attributes('aria-pressed')).toBe('true')
    expect(w.get('[data-testid=pin-page]').attributes('aria-label')).toBe('Unpin this page')
    expect(JSON.parse(localStorage.getItem('pms.nav.1')!).pins).toContain('guests')
    await w.get('[data-testid=pin-page]').trigger('click')
    expect(w.get('[data-testid=pin-page]').attributes('aria-pressed')).toBe('false')
    expect(JSON.parse(localStorage.getItem('pms.nav.1')!).pins).not.toContain('guests')
  })

  it('shows a page that is pinned already (a default one) as pinned', async () => {
    const w = await mountHeader('/room-status')
    expect(w.get('[data-testid=pin-page]').attributes('aria-pressed')).toBe('true')
  })

  it('has no pin on a page below a page of the menu (a reservation, a stay), nor on a page that is not in the menu', async () => {
    expect((await mountHeader('/reservations/12')).find('[data-testid=pin-page]').exists()).toBe(false)
    wrapper!.unmount()
    expect((await mountHeader('/stays/3')).find('[data-testid=pin-page]').exists()).toBe(false)
    wrapper!.unmount()
    expect((await mountHeader('/no/such/page')).find('[data-testid=pin-page]').exists()).toBe(false)
  })

  it('has no pin on a page the person may not see in the menu (a page for administrators)', async () => {
    expect((await mountHeader('/setup/users')).find('[data-testid=pin-page]').exists()).toBe(false)
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    const w = await mountHeader('/guests')
    expect(w.get('[data-testid=pin-page]').attributes('aria-label')).toBe('Sematkan halaman ini di menu')
  })

  it('is just a header, with no pin and no error, where there is no router (a page shown on its own)', () => {
    const w = mount(PageHeader, { props: { title: 'Alone' } })
    expect(w.get('h1').text()).toBe('Alone')
    expect(w.find('[data-testid=pin-page]').exists()).toBe(false)
  })
})
