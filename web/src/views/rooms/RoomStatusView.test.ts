import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import RoomStatusView from './RoomStatusView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const room = (id: number, number: string, floor: string, occupancy: string, extra: object = {}) => ({
  room_id: id, room_number: number, floor, room_type_id: 10, room_type_code: 'DLX', room_type_name: 'Deluxe', status: 'CLEAN', occupancy, allowed_next: [], ...extra,
})

function mountView(permissions = ['reservation.read']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: { data: [
    room(1, '101', '1', 'OCCUPIED'), room(2, '102', '1', 'VACANT'), room(3, '201', '2', 'RESERVED'), room(4, '202', '2', 'VACANT', { block: { type: 'OOO', end_date: '2026-10-03' } }),
  ] } })
  return mount(RoomStatusView)
}

describe('RoomStatusView', () => {
  beforeEach(() => {
    GET = vi.fn()
  })

  it('shows the derived occupancy of every room by floor, with counts and blocks', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=occ-101]').text()).toBe('OCCUPIED')
    expect(w.get('[data-testid=occ-201]').text()).toBe('RESERVED')
    expect(w.get('[data-testid=tile-202]').text()).toContain('OOO')
    expect(w.get('[data-testid=counts]').text()).toBe('1 occupied · 1 reserved · 2 vacant · 1 blocked')
    expect(w.findAll('section')).toHaveLength(2)
  })

  it('refreshes and needs a permission', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=refresh]').trigger('click')
    await flushPromises()
    expect(GET).toHaveBeenCalledTimes(2)
    const denied = mountView(['guest.read'])
    await flushPromises()
    expect(denied.find('[data-testid=no-access]').exists()).toBe(true)
  })
})
