import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import GroupCheckIn from './GroupCheckIn.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const body = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)

const arrival = (line: number, over: object = {}) => ({
  reservation_id: 40 + line, confirmation_number: `RES00000${line}`, reservation_room_id: line, reservation_version: 2, guest_id: 5, guest_name: `Guest ${line}`, room_type_id: 3, room_type_code: 'DLX', room_type_name: 'Deluxe',
  room_id: 100 + line, room_number: `10${line}`, arrival_date: '2026-09-30', departure_date: '2026-10-01', adult_count: 2, child_count: 0, status: 'CONFIRMED', reservation_status: 'CONFIRMED',
  rate: { rate_plan_code: 'BAR', rate_plan_name: 'Best', amount: '900000', price_mode: 'EXCLUSIVE' }, company: null, deposit: null, readiness: { status: 'READY', blockers: [] },
  guest_has_id: true, guest_has_phone: true, guest_is_placeholder: false, estimate_total: '1000000', ...over,
})
const master = { id: 30, folio_number: 'FOL000030', status: 'OPEN', balance: '2000000', version: 3, bill_to_company_id: null, opened_at: '', closed_at: null }
const routing = (over: object = {}) => ({
  group_id: 4, master, instructions: [{ scope: 'ROOM', charge_code_id: null }],
  lines: [1, 2, 3].map((i) => ({ reservation_room_id: i, reservation_id: 40 + i, confirmation_number: `RES00000${i}`, status: 'CONFIRMED', excluded: i === 3 })), ...over,
})

const ALL = ['reservation.read', 'frontdesk.checkin', 'payment.post', 'frontdesk.checkin_unready_room', 'reservation.update']
const mounted: VueWrapper[] = []
function mountCard(permissions = ALL, rows: object[] = [arrival(1), arrival(2, { readiness: { status: 'BLOCKED', blockers: ['ROOM_NOT_READY'] } }), arrival(3)], r: object = routing()) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'boss@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.current = { require_room_inspection_for_checkin: false, currency_decimals: 0, checkin_incidental_deposit: '200000' } as never
  property.clock = { business_date: '2026-09-30' } as never
  GET = vi.fn(async (path: string) => (path.endsWith('/arrivals') ? { data: { data: rows } } : { data: r }))
  POST = vi.fn(async () => ({ data: { batch_id: 'k', rows: [], checked_in: 0, already_checked_in: 0, failed: 0, deposits_recorded: 0 } }))
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  const w = mount(GroupCheckIn, { props: { groupId: 4, active: true }, global: { plugins: [pinia, router] }, attachTo: document.body })
  mounted.push(w)
  return w
}
const rowResult = (line: number, over: object = {}) => ({ line_id: line, status: 'CHECKED_IN', stay: { id: 900 + line, stay_number: `STY00000${line}` }, room_number: `10${line}`, ...over })

