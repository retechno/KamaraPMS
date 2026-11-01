import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { toastText } from '@/test/toasts'
import TapeChartView from './TapeChartView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))

const T = { DLX: 10, STD: 11 }
const base = { reservation_id: 9, confirmation_number: 'RES000009', stay_id: null, open_segment: false, guest_name: 'Siti Nurhaliza' }
// 101: a booking to come (line 4, 5 to 8 Oct). 102: a guest in the house (line 6, 1 to 6 Oct) and a booking after (line 9, 8 to 10 Oct). 103: free. 201: out of service 5 to 7 Oct. 202: free, of another type.
const chart = () => ({
  from: '2026-10-01', to: '2026-10-15',
  rooms: [
    { room_id: 1, room_number: '101', room_type_id: T.DLX, room_type_code: 'DLX', blocks: [], bookings: [{ ...base, reservation_room_id: 4, room_type_id: T.DLX, status: 'CONFIRMED', arrival_date: '2026-10-05', departure_date: '2026-10-08' }] },
    {
      room_id: 2, room_number: '102', room_type_id: T.DLX, room_type_code: 'DLX', blocks: [],
      bookings: [
        { ...base, reservation_id: 11, confirmation_number: 'RES000011', guest_name: 'Budi Santoso', reservation_room_id: 6, room_type_id: T.DLX, status: 'CHECKED_IN', stay_id: 30, open_segment: true, arrival_date: '2026-10-01', departure_date: '2026-10-06' },
        { ...base, reservation_id: 12, confirmation_number: 'RES000012', guest_name: 'Ayu Pratiwi', reservation_room_id: 9, room_type_id: T.DLX, status: 'CONFIRMED', arrival_date: '2026-10-08', departure_date: '2026-10-10' },
      ],
    },
    { room_id: 3, room_number: '103', room_type_id: T.DLX, room_type_code: 'DLX', blocks: [], bookings: [] },
    { room_id: 4, room_number: '201', room_type_id: T.STD, room_type_code: 'STD', bookings: [], blocks: [{ id: 5, block_type: 'OOS', start_date: '2026-10-05', end_date: '2026-10-07' }] },
    { room_id: 5, room_number: '202', room_type_id: T.STD, room_type_code: 'STD', bookings: [], blocks: [] },
  ],
  unassigned: [{ room_type_id: T.DLX, room_type_code: 'DLX', bookings: [{ ...base, reservation_id: 13, confirmation_number: 'RES000013', guest_name: 'Rina Wijaya', reservation_room_id: 5, room_type_id: T.DLX, status: 'CONFIRMED', arrival_date: '2026-10-03', departure_date: '2026-10-04' }] }],
})

const line = (over: object = {}) => ({ id: 4, status: 'CONFIRMED', room_type_id: T.DLX, rate_plan_id: 3, occupancy_kind: 'PAID', adult_count: 2, child_count: 0, nightly_rates: [
  { date: '2026-10-05', amount: '1000000', is_override: false }, { date: '2026-10-06', amount: '1000000', is_override: false }, { date: '2026-10-07', amount: '1000000', is_override: false },
], ...over })
const reservation = (over: object = {}, lineOver: object = {}) => ({ id: 9, version: 7, company_id: null, rooms: [line(lineOver), line({ id: 6, ...lineOver })], ...over })
const offers = (over: object = {}) => ({ room_types: [{ room_type_id: T.DLX, rate_plans: [{ id: 3, missing_nights: 0, bookable: true, estimate: { net: '2000000', service: '0', tax: '220000', total: '2220000' }, ...over }] }] })

const ALL = ['reservation.read', 'reservation.update', 'reservation.upgrade', 'frontdesk.room_move']
const mounted: VueWrapper[] = []
let res = reservation()
let rowEls: Record<string, Element> = {}

