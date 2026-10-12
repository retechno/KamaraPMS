import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import type { AssignLine } from '@/utils/roomAssign'
import AutoAssignDialog from './AutoAssignDialog.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const KING = 7
const room = (id: number, status: string, bed: number | null = null, name = '') => ({ room_id: id, room_number: String(100 + id), housekeeping_status: status, bed_type_id: bed, bed_type_name: name })
const line = (n: number, over: Partial<AssignLine> = {}): AssignLine => ({
  key: `${n}:${n * 10}`, reservationId: n, lineId: n * 10, version: n, confirmation: `RES00000${n}`, guest: `Guest ${n}`, typeId: 1, typeCode: 'DLX', arrival: '2026-10-10', departure: '2026-10-12',
  requestedBedTypeId: null, bedLocked: false, ...over,
})
const problem = (status: number, code: string) => new ApiError({ type: 't', title: 'x', status, code, detail: 'x' })

let wrapper: VueWrapper | null = null
const body = (sel: string) => document.body.querySelector(sel)
const bodyAll = (sel: string) => [...document.body.querySelectorAll(sel)]
const text = (sel: string) => body(sel)?.textContent?.replace(/\s+/g, ' ').trim() ?? ''
const rooms = (): string[] => bodyAll('[data-testid^=proposal-]').filter((e) => /^proposal-\d+:\d+$/.test(e.getAttribute('data-testid') ?? '')).map((e) => e.querySelector('[data-testid=proposed-room]')?.textContent ?? '–')

async function mountDialog(lines: AssignLine[], free: Record<string, unknown[]> = { default: [room(1, 'CLEAN'), room(2, 'INSPECTED'), room(3, 'DIRTY'), room(4, 'CLEANING')] }) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const property = usePropertyStore()
  property.currentId = 7
  property.current = { id: 7, require_room_inspection_for_checkin: false } as never
  property.clock = { business_date: '2026-10-10' } as never
  GET = vi.fn(async (_path: string, init: { params: { query: { room_type_id: number } } }) => ({ data: { data: free[String(init.params.query.room_type_id)] ?? free.default } }))
  wrapper = mount(AutoAssignDialog, { attachTo: document.body, global: { plugins: [pinia] }, props: { lines, open: true, 'onUpdate:open': (v: boolean) => void wrapper?.setProps({ open: v }) } })
  await flushPromises()
  return wrapper
}

