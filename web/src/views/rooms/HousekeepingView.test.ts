import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import HousekeepingView from './HousekeepingView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PUT = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PUT: (...a: unknown[]) => PUT(...a) } }))

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
  PUT = vi.fn().mockResolvedValue({ data: {} })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(HousekeepingView, { global: { plugins: [pinia, router] } })
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
    expect(w.get('[data-testid=room-202]').text()).toContain('Occupied')
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
    expect(w.get('[data-testid=status]').text()).toBe('Being cleaned')
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
    expect(w.get('[data-testid=status]').text()).toBe('Being cleaned')
  })

  it('filters by status chip', async () => {
    const w = mountBoard([], [room({}), room({ room_id: 2, room_number: '202', status: 'CLEAN', allowed_next: [] })])
    await flushPromises()
    await w.get('[data-testid=filter-CLEAN]').trigger('click')
    expect(w.find('[data-testid=room-201]').exists()).toBe(false)
    expect(w.find('[data-testid=room-202]').exists()).toBe(true)
  })

  it('shows the flags of a room and filters by occupancy and flags', async () => {
    const w = mountBoard(['housekeeping.update'], [
      room({ priority: 'HIGH', dnd: true, make_up_requested: true, flag_note: 'allergic to feathers', occupancy: 'OCCUPIED' }),
      room({ room_id: 2, room_number: '202', priority: 'NORMAL', dnd: false, make_up_requested: false }),
    ])
    await flushPromises()
    const flags = w.get('[data-testid=room-201] [data-testid=flags]').text()
    expect(flags).toContain('High')
    expect(flags).toContain('DND')
    expect(flags).toContain('Make-up')
    expect(flags).toContain('allergic to feathers')
    expect(w.get('[data-testid=filter-flagged]').text()).toContain('1')
    await w.get('[data-testid=filter-flagged]').trigger('click')
    expect(w.find('[data-testid=room-202]').exists()).toBe(false)
    await w.get('[data-testid=filter-flagged]').trigger('click')
    await w.get('select[name=occupancy]').setValue('VACANT')
    expect(w.find('[data-testid=room-201]').exists()).toBe(false)
    expect(w.find('[data-testid=room-202]').exists()).toBe(true)
  })

  it('edits the flags of a room', async () => {
    const w = mountBoard(['housekeeping.update'], [room({ priority: 'NORMAL', dnd: false, make_up_requested: false })])
    await flushPromises()
    await w.get('[data-testid=flags-201]').trigger('click')
    await w.get('select[name=priority]').setValue('HIGH')
    await w.get('input[name=dnd]').setValue(true)
    await w.get('input[name=note]').setValue('VIP')
    await w.get('[data-testid=flag-form]').trigger('submit')
    await flushPromises()
    expect(PUT).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/rooms/{id}/housekeeping/flags', {
      params: { path: { propertyId: 7, id: 1 } }, body: { priority: 'HIGH', dnd: true, make_up_requested: false, note: 'VIP' },
    })
    expect(w.find('[data-testid=flag-form]').exists()).toBe(false)
  })

  it('offers no flag editing to read-only users', async () => {
    const w = mountBoard([], [room({})])
    await flushPromises()
    expect(w.find('[data-testid=flags-201]').exists()).toBe(false)
  })

  it('sorts by room and by status, and links to the maintenance report of a room', async () => {
    const w = mountBoard(['maintenance.report'], [room({ room_id: 3, room_number: '301', status: 'CLEAN', allowed_next: [] }), room({}), room({ room_id: 2, room_number: '202', status: 'INSPECTED', allowed_next: [] })])
    await flushPromises()
    const numbers = () => w.findAll('tbody tr').map((r) => r.findAll('td')[0]!.text().slice(0, 3))
    expect(numbers()).toEqual(['301', '201', '202'])
    await w.get('[data-testid=sort-room_number]').trigger('click')
    expect(numbers()).toEqual(['201', '202', '301'])
    expect(w.get('[data-testid=report-201]').attributes('href')).toBe('/maintenance?room=1')
  })

  it('names the empty board and an empty filter differently', async () => {
    const none = mountBoard([], [])
    await flushPromises()
    expect(none.get('[data-testid=empty]').text()).toContain('No active rooms yet')
    const some = mountBoard([], [room({})])
    await flushPromises()
    await some.get('[data-testid=filter-CLEAN]').trigger('click')
    expect(some.find('[data-testid=empty]').exists()).toBe(false)
    expect(some.text()).toContain('No rooms match the filter.')
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    const w = mountBoard(['housekeeping.update'], [room({ priority: 'HIGH', dnd: true, occupancy: 'OCCUPIED', block: { type: 'OOS', end_date: '2026-10-04' } })])
    await flushPromises()
    expect(w.get('[data-testid=room-201] [data-testid=status]').text()).toBe('Kotor')
    expect(w.get('[data-testid=act-CLEANING]').text()).toBe('Mulai bersihkan')
    expect(w.get('[data-testid=block]').text()).toBe('OOS sampai 2026-10-04')
    expect(w.get('[data-testid=flags]').text()).toContain('Tinggi')
    setLocale('en')
  })
})
