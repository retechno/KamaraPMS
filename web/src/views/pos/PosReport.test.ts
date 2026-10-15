import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import PosReportView from './PosReportView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const report = {
  outlet_id: 1, outlet_name: 'Restoran', from: '2026-10-15', to: '2026-10-15',
  days: [{ business_date: '2026-10-15', bills: 2, net: '215000', service: '21500', tax: '23650', rounding: '50', total: '260200' }],
  totals: { business_date: '2026-10-15', bills: 2, net: '215000', service: '21500', tax: '23650', rounding: '50', total: '260200' },
  products: [{ product_id: 1, name: 'Nasi goreng', category: 'Makanan', quantity: '3', base: '150000', discount: '0', net: '150000', service: '15000', tax: '16500', total: '181500' }],
  methods: [{ method: 'CASH', payments: 1, amount: '100000' }, { method: 'CITY_LEDGER', payments: 1, amount: '60000' }],
  voids: [{ business_date: '2026-10-15', bill_number: 'R-0001', what: 'Steak', amount: '181500', reason: 'wrong order', by: 'Ketut', approved_by: 'Wayan' }],
  discounts: [], comps: [], cashiers: [], waiters: [], room_charges: { count: 1, amount: '58730', returned: 0 }, open_bills: { count: 1, balance: '90000' },
}

const mounted: VueWrapper[] = []
function setup() {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'm@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: ['pos.report'] }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.current = { id: 7, currency_decimals: 0, business_date: '2026-10-15' } as never
  GET = vi.fn(async (path: string) => (path.endsWith('/pos/outlets') ? { data: { data: [{ id: 1, name: 'Restoran' }] } } : { data: report }))
  const w = mount(PosReportView, { global: { plugins: [pinia] } })
  mounted.push(w)
  return w
}

beforeEach(async () => {
  localStorage.clear()
  await setLocale('en')
})
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
})

describe('the report of an outlet', () => {
  it('shows the totals, the payments by method and the voids with who approved them', async () => {
    const w = setup()
    await flushPromises()
    expect(w.get('[data-testid=total-net]').text()).toBe('215,000')
    expect(w.get('[data-testid=total-total]').text()).toBe('260,200')
    expect(w.get('[data-testid=report-methods]').text()).toContain('Company')
    expect(w.get('[data-testid=report-voids]').text()).toContain('approved by Wayan')
    expect(w.get('[data-testid=report-room]').text()).toContain('58,730')
    expect(w.get('[data-testid=report-open]').text()).toContain('90,000')
    const q = GET.mock.calls.find((c) => String(c[0]).endsWith('/report'))![1].params.query
    expect(q).toEqual({ from: '2026-10-15', to: '2026-10-15' })
  })
})
