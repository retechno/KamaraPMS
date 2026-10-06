import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import NewReservationView from './NewReservationView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const search = {
  nights: ['2026-10-02', '2026-10-03'],
  room_types: [
    {
      room_type_id: 10, code: 'DLX', name: 'Deluxe', fits_occupancy: true, available_min: 2, per_night: [],
      rate_plans: [
        { id: 1, code: 'BAR', name: 'Best', price_mode: 'EXCLUSIVE', occupancy_kind: 'PAID', nightly: [], missing_nights: 0, estimate: { net: '2000000', service: '0', tax: '0', total: '2200000' } },
        { id: 2, code: 'HALF', name: 'Half', price_mode: 'EXCLUSIVE', occupancy_kind: 'PAID', nightly: [], missing_nights: 1, estimate: null },
      ],
    },
    {
      room_type_id: 11, code: 'STD', name: 'Standard', fits_occupancy: true, available_min: 0, per_night: [],
      rate_plans: [{ id: 1, code: 'BAR', name: 'Best', price_mode: 'EXCLUSIVE', occupancy_kind: 'PAID', nightly: [], missing_nights: 0, estimate: { net: '1', service: '0', tax: '0', total: '1' } }],
    },
  ],
}
const freePlan = { id: 9, code: 'COMP', name: 'Complimentary', price_mode: 'EXCLUSIVE', occupancy_kind: 'COMPLIMENTARY', nightly: [], missing_nights: 0, estimate: { net: '0', service: '0', tax: '0', total: '0' } }
const siti = { id: 3, code: 'GST000001', first_name: 'Siti', last_name: 'Nurhaliza' }

const beds = [{ id: 5, code: 'KING', name: 'King', is_active: true }, { id: 6, code: 'TWIN', name: 'Twin', is_active: true }]

function mountView(permissions = ['reservation.read', 'reservation.create']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-10-02' } as never
  GET = vi.fn(async (path: string) => ({ data: path.endsWith('/guests') ? { data: [siti] } : path.endsWith('/bed-types') ? { data: beds } : search }))
  POST = vi.fn().mockResolvedValue({ data: { id: 42 } })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  const push = vi.spyOn(router, 'push')
  return { w: mount(NewReservationView, { global: { plugins: [pinia, router] } }), push }
}

