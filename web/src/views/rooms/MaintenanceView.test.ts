import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import MaintenanceView from './MaintenanceView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))

const request = (over: Record<string, unknown>) => ({
  id: 1, request_number: 'MNT000001', room_id: 11, room_number: '101', category: 'PLUMBING', description: 'Shower leaks', priority: 'HIGH', status: 'OPEN',
  business_date: '2026-09-30', reported_by: 5, reporter_name: 'Dewi', reported_at: '2026-09-30T01:00:00Z', assigned_to: null, started_at: null, closed_at: null, block: null, ...over,
})
const requests = [
  request({}),
  request({ id: 2, request_number: 'MNT000002', room_id: null, room_number: undefined, location: 'Lobby', category: 'ELECTRICAL', description: 'Lamp is out', priority: 'URGENT', status: 'IN_PROGRESS', assigned_to: 9, assignee_name: 'Joko' }),
  request({ id: 3, request_number: 'MNT000003', status: 'RESOLVED', closed_at: '2026-09-30T05:00:00Z', resolution_note: 'Replaced the seal' }),
  request({ id: 4, request_number: 'MNT000004', room_number: '102', block: { id: 7, block_type: 'OOO', start_date: '2026-09-30', end_date: '2026-10-03', status: 'ACTIVE' } }),
]
const staff = [{ id: 9, full_name: 'Joko', email: 'joko@hotel.test' }]
const ALL = ['maintenance.report', 'maintenance.manage', 'room_block.manage']

function mountView(permissions = ALL, query: Record<string, string> = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-09-30' } as never
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/maintenance-staff')) return { data: { data: staff } }
    if (path.endsWith('/housekeeping')) return { data: { data: [{ room_id: 11, room_number: '101', room_type_code: 'DLX' }, { room_id: 12, room_number: '102', room_type_code: 'DLX' }] } }
    return { data: { data: requests } }
  })
  POST = vi.fn().mockResolvedValue({ data: request({}) })
  PATCH = vi.fn().mockResolvedValue({ data: request({}) })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  void router.push({ path: '/maintenance', query })
  return router.isReady().then(() => mount(MaintenanceView, { global: { plugins: [pinia, router] } }))
}

