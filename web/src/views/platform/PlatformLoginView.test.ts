import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { usePlatformAuthStore } from '@/stores/platformAuth'
import PlatformLoginView from './PlatformLoginView.vue'

let POST = vi.fn()
vi.mock('@/api/platformClient', () => ({ platformApi: { POST: (...a: unknown[]) => POST(...a) } }))

const operator = { id: 1, email: 'rina@kamarapms.example', full_name: 'Rina', is_active: true, totp_enrolled: true, last_login_at: null, recovery_codes_left: 10 }
const problem = (code: string, status: number, context?: Record<string, unknown>, detail?: string) => new ApiError({ type: 't', title: code, status, code, detail, context })
const CODES = ['7F3K-Q9WM-2D', 'P4XN-8LRT-6C', 'H2VB-M7QE-9A', 'K8DS-3NWP-4F', 'R6TY-1JCX-8M', 'Z9EH-5UKQ-2B', 'W3LM-7GDV-6N', 'B5QP-4XRA-1H', 'C7NJ-2TFS-9K', 'M1VG-6HZE-3P']

/** Routes of the POST calls by path. */
function api(routes: Record<string, () => unknown>) {
  POST = vi.fn(async (path: string) => {
    const r = routes[path]
    if (!r) throw new Error(`unexpected ${path}`)
    const v = r()
    if (v instanceof Error) throw v
    return v
  })
}
const LOGIN = '/api/v1/platform/auth/login'
const TOTP = '/api/v1/platform/auth/totp'
const START = '/api/v1/platform/auth/enroll/start'
const CONFIRM = '/api/v1/platform/auth/enroll/confirm'

async function mountView(query = '') {
  setActivePinia(createPinia())
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/platform/login', component: PlatformLoginView },
      { path: '/platform/tenants', component: { template: '<div>tenants</div>' } },
      { path: '/platform/invoices', component: { template: '<div>invoices</div>' } },
      { path: '/login', component: { template: '<div>hotel</div>' } },
      { path: '/dashboard', component: { template: '<div>dashboard</div>' } },
    ],
  })
  await router.push('/platform/login' + query)
  const wrapper = mount(PlatformLoginView, { global: { plugins: [router] }, attachTo: document.body })
  return { wrapper, router }
}
type W = Awaited<ReturnType<typeof mountView>>['wrapper']

async function password(wrapper: W, pw = 'secret password') {
  await wrapper.get('input[name=email]').setValue('rina@kamarapms.example')
  await wrapper.get('input[name=password]').setValue(pw)
  await wrapper.get('form').trigger('submit')
  await flushPromises()
}
async function toCodeStep(wrapper: W) {
  api({ [LOGIN]: () => ({ data: { challenge: 'ch1', next: 'totp', expires_at: '' } }) })
  await password(wrapper)
}

describe('PlatformLoginView, step 1 (password)', () => {
  beforeEach(() => {
    POST = vi.fn()
    setLocale('en')
  })
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('shows the card, the pill, the hotel link and the language select, and focuses the e-mail', async () => {
    const { wrapper } = await mountView()
    await flushPromises()
    expect(wrapper.findAll('h1')).toHaveLength(1)
    expect(wrapper.get('h1').text()).toBe('Platform console')
    expect(wrapper.get('[data-testid=platform-pill]').text()).toBe('Platform')
    expect(wrapper.get('a[href="/login"]').text()).toBe('Sign in as hotel staff')
    expect(wrapper.find('select').exists()).toBe(true)
    expect(wrapper.get('input[name=email]').attributes('autocomplete')).toBe('username')
    expect(wrapper.get('input[name=password]').attributes('autocomplete')).toBe('current-password')
    expect(document.activeElement).toBe(wrapper.get('input[name=email]').element)
  })

  it('hides the password and shows it with the eye, without sending the form', async () => {
    const { wrapper } = await mountView()
    const eye = wrapper.get('[data-testid=toggle-password]')
    expect(wrapper.get('input[name=password]').attributes('type')).toBe('password')
    await eye.trigger('click')
    expect(wrapper.get('input[name=password]').attributes('type')).toBe('text')
    expect(eye.attributes('aria-pressed')).toBe('true')
    expect(POST).not.toHaveBeenCalled()
  })

  it('says the same thing for a wrong password and an unknown e-mail, and clears the password', async () => {
    api({ [LOGIN]: () => problem('INVALID_CREDENTIALS', 401) })
    const { wrapper } = await mountView()
    await password(wrapper)
    const first = wrapper.get('[role=alert]').text()
    expect(first).toBe('The email or password is incorrect.')
    expect((wrapper.get('input[name=password]').element as HTMLInputElement).value).toBe('')
    await wrapper.get('input[name=email]').setValue('nobody@kamarapms.example')
    await password(wrapper, 'x')
    expect(wrapper.get('[role=alert]').text()).toBe(first)
  })

  it('shows the server text when the sign-in is throttled', async () => {
    api({ [LOGIN]: () => problem('TOO_MANY_ATTEMPTS', 429, undefined, 'Too many attempts; try later.') })
    const { wrapper } = await mountView()
    await password(wrapper)
    expect(wrapper.get('[role=alert]').text()).toContain('Too many attempts; try later.')
  })

  it('says so when the server cannot be reached, and disables the button while it works', async () => {
    let release: (v: unknown) => void = () => {}
    POST = vi.fn(() => new Promise((r) => (release = r)))
    const { wrapper } = await mountView()
    await wrapper.get('input[name=email]').setValue('a@b.example')
    await wrapper.get('input[name=password]').setValue('pw')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.get('button[type=submit]').attributes('disabled')).toBeDefined()
    release({ data: { challenge: 'c', next: 'totp', expires_at: '' } })
    await flushPromises()
    expect(wrapper.find('input[name=totp]').exists()).toBe(true)
  })

  it('reports a failure that is not an answer of the server', async () => {
    POST = vi.fn(async () => {
      throw new TypeError('boom')
    })
    const { wrapper } = await mountView()
    await password(wrapper)
    expect(wrapper.get('[role=alert]').text()).toBe('The server could not be reached. Try again.')
  })
})

