import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import SuspendTenantDialog from './SuspendTenantDialog.vue'

let POST = vi.fn()
vi.mock('@/api/platformClient', () => ({ platformApi: { POST: (...a: unknown[]) => POST(...a) } }))

const body = () => document.body
const q = <T extends Element>(sel: string) => body().querySelector<T>(sel)!
let wrapper: VueWrapper | null = null

async function open(mode: 'suspend' | 'reactivate' = 'suspend') {
  wrapper = mount(SuspendTenantDialog, { props: { open: true, tenantCode: 'SGR', tenantName: 'Segara Resort Group', mode }, attachTo: document.body })
  await flushPromises()
  return wrapper
}
async function type(sel: string, value: string) {
  const el = q<HTMLInputElement | HTMLTextAreaElement>(sel)
  el.value = value
  el.dispatchEvent(new Event('input', { bubbles: true }))
  await flushPromises()
}
const submit = async () => {
  q<HTMLButtonElement>('[data-testid=dialog-submit]').click()
  await flushPromises()
}

describe('SuspendTenantDialog', () => {
  beforeEach(async () => {
    POST = vi.fn()
    await setLocale('en')
  })
  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
    document.body.innerHTML = ''
  })

  it('is an alertdialog that names the tenant and says the three consequences', async () => {
    await open()
    const dialog = q('[data-testid=suspend-dialog]')
    expect(dialog.getAttribute('role')).toBe('alertdialog')
    expect(dialog.textContent).toContain('Suspend Segara Resort Group?')
    expect(body().querySelectorAll('[data-testid=suspend-consequences] li')).toHaveLength(3)
    expect(dialog.textContent).toContain('Type SGR to confirm')
  })

  it('puts the cursor in the reason', async () => {
    await open()
    expect(document.activeElement).toBe(q('textarea[name=reason]'))
  })

  it('keeps the danger button disabled until the code matches, ignoring case and spaces', async () => {
    await open()
    expect(q<HTMLButtonElement>('[data-testid=dialog-submit]').disabled).toBe(true)
    await type('input[name=confirm_code]', 'SG')
    expect(q<HTMLButtonElement>('[data-testid=dialog-submit]').disabled).toBe(true)
    await type('input[name=confirm_code]', ' sgr ')
    expect(q<HTMLButtonElement>('[data-testid=dialog-submit]').disabled).toBe(false)
  })

  it('asks for the reason before it sends anything', async () => {
    await open()
    await type('input[name=confirm_code]', 'SGR')
    await submit()
    expect(POST).not.toHaveBeenCalled()
    expect(q('[data-slot=form-field] [role=alert]').textContent).toBe('Write the reason.')
  })

  it('suspends with the reason and the code as typed, then closes and reports', async () => {
    POST = vi.fn(async () => ({ data: {} }))
    const w = await open()
    await type('textarea[name=reason]', 'Tenant asked to pause')
    await type('input[name=confirm_code]', 'sgr')
    await submit()
    expect(POST).toHaveBeenCalledWith('/api/v1/platform/tenants/{code}/suspend', { params: { path: { code: 'SGR' } }, body: { reason: 'Tenant asked to pause', confirm_code: 'sgr' } })
    expect(w.emitted('done')).toHaveLength(1)
    expect(w.emitted('update:open')?.[0]).toEqual([false])
  })

  it('shows a refusal of the server inline and stays open', async () => {
    POST = vi.fn(async () => {
      throw new ApiError({ type: 'x', title: 'The code you typed is not the code of this tenant.', status: 422, code: 'CONFIRMATION_MISMATCH' })
    })
    const w = await open()
    await type('textarea[name=reason]', 'why')
    await type('input[name=confirm_code]', 'SGR')
    await submit()
    expect(q('[role=alert][data-testid=form-error]').textContent).toContain('not the code of this tenant')
    expect(w.emitted('done')).toBeUndefined()
    expect(w.emitted('update:open')).toBeUndefined()
  })

  it('closes on Escape without sending', async () => {
    const w = await open()
    q('[data-testid=suspend-dialog]').dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await flushPromises()
    expect(w.emitted('update:open')?.[0]).toEqual([false])
    expect(POST).not.toHaveBeenCalled()
  })

  it('closes with the cancel button and the close mark', async () => {
    const w = await open()
    q<HTMLButtonElement>('[data-testid=dialog-cancel]').click()
    q<HTMLButtonElement>('[data-testid=dialog-close]').click()
    await flushPromises()
    expect(w.emitted('update:open')).toHaveLength(2)
  })

  it('forgets what was typed when it is opened again', async () => {
    const w = await open()
    await type('textarea[name=reason]', 'old')
    await w.setProps({ open: false })
    await w.setProps({ open: true })
    await flushPromises()
    expect(q<HTMLTextAreaElement>('textarea[name=reason]').value).toBe('')
  })

  describe('reactivate', () => {
    it('is a plain dialog with a reason and no code to type', async () => {
      await open('reactivate')
      expect(q('[data-testid=reactivate-dialog]').getAttribute('role')).toBe('dialog')
      expect(body().querySelector('input[name=confirm_code]')).toBeNull()
      expect(q('[data-testid=reactivate-dialog]').textContent).toContain('Reactivate Segara Resort Group?')
      expect(q<HTMLButtonElement>('[data-testid=dialog-submit]').disabled).toBe(false)
    })

    it('needs a reason, then posts to reactivate', async () => {
      POST = vi.fn(async () => ({ data: {} }))
      const w = await open('reactivate')
      await submit()
      expect(POST).not.toHaveBeenCalled()
      await type('textarea[name=reason]', 'Renovation finished')
      await submit()
      expect(POST).toHaveBeenCalledWith('/api/v1/platform/tenants/{code}/reactivate', { params: { path: { code: 'SGR' } }, body: { reason: 'Renovation finished' } })
      expect(w.emitted('done')).toHaveLength(1)
    })
  })
})
