import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import DeparturesView from './DeparturesView.vue'

let GET = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))

const folio = (over: object = {}) => ({ id: 80, folio_number: 'FOL000001', folio_type: 'GUEST', bill_to_company_id: null, status: 'OPEN', balance: '0', ...over })
const row = (over: object = {}) => ({
  id: 5, stay_number: 'STY000001', version: 1, reservation_id: 9, confirmation_number: 'RES000009',
  guest: { id: 3, name: 'Siti Nurhaliza' }, room: { id: 21, number: '101', room_type_code: 'DLX', room_type_name: 'Deluxe' }, company: null, billing: [],
  rate: { rate_plan_code: 'BAR', rate_plan_name: 'Best available', amount: '1200000', price_mode: 'EXCLUSIVE', is_override: false },
  stay: { arrival_date: '2026-09-28', departure_date: '2026-09-30', nights: 2, adults: 2, children: 0 },
  balance: { amount: '0', status: 'SETTLED', folios: [folio()] }, checkout: { status: 'READY', uncharged_nights: 0 }, ...over,
})
const overdue = () => row({ id: 6, stay_number: 'STY000002', guest: { id: 4, name: 'Budi Santoso' }, stay: { arrival_date: '2026-09-27', departure_date: '2026-09-29', nights: 2, adults: 1, children: 0 },
  balance: { amount: '450000', status: 'OUTSTANDING', folios: [folio({ id: 81, balance: '450000' })] }, checkout: { status: 'BALANCE_DUE', uncharged_nights: 0 } })

const ALL = ['reservation.read', 'frontdesk.checkout', 'guest.write', 'folio.read']
let mounted: VueWrapper | null = null

async function mountView(permissions = ALL, page: object = { data: [row(), overdue()] }) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-09-30' } as never
  GET = vi.fn(async (path: string) => (path.endsWith('/room-types') ? { data: { data: [{ id: 10, code: 'DLX', is_active: true }, { id: 11, code: 'STD', is_active: true }] } } : path === '/api/v1/guests/{id}' ? { data: { id: 3, code: 'G1', first_name: 'Siti', last_name: 'Nurhaliza', can_edit: true } } : { data: page }))
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  mounted = mount(DeparturesView, { attachTo: document.body, global: { plugins: [pinia, router] } })
  await flushPromises()
  return mounted
}
const listCalls = () => GET.mock.calls.filter((c) => String(c[0]).endsWith('/stays/in-house'))

