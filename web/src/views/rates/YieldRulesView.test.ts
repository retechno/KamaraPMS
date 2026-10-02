import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import YieldRulesView from './YieldRulesView.vue'

let GET = vi.fn()
let POST = vi.fn()
let PUT = vi.fn()
let DELETE = vi.fn()
vi.mock('@/api/client', () => ({
  api: {
    GET: (...a: unknown[]) => GET(...a),
    POST: (...a: unknown[]) => POST(...a),
    PUT: (...a: unknown[]) => PUT(...a),
    DELETE: (...a: unknown[]) => DELETE(...a),
  },
}))

const rule = (over: object = {}) => ({
  id: 1, code: 'BUSY', name: 'Busy nights', rate_plan_id: null, room_type_id: 5, stay_from: null, stay_to: null, weekdays: ['FRI', 'SAT'],
  occupancy_from: '70.00', occupancy_to: null, lead_days_min: null, lead_days_max: null, stay_nights_min: null, stay_nights_max: null,
  adjustment_type: 'PERCENT', adjustment_value: '20.0000', floor_amount: null, cap_amount: '1500000', priority: 10, is_active: true,
  created_at: '2026-09-30T00:00:00Z', updated_at: '2026-09-30T00:00:00Z', ...over,
})

const quote = {
  rate_plan_id: 3, room_type_id: 5, price_mode: 'EXCLUSIVE', total: '1320000', grid_total: '1000000', missing_nights: 0,
  nights: [{ date: '2026-10-03', grid_rate: '1000000', occupancy_percent: '72.00', amount: '1320000', steps: [{ code: 'BUSY', name: 'Busy nights', before: '1100000', after: '1320000' }] }],
}

function mountView(permissions = ['rate.manage']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'm@hotel.com', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  return mount(YieldRulesView, { global: { plugins: [pinia] } })
}

