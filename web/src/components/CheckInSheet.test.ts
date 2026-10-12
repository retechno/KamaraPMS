import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { toastText } from '@/test/toasts'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import CheckInSheet from './CheckInSheet.vue'

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))
const openPdf = vi.fn()
vi.mock('@/utils/documents', async (orig) => ({ ...(await orig<typeof import('@/utils/documents')>()), openPdf: (...a: unknown[]) => openPdf(...a) }))

const arrival = (over: object = {}) => ({
  reservation_id: 9, confirmation_number: 'RES000009', reservation_room_id: 4, reservation_version: 2, guest_id: 3, guest_name: 'Daniel Hartono',
  room_type_id: 10, room_type_code: 'SUP', room_type_name: 'Superior', room_id: 22, room_number: '102', housekeeping_status: 'CLEAN', arrival_date: '2026-10-10', departure_date: '2026-10-13',
  adult_count: 2, child_count: 0, status: 'CONFIRMED', reservation_status: 'CONFIRMED',
  rate: { rate_plan_code: 'BAR', rate_plan_name: 'Best', amount: '780000', price_mode: 'EXCLUSIVE' }, company: null, deposit: null,
  readiness: { status: 'READY', blockers: [] }, ...over,
})
const guest = (over: object = {}) => ({ id: 3, code: 'GST000003', first_name: 'Daniel', last_name: 'Hartono', phone: '+62 812 3456 7890', email: 'daniel@mail.test', nationality: 'ID', id_type: 'KTP', id_number: '3171012345670001', can_edit: true, ...over })
const types = [{ id: 10, code: 'SUP', is_active: true }, { id: 11, code: 'DLX', is_active: true }]
const checkInResult = { stay: { id: 55, stay_number: 'STY000055' }, stay_room: { room_number: '102' }, folio: { folio_number: 'FOL000412' } }

interface Setup {
  permissions?: string[]
  arrival?: object
  queue?: object[]
  guestRow?: object | null
  rooms?: object[]
  incidental?: string
  estimate?: string | null
  history?: object[]
}
const mounted: VueWrapper[] = []

function setup(o: Setup = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: o.permissions ?? ['reservation.read', 'reservation.update', 'frontdesk.checkin', 'payment.post', 'guest.write'] }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-10-10' } as never
  property.current = { require_room_inspection_for_checkin: false, currency_decimals: 0, checkin_incidental_deposit: o.incidental ?? '0' } as never
  const rooms = o.rooms ?? [{ room_id: 22, room_number: '102', housekeeping_status: 'CLEAN' }]
  GET = vi.fn(async (path: string) => {
    if (path === '/api/v1/guests/{id}') return o.guestRow === null ? { data: undefined } : { data: o.guestRow ?? guest() }
    if (path === '/api/v1/guests/{id}/history') return { data: { data: o.history ?? [{ type: 'STAY', id: 1, number: 'STY1', role: 'PRIMARY', status: 'COMPLETED', property_id: 7, property_code: 'BALI', property_name: 'Hotel Bali', arrival_date: '2026-08-01', departure_date: '2026-08-03' }, { type: 'STAY', id: 2, number: 'STY2', role: 'PRIMARY', status: 'COMPLETED', property_id: 7, property_code: 'BALI', property_name: 'Hotel Bali', arrival_date: '2026-06-01', departure_date: '2026-06-02' }], hidden_count: 0 } }
    if (path.endsWith('/room-types')) return { data: { data: types } }
    if (path.endsWith('/availability/rooms')) return { data: { data: rooms } }
    if (path.endsWith('/reservations/{id}')) return o.estimate === null ? { data: { rooms: [] } } : { data: { rooms: [{ id: 4, estimate: { net: '2000000', service: '0', tax: '340000', total: o.estimate ?? '2340000' } }] } }
    if (path.endsWith('/emails')) return { data: { enabled: true, data: [] } }
    if (path === '/api/v1/guests') return { data: { data: [guest({ id: 8, first_name: 'Siti', last_name: 'Nur', code: 'GST000008' })] } }
    return { data: {} }
  })
  POST = vi.fn(async (path: string) => {
    if (path.endsWith('/deposits')) return { data: { payment: { id: 91, payment_number: 'PAY000091', amount: '2840000' }, folio_item: {}, folio_balance: '-2840000' } }
    if (path.endsWith('/check-in')) return { data: checkInResult }
    if (path === '/api/v1/guests') return { data: { id: 12, code: 'GST000012', last_name: 'Baru', possible_duplicates: [], hidden_duplicate_count: 0 } }
    return { data: {} }
  })
  PATCH = vi.fn(async () => ({ data: guest({ phone: '+62 811' }) }))
  const w = mount(CheckInSheet, { attachTo: document.body, props: { arrival: arrival(o.arrival) as never, queue: (o.queue ?? []) as never }, global: { plugins: [pinia] } })
  mounted.push(w)
  return w
}

