import { DOMWrapper, flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import ApprovalDialog from './ApprovalDialog.vue'

// The dialog is rendered in a portal on the body, so it is found there and not inside the wrapper.
const $ = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)

async function mountDialog(props: Record<string, unknown> = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'clerk@hotel.com', is_tenant_admin: false }, properties: [] } as never
  const w = mount(ApprovalDialog, { props: { title: 'Approve reversal', ...props }, global: { plugins: [pinia] }, attachTo: document.body })
  await flushPromises()
  return w
}

describe('ApprovalDialog', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  it('prefills the signed-in email, needs a password, and hands the credentials over once', async () => {
    const w = await mountDialog()
    expect(($('input[name=approval_email]').element as HTMLInputElement).value).toBe('clerk@hotel.com')
    expect($('[data-testid=approval-submit]').attributes('disabled')).toBeDefined()
    await $('input[name=approval_password]').setValue('s3cret pass phrase')
    await $('form').trigger('submit')
    expect(w.emitted('approve')).toEqual([[{ email: 'clerk@hotel.com', password: 's3cret pass phrase' }]])
    // the password is cleared from the component as soon as it has been handed over
    expect(($('input[name=approval_password]').element as HTMLInputElement).value).toBe('')
  })

  it('can name another approver', async () => {
    const w = await mountDialog()
    await $('input[name=approval_email]').setValue(' manager@hotel.com ')
    await $('input[name=approval_password]').setValue('another pass phrase')
    await $('form').trigger('submit')
    expect(w.emitted('approve')?.[0]?.[0]).toEqual({ email: 'manager@hotel.com', password: 'another pass phrase' })
  })

  it('shows the server error and cancels without emitting credentials', async () => {
    const error = new ApiError({ type: 't', title: 'Unauthorized', status: 401, code: 'APPROVAL_INVALID_CREDENTIALS', detail: 'the approver email or password is incorrect' })
    const w = await mountDialog({ error })
    expect($('[data-testid=approval-error]').text()).toContain('APPROVAL_INVALID_CREDENTIALS')
    await $('input[name=approval_password]').setValue('typed but abandoned')
    await $('[data-testid=approval-cancel]').trigger('click')
    expect(w.emitted('cancel')).toHaveLength(1)
    expect(w.emitted('approve')).toBeUndefined()
    expect(($('input[name=approval_password]').element as HTMLInputElement).value).toBe('')
  })
})

describe('ApprovalDialog as a dialog', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  it('cancels on Escape and keeps typed text when the click is outside', async () => {
    const w = await mountDialog()
    await $('input[name=approval_password]').setValue('typed')
    document.body.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }))
    await flushPromises()
    expect(w.emitted('cancel')).toBeUndefined()
    await $('[data-testid=approval-dialog]').trigger('keydown', { key: 'Escape' })
    await flushPromises()
    expect(w.emitted('cancel')).toHaveLength(1)
  })
})
