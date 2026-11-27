import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { setLocale } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import UserFormView from './UserFormView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
let PUT = vi.fn()
vi.mock('@/api/client', () => ({
  api: {
    GET: (...a: unknown[]) => GET(...a),
    POST: (...a: unknown[]) => POST(...a),
    PATCH: (...a: unknown[]) => PATCH(...a),
    PUT: (...a: unknown[]) => PUT(...a),
  },
}))

const ROLES = [{ id: 3, name: 'Front desk', permissions: [] }, { id: 4, name: 'Cashier', permissions: [] }]
const USER = {
  id: 21, email: 'dewi@hotel.example', full_name: 'Dewi', is_tenant_admin: false, is_active: true, created_at: '2026-11-01T00:00:00Z',
  grants: [{ property_id: 7, property_code: 'BALI', property_name: 'Bali', role_id: 3, role_name: 'Front desk' }],
}

let wrapper: VueWrapper | null = null

async function mountPage(id?: string) {
  GET = vi.fn((path: string) => {
    if (path === '/api/v1/roles') return Promise.resolve({ data: { data: ROLES } })
    return Promise.resolve({ data: USER })
  })
  POST = vi.fn().mockResolvedValue({ data: USER })
  PATCH = vi.fn().mockResolvedValue({ data: USER })
  PUT = vi.fn().mockResolvedValue({ data: USER })
  const pinia = createPinia()
  setActivePinia(pinia)
  const props = usePropertyStore()
  props.properties = [{ id: 7, code: 'BALI', name: 'Bali' }, { id: 8, code: 'JKT', name: 'Jakarta' }] as never
  props.loaded = true
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/setup/users/new', component: UserFormView },
      { path: '/setup/users/:id', component: UserFormView, props: true },
      { path: '/setup/users', component: { template: '<div data-testid="list">list</div>' } },
    ],
  })
  await router.push(id ? `/setup/users/${id}` : '/setup/users/new')
  const App = defineComponent({ render: () => h(RouterView) })
  wrapper = mount(App, { global: { plugins: [pinia, router] }, attachTo: document.body })
  await flushPromises()
  return { w: wrapper, router }
}

const selects = (w: VueWrapper, label: string) => w.get(`select[aria-label="${label}"]`)

describe('UserFormView', () => {
  beforeEach(() => setLocale('en'))
  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
    document.body.innerHTML = ''
  })

  // KD-20: a row of access with no property or no role was dropped on save without a word, and the staff member was saved without the access meant.
  it('refuses to create a user while an access row lacks its property or role, and says so under the row', async () => {
    const { w, router } = await mountPage()
    await w.get('input[name=email]').setValue('new@hotel.example')
    await w.get('input[name=full_name]').setValue('New')
    await w.get('input[name=password]').setValue('correct horse battery')
    const add = w.findAll('button').find((b) => b.text() === 'Add property')!
    await add.trigger('click')
    await add.trigger('click')
    await selects(w, 'Property 1').setValue('7')
    await selects(w, 'Role 1').setValue('3')
    await selects(w, 'Property 2').setValue('8') // no role yet
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(POST).not.toHaveBeenCalled()
    expect(router.currentRoute.value.path).toBe('/setup/users/new')
    expect(w.find('[data-testid=grant-error-0]').exists()).toBe(false)
    expect(w.get('[data-testid=grant-error-1]').text()).toContain('Choose the property and the role of this row, or remove it')
    expect(selects(w, 'Role 2').attributes('aria-invalid')).toBe('true')
    // completing the row clears the message and saves both
    await selects(w, 'Role 2').setValue('4')
    expect(w.find('[data-testid=grant-error-1]').exists()).toBe(false)
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/users', {
      body: { email: 'new@hotel.example', full_name: 'New', password: 'correct horse battery', is_tenant_admin: false, grants: [{ property_id: 7, role_id: 3 }, { property_id: 8, role_id: 4 }] },
    })
    expect(router.currentRoute.value.path).toBe('/setup/users')
  })

  it('refuses to save an existing user with an empty access row left behind', async () => {
    const { w } = await mountPage('21')
    await w.findAll('button').find((b) => b.text() === 'Add property')!.trigger('click')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(PATCH).not.toHaveBeenCalled()
    expect(PUT).not.toHaveBeenCalled()
    expect(w.get('[data-testid=grant-error-1]').text()).toContain('Choose the property and the role of this row, or remove it')
    // removing the row saves
    await w.findAll('button').filter((b) => b.text() === 'Remove')[1]!.trigger('click')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(PATCH).toHaveBeenCalled()
  })

  // A save was two requests (PATCH the profile, PUT the grants): when the second failed, the profile was saved and the access not.
  it('saves the profile and the access of an existing user in one request', async () => {
    const { w, router } = await mountPage('21')
    await w.get('input[name=full_name]').setValue('Dewi Anggraeni')
    await w.findAll('button').find((b) => b.text() === 'Add property')!.trigger('click')
    await selects(w, 'Property 2').setValue('8')
    await selects(w, 'Role 2').setValue('4')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(PUT).not.toHaveBeenCalled()
    expect(PATCH).toHaveBeenCalledTimes(1)
    expect(PATCH).toHaveBeenCalledWith('/api/v1/users/{userId}', {
      params: { path: { userId: 21 } },
      body: { full_name: 'Dewi Anggraeni', is_active: true, is_tenant_admin: false, grants: [{ property_id: 7, role_id: 3 }, { property_id: 8, role_id: 4 }] },
    })
    expect(router.currentRoute.value.path).toBe('/setup/users')
  })

  it('stays on the form with the error when the save is refused, nothing of it saved', async () => {
    const { w, router } = await mountPage('21')
    const { ApiError } = await import('@/api/problem')
    PATCH = vi.fn().mockRejectedValue(new ApiError({ type: 't', title: 'Not found', status: 404, code: 'PROPERTY_NOT_FOUND', detail: 'the property does not exist' }))
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/setup/users/21')
    expect(w.find('[data-testid=form-error]').exists()).toBe(true)
  })

  it('does not ask for the rows of an administrator, whose access is every property', async () => {
    const { w } = await mountPage()
    await w.get('input[name=email]').setValue('boss@hotel.example')
    await w.get('input[name=full_name]').setValue('Boss')
    await w.get('input[name=password]').setValue('correct horse battery')
    await w.findAll('button').find((b) => b.text() === 'Add property')!.trigger('click')
    await w.get('input[name=is_tenant_admin]').setValue(true)
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(POST).toHaveBeenCalled()
  })
})