describe('DeparturesView', () => {
  beforeEach(() => {
    GET = vi.fn()
    PATCH = vi.fn()
  })
  afterEach(() => {
    mounted?.unmount()
    mounted = null
    document.body.innerHTML = ''
  })

  it('asks the in-house read model for the stays that leave by the business date and flags the overdue ones', async () => {
    const w = await mountView()
    expect(listCalls()[0]?.[1]).toEqual({ params: { path: { propertyId: 7 }, query: { limit: 50, cursor: undefined, departure_until: '2026-09-30' } } })
    expect(w.get('[data-testid=stay-STY000001]').find('[data-testid=overdue]').exists()).toBe(false)
    expect(w.get('[data-testid=stay-STY000002]').find('[data-testid=overdue]').exists()).toBe(true)
  })

  it('shows balance and what is known before the check-out, from the server', async () => {
    const w = await mountView()
    const settled = w.get('[data-testid=stay-STY000001]')
    expect(settled.get('[data-testid=balance-STY000001]').text()).toContain('0')
    expect(settled.get('[data-testid=checkout-status-STY000001]').attributes('data-status')).toBe('READY')
    expect(settled.get('[data-testid=rate-STY000001]').text()).toContain('1,200,000')
    const due = w.get('[data-testid=stay-STY000002]')
    expect(due.get('[data-testid=balance-STY000002]').text()).toContain('450,000')
    expect(due.get('[data-testid=checkout-status-STY000002]').text()).toBe('Balance due')
  })

  it('says No folio for a stay without one, never zero, and shows the folios of a company bill without adding them twice', async () => {
    const w = await mountView(ALL, { data: [
      row({ balance: { amount: '', status: 'NO_FOLIO', folios: [] }, checkout: { status: 'FOLIO_ISSUE', uncharged_nights: 0 } }),
      row({ id: 6, stay_number: 'STY000002', company: { id: 2, name: 'ABC Indonesia' }, billing: [{ scope: 'ROOM', company_id: 2, company_name: 'ABC Indonesia' }],
        balance: { amount: '700000', status: 'OUTSTANDING', folios: [folio({ balance: '0' }), folio({ id: 82, folio_type: 'COMPANY', bill_to_company_id: 2, balance: '700000' })] }, checkout: { status: 'COMPANY_BILL', uncharged_nights: 0 } }),
    ] })
    const none = w.get('[data-testid=balance-STY000001]')
    expect(none.text()).toBe('No folio')
    expect(none.text()).not.toContain('0')
    expect(w.get('[data-testid=checkout-status-STY000001]').text()).toBe('Folio issue')
    const company = w.get('[data-testid=stay-STY000002]')
    expect(company.get('[data-testid=company-STY000002]').text()).toBe('ABC Indonesia')
    expect(company.get('[data-testid=balance-STY000002]').get('[data-testid=balance]').text()).toBe('700,000') // the total is the server's sum, once
    expect(company.text()).toContain('Company 700,000')
    expect(company.text()).toContain('Guest 0')
    expect(w.get('[data-testid=checkout-status-STY000002]').text()).toBe('Company bill')
  })

  it('hides check-out and the folio without the permissions, says when empty and needs read access', async () => {
    const plain = await mountView(['reservation.read'])
    expect(plain.find('[data-testid=checkOut-STY000001]').exists()).toBe(false)
    expect(plain.find('[data-testid=folio-STY000001]').exists()).toBe(false)
    plain.unmount()
    const empty = await mountView(['reservation.read'], { data: [] })
    expect(empty.get('[data-testid=empty]').text()).toContain('No departures')
    empty.unmount()
    GET = vi.fn()
    const denied = await mountView(['guest.read'])
    expect(denied.find('[data-testid=no-access]').exists()).toBe(true)
    expect(listCalls()).toHaveLength(0)
  })

  it('goes to the existing check-out from the row', async () => {
    const w = await mountView()
    const router = (w.vm as unknown as { $router: ReturnType<typeof createRouter> }).$router
    await w.get('[data-testid=checkOut-STY000001]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/stays/5?action=checkout')
  })

  it('asks the server for the date, the exact date, the search and the room type', async () => {
    const w = await mountView()
    await w.get('input[name=departure_date]').setValue('2026-10-02')
    await flushPromises()
    expect(listCalls().at(-1)?.[1]).toMatchObject({ params: { query: { departure_until: '2026-10-02' } } })
    await w.get('input[name=exact]').setValue(true)
    await flushPromises()
    const exact = listCalls().at(-1)?.[1] as { params: { query: Record<string, unknown> } }
    expect(exact.params.query.departure_date).toBe('2026-10-02')
    expect(exact.params.query.departure_until).toBeUndefined()
    await w.get('input[name=q]').setValue('Siti')
    await flushPromises()
    await w.get('select[name=room_type_id]').setValue('11')
    await flushPromises()
    expect(listCalls().at(-1)?.[1]).toMatchObject({ params: { query: { q: 'Siti', room_type_id: 11, departure_date: '2026-10-02' } } })
    // the server answered with both rows whatever was asked: the table shows what it was given
    expect(w.findAll('tbody tr[data-testid^=stay-]')).toHaveLength(2)
    await w.get('[data-testid=clear-filters]').trigger('click')
    await flushPromises()
    expect(listCalls().at(-1)?.[1]).toMatchObject({ params: { query: { departure_until: '2026-09-30' } } })
  })

  it('loads more with the cursor and keeps the filter', async () => {
    const w = await mountView(ALL, { data: [row()], next_cursor: 'c2' })
    await w.get('input[name=q]').setValue('Siti')
    await flushPromises()
    await w.get('[data-testid=more]').trigger('click')
    await flushPromises()
    expect(listCalls().at(-1)?.[1]).toMatchObject({ params: { query: { cursor: 'c2', q: 'Siti' } } })
  })

  it('shows a failure with a retry, never as an empty list', async () => {
    const w = await mountView()
    GET.mockImplementation(async () => {
      throw new ApiError({ type: 't', title: 'Unavailable', status: 503, code: 'SERVICE_UNAVAILABLE', detail: 'try later' })
    })
    await w.get('input[name=q]').setValue('x')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('SERVICE_UNAVAILABLE')
    expect(w.find('[data-testid=empty]').exists()).toBe(false)
    expect(w.get('[data-testid=not-loaded]').text()).toContain('could not be loaded')
    GET.mockImplementation(async () => ({ data: { data: [row()] } }))
    await w.get('[data-testid=retry]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid=form-error]').exists()).toBe(false)
    expect(w.find('[data-testid=stay-STY000001]').exists()).toBe(true)
  })

  it('opens the drawer with the checkout status, and edits the guest in place keeping the filters', async () => {
    PATCH = vi.fn().mockResolvedValue({ data: { id: 3, code: 'G1', first_name: 'Sari', last_name: 'Wijaya', can_edit: true } })
    const w = await mountView()
    await w.get('input[name=q]').setValue('Siti')
    await flushPromises()
    await w.get('[data-testid=open-STY000001]').trigger('click')
    await flushPromises()
    expect(document.body.querySelector('[data-testid=drawer-checkout]')?.textContent).toBe('No known blocker')
    ;(document.body.querySelector('[data-testid=drawer-editGuest]') as HTMLElement).click()
    await flushPromises()
    const dialog = document.body.querySelector('[data-testid=guest-edit-dialog]') as HTMLElement
    const first = dialog.querySelector('input[name=first_name]') as HTMLInputElement
    first.value = 'Sari'
    first.dispatchEvent(new Event('input'))
    const last = dialog.querySelector('input[name=last_name]') as HTMLInputElement
    last.value = 'Wijaya'
    last.dispatchEvent(new Event('input'))
    const calls = listCalls().length
    dialog.querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(w.get('[data-testid=open-STY000001]').text()).toBe('Sari Wijaya')
    expect(listCalls()).toHaveLength(calls) // not reloaded
    expect((w.get('input[name=q]').element as HTMLInputElement).value).toBe('Siti') // the filter stays
  })

  it('reads again when the front desk says something changed', async () => {
    const w = await mountView()
    const calls = listCalls().length
    await w.setProps({ refresh: 1 })
    await flushPromises()
    expect(listCalls()).toHaveLength(calls + 1)
  })
})
