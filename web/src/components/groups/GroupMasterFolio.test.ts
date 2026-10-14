import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import GroupMasterFolio from './GroupMasterFolio.vue'

let GET = vi.fn()
let POST = vi.fn()
let PUT = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PUT: (...a: unknown[]) => PUT(...a) } }))

const body = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)
const has = (sel: string) => document.body.querySelector(sel) !== null

const master = (over: object = {}) => ({ id: 30, folio_number: 'FOL000030', status: 'OPEN', balance: '2000000', version: 3, bill_to_company_id: 8, bill_to_company_name: 'Acme Corp', opened_at: '2026-10-01T00:00:00Z', closed_at: null, ...over })
const line = (id: number, number: string, over: object = {}) => ({ reservation_room_id: id, reservation_id: 40 + id, confirmation_number: number, status: 'CONFIRMED', excluded: false, ...over })
const routing = (over: object = {}) => ({ group_id: 4, master: master(), instructions: [{ scope: 'ROOM', charge_code_id: null }], lines: [line(1, 'RES000001'), line(2, 'RES000002', { excluded: true })], ...over })
const codes = [
  { id: 2, code: 'MINIBAR', name: 'Minibar', charge_type: 'FOOD_BEVERAGE', is_active: true },
  { id: 3, code: 'ROOM', name: 'Room', charge_type: 'ROOM', is_active: true },
  { id: 9, code: 'TRF', name: 'Transfer balance', charge_type: 'TRANSFER', is_active: true },
]
const posted = [
  { folio_id: 11, folio_number: 'FOL000011', reservation_id: 41, confirmation_number: 'RES000001', charge_code_id: 3, charge_code: 'ROOM', charge_code_name: 'Room', count: 2, total: '1800000' },
  { folio_id: 12, folio_number: 'FOL000012', reservation_id: 43, confirmation_number: 'RES000003', charge_code_id: 3, charge_code: 'ROOM', charge_code_name: 'Room', count: 1, total: '900000' },
]

const ALL = ['reservation.read', 'folio.read', 'folio.reverse', 'folio.master_manage', 'folio.master_close']
const mounted: VueWrapper[] = []
function mountPanel(permissions = ALL, data: object = routing(), movable: object[] = [], allCancelled = false) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'boss@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/routing/posted')) return { data: { data: movable } }
    if (path.endsWith('/charge-codes')) return { data: { data: codes } }
    return { data }
  })
  PUT = vi.fn(async () => ({ data: routing() }))
  POST = vi.fn(async () => ({ data: { master: master(), created: true } }))
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  const w = mount(GroupMasterFolio, { props: { groupId: 4, active: true, allCancelled }, global: { plugins: [pinia, router] }, attachTo: document.body })
  mounted.push(w)
  return w
}
async function type(sel: string, value: string): Promise<void> {
  const el = document.body.querySelector(sel) as HTMLInputElement
  el.value = value
  el.dispatchEvent(new Event('input', { bubbles: true }))
  await flushPromises()
}

