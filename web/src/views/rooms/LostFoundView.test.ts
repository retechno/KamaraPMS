import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import LostFoundView from './LostFoundView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))

const item = (over: Record<string, unknown>) => ({
  id: 1, item_number: 'LF000001', description: 'Black phone charger', category: 'ELECTRONICS', room_id: 11, room_number: '101', found_on: '2026-09-30',
  found_at: '2026-09-30T01:00:00Z', found_by: 5, finder_name: 'Dewi', storage_location: 'Shelf A', possible_owner: 'the guest of 101', status: 'STORED',
  closed_on: null, closed_at: null, ...over,
})
const items = [
  item({}),
  item({ id: 2, item_number: 'LF000002', description: 'Blue towel', category: 'CLOTHING', room_id: null, room_number: undefined, location: 'Pool', possible_owner: undefined }),
  item({ id: 3, item_number: 'LF000003', description: 'Gold watch', status: 'RETURNED', claimant_name: 'Siti', claimant_proof: 'KTP', closed_on: '2026-09-30', closed_at: '2026-09-30T05:00:00Z' }),
]
const owners = [{ stay_id: 5, stay_number: 'STY000005', guest_name: 'Siti Nurhaliza', arrival_date: '2026-09-28', departure_date: '2026-09-30', stay_status: 'OPEN', phone: '0812' }]
const ALL = ['lostfound.report', 'lostfound.manage', 'reservation.read']

function mountView(permissions = ALL) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/possible-owners')) return { data: { data: owners } }
    if (path.endsWith('/housekeeping')) return { data: { data: [{ room_id: 11, room_number: '101', room_type_code: 'DLX' }, { room_id: 12, room_number: '102', room_type_code: 'DLX' }] } }
    return { data: { data: items } }
  })
  POST = vi.fn().mockResolvedValue({ data: item({}) })
  PATCH = vi.fn().mockResolvedValue({ data: item({}) })
  return mount(LostFoundView, { global: { plugins: [pinia] } })
}

describe('LostFoundView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
    PATCH = vi.fn()
  })

  it('lists stored items by default with the room or place', async () => {
    const w = mountView()
    await flushPromises()
    const list = GET.mock.calls.find(([p]) => (p as string).endsWith('/lost-found'))
    expect(list?.[1].params.query.status).toBe('STORED')
    expect(w.get('[data-testid=item-LF000001]').text()).toContain('Room 101')
    expect(w.get('[data-testid=item-LF000002]').text()).toContain('Pool')
    expect(w.get('[data-testid=item-LF000003]').classes()).toContain('closed')
    expect(w.get('[data-testid=item-LF000003]').text()).toContain('Siti')
  })

  it('searches and filters', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('input[name=q]').setValue(' charger ')
    await w.get('select[name=status]').setValue('')
    await flushPromises()
    const last = GET.mock.calls.filter(([p]) => (p as string).endsWith('/lost-found')).at(-1)
    expect(last?.[1].params.query).toEqual({ limit: 50, cursor: undefined, q: 'charger' })
  })

  it('records an item found in a room', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=new-item]').trigger('click')
    await w.get('select[name=room_id]').setValue(12)
    await w.get('select[name=category]').setValue('JEWELRY')
    await w.get('input[name=description]').setValue('Silver ring')
    await w.get('input[name=storage_location]').setValue('Safe')
    await w.get('[data-testid=item-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body).toEqual({
      room_id: 12, location: undefined, category: 'JEWELRY', description: 'Silver ring', storage_location: 'Safe', possible_owner: undefined, notes: undefined,
    })
    expect(w.get('[data-testid=notice]').text()).toContain('recorded')
    expect(w.find('[data-testid=item-form]').exists()).toBe(false)
  })

  it('keeps the form and shows the error when recording fails', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=new-item]').trigger('click')
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'VALIDATION_FAILED', detail: 'the item is invalid', errors: [{ field: 'description', code: 'REQUIRED', message: '1-500 characters' }] } as never))
    await w.get('[data-testid=item-form]').trigger('submit')
    await flushPromises()
    expect(w.get('.error-text').text()).toContain('1-500 characters')
    expect(w.find('[data-testid=item-form]').exists()).toBe(true)
  })

  it('suggests who had the room and hands the item to that guest', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=select-LF000001]').trigger('click')
    await flushPromises()
    expect(GET.mock.calls.some(([p]) => (p as string).endsWith('/possible-owners'))).toBe(true)
    expect(w.get('[data-testid=owners]').text()).toContain('Siti Nurhaliza')
    await w.get('[data-testid=owner-STY000005]').trigger('click')
    expect((w.get('input[name=claimant_name]').element as HTMLInputElement).value).toBe('Siti Nurhaliza')
    expect((w.get('input[name=claimant_proof]').element as HTMLInputElement).value).toBe('Stay STY000005')
    await w.get('[data-testid=return-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/lost-found/{id}/return')
    expect(POST.mock.calls[0]?.[1].body).toEqual({ claimant_name: 'Siti Nurhaliza', claimant_proof: 'Stay STY000005', note: undefined })
  })

  it('needs who takes it to hand back and a reason to dispose, and shows a refusal', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=select-LF000002]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid=owners]').exists()).toBe(false) // an item found at a place has no room guests
    await w.get('[data-testid=return]').trigger('click')
    expect((w.get('[data-testid=return-submit]').element as HTMLButtonElement).disabled).toBe(true)
    await w.get('[data-testid=dispose]').trigger('click')
    expect((w.get('[data-testid=dispose-submit]').element as HTMLButtonElement).disabled).toBe(true)
    await w.get('input[name=reason]').setValue('Unclaimed after 90 days')
    POST.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'ITEM_NOT_STORED', detail: 'already closed' }))
    await w.get('[data-testid=dispose-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1].body).toEqual({ reason: 'Unclaimed after 90 days' })
    expect(w.get('[data-testid=lf-error]').text()).toContain('ITEM_NOT_STORED')
  })

  it('changes where an item is kept', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=select-LF000001]').trigger('click')
    await w.get('input[name=storage]').setValue('Safe')
    await w.get('[data-testid=save-storage]').trigger('click')
    await flushPromises()
    expect(PATCH).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/lost-found/{id}', { params: { path: { propertyId: 7, id: 1 } }, body: { storage_location: 'Safe' } })
  })

  it('shows only what the role may do', async () => {
    const finder = mountView(['lostfound.report'])
    await flushPromises()
    expect(finder.find('[data-testid=new-item]').exists()).toBe(true)
    await finder.get('[data-testid=select-LF000001]').trigger('click')
    await flushPromises()
    expect(finder.find('[data-testid=return]').exists()).toBe(false)
    expect(GET.mock.calls.some(([p]) => (p as string).endsWith('/possible-owners'))).toBe(false) // needs reservation.read
    const closed = mountView()
    await flushPromises()
    await closed.get('[data-testid=select-LF000003]').trigger('click')
    expect(closed.find('[data-testid=return]').exists()).toBe(false) // already handed back
    const none = mountView([])
    await flushPromises()
    expect(none.find('[data-testid=no-access]').exists()).toBe(true)
  })
})
