import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import CheckOutWizard from './CheckOutWizard.vue'

let POST = vi.fn()
let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { POST: (...a: unknown[]) => POST(...a), GET: (...a: unknown[]) => GET(...a) } }))

const detail = (departure: string, balance = '0') => ({
  stay: { id: 5, stay_number: 'STY000001', arrival_date: '2026-09-30', departure_date: departure, status: 'OPEN', version: 3 },
  folios: [{ id: 8, folio_number: 'FOL000001', status: 'OPEN', balance }],
}) as never

function mountWizard(d: never) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-09-30' } as never
  useAuthStore().me = { properties: [{ id: 7, permissions: ['payment.post', 'frontdesk.checkout'] }], user: { is_tenant_admin: false } } as never
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(CheckOutWizard, { props: { detail: d }, global: { plugins: [pinia, router] } })
}

const result = { stay: { id: 5, status: 'CHECKED_OUT' }, posted_room_charges: [{ service_date: '2026-09-30', total: '1100000' }], folios: [{ id: 8, folio_number: 'FOL000001', status: 'CLOSED' }], housekeeping: 'DIRTY' }

describe('CheckOutWizard', () => {
  beforeEach(() => {
    POST = vi.fn().mockResolvedValue({ data: result })
    GET = vi.fn(async (path: string) => {
      if (path.endsWith('/cashier/settings')) return { data: { require_shift_for_cash: false } }
      return { data: detail('2026-09-30', '0') }
    })
  })

  it('checks out on the departure day with the stay version and an idempotency key', async () => {
    const w = mountWizard(detail('2026-09-30'))
    await w.get('[data-testid=checkout-submit]').trigger('click')
    await flushPromises()
    const call = POST.mock.calls[0]
    expect(call?.[0]).toBe('/api/v1/properties/{propertyId}/stays/{id}/check-out')
    expect(call?.[1]).toMatchObject({ params: { path: { propertyId: 7, id: 5 } }, body: { version: 3, confirm_early_departure: false } })
    expect(typeof call?.[1].params.header['Idempotency-Key']).toBe('string')
    expect(w.get('[data-testid=checkout-done]').text()).toContain('DIRTY')
    expect(w.emitted('done')).toHaveLength(1)
  })

  it('needs an explicit confirmation for an early departure', async () => {
    const w = mountWizard(detail('2026-10-03'))
    expect(w.get('[data-testid=checkout-submit]').attributes('disabled')).toBeDefined()
    await w.get('input[name=confirm_early]').setValue(true)
    await w.get('[data-testid=checkout-submit]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1]).toMatchObject({ body: { version: 3, confirm_early_departure: true } })
  })

  it('points to the folios that still have a balance and keeps the dialog open on a refusal', async () => {
    // a company folio is not held back by the button (the server decides); a guest folio that is not at zero is, see below
    const w = mountWizard(owing('500000', 'COMPANY'))
    expect(w.get('[data-testid=open-folio-8]').attributes('href')).toBe('/folios/8')
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'FOLIO_NOT_BALANCED', detail: 'not balanced' }))
    await w.get('[data-testid=checkout-submit]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=checkout-error]').text()).toContain('FOLIO_NOT_BALANCED')
    expect(w.find('[data-testid=checkout-done]').exists()).toBe(false)
    expect(w.emitted('done')).toBeUndefined()
  })
  const owing = (balance: string, type = 'GUEST') => ({
    stay: { id: 5, stay_number: 'STY000001', arrival_date: '2026-09-30', departure_date: '2026-09-30', status: 'OPEN', version: 3 },
    folios: [{ id: 8, folio_number: 'FOL000001', folio_type: type, status: 'OPEN', balance }],
  }) as never
  const paymentCalls = () => POST.mock.calls.filter((c) => String(c[0]).endsWith('/payments'))

  it('takes the payment of a guest folio that owes, then reads the stay again and lets the check-out go on', async () => {
    POST = vi.fn().mockResolvedValue({ data: { id: 1 } })
    const w = mountWizard(owing('250000'))
    expect((w.get('input[name=pay_amount]').element as HTMLInputElement).value).toBe('250000')
    await w.get('select[name=pay_method]').setValue('CARD')
    await w.get('[data-testid=checkout-pay-submit]').trigger('click')
    await flushPromises()
    const call = paymentCalls()[0]
    expect(call?.[0]).toBe('/api/v1/properties/{propertyId}/folios/{id}/payments')
    expect(call?.[1]).toMatchObject({ params: { path: { propertyId: 7, id: 8 } }, body: { amount: '250000', payment_method: 'CARD' } })
    expect(GET).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/stays/{id}', expect.anything())
    expect(w.find('[data-testid=checkout-pay]').exists()).toBe(false)
    expect(w.get('[data-testid=checkout-paid]').classes()).toContain('notice')
  })

  it('warns of a credit when the amount is above the balance, and still posts it', async () => {
    POST = vi.fn().mockResolvedValue({ data: { id: 1 } })
    const w = mountWizard(owing('100000'))
    expect(w.find('[data-testid=overpay-warning]').exists()).toBe(false)
    await w.get('input[name=pay_amount]').setValue('150000')
    expect(w.get('[data-testid=overpay-warning]').text()).toContain('credit')
    await w.get('[data-testid=checkout-pay-submit]').trigger('click')
    await flushPromises()
    expect(paymentCalls()).toHaveLength(1)
  })

  it('tries the same payment again with the same key after the network dropped, but a changed amount is another request', async () => {
    POST = vi.fn().mockRejectedValue(new Error('network'))
    const w = mountWizard(owing('100000'))
    const press = async () => {
      await w.get('[data-testid=checkout-pay-submit]').trigger('click')
      await flushPromises()
    }
    await press()
    await press()
    const keyOf = (i: number) => paymentCalls()[i]?.[1].params.header['Idempotency-Key']
    expect(keyOf(1)).toBe(keyOf(0))
    await w.get('input[name=pay_amount]').setValue('90000')
    await press()
    expect(keyOf(2)).not.toBe(keyOf(0))
  })

  it('renews the key once the server has answered (a refusal), so the next attempt is a new request', async () => {
    POST = vi.fn().mockRejectedValue(new ApiError({ type: 't', title: 'x', status: 409, code: 'NO_OPEN_SHIFT', detail: 'x' }))
    const w = mountWizard(owing('100000'))
    for (let i = 0; i < 2; i++) {
      await w.get('[data-testid=checkout-pay-submit]').trigger('click')
      await flushPromises()
    }
    expect(paymentCalls()[1]?.[1].params.header['Idempotency-Key']).not.toBe(paymentCalls()[0]?.[1].params.header['Idempotency-Key'])
  })

  it('keeps the payment and says so when the check-out is refused after it', async () => {
    POST = vi.fn(async (path: string) => {
      if (path.endsWith('/payments')) return { data: { id: 1 } }
      throw new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'STAY_VERSION_CONFLICT', detail: 'moved' })
    })
    GET = vi.fn(async () => ({ data: detail('2026-09-30', '0') }))
    const w = mountWizard(owing('100000'))
    await w.get('[data-testid=checkout-pay-submit]').trigger('click')
    await flushPromises()
    await w.get('[data-testid=checkout-submit]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=checkout-payment-kept]').text()).toContain('stays posted')
    expect(paymentCalls()).toHaveLength(1)
  })

  it('sends a company folio to its folio with a sentence and offers no payment form', () => {
    const w = mountWizard(owing('500000', 'COMPANY'))
    expect(w.find('[data-testid=checkout-pay]').exists()).toBe(false)
    expect(w.get('[data-testid=company-folio-8]').text()).toContain('billed to a company')
  })

  it('tells the guest to be refunded first when the folio is in credit, with no payment form', () => {
    const w = mountWizard(owing('-80000'))
    expect(w.find('[data-testid=checkout-pay]').exists()).toBe(false)
    expect(w.get('[data-testid=refund-first-8]').text()).toContain('Refund')
    expect(w.get('[data-testid=refund-first-8] a').attributes('href')).toBe('/folios/8')
  })

  it('shows no payment form to a role that cannot post payments', () => {
    const w = mountWizard(owing('100000'))
    useAuthStore().me = { properties: [{ id: 7, permissions: ['frontdesk.checkout'] }], user: { is_tenant_admin: false } } as never
    return w.vm.$nextTick().then(() => expect(w.find('[data-testid=checkout-pay]').exists()).toBe(false))
  })
  it('disables the check-out while a guest folio is not at zero, and says why by the button', async () => {
    const w = mountWizard(owing('250000'))
    expect(w.get('[data-testid=checkout-submit]').attributes('disabled')).toBeDefined()
    expect(w.get('[data-testid=checkout-due-reason]').text()).toBe('Pay the balance of 250,000 first.')
    const credit = mountWizard(owing('-80000'))
    expect(credit.get('[data-testid=checkout-submit]').attributes('disabled')).toBeDefined()
    expect(credit.get('[data-testid=checkout-due-reason]').text()).toBe('Refund the credit of 80,000 first.')
    const zero = mountWizard(owing('0'))
    expect(zero.get('[data-testid=checkout-submit]').attributes('disabled')).toBeUndefined()
    expect(zero.find('[data-testid=checkout-due-reason]').exists()).toBe(false)
  })

  it('enables the check-out once the payment has settled the folio', async () => {
    POST = vi.fn().mockResolvedValue({ data: { id: 1 } })
    GET = vi.fn(async () => ({ data: owing('0') }))
    const w = mountWizard(owing('100000'))
    await w.get('[data-testid=checkout-pay-submit]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=checkout-submit]').attributes('disabled')).toBeUndefined()
  })

  it('forgets the check-out error and the "fix what it said" message after a payment, and tells the page to read the stay again', async () => {
    const both = (guest: string) => ({
      stay: { id: 5, stay_number: 'STY000001', arrival_date: '2026-09-30', departure_date: '2026-09-30', status: 'OPEN', version: 3 },
      folios: [
        { id: 8, folio_number: 'FOL000001', folio_type: 'GUEST', status: 'OPEN', balance: guest },
        { id: 9, folio_number: 'FOL000002', folio_type: 'COMPANY', status: 'OPEN', balance: '500000' },
      ],
    }) as never
    POST = vi.fn(async (path: string) => {
      if (path.endsWith('/payments')) return { data: { id: 1 } }
      throw new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'FOLIO_NOT_BALANCED', detail: 'not balanced' })
    })
    GET = vi.fn(async () => ({ data: both('0') }))
    const w = mountWizard(both('0'))
    await w.get('[data-testid=checkout-submit]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid=checkout-error]').exists()).toBe(true)
    await w.setProps({ detail: both('100000') }) // the page read the stay again: the guest owes now
    await w.get('[data-testid=checkout-pay-submit]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid=checkout-error]').exists()).toBe(false)
    expect(w.find('[data-testid=checkout-payment-kept]').exists()).toBe(false)
    expect(w.emitted('paid')).toHaveLength(1)
  })
})
