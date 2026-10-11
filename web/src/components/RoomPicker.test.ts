import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import RoomPicker from './RoomPicker.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const KING = 7
const room = (id: number, status: string, bed: number | null = null, name = '') => ({ room_id: id, room_number: String(200 + id), housekeeping_status: status, bed_type_id: bed, bed_type_name: name })
const FREE = [room(1, 'DIRTY', KING, 'King'), room(2, 'CLEAN', null, 'Twin'), room(3, 'CLEANING', null, 'Twin'), room(4, 'INSPECTED', KING, 'King'), room(5, 'CLEAN', KING, 'King')]
const problem = (status: number, code: string, detail = 'x') => new ApiError({ type: 't', title: 'x', status, code, detail })

let wrapper: VueWrapper | null = null
const body = (sel: string) => document.body.querySelector(sel)
const bodyAll = (sel: string) => [...document.body.querySelectorAll(sel)]

function mountPicker(props: Record<string, unknown> = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const property = usePropertyStore()
  property.currentId = 7
  property.current = { id: 7, require_room_inspection_for_checkin: false } as never
  property.clock = { business_date: '2026-10-10' } as never
  wrapper = mount(RoomPicker, {
    attachTo: document.body,
    global: { plugins: [pinia] },
    props: { target: { reservationId: 41, lineId: 90, version: 3 }, typeId: 10, typeCode: 'DLX', arrival: '2026-10-10', departure: '2026-10-12', requestedBedTypeId: KING, ...props },
  })
  return wrapper
}
async function open(w: VueWrapper) {
  await w.get('[data-testid=pick-room]').trigger('click')
  await flushPromises()
}
// number, status and bed of each option, as the person reads them
const options = () =>
  bodyAll('[data-testid^=room-option-]').map((e) =>
    [e.querySelector('b')?.textContent, e.querySelector('[data-slot=status-badge]')?.textContent, e.querySelector('span.truncate')?.textContent?.replace(/\s+/g, ' ').replace('✓', ' ✓').trim()].filter(Boolean).join(' '),
  )

