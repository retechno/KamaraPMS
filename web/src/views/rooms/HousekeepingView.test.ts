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

/** Runs `fn` with the window of a phone (390 px), then puts the width back. */
async function onAPhone<T>(fn: () => Promise<T>): Promise<T> {
  const wide = window.innerWidth
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 })
  try {
    return await fn()
  } finally {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: wide })
  }
}

/** Opens the "..." menu of a room and returns the test ids of its items (the menu is drawn in the body, only while it is open). */
async function openMenu(w: ReturnType<typeof mountBoard>, number: string): Promise<string[]> {
  await w.get(`[data-testid=room-${number}] [data-slot=row-menu-trigger]`).trigger('click')
  await flushPromises()
  return Array.from(document.body.querySelectorAll('[data-slot=row-menu] [data-testid]')).map((e) => e.getAttribute('data-testid') ?? '')
}

/** The test ids of the buttons of the row of a room, outside the menu. */
const rowButtons = (w: ReturnType<typeof mountBoard>, number: string): string[] =>
  w.get(`[data-testid=room-${number}]`).findAll('button, a').filter((b) => b.attributes('data-slot') !== 'row-menu-trigger').map((b) => b.attributes('data-testid') ?? '')

describe('HousekeepingView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
    document.body.innerHTML = ''
    setLocale('en') // a test that fails before it puts the language back must not turn the next ones Indonesian
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
    expect(w.get('[data-testid=room-202] [data-testid=block]').text()).toBe('OOO until 4 Oct 2026')
  })

  it('offers only the legal next statuses and hides INSPECTED without housekeeping.inspect', async () => {
    const w = mountBoard(['housekeeping.update'], [room({ status: 'CLEAN', allowed_next: ['INSPECTED', 'DIRTY'] })])
    await flushPromises()
    expect(await openMenu(w, '201')).toEqual(['act-DIRTY', 'flags-201']) // mark dirty is not the next step: it is in the menu
    expect(rowButtons(w, '201')).toEqual([]) // no main button: INSPECTED needs housekeeping.inspect
    expect(document.body.querySelector('[data-testid=act-INSPECTED]')).toBeNull()
    expect(document.body.querySelector('[data-testid=act-CLEAN]')).toBeNull()
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
    expect(await openMenu(w, '201')).toContain('flags-201')
    ;(document.body.querySelector('[data-testid=flags-201]') as HTMLElement).click()
    await flushPromises()
    // the note is edited in a sheet, which is drawn in the body
    const sheet = document.body.querySelector('[data-testid=flag-sheet]')!
    const priority = sheet.querySelector('select[name=priority]') as HTMLSelectElement
    priority.value = 'HIGH'
    priority.dispatchEvent(new Event('change'))
    const dnd = sheet.querySelector('input[name=dnd]') as HTMLInputElement
    dnd.checked = true
    dnd.dispatchEvent(new Event('change'))
    const note = sheet.querySelector('input[name=note]') as HTMLInputElement
    note.value = 'VIP'
    note.dispatchEvent(new Event('input'))
    sheet.querySelector('[data-testid=flag-form]')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(PUT).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/rooms/{id}/housekeeping/flags', {
      params: { path: { propertyId: 7, id: 1 } }, body: { priority: 'HIGH', dnd: true, make_up_requested: false, note: 'VIP' },
    })
    expect(document.body.querySelector('[data-testid=flag-form]')).toBeNull()
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
    expect(await openMenu(w, '201')).toEqual(['report-201'])
    expect(document.body.querySelector('[data-testid=report-201]')!.getAttribute('href')).toBe('/maintenance?room=1')
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
    const w = mountBoard(['housekeeping.update'], [
      room({ priority: 'HIGH', dnd: true, occupancy: 'OCCUPIED' }),
      room({ room_id: 2, room_number: '202', block: { type: 'OOS', end_date: '2026-10-04' } }), // a blocked room has no main button
    ])
    await flushPromises()
    expect(w.get('[data-testid=room-201] [data-testid=status]').text()).toBe('Kotor')
    expect(w.get('[data-testid=act-CLEANING]').text()).toBe('Mulai bersihkan')
    expect(w.get('[data-testid=block]').text()).toBe('OOS sampai 4 Okt 2026')
    expect(w.get('[data-testid=flags]').text()).toContain('Tinggi')
    setLocale('en')
  })

  it('is a card on a phone: the next step is the main button, the flags and the maintenance report are in the menu', async () => {
    await onAPhone(async () => {
      const w = mountBoard(['housekeeping.update', 'maintenance.report'], [room({}), room({ room_id: 2, room_number: '202', status: 'CLEAN', allowed_next: [] })])
      await flushPromises()
      expect(w.find('table').exists()).toBe(false)
      const card = w.get('[data-testid=room-201]')
      expect(card.text()).toContain('201')
      expect(card.text()).toContain('Dirty')
      expect(card.find('[data-testid=act-CLEANING]').exists()).toBe(true) // the next cleaning step
      expect(card.find('[data-testid=act-CLEAN]').exists()).toBe(false) // the second step is in the menu
      await card.get('[data-testid=act-CLEANING]').trigger('click')
      await flushPromises()
      expect(PUT.mock.calls.length + POST.mock.calls.length).toBeGreaterThan(0)
      await card.get('[data-slot=row-menu-trigger]').trigger('click')
      await flushPromises()
      const items = Array.from(document.body.querySelectorAll('[data-slot=row-menu] [data-testid]')).map((e) => e.getAttribute('data-testid'))
      expect(items).toEqual(['act-CLEAN', 'flags-201', 'report-201'])
      w.unmount()
      document.body.innerHTML = ''
    })
  })

  it('has a short bar on a phone: one "Filter (n)" button, the chips and the selects in a sheet', async () => {
    await onAPhone(async () => {
      const w = mountBoard(['housekeeping.update'], [room({}), room({ room_id: 2, room_number: '202', status: 'CLEAN', allowed_next: [] })])
      await flushPromises()
      expect(w.find('[data-testid=filter-CLEAN]').exists()).toBe(false)
      expect(w.get('[data-testid=open-filters]').text()).toBe('Filter')
      await w.get('[data-testid=open-filters]').trigger('click')
      await flushPromises()
      const sheet = document.body.querySelector('[data-testid=filter-sheet]')!
      ;(sheet.querySelector('[data-testid=filter-CLEAN]') as HTMLElement).click()
      await flushPromises()
      expect(w.get('[data-testid=filter-count]').text()).toBe('(1)')
      expect(w.find('[data-testid=room-201]').exists()).toBe(false) // filtered to the clean ones
      expect(w.find('[data-testid=room-202]').exists()).toBe(true)
      w.unmount()
      document.body.innerHTML = ''
    })
  })

  it('has a card that is as short as its row: no "Block" and no "Flags" line when there is neither', async () => {
    await onAPhone(async () => {
      const w = mountBoard(['housekeeping.update'], [room({})])
      await flushPromises()
      const card = w.get('[data-testid=room-201]')
      expect(card.findAll('dt').map((e) => e.text())).toEqual(['Occupancy'])
      expect(card.text()).not.toContain('Notes')
      w.unmount()
      const flagged = mountBoard(['housekeeping.update'], [room({ priority: 'HIGH', dnd: true })])
      await flushPromises()
      expect(flagged.get('[data-testid=room-201]').findAll('dt').map((e) => e.text())).toEqual(['Occupancy', 'Notes'])
    })
  })

  describe('one next step per room', () => {
    const UPDATE = ['housekeeping.update']
    const INSPECT = ['housekeeping.update', 'housekeeping.inspect']
    const cases: { name: string; permissions: string[]; over: Record<string, unknown>; button: string[]; menu: string[] }[] = [
      { name: 'dirty: start cleaning is the button, mark clean is in the menu', permissions: UPDATE, over: {}, button: ['act-CLEANING'], menu: ['act-CLEAN', 'flags-201'] },
      { name: 'being cleaned: mark clean is the button', permissions: UPDATE, over: { status: 'CLEANING', allowed_next: ['CLEAN', 'DIRTY'] }, button: ['act-CLEAN'], menu: ['act-DIRTY', 'flags-201'] },
      { name: 'clean with housekeeping.inspect: inspect is the button', permissions: INSPECT, over: { status: 'CLEAN', allowed_next: ['INSPECTED', 'DIRTY'] }, button: ['act-INSPECTED'], menu: ['act-DIRTY', 'flags-201'] },
      { name: 'clean without housekeeping.inspect: no button', permissions: UPDATE, over: { status: 'CLEAN', allowed_next: ['INSPECTED', 'DIRTY'] }, button: [], menu: ['act-DIRTY', 'flags-201'] },
      { name: 'inspected: no button', permissions: INSPECT, over: { status: 'INSPECTED', allowed_next: ['DIRTY'] }, button: [], menu: ['act-DIRTY', 'flags-201'] },
      { name: 'blocked (OOS): no button', permissions: UPDATE, over: { block: { type: 'OOS', end_date: '2026-10-04' } }, button: [], menu: ['act-CLEANING', 'act-CLEAN', 'flags-201'] },
      { name: 'out of order: no button', permissions: INSPECT, over: { status: 'CLEAN', allowed_next: ['INSPECTED', 'DIRTY'], block: { type: 'OOO', end_date: '2026-10-04' } }, button: [], menu: ['act-INSPECTED', 'act-DIRTY', 'flags-201'] },
    ]
    for (const c of cases) {
      it(`${c.name}`, async () => {
        const w = mountBoard(c.permissions, [room(c.over)])
        await flushPromises()
        expect(rowButtons(w, '201')).toEqual(c.button)
        expect(await openMenu(w, '201')).toEqual(c.menu)
        w.unmount()
      })
    }

    it('shows no button and no empty menu for a room with nothing allowed', async () => {
      const w = mountBoard(['maintenance.report'], [room({ allowed_next: [] })])
      await flushPromises()
      expect(rowButtons(w, '201')).toEqual([]) // only the report link is in the menu
      expect(await openMenu(w, '201')).toEqual(['report-201'])
      w.unmount()
      const none = mountBoard([], [room({})])
      await flushPromises()
      expect(none.find('[data-slot=row-menu-trigger]').exists()).toBe(false)
      expect(none.find('[data-slot=row-actions]').exists()).toBe(false)
    })

    it('has the same actions in the row of the table and the card of a phone', async () => {
      const board = [room({}), room({ room_id: 2, room_number: '202', status: 'CLEAN', allowed_next: ['INSPECTED', 'DIRTY'] })]
      const table = mountBoard(INSPECT, board)
      await flushPromises()
      const wide = [await openMenu(table, '201'), rowButtons(table, '201')]
      table.unmount()
      document.body.innerHTML = ''
      await onAPhone(async () => {
        const card = mountBoard(INSPECT, board)
        await flushPromises()
        expect([await openMenu(card, '201'), rowButtons(card, '201')]).toEqual(wide)
        card.unmount()
      })
    })

    it('keeps the Notes column empty when a room has nothing, and tells a block from a note', async () => {
      const w = mountBoard(UPDATE, [
        room({}),
        room({ room_id: 2, room_number: '202', block: { type: 'OOO', end_date: '2026-10-04' } }),
        room({ room_id: 3, room_number: '203', dnd: true, flag_note: 'allergic to feathers' }),
      ])
      await flushPromises()
      const heads = w.findAll('thead th').map((h) => h.text())
      expect(heads).not.toContain('Block')
      expect(heads).not.toContain('Flags')
      const at = heads.indexOf('Notes')
      expect(at).toBeGreaterThan(-1)
      const notes = (n: string) => w.get(`[data-testid=room-${n}]`).findAll('td')[at]!
      expect(notes('201').text()).toBe('')
      expect(notes('201').find('[data-testid=block]').exists()).toBe(false)
      expect(notes('201').find('[data-testid=flags]').exists()).toBe(false)
      expect(notes('202').get('[data-testid=block]').text()).toBe('OOO until 4 Oct 2026')
      expect(notes('202').find('[data-testid=flags]').exists()).toBe(false)
      expect(notes('203').find('[data-testid=block]').exists()).toBe(false)
      expect(notes('203').get('[data-testid=flags]').text()).toContain('allergic to feathers')
      expect(notes('203').get('[data-testid=flags]').text()).toContain('DND')
    })

    it('shows at most one button and the menu trigger in every row of a board of 50 rooms', async () => {
      const statuses = [
        { status: 'DIRTY', allowed_next: ['CLEANING', 'CLEAN'] },
        { status: 'CLEANING', allowed_next: ['CLEAN', 'DIRTY'] },
        { status: 'CLEAN', allowed_next: ['INSPECTED', 'DIRTY'] },
        { status: 'INSPECTED', allowed_next: ['DIRTY'] },
        { status: 'DIRTY', allowed_next: ['CLEANING', 'CLEAN'], block: { type: 'OOO', end_date: '2026-10-04' } },
      ]
      const board = Array.from({ length: 50 }, (_, i) => room({ room_id: i + 1, room_number: String(101 + i), ...statuses[i % statuses.length] }))
      const w = mountBoard(['housekeeping.update', 'housekeeping.inspect', 'maintenance.report'], board)
      await flushPromises()
      const rows = w.findAll('tbody tr')
      expect(rows).toHaveLength(50)
      for (const r of rows) {
        const buttons = r.findAll('button, a').filter((b) => b.attributes('data-slot') !== 'row-menu-trigger')
        expect(buttons.length).toBeLessThanOrEqual(1)
        expect(r.findAll('[data-slot=row-menu-trigger]')).toHaveLength(1)
      }
      expect(w.findAll('tbody button, tbody a').length).toBeLessThanOrEqual(100) // was about 200
    })
  })
})
