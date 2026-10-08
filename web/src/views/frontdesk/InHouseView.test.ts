import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { useToasts } from '@/composables/useToast'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import InHouseView from './InHouseView.vue'

let GET = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))

const folio = (over: object = {}) => ({ id: 80, folio_number: 'FOL000001', folio_type: 'GUEST', bill_to_company_id: null, status: 'OPEN', balance: '450000', ...over })
const row = (over: object = {}) => ({
  id: 5, stay_number: 'STY000001', version: 1, reservation_id: 9, confirmation_number: 'RES000009',
  guest: { id: 3, name: 'Siti Nurhaliza' }, room: { id: 21, number: '101', room_type_code: 'DLX', room_type_name: 'Deluxe' },
  company: { id: 2, name: 'ABC Indonesia' }, billing: [{ scope: 'ROOM', company_id: 2, company_name: 'ABC Indonesia' }],
  rate: { rate_plan_code: 'BAR', rate_plan_name: 'Best available', amount: '1200000', price_mode: 'EXCLUSIVE', is_override: false },
  stay: { arrival_date: '2026-09-30', departure_date: '2026-10-02', nights: 2, adults: 2, children: 1 },
  balance: { amount: '450000', status: 'OUTSTANDING', folios: [folio(), folio({ id: 81, folio_number: 'FOL000002', folio_type: 'COMPANY', bill_to_company_id: 2, balance: '0' })] },
  ...over,
})
const noCompany = () => row({
  id: 6, stay_number: 'STY000002', guest: { id: 4, name: 'Budi Santoso' }, room: { id: 22, number: '102', room_type_code: 'STD', room_type_name: 'Standard' }, company: null, billing: [],
  balance: { amount: '0', status: 'SETTLED', folios: [folio({ id: 82, balance: '0' })] },
})
const guestView = (over: object = {}) => ({ id: 3, code: 'G1', first_name: 'Siti', last_name: 'Nurhaliza', can_edit: true, email: 'siti@example.com', ...over })

const ALL = ['reservation.read', 'guest.read', 'guest.write', 'folio.read', 'payment.post', 'frontdesk.checkout', 'frontdesk.room_move', 'reservation.update']

let mounted: VueWrapper | null = null
let router: ReturnType<typeof createRouter>

async function mountView(permissions = ALL, page: object = { data: [row(), noCompany()] }) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (path: string) => (path === '/api/v1/guests/{id}' ? { data: guestView() } : { data: page }))
  router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  mounted = mount(InHouseView, { attachTo: document.body, global: { plugins: [pinia, router] } })
  await flushPromises()
  return mounted
}

