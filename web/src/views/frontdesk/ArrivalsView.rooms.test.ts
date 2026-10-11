import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { toastText } from '@/test/toasts'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ArrivalsView from './ArrivalsView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const KING = 7
const arrival = (id: number, over: object = {}) => ({
  reservation_id: 90 + id, confirmation_number: `RES0000${90 + id}`, reservation_room_id: id, reservation_version: 2 + id, guest_id: id, guest_name: `Guest ${id}`, room_type_id: 10, room_type_code: 'DLX',
  room_type_name: 'Deluxe', room_id: null, arrival_date: '2026-09-30', departure_date: '2026-10-02', adult_count: 2, child_count: 0, status: 'CONFIRMED', reservation_status: 'CONFIRMED',
  rate: { rate_plan_code: 'BAR', rate_plan_name: 'Best', amount: '1000000', price_mode: 'EXCLUSIVE' }, company: null, deposit: null, readiness: { status: 'BLOCKED', blockers: ['ROOM_NOT_ASSIGNED'] },
  requested_bed_type_id: null, bed_locked: false, ...over,
})
const FREE = [
  { room_id: 21, room_number: '101', housekeeping_status: 'DIRTY', bed_type_id: KING, bed_type_name: 'King' },
  { room_id: 22, room_number: '102', housekeeping_status: 'CLEAN', bed_type_id: KING, bed_type_name: 'King' },
  { room_id: 23, room_number: '103', housekeeping_status: 'INSPECTED', bed_type_id: 8, bed_type_name: 'Twin' },
]
const problem = (status: number, code: string) => new ApiError({ type: 't', title: 'x', status, code, detail: 'x' })

const mounted: VueWrapper[] = []
let arrivals: object[] = []
let push = vi.fn()
const body = (sel: string) => document.body.querySelector(sel)
const bodyAll = (sel: string) => [...document.body.querySelectorAll(sel)]
const arrivalCalls = () => GET.mock.calls.filter((c) => String(c[0]).endsWith('/arrivals')).length
const freeCalls = () => GET.mock.calls.filter((c) => String(c[0]).endsWith('/availability/rooms')).length

async function mountView(permissions = ['reservation.read', 'reservation.update', 'frontdesk.checkin']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-09-30' } as never
  property.current = { require_room_inspection_for_checkin: false } as never
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/arrivals')) return { data: { data: arrivals } }
    if (path.endsWith('/room-types')) return { data: { data: [{ id: 10, code: 'DLX', is_active: true }] } }
    if (path.endsWith('/availability/rooms')) return { data: { data: FREE } }
    return { data: {} }
  })
  POST = vi.fn(async (_p: string, init: { params: { path: { id: number } } }) => ({ data: { id: init.params.path.id, version: 50, rooms: [] } }))
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  push = vi.spyOn(router, 'push') as never
  const w = mount(ArrivalsView, { attachTo: document.body, global: { plugins: [pinia, router] } })
  mounted.push(w)
  await flushPromises()
  return w
}

