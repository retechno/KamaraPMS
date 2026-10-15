import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import OutletChargesView from './OutletChargesView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const rows = [
  { id: 4, source: 'CHARGE', folio_id: 31, folio_number: 'FOL000031', folio_type: 'GUEST', room_number: '304', guest_name: 'Intan Permata', outlet_id: 1, outlet_code: 'RESTO', outlet_name: 'Restoran Teratai',
    bill_number: 'R-0123', amount: '253750.000', result: 'ACCEPTED', code: null, context: {}, at: '2026-10-15T05:52:00Z' },
  { id: 3, source: 'CHARGE', folio_id: 32, folio_number: 'FOL000032', folio_type: 'GUEST', room_number: '305', guest_name: 'Budi Santoso', outlet_id: 2, outlet_code: 'BAR', outlet_name: 'Bar Kolam',
    bill_number: 'B-0045', amount: '110000.000', result: 'REJECTED', code: 'OUTLET_FOLIO_LOCKED', context: { reason: 'NEVER_OPENED' }, at: '2026-10-15T05:41:00Z' },
  { id: 2, source: 'CHARGE', folio_id: 33, folio_number: 'FOL000033', folio_type: 'GUEST', room_number: '214', guest_name: 'Sarah Lim', outlet_id: 3, outlet_code: 'RS', outlet_name: 'Room service',
    bill_number: 'RS-0045', amount: '181500.000', result: 'REJECTED', code: 'OUTLET_LIMIT_EXCEEDED', context: { remaining: '41270', limit: '100000', used: '58730' }, at: '2026-10-15T05:48:00Z' },
]
const mounted: VueWrapper[] = []
function setup(permissions = ['folio.outlet_charge_open'], data: object = { business_date: '2026-10-15', data: rows, accepted: 1, rejected: 2 }) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'fo@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  const w = mount(OutletChargesView, { global: { plugins: [pinia, router] } })
  mounted.push(w)
  return w
}

beforeEach(() => setLocale('en'))
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
})

describe('the outlet charges of the day', () => {
  it('lists what was charged and what was refused, with the reason of each refusal', async () => {
    const w = setup()
    await flushPromises()
    expect(w.findAll('[data-testid^=charge-][data-result]')).toHaveLength(3)
    const ok = w.get('[data-testid=charge-4]')
    expect(ok.text()).toContain('Room 304')
    expect(ok.text()).toContain('Restoran Teratai · R-0123 · Intan Permata')
    expect(ok.text()).toContain('253,750')
    expect(w.get('[data-testid=charge-3] [data-testid=charge-why]').text()).toContain('never opened')
    expect(w.get('[data-testid=charge-2] [data-testid=charge-why]').text()).toContain('remaining 41,270')
  })

  it('filters to the refused ones, and offers the folio of each', async () => {
    const w = setup()
    await flushPromises()
    await w.get('[data-testid=filter-REJECTED]').trigger('click')
    expect(w.findAll('[data-testid^=charge-][data-result]')).toHaveLength(2)
    expect(w.get('[data-testid=charge-open-3]').attributes('href')).toBe('/folios/32')
    expect(w.find('[data-testid=charge-open-4]').exists()).toBe(false)
  })

  it('does not offer to open a folio to who may only read them', async () => {
    const w = setup(['folio.read'])
    await flushPromises()
    expect(w.find('[data-testid=charge-open-3]').exists()).toBe(false)
  })

  it('says so when nothing came in today, and keeps the header and the filters', async () => {
    const w = setup(['folio.outlet_charge_open'], { business_date: '2026-10-15', data: [], accepted: 0, rejected: 0 })
    await flushPromises()
    expect(w.get('[data-testid=charges-empty]').text()).toContain('No outlet charges today')
    expect(w.find('[data-testid=charges-table]').exists()).toBe(true)
    expect(w.find('[data-testid=charges-filter]').exists()).toBe(true)
    expect(w.find('[data-testid=charges]').exists()).toBe(false)
    expect(w.find('[data-testid=empty-action]').exists()).toBe(false) // there is nothing to clear
  })

  it('speaks Indonesian when nothing came in', async () => {
    setLocale('id')
    const w = setup(['folio.outlet_charge_open'], { business_date: '2026-10-15', data: [], accepted: 0, rejected: 0 })
    await flushPromises()
    expect(w.get('[data-testid=charges-empty]').text()).toContain('Belum ada beban dari outlet hari ini')
  })

  it('offers to clear a filter that matches nothing', async () => {
    const w = setup(['folio.outlet_charge_open'], { business_date: '2026-10-15', data: [rows[0]], accepted: 1, rejected: 0 })
    await flushPromises()
    await w.get('[data-testid=filter-REJECTED]').trigger('click')
    expect(w.get('[data-testid=charges-empty]').text()).toContain('Nothing matches this filter')
    await w.get('[data-testid=empty-action]').trigger('click')
    expect(w.findAll('[data-testid^=charge-][data-result]')).toHaveLength(1)
  })

  it('reads again by itself', async () => {
    vi.useFakeTimers()
    try {
      setup()
      await flushPromises()
      expect(GET).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(20_500)
      expect(GET).toHaveBeenCalledTimes(2)
    } finally {
      vi.useRealTimers()
    }
  })
})