const el = (sel: string) => document.body.querySelector(sel)
const find = (sel: string) => {
  const e = el(sel)
  if (!e) throw new Error(`nothing in the sheet matches ${sel}`)
  return new DOMWrapper(e)
}
const has = (sel: string) => el(sel) !== null
const enter = async () => {
  await find('form[data-testid=checkin-4]').trigger('submit')
  await flushPromises()
}
const stepNow = () => el('[aria-current=step]')?.getAttribute('data-testid')
const callsTo = (fn: ReturnType<typeof vi.fn>, suffix: string) => fn.mock.calls.filter((c) => String(c[0]).endsWith(suffix))
const post = (suffix: string) => callsTo(POST, suffix)

beforeEach(() => {
  try {
    window.localStorage.clear()
  } catch {
    // no storage here
  }
  openPdf.mockReset()
})
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
})

describe('the check-in sheet: a returning guest', () => {
  it('goes through guest, room and deposit with Enter alone, then shows the end', async () => {
    setup({ incidental: '500000' })
    await flushPromises()
    expect(stepNow()).toBe('checkin-step-guest')
    // the profile is filled, with the line about the earlier stays
    expect((find('input[name=last_name]').element as HTMLInputElement).value).toBe('Hartono')
    expect((find('input[name=phone]').element as HTMLInputElement).value).toBe('+62 812 3456 7890')
    expect(find('[data-testid=guest-history]').text()).toContain('Stayed 2 times')
    expect(has('[data-testid=guest-hints]')).toBe(false) // the identity and the phone are there
    // nothing was written yet
    expect(POST).not.toHaveBeenCalled()
    expect(PATCH).not.toHaveBeenCalled()

    await enter() // → room, the room of the line is chosen
    expect(stepNow()).toBe('checkin-step-room')
    expect((find('select[name=room]').element as HTMLSelectElement).value).toBe('22')
    expect(POST).not.toHaveBeenCalled()

    await enter() // → deposit, proposed from the estimate of the server and the incidental of the property
    expect(stepNow()).toBe('checkin-step-deposit')
    expect(find('[data-testid=deposit-estimate]').text()).toContain('2,340,000')
    expect(find('[data-testid=deposit-estimate]').text()).toContain('3 nights')
    expect(find('[data-testid=deposit-estimate]').text()).toContain('as the system computes it')
    expect(find('[data-testid=deposit-incidental]').text()).toContain('500,000')
    expect((find('input[name=deposit_amount]').element as HTMLInputElement).value).toBe('2840000')
    expect(find('[data-testid=deposit-method-CASH]').attributes('aria-pressed')).toBe('true')
    expect(find('[data-testid=checkin-submit]').text()).toBe('Take deposit and check in')

    await enter() // the deposit, then the check-in
    expect(post('/deposits')).toHaveLength(1)
    expect(post('/check-in')).toHaveLength(1)
    const order = POST.mock.calls.map((c) => String(c[0]).split('/').pop())
    expect(order).toEqual(['deposits', 'check-in'])
    const [, dep] = post('/deposits')[0] as [string, { params: { path: { id: number }; header: Record<string, string> }; body: object }]
    expect(dep.params.header['Idempotency-Key']).toBeTruthy()
    expect(dep.body).toEqual({ amount: '2840000', payment_method: 'CASH', reference_number: undefined })
    const [, ci] = post('/check-in')[0] as [string, { params: { header: Record<string, string> }; body: object }]
    expect(ci.params.header['Idempotency-Key']).toBeTruthy()
    expect(ci.params.header['Idempotency-Key']).not.toBe(dep.params.header['Idempotency-Key'])
    expect(ci.body).toMatchObject({ version: 2, room_id: 22, guest_id: 3, adult_count: 2, child_count: 0, override_room_not_ready: false })

    expect(stepNow()).toBe('checkin-step-done')
    expect(find('[data-testid=done-title]').text()).toBe('Daniel Hartono is in room 102')
    expect(find('[data-testid=done-summary]').text()).toContain('STY000055')
    expect(find('[data-testid=done-summary]').text()).toContain('2,840,000')
    // nothing is typed or sent again by going on
    expect(has('[data-testid=checkin-next-arrival]')).toBe(false) // nobody else is ready
  })

  it('proposes only the estimate when the property sets no incidental, and less when the reservation holds a deposit already', async () => {
    setup({ arrival: arrival({ deposit: { folio_id: 80, paid: '1000000' } }) })
    await flushPromises()
    await enter()
    await enter()
    expect(find('[data-testid=deposit-held]').text()).toContain('1,000,000')
    expect((find('input[name=deposit_amount]').element as HTMLInputElement).value).toBe('1340000') // 2,340,000 less 1,000,000: figures of the server, no tax computed here
    expect(find('[data-testid=deposit-incidental]').text()).toContain('None set')
  })

  it('does not compute the stay when the estimate cannot be read: it says so and proposes the incidental alone', async () => {
    setup({ estimate: null, incidental: '500000' })
    await flushPromises()
    await enter()
    await enter()
    expect(find('[data-testid=deposit-estimate]').text()).toContain('could not be read')
    expect((find('input[name=deposit_amount]').element as HTMLInputElement).value).toBe('500000')
  })

  it('needs no deposit when what the reservation holds covers the stay', async () => {
    setup({ arrival: arrival({ deposit: { folio_id: 80, paid: '3000000' } }) })
    await flushPromises()
    await enter()
    await enter()
    expect(has('[data-testid=deposit-covered]')).toBe(true)
    expect((find('input[name=deposit_amount]').element as HTMLInputElement).value).toBe('')
    expect(find('[data-testid=checkin-submit]').attributes('disabled')).toBeUndefined()
    await enter()
    expect(post('/deposits')).toHaveLength(0)
    expect(post('/check-in')).toHaveLength(1)
  })
})

