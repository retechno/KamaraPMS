import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import RecordPaymentDialog from './RecordPaymentDialog.vue'

let POST = vi.fn()
vi.mock('@/api/platformClient', () => ({ platformApi: { POST: (...a: unknown[]) => POST(...a) } }))

const invoice = {
  id: 7, number: 'SUB-2026-00041', plan_id: 2, plan_code: 'PRO', period_start: '2026-11-09', period_end: '2027-02-08', amount: '7500000', currency_code: 'IDR', status: 'ISSUED' as const,
  issued_at: '2026-11-09T00:00:00Z', due_on: '2026-11-09', is_overdue: true, tenant_code: 'SGR', tenant_name: 'Segara Resort Group', overdue_days: 5,
}
const result = { invoice: { ...invoice, status: 'PAID' }, subscription: { id: 1, plan_id: 2, status: 'ACTIVE', started_on: '2026-01-01', paid_through: '2027-02-08' } }

const body = () => document.body
const field = (name: string) => body().querySelector<HTMLInputElement>(`input[name=${name}]`)!
let wrapper: VueWrapper | null = null

async function open(over: { open?: boolean } = {}) {
  wrapper = mount(RecordPaymentDialog, { props: { open: over.open ?? true, invoice }, attachTo: document.body })
  await flushPromises()
  return wrapper
}

describe('RecordPaymentDialog', () => {
  beforeEach(async () => {
    POST = vi.fn()
    await setLocale('en')
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-11-14T08:00:00Z'))
  })
  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
    document.body.innerHTML = ''
    vi.useRealTimers()
  })

  it('draws nothing while closed', async () => {
    await open({ open: false })
    expect(body().querySelector('[data-testid=record-payment-dialog]')).toBeNull()
  })

  it('shows the invoice, today as the date and the "after saving" box', async () => {
    await open()
    const summary = body().querySelector('[data-testid=pay-summary]')!.textContent ?? ''
    expect(summary).toContain('SUB-2026-00041')
    expect(summary).toContain('Segara Resort Group (SGR)')
    expect(summary).toContain('PRO')
    expect(summary).toContain('IDR')
    expect(field('paid_on').value).toBe('2026-11-14')
    expect(body().querySelector('[data-testid=pay-after]')!.textContent).toContain('PRO')
    expect(body().textContent).toContain('A paid invoice cannot be changed or voided.')
  })

  it('sends the date and the reference, then reports the result and closes', async () => {
    POST = vi.fn(async () => ({ data: result }))
    const w = await open()
    field('reference').value = 'TRF-90412'
    field('reference').dispatchEvent(new Event('input'))
    field('paid_on').value = '2026-11-12'
    field('paid_on').dispatchEvent(new Event('change'))
    await flushPromises()
    body().querySelector<HTMLFormElement>('[data-testid=pay-form]')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(POST).toHaveBeenCalledTimes(1)
    expect(POST.mock.calls[0]![0]).toBe('/api/v1/platform/invoices/{number}/pay')
    expect(POST.mock.calls[0]![1]).toEqual({ params: { path: { number: 'SUB-2026-00041' } }, body: { paid_on: '2026-11-12', reference: 'TRF-90412' } })
    expect(w.emitted('paid')![0]).toEqual([result])
    expect(w.emitted('update:open')![0]).toEqual([false])
  })

  it('leaves the reference out when it is empty', async () => {
    POST = vi.fn(async () => ({ data: result }))
    await open()
    body().querySelector<HTMLFormElement>('[data-testid=pay-form]')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(POST.mock.calls[0]![1].body).toEqual({ paid_on: '2026-11-14', reference: undefined })
  })

  it('shows the refusal inline and stays open', async () => {
    POST = vi.fn(async () => { throw new ApiError({ status: 409, code: 'INVOICE_NOT_PAYABLE', title: 'Not payable', type: 'about:blank' } as never) })
    const w = await open()
    body().querySelector<HTMLFormElement>('[data-testid=pay-form]')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(body().querySelector('[data-testid=pay-error]')).not.toBeNull()
    expect(w.emitted('paid')).toBeUndefined()
    expect(w.emitted('update:open')).toBeUndefined()
  })

  it('closes with Cancel', async () => {
    const w = await open()
    const cancel = [...body().querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Cancel')!
    cancel.click()
    expect(w.emitted('update:open')![0]).toEqual([false])
  })

  it('starts again from today and an empty reference when it is opened again', async () => {
    const w = await open()
    field('reference').value = 'old'
    field('reference').dispatchEvent(new Event('input'))
    await w.setProps({ open: false })
    await w.setProps({ open: true })
    await flushPromises()
    expect(field('reference').value).toBe('')
  })
})
