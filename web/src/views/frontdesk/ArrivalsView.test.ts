import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ArrivalsView from './ArrivalsView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const arrival = (over: object = {}) => ({
  reservation_id: 9, confirmation_number: 'RES000009', reservation_room_id: 4, reservation_version: 2, guest_id: 3, guest_name: 'Siti Nurhaliza',
  room_type_id: 10, room_type_code: 'DLX', room_id: null, arrival_date: '2026-09-30', departure_date: '2026-10-02', adult_count: 2, child_count: 0, ...over,
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
  POST = vi.fn().mockResolvedValue({ data: { stay: { id: 55 } } })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  const push = vi.spyOn(router, 'push')
  return { w: mount(ArrivalsView, { global: { plugins: [pinia, router] } }), push }
}

describe('ArrivalsView and the check-in panel', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
  })

  it('lists today\'s arrivals', async () => {
    const { w } = mountView()
    await flushPromises()
    expect(w.get('[data-testid=arrival-4]').text()).toContain('Siti Nurhaliza')
    expect(w.find('[data-testid=walk-in]').exists()).toBe(true)
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
    expect(w.get('select[name=room]').findAll('option')[0]?.text()).toBe('301 · INSPECTED')
    expect(w.find('[data-testid=not-ready]').exists()).toBe(false)
  })

  it('needs read permission and hides check-in without frontdesk.checkin', async () => {
    const none = mountView(['guest.read'])
    await flushPromises()
    expect(none.w.find('[data-testid=no-access]').exists()).toBe(true)
    const reader = mountView(['reservation.read'])
    await flushPromises()
    expect(reader.w.find('[data-testid=open-4]').exists()).toBe(false)
    expect(reader.w.find('[data-testid=walk-in]').exists()).toBe(false)
  })

  it('offers free rooms with their status, and checks a ready room in with an Idempotency-Key', async () => {
    const { w, push } = mountView()
    await flushPromises()
    await w.get('[data-testid=open-4]').trigger('click')
    await flushPromises()
    expect(GET.mock.calls.at(-1)).toEqual(['/api/v1/properties/{propertyId}/availability/rooms', { params: { path: { propertyId: 7 }, query: { room_type_id: 10, arrival: '2026-09-30', departure: '2026-10-02' } } }])
    const options = w.get('select[name=room]').findAll('option').map((o) => o.text())
    expect(options).toEqual(['101 · DIRTY (not ready)', '102 · CLEAN'])
    // the first room is dirty: the button is off until a ready room is chosen
    expect(w.get('[data-testid=not-ready]').text()).toContain('101 is DIRTY')
    expect(w.get('[data-testid=checkin-submit]').attributes('disabled')).toBeDefined()
    await w.get('select[name=room]').setValue(22)
    expect(w.find('[data-testid=not-ready]').exists()).toBe(false)
    await w.get('form[data-testid=checkin-4]').trigger('submit')
    await flushPromises()
    const [path, init] = POST.mock.calls[0] as [string, { params: { header: Record<string, string> }; body: object }]
    expect(path).toBe('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/check-in')
    expect(init.params.header['Idempotency-Key']).toBeTruthy()
    expect(init.body).toMatchObject({ version: 2, room_id: 22, guest_id: 3, adult_count: 2, child_count: 0, override_room_not_ready: false })
    expect(push).toHaveBeenCalledWith('/stays/55')
  })

  it('needs the override permission and a reason to use a room that is not ready', async () => {
    const { w } = mountView()
    await flushPromises()
    await w.get('[data-testid=open-4]').trigger('click')
    await flushPromises()
    expect(w.find('input[name=override]').exists()).toBe(false) // no frontdesk.checkin_unready_room

    const sup = mountView(['reservation.read', 'frontdesk.checkin', 'frontdesk.checkin_unready_room'])
    await flushPromises()
    await sup.w.get('[data-testid=open-4]').trigger('click')
    await flushPromises()
    await sup.w.get('input[name=override]').setValue(true)
    await sup.w.get('input[name=override_reason]').setValue('guest waiting')
    await sup.w.get('form[data-testid=checkin-4]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1]).toMatchObject({ body: { room_id: 21, override_room_not_ready: true, override_reason: 'guest waiting' } })
  })

  it('a property that requires inspection treats CLEAN as not ready', async () => {
    const { w } = mountView(undefined, [arrival()], true)
    await flushPromises()
    await w.get('[data-testid=open-4]').trigger('click')
    await flushPromises()
    await w.get('select[name=room]').setValue(22)
    expect(w.get('[data-testid=not-ready]').text()).toContain('an inspected room is needed')
  })

  it('shows the server error and stays open', async () => {
    const { w, push } = mountView()
    await flushPromises()
    await w.get('[data-testid=open-4]').trigger('click')
    await flushPromises()
    await w.get('select[name=room]').setValue(22)
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'ROOM_OCCUPIED', detail: 'the room is occupied' }))
    await w.get('form[data-testid=checkin-4]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=checkin-error]').text()).toContain('ROOM_OCCUPIED')
    expect(push).not.toHaveBeenCalled()
  })

  it('warns when the reservation has no guest', async () => {
    const { w } = mountView(undefined, [arrival({ guest_id: null, guest_name: '' })])
    await flushPromises()
    await w.get('[data-testid=open-4]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid=no-guest]').exists()).toBe(true)
    expect(w.get('[data-testid=checkin-submit]').attributes('disabled')).toBeDefined()
  })

  it('offers the assigned room even though the line holds it, and marks another type as an upgrade', async () => {
    const { w } = mountView(undefined, [arrival({ room_id: 21, room_number: '101' })])
    await flushPromises()
    GET.mockImplementation(async (path: string) => (path.endsWith('/availability/rooms') ? { data: { data: [{ room_id: 22, room_number: '102', housekeeping_status: 'CLEAN' }] } } : path.endsWith('/room-types') ? { data: { data: types } } : { data: { data: [arrival({ room_id: 21, room_number: '101' })] } }))
    await w.get('[data-testid=open-4]').trigger('click')
    await flushPromises()
    expect(w.get('select[name=room]').findAll('option').map((o) => o.text())[0]).toContain('101')
    await w.get('select[name=room_type]').setValue(11)
    await flushPromises()
    expect(w.find('[data-testid=upgrade-note]').exists()).toBe(true)
  })
})
