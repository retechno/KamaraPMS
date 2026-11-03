import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ReservationsView from './ReservationsView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const room = (over: object = {}) => ({
  id: 4, status: 'CONFIRMED', room_type_id: 10, room_type_code: 'DLX', room_id: 21, room_number: '101', rate_plan_code: 'BAR', nights: 2, adult_count: 2, child_count: 0, rate_amount: '1200000', price_mode: 'EXCLUSIVE',
  arrival_date: '2026-10-02', departure_date: '2026-10-04', requested_bed_type_id: null, bed_locked: false, stay_id: null, ...over,
})
const row = (over: object = {}) => ({
  id: 1, confirmation_number: 'RES000001', guest_id: 3, guest_name: 'Siti Nurhaliza', source: 'PHONE', status: 'CONFIRMED', display_status: 'CONFIRMED', arrival_date: '2026-10-02',
  departure_date: '2026-10-04', nights: 2, room_count: 1, rooms: [room()], deposit: null, version: 2, created_at: '2026-09-30T00:00:00Z', ...over,
})

let mounted: VueWrapper | null = null
let router: ReturnType<typeof createRouter>

async function mountView(at = '/reservations', businessDate = '2026-10-02', page: object = { data: [row()] }) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: ['reservation.read'] }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: businessDate } as never
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/room-types')) return { data: { data: [{ id: 10, code: 'DLX', is_active: true }, { id: 11, code: 'STD', is_active: true }] } }
    if (path.endsWith('/rate-plans')) return { data: { data: [{ id: 1, code: 'BAR', name: 'Best' }] } }
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
const chip = (w: VueWrapper, id: string) => w.get(`[data-testid=preset-${id}]`)
const lit = (w: VueWrapper, id: string) => chip(w, id).attributes('aria-pressed') === 'true'