describe('NewReservationView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
    document.body.innerHTML = ''
  })

  it('needs create permission', async () => {
    const { w } = mountView(['reservation.read'])
    await flushPromises()
    expect(w.find('[data-testid=no-access]').exists()).toBe(true)
    expect(w.find('form').exists()).toBe(false)
  })

  it('searches from the business date and offers only bookable plans', async () => {
    const { w } = mountView()
    await flushPromises()
    expect((w.get('input[name=arrival]').element as HTMLInputElement).value).toBe('2026-10-02')
    expect((w.get('input[name=departure]').element as HTMLInputElement).value).toBe('2026-10-03')
    await w.get('input[name=departure]').setValue('2026-10-04')
    await w.get('form[data-testid=search-form]').trigger('submit')
    await flushPromises()
    expect(GET).toHaveBeenLastCalledWith('/api/v1/properties/{propertyId}/availability', {
      params: { path: { propertyId: 7 }, query: { arrival: '2026-10-02', departure: '2026-10-04', adults: 2, children: 0 } },
    })
    expect(w.get('[data-testid=offer-DLX-BAR]').text()).toContain('2,200,000')
    expect(w.get('[data-testid=pick-DLX-BAR]').attributes('disabled')).toBeUndefined()
    expect(w.get('[data-testid=pick-DLX-HALF]').attributes('disabled')).toBeDefined() // incomplete grid
    expect(w.get('[data-testid=offer-DLX-HALF]').get('[data-testid=missing]').text()).toContain('1 night(s) without a rate')
    expect(w.get('[data-testid=pick-STD-BAR]').attributes('disabled')).toBeDefined() // sold out
  })

  it('sends the bed that was asked for with the room', async () => {
    const { w } = mountView()
    await flushPromises()
    await w.get('form[data-testid=search-form]').trigger('submit')
    await flushPromises()
    await w.get('[data-testid=pick-DLX-BAR]').trigger('click')
    await flushPromises()
    expect(w.findAll('select[name=bed_type_id] option').map((o) => o.text())).toEqual(['No preference', 'King', 'Twin'])
    await w.get('select[name=bed_type_id]').setValue(6)
    await w.get('input[name=guest_q]').setValue('siti')
    await w.get('[data-testid=find-guest]').trigger('click')
    await flushPromises()
    await w.get('[data-testid=guest-GST000001]').trigger('click')
    await w.get('form[data-testid=book-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body.rooms[0]).toMatchObject({ room_type_id: 10, bed_type_id: 6 })
  })

  it('asks for the reason when the plan is complimentary, and sends it', async () => {
    const { w } = mountView(['reservation.read', 'reservation.create', 'reservation.complimentary', 'reservation.complimentary_approve'])
    await flushPromises()
    GET.mockImplementation(async (path: string) => ({ data: path.endsWith('/guests') ? { data: [siti] } : path.endsWith('/bed-types') ? { data: beds } : { ...search, room_types: [{ ...search.room_types[0], rate_plans: [...search.room_types[0]!.rate_plans, freePlan] }] } }))
    await w.get('form[data-testid=search-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=kind-COMP]').text()).toBe('Complimentary')
    await w.get('[data-testid=pick-DLX-BAR]').trigger('click')
    expect(w.find('input[name=occupancy_reason]').exists()).toBe(false) // a paid plan needs none
    await w.get('[data-testid=pick-DLX-COMP]').trigger('click')
    await w.get('input[name=occupancy_reason]').setValue('Owner guest')
    await w.get('input[name=guest_q]').setValue('siti')
    await w.get('[data-testid=find-guest]').trigger('click')
    await flushPromises()
    await w.get('[data-testid=guest-GST000001]').trigger('click')
    await w.get('form[data-testid=book-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body.rooms[0]).toMatchObject({ rate_plan_id: 9, occupancy_reason: 'Owner guest' })
  })

  const priced = { ...search, room_types: [{ ...search.room_types[0]!, rate_plans: [{ ...search.room_types[0]!.rate_plans[0]!, nightly: [{ date: '2026-10-02', amount: '1000000' }, { date: '2026-10-03', amount: '1000000' }] }] }] }

  async function pickAndChangeRate(permissions: string[]) {
    const { w } = mountView(permissions)
    await flushPromises()
    GET.mockImplementation(async (path: string) => ({ data: path.endsWith('/guests') ? { data: [siti] } : path.endsWith('/bed-types') ? { data: beds } : priced }))
    await w.get('form[data-testid=search-form]').trigger('submit')
    await flushPromises()
    await w.get('[data-testid=pick-DLX-BAR]').trigger('click')
    await w.get('input[name=guest_q]').setValue('siti')
    await w.get('[data-testid=find-guest]').trigger('click')
    await flushPromises()
    await w.get('[data-testid=guest-GST000001]').trigger('click')
    await w.get('[data-testid=override-toggle]').trigger('click')
    await w.get('input[name=override_amount_2026-10-02]').setValue('800000')
    await w.get('input[name=rate_override_reason]').setValue('Corporate rate')
    return w
  }

  async function pickFree(permissions: string[]) {
    const { w } = mountView(permissions)
    await flushPromises()
    GET.mockImplementation(async (path: string) => ({ data: path.endsWith('/guests') ? { data: [siti] } : path.endsWith('/bed-types') ? { data: beds } : { ...search, room_types: [{ ...search.room_types[0]!, rate_plans: [freePlan] }] } }))
    await w.get('form[data-testid=search-form]').trigger('submit')
    await flushPromises()
    await w.get('[data-testid=pick-DLX-COMP]').trigger('click')
    await w.get('input[name=occupancy_reason]').setValue('Owner guest')
    await w.get('input[name=guest_q]').setValue('siti')
    await w.get('[data-testid=find-guest]').trigger('click')
    await flushPromises()
    await w.get('[data-testid=guest-GST000001]').trigger('click')
    return w
  }

  async function approveInDialog() {
    const email = document.body.querySelector('input[name=approval_email]') as HTMLInputElement
    const password = document.body.querySelector('input[name=approval_password]') as HTMLInputElement
    email.value = 'boss@hotel.test'
    email.dispatchEvent(new Event('input'))
    password.value = 'secret'
    password.dispatchEvent(new Event('input'))
    await flushPromises()
    ;(document.body.querySelector('[data-testid=approval-dialog]') as HTMLFormElement).dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
  }

  it('asks a manager to approve a free room, unless the person may approve it', async () => {
    const w = await pickFree(['reservation.read', 'reservation.create', 'reservation.complimentary'])
    await w.get('form[data-testid=book-form]').trigger('submit')
    await flushPromises()
    expect(POST).not.toHaveBeenCalled() // the dialog comes first
    expect(document.body.textContent).toContain('manager')
    await approveInDialog()
    expect(POST.mock.calls[0]?.[1].body).toMatchObject({ occupancy_approval: { email: 'boss@hotel.test', password: 'secret' } })
    document.body.innerHTML = ''
    POST.mockClear()
    const self = await pickFree(['reservation.read', 'reservation.create', 'reservation.complimentary', 'reservation.complimentary_approve'])
    await self.get('form[data-testid=book-form]').trigger('submit')
    await flushPromises()
    expect(document.body.querySelector('[data-testid=approval-dialog]')).toBeNull()
    expect(POST.mock.calls[0]?.[1].body.occupancy_approval).toBeUndefined()
  })

  it('shows the quota of free nights it would go over, and goes over it only when asked to', async () => {
    const w = await pickFree(['reservation.read', 'reservation.create', 'reservation.complimentary', 'reservation.complimentary_approve'])
    POST.mockRejectedValueOnce(new ApiError({
      type: 't', title: 'Conflict', status: 409, code: 'FREE_NIGHT_QUOTA_EXCEEDED', detail: 'over',
      context: { occupancy_kind: 'COMPLIMENTARY', month: '2026-10-01', quota: 3, used: 2, requested: 2 },
    }))
    await w.get('form[data-testid=book-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=quota-warning]').text()).toContain('quota of 3 nights')
    expect(w.get('[data-testid=quota-warning]').text()).toContain('2026-10')
    await w.get('input[name=exceed_free_quota]').setValue(true)
    await w.get('form[data-testid=book-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[1]?.[1].body).toMatchObject({ exceed_free_quota: true })
  })

  it('does not offer a rate change to someone who may not override', async () => {
    const { w } = mountView()
    await flushPromises()
    GET.mockImplementation(async () => ({ data: priced }))
    await w.get('form[data-testid=search-form]').trigger('submit')
    await flushPromises()
    await w.get('[data-testid=pick-DLX-BAR]').trigger('click')
    expect(w.find('[data-testid=rate-override]').exists()).toBe(false)
  })

  it('asks for an approver before it books a changed price, and sends the change with the approval', async () => {
    const w = await pickAndChangeRate(['reservation.read', 'reservation.create', 'reservation.override_rate'])
    await w.get('form[data-testid=book-form]').trigger('submit')
    await flushPromises()
    expect(POST).not.toHaveBeenCalled() // the dialog comes first
    const email = document.body.querySelector('input[name=approval_email]') as HTMLInputElement
    const password = document.body.querySelector('input[name=approval_password]') as HTMLInputElement
    email.value = 'boss@hotel.test'
    email.dispatchEvent(new Event('input'))
    password.value = 'secret'
    password.dispatchEvent(new Event('input'))
    await flushPromises()
    ;(document.body.querySelector('[data-testid=approval-dialog]') as HTMLFormElement).dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    const body = POST.mock.calls[0]?.[1].body
    expect(body).toMatchObject({ rate_override_reason: 'Corporate rate', rate_override_approval: { email: 'boss@hotel.test', password: 'secret' } })
    expect(body.rooms[0].nightly_overrides).toEqual([{ date: '2026-10-02', amount: '800000' }])
  })

  it('books a changed price straight away for someone who may approve it', async () => {
    const w = await pickAndChangeRate(['reservation.read', 'reservation.create', 'reservation.override_rate', 'reservation.override_rate_approve'])
    await w.get('form[data-testid=book-form]').trigger('submit')
    await flushPromises()
    expect(document.body.querySelector('[data-testid=approval-dialog]')).toBeNull()
    const body = POST.mock.calls[0]?.[1].body
    expect(body.rate_override_approval).toBeUndefined()
    expect(body).toMatchObject({ rate_override_reason: 'Corporate rate' })
  })

  it('books with a booker and an Idempotency-Key, then opens the reservation', async () => {
    const { w, push } = mountView()
    await flushPromises()
    await w.get('form[data-testid=search-form]').trigger('submit')
    await flushPromises()
    await w.get('[data-testid=pick-DLX-BAR]').trigger('click')
    await w.get('input[name=guest_q]').setValue('siti')
    await w.get('[data-testid=find-guest]').trigger('click')
    await flushPromises()
    await w.get('[data-testid=guest-GST000001]').trigger('click')
    expect(w.get('[data-testid=chosen-guest]').text()).toContain('Siti Nurhaliza')
    await w.get('form[data-testid=book-form]').trigger('submit')
    await flushPromises()
    const [path, init] = POST.mock.calls[0] as [string, { params: { header: Record<string, string> }; body: Record<string, unknown> }]
    expect(path).toBe('/api/v1/properties/{propertyId}/reservations')
    expect(init.params.header['Idempotency-Key']).toBeTruthy()
    expect(init.body).toMatchObject({ guest_id: 3, source: 'PHONE', confirm: true, rooms: [{ room_type_id: 10, rate_plan_id: 1, arrival_date: '2026-10-02', departure_date: '2026-10-03', adult_count: 2, child_count: 0 }] })
    expect(push).toHaveBeenCalledWith('/reservations/42')
  })

  it('shows the server error and keeps the form', async () => {
    const { w, push } = mountView()
    await flushPromises()
    await w.get('form[data-testid=search-form]').trigger('submit')
    await flushPromises()
    await w.get('[data-testid=pick-DLX-BAR]').trigger('click')
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'ROOM_TYPE_NOT_AVAILABLE', detail: 'no rooms' }))
    await w.get('form[data-testid=book-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('ROOM_TYPE_NOT_AVAILABLE')
    expect(push).not.toHaveBeenCalled()
    expect(w.find('form[data-testid=book-form]').exists()).toBe(true)
  })
})

