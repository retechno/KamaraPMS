import { toastText } from '@/test/toasts'
import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useToasts } from '@/composables/useToast'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ArrivalsView from './ArrivalsView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const arrival = (over: object = {}) => ({
  reservation_id: 9, confirmation_number: 'RES000009', reservation_room_id: 4, reservation_version: 2, guest_id: 3, guest_name: 'Siti Nurhaliza',
  room_type_id: 10, room_type_code: 'DLX', room_type_name: 'Deluxe', room_id: null, arrival_date: '2026-09-30', departure_date: '2026-10-02', adult_count: 2, child_count: 0,
  status: 'CONFIRMED', reservation_status: 'CONFIRMED', rate: { rate_plan_code: 'BAR', rate_plan_name: 'Best available', amount: '1000000', price_mode: 'EXCLUSIVE' }, company: null, deposit: null,
  readiness: { status: 'BLOCKED', blockers: ['ROOM_NOT_ASSIGNED'] }, ...over,
})
const types = [{ id: 10, code: 'DLX', is_active: true }, { id: 11, code: 'STD', is_active: true }]
const free = [
  { room_id: 21, room_number: '101', housekeeping_status: 'DIRTY' },
  { room_id: 22, room_number: '102', housekeeping_status: 'CLEAN' },
]

function mountView(permissions = ['reservation.read', 'frontdesk.checkin'], arrivals: object[] = [arrival()], inspection = false) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-09-30' } as never
  property.current = { require_room_inspection_for_checkin: inspection } as never
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/arrivals')) return { data: { data: arrivals } }
    if (path.endsWith('/room-types')) return { data: { data: types } }
    if (path.endsWith('/availability/rooms')) return { data: { data: free } }
    return { data: {} }
  })
  POST = vi.fn().mockResolvedValue({ data: { stay: { id: 55, stay_number: 'STY000055' }, stay_room: { room_number: '102' } } })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  const push = vi.spyOn(router, 'push')
  const w = mount(ArrivalsView, { attachTo: document.body, global: { plugins: [pinia, router] } })
  mounted.push(w)
  return { w, push }
}

// The check-in form is in a side sheet, rendered in a portal: it is found in the document, not in the wrapper.
const mounted: VueWrapper[] = []
const panel = (sel: string) => {
  const el = document.body.querySelector(sel)
  if (!el) throw new Error(`nothing in the sheet matches ${sel}`)
  return new DOMWrapper(el)
}
const inSheet = (sel: string) => document.body.querySelector(sel) !== null
const reset = () => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
}

