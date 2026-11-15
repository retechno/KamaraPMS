import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { i18n } from '@/i18n'
import App from './App.vue'
import { usePlatformAuthStore } from './stores/platformAuth'

vi.mock('@/api/client', () => ({ api: { GET: vi.fn().mockResolvedValue({ data: { data: [] } }), POST: vi.fn() } }))

const Page = defineComponent({ props: { name: { type: String, default: '' } }, setup: (p) => () => h('div', { 'data-testid': 'page' }, p.name) })
const Shell = defineComponent({ emits: ['signOut'], setup: (_, { slots }) => () => h('div', { 'data-testid': 'hotel-shell' }, slots.default?.()) })
const Layout = defineComponent({ emits: ['signOut'], setup: (_, { slots, emit }) => () => h('div', { 'data-testid': 'platform-frame', onClick: () => emit('signOut') }, slots.default?.()) })

async function mountAt(path: string) {
  setActivePinia(createPinia())
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/login', component: Page, meta: { public: true } },
      { path: '/', component: Page },
      { path: '/platform/login', component: Page, meta: { platform: true, public: true } },
      { path: '/platform/tenants', component: Page, meta: { platform: true } },
    ],
  })
  await router.push(path)
  const w = mount(App, { global: { plugins: [router, i18n], stubs: { AppShell: Shell, PlatformLayout: Layout } } })
  await flushPromises()
  return { w, router }
}

describe('App picks the frame of the page', () => {
  it('draws a page of the console in the console frame, and the hotel pages in the hotel shell', async () => {
    expect((await mountAt('/platform/tenants')).w.find('[data-testid=platform-frame]').exists()).toBe(true)
    expect((await mountAt('/platform/tenants')).w.find('[data-testid=hotel-shell]').exists()).toBe(false)
    const hotel = await mountAt('/')
    expect(hotel.w.find('[data-testid=hotel-shell]').exists()).toBe(true)
    expect(hotel.w.find('[data-testid=platform-frame]').exists()).toBe(false)
  })

  it('draws the console sign-in bare, with no frame at all', async () => {
    const { w } = await mountAt('/platform/login')
    expect(w.find('[data-testid=page]').exists()).toBe(true)
    expect(w.find('[data-testid=platform-frame]').exists()).toBe(false)
    expect(w.find('[data-testid=hotel-shell]').exists()).toBe(false)
  })

  it('signs the operator out and goes to the console sign-in, never the hotel one, even when the call fails', async () => {
    const { w, router } = await mountAt('/platform/tenants')
    const store = usePlatformAuthStore()
    store.status = 'authenticated'
    store.logout = vi.fn().mockRejectedValue(new Error('offline')) as never
    await w.get('[data-testid=platform-frame]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/platform/login')
  })
})
