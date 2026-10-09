import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ReservationsView from './ReservationsView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const room = (over: object = {}) => ({
  id: 4, status: 'CONFIRMED', room_type_code: 'DLX', room_number: '101', rate_plan_code: 'BAR', nights: 2, adult_count: 2, child_count: 1, rate_amount: '1200000', price_mode: 'EXCLUSIVE',
  arrival_date: '2026-10-02', stay_id: null, ...over,
})
const row = (over: object = {}) => ({
  id: 1, confirmation_number: 'RES000001', guest_id: 3, guest_name: 'Siti Nurhaliza', source: 'PHONE', status: 'CONFIRMED', display_status: 'CONFIRMED', arrival_date: '2026-10-02',
  departure_date: '2026-10-04', nights: 2, room_count: 1, rooms: [room()], deposit: null, version: 2, created_at: '2026-09-30T00:00:00Z', ...over,
})

let mounted: VueWrapper | null = null
let router: ReturnType<typeof createRouter>

async function mountView(permissions: string[], page: object = { data: [row()] }, at = '/reservations') {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-10-02' } as never
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/room-types')) return { data: { data: [{ id: 10, code: 'DLX', is_active: true }, { id: 11, code: 'STD', is_active: true }] } }
    if (path.endsWith('/rate-plans')) return { data: { data: [{ id: 1, code: 'BAR', name: 'Best' }, { id: 2, code: 'CORP', name: 'Corporate' }] } }
    if (path.endsWith('/companies')) return { data: { data: [{ id: 5, code: 'ACME', name: 'Acme Corp' }] } }
    return { data: page }
  })
  router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  await router.push(at)
  mounted = mount(ReservationsView, { attachTo: document.body, global: { plugins: [pinia, router] } })
  await flushPromises()
  return mounted
}
const listCalls = () => GET.mock.calls.filter((c) => String(c[0]).endsWith('/reservations'))
const lastQuery = () => (listCalls().at(-1)?.[1] as { params: { query: Record<string, unknown> } }).params.query

