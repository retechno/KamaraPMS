import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import RateGridView from './RateGridView.vue'

let GET = vi.fn()
let PUT = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), PUT: (...a: unknown[]) => PUT(...a) } }))

const plans = [
  { id: 1, code: 'BAR', name: 'Best available', price_mode: 'EXCLUSIVE', room_charge_code: 'ROOM', is_active: true },
  { id: 2, code: 'NETT', name: 'Nett', price_mode: 'INCLUSIVE', room_charge_code: 'ROOM_NETT', is_active: true },
]
const types = [
  { id: 10, code: 'DLX', name: 'Deluxe', sort_order: 1, is_active: true },
  { id: 11, code: 'STD', name: 'Standard', sort_order: 2, is_active: true },
  { id: 12, code: 'OLD', name: 'Retired', sort_order: 3, is_active: false },
]
const grid = {
  rate_plan_id: 1, price_mode: 'EXCLUSIVE', room_charge_code: 'ROOM', from: '2026-10-01', to: '2026-10-15',
  rates: [
    { room_type_id: 10, stay_date: '2026-10-01', amount: '1000000' },
    { room_type_id: 11, stay_date: '2026-10-02', amount: '750000' },
  ],
}

function mountGrid(permissions: string[] = ['rate.manage']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-10-01' } as never
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/rate-plans')) return { data: { data: plans } }
    if (path.endsWith('/room-types')) return { data: { data: types } }
    return { data: grid }
  })
  PUT = vi.fn().mockResolvedValue({ data: { updated_nights: 8, created_nights: 5 } })
  return mount(RateGridView, { global: { plugins: [pinia] } })
}

describe('RateGridView', () => {
  beforeEach(() => {
    GET = vi.fn()
    PUT = vi.fn()
  })

  it('loads the grid of the first active plan for 14 days from the business date', async () => {
    const w = mountGrid()
    await flushPromises()
    const call = GET.mock.calls.find(([p]) => (p as string).endsWith('/rates'))
    expect(call?.[1]).toMatchObject({ params: { query: { rate_plan_id: 1, from: '2026-10-01', to: '2026-10-15' } } })
    expect(w.findAll('[data-testid^=row-]').map((r) => r.attributes('data-testid'))).toEqual(['row-DLX', 'row-STD']) // inactive types are not sold
    expect(w.get('[data-testid=cell-DLX-2026-10-01]').text()).toBe('1000000')
    expect(w.get('[data-testid=cell-STD-2026-10-02]').text()).toBe('750000')
    expect(w.get('[data-testid=cell-DLX-2026-10-02]').text()).toBe('—')
    expect(w.get('[data-testid=price-mode]').text()).toContain('exclusive: service and tax are added')
  })

  it('shows the price mode of the selected plan', async () => {
    const w = mountGrid()
    await flushPromises()
    GET.mockImplementation(async (path: string) => (path.endsWith('/rates') ? { data: { ...grid, rate_plan_id: 2, price_mode: 'INCLUSIVE', rates: [] } } : { data: { data: path.endsWith('/rate-plans') ? plans : types } }))
    await w.get('select[name=rate_plan]').setValue(2)
    await flushPromises()
    expect(w.get('[data-testid=price-mode]').text()).toContain('inclusive: service and tax are contained')
    expect(GET.mock.calls.at(-1)?.[1]).toMatchObject({ params: { query: { rate_plan_id: 2 } } })
  })

  it('prepares a single-night fill when a cell is clicked', async () => {
    const w = mountGrid()
    await flushPromises()
    await w.get('[data-testid=cell-STD-2026-10-02]').trigger('click')
    expect((w.get('input[name=from]').element as HTMLInputElement).value).toBe('2026-10-02')
    expect((w.get('input[name=to]').element as HTMLInputElement).value).toBe('2026-10-03')
    expect((w.get('input[name=amount]').element as HTMLInputElement).value).toBe('750000')
    expect((w.get('input[name=type-STD]').element as HTMLInputElement).checked).toBe(true)
    expect((w.get('input[name=type-DLX]').element as HTMLInputElement).checked).toBe(false)
  })

  it('sends the range, room types and weekdays, reports the count and reloads', async () => {
    const w = mountGrid()
    await flushPromises()
    await w.get('input[name=from]').setValue('2026-10-01')
    await w.get('input[name=to]').setValue('2026-11-01')
    await w.get('input[name=amount]').setValue('1750000')
    await w.get('input[name=weekday-FRI]').setValue(true)
    await w.get('input[name=weekday-SAT]').setValue(true)
    await w.get('input[name=type-STD]').setValue(false)
    const before = GET.mock.calls.length
    await w.get('[data-testid=fill-form]').trigger('submit')
    await flushPromises()
    expect(PUT).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/rates', {
      params: { path: { propertyId: 7 } },
      body: { rate_plan_id: 1, room_type_ids: [10], from: '2026-10-01', to: '2026-11-01', weekdays: ['FRI', 'SAT'], amount: '1750000' },
    })
    expect(w.get('[data-testid=notice]').text()).toBe('8 night(s) written, 5 of them new.')
    expect(GET.mock.calls.length).toBeGreaterThan(before)
  })

  it('shows field errors next to the inputs', async () => {
    const w = mountGrid()
    await flushPromises()
    PUT.mockRejectedValue(
      new ApiError({
        type: 't', title: 'Unprocessable Entity', status: 422, code: 'VALIDATION_FAILED', detail: 'the rates are invalid',
        errors: [{ field: 'amount', code: 'INVALID_AMOUNT', message: 'a non-negative amount' }],
      }),
    )
    await w.get('[data-testid=fill-form]').trigger('submit')
    await flushPromises()
    expect(w.get('input[name=amount]').attributes('aria-invalid')).toBe('true')
    expect(w.text()).toContain('a non-negative amount')
  })

  it('moves the window by a week', async () => {
    const w = mountGrid()
    await flushPromises()
    await w.findAll('.nav button')[2]?.trigger('click') // Week →
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1]).toMatchObject({ params: { query: { from: '2026-10-08', to: '2026-10-22' } } })
  })

  it('is read-only without rate.manage and explains when there are no plans', async () => {
    const w = mountGrid([])
    await flushPromises()
    expect(w.find('[data-testid=fill-form]').exists()).toBe(false)
    expect(w.get('[data-testid=cell-DLX-2026-10-01]').attributes('disabled')).toBeDefined()

    setActivePinia(createPinia())
    GET = vi.fn().mockResolvedValue({ data: { data: [] } })
    const empty = mount(RateGridView, { global: { plugins: [createPinia()] } })
    await flushPromises()
    expect(empty.find('[data-testid=grid]').exists()).toBe(false)
  })
})
