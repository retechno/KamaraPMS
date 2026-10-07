import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ReservationDetailView from './ReservationDetailView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))

const night = (date: string, amount: string, over = false, yieldRules: string[] | null = null) => ({
  date, rate_plan_id: 1, charge_code_id: 1, price_mode: 'EXCLUSIVE', base_rate: yieldRules ? amount : '1000000', grid_rate: '1000000', yield_rules: yieldRules, discount_amount: '0', amount, is_override: over,
})
const line = (over: object = {}) => ({
  id: 4, status: 'CONFIRMED', room_type_id: 10, room_type_code: 'DLX', room_id: null, rate_plan_id: 1, rate_plan_code: 'BAR', occupancy_kind: 'PAID', guest_id: null,
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

function mountView(res: object = reservation(), permissions = ALL, answers: Record<string, unknown> = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-10-01' } as never
  GET = vi.fn(async (path: string) => {
    for (const [suffix, answer] of Object.entries(answers)) if (path.endsWith(suffix)) return { data: answer }
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

  it('names the yield rules that moved a night price', async () => {
    const w = mountView(reservation({ rooms: [line({ nightly_rates: [night('2026-10-02', '1200000', false, ['BUSY', 'WEEKEND']), night('2026-10-03', '1000000')] })] }))
    await flushPromises()
    expect(w.get('[data-testid=yield-2026-10-02]').text()).toContain('BUSY, WEEKEND')
    expect(w.find('[data-testid=yield-2026-10-03]').exists()).toBe(false)
    expect(w.get('[data-testid=nights-4]').text()).toContain('1,000,000') // the grid price stays visible beside the sold one
  })

  it('shows the header, the nightly rates with overrides, and the estimate', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=status]').text()).toBe('Confirmed')
    expect(w.get('[data-testid=booker]').text()).toBe('Siti Nurhaliza')
    const nights = w.get('[data-testid=nights-4]').text()
    expect(nights).toContain('900,000')
    expect(nights).toContain('override')
    expect(w.get('[data-testid=line-estimate-4]').text()).toBe('2,194,500')
    expect(w.get('[data-testid=estimate]').text()).toBe('2,194,500')
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

  it('shows the rules a confirmation breaks and sends it again with the override', async () => {
    const w = mountView(reservation({ status: 'DRAFT', display_status: 'DRAFT', version: 1, rooms: [line({ status: 'DRAFT' })] }), [...ALL, 'reservation.override_restriction', 'reservation.restriction_approve'])
    await flushPromises()
    POST.mockRejectedValueOnce(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'STAY_RESTRICTED', detail: 'the stay breaks a sales restriction',
      context: { violations: [{ type: 'STOP_SELL', date: '2026-10-02', room_type_id: 10, rate_plan_id: null, scope: 'ROOM_TYPE', row_id: 1 }], overridable: true } } as never))
    await w.get('[data-testid=confirm]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=restriction-refusal]').text()).toContain('The night of 2026-10-02 is closed for sale.')
    POST.mockResolvedValue({ data: reservation({ version: 2 }) })
    await w.get('input[name=restriction_reason]').setValue('the owner asked')
    await w.get('[data-testid=restriction-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls.at(-1)).toEqual(['/api/v1/properties/{propertyId}/reservations/{id}/confirm', { params: { path: { propertyId: 7, id: 1 } }, body: { version: 1, restriction_override: { reason: 'the owner asked' } } }])
    expect(w.find('[data-testid=restriction-refusal]').exists()).toBe(false)
    expect(w.get('[data-testid=status]').text()).toBe('Confirmed')
  })

  it('cancels only with a reason and reports what is left on the folios', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=cancel]').trigger('click')
    POST.mockRejectedValueOnce(new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'VALIDATION_FAILED', detail: 'a reason is required', errors: [{ field: 'reason', code: 'REQUIRED', message: 'a reason is required' }] }))
    await w.get('form[data-testid=reason-form]').trigger('submit')
    await flushPromises()
    expect(w.get('form[data-testid=reason-form] [role=alert]').text()).toContain('a reason is required')
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

  it('puts the main actions in the page header, by status and permission', async () => {
    const draft = mountView(reservation({ status: 'DRAFT', display_status: 'DRAFT' }))
    await flushPromises()
    expect(draft.find('header [data-testid=confirm]').exists()).toBe(true)
    expect(draft.find('header [data-testid=cancel]').exists()).toBe(true)
    expect(draft.find('[data-testid=print-confirmation]').exists()).toBe(false) // no confirmation of a draft
    const confirmed = mountView()
    await flushPromises()
    expect(confirmed.get('header [data-testid=print-confirmation]').text()).toContain('Confirmation (PDF)')
    expect(confirmed.find('[data-testid=confirm]').exists()).toBe(false)
    const cancelled = mountView(reservation({ status: 'CANCELLED', display_status: 'CANCELLED', cancellation_reason: 'changed plans' }))
    await flushPromises()
    expect(cancelled.find('header [data-testid=reinstate]').exists()).toBe(true)
    expect(cancelled.get('[data-testid=summary]').text()).toContain('Cancelled: changed plans')
  })

  it('shows each room with its status as a badge and the nights in the right number', async () => {
    const w = mountView(reservation({ rooms: [line({ nights: 1 }), line({ id: 5, status: 'CHECKED_IN', nights: 3, room_number: '301' })] }))
    await flushPromises()
    expect(w.get('[data-testid=line-status-4]').text()).toBe('Confirmed')
    expect(w.get('[data-testid=line-status-5]').text()).toBe('Checked in')
    expect(w.get('[data-testid=room-4]').text()).toContain('(1 night)')
    expect(w.get('[data-testid=room-5]').text()).toContain('(3 nights)')
    expect(w.get('[data-testid=room-number-5]').text()).toBe('room 301')
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=status]').text()).toBe('Terkonfirmasi')
    expect(w.get('[data-testid=room-4]').text()).toContain('belum ada kamar')
    expect(w.get('[data-testid=summary]').text()).toContain('Perkiraan total (kamar aktif)')
    expect(w.get('header [data-testid=cancel]').text()).toBe('Batalkan reservasi')
    setLocale('en')
  })

  it('shows the bed that was asked for, changes it and takes it off', async () => {
    const beds = { data: [{ id: 5, code: 'KING', name: 'King', is_active: true }, { id: 6, code: 'TWIN', name: 'Twin', is_active: true }] }
    const w = mountView(reservation({ rooms: [line({ bed_type_id: 5, bed_type_code: 'KING', bed_type_name: 'King' })] }), ALL, { '/bed-types': beds })
    await flushPromises()
    expect(w.get('[data-testid=bed-4]').text()).toContain('King')
    expect(w.findAll('select[name=bed_4] option').map((o) => o.text())).toEqual(['No preference', 'King', 'Twin'])
    expect((w.get('[data-testid=save-bed-4]').element as HTMLButtonElement).disabled).toBe(true) // nothing changed yet
    PATCH.mockResolvedValue({ data: reservation({ rooms: [line({ bed_type_id: 6, bed_type_code: 'TWIN', bed_type_name: 'Twin' })] }) })
    await w.get('select[name=bed_4]').setValue(6)
    await w.get('[data-testid=bed-form-4]').trigger('submit')
    await flushPromises()
    expect(PATCH).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}', {
      params: { path: { propertyId: 7, id: 1, lineId: 4 } }, body: { version: 2, bed_type_id: 6 },
    })
    expect(w.get('[data-testid=bed-4]').text()).toContain('Twin')
    await w.get('select[name=bed_4]').setValue(0)
    await w.get('[data-testid=bed-form-4]').trigger('submit')
    await flushPromises()
    expect(PATCH.mock.calls[1]?.[1].body).toMatchObject({ bed_type_id: 0 })
  })

  it('shows a complimentary room with its reason, and changes the reason', async () => {
    const free = line({ rate_plan_code: 'COMP', occupancy_kind: 'COMPLIMENTARY', occupancy_reason: 'Owner guest' })
    const w = mountView(reservation({ rooms: [free] }))
    await flushPromises()
    expect(w.get('[data-testid=kind-4]').text()).toBe('Complimentary')
    expect((w.get('input[name=occupancy_reason_4]').element as HTMLInputElement).value).toBe('Owner guest')
    expect((w.get('[data-testid=save-reason-4]').element as HTMLButtonElement).disabled).toBe(true)
    PATCH.mockResolvedValue({ data: reservation({ rooms: [{ ...free, occupancy_reason: 'Press trip' }] }) })
    await w.get('input[name=occupancy_reason_4]').setValue('Press trip')
    await w.get('[data-testid=reason-form-4]').trigger('submit')
    await flushPromises()
    expect(PATCH).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}', {
      params: { path: { propertyId: 7, id: 1, lineId: 4 } }, body: { version: 2, occupancy_reason: 'Press trip' },
    })
  })

  it('changes the price of the nights, asking for an approver when the person cannot approve', async () => {
    const perms = [...ALL]
    const w = mountView(reservation(), perms)
    await flushPromises()
    expect(w.find('[data-testid=rate-4]').exists()).toBe(false) // no override permission: no editor
    const w2 = mountView(reservation(), [...ALL, 'reservation.override_rate'])
    await flushPromises()
    await w2.get('[data-testid=rate-4] [data-testid=override-toggle]').trigger('click')
    await w2.get('input[name=override_amount_2026-10-02]').setValue('800000')
    expect(w2.find('[data-testid=save-rate-4]').attributes('disabled')).toBeDefined() // a reason first
    await w2.get('input[name=rate_override_reason]').setValue('Price match')
    PATCH.mockResolvedValue({ data: reservation() })
    await w2.get('[data-testid=save-rate-4]').trigger('click')
    await flushPromises()
    expect(PATCH).not.toHaveBeenCalled() // the approval dialog comes first
    const dialog = document.body.querySelector('[data-testid=approval-dialog]') as HTMLFormElement
    expect(dialog).not.toBeNull()
    ;(document.body.querySelector('input[name=approval_email]') as HTMLInputElement).value = 'boss@hotel.test'
    ;(document.body.querySelector('input[name=approval_email]') as HTMLInputElement).dispatchEvent(new Event('input'))
    ;(document.body.querySelector('input[name=approval_password]') as HTMLInputElement).value = 'secret'
    ;(document.body.querySelector('input[name=approval_password]') as HTMLInputElement).dispatchEvent(new Event('input'))
    await flushPromises()
    dialog.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(PATCH).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}', {
      params: { path: { propertyId: 7, id: 1, lineId: 4 } },
      body: { version: 2, nightly_overrides: [{ date: '2026-10-02', amount: '800000' }], rate_override_reason: 'Price match', rate_override_approval: { email: 'boss@hotel.test', password: 'secret' } },
    })
  })

  it('shows no kind or reason form on a paid room', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.find('[data-testid=kind-4]').exists()).toBe(false)
    expect(w.find('[data-testid=reason-form-4]').exists()).toBe(false)
  })

  it('lists the rooms with the bed that was asked for first, and marks them', async () => {
    const free = { data: [
      { room_id: 21, room_number: '101', housekeeping_status: 'CLEAN', bed_type_id: 6, bed_type_name: 'Twin' },
      { room_id: 22, room_number: '102', housekeeping_status: 'CLEAN', bed_type_id: 5, bed_type_name: 'King' },
      { room_id: 23, room_number: '103', housekeeping_status: 'DIRTY' },
    ] }
    const w = mountView(reservation({ rooms: [line({ bed_type_id: 5, bed_type_code: 'KING', bed_type_name: 'King' })] }), ALL, { '/availability/rooms': free })
    await flushPromises()
    await w.get('[data-testid=assign-4]').trigger('click')
    await flushPromises()
    expect(w.findAll('select[name=assign_room] option').map((o) => o.text())).toEqual(['102 · CLEAN · King ✓ matches the request', '101 · CLEAN · Twin', '103 · DIRTY'])
    expect((w.get('select[name=assign_room]').element as HTMLSelectElement).value).toBe('22') // the matching room is proposed
  })

  it('offers no bed change to a role that cannot update the reservation', async () => {
    const w = mountView(reservation({ rooms: [line({ bed_type_id: 5, bed_type_code: 'KING', bed_type_name: 'King' })] }), ['reservation.read'])
    await flushPromises()
    expect(w.get('[data-testid=bed-4]').text()).toContain('King')
    expect(w.find('[data-testid=bed-form-4]').exists()).toBe(false)
  })

  it('keeps and releases the bed of a line', async () => {
    const beds = { data: [{ id: 5, code: 'KING', name: 'King', is_active: true }, { id: 6, code: 'TWIN', name: 'Twin', is_active: true }] }
    const w = mountView(reservation({ rooms: [line({ bed_type_id: 5, bed_type_code: 'KING', bed_type_name: 'King', bed_locked: false })] }), ALL, { '/bed-types': beds })
    await flushPromises()
    expect(w.get('[data-testid=bed-4]').text()).not.toContain('kept')
    expect((w.get('[data-testid=save-bed-4]').element as HTMLButtonElement).disabled).toBe(true)
    PATCH.mockResolvedValue({ data: reservation({ rooms: [line({ bed_type_id: 5, bed_type_code: 'KING', bed_type_name: 'King', bed_locked: true })] }) })
    await w.get('input[name=bed_locked_4]').setValue(true)
    expect((w.get('[data-testid=save-bed-4]').element as HTMLButtonElement).disabled).toBe(false)
    await w.get('[data-testid=bed-form-4]').trigger('submit')
    await flushPromises()
    expect(PATCH.mock.calls[0]?.[1].body).toMatchObject({ bed_type_id: 5, bed_locked: true })
    expect(w.get('[data-testid=bed-4]').text()).toContain('kept')
    // no bed, no lock
    await w.get('select[name=bed_4]').setValue(0)
    expect(w.find('input[name=bed_locked_4]').exists()).toBe(false)
  })
})
