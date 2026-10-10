import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { registerConfirmHost, useConfirmState } from '@/composables/useConfirm'
import { setLocale } from '@/i18n'
import RoleFormView from './RoleFormView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({
  api: {
    GET: (...a: unknown[]) => GET(...a),
    POST: (...a: unknown[]) => POST(...a),
    PATCH: (...a: unknown[]) => PATCH(...a),
  },
}))

const CATALOGUE = [
  { code: 'reservation.read', group: 'reservation', description: 'View reservations' },
  { code: 'reservation.create', group: 'reservation', description: 'Create reservations' },
  { code: 'reservation.cancel', group: 'reservation', description: 'Cancel reservations' },
  { code: 'billing.read', group: 'billing', description: 'View folios' },
  { code: 'billing.post', group: 'billing', description: 'Post charges' },
]

let wrapper: VueWrapper | null = null
let unregister: (() => void) | null = null

async function mountPage(id?: string, permissions: string[] = []) {
  GET = vi.fn((path: string) => {
    if (path === '/api/v1/permissions') return Promise.resolve({ data: { data: CATALOGUE } })
    return Promise.resolve({ data: { name: 'Front desk', description: 'The desk', permissions } })
  })
  const pinia = createPinia()
  setActivePinia(pinia)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/setup/roles/new', component: RoleFormView },
      { path: '/setup/roles/:id', component: RoleFormView, props: true },
      { path: '/setup/roles', component: { template: '<div>list</div>' } },
      { path: '/elsewhere', component: { template: '<div>elsewhere</div>' } },
    ],
  })
  await router.push(id ? `/setup/roles/${id}` : '/setup/roles/new')
  const App = defineComponent({ render: () => h(RouterView) })
  wrapper = mount(App, { global: { plugins: [pinia, router] }, attachTo: document.body })
  await flushPromises()
  return { w: wrapper, router }
}

const items = (w: VueWrapper, group: string) => w.get(`[data-testid=group-items-${group}]`)
const hidden = (w: VueWrapper, group: string) => (items(w, group).element as HTMLElement).style.display === 'none'
const box = (w: VueWrapper, code: string) => w.get(`[data-permission="${code}"]`)
const check = (w: VueWrapper, group: string) => w.get(`[data-testid=group-check-${group}]`)