function setup(permissions = ALL, o: { fine?: boolean; chartData?: object } = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-10-01' } as never
  property.current = { require_room_inspection_for_checkin: false, currency_decimals: 0 } as never
  Object.defineProperty(window, 'matchMedia', { configurable: true, writable: true, value: (q: string) => ({ matches: o.fine ?? true, media: q, addEventListener: () => {}, removeEventListener: () => {} }) })
  const data = o.chartData ?? chart()
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/tape-chart')) return { data }
    if (path.endsWith('/reservations/{id}')) return { data: res }
    if (path.endsWith('/stays/{id}')) return { data: { stay: { id: 30, version: 4 } } }
    if (path.endsWith('/availability')) return { data: offers() }
    return { data: {} }
  })
  POST = vi.fn().mockResolvedValue({ data: {} })
  PATCH = vi.fn().mockResolvedValue({ data: {} })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  const w = mount(TapeChartView, { attachTo: document.body, global: { plugins: [pinia, router] } })
  mounted.push(w)
  return w
}

const el = (sel: string) => document.body.querySelector(sel)
const find = (sel: string) => {
  const e = el(sel)
  if (!e) throw new Error(`nothing matches ${sel}`)
  return new DOMWrapper(e)
}
const has = (sel: string) => el(sel) !== null
const tapeCalls = () => GET.mock.calls.filter((c) => String(c[0]).endsWith('/tape-chart')).length
const flush = async () => { await flushPromises() }

async function openMenu(bar: string): Promise<void> {
  await find(`[data-testid=${bar}]`).trigger('click')
  await flush()
}

/** The bookings that have no room are folded until the section of their type is opened. */
async function openUnassigned(type = 'DLX'): Promise<void> {
  await find(`[data-testid=unassigned-toggle-${type}]`).trigger('click')
  await flush()
}

// jsdom has no layout: a night is 50px wide, and the point under the pointer is the room row named in `rowEls`
beforeEach(() => {
  setLocale('en')
  res = reservation()
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    return { width: this.hasAttribute('data-day-head') ? 50 : 0, height: 0, top: 0, left: 0, right: 0, bottom: 0, x: 0, y: 0, toJSON: () => ({}) } as DOMRect
  })
  rowEls = {}
  ;(document as unknown as { elementsFromPoint: (x: number, y: number) => Element[] }).elementsFromPoint = (x: number) => (x === 9999 ? [] : Object.values(rowEls).filter((r) => (r as HTMLElement).dataset.roomId === String(x)))
})
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
  vi.restoreAllMocks()
})

const down = async (sel: string, x = 0, y = 0): Promise<void> => {
  find(sel).element.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true, cancelable: true, button: 0, clientX: x, clientY: y }))
  await flush()
}
const pointer = (type: string, x: number, y: number) => window.dispatchEvent(Object.assign(new Event(type), { clientX: x, clientY: y }))
/** The pointer goes down on a block, moves over the row of a room (the x of the move names the room to the stub of elementsFromPoint), and is released. */
async function dragTo(bar: string, roomId: number, release = true): Promise<void> {
  const row = el(`[data-room-id="${roomId}"]`)
  if (row) rowEls[String(roomId)] = row // no row: the pointer is over nothing
  await down(`[data-testid=${bar}]`)
  pointer('pointermove', roomId, 30)
  await flush()
  if (release) {
    pointer('pointerup', roomId, 30)
    await flush()
  }
}

