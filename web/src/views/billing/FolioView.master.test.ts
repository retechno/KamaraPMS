import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import FolioView from './FolioView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: vi.fn(), PATCH: vi.fn() } }))

const base = { opened_at: '', closed_at: null, version: 4, balance: '0', totals: { debit: '0', credit: '0' }, items: [], status: 'OPEN', bill_to_company_id: 8, bill_to_company_name: 'Acme Corp' }
const master = { ...base, id: 30, folio_number: 'FOL000030', folio_type: 'MASTER', reservation_id: null, stay_id: null, booking_group_id: 4, booking_group_code: 'GRP1', booking_group_name: 'Conference' }
const guest = { ...base, id: 11, folio_number: 'FOL000011', folio_type: 'GUEST', reservation_id: 41, stay_id: 5, booking_group_id: null, bill_to_company_id: null, bill_to_company_name: undefined }

const mounted: VueWrapper[] = []
function mountView(folio: object, permissions: string[], routing: object | null = null) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'clerk@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-09-30' } as never
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/stays/{id}')) return { data: { line: { id: 9 } } }
    if (path.endsWith('/routing')) return { data: routing }
    if (path.endsWith('/charge-codes') || path.endsWith('/folios') || path.endsWith('/transfers')) return { data: { data: [] } }
    return { data: folio }
  })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  const w = mount(FolioView, { props: { id: String((folio as { id: number }).id) }, global: { plugins: [pinia, router] }, attachTo: document.body })
  mounted.push(w)
  return w
}

describe('FolioView: the master folio of a group', () => {
  beforeEach(() => setLocale('en'))
  afterEach(() => {
    mounted.splice(0).forEach((w) => w.unmount())
    document.body.innerHTML = ''
  })

  it('names the group instead of a reservation, and links to it', async () => {
    const w = mountView(master, ['folio.read', 'folio.reverse'])
    await flushPromises()
    expect(w.get('[data-testid=folio-master]').text()).toBe('Master folio of group GRP1 · Conference')
    expect(w.get('[data-testid=group-link]').attributes('href')).toBe('/groups/4')
    expect(w.findAll('a').some((a) => (a.attributes('href') ?? '').startsWith('/reservations/'))).toBe(false)
    expect(w.get('[data-testid=folio-company]').text()).toContain('Acme Corp')
    // a folio without a reservation has no siblings to ask for
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/folios') && c[1]?.params?.query?.reservation_id !== undefined)).toBe(false)
  })

  it('is closed with its own right, not with the cashier right', async () => {
    const cashier = mountView(master, ['folio.read', 'folio.post_charge'])
    await flushPromises()
    expect(cashier.find('[data-testid=close]').exists()).toBe(false)
    const closer = mountView(master, ['folio.read', 'folio.master_close'])
    await flushPromises()
    expect(closer.find('[data-testid=close]').exists()).toBe(true)
    // an ordinary folio of no stay keeps the cashier right
    const orphan = mountView({ ...guest, stay_id: null }, ['folio.read', 'folio.post_charge'])
    await flushPromises()
    expect(orphan.find('[data-testid=close]').exists()).toBe(true)
  })

  it('says a master folio with a balance is not closed until it is paid or moved to the city ledger', async () => {
    const owing = mountView({ ...master, balance: '3300000' }, ['folio.read', 'folio.master_close'])
    await flushPromises()
    expect(owing.get('[data-testid=master-close-hint]').text()).toContain('must be zero before it closes')
    const settled = mountView(master, ['folio.read', 'folio.master_close'])
    await flushPromises()
    expect(settled.find('[data-testid=master-close-hint]').exists()).toBe(false)
  })

  it('says whose each line of the master is, and lets a charge be posted for a guest of the group', async () => {
    const line = (id: number, room: string, guest: string, over: object = {}) => ({ id, folio_id: 30, transaction_type: 'CHARGE', business_date: '2026-09-30', service_date: '2026-09-30', transaction_at: '2026-09-30T13:00:00Z', description: 'x', charge_code: 'MINIBAR', charge_code_id: 2, quantity: '1', unit_price: '1', price_mode: 'EXCLUSIVE', base_amount: '1', discount_amount: '0', net_amount: '1', rounding_adjustment: '0', service_charge_total: '0', tax_total: '0', debit: '1', credit: '0', payment_id: null, reversed_by_item_id: null, group_code: 'A', components: [], revenue_account_code: null, transfer: null, room_number: room, guest_name: guest, ...over })
    const withItems = { ...master, items: [line(1, '101', 'Ayu Peserta'), line(2, '', '', { stay_id: null })] }
    const routing = { group_id: 4, master: null, instructions: [], lines: [{ reservation_room_id: 1, reservation_id: 41, confirmation_number: 'RES000001', status: 'CHECKED_IN', excluded: false, stay_id: 5, room_number: '101', guest_name: 'Ayu Peserta' }, { reservation_room_id: 2, reservation_id: 42, confirmation_number: 'RES000002', status: 'CONFIRMED', excluded: false, stay_id: null }] }
    const w = mountView(withItems, ['folio.read', 'folio.post_charge', 'reservation.read'], routing)
    await flushPromises()
    expect(w.get('[data-testid=whose-1]').text()).toBe('room 101 · Ayu Peserta')
    expect(w.find('[data-testid=whose-2]').exists()).toBe(false)
    const options = w.findAll('[data-testid=charge-stay] option').map((o) => o.text())
    expect(options).toEqual(['The group (nobody in particular)', '101 · Ayu Peserta']) // only a room with a guest in it now
  })

  it('tells the folio of a member where the room charges go', async () => {
    const group = { id: 4, code: 'GRP1', name: 'Conference', master_folio_id: 30, master_folio_number: 'FOL000030', master_status: 'OPEN' }
    const routed = mountView(guest, ['folio.read', 'reservation.read'], { room: 'GROUP', excluded: false, group, group_instructions: [{ scope: 'ROOM', charge_code_id: null }] })
    await flushPromises()
    expect(routed.get('[data-testid=route-note]').text()).toBe('These charges go to the master folio FOL000030 of group GRP1: room charges.')
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/routing') && c[1].params.path.lineId === 9 && c[1].params.path.id === 41)).toBe(true)
    const plain = mountView(guest, ['folio.read', 'reservation.read'], { room: 'NONE', excluded: false, group: null, group_instructions: [] })
    await flushPromises()
    expect(plain.find('[data-testid=route-note]').exists()).toBe(false)
    const noRight = mountView(guest, ['folio.read'], { room: 'GROUP', excluded: false, group, group_instructions: [] })
    await flushPromises()
    expect(noRight.find('[data-testid=route-note]').exists()).toBe(false)
  })
})