describe('the guest step', () => {
  it('saves what was corrected before the room step, in one request', async () => {
    setup()
    await flushPromises()
    await find('input[name=phone]').setValue('+62 811')
    await enter()
    expect(PATCH).toHaveBeenCalledTimes(1)
    const [path, init] = PATCH.mock.calls[0] as [string, { params: { path: { id: number } }; body: Record<string, string> }]
    expect(path).toBe('/api/v1/guests/{id}')
    expect(init.params.path.id).toBe(3)
    expect(init.body.phone).toBe('+62 811')
    expect(stepNow()).toBe('checkin-step-room')
  })

  it('stays on the step with the message of the server when the profile cannot be saved', async () => {
    setup()
    await flushPromises()
    PATCH.mockRejectedValue(new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'VALIDATION_FAILED', detail: 'invalid', errors: [{ field: 'phone', code: 'INVALID_FORMAT', message: 'a phone number' }] } as never))
    await find('input[name=phone]').setValue('x')
    await enter()
    expect(stepNow()).toBe('checkin-step-guest')
    expect(document.body.textContent).toContain('a phone number')
  })

  it('shows what the registration card misses without blocking: the identity and the phone are recommended', async () => {
    setup({ guestRow: guest({ id_type: '', id_number: '', phone: '' }) })
    await flushPromises()
    expect(find('[data-testid=hint-identity]').text()).toContain('Recommended')
    expect(find('[data-testid=hint-phone]').text()).toContain('Recommended')
    expect(find('[data-testid=checkin-next]').attributes('disabled')).toBeUndefined()
    await enter()
    expect(stepNow()).toBe('checkin-step-room')
  })

  it('stresses the passport of a foreign guest as a danger and still does not block', async () => {
    setup({ guestRow: guest({ nationality: 'NL', id_type: 'PASSPORT', id_number: '' }) })
    await flushPromises()
    const hint = find('[data-testid=hint-passport]')
    expect(hint.text()).toBe('Passport number: must be reported to immigration (APOA).')
    expect(hint.classes().join(' ')).toContain('text-destructive')
    expect(has('[data-testid=hint-identity]')).toBe(false)
    expect(find('[data-testid=checkin-next]').attributes('disabled')).toBeUndefined()
    await enter()
    expect(stepNow()).toBe('checkin-step-room')
  })

  it('finds a guest for a reservation without one, and checks that guest in', async () => {
    setup({ arrival: arrival({ guest_id: null, guest_name: '' }) })
    await flushPromises()
    expect(has('[data-testid=no-guest]')).toBe(true)
    expect(find('[data-testid=checkin-next]').attributes('disabled')).toBeDefined()
    await find('input[name=guest_q]').setValue('Siti')
    await find('[data-testid=guest-search]').trigger('click')
    await flushPromises()
    GET.mockImplementation(async (path: string) => (path === '/api/v1/guests/{id}' ? { data: guest({ id: 8, first_name: 'Siti', last_name: 'Nur' }) } : path.endsWith('/room-types') ? { data: { data: types } } : path.endsWith('/availability/rooms') ? { data: { data: [{ room_id: 22, room_number: '102', housekeeping_status: 'CLEAN' }] } } : { data: {} }))
    await find('[data-testid=guest-result-8]').trigger('click')
    await flushPromises()
    expect((find('input[name=last_name]').element as HTMLInputElement).value).toBe('Nur')
    await enter()
    await enter() // deposit
    await find('[data-testid=checkin-no-deposit]').trigger('click')
    await flushPromises()
    expect(post('/check-in')[0]?.[1]).toMatchObject({ body: { guest_id: 8 } })
  })

  it('adds a new guest on the step when the file has none, with the right to', async () => {
    setup({ arrival: arrival({ guest_id: null, guest_name: '' }) })
    await flushPromises()
    await find('input[name=new_last_name]').setValue('Baru')
    expect(find('[data-testid=checkin-next]').attributes('disabled')).toBeUndefined()
    await enter()
    const created = POST.mock.calls.find((c) => c[0] === '/api/v1/guests')
    expect(created?.[1]).toMatchObject({ body: { origin_property_id: 7, last_name: 'Baru' } })
    expect(stepNow()).toBe('checkin-step-room')
    expect(GET.mock.calls.some((c) => c[0] === '/api/v1/guests/{id}' && (c[1] as { params: { path: { id: number } } }).params.path.id === 12)).toBe(true)
  })

  it('does not offer to add a guest without guest.write', async () => {
    setup({ arrival: arrival({ guest_id: null, guest_name: '' }), permissions: ['reservation.read', 'frontdesk.checkin'] })
    await flushPromises()
    expect(has('input[name=new_last_name]')).toBe(false)
  })
})

