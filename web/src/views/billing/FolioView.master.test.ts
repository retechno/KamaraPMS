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

  it('tells the folio of a member where the room charges go', async () => {
    const group = { id: 4, code: 'GRP1', name: 'Conference', master_folio_id: 30, master_folio_number: 'FOL000030', master_status: 'OPEN' }
    const routed = mountView(guest, ['folio.read', 'reservation.read'], { room: 'GROUP', excluded: false, group, group_instructions: [{ scope: 'ROOM', charge_code_id: null }] })
    await flushPromises()
    expect(routed.get('[data-testid=route-note]').text()).toBe('The room charges go to the master folio FOL000030 of group GRP1.')
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/routing') && c[1].params.path.lineId === 9 && c[1].params.path.id === 41)).toBe(true)
    const plain = mountView(guest, ['folio.read', 'reservation.read'], { room: 'NONE', excluded: false, group: null, group_instructions: [] })
    await flushPromises()
    expect(plain.find('[data-testid=route-note]').exists()).toBe(false)
    const noRight = mountView(guest, ['folio.read'], { room: 'GROUP', excluded: false, group, group_instructions: [] })
    await flushPromises()
    expect(noRight.find('[data-testid=route-note]').exists()).toBe(false)
  })
})
