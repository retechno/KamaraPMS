import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { loadLocale, setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import GroupCancel from './GroupCancel.vue'

let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: vi.fn(), POST: (...a: unknown[]) => POST(...a) } }))

const body = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)

const member = (id: number, over: object = {}) => ({
  reservation_id: id, confirmation_number: `RES00000${id}`, status: 'CONFIRMED', display_status: 'CONFIRMED', guest_name: `Guest ${id}`, company_id: null, arrival_date: '2026-09-30', departure_date: '2026-10-01',
  room_count: 1, version: 2, rooms_cancellable: 1, rooms_in_house_or_done: 0, rooms_no_show: 0, rooms_cancelled: 0, held_balance: '0', ...over,
})
const MEMBERS = [member(1), member(2, { display_status: 'IN_HOUSE', rooms_cancellable: 0, rooms_in_house_or_done: 1 }), member(3, { room_count: 2, rooms_cancellable: 1, rooms_in_house_or_done: 1 }), member(4)]

const mounted: VueWrapper[] = []
function mountCard(members: object[] = MEMBERS, permissions = ['reservation.read', 'group.cancel', 'reservation.reinstate']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'boss@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.current = { currency_decimals: 0 } as never
  POST = vi.fn(async () => ({ data: { batch_id: 'k', rows: [] } }))
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  const w = mount(GroupCancel, { props: { groupId: 4, groupCode: 'CONF26', members: members as never }, global: { plugins: [pinia, router] }, attachTo: document.body })
  mounted.push(w)
  return w
}
const result = (rows: object[]) => ({ data: { batch_id: 'k', rows } })