describe('the room step', () => {
  const dirty = [{ room_id: 21, room_number: '101', housekeeping_status: 'DIRTY' }]
  const dirtyArrival = { room_id: null, room_number: undefined, housekeeping_status: undefined }

  it('does not let a room that is not ready pass without the override permission', async () => {
    setup({ rooms: dirty, arrival: dirtyArrival })
    await flushPromises()
    await enter()
    expect(find('[data-testid=not-ready]').text()).toContain('101 is Dirty')
    expect(has('input[name=override]')).toBe(false)
    expect(find('[data-testid=checkin-next]').attributes('disabled')).toBeDefined()
    await enter()
    expect(stepNow()).toBe('checkin-step-room')
    expect(POST).not.toHaveBeenCalled()
  })

  it('lets it pass with the permission, a ticked override and a reason, which go to the check-in', async () => {
    setup({ rooms: dirty, arrival: dirtyArrival, permissions: ['reservation.read', 'frontdesk.checkin', 'frontdesk.checkin_unready_room'] })
    await flushPromises()
    await enter()
    expect(find('[data-testid=checkin-next]').attributes('disabled')).toBeDefined()
    await find('input[name=override]').setValue(true)
    await find('input[name=override_reason]').setValue('guest is waiting')
    expect(find('[data-testid=checkin-next]').attributes('disabled')).toBeUndefined()
    await enter() // deposit (no right to take one here)
    await enter()
    expect(post('/check-in')[0]?.[1]).toMatchObject({ body: { room_id: 21, override_room_not_ready: true, override_reason: 'guest is waiting' } })
  })

  it('keeps the upgrade path: another room type is marked as an upgrade', async () => {
    setup()
    await flushPromises()
    await enter()
    await find('select[name=room_type]').setValue(11)
    await flushPromises()
    expect(has('[data-testid=upgrade-note]')).toBe(true)
  })
})