describe('ReservationsView', () => {
  beforeEach(() => {
    setLocale('en')
    GET = vi.fn()
  })
  afterEach(() => {
    mounted?.unmount()
    mounted = null
    document.body.innerHTML = ''
  })

  it('lists reservations on open: the guest, the room, the nights, the booked rate, the company, the deposit and the status staff see', async () => {
    const w = await mountView(['reservation.read', 'reservation.create'], { data: [row({ company_name: 'Acme Corp', deposit: { folio_id: 80, paid: '300000' }, rooms: [room({ billing_company: 'Acme Corp' })] })] })
    expect(listCalls()[0]?.[1]).toMatchObject({ params: { path: { propertyId: 7 }, query: { limit: 50 } } })
    const r = w.get('[data-testid=res-RES000001]')
    expect(r.text()).toContain('Siti Nurhaliza')
    expect(r.text()).toContain('2+1')
    expect(r.get('[data-testid=room-RES000001]').text()).toBe('101')
    expect(r.text()).toContain('DLX')
    expect(r.text()).toContain('2 night(s)')
    expect(r.get('[data-testid=rate-RES000001]').text()).toContain('1,200,000')
    expect(r.text()).toContain('BAR')
    expect(r.get('[data-testid=company-RES000001]').text()).toBe('Acme Corp')
    expect(r.get('[data-testid=billing-RES000001]').text()).toContain('Acme Corp')
    expect(r.get('[data-testid=deposit-RES000001]').text()).toBe('300,000')
    expect(r.text()).toContain('Reserved') // CONFIRMED is "reserved" on this screen
    expect(r.get('a').attributes('href')).toBe('/reservations/1')
    expect(w.find('[data-testid=new]').exists()).toBe(true)
  })

  it('shows a dash for what is missing, never a zero: no rate, no company, no deposit, no room', async () => {
    const w = await mountView(['reservation.read'], { data: [row({ rooms: [room({ rate_amount: '', room_number: undefined })], company_name: undefined })] })
    const r = w.get('[data-testid=res-RES000001]')
    expect(r.get('[data-testid=rate-RES000001]').text()).toContain('—')
    expect(r.get('[data-testid=company-RES000001]').text()).toBe('—')
    expect(r.get('[data-testid=deposit-RES000001]').text()).toBe('—')
    expect(r.get('[data-testid=room-RES000001]').text()).toBe('No room')
  })

  it('names the status staff see: draft, reserved, checked in, checked out, cancelled, no-show', async () => {
    const w = await mountView(['reservation.read'], { data: [
      row({ id: 1, confirmation_number: 'RES1', status: 'DRAFT', display_status: 'DRAFT' }),
      row({ id: 2, confirmation_number: 'RES2', display_status: 'CONFIRMED' }),
      row({ id: 3, confirmation_number: 'RES3', display_status: 'IN_HOUSE' }),
      row({ id: 4, confirmation_number: 'RES4', display_status: 'CHECKED_OUT' }),
      row({ id: 5, confirmation_number: 'RES5', status: 'CANCELLED', display_status: 'CANCELLED' }),
      row({ id: 6, confirmation_number: 'RES6', display_status: 'NO_SHOW' }),
    ] })
    const badge = (n: string) => w.get(`[data-testid=res-${n}] [data-slot=status-badge]`)
    expect(['RES1', 'RES2', 'RES3', 'RES4', 'RES5', 'RES6'].map((n) => badge(n).text())).toEqual(['Draft', 'Reserved', 'Checked in', 'Checked out', 'Cancelled', 'No-show'])
    expect(w.get('[data-testid=res-RES1] [data-slot=status-badge]').attributes('title')).toContain('holds no room') // a draft holds no inventory
    expect(w.get('[data-testid=res-RES2] [data-slot=status-badge]').attributes('title')).toBeUndefined()
  })

  it('asks the server for every filter, and keeps them in the address', async () => {
    const w = await mountView(['reservation.read'])
    await w.get('input[name=q]').setValue(' RES0 ')
    await w.get('select[name=status]').setValue('RESERVED')
    await w.get('input[name=arrival_from]').setValue('2026-10-01')
    await w.get('input[name=departure_to]').setValue('2026-10-09')
    await w.get('select[name=room_type_id]').setValue('11')
    await w.get('select[name=rate_plan_id]').setValue('2')
    await w.get('select[name=company_id]').setValue('5')
    await w.get('form[role=search]').trigger('submit')
    await flushPromises()
    expect(lastQuery()).toMatchObject({ q: 'RES0', display_status: 'CONFIRMED', arrival_from: '2026-10-01', arrival_to: undefined, departure_to: '2026-10-09', room_type_id: 11, rate_plan_id: 2, company_id: 5 })
    expect(router.currentRoute.value.query).toMatchObject({ q: ' RES0 ', status: 'RESERVED', arrivalFrom: '2026-10-01', departureTo: '2026-10-09', roomType: '11', ratePlan: '2', company: '5' })
    // the server answered with the same row whatever was asked: the screen shows what it was given, it does not filter again
    expect(w.findAll('tbody tr')).toHaveLength(1)
    await w.get('[data-testid=clear-filters]').trigger('click')
    await flushPromises()
    expect(lastQuery()).toMatchObject({ q: undefined, display_status: undefined, room_type_id: undefined })
    expect(router.currentRoute.value.query).toEqual({})
  })

  it('starts from the filters in the address', async () => {
    await mountView(['reservation.read'], { data: [row()] }, '/reservations?status=CHECKED_IN&q=Siti&company=5')
    expect(lastQuery()).toMatchObject({ display_status: 'IN_HOUSE', q: 'Siti', company_id: 5 })
  })

  it('maps every status of the filter to the status the API knows, and offers no void', async () => {
    const w = await mountView(['reservation.read'])
    const options = w.get('select[name=status]').findAll('option')
    expect(options.map((o) => o.text())).toEqual(['Any', 'Draft', 'Reserved', 'Checked in', 'Checked out', 'Cancelled', 'No-show'])
    const asked: Record<string, unknown> = {}
    for (const ui of ['DRAFT', 'RESERVED', 'CHECKED_IN', 'CHECKED_OUT', 'CANCELLED', 'NO_SHOW']) {
      await w.get('select[name=status]').setValue(ui)
      await flushPromises()
      asked[ui] = lastQuery().display_status
    }
    expect(asked).toEqual({ DRAFT: 'DRAFT', RESERVED: 'CONFIRMED', CHECKED_IN: 'IN_HOUSE', CHECKED_OUT: 'CHECKED_OUT', CANCELLED: 'CANCELLED', NO_SHOW: 'NO_SHOW' })
  })

  it('loads more with the cursor and keeps the filters', async () => {
    const w = await mountView(['reservation.read'], { data: [row()], next_cursor: 'abc' })
    await w.get('input[name=q]').setValue('Siti')
    await w.get('form[role=search]').trigger('submit')
    await flushPromises()
    GET.mockImplementation(async () => ({ data: { data: [row({ id: 2, confirmation_number: 'RES000002' })] } }))
    await w.get('[data-testid=more]').trigger('click')
    await flushPromises()
    expect(lastQuery()).toMatchObject({ cursor: 'abc', q: 'Siti' })
    expect(w.findAll('tbody tr')).toHaveLength(2)
  })

  it('says when nothing matches the filters and when there is nothing at all', async () => {
    const w = await mountView(['reservation.read'], { data: [] })
    expect(w.get('[data-testid=empty]').text()).toContain('No reservations')
    await w.get('input[name=q]').setValue('zzz')
    await w.get('form[role=search]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=empty]').text()).toContain('matches the filters')
  })

  it('shows a failure with a retry, never as an empty list', async () => {
    const w = await mountView(['reservation.read'])
    GET.mockImplementation(async () => {
      throw new ApiError({ type: 't', title: 'Unavailable', status: 503, code: 'SERVICE_UNAVAILABLE', detail: 'try later' })
    })
    await w.get('input[name=q]').setValue('x')
    await w.get('form[role=search]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('SERVICE_UNAVAILABLE')
    expect(w.find('[data-testid=empty]').exists()).toBe(false)
    expect(w.find('[data-testid=not-loaded]').exists()).toBe(true)
    GET.mockImplementation(async () => ({ data: { data: [row()] } }))
    await w.get('[data-testid=retry]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid=form-error]').exists()).toBe(false)
    expect(w.find('[data-testid=res-RES000001]').exists()).toBe(true)
  })

  it('needs read permission', async () => {
    const w = await mountView(['guest.read'])
    expect(w.find('[data-testid=no-access]').exists()).toBe(true)
    expect(listCalls()).toHaveLength(0)
  })

  it('offers the actions that fit the status and the permissions', async () => {
    const can = ['reservation.read', 'reservation.update', 'frontdesk.checkin', 'payment.post']
    const w = await mountView(can, { data: [
      row({ id: 1, confirmation_number: 'RESERVED1', deposit: { folio_id: 80, paid: '1' } }),
      row({ id: 2, confirmation_number: 'DRAFT1', status: 'DRAFT', display_status: 'DRAFT' }),
      row({ id: 3, confirmation_number: 'INHOUSE1', display_status: 'IN_HOUSE', rooms: [room({ status: 'CHECKED_IN', stay_id: 55 })] }),
      row({ id: 4, confirmation_number: 'DONE1', display_status: 'CANCELLED', status: 'CANCELLED' }),
    ] })
    const has = (n: string, a: string) => w.find(`[data-testid=${a}-${n}]`).exists()
    expect(has('RESERVED1', 'view')).toBe(true)
    expect(has('RESERVED1', 'edit')).toBe(true)
    expect(has('RESERVED1', 'checkIn')).toBe(false) // in the menu, not on the row
    await w.get('[data-testid=more-RESERVED1]').trigger('click')
    await flushPromises()
    const menu = document.body.querySelector('[data-testid=menu-RESERVED1]')
    expect(menu?.querySelector('a[href="/arrivals?q=RESERVED1"]')).not.toBeNull() // check-in is the existing check-in
    expect(menu?.querySelector('a[href="/folios/80?tab=payment"]')).not.toBeNull()
    expect(has('DRAFT1', 'checkIn')).toBe(false) // a draft is confirmed before it checks in
    expect(has('INHOUSE1', 'viewStay')).toBe(true)
    expect(w.find('[data-testid=viewStay-INHOUSE1]').exists() && document.body.querySelector('a[href="/stays/55"]')).toBeTruthy()
    expect(has('DONE1', 'view')).toBe(true) // read only: only a way to look
    expect(has('DONE1', 'edit')).toBe(false)
    expect(has('DONE1', 'checkIn')).toBe(false)
  })

  it('does not offer check-in for a reservation that arrives on another date, or without the permission', async () => {
    const later = await mountView(['reservation.read', 'frontdesk.checkin'], { data: [row({ arrival_date: '2026-10-09', rooms: [room({ arrival_date: '2026-10-09' })] })] })
    expect(document.body.querySelector('a[href^="/arrivals"]')).toBeNull()
    expect(later.find('[data-testid=more-RES000001]').exists()).toBe(false)
  })

  it('sorts the page it has by arrival, and links to the tape chart', async () => {
    const w = await mountView(['reservation.read', 'reservation.create'], { data: [
      row({ id: 1, confirmation_number: 'RES000001', arrival_date: '2026-10-09' }),
      row({ id: 2, confirmation_number: 'RES000002', arrival_date: '2026-10-02' }),
    ] })
    const numbers = () => w.findAll('tbody tr').map((r) => r.findAll('td')[0]!.text())
    expect(numbers()).toEqual(['RES000001', 'RES000002'])
    await w.get('[data-testid=sort-arrival_date]').trigger('click')
    expect(numbers()).toEqual(['RES000002', 'RES000001'])
    expect(w.find('a[href="/reservations/tape"]').exists()).toBe(true)
  })

  it('speaks Indonesian', async () => {
    setLocale('id')
    const w = await mountView(['reservation.read', 'reservation.create'])
    expect(w.get('h1').text()).toBe('Reservasi')
    expect(w.get('[data-testid=new]').text()).toBe('Reservasi baru')
    expect(w.get('select[name=status]').findAll('option')[0]?.text()).toBe('Semua')
    expect(w.get('[data-testid=res-RES000001]').text()).toContain('Dipesan')
  })

  it('a slow answer to an older search does not replace the answer to the newer one', async () => {
    const w = await mountView(['reservation.read'])
    let release: (v: unknown) => void = () => {}
    const slow = new Promise((r) => { release = r })
    GET.mockImplementation(async (path: string, init?: { params?: { query?: Record<string, unknown> } }) => {
      if (!path.endsWith('/reservations')) return { data: { data: [] } }
      const q = init?.params?.query
      if (q?.q === 'Siti') return slow
      return { data: { data: [row({ id: 2, confirmation_number: 'RES000002', guest_name: 'Budi Santoso' })] } }
    })
    await w.get('input[name=q]').setValue('Siti')
    await w.get('form[role=search]').trigger('submit')
    await w.get('input[name=q]').setValue('Budi')
    await w.get('form[role=search]').trigger('submit')
    await flushPromises()
    release({ data: { data: [row({ id: 1, confirmation_number: 'RES000001', guest_name: 'Siti Nurhaliza' })] } }) // the older answer comes last
    await flushPromises()
    expect(w.findAll('tbody tr[data-testid^=res-]').map((r) => r.text())).toEqual([expect.stringContaining('Budi Santoso')])
  })

  it('shows arrival and departure in one column, so that the actions stay in view', async () => {
    const w = await mountView(['reservation.read', 'reservation.update'])
    const r = w.get('[data-testid=res-RES000001]')
    expect(r.text()).toContain('2 Oct 2026 → 4 Oct 2026')
    expect(w.findAll('thead th').map((h) => h.text())).not.toContain('Departure')
    expect(w.get('[data-testid=sort-arrival_date]').text()).toBe('Stay')
  })

  it('has a card for each reservation for a narrow screen, with the same facts and links', async () => {
    const w = await mountView(['reservation.read', 'reservation.update'], { data: [row({ company_name: 'Acme Corp', deposit: { folio_id: 80, paid: '300000' } })] })
    const card = w.get('[data-testid=card-RES000001]')
    expect(card.text()).toContain('Siti Nurhaliza')
    expect(card.text()).toContain('Reserved')
    expect(card.text()).toContain('Acme Corp')
    expect(card.text()).toContain('300,000')
    expect(card.text()).toContain('1,200,000')
    expect(card.get('a').attributes('href')).toBe('/reservations/1')
    expect(card.findAll('a').map((a) => a.attributes('href'))).toContain('/reservations/1')
  })
})
