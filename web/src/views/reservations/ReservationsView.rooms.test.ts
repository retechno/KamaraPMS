import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { toastText } from '@/test/toasts'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ReservationsView from './ReservationsView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const line = (id: number, over: object = {}) => ({
  id, status: 'CONFIRMED', room_type_id: 10, room_type_code: 'DLX', room_id: null, rate_plan_code: 'BAR', nights: 2, adult_count: 2, child_count: 0, rate_amount: '1200000', price_mode: 'EXCLUSIVE',
  arrival_date: '2026-10-02', departure_date: '2026-10-04', requested_bed_type_id: null, bed_locked: false, stay_id: null, ...over,
})
const row = (id: number, rooms: object[], over: object = {}) => ({
  id, confirmation_number: `RES00000${id}`, guest_id: 3, guest_name: `Guest ${id}`, source: 'PHONE', status: 'CONFIRMED', display_status: 'CONFIRMED', arrival_date: '2026-10-02', departure_date: '2026-10-04',
  nights: 2, room_count: rooms.length, rooms, deposit: null, version: 2, created_at: '2026-09-30T00:00:00Z', ...over,
})
const FREE = [
  { room_id: 21, room_number: '101', housekeeping_status: 'DIRTY', bed_type_id: null },
  { room_id: 22, room_number: '102', housekeeping_status: 'CLEAN', bed_type_id: null },
  { room_id: 23, room_number: '103', housekeeping_status: 'INSPECTED', bed_type_id: null },
]
const problem = (status: number, code: string) => new ApiError({ type: 't', title: 'x', status, code, detail: 'x' })
const UPDATE = ['reservation.read', 'reservation.update']

let mounted: VueWrapper | null = null
let page: object[] = []
let refreshed: Record<number, object> = {}
const body = (sel: string) => document.body.querySelector(sel)
const bodyAll = (sel: string) => [...document.body.querySelectorAll(sel)]
const listCalls = () => GET.mock.calls.filter((c) => String(c[0]).endsWith('/reservations'))

async function mountView(permissions = UPDATE, width = 1280) {
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: width })
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-10-02' } as never
  property.current = { require_room_inspection_for_checkin: false } as never
  GET = vi.fn(async (path: string, init?: { params?: { query?: { q?: string } } }) => {
    if (path.endsWith('/room-types')) return { data: { data: [{ id: 10, code: 'DLX', is_active: true }] } }
    if (path.endsWith('/rate-plans') || path.endsWith('/companies')) return { data: { data: [] } }
    if (path.endsWith('/availability/rooms')) return { data: { data: FREE } }
    if (path.endsWith('/reservations')) {
      const q = init?.params?.query?.q
      if (q) return { data: { data: Object.values(refreshed).filter((r) => (r as { confirmation_number: string }).confirmation_number === q) } }
      return { data: { data: page } }
    }
    return { data: {} }
  })
  POST = vi.fn(async (_p: string, init: { params: { path: { id: number; lineId: number } }; body: { version: number; room_id: number } }) => ({
    data: { id: init.params.path.id, version: init.body.version + 1, rooms: [{ id: init.params.path.lineId, room_id: init.body.room_id, room_number: String(init.body.room_id + 80) }] },
  }))
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  await router.push('/reservations')
  mounted = mount(ReservationsView, { attachTo: document.body, global: { plugins: [pinia, router] } })
  await flushPromises()
  return { w: mounted, router }
}
const cell = (n: string) => `[data-testid=res-RES00000${n}]`

