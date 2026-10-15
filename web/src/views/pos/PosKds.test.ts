import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import PosKdsView from './PosKdsView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))
type Opts = { channels: () => string[]; onEvent: (e: { id: number; channel: string; type: string; entity_id: number; at: string }) => void; onReload?: () => void }
let stream: Opts | null = null
vi.mock('@/composables/usePosEvents', async (orig) => ({
  ...(await orig<typeof import('@/composables/usePosEvents')>()),
  usePosEvents: (o: Opts) => {
    stream = o
    return { state: { value: 'live' }, stop: () => undefined }
  },
}))

const line = (id: number, name: string, quantity = '1', over: object = {}) => ({ id, quantity, name, options: '', note: '', is_void: false, ...over })
const ticket = (id: number, status: string, over: object = {}) => ({
  id, kind: 'NEW', status, round_no: 1, is_addition: false, bill_id: 10, bill_number: 'R-0123', outlet_id: 1, outlet_name: 'Restoran Teratai', order_type: 'DINE_IN', table_code: '5', waiter: 'Ketut',
  created_at: '2026-10-15T05:00:00Z', started_at: null, ready_at: null, served_at: null, wait_seconds: 120, is_late: false, lines: [line(id * 10, 'Es Teh Manis')], ...over,
})
const board = (over: object = {}) => ({
  station: { id: 1, code: 'KITCHEN', name: 'Dapur', late_after_minutes: 15, kds_enabled: true, is_active: true, printer_id: null, printer_name: null },
  outlets: [{ id: 1, code: 'R', name: 'Restoran Teratai' }, { id: 2, code: 'RS', name: 'Room service' }],
  now: '2026-10-15T05:30:00Z', average_wait_seconds: 660, late_count: 1,
  tickets: [
    ticket(1, 'NEW', { is_addition: true, round_no: 2, wait_seconds: 60, lines: [line(11, 'Es Teh Manis', '1', { note: 'Kurang manis' })] }),
    ticket(2, 'NEW', { table_code: '', room_number: '214', bill_number: 'RS-0045', outlet_name: 'Room service', order_type: 'ROOM_SERVICE', wait_seconds: 180, lines: [line(21, 'Soto Ayam'), line(22, 'Gado-gado', '1', { options: 'Bumbu dipisah' })] }),
    ticket(3, 'COOKING', { wait_seconds: 960, lines: [line(31, 'Nasi Goreng Kampung', '2', { options: 'Reguler · Sedang', note: 'Tanpa bawang goreng' }), line(32, 'Bebek Betutu', '1', { is_void: true })] }),
    ticket(4, 'COOKING', { table_code: '7', bill_number: 'R-0117', waiter: 'Wayan', wait_seconds: 1620, lines: [line(41, 'Ikan Bakar Jimbaran', '2')] }),
    ticket(5, 'READY', { wait_seconds: 120, ready_at: '2026-10-15T05:28:00Z', lines: [line(51, 'Sate Ayam Madura')] }),
  ],
  history: [ticket(6, 'SERVED', { served_at: '2026-10-15T05:10:00Z', lines: [line(61, 'Soto Ayam', '3')] })],
  ...over,
})

const mounted: VueWrapper[] = []
function setup(b: object = board(), only?: object[]) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'k@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: ['pos.kds'] }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/pos/stations')) return { data: { data: only ?? [
      { id: 1, code: 'KITCHEN', name: 'Dapur', kds_enabled: true, is_active: true, late_after_minutes: 15, printer_id: null, printer_name: null },
      { id: 2, code: 'BAR', name: 'Bar', kds_enabled: true, is_active: true, late_after_minutes: 5, printer_id: null, printer_name: null },
      { id: 3, code: 'OFF', name: 'Printer only', kds_enabled: false, is_active: true, late_after_minutes: 15, printer_id: 4, printer_name: 'P' },
    ] } }
    if (path.endsWith('/board')) return { data: b }
    return { data: null }
  })
  POST = vi.fn(async () => ({ data: { id: 1, station_id: 1, status: 'COOKING', changed: true } }))
  const w = mount(PosKdsView, { global: { plugins: [pinia] }, attachTo: document.body })
  mounted.push(w)
  return w
}

beforeEach(async () => {
  localStorage.clear()
  stream = null
  await setLocale('id')
})
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
})

