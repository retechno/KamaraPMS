import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ReservationDetailView from './ReservationDetailView.vue'

// The reservation as a workspace: header, guest, stay, rate, company and billing, payment and folio, notes and history; the actions by status; the editors it reuses.

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))

const night = (date: string, amount: string) => ({ date, rate_plan_id: 1, charge_code_id: 1, price_mode: 'EXCLUSIVE', base_rate: '1000000', grid_rate: '1000000', yield_rules: null, discount_amount: '0', amount, is_override: false })
const line = (over: object = {}) => ({
  id: 4, status: 'CONFIRMED', room_type_id: 10, room_type_code: 'DLX', room_id: null, rate_plan_id: 1, rate_plan_code: 'BAR', occupancy_kind: 'PAID', guest_id: null,
  arrival_date: '2026-10-01', departure_date: '2026-10-03', nights: 2, adult_count: 2, child_count: 0, stay_id: null,
  nightly_rates: [night('2026-10-01', '1000000'), night('2026-10-02', '1000000')], estimate: { net: '2000000', service: '0', tax: '0', total: '2000000' }, ...over,
})
const reservation = (over: object = {}) => ({
  id: 1, confirmation_number: 'RES000001', guest_id: 3, guest: { id: 3, code: 'GST000001', first_name: 'Siti', last_name: 'Nurhaliza' },
  reservation_date: '2026-09-30', source: 'PHONE', status: 'CONFIRMED', display_status: 'CONFIRMED', arrival_date: '2026-10-01', departure_date: '2026-10-03',
  version: 2, rooms: [line()], folios: [], created_at: '2026-09-30T13:00:00Z', ...over,
})
const guestView = { id: 3, code: 'GST000001', first_name: 'Siti', last_name: 'Nurhaliza', email: 'siti@example.com', phone: '+62811', can_edit: true }
const types = [{ id: 10, code: 'DLX', is_active: true }, { id: 11, code: 'STD', is_active: true }]
const entry = (id: number, action: string) => ({ id, created_at: '2026-09-30T13:00:00Z', business_date: '2026-09-30', user: { id: 5, name: 'Dewi' }, action, entity_type: 'reservation', entity_id: 1, old_data: null, new_data: null })

const READ = ['reservation.read', 'guest.read']
const FULL = [...READ, 'guest.write', 'reservation.create', 'reservation.update', 'reservation.cancel', 'reservation.reinstate', 'frontdesk.checkin', 'frontdesk.checkout', 'frontdesk.rate_change', 'folio.read', 'payment.post', 'audit.read']

let mounted: VueWrapper | null = null

async function mountView(res: object = reservation(), permissions = FULL, answers: Record<string, unknown | (() => unknown)> = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-10-01' } as never
  GET = vi.fn(async (path: string) => {
    for (const [suffix, answer] of Object.entries(answers)) if (path.endsWith(suffix)) return { data: typeof answer === 'function' ? (answer as () => unknown)() : answer }
    if (path.endsWith('/room-types')) return { data: { data: types } }
    if (path.endsWith('/rate-plans')) return { data: { data: [{ id: 1, code: 'BAR', name: 'Best' }, { id: 2, code: 'CORP', name: 'Corporate' }] } }
    if (path.endsWith('/companies')) return { data: { data: [{ id: 5, code: 'ACME', name: 'Acme Corp' }] } }
    if (path === '/api/v1/guests/{id}') return { data: guestView }
    if (path.endsWith('/audit-logs')) return { data: { data: [] } }
    if (path.endsWith('/billing-instructions')) return { data: { data: [] } }
    return { data: res }
  })
  POST = vi.fn()
  PATCH = vi.fn().mockResolvedValue({ data: res })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  mounted = mount(ReservationDetailView, { props: { id: '1' }, attachTo: document.body, global: { plugins: [pinia, router] } })
  await flushPromises()
  return mounted
}
const links = (w: VueWrapper) => (id: string) => w.find(`[data-testid=${id}]`).attributes('href')

