import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
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

describe('LoginView, the password field', () => {
  beforeEach(() => {
    POST = vi.fn()
    localStorage.clear()
    setLocale('en')
  })

  it('hides the password at first and shows it with the eye, without sending the form', async () => {
    const { wrapper } = await mountLogin()
    const input = () => wrapper.get('input[name=password]')
    const eye = wrapper.get('[data-testid=toggle-password]')
    expect(input().attributes('type')).toBe('password')
    expect(eye.attributes('type')).toBe('button') // a button of the form that does not submit it
    expect(eye.attributes('aria-pressed')).toBe('false')
    expect(eye.attributes('aria-label')).toBe('Show password')
    await input().setValue('secret words here')
    await eye.trigger('click')
    await flushPromises()
    expect(input().attributes('type')).toBe('text')
    expect(wrapper.get('[data-testid=toggle-password]').attributes('aria-pressed')).toBe('true')
    expect((input().element as HTMLInputElement).value).toBe('secret words here')
    expect(POST).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid=login-error]').exists()).toBe(false)
    await wrapper.get('[data-testid=toggle-password]').trigger('click')
    expect(input().attributes('type')).toBe('password')
  })

  it('is inside the field, 44 px for a finger, named in both languages, and stores nothing', async () => {
    const { wrapper } = await mountLogin()
    const eye = wrapper.get('[data-testid=toggle-password]')
    expect(eye.element.parentElement!.contains(wrapper.get('input[name=password]').element)).toBe(true)
    expect(eye.classes()).toContain('pointer-coarse:size-[44px]')
    const before = JSON.stringify([{ ...localStorage }, { ...sessionStorage }])
    await eye.trigger('click')
    expect(JSON.stringify([{ ...localStorage }, { ...sessionStorage }])).toBe(before) // showing it is not kept anywhere
    wrapper.unmount()
    setLocale('id')
    const id = await mountLogin()
    expect(id.wrapper.get('[data-testid=toggle-password]').attributes('aria-label')).toBe('Tampilkan kata sandi')
  })

  it('is hidden again on the next visit, whatever it was', async () => {
    const first = await mountLogin()
    await first.wrapper.get('[data-testid=toggle-password]').trigger('click')
    first.wrapper.unmount()
    const second = await mountLogin()
    expect(second.wrapper.get('input[name=password]').attributes('type')).toBe('password')
  })
})

describe('LoginView, the forgotten password', () => {
  beforeEach(() => {
    POST = vi.fn()
    localStorage.clear()
    setLocale('en')
  })

  it('only explains: a button that shows a text in place, with no request and no page', async () => {
    const { wrapper, router } = await mountLogin()
    const button = wrapper.get('[data-testid=forgot-password]')
    expect(button.element.tagName).toBe('BUTTON')
    expect(button.attributes('type')).toBe('button')
    expect(button.text()).toBe('Forgot your password?')
    expect(button.attributes('aria-expanded')).toBe('false')
    expect(wrapper.find('[data-testid=forgot-help]').exists()).toBe(false)
    await button.trigger('click')
    expect(wrapper.get('[data-testid=forgot-help]').text()).toBe('Contact your hotel administrator to reset your password.')
    expect(wrapper.get('[data-testid=forgot-password]').attributes('aria-expanded')).toBe('true')
    expect(wrapper.get('[data-testid=forgot-password]').attributes('aria-controls')).toBe(wrapper.get('[data-testid=forgot-help]').attributes('id'))
    expect(POST).not.toHaveBeenCalled()
    expect(router.currentRoute.value.path).toBe('/login')
    expect(wrapper.find('a[href]').exists()).toBe(false)
    await wrapper.get('[data-testid=forgot-password]').trigger('click')
    expect(wrapper.find('[data-testid=forgot-help]').exists()).toBe(false)
  })

  it('says it in Indonesian, and says the same whatever was typed (it knows no account)', async () => {
    setLocale('id')
    const { wrapper } = await mountLogin()
    await wrapper.get('input[name=email]').setValue('nobody@hotel.com')
    await wrapper.get('[data-testid=forgot-password]').trigger('click')
    expect(wrapper.get('[data-testid=forgot-password]').text()).toBe('Lupa kata sandi?')
    expect(wrapper.get('[data-testid=forgot-help]').text()).toBe('Hubungi administrator hotel Anda untuk mengatur ulang kata sandi.')
    expect(POST).not.toHaveBeenCalled()
  })
})

describe('LoginView, the tenant code that is remembered', () => {
  const KEY = 'kamarapms.lastTenantCode'
  beforeEach(() => {
    POST = vi.fn()
    localStorage.clear()
    setLocale('en')
  })
  afterEach(() => vi.restoreAllMocks())

  it('is in the field when the person comes back, and the focus goes to the email', async () => {
    localStorage.setItem(KEY, 'DEMO')
    const { wrapper } = await mountLogin()
    expect((wrapper.get('input[name=tenant_code]').element as HTMLInputElement).value).toBe('DEMO')
    expect(wrapper.get('input[name=email]').attributes('data-autofocus')).toBeDefined()
    expect(wrapper.get('input[name=tenant_code]').attributes('data-autofocus')).toBeUndefined()
  })

  it('is empty at the first visit, and the focus is on the tenant code', async () => {
    const { wrapper } = await mountLogin()
    expect((wrapper.get('input[name=tenant_code]').element as HTMLInputElement).value).toBe('')
    expect(wrapper.get('input[name=tenant_code]').attributes('data-autofocus')).toBeDefined()
  })

  it('is stored, in capitals, after a sign-in that worked, and not after one that did not', async () => {
    POST.mockRejectedValueOnce(new ApiError({ type: 't', title: 'Unauthorized', status: 401, code: 'INVALID_CREDENTIALS' }))
    const { wrapper } = await mountLogin()
    await fill(wrapper)
    expect(localStorage.getItem(KEY)).toBeNull()
    POST.mockResolvedValue({ data: { access_token: 't' } })
    await wrapper.get('input[name=tenant_code]').setValue(' demo ')
    await wrapper.get('input[name=password]').setValue('right password!')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(localStorage.getItem(KEY)).toBe('DEMO')
  })

  it('does no harm when the browser refuses its storage: the field is empty and the sign-in goes on', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('denied')
    })
    POST.mockResolvedValue({ data: { access_token: 't' } })
    const { wrapper, router } = await mountLogin()
    expect((wrapper.get('input[name=tenant_code]').element as HTMLInputElement).value).toBe('')
    await fill(wrapper)
    expect(router.currentRoute.value.path).toBe('/')
  })
})