describe('ReservationsView, the preset chips', () => {
  beforeEach(() => {
    setLocale('en')
    GET = vi.fn()
  })
  afterEach(() => {
    vi.useRealTimers()
    mounted?.unmount()
    mounted = null
    document.body.innerHTML = ''
  })

  it('has the five chips, named in both languages', async () => {
    const w = await mountView()
    expect(w.findAll('[data-testid=presets] button').map((b) => b.text())).toEqual(['Arriving today', 'Tomorrow', '7 days', 'Draft', 'Cancelled'])
    mounted!.unmount()
    setLocale('id')
    const id = await mountView()
    expect(id.findAll('[data-testid=presets] button').map((b) => b.text())).toEqual(['Tiba hari ini', 'Besok', '7 hari', 'Draf', 'Batal'])
  })

  it('asks for each preset by the business date of the property, and keeps it in the address', async () => {
    const w = await mountView()
    const calls = listCalls().length
    await chip(w, 'today').trigger('click')
    await flushPromises()
    expect(listCalls().length).toBe(calls + 1) // at once, one request, no "Cari"
    expect(lastQuery()).toMatchObject({ arrival_from: '2026-10-02', arrival_to: '2026-10-02', display_status: undefined })
    expect(router.currentRoute.value.query).toMatchObject({ arrivalFrom: '2026-10-02', arrivalTo: '2026-10-02' })

    await chip(w, 'tomorrow').trigger('click')
    await flushPromises()
    expect(lastQuery()).toMatchObject({ arrival_from: '2026-10-03', arrival_to: '2026-10-03' })

    await chip(w, 'week').trigger('click')
    await flushPromises()
    expect(lastQuery()).toMatchObject({ arrival_from: '2026-10-02', arrival_to: '2026-10-08' })

    await chip(w, 'draft').trigger('click')
    await flushPromises()
    expect(lastQuery()).toMatchObject({ display_status: 'DRAFT', arrival_from: undefined, arrival_to: undefined })
    expect(router.currentRoute.value.query).toMatchObject({ status: 'DRAFT' })

    await chip(w, 'cancelled').trigger('click')
    await flushPromises()
    expect(lastQuery()).toMatchObject({ display_status: 'CANCELLED' })
    expect((w.get('select[name=status]').element as HTMLSelectElement).value).toBe('CANCELLED')
  })

  it('counts seven days with both ends in: the end is the business date plus six, also at the end of a month, a leap February and a year', async () => {
    for (const [from, to, tomorrow] of [['2026-10-28', '2026-11-03', '2026-10-29'], ['2026-12-28', '2027-01-03', '2026-12-29'], ['2026-02-25', '2026-03-03', '2026-02-26'], ['2028-02-26', '2028-03-03', '2028-02-27'], ['2026-10-31', '2026-11-06', '2026-11-01']] as const) {
      const w = await mountView('/reservations', from)
      await chip(w, 'week').trigger('click')
      await flushPromises()
      expect(lastQuery(), from).toMatchObject({ arrival_from: from, arrival_to: to })
      await chip(w, 'tomorrow').trigger('click')
      await flushPromises()
      expect(lastQuery(), from).toMatchObject({ arrival_from: tomorrow, arrival_to: tomorrow })
      mounted!.unmount()
    }
  })

  it('never reads the clock of the browser', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2031-05-05T12:00:00Z'))
    const w = await mountView('/reservations', '2026-10-02')
    await chip(w, 'today').trigger('click')
    await flushPromises()
    expect(lastQuery()).toMatchObject({ arrival_from: '2026-10-02', arrival_to: '2026-10-02' })
  })

  it('replaces the whole set of filters, the search text too', async () => {
    const w = await mountView('/reservations?q=Siti&roomType=11&company=5&departureTo=2026-11-01')
    expect((w.get('input[name=q]').element as HTMLInputElement).value).toBe('Siti')
    await chip(w, 'draft').trigger('click')
    await flushPromises()
    expect(lastQuery()).toMatchObject({ q: undefined, room_type_id: undefined, company_id: undefined, departure_to: undefined, display_status: 'DRAFT' })
    expect((w.get('input[name=q]').element as HTMLInputElement).value).toBe('')
    expect(router.currentRoute.value.query).toEqual({ status: 'DRAFT' })
  })

  it('gives the default list back when the lit chip is clicked again', async () => {
    const w = await mountView()
    await chip(w, 'week').trigger('click')
    await flushPromises()
    expect(lit(w, 'week')).toBe(true)
    await chip(w, 'week').trigger('click')
    await flushPromises()
    expect(lit(w, 'week')).toBe(false)
    expect(lastQuery()).toMatchObject({ arrival_from: undefined, arrival_to: undefined, display_status: undefined, q: undefined })
    expect(router.currentRoute.value.query).toEqual({})
    expect(w.find('[data-testid=clear-filters]').exists()).toBe(false)
  })

  it('is lit by the filters of the address exactly, one at a time, and not by part of them', async () => {
    const exact = await mountView('/reservations?arrivalFrom=2026-10-02&arrivalTo=2026-10-08')
    expect(['today', 'tomorrow', 'week', 'draft', 'cancelled'].map((id) => lit(exact, id))).toEqual([false, false, true, false, false])
    mounted!.unmount()
    const partial = await mountView('/reservations?arrivalFrom=2026-10-02')
    expect(['today', 'tomorrow', 'week', 'draft', 'cancelled'].map((id) => lit(partial, id))).toEqual([false, false, false, false, false])
    mounted!.unmount()
    const more = await mountView('/reservations?arrivalFrom=2026-10-02&arrivalTo=2026-10-02&q=x')
    expect(lit(more, 'today')).toBe(false) // another filter is on: the chip would lie
  })

  it('goes out when another filter is changed after a chip', async () => {
    const w = await mountView()
    await chip(w, 'draft').trigger('click')
    await flushPromises()
    expect(lit(w, 'draft')).toBe(true)
    await w.get('select[name=room_type_id]').setValue('11')
    await flushPromises()
    expect(lastQuery()).toMatchObject({ display_status: 'DRAFT', room_type_id: 11 })
    expect(lit(w, 'draft')).toBe(false)
    await w.get('select[name=room_type_id]').setValue('')
    await flushPromises()
    expect(lit(w, 'draft')).toBe(true)
  })

  it('loads at once for a chip and a list, and waits for the search button for the text', async () => {
    const w = await mountView()
    const calls = listCalls().length
    await w.get('input[name=q]').setValue('Siti')
    await flushPromises()
    expect(listCalls().length).toBe(calls) // typing asks for nothing
    await w.get('select[name=status]').setValue('RESERVED')
    await flushPromises()
    expect(listCalls().length).toBe(calls + 1)
    expect(lastQuery()).toMatchObject({ q: 'Siti', display_status: 'CONFIRMED' })
    await w.get('input[name=q]').setValue('Budi')
    expect(listCalls().length).toBe(calls + 1)
    await w.get('form[role=search]').trigger('submit')
    await flushPromises()
    expect(listCalls().length).toBe(calls + 2)
    expect(lastQuery()).toMatchObject({ q: 'Budi' })
  })

  it('has the chips off while the business date is not known (the ones that need it)', async () => {
    const w = await mountView('/reservations', '')
    expect(chip(w, 'today').attributes('disabled')).toBeDefined()
    expect(chip(w, 'week').attributes('disabled')).toBeDefined()
    expect(chip(w, 'draft').attributes('disabled')).toBeUndefined()
  })
})