describe('RoomPicker', () => {
  beforeEach(() => {
    setLocale('en')
    GET = vi.fn(async () => ({ data: { data: FREE } }))
    POST = vi.fn(async () => ({ data: { id: 41, version: 4, rooms: [] } }))
  })
  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
    document.body.innerHTML = ''
  })

  it('reads nothing until it is opened, and then the free rooms of the booked type for the nights of the line', async () => {
    const w = mountPicker()
    await flushPromises()
    expect(GET).not.toHaveBeenCalled()
    await open(w)
    expect(GET).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/availability/rooms', { params: { path: { propertyId: 7 }, query: { room_type_id: 10, arrival: '2026-10-10', departure: '2026-10-12' } } })
    expect(body('[data-testid=room-picker]')).not.toBeNull()
  })

  it('does not ask for nights before the business date', async () => {
    const w = mountPicker({ arrival: '2026-10-08' })
    await open(w)
    expect(GET.mock.calls[0]?.[1]).toMatchObject({ params: { query: { arrival: '2026-10-10' } } })
  })

  it('lists the rooms ready first, and among them the bed that was asked for, each with its status and bed', async () => {
    const w = mountPicker()
    await open(w)
    // 204 and 205 are ready with a king, then 202 (ready), then 201 (king, dirty), then 203
    expect(options()).toEqual(['204 Inspected King ✓', '205 Clean King ✓', '202 Clean Twin', '201 Dirty King ✓', '203 Being cleaned Twin'])
  })

  it('for a guest who comes after the business date: no order by housekeeping status (the bed asked for, then the number), and the status is dimmed', async () => {
    const w = mountPicker({ arrival: '2026-10-24', departure: '2026-10-26' })
    await open(w)
    // King first (201 dirty, 204 inspected, 205 clean), then the others (202, 203), each group by number as the server gave them: today's status decided nothing
    expect(bodyAll('[data-testid^=room-option-]').map((e) => e.querySelector('b')?.textContent)).toEqual(['201', '204', '205', '202', '203'])
    expect(bodyAll('[data-testid^=room-option-] [data-slot=status-badge]').every((e) => e.getAttribute('data-later') === 'true' && e.className.includes('opacity-50'))).toBe(true)
  })

  it('for a guest who comes today the status still orders the rooms and is not dimmed', async () => {
    const w = mountPicker()
    await open(w)
    expect(bodyAll('[data-testid^=room-option-]').map((e) => e.querySelector('b')?.textContent)).toEqual(['204', '205', '202', '201', '203'])
    expect(bodyAll('[data-testid^=room-option-] [data-slot=status-badge]').some((e) => e.className.includes('opacity-50'))).toBe(false)
  })

  it('marks the bed that was asked for with a tick and leaves the others without', async () => {
    const w = mountPicker()
    await open(w)
    expect(body('[data-testid=room-option-4] [data-slot=bed-match]')).not.toBeNull()
    expect(body('[data-testid=room-option-2] [data-slot=bed-match]')).toBeNull()
    expect(body('[data-testid=room-option-4]')?.getAttribute('data-bed-match')).toBe('true')
    expect(body('[data-testid=room-option-2]')?.getAttribute('data-ready')).toBe('true')
    expect(body('[data-testid=room-option-1]')?.getAttribute('data-ready')).toBe('false')
  })

  it('puts the cursor in the search field and finds a room by its number', async () => {
    const w = mountPicker()
    await open(w)
    const search = body('[data-testid=room-search]') as HTMLInputElement
    expect(document.activeElement).toBe(search)
    search.value = '20'
    search.dispatchEvent(new Event('input'))
    await flushPromises()
    expect(options()).toHaveLength(5)
    search.value = '204'
    search.dispatchEvent(new Event('input'))
    await flushPromises()
    expect(options()).toEqual(['204 Inspected King ✓'])
    search.value = '999'
    search.dispatchEvent(new Event('input'))
    await flushPromises()
    expect(body('[data-testid=room-picker-nomatch]')?.textContent).toContain('No room has that number.')
  })

  it('moves with the arrows and chooses with Enter: the first room is active, the arrows wrap, Enter assigns the active one', async () => {
    const w = mountPicker()
    await open(w)
    const search = body('[data-testid=room-search]') as HTMLInputElement
    const key = (k: string) => search.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }))
    expect(bodyAll('[role=option]')[0]?.getAttribute('aria-selected')).toBe('true')
    key('ArrowDown')
    key('ArrowDown')
    await flushPromises()
    expect(bodyAll('[role=option]').findIndex((e) => e.getAttribute('aria-selected') === 'true')).toBe(2)
    expect(search.getAttribute('aria-activedescendant')).toBe('room-option-90-2')
    key('ArrowUp')
    key('ArrowUp')
    key('ArrowUp') // from the first to the last
    await flushPromises()
    expect(bodyAll('[role=option]').findIndex((e) => e.getAttribute('aria-selected') === 'true')).toBe(4)
    key('Enter')
    await flushPromises()
    expect(POST).toHaveBeenCalledTimes(1)
    expect(POST.mock.calls[0]?.[1]).toMatchObject({ body: { room_id: 3, version: 3 } }) // 203, the last of the list
  })

  it('assigns the room that is clicked, with the version of the row, and tells the parent; the popover closes', async () => {
    const w = mountPicker()
    await open(w)
    ;(body('[data-testid=room-option-4]') as HTMLElement).click()
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/assign-room', {
      params: { path: { propertyId: 7, id: 41, lineId: 90 } },
      body: { version: 3, room_id: 4, upgrade: false },
    })
    expect(w.emitted('assigned')).toHaveLength(1)
    expect((w.emitted('assigned')![0]![0] as { version: number }).version).toBe(4)
    expect(body('[data-testid=room-picker]')).toBeNull()
  })

  it('never asks for an upgrade unless the page of the reservation says so', async () => {
    const w = mountPicker({ upgrade: true })
    await open(w)
    ;(body('[data-testid=room-option-4]') as HTMLElement).click()
    await flushPromises()
    expect(POST.mock.calls[0]?.[1]).toMatchObject({ body: { upgrade: true } })
  })

  it('says "Choose room" for a line without a room and "Change room" for one that has it', async () => {
    const w = mountPicker()
    expect(w.get('[data-testid=pick-room]').text()).toBe('Choose room')
    w.unmount()
    wrapper = null
    const has = mountPicker({ currentRoomId: 2 })
    expect(has.get('[data-testid=pick-room]').text()).toBe('Change room')
    await open(has)
    expect(options()[0]).toContain('202') // the room that is on the line is listed first when it is among the free ones
  })

  it('says it in Indonesian', async () => {
    setLocale('id')
    const w = mountPicker()
    expect(w.get('[data-testid=pick-room]').text()).toBe('Pilih kamar')
    await open(w)
    expect((body('[data-testid=room-search]') as HTMLInputElement).placeholder).toBe('Cari nomor kamar')
  })

  it('says so when no room of the type is free', async () => {
    GET = vi.fn(async () => ({ data: { data: [] } }))
    const w = mountPicker()
    await open(w)
    expect(body('[data-testid=room-picker-none]')?.textContent).toContain('No free DLX room for these nights.')
  })

  describe('when the assignment is refused', () => {
    async function refused(error: ApiError) {
      const w = mountPicker()
      await open(w)
      POST = vi.fn(async () => {
        throw error
      })
      const before = GET.mock.calls.length
      ;(body('[data-testid=room-option-4]') as HTMLElement).click()
      await flushPromises()
      return { w, before }
    }

    it('tells a room that was taken meanwhile, reads the free rooms again and stays open for another choice', async () => {
      const { w, before } = await refused(problem(409, 'ROOM_NOT_AVAILABLE'))
      expect(body('[data-testid=room-picker-error]')?.textContent).toContain('That room was taken meanwhile')
      expect(GET.mock.calls.length).toBe(before + 1)
      expect(body('[data-testid=room-picker]')).not.toBeNull()
      const failure = w.emitted('failed')![0]![0] as { kind: string; reload: boolean }
      expect(failure).toMatchObject({ kind: 'taken', reload: true })
      expect(w.emitted('assigned')).toBeUndefined()
    })

    it('tells a reservation that was changed meanwhile (an old version) and asks the parent to read it again', async () => {
      const { w, before } = await refused(problem(409, 'VERSION_CONFLICT'))
      expect(body('[data-testid=room-picker-error]')?.textContent).toContain('The reservation was changed meanwhile')
      expect(GET.mock.calls.length).toBe(before + 1)
      expect(w.emitted('failed')![0]![0]).toMatchObject({ kind: 'changed', reload: true, code: 'VERSION_CONFLICT' })
    })

    it('tells a bed that is no longer free as a room that was taken', async () => {
      const { w } = await refused(problem(409, 'BED_NOT_AVAILABLE'))
      expect(w.emitted('failed')![0]![0]).toMatchObject({ kind: 'taken' })
    })

    it('tells a room that no longer waits for its check-in', async () => {
      await refused(problem(409, 'LINE_NOT_CONFIRMED'))
      expect(body('[data-testid=room-picker-error]')?.textContent).toContain('no longer waiting for check-in')
    })

    it('shows the message of the server for another refusal, and reads nothing again for a 422', async () => {
      const { before } = await refused(problem(422, 'VALIDATION_FAILED', 'the request is invalid'))
      expect(body('[data-testid=room-picker-error]')?.textContent).toContain('the request is invalid')
      expect(GET.mock.calls.length).toBe(before)
    })

    it('says the same in Indonesian', async () => {
      setLocale('id')
      await refused(problem(409, 'ROOM_NOT_AVAILABLE'))
      expect(body('[data-testid=room-picker-error]')?.textContent).toContain('Kamar itu sudah diambil')
    })
  })
})
