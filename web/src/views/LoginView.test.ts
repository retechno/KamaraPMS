import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import LoginView from './LoginView.vue'

let POST = vi.fn()
vi.mock('@/api/client', () => ({
  api: { POST: (...a: unknown[]) => POST(...a), GET: vi.fn(async () => ({ data: { user: { full_name: 'A' }, tenant: {}, properties: [] } })) },
}))

async function mountLogin(query = '') {
  setActivePinia(createPinia())
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/login', component: LoginView },
      { path: '/', component: { template: '<div>home</div>' } },
      { path: '/setup/users', component: { template: '<div>users</div>' } },
    ],
  })
  await router.push('/login' + query)
  const wrapper = mount(LoginView, { global: { plugins: [router] } })
  return { wrapper, router }
}

async function fill(wrapper: Awaited<ReturnType<typeof mountLogin>>['wrapper']) {
  await wrapper.get('input[name=tenant_code]').setValue('ABC')
  await wrapper.get('input[name=email]').setValue('admin@hotel.com')
  await wrapper.get('input[name=password]').setValue('wrong password!')
  await wrapper.get('form').trigger('submit')
  await flushPromises()
}

describe('LoginView', () => {
  beforeEach(() => {
    POST = vi.fn()
    localStorage.clear()
  })

  it('shows one generic message for bad credentials and clears the password', async () => {
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Unauthorized', status: 401, code: 'INVALID_CREDENTIALS' }))
    const { wrapper } = await mountLogin()
    await fill(wrapper)
    expect(wrapper.get('[data-testid=login-error]').text()).toBe('The tenant code, email or password is incorrect.')
    expect((wrapper.get('input[name=password]').element as HTMLInputElement).value).toBe('')
  })

  it('explains throttling', async () => {
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Too Many Requests', status: 429, code: 'TOO_MANY_ATTEMPTS' }))
    const { wrapper } = await mountLogin()
    await fill(wrapper)
    expect(wrapper.get('[data-testid=login-error]').text()).toContain('Too many failed attempts')
  })

  it('returns to the page that required sign-in, but never to another site', async () => {
    POST.mockResolvedValue({ data: { access_token: 't' } })
    let { wrapper, router } = await mountLogin('?redirect=/setup/users')
    await fill(wrapper)
    expect(router.currentRoute.value.path).toBe('/setup/users')

    ;({ wrapper, router } = await mountLogin('?redirect=//evil.example.com'))
    await fill(wrapper)
    expect(router.currentRoute.value.path).toBe('/')
  })
})