describe('ReservationsView, the filters that are not always in sight', () => {
  beforeEach(() => setLocale('en'))
  afterEach(() => {
    mounted?.unmount()
    mounted = null
    document.body.innerHTML = ''
  })
  const shown = (w: VueWrapper, name: string) => (w.get(`[name=${name}]`).element.closest('[data-slot=form-field]') as HTMLElement).style.display !== 'none'

  it('keeps the search and the status in sight and the rest behind "More filters"', async () => {
    const w = await mountView()
    expect(shown(w, 'q')).toBe(true)
    expect(shown(w, 'status')).toBe(true)
    for (const n of ['arrival_from', 'arrival_to', 'departure_from', 'departure_to', 'room_type_id', 'rate_plan_id', 'company_id']) expect(shown(w, n), n).toBe(false)
    const more = w.get('[data-testid=more-filters]')
    expect(more.text()).toBe('More filters')
    expect(more.attributes('aria-expanded')).toBe('false')
    await more.trigger('click')
    for (const n of ['arrival_from', 'departure_to', 'room_type_id', 'company_id']) expect(shown(w, n), n).toBe(true)
    expect(w.get('[data-testid=more-filters]').attributes('aria-expanded')).toBe('true')
  })

  it('is open by itself when one of them is on: from the address, from a chip, from a list', async () => {
    const fromAddress = await mountView('/reservations?company=5')
    expect(shown(fromAddress, 'company_id')).toBe(true)
    expect(fromAddress.get('[data-testid=more-filters-count]').text()).toContain('1')
    mounted!.unmount()
    const w = await mountView()
    expect(shown(w, 'arrival_from')).toBe(false)
    await chip(w, 'week').trigger('click')
    await flushPromises()
    expect(shown(w, 'arrival_from')).toBe(true)
    await chip(w, 'draft').trigger('click') // a preset that sets none of them: they close again
    await flushPromises()
    expect(shown(w, 'arrival_from')).toBe(false)
    await w.get('select[name=status]').setValue('')
    await w.get('[data-testid=more-filters]').trigger('click')
    await w.get('select[name=room_type_id]').setValue('11')
    await flushPromises()
    expect(shown(w, 'room_type_id')).toBe(true)
  })

  it('has all of them in the sheet of a phone, without the button', async () => {
    const wide = window.innerWidth
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 })
    try {
      const w = await mountView()
      expect(w.find('[data-testid=more-filters]').exists()).toBe(false)
      await w.get('[data-testid=open-filters]').trigger('click')
      await flushPromises()
      const sheet = document.body.querySelector('[data-testid=filter-sheet]')!
      for (const n of ['arrival_from', 'departure_to', 'room_type_id', 'company_id']) expect((sheet.querySelector(`[name=${n}]`)!.closest('[data-slot=form-field]') as HTMLElement).style.display, n).not.toBe('none')
      await chip(w, 'week').trigger('click')
      await flushPromises()
      expect(w.get('[data-testid=filter-count]').text()).toContain('2') // the count of a phone goes on
    } finally {
      Object.defineProperty(window, 'innerWidth', { configurable: true, value: wide })
    }
  })
})

describe('ReservationsView, the row', () => {
  beforeEach(() => setLocale('en'))
  afterEach(() => {
    mounted?.unmount()
    mounted = null
    document.body.innerHTML = ''
  })

  it('opens the reservation from the name of the guest, as from its number', async () => {
    const w = await mountView('/reservations', '2026-10-02', { data: [row({ id: 9 })] })
    const link = w.get('[data-testid=guest-link]')
    expect(link.text()).toBe('Siti Nurhaliza')
    expect(link.attributes('href')).toBe('/reservations/9')
    await link.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/reservations/9')
  })

  it('has no link, only a dash, when the reservation has no guest name', async () => {
    const w = await mountView('/reservations', '2026-10-02', { data: [row({ guest_name: '' })] })
    expect(w.find('[data-testid=guest-link]').exists()).toBe(false)
  })

  it('shows the "Deposit" line only when the reservation holds a deposit (the list has no other fact about a deposit that is required)', async () => {
    const without = await mountView('/reservations', '2026-10-02', { data: [row()] })
    expect(without.get('[data-testid=res-RES000001]').text()).not.toContain('Deposit')
    mounted!.unmount()
    const withDeposit = await mountView('/reservations', '2026-10-02', { data: [row({ deposit: { folio_id: 80, paid: '0' } })] })
    expect(withDeposit.get('[data-testid=res-RES000001]').text()).toContain('Deposit')
    expect(withDeposit.find('[data-testid=deposit-RES000001]').exists()).toBe(true)
  })
})
