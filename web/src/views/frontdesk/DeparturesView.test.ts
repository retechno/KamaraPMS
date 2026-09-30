import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import DeparturesView from './DeparturesView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const stay = (over: object = {}) => ({ id: 5, stay_number: 'STY000001', guest_name: 'Siti Nurhaliza', room_number: '101', arrival_date: '2026-09-28', departure_date: '2026-09-30', adult_count: 2, child_count: 0, ...over })

function mountView(permissions = ['reservation.read', 'frontdesk.checkout'], page: object = { data: [stay(), stay({ id: 6, stay_number: 'STY000002', departure_date: '2026-09-29' })] }) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-09-30' } as never
  GET = vi.fn().mockResolvedValue({ data: page })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(DeparturesView, { global: { plugins: [pinia, router] } })
}

describe('DeparturesView', () => {
  beforeEach(() => {
    GET = vi.fn()
  })

  it('lists open stays due out up to the business date and flags the overdue ones', async () => {
    const w = mountView()
    await flushPromises()
    expect(GET.mock.calls[0]?.[1]).toMatchObject({ params: { path: { propertyId: 7 }, query: { status: 'OPEN', departure_until: '2026-09-30', limit: 50 } } })
    expect(w.get('[data-testid=stay-STY000001]').find('[data-testid=overdue]').exists()).toBe(false)
    expect(w.get('[data-testid=stay-STY000002]').find('[data-testid=overdue]').exists()).toBe(true)
    expect(w.get('[data-testid=checkout-STY000001]').attributes('href')).toBe('/stays/5')
  })

  it('hides the check-out link without the permission, says when empty and needs read access', async () => {
    const plain = mountView(['reservation.read'])
    await flushPromises()
    expect(plain.find('[data-testid=checkout-STY000001]').exists()).toBe(false)
    const empty = mountView(['reservation.read'], { data: [] })
    await flushPromises()
    expect(empty.get('[data-testid=empty]').text()).toContain('No departures')
    const denied = mountView(['guest.read'])
    await flushPromises()
    expect(denied.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })
})
