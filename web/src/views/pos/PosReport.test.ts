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
  discounts: [], comps: [], cashiers: [], waiters: [],
  drawer_opens: [{ id: 1, opened_at: '2026-10-15T06:30:00Z', business_date: '2026-10-15', reason: 'change for a guest', drawer: 'D1', shift_id: 4, opened_by: 'Ayu' }],
  room_charges: { count: 1, amount: '58730', returned: 0 }, open_bills: { count: 1, balance: '90000' },
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
    expect(w.get('[data-testid=method-CITY_LEDGER]').text()).toContain('To a company')
    expect(w.get('[data-testid=report-voids]').text()).toContain('approved by Wayan')
    expect(w.get('[data-testid=report-open]').text()).toContain('Open bills with items sold')
    expect(w.get('[data-testid=open-count]').text()).toBe('1')
    expect(w.get('[data-testid=open-balance]').text()).toBe('90,000')
    const q = GET.mock.calls.find((c) => String(c[0]).endsWith('/report'))![1].params.query
    expect(q).toEqual({ from: '2026-10-15', to: '2026-10-15' })
  })

  it('lists the times the drawer was opened without a sale, with who and why', async () => {
    const w = setup()
    await flushPromises()
    expect(w.get('[data-testid=drawer-count]').text()).toBe('(1)')
    const row = w.get('[data-testid=drawer-open-1]').text()
    expect(row).toContain('Ayu')
    expect(row).toContain('change for a guest')
    expect(row).toContain('drawer D1')
  })

  it('lists a charge to a room, a company and a complimentary as rows of the table like cash', async () => {
    const w = setup()
    await flushPromises()
    const cells = (k: string) => w.findAll(`[data-testid=method-${k}] td`).map((c) => c.text())
    expect(cells('CASH')).toEqual(['Cash', '1', '100,000'])
    expect(cells('CITY_LEDGER')).toEqual(['To a company', '1', '60,000'])
    expect(cells('ROOM')).toEqual(['Charged to a room', '1', '58,730'])
  })

  it('names what is missing in each empty block', async () => {
    const w = setup()
    await flushPromises()
    expect(w.get('[data-testid=empty-discounts]').text()).toContain('No discounts in this range')
    expect(w.get('[data-testid=empty-comps]').text()).toContain('No complimentary in this range')
    expect(w.find('[data-testid=empty-voids]').exists()).toBe(false)
  })
})

describe('the report of an outlet, what is seen is what is sent', () => {
  it('does not load while a date is half typed, and says why', async () => {
    const w = setup()
    await flushPromises()
    const before = GET.mock.calls.length
    await w.get('form input[data-slot=date-input-text]').setValue('3/2')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(GET.mock.calls.length).toBe(before)
    expect(w.text()).toContain('Write as dd/mm/yyyy')
  })
})
