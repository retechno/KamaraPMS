import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '@/i18n'
import { createMemoryHistory, createRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import RoomStatusView from './RoomStatusView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const room = (id: number, number: string, floor: string, occupancy: string, extra: object = {}) => ({
  room_id: id, room_number: number, floor, room_type_id: 10, room_type_code: 'DLX', room_type_name: 'Deluxe', status: 'CLEAN', status_updated_at: '2026-09-30T08:00:00Z', occupancy, allowed_next: [], priority: 'NORMAL', dnd: false, make_up_requested: false, ...extra,
})

function mountView(permissions = ['reservation.read'], attach = false) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: { data: [
    room(1, '101', '1', 'OCCUPIED'), room(2, '102', '1', 'VACANT'), room(3, '201', '2', 'RESERVED'), room(4, '202', '2', 'VACANT', { block: { type: 'OOO', end_date: '2026-10-03' } }),
  ] } })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(RoomStatusView, { ...(attach ? { attachTo: document.body } : {}), global: { plugins: [router] } })
}

describe('RoomStatusView', () => {
  beforeEach(() => {
    GET = vi.fn()
  })

  it('shows the derived occupancy of every room by floor, with counts and blocks', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=occ-101]').text()).toBe('Occupied')
    expect(w.get('[data-testid=occ-201]').text()).toBe('Reserved')
    expect(w.get('[data-testid=tile-202]').text()).toContain('Out of order')
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

  const tiles = (w: ReturnType<typeof mountView>) => w.findAll('[data-testid^=tile-]').map((t) => t.attributes('data-testid')!.slice(5))

  it('filters the board by occupancy, housekeeping, room type and room number', async () => {
    const w = mountView()
    await flushPromises()
    expect(tiles(w)).toEqual(['101', '102', '201', '202'])
    await w.get('[data-testid=filter-occ-VACANT]').trigger('click')
    expect(tiles(w)).toEqual(['102', '202'])
    expect(w.get('[data-testid=filter-occ-VACANT]').attributes('aria-pressed')).toBe('true')
    await w.get('[data-testid=filter-occ-BLOCKED]').trigger('click')
    expect(tiles(w)).toEqual(['202'])
    await w.get('[data-testid=filter-occ-ALL]').trigger('click')
    await w.get('input[name=room_search]').setValue('20')
    expect(tiles(w)).toEqual(['201', '202'])
    await w.get('[data-testid=filter-hk-DIRTY]').trigger('click')
    expect(w.find('[data-testid=no-match]').exists()).toBe(true) // every room is CLEAN
    await w.get('[data-testid=filter-hk-ALL]').trigger('click')
    await w.get('select[name=room_type]').setValue('10')
    expect(tiles(w)).toEqual(['201', '202'])
    // the counts stay those of the whole property
    expect(w.get('[data-testid=counts]').text()).toBe('1 occupied · 1 reserved · 2 vacant · 1 blocked')
  })

  it('hides a floor whose rooms are all filtered out', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=filter-occ-OCCUPIED]').trigger('click')
    expect(w.findAll('section')).toHaveLength(1)
  })

  it('shows the signs a room carries: do not disturb, make-up, high priority', async () => {
    const w = mountView()
    await flushPromises()
    GET.mockResolvedValue({ data: { data: [room(1, '101', '1', 'OCCUPIED', { dnd: true, make_up_requested: true, priority: 'HIGH' })] } })
    await w.get('[data-testid=refresh]').trigger('click')
    await flushPromises()
    const tile = w.get('[data-testid=tile-101]')
    expect(tile.find('[aria-label="Do not disturb"]').exists()).toBe(true)
    expect(tile.find('[aria-label="Make-up requested"]').exists()).toBe(true)
    expect(tile.find('[aria-label="High priority"]').exists()).toBe(true)
  })

  it('opens the details of a room in a sheet', async () => {
    const w = mountView(['reservation.read'], true)
    await flushPromises()
    GET.mockResolvedValue({ data: { data: [room(4, '202', '2', 'VACANT', { building: 'North', status: 'DIRTY', allowed_next: ['CLEANING'], block: { type: 'OOO', end_date: '2026-10-03' }, flag_note: 'Leak in the bathroom' })] } })
    await w.get('[data-testid=refresh]').trigger('click')
    await flushPromises()
    await w.get('[data-testid=tile-202]').trigger('click')
    await flushPromises()
    const sheet = document.body.querySelector('[data-testid=room-sheet]')!
    expect(sheet.textContent).toContain('Room 202')
    expect(sheet.textContent).toContain('Deluxe')
    expect(sheet.textContent).toContain('North')
    expect(sheet.textContent).toContain('Being cleaned')
    expect(sheet.textContent).toContain('Leak in the bathroom')
    expect(sheet.querySelector('[data-testid=sheet-block]')!.textContent).toContain('3 Oct 2026')
    w.unmount()
    document.body.innerHTML = ''
  })

  it('shows a loading board before the rooms arrive, and has no sheet open by default', async () => {
    const w = mountView()
    expect(w.find('[data-testid=loading]').exists()).toBe(true)
    await flushPromises()
    expect(w.find('[data-testid=loading]').exists()).toBe(false)
    expect(document.body.querySelector('[data-testid=room-sheet]')).toBeNull()
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=occ-101]').text()).toBe('Terisi')
    expect(w.get('[data-testid=counts]').text()).toBe('1 terisi · 1 dipesan · 2 kosong · 1 diblokir')
    setLocale('en')
  })
})
