import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import TapeChartView from './TapeChartView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const booking = { reservation_id: 9, confirmation_number: 'RES000009', reservation_room_id: 4, room_type_id: 10, stay_id: null, open_segment: false, status: 'CONFIRMED', guest_name: 'Siti Nurhaliza', arrival_date: '2026-10-02', departure_date: '2026-10-04' }
const chart = {
  from: '2026-10-01', to: '2026-10-15',
  rooms: [
    { room_id: 1, room_number: '101', room_type_id: 10, room_type_code: 'DLX', bookings: [booking], blocks: [] },
    { room_id: 2, room_number: '102', room_type_id: 10, room_type_code: 'DLX', bookings: [], blocks: [{ id: 5, block_type: 'OOS', start_date: '2026-10-05', end_date: '2026-10-07' }] },
  ],
  unassigned: [{ room_type_id: 10, room_type_code: 'DLX', bookings: [{ ...booking, reservation_id: 10, reservation_room_id: 5, arrival_date: '2026-10-03', departure_date: '2026-10-04' }] }],
}

function mountView(permissions = ['reservation.read'], data: unknown = chart) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-10-01' } as never
  GET = vi.fn().mockResolvedValue({ data })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(TapeChartView, { global: { plugins: [pinia, router] } })
}

describe('TapeChartView', () => {
  beforeEach(() => {
    GET = vi.fn()
  })

  it('loads 14 days from the business date and draws bookings, blocks and unassigned counts', async () => {
    const w = mountView()
    await flushPromises()
    expect(GET.mock.calls[0]?.[1]).toMatchObject({ params: { path: { propertyId: 7 }, query: { from: '2026-10-01', to: '2026-10-15' } } })
    // one element per booking, placed from its arrival night to the night before departure: column 2 is the first night of the window (column 1 is the room), so 2 Oct starts at column 3 and spans 2
    expect(w.find('[data-testid=bar-101-2026-10-01]').exists()).toBe(false)
    const bar = w.get('[data-testid=bar-101-2026-10-02]')
    expect(bar.element.tagName).toBe('BUTTON')
    expect(bar.attributes('style')).toContain('grid-column: 3 / span 2')
    expect(w.findAll('[data-room-id="1"] [data-booking]')).toHaveLength(1)
    expect(w.find('[data-testid=bar-101-2026-10-03]').exists()).toBe(false)
    expect(bar.text()).toContain('Siti Nurhaliza')
    expect(bar.text()).toContain('2 nights')
    expect(bar.classes()).toContain('text-[13px]') // no text under 13 px on the bar
    expect(w.get('[data-testid=block-102-2026-10-05]').text()).toBe('OOS')
    expect(w.get('[data-testid=block-102-2026-10-05]').attributes('style')).toContain('grid-column: 6 / span 2')
    expect(w.find('[data-testid=block-102-2026-10-07]').exists()).toBe(false)
    // the bookings with no room are folded under their type, with a count per night
    expect(w.get('[data-testid=unassigned-DLX]').text()).toContain('1 booking(s)')
    expect(w.find('[data-testid=bar-unassigned-5]').exists()).toBe(false)
    expect(w.get('[data-testid=unassigned-toggle-DLX]').attributes('aria-expanded')).toBe('false')
    const counts = w.findAll('[data-testid=unassigned-counts-DLX] [data-count]').map((c) => c.attributes('data-count'))
    expect(counts).toHaveLength(14)
    expect(counts.map(Number)).toEqual([0, 0, 1, ...Array(11).fill(0)]) // the night of 3 Oct is the third
    // opened, a booking is a bar of its own on a lane
    await w.get('[data-testid=unassigned-toggle-DLX]').trigger('click')
    expect(w.get('[data-testid=unassigned-toggle-DLX]').attributes('aria-expanded')).toBe('true')
    expect(w.find('[data-testid=unassigned-counts-DLX]').exists()).toBe(false)
    expect(w.get('[data-testid=bar-unassigned-5]').attributes('style')).toContain('grid-column: 4 / span 1')
    expect(w.get('[data-testid=bar-unassigned-5]').attributes('title')).toContain('RES000009')
    expect(w.get('[data-testid=unassigned-lane-DLX-1]').find('[data-testid=bar-unassigned-5]').exists()).toBe(true)
  })

  it('shows what is sold and what is left of each room type on each night', async () => {
    const w = mountView()
    await flushPromises()
    const cells = w.findAll('[data-testid=type-head-DLX] [data-sold]')
    expect(cells).toHaveLength(14)
    // 2 rooms: 101 is booked on 2 and 3 Oct, a booking with no room holds 3 Oct, 102 is blocked from 5 to 6 Oct
    const sold = cells.map((c) => `${c.attributes('data-sold')}/${c.attributes('data-available')}`)
    expect(sold.slice(0, 7)).toEqual(['0/2', '1/1', '2/0', '0/2', '0/1', '0/1', '0/2'])
    expect(cells[1]!.text()).toBe('1/1')
    expect(cells[1]!.attributes('title')).toBe('Sold 1, available 1, blocked 0')
    expect(w.get('[data-testid=type-head-DLX]').text()).toContain('sold / available')
    expect(cells[2]!.classes()).toContain('font-semibold') // nothing left
  })
  it('marks a room type that is oversold', async () => {
    const extra = { ...booking, reservation_id: 11, reservation_room_id: 6, arrival_date: '2026-10-03', departure_date: '2026-10-04' }
    const w = mountView(['reservation.read'], { ...chart, unassigned: [{ room_type_id: 10, room_type_code: 'DLX', bookings: [{ ...extra, reservation_room_id: 5 }, extra] }] })
    await flushPromises()
    const cell = w.findAll('[data-testid=type-head-DLX] [data-sold]')[2]!
    expect(cell.attributes('data-available')).toBe('-1')
    expect(cell.classes()).toContain('text-destructive')
  })

  it('lists the unassigned sections in the order of the room types above them', async () => {
    const other = { ...booking, room_type_id: 20, reservation_id: 31, reservation_room_id: 31 }
    const data = { ...chart, rooms: [...chart.rooms, { room_id: 9, room_number: '201', room_type_id: 20, room_type_code: 'STD', bookings: [], blocks: [] }], unassigned: [{ room_type_id: 20, room_type_code: 'STD', bookings: [other] }, chart.unassigned[0]] }
    const w = mountView(['reservation.read'], data)
    await flushPromises()
    // DLX has the rooms 101 and 102 above STD (201), whatever order the server sent
    expect(w.findAll('[data-testid^=unassigned-toggle-]').map((b) => b.attributes('data-testid'))).toEqual(['unassigned-toggle-DLX', 'unassigned-toggle-STD'])
  })
  it('packs the bookings with no room into as few lanes as the nights they share', async () => {
    const u = (id: number, from: string, to: string) => ({ ...booking, reservation_id: 100 + id, confirmation_number: `RES0001${id}`, reservation_room_id: 100 + id, arrival_date: from, departure_date: to })
    // 5 bookings: at most two share a night (3-5 and 4-6 on the 4th), the others follow on a lane that is free
    const w = mountView(['reservation.read'], { ...chart, unassigned: [{ room_type_id: 10, room_type_code: 'DLX', bookings: [u(1, '2026-10-03', '2026-10-05'), u(2, '2026-10-04', '2026-10-06'), u(3, '2026-10-05', '2026-10-07'), u(4, '2026-10-06', '2026-10-08'), u(5, '2026-10-08', '2026-10-09')] }] })
    await flushPromises()
    await w.get('[data-testid=unassigned-toggle-DLX]').trigger('click')
    expect(w.findAll('[data-testid^=unassigned-lane-DLX-]')).toHaveLength(2)
    expect(w.findAll('[data-testid^=bar-unassigned-]')).toHaveLength(5)
    // the same booking is never on two lanes and a lane never holds two bars on one night
    const bars = w.findAll('[data-testid^=unassigned-lane-DLX-]').map((lane) => lane.findAll('[data-booking]').map((b) => /grid-column: (\d+) \/ span (\d+)/.exec(b.attributes('style') ?? '')!.slice(1).map(Number) as [number, number]))
    for (const lane of bars) {
      const sorted = [...lane].sort((a, b) => a[0] - b[0])
      sorted.slice(1).forEach(([start], i) => expect(start).toBeGreaterThanOrEqual(sorted[i]![0] + sorted[i]![1]))
    }
  })

  it('moves the window by a week', async () => {
    const w = mountView()
    await flushPromises()
    // the two buttons of the week have their arrows as icons, one to each side: ‹ Week and Week ›
    const [earlier, later] = w.findAll('button').filter((b) => b.text() === 'Week')
    expect(earlier?.find('svg.lucide-chevron-left').exists()).toBe(true)
    expect(later?.find('svg.lucide-chevron-right').exists()).toBe(true)
    expect(w.text()).not.toContain('→')
    await later?.trigger('click')
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1]).toMatchObject({ params: { query: { from: '2026-10-08', to: '2026-10-22' } } })
  })

  it('needs read permission', async () => {
    const w = mountView(['guest.read'])
    await flushPromises()
    expect(w.find('[data-testid=no-access]').exists()).toBe(true)
    expect(w.text()).not.toContain('Reservations') // the toolbar (weeks, business date, days, link) is not drawn for who may not read
    expect(w.find('select').exists()).toBe(false)
    expect(GET).not.toHaveBeenCalled()
  })

  it('colours a checked-in stay differently, and shows a legend', async () => {
    const w = mountView()
    GET.mockResolvedValue({ data: { ...chart, rooms: [{ ...chart.rooms[0]!, bookings: [{ ...booking, status: 'CHECKED_IN' }] }, chart.rooms[1]] } })
    await w.findAll('button').find((b) => b.text().includes('Business date'))?.trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=bar-101-2026-10-02]').classes()).toContain('bg-status-inhouse') // a guest in the house: solid blue
    expect(w.get('[data-testid=legend]').text()).toContain('Checked in')
    expect(w.get('[data-testid=legend]').text()).toContain('Blocked (OOO / OOS)')
  })

  it('draws a booking to come lighter than a stay in the house, not only in another hue, and a block as the stripe', async () => {
    const w = mountView()
    await flushPromises()
    const confirmed = w.get('[data-testid=bar-101-2026-10-02]').classes()
    expect(confirmed).toEqual(expect.arrayContaining(['bg-status-booked-line', 'text-foreground', 'border-y'])) // more body than the pale tile of the badge
    expect(confirmed).not.toContain('bg-status-booked-bg')
    expect(confirmed).not.toContain('bg-status-inhouse')
    expect(confirmed.some((c) => /(^|-)primary($|\/)|success/.test(c))).toBe(false) // no teal and no green: teal is for actions
    const block = w.get('[data-testid=block-102-2026-10-05]') // out of service here: the plain grey; out of order is the stripe (see the legend test)
    expect(block.classes()).toEqual(expect.arrayContaining(['bg-status-closed-bg', 'border-y']))
  })

  it('draws the legend from the same map as the bars', async () => {
    const w = mountView()
    await flushPromises()
    const legend = w.get('[data-testid=legend]')
    expect(legend.get('[data-legend=CONFIRMED] span').classes()).toEqual(expect.arrayContaining(['bg-status-booked-line']))
    expect(legend.get('[data-legend=CHECKED_IN] span').classes()).toEqual(expect.arrayContaining(['bg-status-inhouse']))
    expect(legend.get('[data-legend=BLOCKED] span').classes()).toContain('status-hatch')
  })

  it('marks the booking that arrives on the business date and the stay that leaves on it', async () => {
    const arriving = { ...booking, reservation_id: 21, reservation_room_id: 21, confirmation_number: 'RES000021', arrival_date: '2026-10-01', departure_date: '2026-10-03' }
    const leaving = { ...booking, reservation_id: 22, reservation_room_id: 22, confirmation_number: 'RES000022', status: 'CHECKED_IN', stay_id: 3, open_segment: true, arrival_date: '2026-09-28', departure_date: '2026-10-01' }
    const moved = { ...leaving, reservation_id: 23, reservation_room_id: 23, open_segment: false } // the end of a segment is a move of room, not a departure
    const data = { ...chart, rooms: [{ ...chart.rooms[0]!, bookings: [booking] }, { ...chart.rooms[1]!, bookings: [arriving] }, { room_id: 3, room_number: '103', room_type_id: 10, room_type_code: 'DLX', bookings: [leaving, moved], blocks: [] }], unassigned: [{ room_type_id: 10, room_type_code: 'DLX', bookings: [{ ...arriving, reservation_id: 24, reservation_room_id: 24 }] }] }
    const w = mountView(['reservation.read'], data)
    await flushPromises()
    const arrive = w.get('[data-testid=bar-102-2026-10-01]')
    expect(arrive.attributes('data-arrives-today')).toBe('true')
    expect(arrive.classes()).toEqual(expect.arrayContaining(['border-l-4', 'border-l-foreground']))
    expect(w.get('[data-testid=bar-101-2026-10-02]').attributes('data-arrives-today')).toBeUndefined() // tomorrow
    await w.get('[data-testid=unassigned-toggle-DLX]').trigger('click')
    expect(w.get('[data-testid=bar-unassigned-24]').attributes('data-arrives-today')).toBe('true')
    // a stay that leaves today only shows on a window that starts before it
    await w.findAll('button').find((x) => x.text() === 'Week')!.trigger('click')
    await flushPromises()
    const leave = w.get('[data-testid=bar-103-2026-09-28]')
    expect(leave.attributes('data-departs-today')).toBe('true')
    expect(leave.classes()).toEqual(expect.arrayContaining(['border-r-4', 'border-r-foreground']))
    expect(leave.attributes('data-arrives-today')).toBeUndefined() // a stay is not an arrival
    expect(w.find('[data-departs-today]').exists()).toBe(true)
    expect(w.findAll('[data-departs-today]')).toHaveLength(1) // the closed segment ends on the same day but is not a departure
    expect(w.get('[data-testid=legend]').text()).toContain('Arrives on the business date')
    expect(w.get('[data-testid=legend]').text()).toContain('Leaves on the business date')
  })

  it('names the weekday over each date and shades the weekend', async () => {
    const w = mountView()
    await flushPromises()
    const heads = w.findAll('[data-day-head]')
    expect(heads[0]!.text()).toContain('Thu') // 1 Oct 2026
    expect(heads[0]!.text()).toContain('01/10')
    expect(heads[0]!.classes()).toContain('bg-primary/10') // the business date
    expect(heads[2]!.text()).toContain('Sat')
    expect(heads[2]!.classes()).toContain('bg-muted/60')
    expect(heads[4]!.classes()).not.toContain('bg-muted/60') // Monday
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    const w = mountView()
    await flushPromises()
    expect(w.get('h1').text()).toBe('Bagan kamar')
    expect(w.get('[data-testid=unassigned-DLX]').text()).toContain('1 pemesanan')
    expect(w.get('[data-testid=tape]').text()).toContain('Belum ditetapkan')
    expect(w.get('select').findAll('option').map((o) => o.text())).toEqual(['14 hari', '28 hari'])
    setLocale('en')
  })
})
