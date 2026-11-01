import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import type { InHouseRow } from '@/api/types'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import InHouseDrawer from './InHouseDrawer.vue'
import InHouseTable from './InHouseTable.vue'

vi.mock('@/api/client', () => ({ api: { GET: vi.fn(async () => ({ data: {} })), POST: vi.fn(), PATCH: vi.fn() } }))

const row = (n: number, status: string, amount: string): InHouseRow => ({
  id: 70 + n, stay_number: `STY0000${70 + n}`, version: 1, reservation_id: 30, confirmation_number: 'RES000030', guest: { id: 9, name: `Guest ${n}` },
  room: { id: 5, number: `30${n}`, room_type_code: 'DLX', room_type_name: 'Deluxe' }, company: null, billing: [],
  rate: { rate_plan_code: 'RO', rate_plan_name: 'Room Only', amount: '880000', price_mode: 'NET', is_override: false },
  stay: { arrival_date: '2026-10-11', departure_date: '2026-10-14', nights: 3, adults: 2, children: 0 },
  balance: { amount, status, folios: [] }, checkout: { status: 'READY', uncharged_nights: 0 },
}) as unknown as InHouseRow

async function mountTable(rows: InHouseRow[]) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 1, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: ['reservation.read', 'frontdesk.checkout'] }] } as never
  usePropertyStore().currentId = 7
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  const w = mount(InHouseTable, { props: { rows, loaded: true }, attachTo: document.body, global: { plugins: [pinia, router] } })
  await flushPromises()
  return w
}

describe('the check-out button of the stays', () => {
  beforeEach(() => setLocale('en'))
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('says "Pay & check out", as an outline with an amber wallet, for a guest who owes money, and "Check out", solid, for the others', async () => {
    const w = await mountTable([row(1, 'OUTSTANDING', '465000'), row(2, 'CREDIT', '-100000'), row(3, 'SETTLED', '0')])
    const owing = w.get('[data-testid=checkOut-STY000071]')
    expect(owing.text()).toBe('Pay & check out')
    expect(owing.attributes('data-owes')).toBe('true')
    expect(owing.classes()).toContain('border')
    expect(owing.classes()).not.toContain('bg-primary')
    expect(owing.find('svg.text-warning-text').exists()).toBe(true)
    for (const stay of ['STY000072', 'STY000073']) {
      const other = w.get(`[data-testid=checkOut-${stay}]`)
      expect(other.text()).toBe('Check out')
      expect(other.attributes('data-owes')).toBe('false')
      expect(other.classes()).toContain('bg-primary')
      expect(other.find('svg').exists()).toBe(false)
    }
    w.unmount()
  })

  it('says it on the card of a phone as well, and in Indonesian', async () => {
    setLocale('id')
    const w = await mountTable([row(1, 'OUTSTANDING', '465000')])
    const card = w.get('[data-testid=card-STY000071]')
    expect(card.findAll('button').map((b) => b.text())).toContain('Bayar & check-out')
    expect(w.get('[data-testid=checkOut-STY000071]').text()).toBe('Bayar & check-out')
    w.unmount()
  })

  it('says it in the drawer of the stay too', async () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    useAuthStore().me = { user: { id: 1, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: ['reservation.read', 'frontdesk.checkout'] }] } as never
    usePropertyStore().currentId = 7
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
    const w = mount(InHouseDrawer, { props: { open: true, row: row(1, 'OUTSTANDING', '465000') }, attachTo: document.body, global: { plugins: [pinia, router] } })
    await flushPromises()
    const button = document.body.querySelector('[data-testid=drawer-checkOut]') as HTMLElement
    expect(button.textContent?.trim()).toBe('Pay & check out')
    expect(button.getAttribute('data-owes')).toBe('true')
    expect(button.querySelector('svg.text-warning-text')).not.toBeNull()
    w.unmount()
  })
})