describe('the deposit step', () => {
  async function toDeposit(o: Setup = {}) {
    setup(o)
    await flushPromises()
    await enter()
    await enter()
  }

  it('takes a deposit by QRIS with its reference', async () => {
    await toDeposit()
    await find('[data-testid=deposit-method-QRIS]').trigger('click')
    expect(find('[data-testid=deposit-method-QRIS]').attributes('aria-pressed')).toBe('true')
    await find('input[name=deposit_reference]').setValue('QR-8841')
    await find('input[name=deposit_amount]').setValue('1500000')
    await enter()
    expect(post('/deposits')[0]?.[1]).toMatchObject({ body: { amount: '1500000', payment_method: 'QRIS', reference_number: 'QR-8841' } })
    expect(find('[data-testid=done-summary]').text()).toContain('QRIS')
    // the method is remembered on this browser for the next guest
    expect(window.localStorage.getItem('checkin.depositMethod')).toBe('QRIS')
  })

  it('offers cash, card, QRIS and transfer, and no other', async () => {
    await toDeposit()
    const methods = Array.from(document.body.querySelectorAll('[data-testid^=deposit-method-]')).map((b) => b.getAttribute('data-testid'))
    expect(methods).toEqual(['deposit-method-CASH', 'deposit-method-CARD', 'deposit-method-QRIS', 'deposit-method-BANK_TRANSFER'])
  })

  it('asks a guest who pays for a deposit, or a deliberate click to go on without one', async () => {
    await toDeposit()
    await find('input[name=deposit_amount]').setValue('')
    expect(find('[data-testid=checkin-submit]').attributes('disabled')).toBeDefined()
    await enter()
    expect(POST).not.toHaveBeenCalled() // Enter alone does not skip it
    await find('[data-testid=checkin-no-deposit]').trigger('click')
    await flushPromises()
    expect(post('/deposits')).toHaveLength(0)
    expect(post('/check-in')).toHaveLength(1)
    expect(find('[data-testid=done-summary]').text()).toContain('No deposit was taken.')
  })

  it('does not require a deposit for a reservation billed to a company (city ledger)', async () => {
    await toDeposit({ arrival: arrival({ company: { id: 2, name: 'ABC Indonesia' } }) })
    expect(find('[data-testid=deposit-company]').text()).toContain('ABC Indonesia')
    expect((find('input[name=deposit_amount]').element as HTMLInputElement).value).toBe('') // nothing is proposed for a company
    expect(has('[data-testid=checkin-no-deposit]')).toBe(false)
    expect(find('[data-testid=checkin-submit]').attributes('disabled')).toBeUndefined()
    expect(find('[data-testid=checkin-submit]').text()).toBe('Check in')
    await enter()
    expect(post('/deposits')).toHaveLength(0)
    expect(post('/check-in')).toHaveLength(1)
  })

  it('goes on without a deposit when the role cannot take payments, and says why', async () => {
    await toDeposit({ permissions: ['reservation.read', 'frontdesk.checkin'] })
    expect(has('[data-testid=deposit-nopermission]')).toBe(true)
    expect(has('input[name=deposit_amount]')).toBe(false)
    await enter()
    expect(post('/deposits')).toHaveLength(0)
    expect(post('/check-in')).toHaveLength(1)
  })
})