describe('the menu of a block', () => {
  it('opens on a click and offers move, change the dates and open the reservation', async () => {
    setup()
    await flush()
    expect(find('[data-testid=bar-101-2026-10-05]').attributes('aria-haspopup')).toBe('menu') // a button: the keyboard reaches it
    await openMenu('bar-101-2026-10-05')
    expect(find('[data-testid=menu-move]').text()).toBe('Move room…')
    expect(find('[data-testid=menu-dates]').text()).toBe('Change dates…')
    expect(find('[data-testid=menu-open]').text()).toBe('Open the reservation')
    expect(find('[data-testid=menu-open] a, a[data-testid=menu-open]').attributes('href')).toBe('/reservations/9')
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await flush()
    expect(has('[data-testid=tape-menu]')).toBe(false)
  })

  it('offers only what the role may do: nothing but the reservation to a reader, the move of a stay only with frontdesk.room_move', async () => {
    setup(['reservation.read'])
    await flush()
    await openMenu('bar-101-2026-10-05')
    expect(has('[data-testid=menu-move]')).toBe(false)
    expect(has('[data-testid=menu-dates]')).toBe(false)
    expect(has('[data-testid=menu-open]')).toBe(true)
    mounted.pop()!.unmount()
    document.body.innerHTML = ''
    setup(['reservation.read', 'reservation.update'])
    await flush()
    await openMenu('bar-102-2026-10-01') // the guest in the house
    expect(has('[data-testid=menu-move]')).toBe(false) // no frontdesk.room_move
    expect(has('[data-testid=menu-dates]')).toBe(true)
  })

  it('a block that is history (a closed segment of a stay) has no change, only the reservation', async () => {
    const c = chart()
    ;(c.rooms[2] as { bookings: object[] }).bookings.push({ ...base, reservation_id: 11, reservation_room_id: 6, room_type_id: T.DLX, status: 'CHECKED_IN', stay_id: 30, open_segment: false, arrival_date: '2026-10-02', departure_date: '2026-10-04' })
    setup(ALL, { chartData: c })
    await flush()
    await openMenu('bar-103-2026-10-02')
    expect(has('[data-testid=menu-move]')).toBe(false)
    expect(has('[data-testid=menu-dates]')).toBe(false)
    expect(has('[data-testid=menu-open]')).toBe(true)
  })

  it('assigns a room to a booking that has none, from its menu', async () => {
    setup()
    await flush()
    await openUnassigned()
    await openMenu('bar-unassigned-5')
    expect(find('[data-testid=menu-move]').text()).toBe('Assign a room…')
  })
})

describe('moving a booking to come: assign-room', () => {
  async function toDialog(): Promise<void> {
    await openMenu('bar-101-2026-10-05')
    await find('[data-testid=menu-move]').trigger('click')
    await flush()
  }

  it('asks for confirmation from the menu, reads the reservation again for its version, and sends assign-room', async () => {
    setup()
    await flush()
    await toDialog()
    expect(has('[data-testid=tape-move-dialog]')).toBe(true)
    expect(POST).not.toHaveBeenCalled() // nothing is sent before the confirmation
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/reservations/{id}'))).toBe(true)
    // the rooms that cannot take the booking are not offered: 102 is taken on those nights, 201 is out of service
    const options = Array.from(document.body.querySelectorAll('select[name=target_room] option')).map((o) => o.textContent)
    expect(options).toEqual(['103 · DLX', '202 · STD (upgrade)'])
    await find('select[name=target_room]').setValue(3)
    expect(find('[data-testid=tape-move-rooms]').text()).toBe('101 → 103')
    expect(find('[data-testid=tape-move-nights]').text()).toContain('3 nights')
    expect(find('[data-testid=tape-move-price]').text()).toBe('The price of the nights does not change.')
    expect(has('[data-testid=tape-move-upgrade]')).toBe(false)
    await find('[data-testid=tape-confirm]').trigger('click')
    await flush()
    const [path, init] = POST.mock.calls[0] as [string, { params: { path: { id: number; lineId: number } }; body: object }]
    expect(path).toBe('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/assign-room')
    expect(init.params.path).toMatchObject({ id: 9, lineId: 4 })
    expect(init.body).toEqual({ version: 7, room_id: 3, upgrade: false })
    expect(has('[data-testid=tape-move-dialog]')).toBe(false)
    expect(tapeCalls()).toBe(2) // the chart is read again
    expect(toastText()).toContain('Moved to room 103')
  })

  it('an upgrade is said in the confirmation and sent as an upgrade, with the permission', async () => {
    setup()
    await flush()
    await toDialog()
    await find('select[name=target_room]').setValue(5)
    expect(find('[data-testid=tape-move-upgrade]').text()).toBe('booked as DLX, in a STD room')
    await find('[data-testid=tape-confirm]').trigger('click')
    await flush()
    expect(POST.mock.calls[0]?.[1]).toMatchObject({ body: { room_id: 5, upgrade: true } })
  })

  it('without reservation.upgrade a room of another type is not offered, and a drop on it is refused', async () => {
    setup(['reservation.read', 'reservation.update', 'frontdesk.room_move'])
    await flush()
    await toDialog()
    expect(Array.from(document.body.querySelectorAll('select[name=target_room] option')).map((o) => o.textContent)).toEqual(['103 · DLX'])
    await find('[data-testid=tape-cancel]').trigger('click')
    await flush()
    await dragTo('bar-101-2026-10-05', 5, false)
    expect(find('[data-room-id="5"]').attributes('data-drop')).toBe('no')
    expect(find('[data-room-id="5"]').attributes('title')).toContain('upgrade needs its own permission')
    pointer('pointerup', 5, 30)
    await flush()
    expect(has('[data-testid=tape-move-dialog]')).toBe(false)
  })

  it('shows the refusal of the server and reads the chart again when the dialog is closed', async () => {
    setup()
    await flush()
    await toDialog()
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'ROOM_NOT_AVAILABLE', detail: 'the room is not available on those nights' }))
    await find('[data-testid=tape-confirm]').trigger('click')
    await flush()
    expect(has('[data-testid=tape-move-dialog]')).toBe(true) // stays open, with the message
    expect(toastText()).toContain('the room is not available')
    expect(tapeCalls()).toBe(1)
    await find('[data-testid=tape-cancel]').trigger('click')
    await flush()
    expect(tapeCalls()).toBe(2) // the chart is read again: somebody else changed it
  })
})

