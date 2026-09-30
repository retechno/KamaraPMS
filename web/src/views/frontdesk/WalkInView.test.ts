import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import WalkInView from './WalkInView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

function mountView(permissions = ['frontdesk.checkin', 'reservation.create', 'reservation.read']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-09-30' } as never
  property.current = { require_room_inspection_for_checkin: false } as never
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/room-types')) return { data: { data: [{ id: 10, code: 'DLX', name: 'Deluxe', is_active: true }] } }
    if (path.endsWith('/rate-plans')) return { data: { data: [{ id: 3, code: 'BAR', name: 'Best', is_active: true }] } }
    if (path.endsWith('/availability/rooms')) return { data: { data: [{ room_id: 21, room_number: '101', housekeeping_status: 'CLEAN' }] } }
    if (path.endsWith('/guests')) return { data: { data: [{ id: 3, code: 'G1', first_name: 'Siti', last_name: 'Nurhaliza' }] } }
    return { data: {} }
  })
  POST = vi.fn().mockResolvedValue({ data: { stay: { id: 55 } } })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  const push = vi.spyOn(router, 'push')
  return { w: mount(WalkInView, { global: { plugins: [pinia, router] } }), push }
}

describe('WalkInView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
  })

  it('needs both check-in and reservation create permission', async () => {
    const { w } = mountView(['frontdesk.checkin'])
    await flushPromises()
    expect(w.find('[data-testid=no-access]').exists()).toBe(true)
    expect(w.find('form').exists()).toBe(false)
  })

  it('searches free rooms from the business date and walks in a new guest', async () => {
    const { w, push } = mountView()
    await flushPromises()
    expect((w.get('input[name=departure]').element as HTMLInputElement).value).toBe('2026-10-01')
    expect(GET.mock.calls.some(([p, i]) => (p as string).endsWith('/availability/rooms') && JSON.stringify(i).includes('"arrival":"2026-09-30"') && JSON.stringify(i).includes('"departure":"2026-10-01"'))).toBe(true)
    expect(w.get('[data-testid=walkin-submit]').attributes('disabled')).toBeDefined() // no guest name yet
    await w.get('input[name=last_name]').setValue('Walker')
    await w.get('input[name=first_name]').setValue('Wendy')
    await w.get('form[data-testid=walkin-form]').trigger('submit')
    await flushPromises()
    const [path, init] = POST.mock.calls[0] as [string, { params: { header: Record<string, string> }; body: Record<string, unknown> }]
    expect(path).toBe('/api/v1/properties/{propertyId}/walk-ins')
    expect(init.params.header['Idempotency-Key']).toBeTruthy()
    expect(init.body).toMatchObject({ new_guest: { first_name: 'Wendy', last_name: 'Walker' }, room_id: 21, rate_plan_id: 3, departure_date: '2026-10-01', adult_count: 1 })
    expect(init.body.guest_id).toBeUndefined()
    expect(push).toHaveBeenCalledWith('/stays/55')
  })

  it('walks in an existing guest', async () => {
    const { w } = mountView()
    await flushPromises()
    await w.get('input[name=guest_q]').setValue('siti')
    await w.get('[data-testid=find-guest]').trigger('click')
    await flushPromises()
    await w.get('[data-testid=guest-G1]').trigger('click')
    expect(w.get('[data-testid=chosen-guest]').text()).toContain('Siti Nurhaliza')
    await w.get('form[data-testid=walkin-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1]).toMatchObject({ body: { guest_id: 3 } })
    expect((POST.mock.calls[0]?.[1] as { body: { new_guest?: unknown } }).body.new_guest).toBeUndefined()
  })
})