describe('ArrivalsView: a room from the list', () => {
  beforeEach(() => {
    setLocale('en')
    arrivals = [arrival(1), arrival(2, { room_id: 30, room_number: '301', housekeeping_status: 'CLEAN' }), arrival(3, { status: 'CHECKED_IN', room_id: 31, room_number: '302' })]
  })
  afterEach(() => {
    while (mounted.length) mounted.pop()!.unmount()
    document.body.innerHTML = ''
  })

  it('offers "Choose room" on an arrival that has none, and nothing on one that has a room or is checked in', async () => {
    const w = await mountView()
    expect(w.find('[data-testid=pick-room-1]').exists()).toBe(true)
    expect(w.get('[data-testid=pick-room-1]').text()).toBe('Choose room')
    expect(w.find('[data-testid=pick-room-2]').exists()).toBe(false)
    expect(w.find('[data-testid=pick-room-3]').exists()).toBe(false)
  })

  it('offers nothing to a person who may not update a reservation', async () => {
    const w = await mountView(['reservation.read', 'frontdesk.checkin'])
    expect(w.find('[data-testid=pick-room-1]').exists()).toBe(false)
    expect(w.find('[data-testid=card-pick-room-1]').exists()).toBe(false)
    expect(w.find('[data-testid=change-room-2]').exists()).toBe(false)
    expect(w.find('[data-testid=auto-bar]').exists()).toBe(false)
  })

  it('has the picker on the card of a phone too', async () => {
    const w = await mountView()
    expect(w.find('[data-testid=card-pick-room-1]').exists()).toBe(true)
    expect(w.find('[data-testid=card-pick-room-2]').exists()).toBe(false)
  })

  it('lists the free rooms of the booked type for the nights of the arrival, and assigns the room that is chosen, with the version of the row, without leaving the page', async () => {
    const w = await mountView()
    await w.get('[data-testid=pick-room-1]').trigger('click')
    await flushPromises()
    expect(GET).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/availability/rooms', { params: { path: { propertyId: 7 }, query: { room_type_id: 10, arrival: '2026-09-30', departure: '2026-10-02' } } })
    const before = arrivalCalls()
    ;(body('[data-testid=room-option-22]') as HTMLElement).click()
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/assign-room', {
      params: { path: { propertyId: 7, id: 91, lineId: 1 } },
      body: { version: 3, room_id: 22, upgrade: false },
    })
    expect(arrivalCalls()).toBe(before + 1) // the list is read again, with its filters
    expect(toastText()).toContain('Room 102 assigned.')
    expect(push).not.toHaveBeenCalled()
    expect(body('[data-testid=room-picker]')).toBeNull()
  })

  it('puts the rooms that are ready first and marks the bed that was asked for', async () => {
    arrivals = [arrival(1, { requested_bed_type_id: KING })]
    const w = await mountView()
    await w.get('[data-testid=pick-room-1]').trigger('click')
    await flushPromises()
    // 102 is ready with a king, 103 is ready with another bed, 101 has the king but is dirty
    expect(bodyAll('[data-testid^=room-option-]').map((e) => e.querySelector('b')?.textContent)).toEqual(['102', '103', '101'])
    expect(body('[data-testid=room-option-102] [data-slot=bed-match]')).toBeNull()
    expect(body('[data-testid=room-option-22] [data-slot=bed-match]')).not.toBeNull()
  })

  it('changes the room of an arrival that has one, from the menu of the row, with the room list of the same type', async () => {
    const w = await mountView()
    expect(w.find('[data-testid=change-room-2]').exists()).toBe(false) // in the menu, not in the row
    await w.get('[data-testid=more-2]').trigger('click')
    await flushPromises()
    ;(body('[data-testid=change-room-2]') as HTMLElement).click()
    await new Promise((r) => setTimeout(r, 5))
    await flushPromises()
    expect(body('[data-testid=room-picker]')).not.toBeNull()
    expect(bodyAll('[data-testid^=room-option-]')).toHaveLength(3)
    ;(body('[data-testid=room-option-23]') as HTMLElement).click()
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/assign-room', {
      params: { path: { propertyId: 7, id: 92, lineId: 2 } },
      body: { version: 4, room_id: 23, upgrade: false },
    })
    expect(body('[data-testid=room-picker]')).toBeNull()
  })

  it('has no way to change the room of an arrival that is checked in', async () => {
    const w = await mountView()
    expect(w.find('[data-testid=pick-room-3]').exists()).toBe(false)
    await w.get('[data-testid=more-3]').trigger('click') // it has other things in its menu, but not this one
    await flushPromises()
    expect(body('[data-testid=menu-3]')).not.toBeNull()
    expect(body('[data-testid=change-room-3]')).toBeNull()
  })

  it('tells a room that was taken meanwhile, reads the free rooms and the arrivals again, and keeps the list open', async () => {
    const w = await mountView()
    await w.get('[data-testid=pick-room-1]').trigger('click')
    await flushPromises()
    POST = vi.fn(async () => {
      throw problem(409, 'ROOM_NOT_AVAILABLE')
    })
    const rooms = freeCalls()
    const lists = arrivalCalls()
    ;(body('[data-testid=room-option-22]') as HTMLElement).click()
    await flushPromises()
    expect(body('[data-testid=room-picker-error]')?.textContent).toContain('That room was taken meanwhile')
    expect(freeCalls()).toBe(rooms + 1)
    expect(arrivalCalls()).toBe(lists + 1)
    expect(body('[data-testid=room-picker]')).not.toBeNull()
  })

  it('tells a reservation that was changed meanwhile (an old version) and reads the row again', async () => {
    const w = await mountView()
    await w.get('[data-testid=pick-room-1]').trigger('click')
    await flushPromises()
    POST = vi.fn(async () => {
      throw problem(409, 'VERSION_CONFLICT')
    })
    const lists = arrivalCalls()
    ;(body('[data-testid=room-option-22]') as HTMLElement).click()
    await flushPromises()
    expect(body('[data-testid=room-picker-error]')?.textContent).toContain('The reservation was changed meanwhile')
    expect(arrivalCalls()).toBe(lists + 1)
  })

  describe('"Assign automatically"', () => {
    it('counts the arrivals without a room above the table, and is not there when every arrival has one', async () => {
      arrivals = [arrival(1), arrival(4)]
      const w = await mountView()
      expect(w.get('[data-testid=without-room]').text()).toBe('2 rooms are waiting for a room')
      expect(w.get('[data-testid=auto-assign-open]').text()).toBe('Assign automatically')
      w.unmount()
      mounted.pop()
      arrivals = [arrival(2, { room_id: 30, room_number: '301' })]
      const none = await mountView()
      expect(none.find('[data-testid=auto-bar]').exists()).toBe(false)
    })

    it('shows the proposal first and saves nothing before the confirmation', async () => {
      arrivals = [arrival(1), arrival(4)]
      const w = await mountView()
      await w.get('[data-testid=auto-assign-open]').trigger('click')
      await flushPromises()
      expect(body('[data-testid=auto-assign]')).not.toBeNull()
      expect(bodyAll('[data-testid=proposed-room]').map((e) => e.textContent)).toEqual(['102', '103']) // ready rooms; 101 is dirty
      expect(POST).not.toHaveBeenCalled()
    })

    it('saves what is confirmed, row by row, and reads the arrivals again', async () => {
      arrivals = [arrival(1), arrival(4)]
      const w = await mountView()
      await w.get('[data-testid=auto-assign-open]').trigger('click')
      await flushPromises()
      const lists = arrivalCalls()
      ;(body('[data-testid=auto-confirm]') as HTMLElement).click()
      await flushPromises()
      expect(POST).toHaveBeenCalledTimes(2)
      expect(POST.mock.calls.map((c) => (c[1] as { body: { room_id: number; version: number } }).body)).toEqual([
        { version: 3, room_id: 22, upgrade: false },
        { version: 6, room_id: 23, upgrade: false },
      ])
      expect(arrivalCalls()).toBe(lists + 1)
      expect(body('[data-testid=auto-summary]')?.textContent).toBe('2 assigned, 0 not.')
    })

    it('marks the one that fails and goes on', async () => {
      arrivals = [arrival(1), arrival(4)]
      const w = await mountView()
      await w.get('[data-testid=auto-assign-open]').trigger('click')
      await flushPromises()
      POST = vi.fn(async (_p: string, init: { body: { room_id: number }; params: { path: { id: number } } }) => {
        if (init.body.room_id === 22) throw problem(409, 'ROOM_NOT_AVAILABLE')
        return { data: { id: init.params.path.id, version: 9, rooms: [] } }
      })
      ;(body('[data-testid=auto-confirm]') as HTMLElement).click()
      await flushPromises()
      expect(POST).toHaveBeenCalledTimes(2)
      expect(body('[data-testid=outcome-failed]')?.textContent).toContain('That room was taken meanwhile')
      expect(body('[data-testid=auto-summary]')?.textContent).toBe('1 assigned, 1 not.')
    })

    it('says it in Indonesian', async () => {
      setLocale('id')
      arrivals = [arrival(1)]
      const w = await mountView()
      expect(w.get('[data-testid=without-room]').text()).toBe('1 kamar belum ditetapkan kamarnya')
      expect(w.get('[data-testid=auto-assign-open]').text()).toBe('Tetapkan otomatis')
      expect(w.get('[data-testid=pick-room-1]').text()).toBe('Pilih kamar')
    })
  })
})
