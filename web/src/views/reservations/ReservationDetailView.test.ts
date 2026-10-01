import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ReservationDetailView from './ReservationDetailView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))

const night = (date: string, amount: string, over = false) => ({ date, rate_plan_id: 1, charge_code_id: 1, price_mode: 'EXCLUSIVE', base_rate: '1000000', discount_amount: '0', amount, is_override: over })
const line = (over: object = {}) => ({
  id: 4, status: 'CONFIRMED', room_type_id: 10, room_type_code: 'DLX', room_id: null, rate_plan_id: 1, rate_plan_code: 'BAR', guest_id: null,
  arrival_date: '2026-10-02', departure_date: '2026-10-04', nights: 2, adult_count: 2, child_count: 0, stay_id: null,
  nightly_rates: [night('2026-10-02', '1000000'), night('2026-10-03', '900000', true)],
  estimate: { net: '1900000', service: '95000', tax: '199500', total: '2194500' }, ...over,
})
const reservation = (over: object = {}) => ({
  id: 1, confirmation_number: 'RES000001', guest_id: 3, guest: { id: 3, code: 'GST000001', first_name: 'Siti', last_name: 'Nurhaliza' },
  reservation_date: '2026-09-30', source: 'PHONE', status: 'CONFIRMED', display_status: 'CONFIRMED', arrival_date: '2026-10-02', departure_date: '2026-10-04',
  version: 2, rooms: [line()], folios: [], created_at: '2026-09-30T13:00:00Z', ...over,
})
const types = [{ id: 10, code: 'DLX', is_active: true }, { id: 11, code: 'STD', is_active: true }]

const ALL = ['payment.post', 'reservation.read', 'reservation.create', 'reservation.update', 'reservation.cancel', 'reservation.reinstate', 'nightaudit.no_show']

function mountView(res: object = reservation(), permissions = ALL) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-10-01' } as never
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/room-types')) return { data: { data: types } }
    if (path.endsWith('/availability/rooms')) return { data: { data: [{ room_id: 21, room_number: '101', housekeeping_status: 'CLEAN' }] } }
    return { data: res }
  })
  POST = vi.fn()
  PATCH = vi.fn()
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(ReservationDetailView, { props: { id: '1' }, global: { plugins: [pinia, router] } })
}

