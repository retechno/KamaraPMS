import { flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import NotFoundView from './NotFoundView.vue'

async function mountPage() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div />' } }, { path: '/:p(.*)*', component: NotFoundView }] })
  await router.push('/nowhere')
  return { w: mount(NotFoundView, { global: { plugins: [createPinia(), router] } }), router }
}

describe('NotFoundView', () => {
  beforeEach(() => setLocale('en'))

  it('has a page header with its own title, and an icon of its own', async () => {
    const { w } = await mountPage()
    expect(w.findAll('[data-slot=page-header]')).toHaveLength(1)
    expect(w.get('h1').text()).toBe('Page not found')
    expect(w.find('[data-testid=not-found] svg').exists()).toBe(true)
    expect(w.get('[data-testid=not-found]').text()).toContain('There is nothing at this address.')
  })

  it('has a main button back to the dashboard, which goes to the home page', async () => {
    const { w, router } = await mountPage()
    const back = w.get('a[data-testid=empty-action]')
    expect(back.text()).toBe('Back to Today')
    expect(back.classes().join(' ')).toContain('bg-primary')
    await back.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/')
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    const { w } = await mountPage()
    expect(w.get('h1').text()).toBe('Halaman tidak ditemukan')
    expect(w.get('a[data-testid=empty-action]').text()).toBe('Kembali ke Hari Ini')
  })
})
