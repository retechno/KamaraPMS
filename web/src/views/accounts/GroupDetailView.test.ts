import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import GroupDetailView from './GroupDetailView.vue'

let GET = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))

const group = (over: object = {}) => ({
  id: 4, code: 'CONF', name: 'Annual conference', company_id: 1, company_name: 'Acme Corp', arrival_date: '2026-10-10', departure_date: '2026-10-14',
  contact_name: 'Budi', is_active: true, reservation_count: 2, cancelled_reservation_count: 0, room_count: 3, ...over,
})
const members = [
  { reservation_id: 11, confirmation_number: 'RES000011', status: 'CONFIRMED', display_status: 'CONFIRMED', guest_name: 'Siti Nurhaliza', company_id: 1, arrival_date: '2026-10-10', departure_date: '2026-10-12', room_count: 2 },
  { reservation_id: 12, confirmation_number: 'RES000012', status: 'CANCELLED', display_status: 'CANCELLED', guest_name: 'Andi', company_id: 1, arrival_date: '2026-10-11', departure_date: '2026-10-13', room_count: 1 },
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

  it('shows a group as cancelled when it has reservations and none of them is left (there is no status column: it is derived)', async () => {
    const w = mountView(group({ reservation_count: 0, cancelled_reservation_count: 2, room_count: 0 }))
    await flushPromises()
    expect(w.get('[data-testid=group-cancelled]').text()).toBe('Cancelled')
    const live = mountView()
    await flushPromises()
    expect(live.find('[data-testid=group-cancelled]').exists()).toBe(false)
    // no room is added to a cancelled group: both buttons are off, and the page says why
    expect(w.find('[data-testid=add-rooms]').exists()).toBe(false)
    expect(w.get('[data-testid=add-rooms-disabled]').attributes('disabled')).toBeDefined()
    expect(w.get('[data-testid=bulk-rooms-disabled]').attributes('disabled')).toBeDefined()
    expect(w.get('[data-testid=group-cancelled-note]').text()).toBe('This group is cancelled: no room is added to it. Bring its reservations back first, or make a new group.')
    expect(live.find('[data-testid=add-rooms]').exists()).toBe(true)
    expect(live.find('[data-testid=group-cancelled-note]').exists()).toBe(false)
    // a group with no reservation at all is not a cancelled one
    const empty = mountView(group({ reservation_count: 0, cancelled_reservation_count: 0, room_count: 0 }))
    await flushPromises()
    expect(empty.find('[data-testid=group-cancelled]').exists()).toBe(false)
  })

  it('shows a group as cancelled when it has reservations and none of them is left (there is no status column: it is derived) - in Indonesian too', async () => {
    setLocale('id')
    const w = mountView(group({ reservation_count: 0, cancelled_reservation_count: 1, room_count: 0 }))
    await flushPromises()
    expect(w.get('[data-testid=group-cancelled]').text()).toBe('Dibatalkan')
    setLocale('en')
  })

  it('shows the status of a reservation as a translated badge', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.get('[data-testid=member-RES000011]').text()).toContain('Confirmed')
    setLocale('id')
    await flushPromises()
    expect(w.get('[data-testid=member-RES000011]').text()).not.toContain('Confirmed')
    setLocale('en')
  })

  it('shows the status staff see, not the header: a room in house is "In house" while the header stays confirmed', async () => {
    const first = members[0] as { display_status: string }
    first.display_status = 'IN_HOUSE'
    try {
      const w = mountView()
      await flushPromises()
      expect(w.get('[data-testid=member-RES000011]').text()).toContain('In house')
      expect(w.get('[data-testid=member-RES000011]').text()).not.toContain('Confirmed')
    } finally {
      first.display_status = 'CONFIRMED'
    }
  })

  it('opens the form of rooms in bulk, and the rooming list is under the reservations', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.find('[data-testid=bulk-rooms]').exists()).toBe(false)
    await w.get('[data-testid=bulk-rooms-open]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid=bulk-rooms]').exists()).toBe(true)
    await w.get('[data-testid=bulk-close]').trigger('click')
    expect(w.find('[data-testid=bulk-rooms]').exists()).toBe(false)
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/rooming-list'))).toBe(false) // no guest.read: the list is not asked for
    const reader = mountView(group(), ['reservation.read'])
    await flushPromises()
    expect(reader.find('[data-testid=bulk-rooms-open]').exists()).toBe(false)
    const inactive = mountView(group({ is_active: false }))
    await flushPromises()
    expect(inactive.find('[data-testid=bulk-rooms-open]').exists()).toBe(false)
    const full = mountView(group(), ['group.manage', 'reservation.read', 'reservation.create', 'guest.read'])
    await flushPromises()
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/rooming-list'))).toBe(true)
    expect(full.find('[data-testid=rooming-list]').exists()).toBe(true)
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
