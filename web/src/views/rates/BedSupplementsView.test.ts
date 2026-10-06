import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import BedSupplementsView from './BedSupplementsView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a) } }))

const row = (over: object = {}) => ({
  id: 1, rate_plan_id: 3, room_type_id: 5, room_type_code: 'DLX', bed_type_id: 8, bed_type_code: 'KING', bed_type_name: 'King',
  adjust_kind: 'AMOUNT', amount: '50000', effective_from: '2026-10-01', in_force: true, created_at: '2026-09-30T00:00:00Z', ...over,
})

function mountView(permissions = ['rate.manage']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'm@hotel.com', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-10-02' } as never
  return mount(BedSupplementsView, { global: { plugins: [pinia] } })
}

describe('BedSupplementsView', () => {
  beforeEach(() => {
    GET = vi.fn(async (path: string) => {
      if (path.endsWith('/bed-adjustments')) return { data: { data: [row({ id: 2, adjust_kind: 'PERCENT', amount: '10', effective_from: '2026-11-01', in_force: false }), row()] } }
      if (path.endsWith('/rate-plans')) return { data: { data: [{ id: 3, code: 'BAR', name: 'Best', occupancy_kind: 'PAID' }, { id: 4, code: 'COMP', name: 'Free', occupancy_kind: 'COMPLIMENTARY' }] } }
      if (path.endsWith('/room-types')) return { data: { data: [{ id: 5, code: 'DLX', name: 'Deluxe' }] } }
      if (path.endsWith('/bed-types')) return { data: { data: [{ id: 8, code: 'KING', name: 'King', is_active: true }, { id: 9, code: 'TWIN', name: 'Twin', is_active: true }] } }
      return { data: {} }
    })
    POST = vi.fn().mockResolvedValue({ data: {} })
  })

  it('lists the supplements of the first paid plan and marks the one in force', async () => {
    const w = mountView()
    await flushPromises()
    expect(w.findAll('select[name=rate_plan_id] option').map((o) => o.text())).toEqual(['BAR · Best']) // a free plan has no supplement
    expect(GET).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/rate-plans/{id}/bed-adjustments', { params: { path: { propertyId: 7, id: 3 } } })
    expect(w.get('[data-testid=supplement-1]').text()).toContain('+50000')
    expect(w.get('[data-testid=supplement-1]').text()).toContain('In force')
    expect(w.get('[data-testid=supplement-2]').text()).toContain('+10%')
    expect(w.get('[data-testid=supplement-2]').text()).not.toContain('In force')
  })

  it('adds a supplement from a date', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=new-supplement]').trigger('click')
    expect((w.get('input[name=effective_from]').element as HTMLInputElement).value).toBe('2026-10-02')
    await w.get('select[name=bed_type_id]').setValue(9)
    await w.get('select[name=adjust_kind]').setValue('PERCENT')
    await w.get('input[name=amount]').setValue('-5')
    await w.get('form[data-testid=supplement-form]').trigger('submit')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/rate-plans/{id}/bed-adjustments', {
      params: { path: { propertyId: 7, id: 3 } },
      body: { room_type_id: 5, bed_type_id: 9, adjust_kind: 'PERCENT', amount: '-5', effective_from: '2026-10-02' },
    })
  })

  it('shows what the server refused', async () => {
    POST = vi.fn().mockRejectedValue(new ApiError({ type: 't', title: 'A supplement already starts on this date.', status: 409, code: 'BED_ADJUSTMENT_EXISTS', detail: 'exists' }))
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=new-supplement]').trigger('click')
    await w.get('input[name=amount]').setValue('1')
    await w.get('form[data-testid=supplement-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('BED_ADJUSTMENT_EXISTS')
  })

  it('is read only without rate.manage', async () => {
    const w = mountView(['reservation.read'])
    await flushPromises()
    expect(w.find('[data-testid=new-supplement]').exists()).toBe(false)
    expect(w.get('[data-testid=read-only]').text()).toContain('rate.manage')
    expect(w.find('[data-testid=supplement-1]').exists()).toBe(true)
  })
})