describe('NewReservationView: the offers', () => {
  async function results(over: object = {}) {
    const m = mountView()
    const base = GET.getMockImplementation() as (path: string) => Promise<unknown>
    GET = vi.fn(async (path: string) => (path.endsWith('/availability') ? { data: { ...search, ...over } } : base(path)))
    await flushPromises()
    await m.w.get('form[data-testid=search-form]').trigger('submit')
    await flushPromises()
    return m.w
  }

  it('says why a plan cannot be booked, on the disabled button', async () => {
    const w = await results()
    expect(w.get('[data-testid=pick-STD-BAR]').attributes('title')).toContain('No room of this type is left')
    expect(w.get('[data-testid=pick-DLX-HALF]').attributes('title')).toContain('1 night(s) have no rate')
    expect(w.get('[data-testid=pick-DLX-BAR]').attributes('title')).toBe('')
    expect(w.get('[data-testid=offer-STD-BAR]').text()).toContain('0') // rooms left, shown as a warning
  })

  it('shows a room type without a rate plan, and one that does not fit the party', async () => {
    const w = await results({
      room_types: [
        { room_type_id: 12, code: 'SUI', name: 'Suite', fits_occupancy: true, available_min: 1, per_night: [], rate_plans: [] },
        { room_type_id: 13, code: 'SGL', name: 'Single', fits_occupancy: false, available_min: 3, per_night: [], rate_plans: [{ id: 1, code: 'BAR', name: 'Best', price_mode: 'INCLUSIVE', nightly: [], missing_nights: 0, estimate: { net: '1', service: '0', tax: '0', total: '500000' } }] },
      ],
    })
    expect(w.get('[data-testid=type-SUI]').text()).toContain('No active rate plan.')
    expect(w.get('[data-testid=offer-SGL-BAR]').text()).toContain('inclusive')
    expect(w.get('[data-testid=no-fit]').text()).toContain('does not fit 2 adult(s) and 0 child(ren)')
    expect(w.get('[data-testid=pick-SGL-BAR]').attributes('disabled')).toBeDefined()
    expect(w.get('[data-testid=pick-SGL-BAR]').attributes('title')).toBe('The room type is too small for this party.')
  })

  it('speaks Indonesian, down to the booking form', async () => {
    setLocale('id')
    const w = await results()
    expect(w.get('h1').text()).toBe('Reservasi baru')
    expect(w.get('[data-testid=results]').text()).toContain('2 malam')
    expect(w.get('[data-testid=offer-DLX-BAR]').text()).toContain('eksklusif')
    await w.get('[data-testid=pick-DLX-BAR]').trigger('click')
    expect(w.get('[data-testid=book-form]').text()).toContain('Pesan DLX dengan BAR')
    expect(w.get('[data-testid=book-form] button[type=submit]').text()).toBe('Pesan dan konfirmasi')
    setLocale('en')
  })
})