describe('ReservationDetailView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
    PATCH = vi.fn()
  })

  it('shows the header, the nightly rates with overrides, and the estimate', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=status]').text()).toBe('Confirmed')
    expect(w.get('[data-testid=booker]').text()).toBe('Siti Nurhaliza')
    const nights = w.get('[data-testid=nights-4]').text()
    expect(nights).toContain('900000')
    expect(nights).toContain('override')
    expect(w.get('[data-testid=line-estimate-4]').text()).toBe('2194500')
    expect(w.get('[data-testid=estimate]').text()).toBe('2194500')
    expect(w.get('[data-testid=room-4]').text()).toContain('no room assigned')
  })

  it('links the company and the group, the company only to roles that can see the city ledger', async () => {
    const corporate = reservation({ company_id: 21, company_name: 'Acme Corp', booking_group_id: 4, group_code: 'CONF' })
    const w = mountView(corporate, [...ALL, 'cityledger.read'])
    await flushPromises()
    expect(w.get('[data-testid=company-link]').attributes('href')).toBe('/city-ledger/21')
    expect(w.get('[data-testid=company-link]').text()).toBe('Acme Corp')
    expect(w.get('[data-testid=group-link]').attributes('href')).toBe('/groups/4')
    expect(w.get('[data-testid=group-link]').text()).toBe('CONF')
    const desk = mountView(corporate)
    await flushPromises()
    expect(desk.get('[data-testid=company-link]').element.tagName).toBe('SPAN')
    expect(mountView().find('[data-testid=billing-links]').exists()).toBe(false)
  })

  it('confirms a draft with the loaded version and shows the answer', async () => {
    const w = mountView(reservation({ status: 'DRAFT', display_status: 'DRAFT', version: 1, rooms: [line({ status: 'DRAFT' })] }))
    await flushPromises()
    POST.mockResolvedValue({ data: reservation({ version: 2 }) })
    await w.get('[data-testid=confirm]').trigger('click')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/reservations/{id}/confirm', { params: { path: { propertyId: 7, id: 1 } }, body: { version: 1 } })
    expect(w.get('[data-testid=status]').text()).toBe('Confirmed')
    expect(w.find('[data-testid=confirm]').exists()).toBe(false)
  })

  it('cancels only with a reason and reports what is left on the folios', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=cancel]').trigger('click')
    POST.mockRejectedValueOnce(new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'VALIDATION_FAILED', detail: 'a reason is required', errors: [{ field: 'reason', code: 'REQUIRED', message: 'a reason is required' }] }))
    await w.get('form[data-testid=reason-form]').trigger('submit')
    await flushPromises()
    expect(w.get('.error-text').text()).toContain('a reason is required')
    POST.mockResolvedValue({ data: { reservation: reservation({ status: 'CANCELLED', display_status: 'CANCELLED', version: 3 }), folio_balance: '-300000', requires_folio_resolution: true } })
    await w.get('input[name=reason]').setValue('guest request')
    await w.get('form[data-testid=reason-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls.at(-1)).toEqual(['/api/v1/properties/{propertyId}/reservations/{id}/cancel', { params: { path: { propertyId: 7, id: 1 } }, body: { version: 2, reason: 'guest request' } }])
    expect(w.get('[data-testid=status]').text()).toBe('Cancelled')
    expect(w.get('[data-testid=notice]').text()).toContain('-300000')
    expect(w.find('[data-testid=reinstate]').exists()).toBe(true)
    expect(w.find('[data-testid=cancel]').exists()).toBe(false)
  })

  it('assigns a free room of the booked type, and marks an upgrade when another type is chosen', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=assign-4]').trigger('click')
    await flushPromises()
    expect(GET.mock.calls.at(-1)).toEqual(['/api/v1/properties/{propertyId}/availability/rooms', { params: { path: { propertyId: 7 }, query: { room_type_id: 10, arrival: '2026-10-02', departure: '2026-10-04' } } }])
    POST.mockResolvedValue({ data: reservation({ version: 3, rooms: [line({ room_id: 21, room_number: '101' })] }) })
    await w.get('form[data-testid=assign-form-4]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls.at(-1)?.[1]).toMatchObject({ body: { version: 2, room_id: 21, upgrade: false } })
    expect(w.get('[data-testid=room-number-4]').text()).toBe('room 101')
    expect(w.find('[data-testid=unassign-4]').exists()).toBe(true)

    // upgrade: another type in the picker
    const w2 = mountView()
    await flushPromises()
    await w2.get('[data-testid=assign-4]').trigger('click')
    await flushPromises()
    await w2.get('select[name=assign_type]').setValue(11)
    await flushPromises()
    POST.mockResolvedValue({ data: reservation({ version: 3 }) })
    await w2.get('form[data-testid=assign-form-4]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls.at(-1)?.[1]).toMatchObject({ body: { upgrade: true } })
  })

  it('offers no-show only for rooms whose arrival has come', async () => {
    const w = mountView(reservation({ rooms: [line({ arrival_date: '2026-10-01' }), line({ id: 5, arrival_date: '2026-10-05', departure_date: '2026-10-06' })] }))
    await flushPromises()
    expect(w.find('[data-testid=no-show-4]').exists()).toBe(true)
    expect(w.find('[data-testid=no-show-5]').exists()).toBe(false)
  })

  it('reloads after a version conflict', async () => {
    const w = mountView()
    await flushPromises()
    PATCH.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'VERSION_CONFLICT', detail: 'changed by someone else' }))
    GET.mockImplementation(async (path: string) => (path.endsWith('/room-types') ? { data: { data: types } } : { data: reservation({ version: 5 }) }))
    await w.get('input[name=remarks]').setValue('late arrival')
    await w.get('form[data-testid=header-form]').trigger('submit')
    await flushPromises()
    expect(PATCH.mock.calls[0]?.[1]).toMatchObject({ body: { version: 2, remarks: 'late arrival' } })
    expect(w.get('[data-testid=form-error]').text()).toContain('VERSION_CONFLICT')
    expect(w.text()).toContain('version 5')
  })

  it('takes a deposit with an Idempotency-Key and links the folio', async () => {
    const w = mountView(reservation({ folios: [{ id: 3, folio_number: 'FOL000001', stay_id: null, status: 'OPEN', balance: '-100000' }] }))
    await flushPromises()
    expect(w.get('[data-testid=folio-link-3]').attributes('href')).toBe('/folios/3')
    POST.mockResolvedValue({ data: { payment: { payment_number: 'PAY000001' }, folio_balance: '-600000' } })
    await w.get('input[name=deposit_amount]').setValue('500000')
    await w.get('select[name=deposit_method]').setValue('BANK_TRANSFER')
    await w.get('form[data-testid=deposit-form]').trigger('submit')
    await flushPromises()
    const [path, init] = POST.mock.calls[0] as [string, { params: { header: Record<string, string> }; body: object }]
    expect(path).toBe('/api/v1/properties/{propertyId}/reservations/{id}/deposits')
    expect(init.params.header['Idempotency-Key']).toBeTruthy()
    expect(init.body).toMatchObject({ amount: '500000', payment_method: 'BANK_TRANSFER' })
    expect(w.get('[data-testid=notice]').text()).toContain('PAY000001')
  })

  it('hides actions the role does not have', async () => {
    const w = mountView(reservation(), ['reservation.read'])
    await flushPromises()
    expect(w.find('[data-testid=cancel]').exists()).toBe(false)
    expect(w.find('[data-testid=assign-4]').exists()).toBe(false)
    expect(w.find('form[data-testid=header-form]').exists()).toBe(false)
    expect(w.find('form[data-testid=deposit-form]').exists()).toBe(false)
    expect(w.get('[data-testid=status]').text()).toBe('Confirmed')
  })
})