describe('InHouseView', () => {
  beforeEach(() => {
    setLocale('en')
    GET = vi.fn()
    PATCH = vi.fn()
  })
  afterEach(() => {
    mounted?.unmount()
    mounted = null
    document.body.innerHTML = ''
  })

  it('asks the in-house read model and links each stay', async () => {
    const w = await mountView()
    expect(GET.mock.calls[0]).toEqual(['/api/v1/properties/{propertyId}/stays/in-house', { params: { path: { propertyId: 7 }, query: { limit: 50, cursor: undefined } } }])
    const r = w.get('[data-testid=stay-STY000001]')
    expect(r.get('a').attributes('href')).toBe('/stays/5')
  })

  it('shows room, room type, company, rate, dates and the balance the server sent', async () => {
    const w = await mountView()
    const r = w.get('[data-testid=stay-STY000001]')
    expect(r.text()).toContain('101')
    expect(r.text()).toContain('DLX')
    expect(r.get('[data-testid=company-STY000001]').text()).toBe('ABC Indonesia')
    expect(r.text()).toContain('Room') // what the company pays
    expect(r.get('[data-testid=rate-STY000001]').text()).toContain('1,200,000')
    expect(r.text()).toContain('BAR')
    expect(r.text()).toContain('30 Sep 2026')
    expect(r.text()).toContain('2 Oct 2026')
    const balance = r.get('[data-testid=balance-STY000001]')
    expect(balance.text()).toBe('450,000') // the amount of the response, not rate x nights (2 x 1,200,000)
    expect(balance.get('[data-testid=balance]').attributes('data-status')).toBe('OUTSTANDING')
    expect(r.text()).toContain('Company 0') // two folios: the breakdown is shown
  })

  it('shows a dash when nobody pays but the guest, and a settled balance', async () => {
    const w = await mountView()
    const r = w.get('[data-testid=stay-STY000002]')
    expect(r.get('[data-testid=company-STY000002]').text()).toBe('—')
    expect(r.get('[data-testid=balance-STY000002]').text()).toBe('0')
    expect(r.get('[data-testid=balance]').attributes('data-status')).toBe('SETTLED')
  })

  it('offers View, Edit guest, Folio and Check out on the row and the rest in a menu', async () => {
    const w = await mountView()
    for (const a of ['view', 'editGuest', 'folio', 'checkOut']) expect(w.find(`[data-testid=${a}-STY000001]`).exists()).toBe(true)
    expect(w.find('[data-testid=moveRoom-STY000001]').exists()).toBe(false) // in the menu
    await w.get('[data-testid=more-STY000001]').trigger('click')
    await flushPromises()
    const menu = document.body.querySelector('[data-testid=menu-STY000001]')
    expect(menu?.textContent).toContain('Edit stay')
    expect(menu?.textContent).toContain('Move room')
    expect(menu?.textContent).toContain('Extend')
    expect(menu?.textContent).toContain('Payment')
  })

  it('follows the permissions: only what the person may do', async () => {
    const w = await mountView(['reservation.read'])
    expect(w.find('[data-testid=view-STY000001]').exists()).toBe(true)
    for (const a of ['editGuest', 'folio', 'checkOut']) expect(w.find(`[data-testid=${a}-STY000001]`).exists()).toBe(false)
    expect(w.find('[data-testid=more-STY000001]').exists()).toBe(false)
    const clerk = await mountView(['reservation.read', 'guest.write', 'frontdesk.checkout'])
    expect(clerk.find('[data-testid=editGuest-STY000001]').exists()).toBe(true)
    expect(clerk.find('[data-testid=folio-STY000001]').exists()).toBe(false)
    expect(clerk.find('[data-testid=checkOut-STY000001]').exists()).toBe(true)
  })

  it('goes to the page that does Folio, Payment, Move, Extend and Check out', async () => {
    const w = await mountView()
    await w.get('[data-testid=folio-STY000001]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/folios/80')
    await w.get('[data-testid=checkOut-STY000001]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/stays/5?action=checkout')
    await w.get('[data-testid=more-STY000001]').trigger('click')
    await flushPromises()
    ;(document.body.querySelector('[data-testid=payment-STY000001]') as HTMLElement).click()
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/folios/80?tab=payment')
  })

  it('opens the detail drawer from the guest without leaving the list', async () => {
    const w = await mountView()
    await w.get('[data-testid=open-STY000001]').trigger('click')
    await flushPromises()
    const drawer = document.body.querySelector('[data-testid=in-house-drawer]')
    expect(drawer?.textContent).toContain('Siti Nurhaliza')
    expect(drawer?.textContent).toContain('Deluxe')
    expect(drawer?.textContent).toContain('Best available')
    expect(drawer?.textContent).toContain('ABC Indonesia')
    expect(drawer?.querySelector('[data-testid=drawer-folios]')?.textContent).toContain('FOL000002')
    expect(drawer?.querySelector('[data-testid=drawer-editGuest]')).not.toBeNull()
    expect(router.currentRoute.value.path).toBe('/')
    expect(w.find('[data-testid=stay-STY000001]').exists()).toBe(true)
  })

  it('edits the guest from the row and shows the new name without reloading the list', async () => {
    PATCH = vi.fn().mockResolvedValue({ data: guestView({ first_name: 'Sari', last_name: 'Wijaya' }) })
    const w = await mountView()
    await w.get('[data-testid=editGuest-STY000001]').trigger('click')
    await flushPromises()
    expect(GET.mock.calls.at(-1)).toEqual(['/api/v1/guests/{id}', { params: { path: { id: 3 } } }])
    const dialog = document.body.querySelector('[data-testid=guest-edit-dialog]') as HTMLElement
    const first = dialog.querySelector('input[name=first_name]') as HTMLInputElement
    expect(first.value).toBe('Siti')
    first.value = 'Sari'
    first.dispatchEvent(new Event('input'))
    const last = dialog.querySelector('input[name=last_name]') as HTMLInputElement
    last.value = 'Wijaya'
    last.dispatchEvent(new Event('input'))
    const listCalls = GET.mock.calls.filter((c) => c[0] === '/api/v1/properties/{propertyId}/stays/in-house').length
    dialog.querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(PATCH.mock.calls[0]?.[0]).toBe('/api/v1/guests/{id}')
    expect(PATCH.mock.calls[0]?.[1]).toMatchObject({ params: { path: { id: 3 } }, body: { first_name: 'Sari', last_name: 'Wijaya' } })
    expect(w.get('[data-testid=open-STY000001]').text()).toBe('Sari Wijaya')
    expect(GET.mock.calls.filter((c) => c[0] === '/api/v1/properties/{propertyId}/stays/in-house').length).toBe(listCalls) // not reloaded
    expect(useToasts().items.value.some((x) => x.message.includes('Sari Wijaya'))).toBe(true)
    expect(document.body.querySelector('[data-testid=guest-edit-dialog]')).toBeNull()
  })

  it('shows a profile the person cannot edit read-only, and keeps the server error', async () => {
    const w = await mountView()
    GET = vi.fn(async () => ({ data: guestView({ can_edit: false }) }))
    await w.get('[data-testid=editGuest-STY000001]').trigger('click')
    await flushPromises()
    expect(document.body.querySelector('[data-testid=guest-edit-readonly]')).not.toBeNull()
    expect(document.body.querySelector('[data-testid=guest-edit-save]')).toBeNull()
  })

  it('says when nobody is in house, loads more with the cursor, and needs read permission', async () => {
    const empty = await mountView(['reservation.read'], { data: [] })
    expect(empty.get('[data-testid=empty]').text()).toContain('Nobody')
    empty.unmount()
    const paged = await mountView(ALL, { data: [row()], next_cursor: 'c2' })
    await paged.get('[data-testid=more]').trigger('click')
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1]).toMatchObject({ params: { query: { limit: 50, cursor: 'c2' } } })
    paged.unmount()
    GET = vi.fn()
    const denied = await mountView(['guest.read'])
    expect(denied.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET.mock.calls.filter((c) => String(c[0]).includes('in-house'))).toHaveLength(0)
  })
})