describe('ArrivalsView and the check-in panel', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
  })
  afterEach(reset)

  it('lists today\'s arrivals', async () => {
    const { w } = mountView()
    await flushPromises()
    expect(w.get('[data-testid=arrival-4]').text()).toContain('Siti Nurhaliza')
    const empty = mountView(undefined, [])
    await flushPromises()
    expect(empty.w.get('[data-testid=empty]').text()).toContain('No one')
  })

  it('shows the real housekeeping status of a room already assigned to the reservation', async () => {
    const assigned = arrival({ room_id: 30, room_number: '301', housekeeping_status: 'INSPECTED' })
    const { w } = mountView(['reservation.read', 'frontdesk.checkin'], [assigned])
    await flushPromises()
    await w.get('[data-testid=open-4]').trigger('click')
    await flushPromises()
    expect(panel('select[name=room]').findAll('option')[0]?.text()).toBe('301 · INSPECTED')
    expect(inSheet('[data-testid=not-ready]')).toBe(false)
  })

  it('needs read permission and hides check-in without frontdesk.checkin', async () => {
    const none = mountView(['guest.read'])
    await flushPromises()
    expect(none.w.find('[data-testid=no-access]').exists()).toBe(true)
    const reader = mountView(['reservation.read'])
    await flushPromises()
    expect(reader.w.find('[data-testid=open-4]').exists()).toBe(false)
  })

  it('offers free rooms with their status, and checks a ready room in with an Idempotency-Key', async () => {
    const { w, push } = mountView()
    await flushPromises()
    await w.get('[data-testid=open-4]').trigger('click')
    await flushPromises()
    expect(GET.mock.calls.at(-1)).toEqual(['/api/v1/properties/{propertyId}/availability/rooms', { params: { path: { propertyId: 7 }, query: { room_type_id: 10, arrival: '2026-09-30', departure: '2026-10-02' } } }])
    const options = panel('select[name=room]').findAll('option').map((o) => o.text())
    // the ready room is proposed first: the room that is not ready cannot be checked into without an override
    expect(options).toEqual(['102 · CLEAN', '101 · DIRTY (not ready)'])
    expect((document.body.querySelector('select[name=room]') as HTMLSelectElement).value).toBe('22')
    expect(inSheet('[data-testid=not-ready]')).toBe(false)
    expect(panel('[data-testid=checkin-submit]').attributes('disabled')).toBeUndefined()
    // choosing the room that is not ready says so and turns the button off until a ready room is chosen
    await panel('select[name=room]').setValue(21)
    expect(panel('[data-testid=not-ready]').text()).toContain('101 is DIRTY')
    expect(panel('[data-testid=checkin-submit]').attributes('disabled')).toBeDefined()
    await panel('select[name=room]').setValue(22)
    expect(inSheet('[data-testid=not-ready]')).toBe(false)
    await panel('form[data-testid=checkin-4]').trigger('submit')
    await flushPromises()
    const [path, init] = POST.mock.calls[0] as [string, { params: { header: Record<string, string> }; body: object }]
    expect(path).toBe('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/check-in')
    expect(init.params.header['Idempotency-Key']).toBeTruthy()
    expect(init.body).toMatchObject({ version: 2, room_id: 22, guest_id: 3, adult_count: 2, child_count: 0, override_room_not_ready: false })
    // the desk stays on the list: it is read again, and the stay is named in a message
    expect(push).not.toHaveBeenCalled()
    expect(GET.mock.calls.filter((c) => String(c[0]).endsWith('/arrivals'))).toHaveLength(2)
    expect(useToasts().items.value.some((x) => x.message.includes('STY000055'))).toBe(true)
  })

  it('needs the override permission and a reason to use a room that is not ready', async () => {
    const { w } = mountView()
    await flushPromises()
    await w.get('[data-testid=open-4]').trigger('click')
    await flushPromises()
    expect(inSheet('input[name=override]')).toBe(false) // no frontdesk.checkin_unready_room
    reset()

    const sup = mountView(['reservation.read', 'frontdesk.checkin', 'frontdesk.checkin_unready_room'])
    await flushPromises()
    await sup.w.get('[data-testid=open-4]').trigger('click')
    await flushPromises()
    await panel('select[name=room]').setValue(21) // the room that is not ready, chosen on purpose
    await panel('input[name=override]').setValue(true)
    await panel('input[name=override_reason]').setValue('guest waiting')
    await panel('form[data-testid=checkin-4]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1]).toMatchObject({ body: { room_id: 21, override_room_not_ready: true, override_reason: 'guest waiting' } })
  })

  it('a property that requires inspection treats CLEAN as not ready', async () => {
    const { w } = mountView(undefined, [arrival()], true)
    await flushPromises()
    await w.get('[data-testid=open-4]').trigger('click')
    await flushPromises()
    await panel('select[name=room]').setValue(22)
    expect(panel('[data-testid=not-ready]').text()).toContain('an inspected room is needed')
  })

  it('shows the server error and stays open', async () => {
    const { w, push } = mountView()
    await flushPromises()
    await w.get('[data-testid=open-4]').trigger('click')
    await flushPromises()
    await panel('select[name=room]').setValue(22)
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'ROOM_OCCUPIED', detail: 'the room is occupied' }))
    await panel('form[data-testid=checkin-4]').trigger('submit')
    await flushPromises()
    expect(toastText()).toContain('the room is occupied')
    expect(push).not.toHaveBeenCalled()
  })

  it('warns when the reservation has no guest', async () => {
    const { w } = mountView(undefined, [arrival({ guest_id: null, guest_name: '' })])
    await flushPromises()
    await w.get('[data-testid=open-4]').trigger('click')
    await flushPromises()
    expect(inSheet('[data-testid=no-guest]')).toBe(true)
    expect(panel('[data-testid=checkin-submit]').attributes('disabled')).toBeDefined()
  })

  it('offers the assigned room even though the line holds it, and marks another type as an upgrade', async () => {
    const { w } = mountView(undefined, [arrival({ room_id: 21, room_number: '101' })])
    await flushPromises()
    GET.mockImplementation(async (path: string) => (path.endsWith('/availability/rooms') ? { data: { data: [{ room_id: 22, room_number: '102', housekeeping_status: 'CLEAN' }] } } : path.endsWith('/room-types') ? { data: { data: types } } : { data: { data: [arrival({ room_id: 21, room_number: '101' })] } }))
    await w.get('[data-testid=open-4]').trigger('click')
    await flushPromises()
    expect(panel('select[name=room]').findAll('option').map((o) => o.text())[0]).toContain('101')
    await panel('select[name=room_type]').setValue(11)
    await flushPromises()
    expect(inSheet('[data-testid=upgrade-note]')).toBe(true)
  })

  it('shows the bed the guest asked for, and whether the assigned room has it', async () => {
    const { w } = mountView(undefined, [
      arrival({ requested_bed_type_id: 5, requested_bed_type_code: 'KING', room_id: 21, room_number: '101', room_bed_type_code: 'KING', housekeeping_status: 'CLEAN' }),
      arrival({ reservation_room_id: 5, reservation_id: 10, confirmation_number: 'RES000010', requested_bed_type_id: 5, requested_bed_type_code: 'KING', room_id: 22, room_number: '102', room_bed_type_code: 'TWIN', housekeeping_status: 'CLEAN' }),
      arrival({ reservation_room_id: 6, reservation_id: 11, confirmation_number: 'RES000011' }),
    ])
    await flushPromises()
    expect(w.get('[data-testid=bed-4]').text()).toContain('King'.toUpperCase())
    expect(w.get('[data-testid=bed-match-4]').attributes('title')).toBe('matches the request')
    expect(w.get('[data-testid=bed-match-5]').attributes('title')).toBe('another bed than asked')
    expect(w.find('[data-testid=bed-6]').exists()).toBe(false) // no request: no badge
  })

  it('proposes a room with the requested bed first at check-in', async () => {
    const { w } = mountView(undefined, [arrival({ requested_bed_type_id: 5, requested_bed_type_code: 'KING' })])
    await flushPromises()
    GET.mockImplementation(async (path: string) => {
      if (path.endsWith('/availability/rooms')) {
        return { data: { data: [
          { room_id: 21, room_number: '101', housekeeping_status: 'CLEAN', bed_type_id: 6, bed_type_name: 'Twin' },
          { room_id: 22, room_number: '102', housekeeping_status: 'CLEAN', bed_type_id: 5, bed_type_name: 'King' },
        ] } }
      }
      if (path.endsWith('/room-types')) return { data: { data: types } }
      return { data: {} }
    })
    await w.get('[data-testid=open-4]').trigger('click')
    await flushPromises()
    const options = Array.from(document.body.querySelectorAll('select[name=room] option')).map((o) => o.textContent)
    expect(options).toEqual(['102 · CLEAN · King ✓ matches the request', '101 · CLEAN · Twin'])
    expect((document.body.querySelector('select[name=room]') as HTMLSelectElement).value).toBe('22')
  })

  it('shows the company, the booked rate, the deposit and the readiness the server sent', async () => {
    const { w } = mountView(['reservation.read'], [
      arrival({ company: { id: 2, name: 'ABC Indonesia' }, deposit: { folio_id: 80, paid: '300000' }, room_id: 21, room_number: '101', readiness: { status: 'READY', blockers: [] } }),
      arrival({ reservation_room_id: 5, reservation_id: 10, confirmation_number: 'RES000010', rate: { rate_plan_code: 'BAR', rate_plan_name: 'Best', amount: '', price_mode: '' }, readiness: { status: 'BLOCKED', blockers: ['ROOM_NOT_ASSIGNED', 'GUEST_MISSING'] } }),
    ])
    await flushPromises()
    expect(w.get('[data-testid=company-4]').text()).toBe('ABC Indonesia')
    expect(w.get('[data-testid=rate-4]').text()).toContain('1,000,000')
    expect(w.get('[data-testid=deposit-4]').text()).toBe('300,000')
    expect(w.get('[data-testid=arrival-4] [data-testid=readiness]').attributes('data-status')).toBe('READY')
    // a missing value is a dash, never a zero or a rate taken from somewhere else
    expect(w.get('[data-testid=company-5]').text()).toBe('—')
    expect(w.get('[data-testid=deposit-5]').text()).toBe('—')
    expect(w.get('[data-testid=rate-5]').text()).toContain('—')
    const blockers = w.findAll('[data-testid=arrival-5] [data-blocker]').map((b) => b.attributes('data-blocker'))
    expect(blockers).toEqual(['ROOM_NOT_ASSIGNED', 'GUEST_MISSING'])
    expect(w.get('[data-testid=no-room-5]').text()).toBe('Not assigned')
  })

  it('asks the server for the date, status, search and room type, and does not filter the page itself', async () => {
    const { w } = mountView(['reservation.read'], [arrival(), arrival({ reservation_room_id: 5, guest_name: 'Someone Else', confirmation_number: 'RES000010' })])
    await flushPromises()
    expect(GET.mock.calls.find((c) => String(c[0]).endsWith('/arrivals'))?.[1]).toMatchObject({ params: { query: { date: undefined, status: 'CONFIRMED', q: undefined, room_type_id: undefined } } })
    await w.get('input[name=date]').setValue('2026-10-05')
    await flushPromises()
    await w.get('select[name=status]').setValue('CHECKED_IN')
    await flushPromises()
    await w.get('input[name=q]').setValue('Siti')
    await flushPromises()
    await w.get('select[name=room_type_id]').setValue('11')
    await flushPromises()
    const last = GET.mock.calls.filter((c) => String(c[0]).endsWith('/arrivals')).at(-1)
    expect(last?.[1]).toMatchObject({ params: { query: { date: '2026-10-05', status: 'CHECKED_IN', q: 'Siti', room_type_id: 11 } } })
    // the server answered with both rows whatever the search: the screen shows what it was given
    expect(w.findAll('tbody tr[data-testid^=arrival-]')).toHaveLength(2)
    await w.get('[data-testid=clear-filters]').trigger('click')
    await flushPromises()
    expect(GET.mock.calls.filter((c) => String(c[0]).endsWith('/arrivals')).at(-1)?.[1]).toMatchObject({ params: { query: { date: undefined, status: 'CONFIRMED', q: undefined } } })
  })

  it('offers only the actions that fit the status and the permissions', async () => {
    const all = ['reservation.read', 'frontdesk.checkin', 'guest.write', 'reservation.update', 'folio.read', 'payment.post']
    const { w } = mountView(all, [arrival({ guest_id: 3, deposit: { folio_id: 80, paid: '1' } }), arrival({ reservation_room_id: 5, guest_id: null, status: 'CHECKED_IN' })])
    await flushPromises()
    expect(w.find('[data-testid=open-4]').exists()).toBe(true)
    expect(w.find('[data-testid=editGuest-4]').exists()).toBe(true)
    expect(w.find('[data-testid=open-5]').exists()).toBe(false) // already checked in
    expect(w.find('[data-testid=editGuest-5]').exists()).toBe(false) // no guest to edit
    await w.get('[data-testid=more-4]').trigger('click')
    await flushPromises()
    const menu = document.body.querySelector('[data-testid=menu-4]')?.textContent ?? ''
    for (const label of ['View reservation', 'Edit reservation', 'Folio', 'Deposit / payment']) expect(menu).toContain(label)
    reset()
    const reader = mountView(['reservation.read'], [arrival()])
    await flushPromises()
    expect(reader.w.find('[data-testid=open-4]').exists()).toBe(false)
    expect(reader.w.find('[data-testid=editGuest-4]').exists()).toBe(false)
  })

  it('does not offer check-in for an arrival that is not on the business date', async () => {
    const { w } = mountView(undefined, [arrival({ arrival_date: '2026-10-05', readiness: { status: 'BLOCKED', blockers: ['NOT_BUSINESS_DATE'] } })])
    await flushPromises()
    expect(w.find('[data-testid=open-4]').exists()).toBe(false)
    expect(w.get('[data-testid=arrival-4] [data-blocker=NOT_BUSINESS_DATE]').text()).toBe('Not today')
  })

  it('opens the drawer from the guest, and Edit guest saves and renames the row without a reload', async () => {
    const { w } = mountView(['reservation.read', 'guest.write', 'frontdesk.checkin'])
    await flushPromises()
    await w.get('[data-testid=detail-4]').trigger('click')
    await flushPromises()
    const drawer = document.body.querySelector('[data-testid=arrival-drawer]')
    expect(drawer?.textContent).toContain('Siti Nurhaliza')
    expect(drawer?.textContent).toContain('Best available')
    expect(drawer?.querySelector('[data-testid=drawer-deposit]')?.textContent).toBe('No deposit')
    ;(drawer?.querySelector('[data-testid=drawer-editGuest]') as HTMLElement).click()
    await flushPromises()
    expect(document.body.querySelector('[data-testid=guest-edit-dialog]')).not.toBeNull()
    expect(GET.mock.calls.at(-1)).toEqual(['/api/v1/guests/{id}', { params: { path: { id: 3 } } }])
  })

  it('shows a failure with a retry, not an empty list', async () => {
    const { w } = mountView()
    await flushPromises()
    GET.mockImplementation(async () => {
      throw new ApiError({ type: 't', title: 'Unavailable', status: 503, code: 'SERVICE_UNAVAILABLE', detail: 'try later' })
    })
    await w.get('input[name=q]').setValue('x')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('SERVICE_UNAVAILABLE')
    expect(w.find('[data-testid=empty]').exists()).toBe(false)
    expect(w.get('[data-testid=not-loaded]').text()).toContain('could not be loaded') // a failure is not an empty list
    GET.mockImplementation(async (path: string) => (path.endsWith('/arrivals') ? { data: { data: [arrival()] } } : { data: { data: [] } }))
    await w.get('[data-testid=retry]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid=form-error]').exists()).toBe(false)
  })

  it('proposes a ready room before a room that is not ready, even when the room that is not ready has the bed that was asked for', async () => {
    const { w } = mountView(undefined, [arrival({ requested_bed_type_id: 5, requested_bed_type_code: 'KING' })])
    await flushPromises()
    GET.mockImplementation(async (path: string) => {
      if (path.endsWith('/availability/rooms')) {
        return { data: { data: [
          { room_id: 21, room_number: '101', housekeeping_status: 'DIRTY', bed_type_id: 5, bed_type_name: 'King' },
          { room_id: 22, room_number: '102', housekeeping_status: 'CLEAN', bed_type_id: 6, bed_type_name: 'Twin' },
          { room_id: 23, room_number: '103', housekeeping_status: 'CLEAN', bed_type_id: 5, bed_type_name: 'King' },
        ] } }
      }
      if (path.endsWith('/room-types')) return { data: { data: types } }
      return { data: {} }
    })
    await w.get('[data-testid=open-4]').trigger('click')
    await flushPromises()
    const options = Array.from(document.body.querySelectorAll('select[name=room] option')).map((o) => o.textContent)
    expect(options).toEqual(['103 · CLEAN · King ✓ matches the request', '102 · CLEAN · Twin', '101 · DIRTY · King ✓ matches the request (not ready)'])
    expect((document.body.querySelector('select[name=room]') as HTMLSelectElement).value).toBe('23')
  })

  it('proposes a room that is not ready only when there is no ready room, and says so', async () => {
    const { w } = mountView()
    await flushPromises()
    GET.mockImplementation(async (path: string) => (path.endsWith('/availability/rooms') ? { data: { data: [
      { room_id: 21, room_number: '101', housekeeping_status: 'DIRTY' }, { room_id: 22, room_number: '102', housekeeping_status: 'CLEANING' },
    ] } } : path.endsWith('/room-types') ? { data: { data: types } } : { data: {} }))
    await w.get('[data-testid=open-4]').trigger('click')
    await flushPromises()
    expect((document.body.querySelector('select[name=room]') as HTMLSelectElement).value).toBe('21')
    expect(panel('[data-testid=not-ready]').text()).toContain('101 is DIRTY')
    expect(panel('[data-testid=checkin-submit]').attributes('disabled')).toBeDefined()
  })

  it('keeps the room that is already on the line first, even when it is not ready: it was chosen on purpose', async () => {
    const { w } = mountView(undefined, [arrival({ room_id: 21, room_number: '101', housekeeping_status: 'DIRTY' })])
    await flushPromises()
    GET.mockImplementation(async (path: string) => (path.endsWith('/availability/rooms') ? { data: { data: [{ room_id: 22, room_number: '102', housekeeping_status: 'CLEAN' }] } } : path.endsWith('/room-types') ? { data: { data: types } } : { data: {} }))
    await w.get('[data-testid=open-4]').trigger('click')
    await flushPromises()
    expect((document.body.querySelector('select[name=room]') as HTMLSelectElement).value).toBe('21')
    expect(panel('[data-testid=not-ready]').text()).toContain('101 is DIRTY')
  })
})
