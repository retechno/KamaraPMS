import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import StayDetailView from './StayDetailView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const detail = (over: object = {}) => ({
  stay: { id: 5, stay_number: 'STY000001', arrival_date: '2026-09-30', departure_date: '2026-10-02', adult_count: 2, child_count: 0, status: 'OPEN', version: 3 },
  guest: { id: 3, code: 'G1', first_name: 'Siti', last_name: 'Nurhaliza' }, guests: [{ id: 4, code: 'G2', first_name: 'Budi', last_name: 'Santoso' }],
  segments: [{ id: 1, room_id: 21, room_number: '101', start_business_date: '2026-09-30', end_business_date: null }],
  line: { id: 4, reservation_id: 9, confirmation_number: 'RES000009', room_type_code: 'DLX', status: 'CHECKED_IN' },
  nightly_rates: [{ date: '2026-09-30', amount: '1000000', price_mode: 'EXCLUSIVE', is_override: false, posted: false }],
  folios: [{ id: 8, folio_number: 'FOL000001', status: 'OPEN', balance: '-100000' }], ...over,
})

function mountView(d: object = detail(), permissions = ['reservation.read', 'frontdesk.reverse_checkin']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-09-30' } as never
  GET = vi.fn().mockResolvedValue({ data: d })
  POST = vi.fn().mockResolvedValue({ data: {} })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(StayDetailView, { props: { id: '5' }, global: { plugins: [pinia, router] } })
}

describe('StayDetailView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
  })

  it('shows the stay, companions, segments, nights and the folio link', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=stay-status]').text()).toBe('OPEN')
    expect(w.get('[data-testid=companions]').text()).toContain('Budi Santoso')
    expect(w.get('[data-testid=segments]').text()).toContain('Room 101 from 2026-09-30')
    expect(w.get('[data-testid=nights]').text()).toContain('not yet')
    expect(w.get('[data-testid=folio-8]').attributes('href')).toBe('/folios/8')
  })

  it('reverses the check-in only with a reason, on the arrival day, with the stay version', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=reverse]').trigger('click')
    expect(w.get('form[data-testid=reverse-form] button[type=submit]').attributes('disabled')).toBeDefined()
    await w.get('input[name=reason]').setValue('wrong guest')
    await w.get('form[data-testid=reverse-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]).toEqual(['/api/v1/properties/{propertyId}/stays/{id}/reverse-check-in', { params: { path: { propertyId: 7, id: 5 } }, body: { version: 3, reason: 'wrong guest' } }])
    expect(w.get('[data-testid=notice]').text()).toContain('reversed')
  })

  it('hides the reversal after the arrival day, after a room move and without permission', async () => {
    const later = mountView(detail({ stay: { ...detail().stay, arrival_date: '2026-09-29' } }))
    await flushPromises()
    expect(later.find('[data-testid=reverse]').exists()).toBe(false)
    const moved = mountView(detail({ segments: [{ id: 1, room_id: 21, room_number: '101', start_business_date: '2026-09-30', end_business_date: '2026-09-30' }, { id: 2, room_id: 22, room_number: '102', start_business_date: '2026-09-30', end_business_date: null }] }))
    await flushPromises()
    expect(moved.find('[data-testid=reverse]').exists()).toBe(false)
    const plain = mountView(detail(), ['reservation.read'])
    await flushPromises()
    expect(plain.find('[data-testid=reverse]').exists()).toBe(false)
  })

  it('shows the server refusal', async () => {
    const w = mountView()
    await flushPromises()
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'CHECK_IN_HAS_CHARGES', detail: 'charges are posted' }))
    await w.get('[data-testid=reverse]').trigger('click')
    await w.get('input[name=reason]').setValue('x')
    await w.get('form[data-testid=reverse-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('CHECK_IN_HAS_CHARGES')
  })

  it('opens the check-out wizard for an open stay and hides it once checked out or without permission', async () => {
    const w = mountView(detail(), ['reservation.read', 'frontdesk.checkout'])
    await flushPromises()
    await w.get('[data-testid=checkout]').trigger('click')
    expect(w.find('[data-testid=checkout-wizard]').exists()).toBe(true)
    await w.get('[data-testid=checkout-cancel]').trigger('click')
    expect(w.find('[data-testid=checkout-wizard]').exists()).toBe(false)
    const gone = mountView(detail({ stay: { ...detail().stay, status: 'CHECKED_OUT' } }), ['reservation.read', 'frontdesk.checkout'])
    await flushPromises()
    expect(gone.find('[data-testid=checkout]').exists()).toBe(false)
    const plain = mountView()
    await flushPromises()
    expect(plain.find('[data-testid=checkout]').exists()).toBe(false)
  })
})