describe('ReservationsView: rooms from the list', () => {
  beforeEach(() => {
    setLocale('en')
    refreshed = {}
    page = [
      row(1, [line(10)]), // one room, none assigned
      row(2, [line(20, { room_id: 30, room_number: '301' })]), // one room, assigned
      row(3, [line(30), line(31, { room_type_code: 'STD', room_type_id: 11 })]), // two rooms, none assigned
      row(4, [line(40, { status: 'CHECKED_IN', room_id: 31, room_number: '302', stay_id: 5 })], { display_status: 'IN_HOUSE' }),
      row(5, [line(50, { status: 'CANCELLED' })], { display_status: 'CANCELLED', status: 'CANCELLED' }),
    ]
  })
  afterEach(() => {
    mounted?.unmount()
    mounted = null
    document.body.innerHTML = ''
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 })
  })

  describe('"Choose room" in the Room column of a reservation of one room', () => {
    it('is offered for a reservation of one room that waits for check-in and has none, and for no other', async () => {
      const { w } = await mountView()
      expect(w.get('[data-testid=pick-room-RES000001]').text()).toBe('Choose room')
      expect(w.find('[data-testid=pick-room-RES000002]').exists()).toBe(false) // it has a room
      expect(w.find('[data-testid=pick-room-RES000003]').exists()).toBe(false) // two rooms: the sheet of the menu
      expect(w.find('[data-testid=pick-room-RES000004]').exists()).toBe(false) // checked in
      expect(w.find('[data-testid=pick-room-RES000005]').exists()).toBe(false) // cancelled
    })

    it('says how many rooms of a reservation of several have one, in the colour of a warning, and keeps the old text when all have', async () => {
      page = [
        row(1, [line(10, { room_id: 30, room_number: '301' }), line(11), line(12, { room_type_code: 'STD', room_type_id: 11 })]),
        row(3, [line(30), line(31, { room_type_code: 'STD', room_type_id: 11 })]),
        row(2, [line(20, { room_id: 30, room_number: '301' }), line(21, { room_id: 31, room_number: '302' })]),
      ]
      const { w } = await mountView()
      const part = w.get('[data-testid=rooms-progress-RES000001]')
      expect(part.text()).toBe('1 of 3 rooms assigned')
      expect(part.classes()).toContain('text-warning-text')
      expect(w.get('[data-testid=rooms-progress-RES000003]').text()).toBe('0 of 2 rooms assigned')
      expect(w.find('[data-testid=rooms-progress-RES000002]').exists()).toBe(false) // all have a room: "301 +1 more" as before
      expect(w.get('[data-testid=room-RES000002]').text()).toContain('301')
    })

    it('is offered on the card of a phone too', async () => {
      const { w } = await mountView(UPDATE, 390)
      expect(w.get('[data-testid=pick-room-RES000001]').text()).toBe('Choose room')
    })

    it('is not offered to a person who may not update a reservation', async () => {
      const { w } = await mountView(['reservation.read'])
      expect(w.find('[data-testid=pick-room-RES000001]').exists()).toBe(false)
    })

    it('assigns the room that is chosen with the version of the row, and puts the room and the new version on the row, without reading the list again or leaving it', async () => {
      const { w, router } = await mountView()
      await w.get('[data-testid=pick-room-RES000001]').trigger('click')
      await flushPromises()
      expect(GET).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/availability/rooms', { params: { path: { propertyId: 7 }, query: { room_type_id: 10, arrival: '2026-10-02', departure: '2026-10-04' } } })
      const lists = listCalls().length
      ;(body('[data-testid=room-option-22]') as HTMLElement).click()
      await flushPromises()
      expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/assign-room', {
        params: { path: { propertyId: 7, id: 1, lineId: 10 } },
        body: { version: 2, room_id: 22, upgrade: false },
      })
      expect(w.get('[data-testid=room-RES000001]').text()).toContain('102') // the room of the answer (22 + 80 in the double of this test)
      expect(w.find('[data-testid=pick-room-RES000001]').exists()).toBe(false) // it has a room now
      expect(listCalls().length).toBe(lists)
      expect(router.currentRoute.value.path).toBe('/reservations') // the row was not opened
      expect(toastText()).toContain('Room 102 assigned.')
    })

    it('tells a room that was taken meanwhile, reads the row again by its number and replaces it in place', async () => {
      const { w } = await mountView()
      await w.get('[data-testid=pick-room-RES000001]').trigger('click')
      await flushPromises()
      refreshed[1] = row(1, [line(10, { room_id: 23, room_number: '103' })], { version: 8 })
      POST = vi.fn(async () => {
        throw problem(409, 'ROOM_NOT_AVAILABLE')
      })
      ;(body('[data-testid=room-option-22]') as HTMLElement).click()
      await flushPromises()
      const refresh = listCalls().find((c) => (c[1] as { params: { query: { q?: string } } }).params.query.q === 'RES000001')
      expect(refresh?.[1]).toMatchObject({ params: { path: { propertyId: 7 }, query: { q: 'RES000001', limit: 5 } } })
      expect(w.get('[data-testid=room-RES000001]').text()).toContain('103') // the row is as it is now
    })

    it('tells a reservation that was changed meanwhile (an old version)', async () => {
      const { w } = await mountView()
      await w.get('[data-testid=pick-room-RES000001]').trigger('click')
      await flushPromises()
      POST = vi.fn(async () => {
        throw problem(409, 'VERSION_CONFLICT')
      })
      ;(body('[data-testid=room-option-22]') as HTMLElement).click()
      await flushPromises()
      expect(body('[data-testid=room-picker-error]')?.textContent).toContain('The reservation was changed meanwhile')
    })
  })

  describe('"Assign rooms" in the menu of a row', () => {
    const open = async (w: VueWrapper, n: string) => {
      document.body.querySelectorAll('[data-slot=row-menu]').forEach((m) => m.remove())
      await w.get(`${cell(n)} [data-slot=row-menu-trigger]`).trigger('click')
      await flushPromises()
      ;(body(`[data-testid=assign-rooms-RES00000${n}]`) as HTMLElement).click()
      await flushPromises()
    }

    it('is in the menu of a reservation with a room that waits for check-in, for who may update, and not otherwise', async () => {
      const { w } = await mountView()
      for (const n of ['1', '2', '3']) {
        document.body.querySelectorAll('[data-slot=row-menu]').forEach((m) => m.remove())
        await w.get(`${cell(n)} [data-slot=row-menu-trigger]`).trigger('click')
        await flushPromises()
        expect(body(`[data-testid=assign-rooms-RES00000${n}]`), n).not.toBeNull()
      }
      expect(w.find(`${cell('4')} [data-slot=row-menu-trigger]`).exists() && body('[data-testid=assign-rooms-RES000004]') !== null).toBe(false)
      expect(body('[data-testid=assign-rooms-RES000005]')).toBeNull()
      mounted?.unmount()
      document.body.innerHTML = ''
      const reader = await mountView(['reservation.read'])
      expect(reader.w.find('[data-testid=assign-rooms-RES000001]').exists()).toBe(false)
    })

    it('opens a sheet with each room that waits for check-in: its type, its nights and its room now, each with a picker of its own', async () => {
      const { w } = await mountView()
      await open(w, '3')
      expect(body('[data-testid=assign-rooms-sheet]')).not.toBeNull()
      expect(body('[data-testid=assign-rooms-sheet]')?.textContent).toContain('Rooms of RES000003')
      expect(body('[data-testid=sheet-line-30]')?.textContent).toContain('DLX')
      expect(body('[data-testid=sheet-line-31]')?.textContent).toContain('STD')
      expect(body('[data-testid=sheet-line-30]')?.textContent).toMatch(/2 Oct 2026\s*→\s*4 Oct 2026/) // the arrow of the tables, not "to"
      expect(body('[data-testid=sheet-room-30]')?.textContent).toBe('No room yet')
      expect(body('[data-testid=sheet-pick-30]')?.textContent).toBe('Choose room')
      expect(body('[data-testid=sheet-pick-31]')?.textContent).toBe('Choose room')
    })

    it('leaves out a room that is checked in, and says "Change room" where a room is assigned', async () => {
      page = [row(6, [line(60, { room_id: 40, room_number: '401' }), line(61, { status: 'CHECKED_IN', room_id: 41, room_number: '402', stay_id: 7 }), line(62)])]
      const { w } = await mountView()
      await open(w, '6')
      expect(body('[data-testid=sheet-line-61]')).toBeNull()
      expect(body('[data-testid=sheet-pick-60]')?.textContent).toBe('Change room')
      expect(body('[data-testid=sheet-room-60]')?.textContent).toBe('401')
      expect(body('[data-testid=sheet-pick-62]')?.textContent).toBe('Choose room')
    })

    it('assigns the rooms one after the other: the second asks for the version that the answer of the first gave', async () => {
      const { w } = await mountView()
      await open(w, '3')
      ;(body('[data-testid=sheet-pick-30]') as HTMLElement).click()
      await flushPromises()
      ;(body('[data-testid=room-option-22]') as HTMLElement).click()
      await flushPromises()
      expect(POST.mock.calls[0]?.[1]).toMatchObject({ params: { path: { id: 3, lineId: 30 } }, body: { version: 2, room_id: 22 } })
      expect(body('[data-testid=sheet-room-30]')?.textContent).toBe('102') // the sheet follows the row
      ;(body('[data-testid=sheet-pick-31]') as HTMLElement).click()
      await flushPromises()
      ;(body('[data-testid=room-option-23]') as HTMLElement).click()
      await flushPromises()
      expect(POST.mock.calls[1]?.[1]).toMatchObject({ params: { path: { id: 3, lineId: 31 } }, body: { version: 3, room_id: 23 } }) // 3, the version the first answer gave
      expect(body('[data-testid=sheet-room-31]')?.textContent).toBe('103')
    })

    it('changes the room of a line that has one: the same call, with the room that is on the line', async () => {
      page = [row(6, [line(60, { room_id: 40, room_number: '401' })])]
      const { w } = await mountView()
      await open(w, '6')
      ;(body('[data-testid=sheet-pick-60]') as HTMLElement).click()
      await flushPromises()
      ;(body('[data-testid=room-option-21]') as HTMLElement).click()
      await flushPromises()
      expect(POST.mock.calls[0]?.[1]).toMatchObject({ params: { path: { id: 6, lineId: 60 } }, body: { version: 2, room_id: 21, upgrade: false } })
    })

    it('offers "Assign automatically" for the rooms of that reservation only, shows the proposal first, saves one by one and reads the row again', async () => {
      page = [row(7, [line(70), line(71)]), row(8, [line(80)])]
      const { w } = await mountView()
      await open(w, '7')
      ;(body('[data-testid=sheet-auto]') as HTMLElement).click()
      await flushPromises()
      expect(body('[data-testid=auto-assign]')).not.toBeNull()
      expect(bodyAll('[data-testid^=proposal-]').filter((e) => /^proposal-\d+:\d+$/.test(e.getAttribute('data-testid')!)).map((e) => e.getAttribute('data-testid'))).toEqual(['proposal-7:70', 'proposal-7:71']) // not RES000008
      expect(POST).not.toHaveBeenCalled()
      refreshed[7] = row(7, [line(70, { room_id: 22, room_number: '102' }), line(71, { room_id: 23, room_number: '103' })], { version: 4 })
      ;(body('[data-testid=auto-confirm]') as HTMLElement).click()
      await flushPromises()
      expect(POST).toHaveBeenCalledTimes(2)
      expect(POST.mock.calls.map((c) => (c[1] as { body: { version: number } }).body.version)).toEqual([2, 3]) // one reservation: the version goes on
      expect(body('[data-testid=auto-summary]')?.textContent).toBe('2 assigned, 0 not.')
      expect(listCalls().some((c) => (c[1] as { params: { query: { q?: string } } }).params.query.q === 'RES000007')).toBe(true) // the row is read again
    })

    it('tells a conflict in the sheet and reads the row again', async () => {
      const { w } = await mountView()
      await open(w, '3')
      ;(body('[data-testid=sheet-pick-30]') as HTMLElement).click()
      await flushPromises()
      refreshed[3] = row(3, [line(30, { room_id: 21, room_number: '101' }), line(31, { room_type_code: 'STD', room_type_id: 11 })], { version: 9 })
      POST = vi.fn(async () => {
        throw problem(409, 'ROOM_NOT_AVAILABLE')
      })
      ;(body('[data-testid=room-option-22]') as HTMLElement).click()
      await flushPromises()
      expect(body('[data-testid=room-picker-error]')?.textContent).toContain('That room was taken meanwhile')
      expect(body('[data-testid=sheet-room-30]')?.textContent).toBe('101') // the sheet shows the row as it is now
    })

    it('says it in Indonesian', async () => {
      setLocale('id')
      const { w } = await mountView()
      expect(w.get('[data-testid=pick-room-RES000001]').text()).toBe('Pilih kamar')
      await open(w, '3')
      expect(body('[data-testid=assign-rooms-sheet]')?.textContent).toContain('Kamar RES000003')
      expect(body('[data-testid=sheet-auto]')?.textContent).toBe('Tetapkan otomatis')
    })
  })
})
