import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import StayActions from './StayActions.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const detail = (over: object = {}) => ({
  stay: { id: 5, stay_number: 'STY000001', arrival_date: '2026-09-30', departure_date: '2026-10-02', status: 'OPEN', version: 3 }, ...over,
}) as never

const all = ['reservation.read', 'reservation.update', 'frontdesk.room_move', 'frontdesk.checkin']

function mountActions(permissions = all, d: never = detail()) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-09-30' } as never
  GET = vi.fn((path: string) => {
    if (path.endsWith('/room-types')) return Promise.resolve({ data: { data: [{ id: 1, code: 'DLX', name: 'Deluxe', is_active: true }] } })
    if (path.endsWith('/availability/rooms')) return Promise.resolve({ data: { data: [{ room_id: 22, room_number: '102', housekeeping_status: 'CLEAN' }] } })
    return Promise.resolve({ data: { data: [{ id: 9, code: 'G2', first_name: 'Budi', last_name: 'Santoso' }] } })
  })
  POST = vi.fn().mockResolvedValue({ data: { new_segment: { room_number: '102' } } })
  return mount(StayActions, { props: { detail: d }, global: { plugins: [pinia] } })
}

describe('StayActions', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
  })

  it('moves the guest to a free room with the stay version and a reason', async () => {
    const w = mountActions()
    await w.get('[data-testid=open-move]').trigger('click')
    await flushPromises()
    expect(GET.mock.calls.find((c) => String(c[0]).endsWith('/availability/rooms'))?.[1]).toMatchObject({ params: { query: { room_type_id: 1, arrival: '2026-09-30', departure: '2026-10-02' } } })
    expect(w.get('form[data-testid=move-form] button[type=submit]').attributes('disabled')).toBeDefined()
    await w.get('input[name=reason]').setValue('noisy')
    await w.get('form[data-testid=move-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]).toEqual(['/api/v1/properties/{propertyId}/stays/{id}/move', {
      params: { path: { propertyId: 7, id: 5 } }, body: { version: 3, room_id: 22, reason: 'noisy', override_room_not_ready: false, override_reason: undefined },
    }])
    expect(w.emitted('changed')?.[0]?.[0]).toContain('102')
  })

  it('changes the departure and shows the server refusal with its hint', async () => {
    const w = mountActions()
    await w.get('[data-testid=open-departure]').trigger('click')
    expect(w.get('form[data-testid=departure-form] button[type=submit]').attributes('disabled')).toBeDefined()
    await w.get('input[name=departure]').setValue('2026-10-05')
    POST.mockRejectedValueOnce(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'ROOM_NOT_AVAILABLE_FOR_EXTENSION', detail: 'busy' }))
    await w.get('form[data-testid=departure-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=action-error]').text()).toContain('Move the guest to another room first')
    await w.get('form[data-testid=departure-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[1]).toEqual(['/api/v1/properties/{propertyId}/stays/{id}/change-departure', { params: { path: { propertyId: 7, id: 5 } }, body: { version: 3, departure_date: '2026-10-05' } }])
    expect(w.emitted('changed')?.[0]?.[0]).toContain('2026-10-05')
  })

  it('adds an accompanying guest found by search', async () => {
    const w = mountActions()
    await w.get('[data-testid=open-guest]').trigger('click')
    await w.get('input[name=guest_q]').setValue('budi')
    await w.get('[data-testid=find-guest]').trigger('click')
    await flushPromises()
    await w.get('[data-testid=guest-G2]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]).toEqual(['/api/v1/properties/{propertyId}/stays/{id}/guests', { params: { path: { propertyId: 7, id: 5 } }, body: { guest_id: 9 } }])
    expect(w.emitted('changed')).toHaveLength(1)
  })

  it('offers only what the role allows and nothing for a stay that has left', () => {
    const limited = mountActions(['reservation.read', 'frontdesk.room_move'])
    expect(limited.find('[data-testid=open-move]').exists()).toBe(true)
    expect(limited.find('[data-testid=open-departure]').exists()).toBe(false)
    expect(limited.find('[data-testid=open-guest]').exists()).toBe(false)
    const gone = mountActions(all, detail({ stay: { id: 5, status: 'CHECKED_OUT', version: 4, departure_date: '2026-10-02' } }))
    expect(gone.find('[data-testid=stay-actions]').exists()).toBe(false)
  })
})
