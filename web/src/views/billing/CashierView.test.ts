import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
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

function mountView(permissions = ['folio.read']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-09-30' } as never
  GET = vi.fn().mockResolvedValue({ data: page })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(CashierView, { global: { plugins: [pinia, router] } })
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
    expect(w.get('[data-testid=total-CASH]').text()).toContain('100000')
    expect(w.get('[data-testid=payment-PAY000002]').classes()).toContain('struck') // voided
    expect(w.get('[data-testid=payment-PAY000001]').text()).toContain('POSTED')
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
})
