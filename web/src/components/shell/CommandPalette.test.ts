import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import CommandPalette from './CommandPalette.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const body = () => document.body
const $ = <T extends Element>(sel: string) => body().querySelector<T>(sel)

let mounted: VueWrapper | null = null

async function openPalette(permissions = ['guest.read', 'reservation.read'], admin = false) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 1, email: 'a@b.c', is_tenant_admin: admin }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  await router.push('/')
  mounted = mount(CommandPalette, { props: { open: true, 'onUpdate:open': (v: boolean) => void mounted?.setProps({ open: v }) }, attachTo: body(), global: { plugins: [pinia, router] } })
  await flushPromises()
  return { router }
}

async function type(text: string) {
  const input = $<HTMLInputElement>('[data-testid=palette-input]')!
  input.value = text
  input.dispatchEvent(new Event('input'))
  await flushPromises()
}

const press = async (key: string) => {
  $('[data-testid=palette-input]')!.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true }))
  await flushPromises()
}

describe('CommandPalette', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    setLocale('en')
    GET = vi.fn(async (path: string) => {
      if (path === '/api/v1/guests') return { data: { data: [{ id: 21, code: 'GST000021', first_name: 'Dewi', last_name: 'Lestari' }] } }
      return { data: { data: [{ id: 33, confirmation_number: 'RSV-0033', guest_name: 'Dewi Lestari' }] } }
    })
  })
  afterEach(() => {
    vi.useRealTimers()
    mounted?.unmount()
    mounted = null
    body().innerHTML = ''
  })

  it('lists pages first and finds one by what is typed', async () => {
    await openPalette()
    expect($('[data-testid=palette-page-dashboard]')).not.toBeNull()
    await type('depart')
    expect($('[data-testid=palette-page-departures]')).not.toBeNull()
    expect($('[data-testid=palette-page-arrivals]')).toBeNull()
  })

  it('goes to the page on Enter, after moving with the arrow keys', async () => {
    const { router } = await openPalette()
    await type('room')
    const first = $('[aria-selected=true]')!.getAttribute('data-testid')
    await press('ArrowDown')
    expect($('[aria-selected=true]')!.getAttribute('data-testid')).not.toBe(first)
    await press('Enter')
    await flushPromises()
    expect(router.currentRoute.value.path).not.toBe('/')
    expect((mounted!.props() as Record<string, unknown>).open).toBe(false)
  })

  it('looks up guests and reservations from two letters on, once typing pauses', async () => {
    const { router } = await openPalette()
    await type('de')
    expect(GET).not.toHaveBeenCalled() // waits for a pause
    await vi.advanceTimersByTimeAsync(250)
    await flushPromises()
    expect(GET).toHaveBeenCalledWith('/api/v1/guests', { params: { query: { q: 'de', property_id: 7, limit: 5 } } })
    expect(GET).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/reservations', { params: { path: { propertyId: 7 }, query: { q: 'de', limit: 5 } } })
    expect($('[data-testid=palette-guest-21]')!.textContent).toContain('Dewi Lestari')
    expect($('[data-testid=palette-res-33]')!.textContent).toContain('RSV-0033')
    $<HTMLButtonElement>('[data-testid=palette-guest-21]')!.click()
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/guests/21')
  })

  it('does not look up what the role may not read, and stays quiet when a lookup fails', async () => {
    await openPalette(['guest.read'])
    GET.mockRejectedValue(new Error('boom'))
    await type('de')
    await vi.advanceTimersByTimeAsync(250)
    await flushPromises()
    expect(GET).toHaveBeenCalledTimes(1)
    expect(GET.mock.calls[0]![0]).toBe('/api/v1/guests')
    expect($('[data-testid=palette-guest-21]')).toBeNull()
  })

  it('only offers the administrator pages to administrators', async () => {
    await openPalette([], false)
    await type('users')
    expect($('[data-testid=palette-page-users]')).toBeNull()
    mounted!.unmount()
    body().innerHTML = ''
    await openPalette([], true)
    await type('users')
    expect($('[data-testid=palette-page-users]')).not.toBeNull()
  })

  it('says so when nothing matches', async () => {
    await openPalette([])
    await type('zzzzzz')
    expect($('[data-testid=palette-empty]')!.textContent).toContain('Nothing found')
  })

  it('searches the pages in the current language', async () => {
    await openPalette([])
    setLocale('id')
    await type('kedatangan')
    expect($('[data-testid=palette-page-arrivals]')).not.toBeNull()
  })
})