describe('GroupCheckIn', () => {
  beforeEach(() => setLocale('en'))
  afterEach(() => {
    mounted.splice(0).forEach((w) => w.unmount())
    document.body.innerHTML = ''
  })

  it('reads the arrivals of the group with the estimate, and shows the readiness and the identity warning of each room', async () => {
    const w = mountCard(ALL, [arrival(1, { guest_nationality: 'FR', guest_has_id: false }), arrival(2, { readiness: { status: 'BLOCKED', blockers: ['ROOM_NOT_READY'] } }), arrival(3, { guest_is_placeholder: true, guest_name: 'Guest 3' })])
    await flushPromises()
    const call = GET.mock.calls.find((c) => String(c[0]).endsWith('/arrivals'))
    expect(call?.[1].params.query).toEqual({ booking_group_id: 4, with_estimate: true })
    expect(w.get('[data-testid=identity-1]').text()).toBe('Passport?')
    expect(w.find('[data-testid=identity-3]').exists()).toBe(false) // a placeholder has no profile to judge
    expect(w.get('[data-testid=placeholder-3]').text()).not.toBe('')
    expect(w.findAll('[data-testid=readiness]').map((r) => r.attributes('data-status'))).toEqual(['READY', 'BLOCKED', 'READY'])
    expect(w.get('[data-testid=pick-2]').attributes('disabled')).toBeDefined() // not ready: not ticked without the override
  })

  it('selects every room that is ready with one button and checks them in, 20 to a request, each request with a key of its own', async () => {
    const many = Array.from({ length: 45 }, (_, i) => arrival(i + 1, { room_id: 200 + i, room_number: String(200 + i) }))
    const lines = many.map((a) => ({ reservation_room_id: a.reservation_room_id, reservation_id: a.reservation_id, confirmation_number: a.confirmation_number, status: 'CONFIRMED', excluded: false }))
    const w = mountCard(ALL, many, routing({ lines }))
    await flushPromises()
    await w.get('[data-testid=select-ready]').trigger('click')
    POST.mockImplementation(async (_p: string, o: { body: { rows: { line_id: number }[] } }) => ({ data: { batch_id: 'k', rows: o.body.rows.map((r) => rowResult(r.line_id)), checked_in: o.body.rows.length, already_checked_in: 0, failed: 0, deposits_recorded: 0 } }))
    await w.get('[data-testid=group-checkin-run]').trigger('click')
    await flushPromises()
    expect(POST).toHaveBeenCalledTimes(3)
    expect(POST.mock.calls.map((c) => c[1].body.rows.length)).toEqual([20, 20, 5])
    const keys = POST.mock.calls.map((c) => c[1].params.header['Idempotency-Key'])
    expect(new Set(keys).size).toBe(3)
    expect(POST.mock.calls[0]![1].body.rows[0]).toMatchObject({ line_id: 1, version: 2, room_id: 200, guest_id: 5, adult_count: 2, override_room_not_ready: false })
    expect(w.get('[data-testid=group-checkin-notice]').text()).toBe('45 checked in, 0 already in, 0 could not be checked in.')
  })

  it('shows what failed on the row, keeps it ticked, and sends it again in a new request', async () => {
    const w = mountCard()
    await flushPromises()
    await w.get('[data-testid=select-ready]').trigger('click')
    POST.mockResolvedValueOnce({ data: { batch_id: 'k', rows: [rowResult(1), { line_id: 3, status: 'FAILED', code: 'ROOM_OCCUPIED', message: 'the room is occupied' }], checked_in: 1, already_checked_in: 0, failed: 1, deposits_recorded: 0 } })
    await w.get('[data-testid=group-checkin-run]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=failed-3]').text()).toBe('ROOM_OCCUPIED: the room is occupied')
    expect(w.get('[data-testid=group-checkin-notice]').classes()).toContain('warning')
    expect(w.get('[data-testid=pick-3]').element).toHaveProperty('checked', true) // still ticked
    expect(w.get('[data-testid=group-checkin-done]').text()).toContain('STY000001')
    expect(w.get('[data-testid=group-checkin-failed]').text()).toContain('A deposit that was taken is not taken twice')
    POST.mockResolvedValueOnce({ data: { batch_id: 'k2', rows: [rowResult(3)], checked_in: 1, already_checked_in: 0, failed: 0, deposits_recorded: 0 } })
    await w.get('[data-testid=group-checkin-run]').trigger('click')
    await flushPromises()
    expect(POST).toHaveBeenCalledTimes(2)
    expect(POST.mock.calls[1]![1].body.rows.map((r: { line_id: number }) => r.line_id)).toEqual([3]) // only the room that failed
    expect(POST.mock.calls[1]![1].params.header['Idempotency-Key']).not.toBe(POST.mock.calls[0]![1].params.header['Idempotency-Key'])
  })

  it('keeps the rooms that were checked in on the screen when no room is left to check in, and says a deposit was not taken again', async () => {
    const w = mountCard(ALL, [arrival(1)])
    await flushPromises()
    await w.get('[data-testid=select-ready]').trigger('click')
    POST.mockResolvedValueOnce({ data: { batch_id: 'k', rows: [{ ...rowResult(1), deposit: { status: 'REPLAYED', payment_number: 'PAY000001', amount: '200000' } }], checked_in: 1, already_checked_in: 0, failed: 0, deposits_recorded: 0 } })
    GET = vi.fn(async (path: string) => (path.endsWith('/arrivals') ? { data: { data: [] } } : { data: routing() }))
    await w.get('[data-testid=group-checkin-run]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid=group-checkin-empty]').exists()).toBe(true)
    expect(w.get('[data-testid=group-checkin-done]').text()).toBe('Room 101 checked in: STY000001 · Deposit PAY000001: 200,000 (taken by an earlier request, not taken again)')
  })

  it('shows the deposit column only where it can be taken, with the amount by the rule of each room, and sends it with the row', async () => {
    const w = mountCard()
    await flushPromises()
    // the room goes to the master (ROOM): the incidental deposit alone; the room that is left out (3) follows the panel: estimate and incidental
    expect((w.get('[data-testid=deposit-1]').element as HTMLInputElement).value).toBe('200000')
    expect((w.get('[data-testid=deposit-3]').element as HTMLInputElement).value).toBe('1200000')
    await w.get('[data-testid=select-ready]').trigger('click')
    POST.mockResolvedValueOnce({ data: { batch_id: 'k', rows: [{ ...rowResult(1), deposit: { status: 'RECORDED', payment_number: 'PAY000001', amount: '200000' } }, rowResult(3)], checked_in: 2, already_checked_in: 0, failed: 0, deposits_recorded: 1 } })
    await w.get('[data-testid=group-checkin-run]').trigger('click')
    await flushPromises()
    const rows = POST.mock.calls[0]![1].body.rows
    expect(rows[0].deposit).toEqual({ amount: '200000', payment_method: 'BANK_TRANSFER' })
    expect(rows[1].deposit).toEqual({ amount: '1200000', payment_method: 'BANK_TRANSFER' })
    expect(w.get('[data-testid=deposit-note-1]').text()).toBe('Deposit PAY000001: 200,000')
    // everything goes to the master: no deposit asked, and no column
    const all = mountCard(ALL, [arrival(1)], routing({ instructions: [{ scope: 'ALL', charge_code_id: null }] }))
    await flushPromises()
    expect(all.find('[data-testid=deposit-1]').exists()).toBe(false)
    expect(all.find('[data-testid=group-checkin-method]').exists()).toBe(false)
    // without payment.post: no column, and no deposit in the request
    const clerk = mountCard(['reservation.read', 'frontdesk.checkin'])
    await flushPromises()
    expect(clerk.find('[data-testid=deposit-1]').exists()).toBe(false)
    await clerk.get('[data-testid=pick-1]').setValue(true)
    await clerk.get('[data-testid=group-checkin-run]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls.at(-1)?.[1].body.rows[0].deposit).toBeUndefined()
  })

  it('reads the rooms again on request, and a room that failed keeps its tick when it is ready again', async () => {
    const w = mountCard()
    await flushPromises()
    await w.get('[data-testid=select-ready]').trigger('click')
    POST.mockResolvedValueOnce({ data: { batch_id: 'k', rows: [rowResult(1), { line_id: 3, status: 'FAILED', code: 'ROOM_NOT_READY', message: 'the room is not ready' }], checked_in: 1, already_checked_in: 0, failed: 1, deposits_recorded: 0 } })
    // after the run the list says the room of line 3 is not ready; the person has it cleaned and reads the rooms again
    GET = vi.fn(async (path: string) => (path.endsWith('/arrivals') ? { data: { data: [arrival(2, { readiness: { status: 'BLOCKED', blockers: ['ROOM_NOT_READY'] } }), arrival(3, { readiness: { status: 'BLOCKED', blockers: ['ROOM_NOT_READY'] } })] } } : { data: routing() }))
    await w.get('[data-testid=group-checkin-run]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=group-checkin-run]').attributes('disabled')).toBeDefined() // line 3 cannot be checked in while it is not ready
    GET = vi.fn(async (path: string) => (path.endsWith('/arrivals') ? { data: { data: [arrival(2, { readiness: { status: 'BLOCKED', blockers: ['ROOM_NOT_READY'] } }), arrival(3)] } } : { data: routing() }))
    await w.get('[data-testid=group-checkin-reload]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid=pick-3]').element).toHaveProperty('checked', true)
    expect(w.get('[data-testid=group-checkin-run]').attributes('disabled')).toBeUndefined()
  })

  it('checks in a room that is not ready only with the override and a reason', async () => {
    const w = mountCard()
    await flushPromises()
    await w.get('[name=override_not_ready]').setValue(true)
    expect(w.get('[data-testid=pick-2]').attributes('disabled')).toBeUndefined()
    await w.get('[data-testid=pick-2]').setValue(true)
    expect(w.get('[data-testid=group-checkin-run]').attributes('disabled')).toBeDefined() // the reason is missing
    expect(w.get('[data-testid=group-checkin-progress]').text()).toBe('Say why the rooms that are not ready are checked in.')
    await w.get('[name=override_reason]').setValue('the guests wait in the lobby')
    await w.get('[data-testid=group-checkin-run]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]![1].body.rows[0]).toMatchObject({ line_id: 2, override_room_not_ready: true, override_reason: 'the guests wait in the lobby' })
    // without the right to override there is no checkbox at all
    const clerk = mountCard(['reservation.read', 'frontdesk.checkin'])
    await flushPromises()
    expect(clerk.find('[name=override_not_ready]').exists()).toBe(false)
  })

  it('says what the group payment is: the routing, the master and its balance, and records a group deposit on the master', async () => {
    const w = mountCard()
    await flushPromises()
    expect(w.get('[data-testid=group-pay-routing]').text()).toBe('Goes to the master folio: room charges.')
    expect(w.get('[data-testid=group-pay-master]').text()).toContain('FOL000030')
    expect(w.get('[data-testid=group-pay-master]').text()).toContain('2,000,000')
    await w.get('[data-testid=group-deposit-open]').trigger('click')
    const amount = document.body.querySelector('[name=group_deposit_amount]') as HTMLInputElement
    amount.value = '5000000'
    amount.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    POST.mockResolvedValueOnce({ data: {} })
    await body('[data-testid=group-deposit-save]').trigger('click')
    await flushPromises()
    const call = POST.mock.calls.at(-1)
    expect(call?.[0]).toBe('/api/v1/properties/{propertyId}/folios/{id}/payments')
    expect(call?.[1].params.path).toEqual({ propertyId: 7, id: 30 })
    expect(call?.[1].body).toMatchObject({ amount: '5000000', payment_method: 'BANK_TRANSFER' })
    expect(w.get('[data-testid=group-checkin-notice]').text()).toBe('The group deposit is recorded on FOL000030.')
    // no master: the card says so and offers no deposit
    const bare = mountCard(ALL, [arrival(1)], routing({ master: null, instructions: [] }))
    await flushPromises()
    expect(bare.find('[data-testid=group-pay-nomaster]').exists()).toBe(true)
    expect(bare.find('[data-testid=group-deposit-open]').exists()).toBe(false)
  })

  it('says so when no room of the group arrives today, and is not shown to a person who may not check in', async () => {
    const w = mountCard(ALL, [])
    await flushPromises()
    expect(w.get('[data-testid=group-checkin-empty]').text()).toBe('No room of this group arrives today.')
    const none = mountCard(['reservation.read'])
    await flushPromises()
    expect(none.find('[data-testid=group-checkin]').exists()).toBe(false)
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    const w = mountCard()
    await flushPromises()
    expect(w.get('[data-testid=group-checkin]').text()).toContain('Check-in grup')
    expect(w.get('[data-testid=group-pay-routing]').text()).toBe('Masuk master folio: biaya kamar.')
  })
})