describe('moving a guest in the house', () => {
  async function toDialog(permissions = ALL): Promise<void> {
    setup(permissions)
    await flush()
    await openMenu('bar-102-2026-10-01')
    await find('[data-testid=menu-move]').trigger('click')
    await flush()
  }

  it('says it is from tonight, asks for a reason, reads the stay for its version and sends the move of the stay', async () => {
    await toDialog()
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/stays/{id}'))).toBe(true)
    expect(find('[data-testid=tape-move-nights]').text()).toContain('from tonight')
    expect(find('[data-testid=tape-move-nights]').text()).toContain('1 Oct 2026 → 6 Oct 2026')
    await find('select[name=target_room]').setValue(3)
    expect(find('[data-testid=tape-confirm]').attributes('disabled')).toBeDefined() // the reason is needed
    await find('input[name=reason]').setValue('air conditioning')
    await find('[data-testid=tape-confirm]').trigger('click')
    await flush()
    const [path, init] = POST.mock.calls[0] as [string, { params: { path: { id: number } }; body: object }]
    expect(path).toBe('/api/v1/properties/{propertyId}/stays/{id}/move')
    expect(init.params.path.id).toBe(30)
    expect(init.body).toMatchObject({ version: 4, room_id: 3, reason: 'air conditioning', override_room_not_ready: false })
    expect(POST.mock.calls.some((c) => String(c[0]).endsWith('/assign-room'))).toBe(false)
  })

  it('offers the override when the server says the room is not ready, to a role that may', async () => {
    await toDialog([...ALL, 'frontdesk.checkin_unready_room'])
    await find('select[name=target_room]').setValue(3)
    await find('input[name=reason]').setValue('noise')
    POST.mockRejectedValueOnce(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'ROOM_NOT_READY', detail: 'the room is not ready' }))
    await find('[data-testid=tape-confirm]').trigger('click')
    await flush()
    expect(has('[data-testid=tape-override]')).toBe(true)
    await find('input[name=override]').setValue(true)
    await find('input[name=override_reason]').setValue('guest agreed')
    await find('[data-testid=tape-confirm]').trigger('click')
    await flush()
    expect(POST.mock.calls[1]?.[1]).toMatchObject({ body: { room_id: 3, override_room_not_ready: true, override_reason: 'guest agreed' } })
  })

  it('does not offer the override without the permission', async () => {
    await toDialog()
    await find('select[name=target_room]').setValue(3)
    await find('input[name=reason]').setValue('noise')
    POST.mockRejectedValueOnce(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'ROOM_NOT_READY', detail: 'the room is not ready' }))
    await find('[data-testid=tape-confirm]').trigger('click')
    await flush()
    expect(has('[data-testid=tape-override]')).toBe(false)
    expect(toastText()).toContain('the room is not ready')
  })
})