describe('RoleFormView', () => {
  beforeEach(() => {
    setLocale('en')
    POST = vi.fn().mockResolvedValue({})
    PATCH = vi.fn().mockResolvedValue({})
  })
  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
    unregister?.()
    unregister = null
    document.body.innerHTML = ''
  })

  describe('permission groups', () => {
    it('are all closed on a new role, which has no permission selected', async () => {
      const { w } = await mountPage()
      expect(hidden(w, 'reservation')).toBe(true)
      expect(hidden(w, 'billing')).toBe(true)
      expect(w.get('[data-testid=group-toggle-reservation]').attributes('aria-expanded')).toBe('false')
    })

    it('are open when the role has a permission in them, and closed when it has none', async () => {
      const { w } = await mountPage('4', ['reservation.read', 'reservation.create'])
      expect(hidden(w, 'reservation')).toBe(false)
      expect(hidden(w, 'billing')).toBe(true)
      expect(w.get('[data-testid=group-toggle-reservation]').attributes('aria-expanded')).toBe('true')
    })

    it('are opened and closed by the person, and that choice stays while permissions are ticked', async () => {
      const { w } = await mountPage('4', ['reservation.read'])
      await w.get('[data-testid=group-toggle-reservation]').trigger('click')
      expect(hidden(w, 'reservation')).toBe(true)
      await w.get('[data-testid=group-toggle-billing]').trigger('click')
      expect(hidden(w, 'billing')).toBe(false)
      await box(w, 'billing.read').setValue(true)
      expect(hidden(w, 'billing')).toBe(false)
      expect(hidden(w, 'reservation')).toBe(true)
    })

    it('say how many are selected in each: "3 of 5"', async () => {
      const { w } = await mountPage('4', ['reservation.read', 'reservation.create'])
      expect(w.get('[data-testid=group-count-reservation]').text()).toBe('2 of 3')
      expect(w.get('[data-testid=group-count-billing]').text()).toBe('0 of 2')
      await box(w, 'reservation.cancel').setValue(true)
      expect(w.get('[data-testid=group-count-reservation]').text()).toBe('3 of 3')
    })

    it('say it in Indonesian: "2 dari 3"', async () => {
      setLocale('id')
      const { w } = await mountPage('4', ['reservation.read', 'reservation.create'])
      expect(w.get('[data-testid=group-count-reservation]').text()).toBe('2 dari 3')
    })

    it('have a checkbox that is indeterminate when only some are selected, checked when all are, and empty when none', async () => {
      const { w } = await mountPage('4', ['reservation.read'])
      const group = check(w, 'reservation').element as HTMLInputElement
      expect(group.indeterminate).toBe(true)
      expect(group.checked).toBe(false)
      expect(check(w, 'reservation').attributes('aria-checked')).toBe('mixed')
      await box(w, 'reservation.create').setValue(true)
      await box(w, 'reservation.cancel').setValue(true)
      expect(group.indeterminate).toBe(false)
      expect(group.checked).toBe(true)
      await box(w, 'reservation.read').setValue(false)
      await box(w, 'reservation.create').setValue(false)
      await box(w, 'reservation.cancel').setValue(false)
      expect(group.indeterminate).toBe(false)
      expect(group.checked).toBe(false)
    })

    it('select all of the group from its checkbox, and clear all from it again', async () => {
      const { w } = await mountPage('4', ['reservation.read'])
      await check(w, 'reservation').setValue(true)
      for (const code of ['reservation.read', 'reservation.create', 'reservation.cancel']) expect((box(w, code).element as HTMLInputElement).checked).toBe(true)
      expect((box(w, 'billing.read').element as HTMLInputElement).checked).toBe(false)
      await check(w, 'reservation').setValue(false)
      for (const code of ['reservation.read', 'reservation.create', 'reservation.cancel']) expect((box(w, code).element as HTMLInputElement).checked).toBe(false)
    })
  })

  describe('the save bar', () => {
    it('is not there until something changes, sticks to the bottom of the screen, and goes when the change is undone', async () => {
      const { w } = await mountPage('4', ['reservation.read'])
      expect(w.find('[data-testid=save-bar]').exists()).toBe(false)
      await box(w, 'reservation.create').setValue(true)
      const bar = w.get('[data-testid=save-bar]')
      expect(bar.classes()).toEqual(expect.arrayContaining(['sticky', 'bottom-0']))
      expect(bar.text()).toContain('Unsaved changes')
      await box(w, 'reservation.create').setValue(false)
      expect(w.find('[data-testid=save-bar]').exists()).toBe(false)
    })

    it('shows for a change of the name or the description too', async () => {
      const { w } = await mountPage('4', ['reservation.read'])
      await w.get('input[name=name]').setValue('Front desk lead')
      expect(w.find('[data-testid=save-bar]').exists()).toBe(true)
      await w.get('input[name=name]').setValue('Front desk')
      expect(w.find('[data-testid=save-bar]').exists()).toBe(false)
      await w.get('input[name=description]').setValue('Changed')
      expect(w.find('[data-testid=save-bar]').exists()).toBe(true)
    })

    it('shows on a new role once there is a name or a permission', async () => {
      const { w } = await mountPage()
      expect(w.find('[data-testid=save-bar]').exists()).toBe(false)
      await w.get('input[name=name]').setValue('Night manager')
      expect(w.get('[data-testid=save-bar]').text()).toContain('Create role')
    })

    it('saves the permissions that are selected and goes to the list without asking', async () => {
      const { w, router } = await mountPage('4', ['reservation.read'])
      await box(w, 'billing.read').setValue(true)
      await w.get('[data-testid=save]').trigger('click')
      await flushPromises()
      expect(PATCH).toHaveBeenCalledTimes(1)
      expect((PATCH.mock.calls[0]?.[1] as { body: { permissions: string[] } }).body.permissions.sort()).toEqual(['billing.read', 'reservation.read'])
      expect(router.currentRoute.value.path).toBe('/setup/roles')
    })
  })

  describe('leaving with changes that are not saved', () => {
    it('asks in the confirm dialog of the app, stays on "Keep editing" and leaves on "Leave"', async () => {
      unregister = registerConfirmHost()
      const { w, router } = await mountPage('4', ['reservation.read'])
      await box(w, 'billing.read').setValue(true)
      const leaving = router.push('/elsewhere')
      await flushPromises()
      const { pending, answer } = useConfirmState()
      expect(pending.value?.options.title).toBe('Leave without saving?')
      expect(pending.value?.options.confirmLabel).toBe('Leave')
      expect(pending.value?.options.cancelLabel).toBe('Keep editing')
      answer(false)
      await leaving
      await flushPromises()
      expect(router.currentRoute.value.path).toBe('/setup/roles/4')
      expect((box(w, 'billing.read').element as HTMLInputElement).checked).toBe(true) // what was ticked is still ticked

      const again = router.push('/elsewhere')
      await flushPromises()
      expect(pending.value).not.toBeNull()
      answer(true)
      await again
      expect(router.currentRoute.value.path).toBe('/elsewhere')
    })

    it('does not ask when nothing changed', async () => {
      unregister = registerConfirmHost()
      const { router } = await mountPage('4', ['reservation.read'])
      await router.push('/elsewhere')
      await flushPromises()
      expect(useConfirmState().pending.value).toBeNull()
      expect(router.currentRoute.value.path).toBe('/elsewhere')
    })

    it('does not ask after a change is undone', async () => {
      unregister = registerConfirmHost()
      const { w, router } = await mountPage('4', ['reservation.read'])
      await box(w, 'billing.read').setValue(true)
      await box(w, 'billing.read').setValue(false)
      await router.push('/elsewhere')
      expect(useConfirmState().pending.value).toBeNull()
      expect(router.currentRoute.value.path).toBe('/elsewhere')
    })

    it('asks in Indonesian', async () => {
      setLocale('id')
      unregister = registerConfirmHost()
      const { w, router } = await mountPage('4', ['reservation.read'])
      await box(w, 'billing.read').setValue(true)
      void router.push('/elsewhere')
      await flushPromises()
      const { pending, answer } = useConfirmState()
      expect(pending.value?.options.title).toBe('Keluar tanpa menyimpan?')
      expect(pending.value?.options.cancelLabel).toBe('Lanjut mengubah')
      answer(false)
    })

    it('makes the browser ask when the tab is closed or reloaded, and only then', async () => {
      const { w } = await mountPage('4', ['reservation.read'])
      const clean = new Event('beforeunload', { cancelable: true })
      window.dispatchEvent(clean)
      expect(clean.defaultPrevented).toBe(false)
      await box(w, 'billing.read').setValue(true)
      const dirty = new Event('beforeunload', { cancelable: true })
      window.dispatchEvent(dirty)
      expect(dirty.defaultPrevented).toBe(true)
    })

    it('stops listening to the tab when the page is left', async () => {
      const { w } = await mountPage('4', ['reservation.read'])
      await box(w, 'billing.read').setValue(true)
      w.unmount()
      wrapper = null
      const after = new Event('beforeunload', { cancelable: true })
      window.dispatchEvent(after)
      expect(after.defaultPrevented).toBe(false)
    })
  })
})