describe('AutoAssignDialog', () => {
  beforeEach(() => {
    setLocale('en')
    POST = vi.fn(async (_path: string, init: { body: { room_id: number }; params: { path: { id: number } } }) => ({ data: { id: init.params.path.id, version: 100 + init.body.room_id, rooms: [] } }))
  })
  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
    document.body.innerHTML = ''
  })

  it('reads the free rooms once for lines that ask the same thing, and shows a proposal for each, ready rooms only to begin with', async () => {
    await mountDialog([line(1), line(2), line(3), line(4)])
    expect(GET).toHaveBeenCalledTimes(1)
    expect(GET).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/availability/rooms', { params: { path: { propertyId: 7 }, query: { room_type_id: 1, arrival: '2026-10-10', departure: '2026-10-12' } } })
    // two ready rooms (101, 102): the third and the fourth line have none that fits; the rooms that are not ready are not proposed
    expect(rooms()).toEqual(['101', '102', '–', '–'])
    expect(bodyAll('[data-testid=proposal-none]')).toHaveLength(2)
    expect(text('[data-testid=auto-confirm]')).toBe('Assign 2 rooms')
  })

  it('asks once for each question when the lines are of different types or nights', async () => {
    await mountDialog([line(1), line(2, { typeId: 2, typeCode: 'STD' }), line(3, { departure: '2026-10-13' })])
    expect(GET).toHaveBeenCalledTimes(3)
  })

  it('adds the rooms that are not ready when asked, after the ready ones, each with its status', async () => {
    await mountDialog([line(1), line(2), line(3), line(4)])
    const box = body('[data-testid=include-not-ready]') as HTMLInputElement
    box.click()
    await flushPromises()
    expect(rooms()).toEqual(['101', '102', '103', '104'])
    expect(text('[data-testid="proposed-status-3:30"]')).toBe('Dirty')
    expect(body('[data-testid="proposed-status-3:30"]')?.getAttribute('data-ready')).toBe('false')
    expect(body('[data-testid="proposed-status-1:10"]')?.getAttribute('data-ready')).toBe('true')
    expect(text('[data-testid="proposed-status-4:40"]')).toBe('Being cleaned')
    expect(text('[data-testid=auto-confirm]')).toBe('Assign 4 rooms')
    box.click()
    await flushPromises()
    expect(rooms()).toEqual(['101', '102', '–', '–'])
  })

  it('shows the bed that was asked for on a proposal that has it, and gives a kept bed only a room with it', async () => {
    await mountDialog(
      [line(1), line(2, { requestedBedTypeId: KING, bedLocked: true })],
      { default: [room(1, 'CLEAN', 8, 'Twin'), room(2, 'CLEAN', KING, 'King')] },
    )
    expect(rooms()).toEqual(['101', '102']) // the kept bed chose first: 102, and the other line took what was left
    expect(text('[data-testid=proposed-bed]')).toBe('✓ King')
  })

  it('saves the proposals one after the other, in the order of the list, with the room and the version of each row', async () => {
    const order: string[] = []
    POST = vi.fn(async (_p: string, init: { body: { room_id: number; version: number }; params: { path: { id: number; lineId: number } } }) => {
      order.push(`start ${init.params.path.lineId}`)
      await new Promise((r) => setTimeout(r, 5))
      order.push(`end ${init.params.path.lineId}`)
      return { data: { id: init.params.path.id, version: init.body.version + 1, rooms: [] } }
    })
    const w = await mountDialog([line(1), line(2)])
    ;(body('[data-testid=auto-confirm]') as HTMLElement).click()
    // wait for the work, not for a fixed time: a busy machine (the whole suite in parallel) takes longer than any number of milliseconds chosen here
    await vi.waitFor(() => expect(order).toHaveLength(4), { timeout: 5000 })
    await flushPromises()
    expect(order).toEqual(['start 10', 'end 10', 'start 20', 'end 20']) // never two at once
    expect(POST.mock.calls[0]?.[1]).toMatchObject({ params: { path: { propertyId: 7, id: 1, lineId: 10 } }, body: { version: 1, room_id: 1, upgrade: false } })
    expect(POST.mock.calls[1]?.[1]).toMatchObject({ params: { path: { propertyId: 7, id: 2, lineId: 20 } }, body: { version: 2, room_id: 2 } })
    expect(text('[data-testid=auto-summary]')).toBe('2 assigned, 0 not.')
    expect(w.emitted('changed')).toEqual([[2]])
  })

  it('follows the version of a reservation that has two rooms: the second room asks for the version the first answer gave', async () => {
    const a = line(1, { key: '5:50', reservationId: 5, lineId: 50, version: 3 })
    const b = line(2, { key: '5:60', reservationId: 5, lineId: 60, version: 3 }) // the same reservation, the same version on the row
    const other = line(3, { key: '6:70', reservationId: 6, lineId: 70, version: 9 })
    POST = vi.fn(async (_p: string, init: { body: { version: number }; params: { path: { id: number } } }) => ({ data: { id: init.params.path.id, version: init.body.version + 1, rooms: [] } }))
    await mountDialog([a, b, other], { default: [room(1, 'CLEAN'), room(2, 'CLEAN'), room(3, 'CLEAN')] })
    ;(body('[data-testid=auto-confirm]') as HTMLElement).click()
    await flushPromises()
    const versions = POST.mock.calls.map((c) => [(c[1] as { params: { path: { id: number } } }).params.path.id, (c[1] as { body: { version: number } }).body.version])
    expect(versions).toEqual([[5, 3], [5, 4], [6, 9]]) // 3, then the 4 that the answer gave, and the other reservation with its own
  })

  it('marks the one that fails and goes on with the others', async () => {
    POST = vi.fn(async (_p: string, init: { body: { room_id: number }; params: { path: { id: number } } }) => {
      if (init.body.room_id === 2) throw problem(409, 'ROOM_NOT_AVAILABLE')
      return { data: { id: init.params.path.id, version: 50, rooms: [] } }
    })
    const w = await mountDialog([line(1), line(2), line(3)], { default: [room(1, 'CLEAN'), room(2, 'CLEAN'), room(3, 'CLEAN')] })
    ;(body('[data-testid=auto-confirm]') as HTMLElement).click()
    await flushPromises()
    expect(POST).toHaveBeenCalledTimes(3) // the failure did not stop the third
    expect(bodyAll('[data-testid=outcome-ok]')).toHaveLength(2)
    expect(text('[data-testid=outcome-failed]')).toContain('That room was taken meanwhile')
    expect(text('[data-testid=auto-summary]')).toBe('2 assigned, 1 not.')
    expect(w.emitted('changed')).toEqual([[2]])
    // and the button of the confirmation is gone: what was done is not done twice
    expect(body('[data-testid=auto-confirm]')).toBeNull()
  })

  it('tells a reservation that was changed meanwhile, in its row', async () => {
    POST = vi.fn(async () => {
      throw problem(409, 'VERSION_CONFLICT')
    })
    await mountDialog([line(1)], { default: [room(1, 'CLEAN')] })
    ;(body('[data-testid=auto-confirm]') as HTMLElement).click()
    await flushPromises()
    expect(text('[data-testid=outcome-failed]')).toContain('The reservation was changed meanwhile')
    expect(text('[data-testid=auto-summary]')).toBe('0 assigned, 1 not.')
  })

  it('has nothing to confirm when no room fits', async () => {
    await mountDialog([line(1)], { default: [room(1, 'DIRTY')] })
    expect((body('[data-testid=auto-confirm]') as HTMLButtonElement).disabled).toBe(true)
    expect(text('[data-testid=proposal-none]')).toBe('No room fits')
  })

  it('says so when the rooms of a type could not be read, for the lines that wait for them', async () => {
    const w = await mountDialog([line(1), line(2, { typeId: 2, typeCode: 'STD' })])
    GET = vi.fn(async (_p: string, init: { params: { query: { room_type_id: number } } }) => {
      if (init.params.query.room_type_id === 2) throw problem(500, 'INTERNAL')
      return { data: { data: [room(1, 'CLEAN')] } }
    })
    await w.setProps({ open: false })
    await w.setProps({ open: true })
    await flushPromises()
    expect(rooms()).toEqual(['101', '–'])
    expect(text('[data-testid=proposal-unread]')).toBe('The free STD rooms could not be read.')
  })

  it('reads the rooms again each time it is opened, and forgets what was done before', async () => {
    const w = await mountDialog([line(1)], { default: [room(1, 'CLEAN')] })
    ;(body('[data-testid=auto-confirm]') as HTMLElement).click()
    await flushPromises()
    expect(body('[data-testid=auto-summary]')).not.toBeNull()
    await w.setProps({ open: false })
    await w.setProps({ open: true })
    await flushPromises()
    expect(GET).toHaveBeenCalledTimes(2)
    expect(body('[data-testid=auto-summary]')).toBeNull()
    expect(body('[data-testid=auto-confirm]')).not.toBeNull()
    expect((body('[data-testid=include-not-ready]') as HTMLInputElement).checked).toBe(false)
  })

  it('keeps the rows it was opened with when the parent reads its list again meanwhile', async () => {
    const w = await mountDialog([line(1), line(2)], { default: [room(1, 'CLEAN'), room(2, 'CLEAN')] })
    ;(body('[data-testid=auto-confirm]') as HTMLElement).click()
    await flushPromises()
    await w.setProps({ lines: [] }) // the rooms that were assigned are no longer in the list of the parent
    expect(rooms()).toEqual(['101', '102'])
  })

  it('says it in Indonesian', async () => {
    setLocale('id')
    await mountDialog([line(1), line(2)], { default: [room(1, 'CLEAN'), room(2, 'CLEAN')] })
    expect(text('[data-testid=auto-confirm]')).toBe('Tetapkan 2 kamar')
    expect(document.body.textContent).toContain('Sertakan kamar yang belum siap')
    ;(body('[data-testid=auto-confirm]') as HTMLElement).click()
    await flushPromises()
    expect(text('[data-testid=auto-summary]')).toBe('2 berhasil, 0 gagal.')
  })
})