describe('dragging a block to another room', () => {
  it('marks the rooms that cannot take it, lets the free one be dropped on, and opens the same confirmation', async () => {
    setup()
    await flush()
    await dragTo('bar-101-2026-10-05', 3, false)
    expect(find('[data-room-id="3"]').attributes('data-drop')).toBe('hover')
    expect(has('[data-testid=tape-ghost]')).toBe(true)
    expect(find('[data-room-id="2"]').attributes('data-drop')).toBe('no') // a guest in the house on those nights
    expect(find('[data-room-id="2"]').attributes('title')).toBe('Taken on those nights.')
    expect(find('[data-room-id="4"]').attributes('data-drop')).toBe('no') // out of service
    expect(find('[data-room-id="4"]').attributes('title')).toBe('Blocked on those nights.')
    expect(find('[data-room-id="5"]').attributes('data-drop')).toBe('ok') // another type, with the permission
    expect(find('[data-room-id="1"]').attributes('data-drop')).toBe('none') // its own room
    // the mark is over the nights of the block (5 to 8 Oct: columns 6 to 8), not the whole width of the row
    const zones = Array.from(document.body.querySelectorAll('[data-testid=tape-drop-zone]'))
    expect(zones.length).toBeGreaterThan(0)
    for (const z of zones) expect(z.getAttribute('style')).toContain('grid-column: 6 / span 3')
    expect(find('[data-room-id="2"] [data-testid=tape-drop-zone]').attributes('data-zone')).toBe('no')
    expect(find('[data-room-id="3"] [data-testid=tape-drop-zone]').attributes('data-zone')).toBe('hover')
    expect(find('[data-room-id="2"]').classes().join(' ')).not.toContain('status-hatch') // the row itself is not marked
    pointer('pointerup', 3, 30)
    await flush()
    expect(has('[data-testid=tape-move-dialog]')).toBe(true)
    expect(find('[data-testid=tape-move-rooms]').text()).toBe('101 → 103')
    expect(POST).not.toHaveBeenCalled() // a drop is never the move: the confirmation is
    expect(find('[data-testid=bar-101-2026-10-05]').classes()).not.toContain('opacity-40')
  })

  it('a drop on a room that cannot take it opens nothing and sends nothing', async () => {
    setup()
    await flush()
    await dragTo('bar-101-2026-10-05', 2)
    expect(has('[data-testid=tape-move-dialog]')).toBe(false)
    expect(POST).not.toHaveBeenCalled()
    expect(PATCH).not.toHaveBeenCalled()
  })

  it('a drop outside every room, and Esc during the drag, cancel it', async () => {
    setup()
    await flush()
    await dragTo('bar-101-2026-10-05', 9999)
    expect(has('[data-testid=tape-move-dialog]')).toBe(false)
    await dragTo('bar-101-2026-10-05', 3, false)
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await flush()
    expect(find('[data-room-id="3"]').attributes('data-drop')).toBe('none')
    pointer('pointerup', 3, 30)
    await flush()
    expect(has('[data-testid=tape-move-dialog]')).toBe(false)
  })

  it('a click without a move opens the menu, not a drag', async () => {
    setup()
    await flush()
    await down('[data-testid=bar-101-2026-10-05]')
    pointer('pointerup', 0, 0)
    await openMenu('bar-101-2026-10-05')
    expect(has('[data-testid=tape-menu]')).toBe(true)
    expect(has('[data-testid=tape-move-dialog]')).toBe(false)
  })

  it('a guest in the house is dragged by its open segment, with the move of the stay', async () => {
    setup()
    await flush()
    await dragTo('bar-102-2026-10-01', 3)
    expect(has('[data-testid=tape-move-dialog]')).toBe(true)
    expect(find('[data-testid=tape-move-nights]').text()).toContain('from tonight')
  })

  it('a booking with no room is dragged onto a room to assign it', async () => {
    setup()
    await flush()
    await openUnassigned()
    await dragTo('bar-unassigned-5', 3)
    expect(find('[data-testid=tape-move-rooms]').text()).toBe('No room yet → 103')
  })
})