describe('GroupMasterFolio', () => {
  beforeEach(() => setLocale('en'))
  afterEach(() => {
    mounted.splice(0).forEach((w) => w.unmount())
    document.body.innerHTML = ''
    vi.unstubAllGlobals()
  })

  it('shows the master folio with its number as a link, its balance and who it is billed to', async () => {
    const w = mountPanel()
    await flushPromises()
    expect(w.get('[data-testid=master-link]').text()).toBe('FOL000030')
    expect(w.get('[data-testid=master-link]').attributes('href')).toBe('/folios/30')
    expect(w.get('[data-testid=master-status]').text()).toBe('Open')
    expect(w.get('[data-testid=master-balance]').text()).toContain('2,000,000')
    expect(w.get('[data-testid=master-company]').text()).toBe('Billed to Acme Corp')
    expect(GET.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/groups/{id}/routing')
  })

  it('opens the master folio of a group that has none, for who may manage it', async () => {
    const w = mountPanel(ALL, routing({ master: null, instructions: [], lines: [] }))
    await flushPromises()
    expect(w.get('[data-testid=master-none]').text()).toBe('The group has no master folio yet.')
    await w.get('[data-testid=master-open]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/groups/{id}/master-folio')
    expect(w.get('[data-testid=master-notice]').text()).toBe('The master folio FOL000030 is open.')
    const reader = mountPanel(['reservation.read'], routing({ master: null, instructions: [], lines: [] }))
    await flushPromises()
    expect(reader.find('[data-testid=master-open]').exists()).toBe(false)
    expect(reader.find('[data-testid=routing-save]').exists()).toBe(false)
  })

  it('saves the instructions and the rooms that stay out, and applies nothing until it is asked', async () => {
    const w = mountPanel()
    await flushPromises()
    expect((w.get('input[name=route_room]').element as HTMLInputElement).checked).toBe(true)
    expect((w.get('input[name=exclude_2]').element as HTMLInputElement).checked).toBe(true)
    expect(w.get('[data-testid=routing-save]').attributes('disabled')).toBeDefined() // nothing changed
    await w.get('input[name=route_all]').setValue(true)
    await w.get('input[name=exclude_1]').setValue(true)
    await w.get('input[name=exclude_2]').setValue(false)
    await w.get('[data-testid=route-code-add]').setValue('2')
    await w.get('[data-testid=route-code-button]').trigger('click')
    expect(w.find('[data-testid=route-code-2]').exists()).toBe(true)
    // the room and the transfer code are not among the choices
    const options = w.findAll('[data-testid=route-code-add] option').map((o) => o.text())
    expect(options.join('|')).not.toContain('TRF')
    expect(options.join('|')).not.toContain('ROOM')
    expect(w.get('[data-testid=routing-save]').attributes('disabled')).toBeUndefined()
    await w.get('[data-testid=routing-save]').trigger('click')
    await flushPromises()
    expect(PUT.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/groups/{id}/routing')
    expect(PUT.mock.calls[0]?.[1].body).toEqual({
      instructions: [{ scope: 'ROOM' }, { scope: 'ALL' }, { scope: 'CHARGE_CODE', charge_code_id: 2 }],
      excluded_lines: [1],
    })
    expect(w.get('[data-testid=master-notice]').text()).toContain('applies from the next night not yet posted')
  })

  it('closes the master folio after the question, and names the rooms that are not over', async () => {
    const w = mountPanel(ALL, routing({ master: master({ balance: '0' }) }))
    await flushPromises()
    vi.stubGlobal('confirm', vi.fn(() => true))
    POST = vi.fn().mockRejectedValue(new ApiError({ code: 'MASTER_FOLIO_GROUP_ACTIVE', title: 'active', status: 409, context: { lines: [{ reservation_room_id: 1, confirmation_number: 'RES000001', status: 'IN_HOUSE' }], lines_total: 1 } } as never))
    await w.get('[data-testid=master-close]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/folios/{id}/close')
    expect(POST.mock.calls[0]?.[1].body).toEqual({ version: 3 })
    expect(w.get('[data-testid=master-active-lines]').text()).toContain('RES000001 · In house')
    // the right is its own: a reader of folios without it sees no button, and a closed master has none
    const reader = mountPanel(['reservation.read', 'folio.read', 'folio.master_manage'])
    await flushPromises()
    expect(reader.find('[data-testid=master-close]').exists()).toBe(false)
    const closed = mountPanel(ALL, routing({ master: master({ status: 'CLOSED', closed_at: '2026-10-05T00:00:00Z' }) }))
    await flushPromises()
    expect(closed.find('[data-testid=master-close]').exists()).toBe(false)
    expect(closed.get('[data-testid=master-status]').text()).toBe('Closed')
  })

  it('does not offer to close a master that owes, and says why', async () => {
    const w = mountPanel(ALL, routing({ master: master({ balance: '2000000' }) }))
    await flushPromises()
    expect(w.get('[data-testid=master-close]').attributes('disabled')).toBeDefined()
    expect(w.get('[data-testid=master-close-hint]').text()).toBe('The balance of the master folio (2,000,000) must be zero before it closes: have it paid, or move it to the city ledger of the company.')
    // a credit is refunded or moved, not paid
    const credit = mountPanel(ALL, routing({ master: master({ balance: '-2300000' }) }))
    await flushPromises()
    expect(credit.get('[data-testid=master-close-hint]').text()).toBe('The balance of the master folio (-2,300,000) is a credit: refund it to whoever paid, or move it to another folio, before it closes.')
    const paid = mountPanel(ALL, routing({ master: master({ balance: '0' }) }))
    await flushPromises()
    expect(paid.get('[data-testid=master-close]').attributes('disabled')).toBeUndefined()
    expect(paid.find('[data-testid=master-close-hint]').exists()).toBe(false)
  })

  it('says, when every room of the group is cancelled, that the master still holds a deposit and what to do with it', async () => {
    const w = mountPanel(ALL, routing({ master: master({ balance: '-500000' }) }), [], true)
    await flushPromises()
    expect(w.get('[data-testid=master-cancelled-hint]').text()).toBe('Every room of this group is cancelled, and the master holds a credit of 500,000. Refund it to whoever paid (on the folio of the master), then close it.')
    const settled = mountPanel(ALL, routing({ master: master({ balance: '0' }) }), [], true)
    await flushPromises()
    expect(settled.find('[data-testid=master-cancelled-hint]').exists()).toBe(false)
    const live = mountPanel(ALL, routing({ master: master({ balance: '-500000' }) }))
    await flushPromises()
    expect(live.find('[data-testid=master-cancelled-hint]').exists()).toBe(false) // a group with rooms left is not told it is cancelled
  })

  it('offers to move what is already posted, in one request with one key, one reason and one approval', async () => {
    const w = mountPanel(ALL, routing(), posted)
    await flushPromises()
    expect(w.get('[data-testid=posted-text]').text()).toBe('3 charge(s) already posted on 2 folio(s) of the members match these instructions, but they stay on those folios until they are moved.')
    await w.get('[data-testid=posted-move]').trigger('click')
    await flushPromises()
    expect(body('[data-testid=move-posted-total]').text()).toBe('3 charge(s), 2,700,000')
    expect(body('[data-testid=move-posted-missing]').text()).toBe('Still needed: a reason.')
    expect(body('[data-testid=move-posted-continue]').attributes('disabled')).toBeDefined()
    await type('input[name=move_reason]', 'the group pays the rooms')
    expect(body('[data-testid=move-posted-continue]').attributes('disabled')).toBeUndefined()
    ;(document.body.querySelector('[data-testid=move-posted-form]') as HTMLFormElement).dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(POST).not.toHaveBeenCalled() // the approval comes first
    POST = vi.fn(async () => ({ data: { batches: [], moved: 3, total: '2700000', replayed: false } }))
    await type('input[name=approval_email]', 'boss@hotel.test')
    await type('input[name=approval_password]', 'secret')
    ;(document.body.querySelector('[data-testid=approval-dialog]') as HTMLFormElement).dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(POST).toHaveBeenCalledTimes(1) // one request for all the folios, not one for each
    const call = POST.mock.calls[0]
    expect(call?.[0]).toBe('/api/v1/properties/{propertyId}/groups/{id}/routing/move-posted')
    expect(call?.[1].params.path).toEqual({ propertyId: 7, id: 4 })
    expect(typeof call?.[1].params.header['Idempotency-Key']).toBe('string')
    // what was shown is what is approved: the count and the total of the list
    expect(call?.[1].body).toEqual({ reason: 'the group pays the rooms', approval: { email: 'boss@hotel.test', password: 'secret' }, expected_count: 3, expected_total: '2700000' })
    expect(w.get('[data-testid=master-notice]').text()).toBe('3 charge(s) moved to FOL000030.')
    expect(has('[data-testid=move-posted-dialog]')).toBe(false)
  })

  it('keeps the key of the request when the answer is lost, so a repeat that went through moves nothing twice', async () => {
    const w = mountPanel(ALL, routing(), posted)
    await flushPromises()
    await w.get('[data-testid=posted-move]').trigger('click')
    await flushPromises()
    await type('input[name=move_reason]', 'x')
    ;(document.body.querySelector('[data-testid=move-posted-form]') as HTMLFormElement).dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    POST = vi.fn().mockRejectedValueOnce(new ApiError({ code: 'INTERNAL', title: 'lost', status: 500 } as never)).mockResolvedValueOnce({ data: { batches: [], moved: 3, total: '2700000', replayed: true } })
    const submit = async () => {
      await type('input[name=approval_email]', 'boss@hotel.test')
      await type('input[name=approval_password]', 'secret')
      ;(document.body.querySelector('[data-testid=approval-dialog]') as HTMLFormElement).dispatchEvent(new Event('submit', { cancelable: true }))
      await flushPromises()
    }
    await submit()
    expect(has('[data-testid=approval-error]') || has('[data-testid=approval-dialog]')).toBe(true) // the dialog stays: nothing is lost
    await submit()
    const keys = POST.mock.calls.map((c) => c[1].params.header['Idempotency-Key'])
    expect(keys).toHaveLength(2)
    expect(keys[0]).toBe(keys[1])
    expect(w.get('[data-testid=master-notice]').text()).toBe('3 charge(s) moved to FOL000030.')
  })

  it('moves nothing and reads the list again when what is posted changed since it was shown', async () => {
    const w = mountPanel(ALL, routing(), posted)
    await flushPromises()
    await w.get('[data-testid=posted-move]').trigger('click')
    await flushPromises()
    await type('input[name=move_reason]', 'x')
    ;(document.body.querySelector('[data-testid=move-posted-form]') as HTMLFormElement).dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    POST = vi.fn().mockRejectedValue(new ApiError({ code: 'POSTED_LIST_CHANGED', title: 'changed', status: 409 } as never))
    const reads = GET.mock.calls.length
    await type('input[name=approval_email]', 'boss@hotel.test')
    await type('input[name=approval_password]', 'secret')
    ;(document.body.querySelector('[data-testid=approval-dialog]') as HTMLFormElement).dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(w.get('[data-testid=master-notice]').text()).toBe('What is posted changed since the list was shown: nothing was moved. Look at the list again and ask again.')
    expect(w.get('[data-testid=master-notice]').classes()).toContain('warning') // nothing was done: a warning, not a confirmation
    expect(has('[data-testid=move-posted-dialog]')).toBe(false)
    expect(GET.mock.calls.length).toBeGreaterThan(reads) // the list is read again
  })

  it('is in Indonesian too', async () => {
    setLocale('id')
    const w = mountPanel()
    await flushPromises()
    expect(w.get('[data-testid=master-company]').text()).toBe('Ditagihkan ke Acme Corp')
    expect(w.get('[data-testid=master-status]').text()).toBe('Terbuka')
    expect(w.get('[data-testid=routing-save]').text()).toBe('Simpan pengaturan')
  })
})