describe('GroupCancel', () => {
  beforeEach(() => setLocale('en'))
  afterEach(() => {
    mounted.splice(0).forEach((w) => w.unmount())
    document.body.innerHTML = ''
  })

  it('lists the reservations that have a room waiting, says which have a room in house, and needs a reason', async () => {
    const w = mountCard()
    await flushPromises()
    expect(w.find('[data-testid=cancel-pick-1]').exists()).toBe(true)
    expect(w.find('[data-testid=cancel-pick-2]').exists()).toBe(false) // every room is in house: nothing to cancel
    expect(w.get('[data-testid=cancel-inhouse-3]').text()).toBe('1 checked in')
    expect(w.get('[data-testid=cancel-untouched]').text()).toBe('1 reservation(s) have nothing to cancel: every room is checked in or out.')
    expect(w.get('[data-testid=cancel-open]').attributes('disabled')).toBeDefined()
    await w.get('[data-testid=cancel-select-all]').trigger('click')
    expect(w.get('[data-testid=cancel-open]').text()).toBe('Cancel 3 reservation(s)…')
    expect(w.get('[data-testid=cancel-open]').attributes('disabled')).toBeDefined() // no reason yet
    expect(w.get('[data-testid=cancel-progress]').text()).toBe('Write why the rooms are cancelled.')
    await w.get('[data-testid=cancel-reason]').setValue('the conference is off')
    expect(w.get('[data-testid=cancel-open]').attributes('disabled')).toBeUndefined()
  })

  it('says in the dialog how many rooms are cancelled and how many are skipped, and sends nothing before the confirmation', async () => {
    const w = mountCard()
    await flushPromises()
    await w.get('[data-testid=cancel-select-all]').trigger('click')
    await w.get('[data-testid=cancel-reason]').setValue('the conference is off')
    await w.get('[data-testid=cancel-open]').trigger('click')
    expect(body('[data-testid=count-cancel]').text()).toBe('3 rooms will be cancelled')
    expect(body('[data-testid=count-skip]').text()).toBe('1 room is skipped because it is checked in or out (1 reservation(s) stay open)')
    expect(POST).not.toHaveBeenCalled()
    await body('[data-testid=group-cancel-back]').trigger('click')
    expect(document.body.querySelector('[data-testid=group-cancel-dialog]')).toBeNull()
    expect(POST).not.toHaveBeenCalled()
  })

  it('cancels in parts of 20 with a key for each, tells each reservation, keeps the failed ones ticked and says what is held', async () => {
    const many = Array.from({ length: 45 }, (_, i) => member(i + 1))
    const w = mountCard(many)
    await flushPromises()
    await w.get('[data-testid=cancel-select-all]').trigger('click')
    await w.get('[data-testid=cancel-reason]').setValue('off')
    await w.get('[data-testid=cancel-open]').trigger('click')
    POST.mockImplementation(async (_p: string, req: { body: { rows: { reservation_id: number }[] } }) => result(req.body.rows.map((r) => (
      r.reservation_id === 7 ? { reservation_id: 7, confirmation_number: 'RES000007', status: 'FAILED', code: 'VERSION_CONFLICT', message: 'changed' }
        : { reservation_id: r.reservation_id, confirmation_number: `RES00000${r.reservation_id}`, status: 'CANCELLED', rooms: [{ line_id: r.reservation_id, status: 'CANCELLED' }], deposits: r.reservation_id === 1 ? [{ payment_id: 5, payment_number: 'PAY000005' }] : [] }
    ))))
    await body('[data-testid=group-cancel-confirm]').trigger('click')
    await flushPromises()
    expect(POST).toHaveBeenCalledTimes(3) // 20 + 20 + 5
    expect(POST.mock.calls.map((c) => c[1].body.rows.length)).toEqual([20, 20, 5])
    expect(POST.mock.calls[0]![0]).toBe('/api/v1/properties/{propertyId}/groups/{id}/cancellations')
    expect(POST.mock.calls[0]![1].body.reason).toBe('off')
    const keys = POST.mock.calls.map((c) => c[1].params.header['Idempotency-Key'])
    expect(new Set(keys).size).toBe(3)
    expect(w.emitted('changed')).toBeTruthy()
    expect(w.get('[data-testid=group-cancel-notice]').text()).toBe('44 cancelled, 0 partly (a room in house stays), 0 already cancelled, 0 skipped, 1 failed.')
    expect(w.get('[data-testid=cancel-result-7]').text()).toContain('VERSION_CONFLICT')
    expect(w.find('[data-testid=group-cancel-failed]').exists()).toBe(true)
    expect((w.get('[data-testid=cancel-pick-7]').element as HTMLInputElement).checked).toBe(true) // the one that failed stays ticked
    expect((w.get('[data-testid=cancel-pick-8]').element as HTMLInputElement).checked).toBe(false)
  })

  it('tells a reservation that was partly cancelled and one that was skipped', async () => {
    const w = mountCard()
    await flushPromises()
    await w.get('[data-testid=cancel-select-all]').trigger('click')
    await w.get('[data-testid=cancel-reason]').setValue('off')
    await w.get('[data-testid=cancel-open]').trigger('click')
    POST.mockResolvedValueOnce(result([
      { reservation_id: 1, confirmation_number: 'RES000001', status: 'CANCELLED' },
      { reservation_id: 3, confirmation_number: 'RES000003', status: 'PARTLY_CANCELLED' },
      { reservation_id: 4, confirmation_number: 'RES000004', status: 'SKIPPED', code: 'ALREADY_CHECKED_IN' },
    ]))
    await body('[data-testid=group-cancel-confirm]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=cancel-result-3]').text()).toContain('Partly cancelled')
    expect(w.get('[data-testid=cancel-result-3]').text()).toContain('the room in house stays')
    expect(w.get('[data-testid=cancel-result-4]').text()).toContain('every room is checked in or out')
    expect(w.get('[data-testid=group-cancel-notice]').text()).toBe('1 cancelled, 1 partly (a room in house stays), 0 already cancelled, 1 skipped, 0 failed.')
  })

  it('reinstates the cancelled reservations and tells the room that was taken', async () => {
    const w = mountCard([member(1, { display_status: 'CANCELLED', status: 'CANCELLED', rooms_cancellable: 0, rooms_cancelled: 1 }), member(2, { display_status: 'CANCELLED', status: 'CANCELLED', rooms_cancellable: 0, rooms_cancelled: 1 })])
    await flushPromises()
    expect(w.get('[data-testid=reinstate-open]').text()).toBe('Reinstate the cancelled reservations (2)')
    // the person is asked first: how many come back, and that the rooms may be gone
    await w.get('[data-testid=reinstate-open]').trigger('click')
    expect(body('[data-testid=reinstate-count]').text()).toBe('2 reservations will be reinstated')
    expect(body('[data-testid=reinstate-warning]').text()).toContain('may have been taken by other bookings')
    expect(POST).not.toHaveBeenCalled()
    await body('[data-testid=group-reinstate-back]').trigger('click')
    expect(document.body.querySelector('[data-testid=group-reinstate-dialog]')).toBeNull()
    expect(POST).not.toHaveBeenCalled()
    await w.get('[data-testid=reinstate-open]').trigger('click')
    POST.mockResolvedValueOnce(result([
      { reservation_id: 1, confirmation_number: 'RES000001', status: 'REINSTATED' },
      { reservation_id: 2, confirmation_number: 'RES000002', status: 'FAILED', code: 'ROOM_UNAVAILABLE', message: 'taken' },
    ]))
    await body('[data-testid=group-reinstate-confirm]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]![0]).toBe('/api/v1/properties/{propertyId}/groups/{id}/reinstatements')
    expect(w.get('[data-testid=cancel-result-1]').text()).toContain('Reinstated')
    expect(w.get('[data-testid=cancel-result-2]').text()).toContain('ROOM_UNAVAILABLE')
    expect(w.get('[data-testid=group-cancel-notice]').text()).toBe('1 reinstated, 0 skipped, 1 could not be reinstated.')
  })

  it('is not shown without the right to cancel a group, and says so when nothing waits', async () => {
    const w = mountCard(MEMBERS, ['reservation.read', 'reservation.cancel'])
    await flushPromises()
    expect(w.find('[data-testid=group-cancel]').exists()).toBe(false)
    // the right of one reservation (reservation.reinstate) is not the right to bring a group back
    const redo = mountCard([member(1, { display_status: 'CANCELLED', status: 'CANCELLED', rooms_cancellable: 0, rooms_cancelled: 1 })], ['reservation.read', 'reservation.reinstate'])
    await flushPromises()
    expect(redo.find('[data-testid=group-cancel]').exists()).toBe(false)
    const none = mountCard([member(2, { display_status: 'IN_HOUSE', rooms_cancellable: 0, rooms_in_house_or_done: 1 })])
    await flushPromises()
    expect(none.find('[data-testid=group-cancel]').exists()).toBe(false)
  })

  it('speaks Indonesian', async () => {
    await loadLocale('id')
    await setLocale('id')
    const w = mountCard()
    await flushPromises()
    await w.get('[data-testid=cancel-select-all]').trigger('click')
    await w.get('[data-testid=cancel-reason]').setValue('acara batal')
    await w.get('[data-testid=cancel-open]').trigger('click')
    expect(body('[data-testid=count-cancel]').text()).toBe('3 kamar akan dibatalkan')
    expect(body('[data-testid=count-skip]').text()).toBe('1 kamar dilewati karena sudah check-in atau check-out (1 reservasi tetap berjalan)')
    expect(w.get('[data-testid=cancel-open]').text()).toBe('Batalkan 3 reservasi…')
  })
})