describe('changing the nights', () => {
  async function dialog(bar: string, arrival?: string, departure?: string): Promise<void> {
    await openMenu(bar)
    await find('[data-testid=menu-dates]').trigger('click')
    await flush()
    if (arrival) await find('input[name=arrival_date]').setValue(arrival)
    if (departure) await find('input[name=departure_date]').setValue(departure)
    await flush()
  }

  it('shows an estimate from the server for the added nights and sends the amendment of the room line', async () => {
    setup()
    await flush()
    await dialog('bar-101-2026-10-05', undefined, '2026-10-10')
    expect(GET.mock.calls.find((c) => String(c[0]).endsWith('/availability'))?.[1]).toMatchObject({ params: { query: { arrival: '2026-10-08', departure: '2026-10-10', adults: 2, children: 0 } } })
    expect(find('[data-testid=tape-price-added]').text()).toBe('Estimate from the server for 2 added nights: 2,220,000')
    expect(find('[data-testid=tape-dates-new]').text()).toContain('5 nights')
    expect(POST).not.toHaveBeenCalled()
    expect(PATCH).not.toHaveBeenCalled()
    await find('[data-testid=tape-confirm]').trigger('click')
    await flush()
    const [path, init] = PATCH.mock.calls[0] as [string, { params: { path: { id: number; lineId: number } }; body: Record<string, unknown> }]
    expect(path).toBe('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}')
    expect(init.params.path).toMatchObject({ id: 9, lineId: 4 })
    expect(init.body).toEqual({ version: 7, arrival_date: undefined, departure_date: '2026-10-10' })
    expect(toastText()).toContain('Dates changed: 5 nights')
    expect(tapeCalls()).toBe(2)
  })

  it('extends a stay in the house with the change of departure of the stay', async () => {
    setup()
    await flush()
    await dialog('bar-102-2026-10-01', undefined, '2026-10-08')
    expect(find('input[name=arrival_date]').attributes('disabled')).toBeDefined() // the arrival of a stay cannot change
    expect(find('[data-testid=tape-price-added]').text()).toContain('2 added nights')
    await find('[data-testid=tape-confirm]').trigger('click')
    await flush()
    const [path, init] = POST.mock.calls[0] as [string, { params: { path: { id: number } }; body: object }]
    expect(path).toBe('/api/v1/properties/{propertyId}/stays/{id}/change-departure')
    expect(init.params.path.id).toBe(30)
    expect(init.body).toEqual({ version: 4, departure_date: '2026-10-08' })
  })

  it('refuses extending into a booking of the room, before asking the server', async () => {
    setup()
    await flush()
    await dialog('bar-102-2026-10-01', undefined, '2026-10-09') // the next guest of 102 arrives on the 8th
    expect(find('[data-testid=tape-dates-refusal]').text()).toBe('The room is taken on the added nights.')
    expect(find('[data-testid=tape-confirm]').attributes('disabled')).toBeDefined()
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/availability'))).toBe(false)
  })

  it('shows the nights that are taken away with the price they were sold at, and no estimate', async () => {
    setup()
    await flush()
    await dialog('bar-101-2026-10-05', undefined, '2026-10-06')
    const removed = find('[data-testid=tape-price-removed]').text()
    expect(removed).toContain('6 Oct 2026')
    expect(removed).toContain('1,000,000')
    expect(removed).toContain('7 Oct 2026')
    expect(has('[data-testid=tape-price-added]')).toBe(false)
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/availability'))).toBe(false)
  })

  it('changes the arrival of a booking that is not checked in, with the added night priced by the server', async () => {
    setup()
    await flush()
    await dialog('bar-101-2026-10-05', '2026-10-04')
    expect(GET.mock.calls.find((c) => String(c[0]).endsWith('/availability'))?.[1]).toMatchObject({ params: { query: { arrival: '2026-10-04', departure: '2026-10-05' } } })
    await find('[data-testid=tape-confirm]').trigger('click')
    await flush()
    expect(PATCH.mock.calls[0]?.[1]).toMatchObject({ body: { version: 7, arrival_date: '2026-10-04', departure_date: undefined } })
  })

  it('writes no figure for a line whose price was changed by hand: the system computes it when it is saved', async () => {
    res = reservation({}, { nightly_rates: [{ date: '2026-10-05', amount: '900000', is_override: true }] })
    setup()
    await flush()
    await dialog('bar-101-2026-10-05', undefined, '2026-10-10')
    expect(find('[data-testid=tape-price-system]').text()).toBe('The system computes the price when it is saved.')
    expect(has('[data-testid=tape-price-added]')).toBe(false)
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/availability'))).toBe(false) // not even asked
    expect(find('[data-testid=tape-confirm]').attributes('disabled')).toBeUndefined()
  })

  it('writes no figure for a reservation of a company, nor for a plan that is not paid', async () => {
    res = reservation({ company_id: 2 })
    setup()
    await flush()
    await dialog('bar-101-2026-10-05', undefined, '2026-10-10')
    expect(has('[data-testid=tape-price-system]')).toBe(true)
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/availability'))).toBe(false)
    mounted.pop()!.unmount()
    document.body.innerHTML = ''
    res = reservation({}, { occupancy_kind: 'COMPLIMENTARY' })
    setup()
    await flush()
    await dialog('bar-101-2026-10-05', undefined, '2026-10-10')
    expect(has('[data-testid=tape-price-system]')).toBe(true)
  })

  it('writes no figure when the server cannot price the nights (a night without a rate)', async () => {
    setup()
    GET.mockImplementation(async (path: string) => (path.endsWith('/tape-chart') ? { data: chart() } : path.endsWith('/reservations/{id}') ? { data: res } : path.endsWith('/availability') ? { data: offers({ missing_nights: 1 }) } : { data: {} }))
    await flush()
    await dialog('bar-101-2026-10-05', undefined, '2026-10-10')
    expect(find('[data-testid=tape-price-system]').text()).toContain('system computes the price')
  })

  it('shows a refusal of the server (a restriction, a night already charged) in the dialog, and reads the chart again', async () => {
    setup()
    await flush()
    await dialog('bar-101-2026-10-05', undefined, '2026-10-10')
    PATCH.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'VERSION_CONFLICT', detail: 'the reservation was changed by someone else' }))
    await find('[data-testid=tape-confirm]').trigger('click')
    await flush()
    expect(toastText()).toContain('changed by someone else')
    expect(has('[data-testid=tape-dates-dialog]')).toBe(true)
    await find('[data-testid=tape-cancel]').trigger('click')
    await flush()
    expect(tapeCalls()).toBe(2)
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    setup()
    await flush()
    await dialog('bar-101-2026-10-05', undefined, '2026-10-10')
    expect(find('[data-testid=tape-price-added]').text()).toBe('Perkiraan dari server untuk 2 malam tambahan: 2.220.000')
    expect(find('[data-testid=tape-dates-dialog]').text()).toContain('Ubah tanggal')
  })
})

