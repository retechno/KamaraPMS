import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import CashierView from './CashierView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))
let openPdf = vi.fn()
vi.mock('@/utils/documents', async (orig) => ({ ...(await orig<typeof import('@/utils/documents')>()), openPdf: (...a: unknown[]) => openPdf(...a) }))

const page = {
  data: [
    { id: 2, payment_number: 'PAY000002', folio_id: 3, payment_type: 'PAYMENT', payment_method: 'CARD', amount: '50000', status: 'VOIDED' },
    { id: 1, payment_number: 'PAY000001', folio_id: 3, payment_type: 'PAYMENT', payment_method: 'CASH', amount: '100000', status: 'POSTED' },
  ],
  totals: [{ payment_method: 'CASH', paid: '100000', refunded: '0', net: '100000' }],
}

function mountView(permissions = ['folio.read'], get?: typeof GET) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-09-30' } as never
  GET = get ?? vi.fn().mockResolvedValue({ data: page })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(CashierView, { global: { plugins: [pinia, router] } })
}

/** Runs `fn` with the window of a phone (390 px), then puts the width back. */
async function onAPhone<T>(fn: () => Promise<T>): Promise<T> {
  const wide = window.innerWidth
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 })
  try {
    return await fn()
  } finally {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: wide })
  }
}