describe('PlatformLoginView, step 2 (code)', () => {
  beforeEach(() => {
    POST = vi.fn()
    setLocale('en')
  })
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('asks for the code of the e-mail, with the right input and focus on it', async () => {
    const { wrapper } = await mountView()
    await toCodeStep(wrapper)
    expect(wrapper.get('h1').text()).toBe('Authenticator code')
    expect(wrapper.get('[data-testid=login-email]').text()).toBe('rina@kamarapms.example')
    const input = wrapper.get('input[name=totp]')
    expect(input.attributes('inputmode')).toBe('numeric')
    expect(input.attributes('autocomplete')).toBe('one-time-code')
    expect(document.activeElement).toBe(input.element)
    expect(wrapper.find('[data-testid=attempts-left]').exists()).toBe(false)
  })

  it('sends the digits only (spaces ignored) and goes to the page asked for', async () => {
    const { wrapper, router } = await mountView('?redirect=/platform/invoices')
    await toCodeStep(wrapper)
    api({ [TOTP]: () => ({ data: { access_token: 'tok', operator } }) })
    await wrapper.get('input[name=totp]').setValue('482 913')
    await wrapper.get('[data-testid=step-code] form').trigger('submit')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith(TOTP, { body: { challenge: 'ch1', code: '482913', recovery_code: undefined } })
    expect(usePlatformAuthStore().isAuthenticated).toBe(true)
    expect(router.currentRoute.value.path).toBe('/platform/invoices')
  })

  it('goes to the tenants when there is no redirect', async () => {
    const { wrapper, router } = await mountView()
    await toCodeStep(wrapper)
    api({ [TOTP]: () => ({ data: { access_token: 'tok', operator } }) })
    await wrapper.get('input[name=totp]').setValue('123456')
    await wrapper.get('[data-testid=step-code] form').trigger('submit')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/platform/tenants')
  })

  it.each(['//evil.test', 'https://x.example', '/dashboard', '/platform/../dashboard\\x', '/\\evil.test'])('ignores the redirect %s', async (target) => {
    const { wrapper, router } = await mountView('?redirect=' + encodeURIComponent(target))
    await toCodeStep(wrapper)
    api({ [TOTP]: () => ({ data: { access_token: 'tok', operator } }) })
    await wrapper.get('input[name=totp]').setValue('123456')
    await wrapper.get('[data-testid=step-code] form').trigger('submit')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/platform/tenants')
  })

  it('shows a wrong code with the attempts left, and clears the field', async () => {
    const { wrapper } = await mountView()
    await toCodeStep(wrapper)
    api({ [TOTP]: () => problem('TOTP_INVALID', 401, { attempts_left: 4 }) })
    await wrapper.get('input[name=totp]').setValue('000000')
    await wrapper.get('[data-testid=step-code] form').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[role=alert]').text()).toBe('The code is not correct.')
    expect(wrapper.get('[data-testid=attempts-left]').text()).toBe('Attempts left: 4')
    expect((wrapper.get('input[name=totp]').element as HTMLInputElement).value).toBe('')
  })

  it('sends a recovery code as recovery_code, not as code', async () => {
    const { wrapper } = await mountView()
    await toCodeStep(wrapper)
    api({ [TOTP]: () => ({ data: { access_token: 'tok', operator } }) })
    expect(wrapper.get('input[name=recovery_code]').element.closest('details')).not.toBeNull()
    await wrapper.get('input[name=recovery_code]').setValue('7F3K-Q9WM-2D')
    await wrapper.get('input[name=recovery_code]').element.closest('form')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(POST).toHaveBeenCalledWith(TOTP, { body: { challenge: 'ch1', code: undefined, recovery_code: '7F3K-Q9WM-2D' } })
    expect(usePlatformAuthStore().isAuthenticated).toBe(true)
  })

  it('never keeps a recovery code or the challenge in a storage, the URL or the console', async () => {
    const sets = vi.spyOn(Storage.prototype, 'setItem')
    const log = vi.spyOn(console, 'log')
    const { wrapper, router } = await mountView()
    await toCodeStep(wrapper)
    api({ [TOTP]: () => ({ data: { access_token: 'tok', operator } }) })
    await wrapper.get('input[name=recovery_code]').setValue('7F3K-Q9WM-2D')
    await wrapper.get('input[name=recovery_code]').element.closest('form')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(sets).not.toHaveBeenCalled()
    expect(log).not.toHaveBeenCalled()
    expect(JSON.stringify({ ...localStorage, ...sessionStorage })).not.toContain('7F3K')
    expect(router.currentRoute.value.fullPath).not.toContain('ch1')
    sets.mockRestore()
    log.mockRestore()
  })

  it('goes back to the password when the challenge has expired, with a message', async () => {
    const { wrapper } = await mountView()
    await toCodeStep(wrapper)
    api({ [TOTP]: () => problem('CHALLENGE_INVALID', 401) })
    await wrapper.get('input[name=totp]').setValue('123456')
    await wrapper.get('[data-testid=step-code] form').trigger('submit')
    await flushPromises()
    expect(wrapper.find('input[name=password]').exists()).toBe(true)
    expect(wrapper.get('[role=alert]').text()).toContain('expired')
  })

  it('goes back to the password with the Back button, and forgets the step', async () => {
    const { wrapper } = await mountView()
    await toCodeStep(wrapper)
    await wrapper.get('[data-testid=back]').trigger('click')
    await flushPromises()
    expect(wrapper.find('input[name=password]').exists()).toBe(true)
    expect(wrapper.find('input[name=totp]').exists()).toBe(false)
    expect((wrapper.get('input[name=password]').element as HTMLInputElement).value).toBe('')
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    const { wrapper } = await mountView()
    await toCodeStep(wrapper)
    api({ [TOTP]: () => problem('TOTP_INVALID', 401, { attempts_left: 4 }) })
    await wrapper.get('input[name=totp]').setValue('000000')
    await wrapper.get('[data-testid=step-code] form').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[data-testid=attempts-left]').text()).toBe('Sisa percobaan: 4')
  })
})

