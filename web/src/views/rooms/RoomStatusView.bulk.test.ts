import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import RoomStatusView from './RoomStatusView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const room = (id: number, number: string, floor: string, status: string, extra: object = {}) => ({
  room_id: id, room_number: number, floor, room_type_id: 10, room_type_code: 'DLX', room_type_name: 'Deluxe', status, status_updated_at: '2026-09-30T08:00:00Z', occupancy: 'VACANT', allowed_next: [], priority: 'NORMAL', dnd: false, make_up_requested: false, ...extra,
})
const BOARD = [room(1, '101', '1', 'DIRTY'), room(2, '102', '1', 'DIRTY'), room(3, '201', '2', 'CLEAN'), room(4, '202', '2', 'INSPECTED'), room(5, '203', '2', 'CLEANING')]
const MAID = ['housekeeping.read', 'housekeeping.update']
const SUPERVISOR = [...MAID, 'housekeeping.inspect']

function mountView(permissions: string[], board: object[] = BOARD) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: { data: board } })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(RoomStatusView, { attachTo: document.body, global: { plugins: [router] } })
}
type Wrapper = ReturnType<typeof mountView>
const pick = (w: Wrapper, ...numbers: string[]) => Promise.all(numbers.map((n) => w.get(`[data-testid=pick-${n}]`).trigger('click')))
const posted = (): { room_ids: number[]; status: string }[] => POST.mock.calls.map((c) => (c[1] as { body: { room_ids: number[]; status: string } }).body)
const many = (n: number) => Array.from({ length: n }, (_, i) => room(i + 1, String(100 + i), '1', 'DIRTY'))
const okResults = (ids: number[]) => ({ data: { summary: { requested: ids.length, changed: ids.length, failed: 0 }, results: ids.map((id) => ({ room_id: id, room_number: String(id), ok: true, from: 'DIRTY', status: 'CLEAN' })) } })