describe('YieldRulesView', () => {
  beforeEach(() => {
    GET = vi.fn(async (path: string) => {
      if (path.endsWith('/yield-rules')) return { data: { data: [rule(), rule({ id: 2, code: 'OFFSEASON', name: 'Low season', is_active: false, weekdays: null, occupancy_from: null, adjustment_value: '-10.0000', cap_amount: null })] } }
      if (path.endsWith('/rate-plans')) return { data: { data: [{ id: 3, code: 'BAR', name: 'Best' }] } }
      if (path.endsWith('/room-types')) return { data: { data: [{ id: 5, code: 'DLX', name: 'Deluxe' }] } }
      if (path.endsWith('/rate-quotes')) return { data: quote }
      return { data: {} }
    })
    POST = vi.fn().mockResolvedValue({ data: {} })
    PUT = vi.fn().mockResolvedValue({ data: {} })
    DELETE = vi.fn().mockResolvedValue({})
  })

  it('lists the rules in a readable form', async () => {
    const w = mountView()
    await flushPromises()
    const busy = w.get('[data-testid=rule-BUSY]').text()
    expect(busy).toContain('DLX')
    expect(busy).toContain('FRI SAT')
    expect(busy).toContain('occupancy 70.00% to 100%')
    expect(busy).toContain('+20.0000%')
    expect(w.get('[data-testid=rule-OFFSEASON]').text()).toContain('Off')
    expect(w.get('[data-testid=rule-OFFSEASON]').text()).toContain('-10.0000%')
  })

  it('creates a rule with its conditions and adjustment', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=new-rule]').trigger('click')
    await w.get('input[name=code]').setValue('LAST')
    await w.get('input[name=name]').setValue('Last minute')
    await w.get('input[name=lead_max]').setValue('2')
    await w.findAll('input[name=weekdays]')[0]!.setValue(true)
    await w.get('select[name=adjustment_type]').setValue('AMOUNT')
    await w.get('input[name=adjustment_value]').setValue('-50000')
    await w.get('input[name=floor_amount]').setValue('400000')
    await w.get('form[data-testid=rule-form]').trigger('submit')
    await flushPromises()
    const [path, init] = POST.mock.calls[0] as [string, { body: Record<string, unknown> }]
    expect(path).toBe('/api/v1/properties/{propertyId}/yield-rules')
    expect(init.body).toMatchObject({
      code: 'LAST', name: 'Last minute', rate_plan_id: null, room_type_id: null, weekdays: ['MON'], lead_days_min: null, lead_days_max: 2,
      adjustment_type: 'AMOUNT', adjustment_value: '-50000', floor_amount: '400000', cap_amount: null, occupancy_from: null, priority: 100, is_active: true,
    })
  })

  it('edits with the whole rule and cannot change its code', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=edit-BUSY]').trigger('click')
    expect((w.get('input[name=code]').element as HTMLInputElement).disabled).toBe(true)
    expect((w.get('input[name=occupancy_from]').element as HTMLInputElement).value).toBe('70.00')
    await w.get('input[name=adjustment_value]').setValue('25')
    await w.get('form[data-testid=rule-form]').trigger('submit')
    await flushPromises()
    const [path, init] = PUT.mock.calls[0] as [string, { params: { path: { id: number } }; body: Record<string, unknown> }]
    expect(path).toBe('/api/v1/properties/{propertyId}/yield-rules/{id}')
    expect(init.params.path.id).toBe(1)
    expect(init.body).toMatchObject({ code: 'BUSY', adjustment_value: '25', occupancy_from: '70.00', cap_amount: '1500000', weekdays: ['FRI', 'SAT'], room_type_id: 5 })
  })

  it('turns a rule off and deletes one after confirming', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=toggle-BUSY]').trigger('click')
    await flushPromises()
    expect((PUT.mock.calls[0] as [string, { body: Record<string, unknown> }])[1].body).toMatchObject({ code: 'BUSY', is_active: false })

    vi.spyOn(window, 'confirm').mockReturnValue(false)
    await w.get('[data-testid=delete-BUSY]').trigger('click')
    expect(DELETE).not.toHaveBeenCalled()
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    await w.get('[data-testid=delete-BUSY]').trigger('click')
    await flushPromises()
    expect((DELETE.mock.calls[0] as [string, { params: { path: { id: number } } }])[1].params.path.id).toBe(1)
  })

  it('shows the server field errors beside the fields', async () => {
    POST.mockRejectedValueOnce(new ApiError({
      type: 't', title: 'x', status: 422, code: 'VALIDATION_FAILED', detail: 'invalid', errors: [{ field: 'adjustment_value', code: 'OUT_OF_RANGE', message: 'a percentage above -100 and at most 1000' }],
    }))
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=new-rule]').trigger('click')
    await w.get('form[data-testid=rule-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('VALIDATION_FAILED')
    expect(w.get('form[data-testid=rule-form]').text()).toContain('a percentage above -100')
  })

  it('checks a price and shows the rules that moved it', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('select[name=quote_plan]').setValue(3)
    await w.get('select[name=quote_type]').setValue(5)
    await w.get('input[name=quote_arrival]').setValue('2026-10-03')
    await w.get('input[name=quote_departure]').setValue('2026-10-04')
    await w.get('form[data-testid=quote-form]').trigger('submit')
    await flushPromises()
    const call = GET.mock.calls.find((c) => (c[0] as string).endsWith('/rate-quotes')) as [string, { params: { query: object } }]
    expect(call[1].params.query).toEqual({ rate_plan_id: 3, room_type_id: 5, arrival_date: '2026-10-03', departure_date: '2026-10-04' })
    expect(w.get('[data-testid=quote]').text()).toContain('BUSY 1100000 → 1320000')
    expect(w.get('[data-testid=quote-total]').text()).toBe('1320000')
  })

  it('is read-only without rate.manage', async () => {
    const w = mountView(['folio.read'])
    await flushPromises()
    expect(w.find('[data-testid=new-rule]').exists()).toBe(false)
    expect(w.find('[data-testid=edit-BUSY]').exists()).toBe(false)
    expect(w.find('[data-testid=read-only]').exists()).toBe(true)
    expect(w.find('[data-testid=rule-BUSY]').exists()).toBe(true)
  })
})