describe('pulling the ends of a block', () => {
  const pullRight = async (bar: string, handle: string, columns: number, release = true): Promise<void> => {
    await down(`[data-handle="${handle}"]`)
    pointer('pointermove', columns * 50, 0)
    await flush()
    if (release) {
      pointer('pointerup', columns * 50, 0)
      await flush()
    }
    void bar
  }

  it('draws the block longer while the right end is pulled, and opens the confirmation with the new dates', async () => {
    setup()
    await flush()
    await down('[data-handle="right-4"]')
    pointer('pointermove', 100, 0) // two nights
    await flush()
    expect(find('[data-testid=bar-101-2026-10-05]').attributes('style')).toContain('grid-column: 6 / span 5')
    pointer('pointerup', 100, 0)
    await flush()
    expect(has('[data-testid=tape-dates-dialog]')).toBe(true)
    expect((find('input[name=departure_date]').element as HTMLInputElement).value).toBe('2026-10-10')
    expect(PATCH).not.toHaveBeenCalled()
  })

  it('pulls the left end of a booking to come, and has no left handle on a stay in the house', async () => {
    setup()
    await flush()
    expect(has('[data-handle="left-6"]')).toBe(false)
    expect(has('[data-handle="right-6"]')).toBe(true)
    await pullRight('bar-101-2026-10-05', 'left-4', -1)
    expect((find('input[name=arrival_date]').element as HTMLInputElement).value).toBe('2026-10-04')
  })

  it('keeps at least one night, and for a stay a departure after the business date', async () => {
    setup()
    await flush()
    await pullRight('bar-101-2026-10-05', 'right-4', -9, false)
    expect(find('[data-testid=bar-101-2026-10-05]').attributes('style')).toContain('span 1')
    pointer('pointerup', -450, 0)
    await flush()
    mounted.pop()!.unmount()
    document.body.innerHTML = ''
    setup()
    await flush()
    await pullRight('bar-102-2026-10-01', 'right-6', -9, false)
    expect(find('[data-testid=bar-102-2026-10-01]').attributes('style')).toContain('span 1') // the departure stays after the business date (1 Oct): 2 Oct at the earliest
  })

  it('a pull into a booking of the room is refused while it is made, and opens nothing', async () => {
    setup()
    await flush()
    await down('[data-handle="right-6"]')
    pointer('pointermove', 4 * 50, 0) // to 10 Oct: the next guest arrives on the 8th
    await flush()
    expect(find('[data-testid=tape-pull-refusal]').text()).toBe('The room is taken on the added nights.')
    pointer('pointerup', 4 * 50, 0)
    await flush()
    expect(has('[data-testid=tape-dates-dialog]')).toBe(false)
    expect(POST).not.toHaveBeenCalled()
  })
})