describe('RoomStatusView: the bulk change', () => {
  let ask: ReturnType<typeof vi.spyOn>
  beforeEach(() => {
    ask = vi.spyOn(window, 'confirm').mockReturnValue(true) // no shell in these tests: the question falls back to the browser's
  })
  afterEach(() => {
    ask.mockRestore()
    document.body.innerHTML = ''
  })

  it('has no checkboxes and no bar for someone who cannot change housekeeping', async () => {
    const w = mountView(['housekeeping.read'])
    await flushPromises()
    expect(w.find('[data-testid=pick-101]').exists()).toBe(false)
    expect(w.find('[data-testid=pick-all]').exists()).toBe(false)
    expect(w.find('[data-testid=bulk-bar]').exists()).toBe(false)
    w.unmount()
  })

  it('selects rooms, shows the bar with the number on each button, and hides "Inspected" without housekeeping.inspect', async () => {
    const w = mountView(MAID)
    await flushPromises()
    expect(w.find('[data-testid=bulk-bar]').exists()).toBe(false)
    await pick(w, '101', '102')
    expect(w.get('[data-testid=bulk-count]').text()).toBe('2 rooms selected')
    expect(w.get('[data-testid=bulk-clean]').text()).toBe('Mark clean (2)')
    expect(w.get('[data-testid=bulk-dirty]').text()).toBe('Mark dirty (0)') // both are dirty already: nothing to change
    expect(w.find('[data-testid=bulk-inspected]').exists()).toBe(false)
    expect(w.get('[data-testid=tile-101]').attributes('data-picked')).toBe('true')
    await w.get('[data-testid=bulk-cancel]').trigger('click')
    expect(w.find('[data-testid=bulk-bar]').exists()).toBe(false)
    w.unmount()

    const sup = mountView(SUPERVISOR)
    await flushPromises()
    await pick(sup, '101', '201') // a dirty room cannot be inspected, a clean one can
    expect(sup.get('[data-testid=bulk-inspected]').text()).toBe('Inspected (1)')
    sup.unmount()
  })

  it('opens the details when nothing is selected, and selects the tile when something is', async () => {
    const w = mountView(MAID)
    await flushPromises()
    await w.get('[data-testid=tile-101]').trigger('click')
    await flushPromises()
    expect(document.body.querySelector('[data-testid=room-sheet]')).not.toBeNull()
    w.unmount()
    document.body.innerHTML = ''

    const w2 = mountView(MAID)
    await flushPromises()
    await pick(w2, '101')
    await w2.get('[data-testid=tile-102]').trigger('click')
    expect(w2.get('[data-testid=bulk-count]').text()).toBe('2 rooms selected')
    expect(document.body.querySelector('[data-testid=room-sheet]')).toBeNull()
    await w2.get('[data-testid=tile-102]').trigger('click') // again: unselected
    expect(w2.get('[data-testid=bulk-count]').text()).toBe('1 room selected')
    w2.unmount()
  })

  it('selects everything that is shown, or a floor, and drops the rooms a filter hides', async () => {
    const w = mountView(MAID)
    await flushPromises()
    await w.get('[data-testid=pick-all]').trigger('click')
    expect(w.get('[data-testid=bulk-count]').text()).toBe('5 rooms selected')
    await w.get('[data-testid=pick-all]').trigger('click') // pressed again: none
    expect(w.find('[data-testid=bulk-bar]').exists()).toBe(false)

    await w.get('[data-testid=pick-floor-2]').trigger('click')
    expect(w.get('[data-testid=bulk-count]').text()).toBe('3 rooms selected')
    await w.get('[data-testid=filter-hk-INSPECTED]').trigger('click') // only 202 is shown: the others are not selected any more
    expect(w.get('[data-testid=bulk-count]').text()).toBe('1 room selected')
    await w.get('[data-testid=filter-hk-ALL]').trigger('click')
    expect(w.get('[data-testid=bulk-count]').text()).toBe('1 room selected') // and do not come back by themselves
    w.unmount()
  })

  it('counts the rooms on the cleaning chips, by the other filters', async () => {
    const w = mountView(MAID)
    await flushPromises()
    const counts = () => ['ALL', 'DIRTY', 'CLEANING', 'CLEAN', 'INSPECTED'].map((k) => w.get(`[data-testid=count-hk-${k}]`).text())
    expect(counts()).toEqual(['5', '2', '1', '1', '1'])
    await w.get('input[name=room_search]').setValue('20') // 201, 202, 203
    expect(counts()).toEqual(['3', '0', '1', '1', '1'])
    await w.get('[data-testid=filter-hk-DIRTY]').trigger('click') // the chip being pressed does not change the counts
    expect(counts()).toEqual(['3', '0', '1', '1', '1'])
    w.unmount()
  })

  it('changes the rooms, reports the skipped ones room by room, and keeps only those selected', async () => {
    const w = mountView(SUPERVISOR)
    await flushPromises()
    POST.mockResolvedValue({
      data: {
        summary: { requested: 3, changed: 1, failed: 2 },
        results: [
          { room_id: 1, room_number: '101', ok: true, from: 'DIRTY', status: 'CLEAN' },
          { room_id: 3, room_number: '201', ok: false, from: 'CLEAN', status: 'CLEAN', error: { code: 'INVALID_HK_TRANSITION', message: 'x', context: {} } },
          { room_id: 4, room_number: '202', ok: false, from: 'INSPECTED', status: 'INSPECTED', error: { code: 'INVALID_HK_TRANSITION', message: 'x', context: {} } },
        ],
      },
    })
    await pick(w, '101', '201', '202')
    expect(w.get('[data-testid=bulk-clean]').text()).toBe('Mark clean (1)') // 201 is clean already, 202 cannot go there
    await w.get('[data-testid=bulk-clean]').trigger('click')
    await flushPromises()

    expect(POST).toHaveBeenCalledTimes(1)
    expect(POST.mock.calls[0]![0]).toBe('/api/v1/properties/{propertyId}/housekeeping/bulk')
    expect(posted()).toEqual([{ room_ids: [1, 3, 4], status: 'CLEAN' }])
    expect(w.get('[data-testid=bulk-result-summary]').text()).toBe('1 changed · 1 unchanged · 1 skipped')
    expect(w.get('[data-testid=same-201]').text()).toBe('201 · already clean') // neutral: not in the list of the skipped
    expect(w.find('[data-testid=skipped-201]').exists()).toBe(false)
    expect(w.get('[data-testid=skipped-202]').text()).toBe('202 · cannot go from inspected to clean')
    expect(w.get('[data-testid=bulk-count]').text()).toBe('1 room selected') // only the one that was skipped; the unchanged one has nothing left to do
    expect(GET).toHaveBeenCalledTimes(2) // the board was read again
    await w.get('[data-testid=bulk-result-close]').trigger('click')
    expect(w.find('[data-testid=bulk-result]').exists()).toBe(false)
    w.unmount()
  })

  it('says how many changed when all did, and leaves nothing selected', async () => {
    const w = mountView(MAID)
    await flushPromises()
    POST.mockResolvedValue(okResults([1, 2]))
    await pick(w, '101', '102')
    await w.get('[data-testid=bulk-clean]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=bulk-result-summary]').text()).toBe('2 rooms changed')
    expect(w.find('[data-testid=bulk-skipped]').exists()).toBe(false)
    expect(w.find('[data-testid=bulk-bar]').exists()).toBe(false)
    w.unmount()
  })

  it('shows the error of a refused request and keeps the selection', async () => {
    const w = mountView(MAID)
    await flushPromises()
    const { ApiError } = await import('@/api/problem')
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Forbidden', status: 403, code: 'PERMISSION_DENIED', detail: 'not allowed' }))
    await pick(w, '101')
    await w.get('[data-testid=bulk-clean]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid=form-error]').exists()).toBe(true)
    expect(w.get('[data-testid=bulk-count]').text()).toBe('1 room selected')
    w.unmount()
  })

  describe('asks first only when it matters', () => {
    it('does not ask for 10 rooms, and asks for 11', async () => {
      const w = mountView(MAID, many(11))
      await flushPromises()
      POST.mockImplementation((_p: string, o: { body: { room_ids: number[] } }) => Promise.resolve(okResults(o.body.room_ids)))
      for (let i = 0; i < 10; i++) await pick(w, String(100 + i))
      await w.get('[data-testid=bulk-clean]').trigger('click')
      await flushPromises()
      expect(ask).not.toHaveBeenCalled()
      expect(posted()).toHaveLength(1)
      expect(posted()[0]!.room_ids).toHaveLength(10)

      POST.mockClear()
      GET.mockResolvedValue({ data: { data: many(11) } })
      await w.get('[data-testid=refresh]').trigger('click')
      await flushPromises()
      await w.get('[data-testid=pick-all]').trigger('click')
      expect(w.get('[data-testid=bulk-count]').text()).toBe('11 rooms selected')
      ask.mockReturnValueOnce(false)
      await w.get('[data-testid=bulk-clean]').trigger('click')
      await flushPromises()
      expect(ask).toHaveBeenCalledTimes(1)
      expect(ask.mock.calls[0]![0]).toContain('Change 11 rooms to clean?')
      expect(ask.mock.calls[0]![0]).toContain('More than 10 rooms change in one go.')
      expect(POST).not.toHaveBeenCalled() // "no": nothing changes, the selection stays
      expect(w.get('[data-testid=bulk-count]').text()).toBe('11 rooms selected')
      await w.get('[data-testid=bulk-clean]').trigger('click') // "yes"
      await flushPromises()
      expect(posted()).toHaveLength(1)
      expect(posted()[0]!.room_ids).toHaveLength(11)
      w.unmount()
    })

    it('asks when inspected rooms would go back to dirty, with their number, even for few rooms', async () => {
      const w = mountView(SUPERVISOR, [room(1, '101', '1', 'INSPECTED'), room(2, '102', '1', 'INSPECTED'), room(3, '103', '1', 'CLEAN')])
      await flushPromises()
      POST.mockResolvedValue(okResults([1, 2, 3]))
      await pick(w, '101', '102', '103')
      await w.get('[data-testid=bulk-dirty]').trigger('click')
      await flushPromises()
      expect(ask).toHaveBeenCalledTimes(1)
      const text = ask.mock.calls[0]![0] as string
      expect(text).toContain('Change 3 rooms to dirty?')
      expect(text).toContain('2 rooms that were already inspected will go back to dirty.')
      expect(text).not.toContain('More than')
      expect(posted()).toHaveLength(1)
      w.unmount()
    })

    it('does not ask when the action does not lower inspected rooms (to clean, or no inspected room in the selection)', async () => {
      const w = mountView(SUPERVISOR, [room(1, '101', '1', 'CLEAN'), room(2, '102', '1', 'DIRTY'), room(3, '103', '1', 'INSPECTED')])
      await flushPromises()
      POST.mockResolvedValue(okResults([2]))
      await pick(w, '101', '102')
      await w.get('[data-testid=bulk-clean]').trigger('click') // 101 is clean already, 102 changes
      await flushPromises()
      expect(ask).not.toHaveBeenCalled()
      // 102 left the selection; 101 (clean) is alone in it, to dirty: no inspected room in it
      await w.get('[data-testid=bulk-dirty]').trigger('click')
      await flushPromises()
      expect(ask).not.toHaveBeenCalled()
      w.unmount()
    })

    it('counts only the rooms that change against the limit of 10', async () => {
      const board = [...many(10), room(50, '150', '1', 'CLEAN'), room(51, '151', '1', 'CLEAN')] // 10 dirty and 2 clean ones
      const w = mountView(MAID, board)
      await flushPromises()
      POST.mockImplementation((_p: string, o: { body: { room_ids: number[] } }) => Promise.resolve(okResults(o.body.room_ids)))
      await w.get('[data-testid=pick-all]').trigger('click')
      expect(w.get('[data-testid=bulk-count]').text()).toBe('12 rooms selected')
      expect(w.get('[data-testid=bulk-clean]').text()).toBe('Mark clean (10)') // 12 selected, 10 change: no question
      await w.get('[data-testid=bulk-clean]').trigger('click')
      await flushPromises()
      expect(ask).not.toHaveBeenCalled()
      expect(posted()[0]!.room_ids).toHaveLength(12) // the server is told all of them and says which ones stayed
      w.unmount()
    })

    it('disables a button that would change nothing and says why', async () => {
      const w = mountView(SUPERVISOR)
      await flushPromises()
      await pick(w, '101', '102') // both dirty
      const dirty = w.get('[data-testid=bulk-dirty]')
      expect(dirty.attributes('disabled')).toBeDefined()
      expect(dirty.attributes('title')).toBe('All are already dirty')
      expect(w.get('[data-testid=bulk-inspected]').attributes('title')).toBe('None of the selected rooms can become inspected')
      expect(w.get('[data-testid=bulk-clean]').attributes('disabled')).toBeUndefined()
      await dirty.trigger('click')
      expect(POST).not.toHaveBeenCalled()
      w.unmount()
    })

    it('sends a long selection in requests of 100 rooms and puts the answers together', async () => {
      const w = mountView(MAID, many(150))
      await flushPromises()
      POST.mockImplementation((_p: string, o: { body: { room_ids: number[] } }) => Promise.resolve(okResults(o.body.room_ids)))
      await w.get('[data-testid=pick-all]').trigger('click')
      await w.get('[data-testid=bulk-clean]').trigger('click')
      await flushPromises()
      expect(ask).toHaveBeenCalledTimes(1)
      expect(posted().map((b) => b.room_ids.length)).toEqual([100, 50])
      expect(w.get('[data-testid=bulk-result-summary]').text()).toBe('150 rooms changed')
      w.unmount()
    })
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    const w = mountView(SUPERVISOR)
    await flushPromises()
    await pick(w, '101', '201')
    expect(w.get('[data-testid=bulk-count]').text()).toBe('2 kamar dipilih')
    expect(w.get('[data-testid=bulk-clean]').text()).toBe('Tandai bersih (1)')
    expect(w.get('[data-testid=bulk-inspected]').text()).toBe('Diperiksa (1)')
    await pick(w, '201')
    expect(w.get('[data-testid=bulk-dirty]').attributes('title')).toBe('Semua sudah kotor')
    w.unmount()
    setLocale('en')
  })
})
