import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import type { InHouseRow } from '@/api/types'
import { closeRoomPanel, forgetRoomDirectory, useRoomPanel } from '@/composables/useRoomPanel'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import InHouseTable from './InHouseTable.vue'

vi.mock('@/api/client', () => ({ api: { GET: vi.fn(async () => ({ data: {} })), POST: vi.fn(), PATCH: vi.fn() } }))

const row = (): InHouseRow => ({
  id: 70, stay_number: 'STY000070', version: 1, reservation_id: 30, confirmation_number: 'RES000030', guest: { id: 9, name: 'Ayu Pratiwi' },
  room: { id: 5, number: '305', room_type_code: 'DLX', room_type_name: 'Deluxe' }, company: null, billing: [],
  rate: { rate_plan_code: 'RO', rate_plan_name: 'Room Only', amount: '880000', price_mode: 'NET', is_override: false },
  stay: { arrival_date: '2026-10-11', departure_date: '2026-10-14', nights: 3, adults: 2, children: 0 },
  balance: { amount: '465000', status: 'OUTSTANDING', folios: [] }, checkout: { status: 'BALANCE_DUE', uncharged_nights: 0 },
}) as InHouseRow

describe('the room number of the in-house table', () => {
  beforeEach(() => {
    setLocale('en')
    forgetRoomDirectory()
    closeRoomPanel()
  })
  afterEach(() => {
    closeRoomPanel()
    document.body.innerHTML = ''
  })

  it('opens the room panel, and leaves the row (its drawer) alone', async () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    useAuthStore().me = { user: { id: 1, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: ['reservation.read'] }] } as never
    usePropertyStore().currentId = 7
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
    const w = mount(InHouseTable, { props: { rows: [row()], loaded: true }, attachTo: document.body, global: { plugins: [pinia, router] } })
    await flushPromises()
    const link = w.get('[data-testid=stay-STY000070] [data-slot=room-link]')
    expect(link.text()).toBe('305')
    await link.trigger('click')
    await flushPromises()
    expect(useRoomPanel().target.value).toMatchObject({ roomId: 5, number: '305' })
    expect(document.body.querySelector('[data-testid=in-house-drawer]')).toBeNull()
    w.unmount()
  })
})
