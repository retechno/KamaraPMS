import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { loadLocale, setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import GroupHoldings from './GroupHoldings.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const holding = (id: number, over: object = {}) => ({
  reservation_id: id, confirmation_number: `RES00000${id}`, guest_name: `Guest ${id}`, held_balance: '-300000',
  deposits: [{ payment_id: 50 + id, payment_number: `PAY00000${id}`, payment_method: 'BANK_TRANSFER', amount: '300000', refundable: '300000', folio_id: 70 + id, folio_number: `FOL00000${id}` }], ...over,
})
const ALL = ['reservation.read', 'folio.read', 'payment.refund', 'folio.post_charge']
const mounted: VueWrapper[] = []
function mountCard(rows: object[] = [holding(1), holding(2)], permissions = ALL) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'boss@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.current = { currency_decimals: 0, refund_methods: ['BANK_TRANSFER', 'CASH'] } as never
  GET = vi.fn(async () => ({ data: { data: rows } }))
  POST = vi.fn(async () => ({ data: {} }))
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  const w = mount(GroupHoldings, { props: { groupId: 4 }, global: { plugins: [pinia, router] }, attachTo: document.body })
  mounted.push(w)
  return w
}
const typeIn = async (w: VueWrapper, sel: string, value: string) => {
  const el = w.get(sel).element as HTMLInputElement
  el.value = value
  el.dispatchEvent(new Event('input', { bubbles: true }))
  await flushPromises()
}

describe('GroupHoldings', () => {
  beforeEach(() => setLocale('en'))
  afterEach(() => {
    mounted.splice(0).forEach((w) => w.unmount())
    document.body.innerHTML = ''
  })

  it('lists what each cancelled reservation holds and keeps it listed by default', async () => {
    const w = mountCard()
    await flushPromises()
    expect(GET.mock.calls[0]![0]).toBe('/api/v1/properties/{propertyId}/groups/{id}/holdings')
    expect(w.get('[data-testid=holding-balance-1]').text()).toContain('Holds a deposit of')
    expect(w.get('[data-testid=holding-balance-1]').text()).toContain('300,000')
    expect(w.get('[data-testid=holding-balance-1]').text()).toContain('PAY000001')
    expect(w.get('[data-testid=keep-note-1]').text()).toBe('Kept for now: it stays listed until it is refunded or used for a fee.')
    expect(w.find('[data-testid=decision-form-1]').exists()).toBe(false)
  })

  it('refunds the deposit with an approver: the amount is the refundable one and never more', async () => {
    const w = mountCard()
    await flushPromises()
    await w.get('[data-testid=decide-refund-1]').trigger('click')
    const amount = w.get('[name=holding_amount_1]').element as HTMLInputElement
    expect(amount.value).toBe('300,000')
    expect(w.get('[data-testid=decision-save-1]').attributes('disabled')).toBeDefined() // a reason first
    await typeIn(w, '[name=holding_reason_1]', 'the conference is off')
    expect(w.get('[data-testid=decision-save-1]').attributes('disabled')).toBeUndefined()
    await typeIn(w, '[name=holding_amount_1]', '300001')
    expect(w.get('[data-testid=decision-save-1]').attributes('disabled')).toBeDefined() // more than the deposit holds
    await typeIn(w, '[name=holding_amount_1]', '300000')
    await w.get('[data-testid=decision-form-1]').trigger('submit')
    await flushPromises()
    // the approval dialog is asked, and nothing is posted before it
    expect(POST).not.toHaveBeenCalled()
    const email = document.body.querySelector('input[type=email], input[name=email]') as HTMLInputElement
    const pass = document.body.querySelector('input[type=password]') as HTMLInputElement
    expect(email && pass).toBeTruthy()
    email.value = 'boss@hotel.test'
    email.dispatchEvent(new Event('input', { bubbles: true }))
    pass.value = 'secret'
    pass.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    await new DOMWrapper(document.body.querySelector('[data-testid=approval-dialog]') as Element).trigger('submit')
    await flushPromises()
    const call = POST.mock.calls[0]
    expect(call?.[0]).toBe('/api/v1/properties/{propertyId}/payments/{id}/refunds')
    expect(call?.[1].params.path).toEqual({ propertyId: 7, id: 51 })
    expect(call?.[1].body).toMatchObject({ amount: '300000', reason: 'the conference is off', payment_method: 'BANK_TRANSFER', approval: { email: 'boss@hotel.test', password: 'secret' } })
    expect(w.emitted('changed')).toBeTruthy()
    expect(w.get('[data-testid=group-holdings-notice]').text()).toBe('The deposit of RES000001 was refunded.')
  })

  it('turns part of the deposit into the fee of the cancellation, with the amount the person gives', async () => {
    const w = mountCard()
    await flushPromises()
    await w.get('[data-testid=decide-fee-2]').trigger('click')
    expect((w.get('[name=holding_amount_2]').element as HTMLInputElement).value).toBe('') // nothing is calculated
    await typeIn(w, '[name=holding_amount_2]', '200000')
    await typeIn(w, '[name=holding_reason_2]', 'the deposit pays the fee')
    POST.mockResolvedValueOnce({ data: { folio_number: 'FOL000002', item: { debit: '200000' } } })
    await w.get('[data-testid=decision-form-2]').trigger('submit')
    await flushPromises()
    const call = POST.mock.calls[0]
    expect(call?.[0]).toBe('/api/v1/properties/{propertyId}/reservations/{id}/fees')
    expect(call?.[1].params.path).toEqual({ propertyId: 7, id: 2 })
    expect(call?.[1].body).toEqual({ type: 'CANCEL_FEE', reservation_room_id: null, amount: '200000', reason: 'the deposit pays the fee' })
    expect(w.get('[data-testid=group-holdings-notice]').text()).toBe('Fee posted to FOL000002: 200,000.')
  })

  it('offers only the decisions the person may take, and nothing without folio.read', async () => {
    const w = mountCard([holding(1)], ['reservation.read', 'folio.read'])
    await flushPromises()
    expect(w.find('[data-testid=decide-keep-1]').exists()).toBe(true)
    expect(w.find('[data-testid=decide-refund-1]').exists()).toBe(false)
    expect(w.find('[data-testid=decide-fee-1]').exists()).toBe(false)
    const blind = mountCard([holding(1)], ['reservation.read'])
    await flushPromises()
    expect(blind.find('[data-testid=group-holdings]').exists()).toBe(false)
    expect(GET).not.toHaveBeenCalled()
  })

  it('is not shown when nothing is held', async () => {
    const w = mountCard([])
    await flushPromises()
    expect(w.find('[data-testid=group-holdings]').exists()).toBe(false)
  })

  it('speaks Indonesian', async () => {
    await loadLocale('id')
    await setLocale('id')
    const w = mountCard()
    await flushPromises()
    expect(w.get('[data-testid=holding-balance-1]').text()).toContain('Menahan deposit')
    expect(w.get('[data-testid=keep-note-1]').text()).toContain('Ditahan dulu')
    await w.get('[data-testid=decide-fee-1]').trigger('click')
    expect(w.get('[data-testid=decision-save-1]').text()).toBe('Catat biaya')
  })
})
