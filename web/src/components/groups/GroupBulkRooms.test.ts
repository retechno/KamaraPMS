import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import GroupBulkRooms from './GroupBulkRooms.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const group = { id: 4, code: 'CONF', name: 'Conference', arrival_date: '2026-10-01', departure_date: '2026-10-10', is_active: true, company_id: null, contact_guest_id: null, reservation_count: 0, cancelled_reservation_count: 0, room_count: 0, created_at: '' }
const money = (total: string) => ({ net: total, service: '0', tax: '0', total })

const previewOf = (over: object = {}, rowOver: object = {}) => ({
  rows: [{
    row: 0, room_type_id: 1, room_type_code: 'DLX', rooms: 2, nights: 2, nightly: [], uniform: true, grid_rate: '1000000.00', rate_per_night: '1000000.00', override: false,
    room_estimate: money('2200000'), row_estimate: money('4400000'), problems: [], ...rowOver,
  }],
  availability: [{ room_type_id: 1, code: 'DLX', nights: [{ date: '2026-10-02', available: 5, needed: 2, left: 3 }, { date: '2026-10-03', available: 5, needed: 2, left: 3 }] }],
  rooms: 2, nights: 4, total: money('4400000'), needs_approval: false, ok: true, ...over,
})

const mounted: VueWrapper[] = []
function mountForm(permissions = ['reservation.create', 'reservation.override_rate_approve']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'clerk@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-09-30', property_local_time: '2026-09-30T10:00:00+07:00', server_time: '', timezone: 'Asia/Jakarta', night_audit_allowed: false, night_audit_overdue: false } as never
  const w = mount(GroupBulkRooms, { props: { group }, global: { plugins: [pinia] }, attachTo: document.body })
  mounted.push(w)
  return w
}

const typesPage = { data: { data: [{ id: 1, code: 'DLX', name: 'Deluxe', is_active: true }, { id: 2, code: 'STD', name: 'Standard', is_active: true }] } }
const plansPage = { data: { data: [{ id: 9, code: 'BAR', name: 'Best', is_active: true }] } }

async function settle(): Promise<void> {
  await flushPromises()
  await vi.advanceTimersByTimeAsync(450)
  await flushPromises()
}

