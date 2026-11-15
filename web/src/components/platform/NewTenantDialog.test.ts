import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import NewTenantDialog from './NewTenantDialog.vue'

let POST = vi.fn()
vi.mock('@/api/platformClient', () => ({ platformApi: { POST: (...a: unknown[]) => POST(...a) } }))

const body = () => document.body
let wrapper: VueWrapper | null = null
const input = (name: string) => body().querySelector<HTMLInputElement>(`[name=${name}]`)!
function type(name: string, value: string) {
  const el = input(name)
  el.value = value
  el.dispatchEvent(new Event('input'))
}
const submit = () => body().querySelector<HTMLFormElement>('[data-testid=new-tenant-form]')!.dispatchEvent(new Event('submit', { cancelable: true }))
const created = (extra: object = {}) => ({ tenant: { id: 5, code: 'SGR', name: 'Segara Resort', status: 'ACTIVE', timezone: 'Asia/Jakarta', created_at: '2026-11-14T00:00:00Z' }, ...extra })

async function open() {
  wrapper = mount(NewTenantDialog, { props: { open: true }, attachTo: document.body })
  await flushPromises()
  return wrapper
}

describe('NewTenantDialog', () => {
  beforeEach(async () => {
    POST = vi.fn()
    await setLocale('en')
  })
  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
    document.body.innerHTML = ''
  })

  it('needs a code and a name', async () => {
    await open()
    const button = body().querySelector<HTMLButtonElement>('[data-testid=new-tenant-submit]')!
    expect(button.disabled).toBe(true)
    type('code', 'sgr')
    type('name', 'Segara Resort')
    await flushPromises()
    expect(button.disabled).toBe(false)
    expect(input('timezone').value).toBe('Asia/Jakarta')
  })

  it('creates a tenant without an administrator: no admin in the request, no password shown', async () => {
    POST = vi.fn(async () => ({ data: created() }))
    await open()
    type('code', 'sgr')
    type('name', 'Segara Resort')
    await flushPromises()
    submit()
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/platform/tenants', { body: { code: 'SGR', name: 'Segara Resort', timezone: 'Asia/Jakarta' } })
    expect(body().querySelector('[data-testid=admin-password]')).toBeNull()
    expect(body().querySelector('[data-testid=open-new-tenant]')).not.toBeNull()
  })

  it('shows the generated password once, copies it, and drops it when closed (the page then opens the tenant)', async () => {
    POST = vi.fn(async () => ({ data: created({ admin_password: 'S3cret-Pass-123' }) }))
    const writeText = vi.fn(async () => undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const w = await open()
    type('code', 'SGR')
    type('name', 'Segara Resort')
    type('admin_email', 'boss@segara.example')
    type('admin_name', 'Boss')
    await flushPromises()
    submit()
    await flushPromises()
    expect(POST.mock.calls[0]![1].body.admin).toEqual({ email: 'boss@segara.example', full_name: 'Boss' })
    expect(body().querySelector('[data-testid=admin-password]')!.textContent).toBe('S3cret-Pass-123')

    body().querySelector<HTMLButtonElement>('[data-testid=copy-password]')!.click()
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith('S3cret-Pass-123')
    expect(body().querySelector('[data-testid=copy-password]')!.textContent).toContain('Copied')

    body().querySelector<HTMLButtonElement>('[data-testid=open-new-tenant]')!.click()
    await flushPromises()
    expect(w.emitted('created')![0]).toEqual(['SGR'])
    expect(w.emitted('update:open')![0]).toEqual([false])
    // nothing of the password is left in the page or in the storage
    expect(JSON.stringify(Object.entries(localStorage)) + JSON.stringify(Object.entries(sessionStorage))).not.toContain('S3cret')
    await w.setProps({ open: false })
    await w.setProps({ open: true })
    await flushPromises()
    expect(body().textContent).not.toContain('S3cret-Pass-123')
  })

  it('shows a refusal inline with the message under the field', async () => {
    POST = vi.fn(async () => {
      throw new ApiError({ status: 422, code: 'VALIDATION_FAILED', title: 'Invalid', type: 'about:blank', errors: [{ field: 'code', code: 'ALREADY_EXISTS', message: 'taken' }] } as never)
    })
    const w = await open()
    type('code', 'SGR')
    type('name', 'Segara')
    await flushPromises()
    submit()
    await flushPromises()
    expect(body().querySelector('[data-testid=new-tenant-error]')).not.toBeNull()
    expect(w.emitted('created')).toBeUndefined()
    expect(body().querySelector('[data-testid=new-tenant-form]')).not.toBeNull()
  })

  it('closes with Cancel without opening anything', async () => {
    const w = await open()
    ;[...body().querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Cancel')!.click()
    expect(w.emitted('update:open')![0]).toEqual([false])
    expect(w.emitted('created')).toBeUndefined()
  })
})
