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
        { id: 1, code: 'BAR', name: 'Best', price_mode: 'EXCLUSIVE', nightly: [], missing_nights: 0, estimate: { net: '2000000', service: '0', tax: '0', total: '2200000' } },
        { id: 2, code: 'HALF', name: 'Half', price_mode: 'EXCLUSIVE', nightly: [], missing_nights: 1, estimate: null },
      ],
    },
    {
      room_type_id: 11, code: 'STD', name: 'Standard', fits_occupancy: true, available_min: 0, per_night: [],
      rate_plans: [{ id: 1, code: 'BAR', name: 'Best', price_mode: 'EXCLUSIVE', nightly: [], missing_nights: 0, estimate: { net: '1', service: '0', tax: '0', total: '1' } }],
    },
  ],
}
const siti = { id: 3, code: 'GST000001', first_name: 'Siti', last_name: 'Nurhaliza' }

function mountView(permissions = ['reservation.read', 'reservation.create']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-10-02' } as never
  GET = vi.fn(async (path: string) => ({ data: path.endsWith('/guests') ? { data: [siti] } : search }))
  POST = vi.fn().mockResolvedValue({ data: { id: 42 } })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  const push = vi.spyOn(router, 'push')
  return { w: mount(NewReservationView, { global: { plugins: [pinia, router] } }), push }
}

describe('NewReservationView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
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
})