describe('GroupBulkRooms', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    setLocale('en')
    GET = vi.fn(async (path: string) => (path.endsWith('/room-types') ? typesPage : plansPage))
    POST = vi.fn(async (path: string) => (path.endsWith('/preview') ? { data: previewOf() } : { data: { reservations: [{}, {}], rooms: 2, nights: 4, replayed: false } }))
  })
  afterEach(() => {
    vi.useRealTimers()
    mounted.splice(0).forEach((w) => w.unmount())
    document.body.innerHTML = ''
  })

  it('starts with one row on the dates of the group, never before the business date, and previews it', async () => {
    const w = mountForm()
    await settle()
    expect((w.get('[name=arrival_0]').element as HTMLInputElement).value).toBe('2026-10-01')
    expect((w.get('[name=departure_0]').element as HTMLInputElement).value).toBe('2026-10-10')
    expect((w.get('[name=type_0]').element as HTMLSelectElement).value).toBe('1')
    const call = POST.mock.calls.find((c) => String(c[0]).endsWith('/preview'))
    expect(call?.[1].params.path).toEqual({ propertyId: 7, id: 4 })
    expect(call?.[1].body).toEqual({ rows: [{ room_type_id: 1, rate_plan_id: 9, count: 1, arrival_date: '2026-10-01', departure_date: '2026-10-10', adult_count: 2, child_count: 0, rate_per_night: undefined }], confirm: true })
    expect(w.get('[data-testid=bulk-totals]').text()).toContain('2 rooms, 4 room nights')
    expect(w.find("[data-testid=bulk-ok]").exists()).toBe(true)
    expect(w.get('[data-testid=bulk-night-DLX-2026-10-02]').text()).toContain('3 left')
    expect(w.get('[data-testid=rate-hint]').text()).toContain('Price in force')
  })

  it('lists the nights earliest first whatever the order of the answer', async () => {
    POST = vi.fn(async () => ({
      data: previewOf({ availability: [{ room_type_id: 1, code: 'DLX', nights: [{ date: '2026-10-17', available: 5, needed: 1, left: 4 }, { date: '2026-10-14', available: 5, needed: 1, left: 4 }, { date: '2026-10-16', available: 5, needed: 1, left: 4 }, { date: '2026-10-15', available: 5, needed: 1, left: 4 }] }] }),
    }))
    const w = mountForm()
    await settle()
    const order = w.findAll('[data-testid^=bulk-night-DLX-]').map((n) => n.attributes('data-testid')?.replace('bulk-night-DLX-', ''))
    expect(order).toEqual(['2026-10-14', '2026-10-15', '2026-10-16', '2026-10-17'])
  })

  it('says the price of a row in words: one price, or the total of a room and the range per night', async () => {
    const w = mountForm()
    await settle()
    expect(w.get('[data-testid=bulk-price-0]').text()).toBe('1,000,000.00 per night')
  })

  it('shows the range when the nights differ, then the total of the row', async () => {
    POST = vi.fn(async () => ({
      data: previewOf({}, { uniform: false, grid_rate: undefined, rate_per_night: '', nights: 4, nightly: [{ date: 'a', amount: '500000.00' }, { date: 'b', amount: '600000.00' }, { date: 'c', amount: '500000.00' }, { date: 'd', amount: '600000.00' }], room_estimate: money('2200000'), row_estimate: money('2200000'), rooms: 1 }),
    }))
    const w = mountForm()
    await settle()
    expect(w.get('[data-testid=bulk-price-0]').text()).toBe('2,200,000 per room for 4 nights (500,000–600,000 per night)')
    expect(w.get('[data-testid=bulk-rowtotal-0]').text()).toBe('Row total: 2,200,000')
  })

  it('gives the reason the save is off while a row is short or the preview is not done', async () => {
    POST = vi.fn(async () => ({ data: previewOf({ ok: false }, { short: { date: '2026-10-02', missing: 1 }, problems: [{ code: 'ROOM_TYPE_NOT_AVAILABLE', message: 'short' }] }) }))
    const w = mountForm()
    await flushPromises()
    await w.get('[name=count_0]').setValue('2')
    await flushPromises()
    expect(w.get('[data-testid=bulk-blocker]').text()).toBe('Checking the rooms…') // the preview is not made yet
    expect(w.get('[data-testid=bulk-save]').attributes('disabled')).toBeDefined()
    await settle()
    expect(w.get('[data-testid=bulk-blocker]').text()).toBe('A row has too few rooms.')
    expect(w.get('[data-testid=bulk-save]').attributes('disabled')).toBeDefined()
  })

  it('marks the row that is short and does not let the request be saved', async () => {
    POST = vi.fn(async () => ({
      data: previewOf({ ok: false, availability: [{ room_type_id: 1, code: 'DLX', nights: [{ date: '2026-10-02', available: 1, needed: 2, left: -1 }] }] },
        { short: { date: '2026-10-02', missing: 1 }, problems: [{ code: 'ROOM_TYPE_NOT_AVAILABLE', message: 'short' }] }),
    }))
    const w = mountForm()
    await settle()
    expect(w.get('[data-testid=bulk-short-0]').text()).toContain('Short by 1 room(s) from')
    expect(w.get('[data-testid=bulk-night-DLX-2026-10-02]').classes().join(' ')).toContain('text-destructive')
    expect(w.get('[data-testid=bulk-save]').attributes('disabled')).toBeDefined()
  })

  it('books once with an Idempotency-Key and starts again with a fresh row', async () => {
    const w = mountForm()
    await settle()
    await w.get('[name=count_0]').setValue('2')
    await settle()
    expect(w.get('[data-testid=bulk-save]').text()).toBe('Book 2 rooms')
    await w.get('[data-testid=bulk-save]').trigger('click')
    await flushPromises()
    const save = POST.mock.calls.find((c) => String(c[0]).endsWith('/room-batches'))
    expect(save?.[1].params.header['Idempotency-Key']).toBeTruthy()
    expect(save?.[1].body.rows[0].count).toBe(2)
    expect(save?.[1].body.rate_override_reason).toBeUndefined()
    expect(w.emitted('saved')).toHaveLength(1)
    expect(w.get('[data-testid=bulk-done]').text()).toContain('2 rooms booked in 2 reservations')
    expect((w.get('[name=count_0]').element as HTMLInputElement).value).toBe('1') // not the same rows again: that would book them twice
    // the fresh row is not previewed: the rooms just booked would make it look short, like a failure
    const previews = POST.mock.calls.filter((c) => String(c[0]).endsWith('/preview')).length
    await settle()
    expect(POST.mock.calls.filter((c) => String(c[0]).endsWith('/preview')).length).toBe(previews)
    expect(w.find('[data-testid^=bulk-short-]').exists()).toBe(false)
    expect(w.find('[data-testid=bulk-blocker]').exists()).toBe(false)
    expect(w.find('[data-testid=bulk-done]').exists()).toBe(true)
    // the first change of the fresh row clears the message and previews again
    await w.get('[name=count_0]').setValue('3')
    await settle()
    expect(w.find('[data-testid=bulk-done]').exists()).toBe(false)
  })

  it('keeps the same key when the answer was lost, and takes a new one when the server has answered', async () => {
    const w = mountForm()
    await settle()
    let n = 0
    POST = vi.fn(async (path: string) => {
      if (path.endsWith('/preview')) return { data: previewOf() }
      n++
      if (n === 1) throw new Error('network')
      if (n === 2) throw new ApiError({ code: 'ROOM_TYPE_NOT_AVAILABLE', title: 'The rooms ran out', status: 409, context: { row: 0, room_in_row: 1 } } as never)
      return { data: { reservations: [], rooms: 0, nights: 0, replayed: false } }
    })
    const keyOf = () => POST.mock.calls.filter((c) => String(c[0]).endsWith('/room-batches')).map((c) => c[1].params.header['Idempotency-Key'])
    await w.get('[data-testid=bulk-save]').trigger('click')
    await flushPromises()
    await w.get('[data-testid=bulk-save]').trigger('click')
    await flushPromises()
    const [k1, k2] = keyOf()
    expect(k2).toBe(k1) // nothing came back: it is the same request
    expect(w.get('[data-testid=bulk-error]').text()).toContain('Row 1, room 1')
    await w.get('[data-testid=bulk-save]').trigger('click')
    await flushPromises()
    expect(keyOf()[2]).not.toBe(k1) // the server answered the first one: a new request
  })

  it('needs one reason and one approval for the rates of the whole request', async () => {
    const w = mountForm(['reservation.create']) // cannot approve
    await settle()
    POST = vi.fn(async (path: string) => (path.endsWith('/preview') ? { data: previewOf({ needs_approval: true }, { override: true }) } : { data: { reservations: [], rooms: 2, nights: 4, replayed: false } }))
    await w.get('[name=rate_0]').setValue('700000')
    await settle()
    expect(POST.mock.calls.find((c) => String(c[0]).endsWith('/preview'))?.[1].body.rows[0].rate_per_night).toBe('700000')
    expect(w.get('[data-testid=bulk-save]').attributes('disabled')).toBeDefined() // no reason yet
    await w.get('[name=rate_override_reason]').setValue('Group rate of the contract')
    await w.get('[data-testid=bulk-save]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls.some((c) => String(c[0]).endsWith('/room-batches'))).toBe(false) // the approval dialog comes first
    const email = document.body.querySelector('input[name=approval_email]') as HTMLInputElement
    const password = document.body.querySelector('input[name=approval_password]') as HTMLInputElement
    email.value = 'boss@hotel.test'
    email.dispatchEvent(new Event('input'))
    password.value = 'secret'
    password.dispatchEvent(new Event('input'))
    await flushPromises()
    ;(document.body.querySelector('[data-testid=approval-dialog]') as HTMLFormElement).dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    const saves = POST.mock.calls.filter((c) => String(c[0]).endsWith('/room-batches'))
    expect(saves).toHaveLength(1) // one request, one approval, whatever the number of rows
    expect(saves[0]?.[1].body.rate_override_reason).toBe('Group rate of the contract')
    expect(saves[0]?.[1].body.rate_override_approval).toEqual({ email: 'boss@hotel.test', password: 'secret' })
  })

  it('does not ask a person who can approve for credentials', async () => {
    const w = mountForm()
    await settle()
    POST = vi.fn(async (path: string) => (path.endsWith('/preview') ? { data: previewOf({}, { override: true }) } : { data: { reservations: [], rooms: 2, nights: 4, replayed: false } }))
    await w.get('[name=rate_0]').setValue('700000')
    await settle()
    await w.get('[name=rate_override_reason]').setValue('Contract')
    await w.get('[data-testid=bulk-save]').trigger('click')
    await flushPromises()
    expect(document.body.querySelector('[data-testid=approval-dialog]')).toBeNull()
    expect(POST.mock.calls.some((c) => String(c[0]).endsWith('/room-batches'))).toBe(true)
  })

  it('adds, copies and removes rows, and refuses more than the limits', async () => {
    const w = mountForm()
    await settle()
    await w.get('[data-testid=bulk-add-row]').trigger('click')
    await w.get('[data-testid=bulk-copy-0]').trigger('click')
    expect(w.findAll('[data-testid^=bulk-row-]').filter((x) => /^bulk-row-\d+$/.test(x.attributes('data-testid') ?? ''))).toHaveLength(3)
    await w.get('[data-testid=bulk-remove-2]').trigger('click')
    expect(w.find('[data-testid=bulk-row-2]').exists()).toBe(false)
    await w.get('[name=count_0]').setValue('101')
    await settle()
    expect(w.get('[data-testid=bulk-limit]').text()).toContain('At most 100 rooms and 20 rows')
    expect(w.get('[data-testid=bulk-save]').attributes('disabled')).toBeDefined()
  })

  it('says it in Indonesian', async () => {
    setLocale('id')
    const w = mountForm()
    await settle()
    expect(w.text()).toContain('Tambah kamar massal')
    expect(w.get('[data-testid=bulk-save]').text()).toBe('Pesan 1 kamar')
    setLocale('en')
  })
})

describe('GroupBulkRooms, what is seen is what is sent', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    setLocale('en')
    GET = vi.fn(async (path: string) => (path.endsWith('/room-types') ? typesPage : plansPage))
    POST = vi.fn(async (path: string) => (path.endsWith('/preview') ? { data: previewOf() } : { data: { reservations: [{}, {}], rooms: 2, nights: 4, replayed: false } }))
  })
  afterEach(() => {
    vi.useRealTimers()
    mounted.splice(0).forEach((w) => w.unmount())
    document.body.innerHTML = ''
  })

  it('does not book while a date of a row is half typed, and says why', async () => {
    const w = mountForm()
    await settle()
    await w.get('[name=count_0]').setValue('2')
    await settle()
    expect(w.get('[data-testid=bulk-save]').attributes('disabled')).toBeUndefined()
    await w.get('input[data-slot=date-input-text]').setValue('3/2')
    await w.get('[data-testid=bulk-save]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls.some((c) => String(c[0]).endsWith('/room-batches'))).toBe(false)
    expect(w.text()).toContain('Write as dd/mm/yyyy')
  })
})
