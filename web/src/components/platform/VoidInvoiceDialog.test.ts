import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import VoidInvoiceDialog from './VoidInvoiceDialog.vue'

let POST = vi.fn()
vi.mock('@/api/platformClient', () => ({ platformApi: { POST: (...a: unknown[]) => POST(...a) } }))

const invoice = {
  id: 7, number: 'SUB-2026-00041', plan_id: 2, plan_code: 'PRO', period_start: '2026-11-09', period_end: '2027-02-08', amount: '7500000', currency_code: 'IDR', status: 'ISSUED' as const,
  issued_at: '2026-11-09T00:00:00Z', due_on: '2026-11-09', is_overdue: true, tenant_code: 'SGR', tenant_name: 'Segara Resort Group', overdue_days: 5,
}
const body = () => document.body
let wrapper: VueWrapper | null = null
const reason = () => body().querySelector<HTMLInputElement>('input[name=reason]')!
const submit = () => body().querySelector<HTMLFormElement>('[data-testid=void-form]')!.dispatchEvent(new Event('submit', { cancelable: true }))

async function open(isOpen = true) {
  wrapper = mount(VoidInvoiceDialog, { props: { open: isOpen, invoice }, attachTo: document.body })
  await flushPromises()
  return wrapper
}

describe('VoidInvoiceDialog', () => {
  beforeEach(async () => {
    POST = vi.fn()
    await setLocale('en')
  })
  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
    document.body.innerHTML = ''
  })

  it('draws nothing while closed', async () => {
    await open(false)
    expect(body().querySelector('[data-testid=void-invoice-dialog]')).toBeNull()
  })

  it('names the invoice and keeps the button off until there is a reason', async () => {
    await open()
    expect(body().textContent).toContain('Void invoice SUB-2026-00041')
    const button = body().querySelector<HTMLButtonElement>('[data-testid=void-submit]')!
    expect(button.disabled).toBe(true)
    reason().value = '  Issued twice  '
    reason().dispatchEvent(new Event('input'))
    await flushPromises()
    expect(button.disabled).toBe(false)
  })

  it('sends the trimmed reason, then tells the page and closes', async () => {
    POST = vi.fn(async () => ({ data: {} }))
    const w = await open()
    reason().value = '  Issued twice  '
    reason().dispatchEvent(new Event('input'))
    await flushPromises()
    submit()
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/platform/invoices/{number}/void', { params: { path: { number: 'SUB-2026-00041' } }, body: { reason: 'Issued twice' } })
    expect(w.emitted('voided')).toHaveLength(1)
    expect(w.emitted('update:open')![0]).toEqual([false])
  })

  it('does not send an empty reason', async () => {
    await open()
    submit()
    await flushPromises()
    expect(POST).not.toHaveBeenCalled()
  })

  it('shows a refusal inline and stays open', async () => {
    POST = vi.fn(async () => { throw new ApiError({ status: 409, code: 'INVOICE_NOT_VOIDABLE', title: 'Paid', type: 'about:blank' } as never) })
    const w = await open()
    reason().value = 'x'
    reason().dispatchEvent(new Event('input'))
    await flushPromises()
    submit()
    await flushPromises()
    expect(body().querySelector('[data-testid=void-error]')).not.toBeNull()
    expect(w.emitted('voided')).toBeUndefined()
    expect(w.emitted('update:open')).toBeUndefined()
  })

  it('closes with Cancel', async () => {
    const w = await open()
    ;[...body().querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Cancel')!.click()
    expect(w.emitted('update:open')![0]).toEqual([false])
  })
})