describe('Reservation workspace', () => {
  beforeEach(() => {
    setLocale('en')
  })
  afterEach(() => {
    mounted?.unmount()
    mounted = null
    document.body.innerHTML = ''
  })

  it('has a header with the guest, the number, the status, the dates and the rooms', async () => {
    const w = await mountView(reservation({ rooms: [line({ room_number: '101' }), line({ id: 5, room_type_code: 'STD' })] }))
    const head = w.get('[data-testid=header-card]')
    expect(head.get('[data-testid=header-guest]').text()).toBe('Siti Nurhaliza')
    expect(head.text()).toContain('RES000001')
    expect(head.text()).toContain('1 Oct 2026')
    expect(head.get('[data-testid=header-rooms]').text()).toBe('101 · DLX, STD · no room assigned')
    expect(w.get('[data-testid=status]').text()).toBe('Reserved')
    expect(w.find('[data-testid=draft-note]').exists()).toBe(false)
  })

  it('tells that a draft holds no room and no stock, and calls it a draft', async () => {
    const w = await mountView(reservation({ status: 'DRAFT', display_status: 'DRAFT', rooms: [line({ status: 'DRAFT' })] }))
    expect(w.get('[data-testid=status]').text()).toBe('Draft')
    expect(w.get('[data-testid=draft-note]').text()).toContain('holds no room')
  })

  it('shows the guest with the contact data, and says why it cannot when the person may not read guests', async () => {
    const w = await mountView()
    expect(w.get('[data-testid=guest-email]').text()).toBe('siti@example.com')
    expect(w.get('[data-testid=guest-phone]').text()).toBe('+62811')
    expect(GET.mock.calls.some((c) => c[0] === '/api/v1/guests/{id}')).toBe(true)
    w.unmount()
    const denied = await mountView(reservation(), ['reservation.read'])
    expect(denied.get('[data-testid=guest-email]').text()).toContain('right to read guests')
    expect(GET.mock.calls.some((c) => c[0] === '/api/v1/guests/{id}')).toBe(false)
  })

  it('edits the guest with the guest editor of the in-house screens, and reads the reservation again', async () => {
    const w = await mountView()
    await w.get('[data-testid=edit-guest-card]').trigger('click')
    await flushPromises()
    expect(document.body.querySelector('[data-testid=guest-edit-dialog]')).not.toBeNull()
    expect(GET.mock.calls.filter((c) => c[0] === '/api/v1/guests/{id}').length).toBeGreaterThanOrEqual(2) // the card and the dialog read the same endpoint
  })

  it('offers a reserved reservation to be edited and cancelled, and not to be confirmed again', async () => {
    const w = await mountView()
    expect(w.find('[data-testid=edit-reservation]').exists()).toBe(true)
    expect(w.find('[data-testid=cancel]').exists()).toBe(true)
    expect(w.find('[data-testid=confirm]').exists()).toBe(false)
  })

  it('links check-in to the existing check-in of the arrivals screen', async () => {
    const w = await mountView(reservation({ rooms: [line({ arrival_date: '2026-10-01' })], folios: [{ id: 80, folio_number: 'FOL000001', stay_id: null, status: 'OPEN', balance: '-300000', folio_type: 'GUEST', bill_to_company_id: null }] }))
    expect(links(w)('check-in')).toBe('/arrivals?q=RES000001')
    expect(links(w)('open-payment')).toBe('/folios/80?tab=payment')
    w.unmount()
    const another = await mountView(reservation({ rooms: [line({ arrival_date: '2026-10-05', departure_date: '2026-10-06' })] }))
    expect(another.find('[data-testid=check-in]').exists()).toBe(false)
  })

  it('a checked-in reservation offers its stay, its rate, its folio and the check-out', async () => {
    const w = await mountView(reservation({
      display_status: 'IN_HOUSE', rooms: [line({ status: 'CHECKED_IN', stay_id: 55, room_number: '101' })],
      folios: [{ id: 80, folio_number: 'FOL000001', stay_id: 55, status: 'OPEN', balance: '450000', folio_type: 'GUEST', bill_to_company_id: null }],
    }))
    expect(w.get('[data-testid=status]').text()).toBe('Checked in')
    expect(links(w)('view-stay')).toBe('/stays/55')
    expect(links(w)('check-out')).toBe('/stays/55?action=checkout')
    expect(links(w)('open-folio')).toBe('/folios/80')
    expect(w.find('[data-testid=edit-reservation]').exists()).toBe(false)
    expect(w.find('[data-testid=confirm]').exists()).toBe(false)
    expect(w.find('[data-testid=check-in]').exists()).toBe(false)
    // Edit rate is the editor of the in-house screens, on the stay of the room
    GET.mockClear()
    await w.get('[data-testid=edit-rate]').trigger('click')
    await flushPromises()
    expect(GET.mock.calls.some((c) => c[0] === '/api/v1/properties/{propertyId}/stays/{id}' && JSON.stringify(c[1]).includes('55'))).toBe(true)
    expect(document.body.querySelector('[data-testid=edit-rate-dialog]')).not.toBeNull()
    expect(document.body.querySelector('[data-testid=rate-room]')?.textContent).toContain('101')
  })

  it('a checked-out reservation is read only, with its folio', async () => {
    const w = await mountView(reservation({
      display_status: 'CHECKED_OUT', rooms: [line({ status: 'COMPLETED', stay_id: 55 })],
      folios: [{ id: 80, folio_number: 'FOL000001', stay_id: 55, status: 'CLOSED', balance: '0', folio_type: 'GUEST', bill_to_company_id: null }],
    }))
    expect(w.get('[data-testid=status]').text()).toBe('Checked out')
    expect(links(w)('open-folio')).toBe('/folios/80')
    for (const id of ['edit-reservation', 'confirm', 'check-in', 'check-out', 'cancel', 'open-payment']) expect(w.find(`[data-testid=${id}]`).exists()).toBe(false)
  })

  it('a cancelled or no-show reservation is read only', async () => {
    const cancelled = await mountView(reservation({ status: 'CANCELLED', display_status: 'CANCELLED', rooms: [line({ status: 'CANCELLED' })] }))
    expect(cancelled.get('[data-testid=status]').text()).toBe('Cancelled')
    for (const id of ['edit-reservation', 'confirm', 'check-in', 'cancel']) expect(cancelled.find(`[data-testid=${id}]`).exists()).toBe(false)
    cancelled.unmount()
    const noShow = await mountView(reservation({ display_status: 'NO_SHOW', rooms: [line({ status: 'NO_SHOW' })] }))
    expect(noShow.get('[data-testid=status]').text()).toBe('No-show')
    for (const id of ['edit-reservation', 'confirm', 'check-in', 'cancel']) expect(noShow.find(`[data-testid=${id}]`).exists()).toBe(false)
  })

  it('offers only what the permissions allow', async () => {
    const w = await mountView(reservation({ rooms: [line({ arrival_date: '2026-10-01' })] }), READ)
    for (const id of ['edit-reservation', 'check-in', 'cancel', 'edit-guest-card']) expect(w.find(`[data-testid=${id}]`).exists()).toBe(false)
    expect(w.find('[data-testid=guest-card]').exists()).toBe(true)
  })

  it('says No folio when there is none, and shows each folio with the balance of the ledger', async () => {
    const none = await mountView()
    expect(none.get('[data-testid=no-folio]').text()).toBe('No folio')
    none.unmount()
    const w = await mountView(reservation({ folios: [
      { id: 80, folio_number: 'FOL000001', stay_id: 55, status: 'OPEN', balance: '450000', folio_type: 'GUEST', bill_to_company_id: null },
      { id: 81, folio_number: 'FOL000002', stay_id: 55, status: 'OPEN', balance: '0', folio_type: 'COMPANY', bill_to_company_id: 5, bill_to_company_name: 'Acme Corp' },
    ] }))
    expect(w.find('[data-testid=no-folio]').exists()).toBe(false)
    const text = w.get('[data-testid=folios]').text()
    expect(text).toContain('450,000')
    expect(text).toContain('Acme Corp')
    expect(w.get('[data-testid=folio-link-81]').attributes('href')).toBe('/folios/81')
  })

  it('shows the history from the audit trail, and says so when the person may not read it or it cannot be loaded', async () => {
    const w = await mountView(reservation(), FULL, { '/audit-logs': { data: [entry(2, 'reservation.confirmed'), entry(1, 'reservation.created')] } })
    const list = w.get('[data-testid=history-list]').text()
    expect(list).toContain('Confirmed')
    expect(list).not.toContain('reservation.confirmed') // the action in words, not its key
    expect(list).toContain('Dewi')
    expect(GET.mock.calls.find((c) => String(c[0]).endsWith('/audit-logs'))?.[1]).toMatchObject({ params: { query: { entity_type: 'reservation', entity_id: 1 } } })
    w.unmount()
    const denied = await mountView(reservation(), READ)
    expect(denied.get('[data-testid=history-denied]').text()).toContain('audit trail')
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/audit-logs'))).toBe(false)
    denied.unmount()
    const empty = await mountView()
    expect(empty.get('[data-testid=history-empty]').text()).toBe('No entries.') // empty is told apart from a failure
    empty.unmount()
    const failed = await mountView(reservation(), FULL, {
      '/audit-logs': () => {
        throw new ApiError({ type: 't', title: 'Unavailable', status: 503, code: 'SERVICE_UNAVAILABLE', detail: 'try later' })
      },
    })
    expect(failed.find('[data-testid=history-failed]').exists()).toBe(true)
    expect(failed.find('[data-testid=history-empty]').exists()).toBe(false)
  })

  it('edits the dates, the type, the plan and the party of a room, sending only what changed with the loaded version', async () => {
    const w = await mountView()
    await w.get('[data-testid=edit-reservation]').trigger('click')
    await flushPromises()
    const dialog = document.body.querySelector('[data-testid=reservation-edit-dialog]') as HTMLElement
    const set = (sel: string, value: string) => {
      const el = dialog.querySelector(sel) as HTMLInputElement
      el.value = value
      el.dispatchEvent(new Event(el.tagName === 'SELECT' ? 'change' : 'input'))
    }
    expect((dialog.querySelector('[data-testid=save-line-4]') as HTMLButtonElement).disabled).toBe(true) // nothing changed
    set('input[name=departure_4]', '2026-10-04')
    set('select[name=plan_4]', '2')
    await flushPromises()
    ;(dialog.querySelector('form') as HTMLFormElement).dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(PATCH.mock.calls[0]).toEqual(['/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}', {
      params: { path: { propertyId: 7, id: 1, lineId: 4 } }, body: { version: 2, departure_date: '2026-10-04', rate_plan_id: 2 },
    }])
  })

  it('does not let the room type change while a room is assigned, and edits the company and the notes of the reservation', async () => {
    const w = await mountView(reservation({ rooms: [line({ room_number: '101', room_id: 21 })] }))
    await w.get('[data-testid=edit-reservation]').trigger('click')
    await flushPromises()
    const dialog = document.body.querySelector('[data-testid=reservation-edit-dialog]') as HTMLElement
    expect((dialog.querySelector('select[name=type_4]') as HTMLSelectElement).disabled).toBe(true)
    const company = dialog.querySelector('select[name=company_id]') as HTMLSelectElement
    company.value = '5'
    company.dispatchEvent(new Event('change'))
    const note = dialog.querySelector('input[name=special_request]') as HTMLInputElement
    note.value = 'late arrival'
    note.dispatchEvent(new Event('input'))
    await flushPromises()
    ;(dialog.querySelector('form[data-testid=edit-header]') as HTMLFormElement).dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(PATCH.mock.calls[0]).toEqual(['/api/v1/properties/{propertyId}/reservations/{id}', { params: { path: { propertyId: 7, id: 1 } }, body: { version: 2, company_id: 5, special_request: 'late arrival' } }])
  })

  it('shows the refusal of the server in the editor', async () => {
    const w = await mountView()
    await w.get('[data-testid=edit-reservation]').trigger('click')
    await flushPromises()
    PATCH.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'ROOM_TYPE_NOT_AVAILABLE', detail: 'the room type has no availability on some nights' }))
    const dialog = document.body.querySelector('[data-testid=reservation-edit-dialog]') as HTMLElement
    const el = dialog.querySelector('input[name=departure_4]') as HTMLInputElement
    el.value = '2026-10-09'
    el.dispatchEvent(new Event('input'))
    await flushPromises()
    ;(dialog.querySelector('form') as HTMLFormElement).dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(document.body.querySelector('[data-testid=edit-error]')?.textContent).toContain('ROOM_TYPE_NOT_AVAILABLE')
  })

  it('confirms a draft with the loaded version and cancels with a reason, as before', async () => {
    const w = await mountView(reservation({ status: 'DRAFT', display_status: 'DRAFT', rooms: [line({ status: 'DRAFT' })] }))
    POST.mockResolvedValue({ data: reservation() })
    await w.get('[data-testid=confirm]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]).toEqual(['/api/v1/properties/{propertyId}/reservations/{id}/confirm', { params: { path: { propertyId: 7, id: 1 } }, body: { version: 2, restriction_override: undefined } }])
    expect(w.get('[data-testid=status]').text()).toBe('Reserved')
  })

  it('takes a deposit only before check-in: not on a reservation that is in house, checked out, cancelled or no-show', async () => {
    const reserved = await mountView()
    expect(reserved.find('[data-testid=deposit-card]').exists()).toBe(true)
    reserved.unmount()
    const draft = await mountView(reservation({ status: 'DRAFT', display_status: 'DRAFT', rooms: [line({ status: 'DRAFT' })] }))
    expect(draft.find('[data-testid=deposit-card]').exists()).toBe(true)
    draft.unmount()
    for (const [display, status] of [['IN_HOUSE', 'CHECKED_IN'], ['CHECKED_OUT', 'COMPLETED'], ['NO_SHOW', 'NO_SHOW']] as const) {
      const w = await mountView(reservation({ display_status: display, rooms: [line({ status, stay_id: status === 'NO_SHOW' ? null : 55 })] }))
      expect(w.find('[data-testid=deposit-card]').exists(), display).toBe(false)
      w.unmount()
    }
    const cancelled = await mountView(reservation({ status: 'CANCELLED', display_status: 'CANCELLED', rooms: [line({ status: 'CANCELLED' })] }))
    expect(cancelled.find('[data-testid=deposit-card]').exists()).toBe(false)
  })

  it('names the status of a room like the reservation and shows the rooms of the header in a short line', async () => {
    const w = await mountView(reservation({ rooms: [line({ id: 4, status: 'CONFIRMED' }), line({ id: 5, status: 'CONFIRMED' }), line({ id: 6, status: 'COMPLETED', room_number: '201', room_type_code: 'SUP' })] }))
    expect(w.get('[data-testid=line-status-4]').text()).toBe('Reserved')
    expect(w.get('[data-testid=line-status-6]').text()).toBe('Checked out')
    expect(w.get('[data-testid=header-rooms]').text()).toBe('201 · SUP, DLX ×2 · no room assigned')
  })

  it('says that the company of a reservation is not its payer, and shows the company of a folio on a line of its own', async () => {
    const w = await mountView(reservation({
      company_id: 5, company_name: 'Acme Corp',
      folios: [{ id: 81, folio_number: 'FOL000002', stay_id: 55, status: 'OPEN', balance: '700000', folio_type: 'COMPANY', bill_to_company_id: 5, bill_to_company_name: 'PT Nusantara Teknologi Informasi dan Komunikasi Indonesia Raya Tbk' }],
    }))
    expect(w.get('[data-testid=payer-note]').text()).toContain('Who pays')
    expect(w.get('[data-testid=folio-company-81]').text()).toContain('PT Nusantara Teknologi')
    expect(w.get('[data-testid=folios]').text()).toContain('700,000')
  })

  it('keeps the close button of the editor in view', async () => {
    const w = await mountView()
    await w.get('[data-testid=edit-reservation]').trigger('click')
    await flushPromises()
    const close = document.body.querySelector('[data-testid=edit-close]') as HTMLElement
    expect(close.parentElement?.className).toContain('sticky')
  })

  it('reads the history again after an action, so that it shows what was just done', async () => {
    let entries = [entry(1, 'reservation.created')]
    const w = await mountView(reservation({ status: 'DRAFT', display_status: 'DRAFT', rooms: [line({ status: 'DRAFT' })] }), FULL, { '/audit-logs': () => ({ data: entries }) })
    expect(w.get('[data-testid=history-list]').text()).toContain('Created')
    entries = [entry(2, 'reservation.confirmed'), entry(1, 'reservation.created')]
    POST.mockResolvedValue({ data: reservation() })
    await w.get('[data-testid=confirm]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=history-list]').text()).toContain('Confirmed')
    expect(GET.mock.calls.filter((c) => String(c[0]).endsWith('/audit-logs'))).toHaveLength(2)
  })
})
