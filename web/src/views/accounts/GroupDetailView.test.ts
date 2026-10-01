import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import GroupDetailView from './GroupDetailView.vue'

let GET = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))

const group = (over: object = {}) => ({
  id: 4, code: 'CONF', name: 'Annual conference', company_id: 1, company_name: 'Acme Corp', arrival_date: '2026-10-10', departure_date: '2026-10-14',
  contact_name: 'Budi', is_active: true, reservation_count: 2, room_count: 3, ...over,
})
const members = [
  { reservation_id: 11, confirmation_number: 'RES000011', status: 'CONFIRMED', guest_name: 'Siti Nurhaliza', company_id: 1, arrival_date: '2026-10-10', departure_date: '2026-10-12', room_count: 2 },
  { reservation_id: 12, confirmation_number: 'RES000012', status: 'CANCELLED', guest_name: 'Andi', company_id: 1, arrival_date: '2026-10-11', departure_date: '2026-10-13', room_count: 1 },
]

function mountView(g = group(), permissions = ['group.manage', 'reservation.read', 'reservation.create']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (path: string) => ({ data: path.endsWith('/reservations') ? { data: members } : g }))
  PATCH = vi.fn().mockResolvedValue({ data: g })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(GroupDetailView, { props: { id: '4' }, global: { plugins: [pinia, router] } })
}

describe('GroupDetailView', () => {
  beforeEach(() => {
    GET = vi.fn()
    PATCH = vi.fn()
  })

  it('shows the group and its reservations, and links to booking rooms into it', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=group-summary]').text()).toContain('Acme Corp')
    expect(w.get('[data-testid=group-summary]').text()).toContain('2 (3 rooms)')
    expect(w.get('[data-testid=member-RES000011]').text()).toContain('Siti Nurhaliza')
    expect(w.get('[data-testid=member-RES000012]').text()).toContain('Cancelled')
    expect(w.get('[data-testid=add-rooms]').attributes('href')).toBe('/reservations/new?group=4')
  })

  it('offers no booking into an inactive group, nor to roles that cannot create reservations', async () => {
    const inactive = mountView(group({ is_active: false }))
    await flushPromises()
    expect(inactive.find('[data-testid=add-rooms]').exists()).toBe(false)
    const reader = mountView(group(), ['reservation.read'])
    await flushPromises()
    expect(reader.find('[data-testid=add-rooms]').exists()).toBe(false)
    expect(reader.find('[data-testid=edit-group]').exists()).toBe(false)
  })

  it('edits the dates and explains a refusal', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=edit-group]').trigger('click')
    await w.get('input[name=departure_date]').setValue('2026-10-16')
    await w.get('[data-testid=group-form]').trigger('submit')
    await flushPromises()
    expect(PATCH.mock.calls[0]?.[1].params.path).toEqual({ propertyId: 7, id: 4 })
    expect(PATCH.mock.calls[0]?.[1].body).toMatchObject({ departure_date: '2026-10-16', arrival_date: '2026-10-10', is_active: true })
    await w.get('[data-testid=edit-group]').trigger('click')
    PATCH.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'GROUP_HAS_ROOMS_OUTSIDE_DATES', detail: 'rooms of the group fall outside the new dates' }))
    await w.get('[data-testid=group-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('Move or cancel those rooms')
  })
})