describe('PlatformLoginView, step 3 (enrolment)', () => {
  const started = { secret: 'JBSW Y3DP EHPK 3PXP', otpauth_uri: 'otpauth://x', qr_png: 'data:image/png;base64,AAAA' }
  let writeText = vi.fn()

  async function toEnrol(query = '') {
    const view = await mountView(query)
    api({ [LOGIN]: () => ({ data: { challenge: 'en1', next: 'enroll', expires_at: '' } }), [START]: () => ({ data: started }) })
    await password(view.wrapper)
    return view
  }

  beforeEach(() => {
    POST = vi.fn()
    setLocale('en')
    writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText: (...a: unknown[]) => writeText(...a) }, configurable: true })
  })
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('starts the enrolment when the step opens and shows the QR and the secret', async () => {
    const { wrapper } = await toEnrol()
    expect(POST).toHaveBeenCalledWith(START, { body: { challenge: 'en1' } })
    expect(wrapper.get('h1').text()).toBe('Set up the authenticator app')
    expect(wrapper.get('[data-testid=enroll-qr]').attributes('src')).toBe(started.qr_png)
    expect(wrapper.get('[data-testid=enroll-secret]').text()).toBe('JBSW Y3DP EHPK 3PXP')
    expect(wrapper.find('[data-testid=recovery-codes]').exists()).toBe(false)
    await wrapper.get('[data-testid=copy-secret]').trigger('click')
    expect(writeText).toHaveBeenCalledWith('JBSW Y3DP EHPK 3PXP')
  })

  it('confirms with the digits, shows ten codes, and opens the console only after the box is ticked (once)', async () => {
    const sets = vi.spyOn(Storage.prototype, 'setItem')
    const { wrapper, router } = await toEnrol('?redirect=/platform/invoices')
    api({ [CONFIRM]: () => ({ data: { access_token: 'tok', operator, recovery_codes: CODES } }) })
    const store = usePlatformAuthStore()
    await wrapper.get('[data-testid=step-enroll] input[name=totp]').setValue('482 913')
    await wrapper.get('[data-testid=step-enroll] form').trigger('submit')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith(CONFIRM, { body: { challenge: 'en1', code: '482913' } })
    expect(wrapper.findAll('[data-testid=recovery-codes] li')).toHaveLength(10)
    expect(store.isAuthenticated).toBe(false)

    const open = wrapper.get('[data-testid=open-console]')
    expect(open.attributes('disabled')).toBeDefined()
    await open.trigger('click')
    expect(store.isAuthenticated).toBe(false)

    await wrapper.get('[data-testid=codes-saved]').setValue(true)
    expect(wrapper.get('[data-testid=open-console]').attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-testid=open-console]').trigger('click')
    await wrapper.get('[data-testid=open-console]').trigger('click')
    await flushPromises()
    expect(store.isAuthenticated).toBe(true)
    expect(router.currentRoute.value.path).toBe('/platform/invoices')

    // the codes and the secret reach no storage and no store
    expect(sets).not.toHaveBeenCalled()
    expect(JSON.stringify(store.$state)).not.toContain('7F3K')
    expect(JSON.stringify({ ...localStorage, ...sessionStorage })).not.toContain('7F3K')
    sets.mockRestore()
  })

  it('copies all the codes and offers the download', async () => {
    const { wrapper } = await toEnrol()
    api({ [CONFIRM]: () => ({ data: { access_token: 'tok', operator, recovery_codes: CODES } }) })
    await wrapper.get('[data-testid=step-enroll] input[name=totp]').setValue('123456')
    await wrapper.get('[data-testid=step-enroll] form').trigger('submit')
    await flushPromises()
    await wrapper.get('[data-testid=copy-codes]').trigger('click')
    expect(writeText).toHaveBeenCalledWith(CODES.join('\n'))
    const create = vi.fn(() => 'blob:x')
    Object.assign(URL, { createObjectURL: create, revokeObjectURL: vi.fn() })
    await wrapper.get('[data-testid=download-codes]').trigger('click')
    expect(create).toHaveBeenCalledTimes(1)
  })

  it('shows a wrong first code and stays on the step', async () => {
    const { wrapper } = await toEnrol()
    api({ [CONFIRM]: () => problem('TOTP_INVALID', 401, { attempts_left: 3 }) })
    await wrapper.get('[data-testid=step-enroll] input[name=totp]').setValue('000000')
    await wrapper.get('[data-testid=step-enroll] form').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[role=alert]').text()).toBe('The code is not correct.')
    expect(wrapper.find('[data-testid=recovery-codes]').exists()).toBe(false)
  })

  it('goes back to the password when the enrolment challenge has expired', async () => {
    const { wrapper } = await toEnrol()
    api({ [CONFIRM]: () => problem('CHALLENGE_INVALID', 401) })
    await wrapper.get('[data-testid=step-enroll] input[name=totp]').setValue('123456')
    await wrapper.get('[data-testid=step-enroll] form').trigger('submit')
    await flushPromises()
    expect(wrapper.find('input[name=password]').exists()).toBe(true)
    expect(wrapper.find('[data-testid=enroll-secret]').exists()).toBe(false)
  })

  it('tells when the QR code cannot be loaded and lets the person go back', async () => {
    const view = await mountView()
    api({ [LOGIN]: () => ({ data: { challenge: 'en1', next: 'enroll', expires_at: '' } }), [START]: () => problem('CHALLENGE_TAKEN', 409, undefined, 'This sign-in step was already used.') })
    await password(view.wrapper)
    expect(view.wrapper.get('[role=alert]').text()).toContain('already used')
    expect(view.wrapper.find('[data-testid=enroll-qr]').exists()).toBe(false)
    await view.wrapper.get('[data-testid=back]').trigger('click')
    expect(view.wrapper.find('input[name=password]').exists()).toBe(true)
  })
})
