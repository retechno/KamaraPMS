import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import InHouseView from './InHouseView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

function mountView(permissions = ['reservation.read'], page: object = { data: [{ id: 5, stay_number: 'STY000001', guest_name: 'Siti Nurhaliza', room_number: '101', arrival_date: '2026-09-30', departure_date: '2026-10-02', adult_count: 2, child_count: 0 }] }) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: page })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(InHouseView, { global: { plugins: [pinia, router] } })
}

describe('InHouseView', () => {
  beforeEach(() => {
    GET = vi.fn()
  })

  it('lists open stays with a link to each', async () => {
    const w = mountView()
    await flushPromises()
    expect(GET.mock.calls[0]?.[1]).toMatchObject({ params: { path: { propertyId: 7 }, query: { status: 'OPEN', limit: 50 } } })
    const row = w.get('[data-testid=stay-STY000001]')
    expect(row.text()).toContain('101')
    expect(row.get('a').attributes('href')).toBe('/stays/5')
  })

  it('says when nobody is in house, loads more, and needs read permission', async () => {
    const empty = mountView(['reservation.read'], { data: [] })
    await flushPromises()
    expect(empty.get('[data-testid=empty]').text()).toContain('Nobody')
    const denied = mountView(['guest.read'])
    await flushPromises()
    expect(denied.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })
})