describe('when something fails in between', () => {
  async function toDeposit() {
    setup()
    await flushPromises()
    await enter()
    await enter()
  }
  const refused = (code: string, detail: string) => new ApiError({ type: 't', title: 'Conflict', status: 409, code, detail })

  it('a deposit that fails records nothing and no check-in is tried', async () => {
    await toDeposit()
    POST.mockImplementation(async (path: string) => {
      if (path.endsWith('/deposits')) throw refused('PERIOD_CLOSED', 'the deposit was refused')
      return { data: checkInResult }
    })
    await enter()
    expect(post('/check-in')).toHaveLength(0)
    expect(stepNow()).toBe('checkin-step-deposit')
    expect(toastText()).toContain('the deposit was refused')
    expect(has('[data-testid=checkin-failed-after-deposit]')).toBe(false)
    // the retry takes a new key: the server answered
    const first = (post('/deposits')[0]![1] as { params: { header: Record<string, string> } }).params.header['Idempotency-Key']
    POST.mockImplementation(async (path: string) => (path.endsWith('/deposits') ? { data: { payment: { id: 91, payment_number: 'PAY000091', amount: '2340000' } } } : { data: checkInResult }))
    await enter()
    const second = (post('/deposits')[1]![1] as { params: { header: Record<string, string> } }).params.header['Idempotency-Key']
    expect(second).not.toBe(first)
    expect(stepNow()).toBe('checkin-step-done')
  })

  it('a check-in that fails after the deposit says the money is safe, and the retry does not take it again', async () => {
    await toDeposit()
    let attempts = 0
    POST.mockImplementation(async (path: string) => {
      if (path.endsWith('/deposits')) return { data: { payment: { id: 91, payment_number: 'PAY000091', amount: '2340000' }, folio_item: {}, folio_balance: '0' } }
      attempts++
      if (attempts === 1) throw refused('ROOM_OCCUPIED', 'the room is occupied')
      return { data: checkInResult }
    })
    await enter()
    expect(stepNow()).toBe('checkin-step-deposit')
    const banner = find('[data-testid=checkin-failed-after-deposit]')
    expect(banner.text()).toContain('PAY000091')
    expect(banner.text()).toContain('2,340,000')
    expect(banner.text()).toContain('safe on the reservation')
    expect(toastText()).toContain('the room is occupied')
    expect(has('input[name=deposit_amount]')).toBe(false) // the amount is not asked again
    expect(find('[data-testid=checkin-submit]').text()).toBe('Try the check-in again')

    await enter()
    expect(post('/deposits')).toHaveLength(1) // not taken twice
    expect(post('/check-in')).toHaveLength(2)
    const keys = post('/check-in').map((c) => (c[1] as { params: { header: Record<string, string> } }).params.header['Idempotency-Key'])
    expect(keys[0]).not.toBe(keys[1]) // the server answered the first, so the second is a new request
    expect(stepNow()).toBe('checkin-step-done')
    expect(find('[data-testid=done-summary]').text()).toContain('2,340,000')
  })

  it('after a failure the desk can go back to another room and try again, with the deposit kept', async () => {
    await toDeposit()
    let attempts = 0
    POST.mockImplementation(async (path: string) => {
      if (path.endsWith('/deposits')) return { data: { payment: { id: 91, payment_number: 'PAY000091', amount: '2340000' }, folio_item: {}, folio_balance: '0' } }
      if (++attempts === 1) throw refused('ROOM_OCCUPIED', 'the room is occupied')
      return { data: checkInResult }
    })
    await enter()
    await find('[data-testid=checkin-back]').trigger('click')
    expect(stepNow()).toBe('checkin-step-room')
    await enter()
    expect(has('[data-testid=deposit-posted]') || has('[data-testid=checkin-failed-after-deposit]')).toBe(true)
  })
})