describe('without a mouse, and without the right', () => {
  it('has no drag and no handle on a phone or a touch screen: only the menu', async () => {
    setup(ALL, { fine: false })
    await flush()
    expect(document.body.querySelectorAll('[data-handle]')).toHaveLength(0)
    expect(find('[data-testid=tape-hint]').text()).toContain('Tap a block')
    await down('[data-testid=bar-101-2026-10-05]')
    pointer('pointermove', 3, 30)
    await flush()
    expect(find('[data-room-id="3"]').attributes('data-drop')).toBe('none')
    await openMenu('bar-101-2026-10-05')
    expect(has('[data-testid=menu-move]')).toBe(true) // the menu does everything the drag does
  })

  it('has no handles for a reader, and the hint of the drag only for who may change something', async () => {
    setup(['reservation.read'])
    await flush()
    expect(document.body.querySelectorAll('[data-handle]')).toHaveLength(0)
    expect(has('[data-testid=tape-hint]')).toBe(false)
    await dragTo('bar-101-2026-10-05', 3)
    expect(has('[data-testid=tape-move-dialog]')).toBe(false)
  })

  it('draws every block at 13 px or more', async () => {
    setup()
    await flush()
    for (const bar of Array.from(document.body.querySelectorAll('[data-booking]'))) expect(bar.className).toContain('text-[13px]')
  })
})
