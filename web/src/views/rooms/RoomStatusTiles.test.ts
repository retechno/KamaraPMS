import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { statusSwatch } from '@/components/app/statusMap'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import RoomStatusView from './RoomStatusView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const room = (id: number, number: string, occupancy: string, status: string, extra: object = {}) => ({
  room_id: id, room_number: number, floor: '1', room_type_id: 10, room_type_code: 'DLX', room_type_name: 'Deluxe', status, status_updated_at: '2026-09-30T08:00:00Z', occupancy, allowed_next: [], priority: 'NORMAL', dnd: false, ...extra,
})

async function mountTiles() {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: ['reservation.read'] }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: { data: [
    room(1, '101', 'OCCUPIED', 'DIRTY'), // a guest is in it and it is dirty: normal, and not urgent
    room(2, '102', 'VACANT', 'DIRTY'), // nobody is in it and it is dirty: this is what has to be cleaned
    room(3, '103', 'RESERVED', 'INSPECTED'),
    room(4, '104', 'VACANT', 'CLEAN'),
    room(5, '105', 'VACANT', 'CLEANING'),
    room(6, '106', 'OCCUPIED', 'CLEAN'),
    room(7, '107', 'VACANT', 'DIRTY', { block: { type: 'OOO', end_date: '2026-10-03' } }),
    room(8, '108', 'VACANT', 'CLEAN', { block: { type: 'OOS', end_date: '2026-10-03' } }),
  ] } })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  const w = mount(RoomStatusView, { global: { plugins: [router] } })
  await flushPromises()
  return w
}

describe('the tiles of the room status page', () => {
  it('does not fill the tile of an occupied room with the colour of dirty: it is white, with a colour bar and a chip', async () => {
    const w = await mountTiles()
    const tile = w.get('[data-testid=tile-101]')
    expect(tile.attributes('data-tile')).toBe('neutral')
    expect(tile.classes()).toEqual(expect.arrayContaining(['bg-card', 'border-l-4', 'border-l-status-dirty']))
    expect(tile.classes()).not.toContain('bg-status-dirty-bg')
    const chip = tile.get('[data-slot=tile-cleaning]')
    expect(chip.attributes('data-status')).toBe('DIRTY')
    expect(chip.text()).toBe('Dirty')
    expect(chip.classes()).toContain('bg-status-dirty-bg') // the chip carries the colour, small
  })

  it('fills the tile of a vacant dirty room completely, so it cannot be missed', async () => {
    const w = await mountTiles()
    const tile = w.get('[data-testid=tile-102]')
    expect(tile.attributes('data-tile')).toBe('filled')
    expect(tile.classes()).toEqual(expect.arrayContaining(statusSwatch('housekeeping', 'DIRTY').fill.split(' ')))
    expect(tile.get('[data-slot=tile-cleaning]').text()).toBe('Dirty')
    expect(tile.get('[data-testid=occ-102]').text()).toBe('Vacant') // the occupancy is a word and an icon on a white chip
    expect(tile.get('[data-testid=occ-102]').find('svg').exists()).toBe(true)
  })

  it('fills a reserved or vacant room with the colour of its cleaning, and a blocked room with the stripe', async () => {
    const w = await mountTiles()
    expect(w.get('[data-testid=tile-103]').classes()).toContain('bg-status-inspected') // solid: ready to sell
    expect(w.get('[data-testid=tile-104]').classes()).toContain('bg-status-clean-bg')
    expect(w.get('[data-testid=tile-105]').classes()).toContain('bg-status-cleaning-bg')
    expect(w.get('[data-testid=tile-107]').classes()).toContain('status-hatch') // out of order, whatever its cleaning was
    expect(w.get('[data-testid=tile-107]').text()).toContain('Out of order')
    expect(w.get('[data-testid=tile-108]').classes()).toContain('bg-status-closed-bg') // out of service: the plain grey
  })

  it('gives an occupied clean room the white tile as well, with the bar of its colour', async () => {
    const w = await mountTiles()
    const tile = w.get('[data-testid=tile-106]')
    expect(tile.classes()).toEqual(expect.arrayContaining(['bg-card', 'border-l-status-clean']))
  })

  it('uses no teal anywhere on a tile: teal is for actions', async () => {
    const w = await mountTiles()
    for (const tile of w.findAll('[data-testid^=tile-]')) {
      expect(tile.classes().filter((c) => /(^|-)primary($|\/)/.test(c)), tile.attributes('data-testid')).toEqual([])
    }
  })

  it('draws the legend from the same map as the tiles', async () => {
    const w = await mountTiles()
    const legend = w.get('[data-testid=legend]')
    const entries = legend.findAll('[data-legend]').map((e) => e.attributes('data-legend'))
    expect(entries).toEqual(['DIRTY', 'CLEANING', 'CLEAN', 'INSPECTED', 'BLOCKED', 'OCCUPIED'])
    for (const status of ['DIRTY', 'CLEANING', 'CLEAN', 'INSPECTED', 'BLOCKED']) {
      const swatch = legend.get(`[data-legend=${status}] span`)
      expect(swatch.classes()).toEqual(expect.arrayContaining(statusSwatch('housekeeping', status).swatch.split(' ')))
    }
    expect(legend.text()).toContain('Blocked')
    expect(legend.get('[data-legend=OCCUPIED]').text()).toContain('Occupied')
  })
})