describe('the end', () => {
  it('prints the registration card and the deposit receipt, and e-mails the confirmation', async () => {
    setup()
    await flushPromises()
    await enter()
    await enter()
    await enter()
    await flushPromises()
    await find('[data-testid=done-card]').trigger('click')
    expect(openPdf).toHaveBeenCalledWith('/api/v1/properties/7/stays/55/registration-card.pdf')
    await find('[data-testid=done-receipt]').trigger('click')
    expect(openPdf).toHaveBeenCalledWith('/api/v1/properties/7/payments/91/receipt.pdf')
    await find('[data-testid=done-email]').trigger('click')
    await flushPromises()
    expect(post('/emails')).toHaveLength(1)
    expect(find('[data-testid=done-email-notice]').text()).toContain('queued')
  })

  it('has no receipt button when no deposit was taken, and no e-mail button without a mail server', async () => {
    setup({ permissions: ['reservation.read', 'reservation.update', 'frontdesk.checkin'] })
    GET.mockImplementation(async (path: string) => (path.endsWith('/emails') ? { data: { enabled: false, data: [] } } : path === '/api/v1/guests/{id}' ? { data: guest() } : path.endsWith('/room-types') ? { data: { data: types } } : path.endsWith('/availability/rooms') ? { data: { data: [{ room_id: 22, room_number: '102', housekeeping_status: 'CLEAN' }] } } : { data: {} }))
    await flushPromises()
    await enter()
    await enter()
    await enter()
    await flushPromises()
    expect(has('[data-testid=done-card]')).toBe(true)
    expect(has('[data-testid=done-receipt]')).toBe(false)
    expect(has('[data-testid=done-email]')).toBe(false)
  })

  it('goes to the next ready arrival without closing the sheet, and starts it at the first step', async () => {
    const next = arrival({ reservation_room_id: 7, reservation_id: 12, confirmation_number: 'RES000012', guest_id: 6, guest_name: 'Rina Wijaya' })
    const blocked = arrival({ reservation_room_id: 8, guest_name: 'Not Ready', readiness: { status: 'BLOCKED', blockers: ['ROOM_NOT_READY'] } })
    const w = setup({ queue: [arrival(), blocked, next] })
    await flushPromises()
    await enter()
    await enter()
    await find('[data-testid=checkin-no-deposit]').trigger('click')
    await flushPromises()
    expect(stepNow()).toBe('checkin-step-done')
    expect(w.emitted('done')).toHaveLength(1)
    expect(find('[data-testid=checkin-next-arrival]').text()).toContain('Rina Wijaya')
    await find('[data-testid=checkin-next-arrival]').trigger('click')
    await flushPromises()
    expect(has('[data-testid=checkin-sheet]')).toBe(true)
    expect(has('form[data-testid=checkin-7]')).toBe(true)
    expect(stepNow()).toBe('checkin-step-guest')
    expect(document.body.textContent).toContain('RES000012')
    expect(w.emitted('close')).toBeUndefined()
  })

  it('offers no next check-in for an arrival that was done here', async () => {
    setup({ queue: [arrival()] })
    await flushPromises()
    await enter()
    await enter()
    await find('[data-testid=checkin-no-deposit]').trigger('click')
    await flushPromises()
    expect(has('[data-testid=checkin-next-arrival]')).toBe(false)
  })

  it('closes with Close, and tells the host', async () => {
    const w = setup()
    await flushPromises()
    await enter()
    await enter()
    await find('[data-testid=checkin-no-deposit]').trigger('click')
    await flushPromises()
    await find('[data-testid=checkin-close]').trigger('click')
    await flushPromises()
    expect(w.emitted('close')).toHaveLength(1)
    expect(has('[data-testid=checkin-sheet]')).toBe(false)
  })
})

describe('the keyboard and the steps', () => {
  const esc = async () => {
    const target = (document.activeElement as HTMLElement | null) ?? document.body
    target.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))
    await flushPromises()
  }

  it('Esc goes back one step and keeps what was typed; at the first step it closes the sheet', async () => {
    const w = setup()
    await flushPromises()
    await enter()
    await enter()
    await find('input[name=deposit_amount]').setValue('1234567')
    await find('[data-testid=deposit-method-CARD]').trigger('click')
    await esc()
    expect(stepNow()).toBe('checkin-step-room')
    expect(w.emitted('close')).toBeUndefined()
    await enter() // forward again: the amount and the method are as they were
    expect((find('input[name=deposit_amount]').element as HTMLInputElement).value).toBe('1234567')
    expect(find('[data-testid=deposit-method-CARD]').attributes('aria-pressed')).toBe('true')
    await esc()
    await esc()
    expect(stepNow()).toBe('checkin-step-guest')
    await esc()
    expect(w.emitted('close')).toHaveLength(1)
  })

  it('the steps of the header go back to a step that was reached, and not ahead of it', async () => {
    setup()
    await flushPromises()
    expect(find('[data-testid=checkin-step-room]').attributes('disabled')).toBeDefined()
    expect(find('[data-testid=checkin-step-deposit]').attributes('disabled')).toBeDefined()
    await enter()
    await enter()
    await find('input[name=deposit_amount]').setValue('99')
    await find('[data-testid=checkin-step-guest]').trigger('click')
    expect(stepNow()).toBe('checkin-step-guest')
    expect(find('[data-testid=checkin-step-deposit]').attributes('disabled')).toBeUndefined() // reached before
    await find('[data-testid=checkin-step-deposit]').trigger('click')
    expect((find('input[name=deposit_amount]').element as HTMLInputElement).value).toBe('99')
  })

  it('the steps cannot be used to leave the end once the guest is in the room', async () => {
    setup()
    await flushPromises()
    await enter()
    await enter()
    await enter()
    await flushPromises()
    expect(find('[data-testid=checkin-step-guest]').attributes('disabled')).toBeDefined()
    expect(stepNow()).toBe('checkin-step-done')
  })

  it('puts the cursor on the button of a step that is complete, so Enter goes on', async () => {
    setup()
    await flushPromises()
    await flushPromises()
    expect(document.activeElement?.getAttribute('data-testid')).toBe('checkin-next')
  })
})