describe('the kitchen display', () => {
  it('offers the stations that have a display, and shows the outlets the chosen one serves', async () => {
    const w = setup()
    await flushPromises()
    expect(w.findAll('[data-testid^=station-]').map((b) => b.text())).toEqual(['Dapur', 'Bar'])
    expect(w.get('[data-testid=kds-outlets]').text()).toBe('Restoran Teratai, Room service')
    expect(w.get('[data-testid=kds-summary]').text()).toBe('Rata-rata 11 mnt · 2 terlambat')
    expect(stream?.channels()).toEqual(['station:1'])
  })

  it('draws the tickets in the three columns, with the table or the room, the bill and the waiter', async () => {
    const w = setup()
    await flushPromises()
    expect(w.findAll('[data-testid=col-NEW] [data-testid^=ticket-]')).toHaveLength(2)
    expect(w.findAll('[data-testid=col-COOKING] [data-testid^=ticket-]')).toHaveLength(2)
    expect(w.findAll('[data-testid=col-READY] [data-testid^=ticket-]')).toHaveLength(1)
    expect(w.get('[data-testid=col-NEW]').text()).toContain('Baru')
    expect(w.get('[data-testid=col-READY]').text()).toContain('Siap diambil')
    const t1 = w.get('[data-testid=ticket-1]').text()
    expect(t1).toContain('Meja 5')
    expect(t1).toContain('R-0123 · Ketut')
    expect(t1).toContain('Kurang manis')
    expect(w.get('[data-testid=ticket-1] [data-testid=addition]').text()).toContain('tambahan')
    expect(w.get('[data-testid=ticket-2]').text()).toContain('Kamar 214')
    expect(w.get('[data-testid=ticket-2]').text()).toContain('Bumbu dipisah')
  })

  it('marks a ticket that waited longer than the station allows, and a ready one by how long ago', async () => {
    const w = setup()
    await flushPromises()
    expect(w.get('[data-testid=ticket-4]').attributes('data-late')).toBe('true')
    expect(w.get('[data-testid=ticket-4] [data-testid=late]').text()).toBe('Terlambat')
    expect(w.get('[data-testid=ticket-4] [data-testid=age]').text()).toBe('27 mnt')
    expect(w.get('[data-testid=ticket-3]').attributes('data-late')).toBe('true') // 16 minutes, the station says 15
    expect(w.get('[data-testid=ticket-1]').attributes('data-late')).toBe('false')
    expect(w.get('[data-testid=ticket-5] [data-testid=age]').text()).toBe('siap 2 mnt lalu')
    expect(w.find('[data-testid=ticket-5] [data-testid=late]').exists()).toBe(false)
  })

  it('shows a voided line struck through and says it is cancelled', async () => {
    const w = setup()
    await flushPromises()
    const v = w.get('[data-testid=tline-32]')
    expect(v.attributes('data-void')).toBe('true')
    expect(v.text()).toContain('DIBATALKAN')
    expect(v.html()).toContain('line-through')
    expect(w.get('[data-testid=tline-31]').attributes('data-void')).toBe('false')
  })

  it('moves a ticket on with its button, and reads the board again', async () => {
    const w = setup()
    await flushPromises()
    await w.get('[data-testid=next-1]').trigger('click')
    await flushPromises()
    const [path, init] = POST.mock.calls[0]!
    expect(path).toBe('/api/v1/properties/{propertyId}/pos/tickets/{id}/status')
    expect(init.params.path.id).toBe(1)
    expect(init.body).toEqual({ action: 'START' })
    expect(GET.mock.calls.filter((c) => String(c[0]).endsWith('/board'))).toHaveLength(2)
    await w.get('[data-testid=next-3]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[1]![1].body).toEqual({ action: 'READY' })
    await w.get('[data-testid=next-5]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[2]![1].body).toEqual({ action: 'SERVE' })
    // a new ticket may be made ready without cooking it first
    await w.get('[data-testid=ready-2]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[3]![1].body).toEqual({ action: 'READY' })
  })

  it('does not scold when another screen got there first: it just shows what is true now', async () => {
    const w = setup()
    await flushPromises()
    POST = vi.fn(async () => {
      throw new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'TICKET_STATUS_INVALID', detail: 'moved on' })
    })
    await w.get('[data-testid=next-1]').trigger('click')
    await flushPromises()
    expect(w.text()).not.toContain('moved on')
    expect(GET.mock.calls.filter((c) => String(c[0]).endsWith('/board'))).toHaveLength(2)
  })

  it('reads the board again when the stream says a ticket changed', async () => {
    vi.useFakeTimers()
    try {
      setup()
      await flushPromises()
      const n = () => GET.mock.calls.filter((c) => String(c[0]).endsWith('/board')).length
      expect(n()).toBe(1)
      stream?.onEvent({ id: 1, channel: 'station:1', type: 'ticket.created', entity_id: 9, at: '' })
      stream?.onEvent({ id: 2, channel: 'station:1', type: 'ticket.updated', entity_id: 9, at: '' })
      await vi.advanceTimersByTimeAsync(300)
      await flushPromises()
      expect(n()).toBe(2) // two events, one read
    } finally {
      vi.useRealTimers()
    }
  })

  it('lists the tickets of the last hour on request', async () => {
    const w = setup()
    await flushPromises()
    expect(w.find('[data-testid=kds-history]').exists()).toBe(false)
    expect(w.get('[data-testid=kds-history-toggle]').text()).toBe('Selesai 1 jam terakhir: 1 tiket · Riwayat')
    await w.get('[data-testid=kds-history-toggle]').trigger('click')
    expect(w.get('[data-testid=history-6]').text()).toContain('3 × Soto Ayam')
  })

  it('says so when no station has a display', async () => {
    const w = setup(board(), [{ id: 3, code: 'OFF', name: 'Printer only', kds_enabled: false, is_active: true, late_after_minutes: 15, printer_id: 4, printer_name: 'P' }])
    await flushPromises()
    expect(w.find('[data-testid=kds]').exists()).toBe(false)
    expect(w.get('[data-testid=kds-no-station]').text()).toContain('Belum ada stasiun dengan layar dapur')
  })
})