describe('NewReservationView: company and group', () => {
  const acme = { id: 1, code: 'ACME', name: 'Acme Corp' }
  const conf = { id: 4, code: 'CONF', name: 'Conference', company_id: 1, company_name: 'Acme Corp', arrival_date: '2026-10-02', departure_date: '2026-10-05' }

  async function bookable(permissions?: string[]) {
    const m = mountView(permissions)
    const base = GET.getMockImplementation() as (path: string) => Promise<unknown>
    GET = vi.fn(async (path: string) => {
      if (path.endsWith('/companies')) return { data: { data: [acme] } }
      if (path.endsWith('/groups')) return { data: { data: [conf] } }
      return base(path)
    })
    await flushPromises()
    await m.w.get('[data-testid=search-form]').trigger('submit')
    await flushPromises()
    await m.w.get('[data-testid=pick-DLX-BAR]').trigger('click')
    await flushPromises()
    return m
  }

  it('books for a company that is billed', async () => {
    const { w } = await bookable()
    expect(w.findAll('select[name=company_id] option').map((o) => o.text())).toEqual(['None (the guest pays)', 'ACME · Acme Corp'])
    await w.get('select[name=company_id]').setValue(1)
    await w.get('[data-testid=book-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body).toMatchObject({ company_id: 1 })
    expect(POST.mock.calls[0]?.[1].body.booking_group_id).toBeUndefined()
  })

  it('books into a group, which brings its company and its dates', async () => {
    const { w } = await bookable()
    await w.get('select[name=booking_group_id]').setValue(4)
    expect(w.get('[data-testid=group-hint]').text()).toContain('2 Oct 2026 to 5 Oct 2026')
    expect(w.get('[data-testid=group-hint]').text()).toContain('Acme Corp is billed')
    expect(w.find('select[name=company_id]').exists()).toBe(false) // the group decides
    await w.get('[data-testid=book-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body).toMatchObject({ booking_group_id: 4 })
    expect(POST.mock.calls[0]?.[1].body.company_id).toBeUndefined()
  })

  it('books without company or group when the role cannot list them', async () => {
    const m = mountView()
    const base = GET.getMockImplementation() as (path: string) => Promise<unknown>
    GET = vi.fn(async (path: string) => {
      if (path.endsWith('/companies') || path.endsWith('/groups')) throw new Error('forbidden')
      return base(path)
    })
    await flushPromises()
    await m.w.get('[data-testid=search-form]').trigger('submit')
    await flushPromises()
    await m.w.get('[data-testid=pick-DLX-BAR]').trigger('click')
    await flushPromises()
    expect(m.w.find('select[name=company_id]').exists()).toBe(false)
    expect(m.w.find('select[name=booking_group_id]').exists()).toBe(false)
  })

  it('offers the variants with what is left and keeps the bed that was chosen, with its supplement', async () => {
    const { w } = mountView()
    const nightly = (adj: string) => [{ date: '2026-10-02', amount: String(1000000 + Number(adj)), bed_adjustment: adj }, { date: '2026-10-03', amount: String(1000000 + Number(adj)), bed_adjustment: adj }]
    const plan = (adj: string) => ({ id: 1, code: 'BAR', name: 'Best', price_mode: 'EXCLUSIVE', occupancy_kind: 'PAID', nightly: nightly(adj), missing_nights: 0, estimate: { net: '1', service: '0', tax: '0', total: '1' } })
    const variants = {
      ...search,
      room_types: [{ ...search.room_types[0], beds: [
        { bed_type_id: 5, code: 'KING', name: 'King', available_min: 0, per_night: [], rate_plans: [plan('50000')] },
        { bed_type_id: 6, code: 'TWIN', name: 'Twin', available_min: 1, per_night: [], rate_plans: [plan('30000')] },
      ] }, search.room_types[1]],
    }
    GET.mockImplementation(async (path: string) => ({ data: path.endsWith('/guests') ? { data: [siti] } : path.endsWith('/bed-types') ? { data: beds } : variants }))
    await flushPromises()
    await w.get('form[data-testid=search-form]').trigger('submit')
    await flushPromises()
    await w.get('[data-testid=pick-DLX-BAR]').trigger('click')
    await flushPromises()
    expect(w.findAll('select[name=bed_type_id] option').map((o) => o.text())).toEqual(['No preference', 'King · sold out', 'Twin · 1 left'])
    expect(w.find('input[name=bed_locked]').exists()).toBe(false) // nothing to keep without a bed
    await w.get('select[name=bed_type_id]').setValue(6)
    await w.get('input[name=bed_locked]').setValue(true)
    expect(w.get('[data-testid=bed-supplement]').text()).toContain('60,000') // 30,000 a night for two nights
    await w.get('input[name=guest_q]').setValue('siti')
    await w.get('[data-testid=find-guest]').trigger('click')
    await flushPromises()
    await w.get('[data-testid=guest-GST000001]').trigger('click')
    await w.get('form[data-testid=book-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body.rooms[0]).toMatchObject({ room_type_id: 10, bed_type_id: 6, bed_locked: true })
    // taking the bed off takes the lock off
    await w.get('select[name=bed_type_id]').setValue(0)
    expect(w.find('input[name=bed_locked]').exists()).toBe(false)
  })
})