describe('CashierView', () => {
  beforeEach(() => {
    openPdf = vi.fn().mockResolvedValue(undefined)
    GET = vi.fn()
  })

  it('shows the business date payments and the totals per method', async () => {
    const w = mountView()
    await flushPromises()
    expect(GET.mock.calls[0]?.[1]).toMatchObject({ params: { path: { propertyId: 7 }, query: { business_date: '2026-09-30' } } })
    expect(w.get('[data-testid=total-CASH]').text()).toContain('100,000')
    expect(w.get('[data-testid=payment-PAY000002]').classes()).toContain('struck') // voided
    expect(w.get('[data-testid=payment-PAY000001]').text()).toContain('Posted')
  })

  it('filters by date and method', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('input[name=business_date]').setValue('2026-09-29')
    await w.get('select[name=method]').setValue('CARD')
    await w.get('form[role=search]').trigger('submit')
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1]).toMatchObject({ params: { query: { business_date: '2026-09-29', method: 'CARD' } } })
  })

  it('needs folio.read', async () => {
    const w = mountView(['guest.read'])
    await flushPromises()
    expect(w.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })

  it('prints the receipt of a payment, for roles that can read reservations', async () => {
    const w = mountView(['folio.read', 'reservation.read'])
    await flushPromises()
    const button = w.get('[data-testid^=receipt-]')
    await button.trigger('click')
    expect(openPdf.mock.calls[0]?.[0]).toMatch(/^\/api\/v1\/properties\/7\/payments\/\d+\/receipt\.pdf$/)
    const plain = mountView()
    await flushPromises()
    expect(plain.find('[data-testid^=receipt-]').exists()).toBe(false)
  })

  it('shows each method as a figure with what was paid and refunded', async () => {
    const w = mountView()
    GET.mockResolvedValue({ data: { ...page, totals: [{ payment_method: 'CASH', paid: '100000', refunded: '20000', net: '80000' }, { payment_method: 'BANK_TRANSFER', paid: '500000', refunded: '0', net: '500000' }] } })
    await flushPromises()
    await w.get('form[role=search]').trigger('submit')
    await flushPromises()
    const cash = w.get('[data-testid=total-CASH]').text()
    expect(cash).toContain('Cash')
    expect(cash).toContain('80,000')
    expect(cash).toContain('Paid 100,000 · Refunded 20,000')
    expect(w.get('[data-testid=total-BANK_TRANSFER]').text()).toContain('Bank transfer')
  })

  it('marks a refund and strikes a voided payment', async () => {
    const w = mountView()
    GET.mockResolvedValue({ data: { data: [{ id: 5, payment_number: 'PAY000005', folio_id: 3, payment_type: 'REFUND', payment_method: 'CASH', amount: '30000', status: 'POSTED' }, ...page.data], totals: [] } })
    await w.get('form[role=search]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=payment-PAY000005]').text()).toContain('Refund')
    expect(w.get('[data-testid=payment-PAY000005]').classes()).not.toContain('struck')
    expect(w.get('[data-testid=payment-PAY000002]').classes()).toContain('line-through')
    expect(w.get('[data-testid=payment-PAY000002]').text()).toContain('Voided')
  })

  it('sorts the payments by amount', async () => {
    const w = mountView()
    await flushPromises()
    const numbers = () => w.findAll('tbody tr').map((r) => r.findAll('td')[0]!.text())
    expect(numbers()).toEqual(['PAY000002', 'PAY000001'])
    await w.get('[data-testid=sort-amount]').trigger('click')
    expect(numbers()).toEqual(['PAY000002', 'PAY000001']) // 50000 then 100000
    await w.get('[data-testid=sort-amount]').trigger('click')
    expect(numbers()).toEqual(['PAY000001', 'PAY000002'])
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    const w = mountView()
    await flushPromises()
    expect(w.get('h1').text()).toBe('Kasir')
    expect(w.get('[data-testid=total-CASH]').text()).toContain('Tunai')
    expect(w.get('[data-testid=payment-PAY000001]').text()).toContain('Terposting')
    setLocale('en')
  })

  it('is a card on a phone, with the receipt as its main action', async () => {
    await onAPhone(async () => {
      const w = mountView(['folio.read', 'reservation.read'])
      await flushPromises()
      expect(w.find('table').exists()).toBe(false)
      const card = w.get('[data-testid=payment-PAY000001]')
      expect(card.text()).toContain('PAY000001')
      expect(card.text()).toContain('Posted')
      await card.get('[data-testid=receipt-PAY000001]').trigger('click')
      expect(openPdf).toHaveBeenCalled()
      w.unmount()
    })
  })

  it('keeps the receipt as a button in the row of the table', async () => {
    const w = mountView(['folio.read', 'reservation.read'])
    await flushPromises()
    expect(w.get('[data-testid=payment-PAY000001] [data-testid=receipt-PAY000001]').text()).toBe('Receipt')
  })

  it('has a short bar on a phone: "Filter" with the number of filters that are on (another day, a method), and them in a sheet', async () => {
    await onAPhone(async () => {
      const w = mountView()
      await flushPromises()
      expect(w.get('[data-testid=open-filters]').text()).toBe('Filter') // the day shown to begin with is not a filter
      expect(w.find('input[name=business_date]').exists()).toBe(false)
      await w.get('[data-testid=open-filters]').trigger('click')
      await flushPromises()
      const sheet = document.body.querySelector('[data-testid=filter-sheet]')!
      const method = sheet.querySelector('select[name=method]') as HTMLSelectElement
      method.value = 'CASH'
      method.dispatchEvent(new Event('change'))
      await flushPromises()
      expect(w.get('[data-testid=filter-count]').text()).toBe('(1)')
      w.unmount()
      document.body.innerHTML = ''
    })
  })

  it('writes every figure of the summary through the money formatter, with "Dikembalikan" for what was refunded', async () => {
    setLocale('id')
    const w = mountView()
    GET.mockResolvedValue({ data: { ...page, totals: [{ payment_method: 'CASH', paid: '1500000', refunded: '250000', net: '1250000' }] } })
    await flushPromises()
    await w.get('form[role=search]').trigger('submit')
    await flushPromises()
    const cash = w.get('[data-testid=total-CASH]').text()
    expect(cash).toContain('1.250.000')
    expect(cash).toContain('Dibayar 1.500.000 · Dikembalikan 250.000')
    expect(cash).not.toMatch(/\d{7}/) // no figure without its thousands separators
    expect(cash).not.toContain('Direfund')
    setLocale('en')
  })

  it('shows the summary cards at every width: a phone, a laptop and a desktop', async () => {
    for (const width of [390, 1366, 1440]) {
      const wide = window.innerWidth
      Object.defineProperty(window, 'innerWidth', { configurable: true, value: width })
      const w = mountView()
      await flushPromises()
      expect(w.find('[data-testid=totals]').exists(), `${width}px`).toBe(true)
      expect(w.get('[data-testid=total-CASH]').text(), `${width}px`).toContain('100,000')
      w.unmount()
      Object.defineProperty(window, 'innerWidth', { configurable: true, value: wide })
    }
  })

  it('keeps the totals of the search that is shown when an older search answers last', async () => {
    let answerFirst: (v: unknown) => void = () => undefined
    const slow = new Promise((resolve) => {
      answerFirst = resolve
    })
    const get = vi.fn().mockReturnValueOnce(slow).mockResolvedValue({ data: page })
    const w = mountView(['folio.read'], get)
    await flushPromises()
    expect(w.find('[data-testid=totals]').exists()).toBe(false) // the first search is still on its way
    await w.get('form[role=search]').trigger('submit') // a second search, which answers first
    await flushPromises()
    expect(w.get('[data-testid=total-CASH]').text()).toContain('100,000')
    answerFirst({ data: { data: [], totals: [] } }) // the older one answers last, without totals
    await flushPromises()
    expect(w.get('[data-testid=total-CASH]').text()).toContain('100,000')
    expect(w.find('[data-testid=payment-PAY000001]').exists()).toBe(true) // and its rows are not shown either
  })

  it('names the folio of a payment by its number, with a link to it, and never as "#id"', async () => {
    const w = mountView()
    GET.mockResolvedValue({ data: { data: [
      { id: 6, payment_number: 'PAY000006', folio_id: 26, folio_number: 'FOL000026', payment_type: 'PAYMENT', payment_method: 'CASH', amount: '1000', status: 'POSTED' },
      { id: 7, payment_number: 'PAY000007', folio_id: 31, payment_type: 'PAYMENT', payment_method: 'CASH', amount: '1000', status: 'POSTED' }, // an answer without the number
    ], totals: [] } })
    await flushPromises()
    await w.get('form[role=search]').trigger('submit')
    await flushPromises()
    const link = w.get('[data-testid=payment-PAY000006] a[href="/folios/26"]')
    expect(link.text()).toBe('FOL000026')
    expect(w.get('[data-testid=payment-PAY000006]').text()).not.toContain('#26')
    expect(w.get('[data-testid=payment-PAY000007] a[href="/folios/31"]').text()).toBe('#31') // the old way when the API gives no number
  })
})
