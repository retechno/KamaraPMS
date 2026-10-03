import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import RoomsView from './RoomsView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({
  api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a) },
}))

const types = [
  { id: 1, code: 'DLX', name: 'Deluxe', is_active: true },
  { id: 2, code: 'OLD', name: 'Retired', is_active: false },
]
const beds = [
  { id: 5, code: 'KING', name: 'King', sort_order: 10, is_active: true },
  { id: 6, code: 'TWIN', name: 'Twin', sort_order: 40, is_active: true },
  { id: 7, code: 'OLD', name: 'Old sofa', sort_order: 90, is_active: false },
]
const rooms = [
  { id: 11, room_type_id: 1, room_number: '201', floor: '2', bed_type_id: 5, is_active: true },
  { id: 12, room_type_id: 1, room_number: '1001', floor: '10', is_active: true },
  { id: 13, room_type_id: 1, room_number: '202', floor: '2', is_active: true },
]

function mountRooms(permissions: string[]) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (path: string) => ({ data: { data: path.endsWith('/rooms') ? rooms : path.endsWith('/bed-types') ? beds : types } }))
  POST = vi.fn().mockResolvedValue({ data: {} })
  PATCH = vi.fn().mockResolvedValue({ data: {} })
  return mount(RoomsView, { global: { plugins: [pinia] } })
}

describe('RoomsView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
    PATCH = vi.fn()
  })

  it('sorts room numbers naturally (202 before 1001)', async () => {
    const w = mountRooms(['room.manage'])
    await flushPromises()
    const order = w.findAll('tbody tr').map((r) => r.attributes('data-testid'))
    expect(order).toEqual(['room-201', 'room-202', 'room-1001'])
  })

  it('creates a room with the chosen type and initial housekeeping status', async () => {
    const w = mountRooms(['room.manage'])
    await flushPromises()
    await w.get('[data-testid=new-room]').trigger('click')
    await w.get('input[name=room_number]').setValue('301')
    await w.get('input[name=floor]').setValue('3')
    await w.get('select[name=initial_housekeeping_status]').setValue('CLEAN')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/rooms', {
      params: { path: { propertyId: 7 } },
      body: { room_number: '301', room_type_id: 1, floor: '3', building: undefined, is_active: true, initial_housekeeping_status: 'CLEAN' },
    })
  })

  it('offers only active types for a new room', async () => {
    const w = mountRooms(['room.manage'])
    await flushPromises()
    await w.get('[data-testid=new-room]').trigger('click')
    const options = w.findAll('select[name=room_type_id] option').map((o) => o.text())
    expect(options).toEqual(['DLX · Deluxe'])
  })

  it('explains why a room cannot be deactivated', async () => {
    const w = mountRooms(['room.manage'])
    await flushPromises()
    PATCH.mockRejectedValue(
      new ApiError({
        type: 't', title: 'Conflict', status: 409, code: 'ROOM_IN_USE', detail: 'the room is occupied or assigned to future reservations',
        context: { conflicts: [{ type: 'STAY', id: 4, reference: 'STY000004', from: '2026-09-30', to: '2026-10-02' }] },
      }),
    )
    await w.get('[data-testid=room-201] button').trigger('click')
    await w.get('input[name=is_active]').setValue(false)
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('ROOM_IN_USE')
    expect(w.get('[data-testid=conflicts]').text()).toContain('STY000004')
  })

  it('hides editing without room.manage', async () => {
    const w = mountRooms([])
    await flushPromises()
    expect(w.find('[data-testid=new-room]').exists()).toBe(false)
    expect(w.find('[data-testid=room-201] button').exists()).toBe(false)
  })

  it('gives a room a bed type, keeps an inactive one it already has, and takes it off', async () => {
    const w = mountRooms(['room.manage'])
    await flushPromises()
    expect(w.get('[data-testid=room-201]').text()).toContain('King')
    // new room: only the active beds are offered, and none by default
    await w.get('[data-testid=new-room]').trigger('click')
    expect(w.findAll('select[name=bed_type_id] option').map((o) => o.text())).toEqual(['No bed type', 'King', 'Twin'])
    await w.get('input[name=room_number]').setValue('301')
    await w.get('select[name=bed_type_id]').setValue(6)
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body).toMatchObject({ room_number: '301', bed_type_id: 6 })
    // editing a room that has the bed type 201 has: it stays selected; 0 takes it off
    await w.get('[data-testid=room-201] button').trigger('click')
    expect((w.get('select[name=bed_type_id]').element as HTMLSelectElement).value).toBe('5')
    await w.get('select[name=bed_type_id]').setValue(0)
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(PATCH.mock.calls[0]?.[1].body).toMatchObject({ bed_type_id: 0 })
  })
})
