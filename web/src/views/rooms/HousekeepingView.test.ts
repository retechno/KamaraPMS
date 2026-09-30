import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import HousekeepingView from './HousekeepingView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const room = (over: Record<string, unknown>) => ({
  room_id: 1, room_number: '201', floor: '2', room_type_id: 1, room_type_code: 'DLX', room_type_name: 'Deluxe',
  status: 'DIRTY', status_updated_at: '2026-09-30T10:00:00Z', occupancy: 'VACANT', allowed_next: ['CLEANING', 'CLEAN'], ...over,
})

function mountBoard(permissions: string[], board: object[]) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const auth = useAuthStore()
  auth.me = {
    user: { id: 5, is_tenant_admin: false },
    properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }],
  } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: { data: board } })
  POST = vi.fn().mockResolvedValue({ data: {} })
  return mount(HousekeepingView, { global: { plugins: [pinia] } })
}

describe('HousekeepingView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
  })

  it('lists rooms with status, derived occupancy and blocks', async () => {
    const w = mountBoard(['housekeeping.update'], [
      room({}),
      room({ room_id: 2, room_number: '202', status: 'CLEAN', occupancy: 'OCCUPIED', allowed_next: ['INSPECTED', 'DIRTY'], block: { type: 'OOO', end_date: '2026-10-04' } }),
    ])
    await flushPromises()
    expect(GET).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/housekeeping', { params: { path: { propertyId: 7 } } })
    expect(w.get('[data-testid=room-201] [data-testid=status]').text()).toBe('Dirty')
    expect(w.get('[data-testid=room-202]').text()).toContain('occupied')
    expect(w.get('[data-testid=room-202] [data-testid=block]').text()).toBe('OOO until 2026-10-04')
  })

  it('offers only the legal next statuses and hides INSPECTED without housekeeping.inspect', async () => {
    const w = mountBoard(['housekeeping.update'], [room({ status: 'CLEAN', allowed_next: ['INSPECTED', 'DIRTY'] })])
    await flushPromises()
    expect(w.find('[data-testid=act-DIRTY]').exists()).toBe(true)
    expect(w.find('[data-testid=act-INSPECTED]').exists()).toBe(false)
    expect(w.find('[data-testid=act-CLEAN]').exists()).toBe(false)
  })

  it('shows INSPECTED to supervisors and no actions to read-only users', async () => {
    const sup = mountBoard(['housekeeping.update', 'housekeeping.inspect'], [room({ status: 'CLEAN', allowed_next: ['INSPECTED', 'DIRTY'] })])
    await flushPromises()
    expect(sup.find('[data-testid=act-INSPECTED]').exists()).toBe(true)

    const reader = mountBoard([], [room({})])
    await flushPromises()
    expect(reader.findAll('button').filter((b) => b.attributes('data-testid')?.startsWith('act-'))).toHaveLength(0)
  })

  it('posts the change and reloads the board', async () => {
    const w = mountBoard(['housekeeping.update'], [room({})])
    await flushPromises()
    GET.mockResolvedValue({ data: { data: [room({ status: 'CLEANING', allowed_next: ['CLEAN', 'DIRTY'] })] } })
    await w.get('[data-testid=act-CLEANING]').trigger('click')
    await flushPromises()

    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/rooms/{id}/housekeeping', {
      params: { path: { propertyId: 7, id: 1 } },
      body: { status: 'CLEANING' },
    })
    expect(w.get('[data-testid=status]').text()).toBe('Cleaning')
  })

  it('shows the error code and refreshes when another user changed the room first', async () => {
    const w = mountBoard(['housekeeping.update'], [room({})])
    await flushPromises()
    POST.mockRejectedValue(
      new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'INVALID_HK_TRANSITION', detail: 'cannot change from CLEANING to CLEANING' }),
    )
    GET.mockResolvedValue({ data: { data: [room({ status: 'CLEANING', allowed_next: ['CLEAN', 'DIRTY'] })] } })
    await w.get('[data-testid=act-CLEANING]').trigger('click')
    await flushPromises()

    expect(w.get('[data-testid=hk-error]').text()).toContain('INVALID_HK_TRANSITION')
    expect(w.get('[data-testid=status]').text()).toBe('Cleaning')
  })

  it('filters by status chip', async () => {
    const w = mountBoard([], [room({}), room({ room_id: 2, room_number: '202', status: 'CLEAN', allowed_next: [] })])
    await flushPromises()
    await w.get('[data-testid=filter-CLEAN]').trigger('click')
    expect(w.find('[data-testid=room-201]').exists()).toBe(false)
    expect(w.find('[data-testid=room-202]').exists()).toBe(true)
  })
})