describe('MaintenanceView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
    PATCH = vi.fn()
  })

  it('lists open requests first, with the place, priority and any room block', async () => {
    const w = await mountView()
    await flushPromises()
    expect(GET.mock.calls.find(([p]) => (p as string).endsWith('/maintenance-requests'))?.[1].params.query.open).toBe(true)
    expect(w.get('[data-testid=request-MNT000001]').text()).toContain('Room 101')
    expect(w.get('[data-testid=request-MNT000002]').text()).toContain('Lobby')
    expect(w.get('[data-testid=request-MNT000002]').text()).toContain('Joko')
    expect(w.get('[data-testid=request-MNT000004] [data-testid=blocked]').text()).toContain('OOO until 2026-10-03')
    expect(w.get('[data-testid=request-MNT000003]').classes()).toContain('closed')
  })

  it('reports a problem against a room', async () => {
    const w = await mountView()
    await flushPromises()
    await w.get('[data-testid=new-request]').trigger('click')
    await w.get('select[name=room_id]').setValue(12)
    await w.get('select[name=category]').setValue('AC')
    await w.get('input[name=description]').setValue('Too warm')
    POST.mockResolvedValue({ data: request({ id: 9, request_number: 'MNT000009' }) })
    await w.get('[data-testid=request-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body).toEqual({ room_id: 12, location: undefined, category: 'AC', description: 'Too warm', priority: 'NORMAL' })
    expect(w.get('[data-testid=notice]').text()).toContain('MNT000009')
    expect(w.find('[data-testid=request-form]').exists()).toBe(false)
  })

  it('opens the form for a room when it comes from the housekeeping board', async () => {
    const w = await mountView(ALL, { room: '12' })
    await flushPromises()
    expect((w.get('select[name=room_id]').element as HTMLSelectElement).value).toBe('12')
  })

  it('shows field errors of a report', async () => {
    const w = await mountView()
    await flushPromises()
    await w.get('[data-testid=new-request]').trigger('click')
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'VALIDATION_FAILED', detail: 'the request is invalid', errors: [{ field: 'description', code: 'REQUIRED', message: '1-1000 characters' }] } as never))
    await w.get('[data-testid=request-form]').trigger('submit')
    await flushPromises()
    expect(w.get('.error-text').text()).toContain('1-1000 characters')
  })

  it('assigns, starts and resolves a request', async () => {
    const w = await mountView()
    await flushPromises()
    await w.get('[data-testid=select-MNT000001]').trigger('click')
    await w.get('select[name=assign_to]').setValue(9)
    await w.get('[data-testid=assign]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/maintenance-requests/{id}/assign')
    expect(POST.mock.calls[0]?.[1]).toMatchObject({ params: { path: { propertyId: 7, id: 1 } }, body: { user_id: 9 } })
    await w.get('[data-testid=select-MNT000001]').trigger('click')
    await w.get('[data-testid=start]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[1]?.[0]).toBe('/api/v1/properties/{propertyId}/maintenance-requests/{id}/start')
    await w.get('[data-testid=select-MNT000001]').trigger('click')
    await w.get('[data-testid=resolve]').trigger('click')
    await w.get('input[name=note]').setValue('Replaced the seal')
    await w.get('[data-testid=close-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[2]?.[0]).toBe('/api/v1/properties/{propertyId}/maintenance-requests/{id}/resolve')
    expect(POST.mock.calls[2]?.[1].body).toEqual({ note: 'Replaced the seal', release_block: false })
  })

  it('needs a reason to cancel, and offers to put a blocked room back on sale', async () => {
    const w = await mountView()
    await flushPromises()
    await w.get('[data-testid=select-MNT000004]').trigger('click')
    await w.get('[data-testid=cancel]').trigger('click')
    expect((w.get('[data-testid=close-submit]').element as HTMLButtonElement).disabled).toBe(true)
    expect(w.find('input[name=release_block]').exists()).toBe(true)
    await w.get('input[name=note]').setValue('Duplicate')
    await w.get('[data-testid=close-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/maintenance-requests/{id}/cancel')
    expect(POST.mock.calls[0]?.[1].body).toEqual({ note: 'Duplicate', release_block: true })
  })

  it('blocks the room of a request, and shows a refusal', async () => {
    const w = await mountView()
    await flushPromises()
    await w.get('[data-testid=select-MNT000001]').trigger('click')
    expect((w.get('input[name=block_end]').element as HTMLInputElement).value).toBe('2026-10-01')
    await w.get('select[name=block_type]').setValue('OOS')
    await w.get('input[name=block_end]').setValue('2026-10-05')
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'ROOM_BLOCK_CONFLICT', detail: 'a stay holds the room' }))
    await w.get('[data-testid=block-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body).toEqual({ block_type: 'OOS', end_date: '2026-10-05' })
    expect(w.get('[data-testid=mt-error]').text()).toContain('ROOM_BLOCK_CONFLICT')
  })

  it('reopens a resolved request and cannot block a place or an already blocked room', async () => {
    const w = await mountView()
    await flushPromises()
    await w.get('[data-testid=select-MNT000003]').trigger('click')
    expect(w.find('[data-testid=resolve]').exists()).toBe(false)
    await w.get('[data-testid=reopen]').trigger('click')
    await flushPromises()
    expect(POST.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/maintenance-requests/{id}/reopen')
    await w.get('[data-testid=select-MNT000002]').trigger('click')
    expect(w.find('[data-testid=block-form]').exists()).toBe(false) // a place has no room to block
    await w.get('[data-testid=select-MNT000004]').trigger('click')
    expect(w.find('[data-testid=block-form]').exists()).toBe(false) // already blocked
  })

  it('shows only what the role may do', async () => {
    const reporter = await mountView(['maintenance.report'])
    await flushPromises()
    expect(reporter.find('[data-testid=new-request]').exists()).toBe(true)
    await reporter.get('[data-testid=select-MNT000001]').trigger('click')
    expect(reporter.find('[data-testid=start]').exists()).toBe(false)
    expect(reporter.find('[data-testid=resolve]').exists()).toBe(false)
    expect(GET.mock.calls.some(([p]) => (p as string).endsWith('/maintenance-staff'))).toBe(false)
    const noBlock = await mountView(['maintenance.report', 'maintenance.manage'])
    await flushPromises()
    await noBlock.get('[data-testid=select-MNT000001]').trigger('click')
    expect(noBlock.find('[data-testid=block-form]').exists()).toBe(false) // room_block.manage is needed too
    const none = await mountView([])
    await flushPromises()
    expect(none.find('[data-testid=no-access]').exists()).toBe(true)
  })
})
