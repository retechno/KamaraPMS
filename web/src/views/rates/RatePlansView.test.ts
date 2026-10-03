import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import RatePlansView from './RatePlansView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PATCH = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PATCH: (...a: unknown[]) => PATCH(...a) } }))

const codes = [
  { id: 1, code: 'ROOM', name: 'Room charge', charge_type: 'ROOM', price_mode: 'EXCLUSIVE', is_active: true },
  { id: 2, code: 'ROOM_NETT', name: 'Room nett', charge_type: 'ROOM', price_mode: 'INCLUSIVE', is_active: true },
  { id: 3, code: 'ROOM_OLD', name: 'Old', charge_type: 'ROOM', price_mode: 'EXCLUSIVE', is_active: false },
]
const bar = {
  id: 5, code: 'BAR', name: 'Best available', meal_plan: 'BB', is_refundable: true, room_charge_code_id: 3, room_charge_code: 'ROOM_OLD',
  price_mode: 'EXCLUSIVE', occupancy_kind: 'PAID', is_active: true,
}

function mountView(permissions: string[] = ['rate.manage']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn(async (path: string) => ({ data: { data: path.endsWith('/rate-plans') ? [bar] : codes } }))
  POST = vi.fn().mockResolvedValue({ data: {} })
  PATCH = vi.fn().mockResolvedValue({ data: {} })
  return mount(RatePlansView, { global: { plugins: [pinia] } })
}

describe('RatePlansView', () => {
  beforeEach(() => {
    GET = vi.fn()
    POST = vi.fn()
    PATCH = vi.fn()
  })

  it('lists plans with meal plan, room charge code and price mode', async () => {
    const w = mountView()
    await flushPromises()
    expect(GET.mock.calls.some(([p, o]) => (p as string).endsWith('/charge-codes') && (o as { params: { query: { charge_type: string } } }).params.query.charge_type === 'ROOM')).toBe(true)
    expect(w.get('[data-testid=plan-BAR]').text()).toContain('BB')
    expect(w.get('[data-testid=plan-BAR]').text()).toContain('ROOM_OLD')
  })

  it('offers only active ROOM codes for a new plan and explains the price mode', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=new-plan]').trigger('click')
    expect(w.findAll('select[name=room_charge_code_id] option').map((o) => o.text())).toEqual(['ROOM · Room charge', 'ROOM_NETT · Room nett'])
    expect(w.get('[data-testid=mode-hint]').text()).toContain('exclusive prices (service and tax are added)')
    await w.get('select[name=room_charge_code_id]').setValue(2)
    expect(w.get('[data-testid=mode-hint]').text()).toContain('inclusive prices')
  })

  it('creates a plan', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=new-plan]').trigger('click')
    await w.get('input[name=code]').setValue('NETT')
    await w.get('input[name=name]').setValue('Nett rate')
    await w.get('select[name=meal_plan]').setValue('HB')
    await w.get('[data-testid=plan-form]').trigger('submit')
    await flushPromises()
    expect(POST).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/rate-plans', {
      params: { path: { propertyId: 7 } },
      body: {
        code: 'NETT', name: 'Nett rate', description: '', meal_plan: 'HB', cancellation_policy: '', is_refundable: true, room_charge_code_id: 1, occupancy_kind: 'PAID', is_active: true,
      },
    })
  })

  it('creates a complimentary plan, and the kind cannot be changed afterwards', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=new-plan]').trigger('click')
    expect(w.find('[data-testid=kind-hint]').exists()).toBe(false)
    await w.get('input[name=code]').setValue('COMP')
    await w.get('input[name=name]').setValue('Complimentary')
    await w.get('select[name=occupancy_kind]').setValue('COMPLIMENTARY')
    expect(w.find('[data-testid=kind-hint]').exists()).toBe(true)
    await w.get('[data-testid=plan-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls[0]?.[1]).toMatchObject({ body: { code: 'COMP', occupancy_kind: 'COMPLIMENTARY' } })
    await w.get('[data-testid=plan-BAR] button').trigger('click')
    expect((w.get('select[name=occupancy_kind]').element as HTMLSelectElement).disabled).toBe(true)
  })

  it('keeps an unusable current code selectable while editing, and shows a price mode conflict', async () => {
    const w = mountView()
    await flushPromises()
    PATCH.mockRejectedValue(new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'RATE_PLAN_PRICE_MODE_MISMATCH', detail: 'the plan already has rates' }))
    await w.get('[data-testid=plan-BAR] button').trigger('click')
    expect(w.findAll('select[name=room_charge_code_id] option').map((o) => o.text())).toContain('ROOM_OLD · Old')
    expect(w.get('input[name=code]').attributes('disabled')).toBeDefined()
    await w.get('select[name=room_charge_code_id]').setValue(2)
    await w.get('[data-testid=plan-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('RATE_PLAN_PRICE_MODE_MISMATCH')
    expect(w.get('[data-testid=form-error]').text()).toContain('Create a new plan for the other price mode')
  })

  it('is read-only without rate.manage', async () => {
    const w = mountView([])
    await flushPromises()
    expect(w.find('[data-testid=new-plan]').exists()).toBe(false)
    expect(w.get('[data-testid=read-only]').text()).toContain('rate.manage')
    expect(w.find('[data-testid=plan-BAR] button').exists()).toBe(false)
  })
})
