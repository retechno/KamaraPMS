import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import RestrictionsView from './RestrictionsView.vue'

let GET = vi.fn()
let PUT = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), PUT: (...a: unknown[]) => PUT(...a) } }))

const day = (date: string, over: object = {}) => ({
  date, stop_sell: false, closed_to_arrival: false, closed_to_departure: false, min_stay: null, max_stay: null, sources: {}, ...over,
})

function mountView(permissions = ['rate.manage']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'm@hotel.com', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-12-01' } as never
  return mount(RestrictionsView, { global: { plugins: [pinia] } })
}

describe('RestrictionsView', () => {
  beforeEach(() => {
    GET = vi.fn(async (path: string, opts?: { params?: { query?: { room_type_id?: number; from?: string; to?: string } } }) => {
      if (path.endsWith('/rate-plans')) return { data: { data: [{ id: 3, code: 'BAR', name: 'Best', is_active: true }, { id: 4, code: 'CORP', name: 'Corporate', is_active: true }] } }
      if (path.endsWith('/room-types')) return { data: { data: [{ id: 5, code: 'DLX', name: 'Deluxe', is_active: true, sort_order: 1 }, { id: 6, code: 'STD', name: 'Standard', is_active: true, sort_order: 2 }] } }
      if (path.endsWith('/rate-restrictions/effective')) {
        const q = opts?.params?.query
        const from = q?.from ?? '2026-12-01'
        const out = []
        for (let i = 0; i < 14; i++) {
          const d = new Date(Date.UTC(2026, 11, 1 + i)).toISOString().slice(0, 10)
          if (q?.room_type_id === 5 && d === '2026-12-03') out.push(day(d, { stop_sell: true, min_stay: 3, sources: { stop_sell: { row_id: 1, scope: 'ROOM_TYPE' }, min_stay: { row_id: 2, scope: 'PROPERTY' } } }))
          else if (q?.room_type_id === 5 && d === '2026-12-05') out.push(day(d, { closed_to_arrival: true, closed_to_departure: true, max_stay: 7 }))
          else out.push(day(d))
        }
        void from
        return { data: { data: out } }
      }
      return { data: {} }
    })
    PUT = vi.fn().mockResolvedValue({ data: { dates: 1, scopes: 1, created: 1, updated: 0, deleted: 0 } })
  })

  it('shows what each night is under for the first plan, with the marks and where they come from', async () => {
    const w = mountView()
    await flushPromises()
    const calls = GET.mock.calls.filter((c) => String(c[0]).endsWith('/rate-restrictions/effective'))
    expect(calls).toHaveLength(2) // one per room type
    expect(calls[0]?.[1]).toMatchObject({ params: { path: { propertyId: 7 }, query: { room_type_id: 5, rate_plan_id: 3, from: '2026-12-01', to: '2026-12-15' } } })
    const cell = w.get('[data-testid=cell-DLX-2026-12-03]')
    expect(cell.findAll('[data-testid^=mark-]').map((m) => m.text())).toEqual(['SS', '≥3'])
    expect(cell.attributes('title')).toContain('Stop sell: this room type')
    expect(cell.attributes('title')).toContain('Shortest stay: the whole property')
    expect(w.get('[data-testid=cell-DLX-2026-12-05]').findAll('[data-testid^=mark-]').map((m) => m.text())).toEqual(['CTA', 'CTD', '≤7'])
    expect(w.get('[data-testid=cell-STD-2026-12-03]').findAll('[data-testid^=mark-]')).toHaveLength(0)
  })

  it('reloads the grid for another plan and another window', async () => {
    const w = mountView()
    await flushPromises()
    GET.mockClear()
    await w.get('select[name=rate_plan]').setValue(4)
    await flushPromises()
    expect(GET.mock.calls.every((c) => (c[1] as { params: { query: { rate_plan_id: number } } }).params.query.rate_plan_id === 4)).toBe(true)
    GET.mockClear()
    await w.get('[data-testid=week-next]').trigger('click')
    await flushPromises()
    expect(GET.mock.calls[0]?.[1]).toMatchObject({ params: { query: { from: '2026-12-08', to: '2026-12-22' } } })
  })

  it('prepares a fill from a clicked night and sends what was chosen', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=cell-DLX-2026-12-05]').trigger('click')
    expect((w.get('input[name=from]').element as HTMLInputElement).value).toBe('2026-12-05')
    expect((w.get('input[name=to]').element as HTMLInputElement).value).toBe('2026-12-06')
    expect((w.get('input[name=type-DLX]').element as HTMLInputElement).checked).toBe(true)
    await w.get('select[name=stop_sell]').setValue('yes')
    await w.get('select[name=closed_to_departure]').setValue('clear')
    await w.get('input[name=min_stay]').setValue('2')
    await w.get('input[name=clear_max_stay]').setValue(true)
    await w.get('input[name=weekday-FRI]').setValue(true)
    GET.mockClear()
    await w.get('form[data-testid=fill-form]').trigger('submit')
    await flushPromises()
    expect(PUT).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/rate-restrictions', {
      params: { path: { propertyId: 7 } },
      body: {
        room_type_ids: [5], rate_plan_ids: undefined, from: '2026-12-05', to: '2026-12-06', weekdays: ['FRI'],
        set: { stop_sell: true, min_stay: 2 }, clear: ['closed_to_departure', 'max_stay'],
      },
    })
    expect(w.get('[data-testid=notice]').text()).toContain('1 added')
    expect(GET.mock.calls.some((c) => String(c[0]).endsWith('/rate-restrictions/effective'))).toBe(true) // the grid is read again
  })

  it('sends no scope when no room type or plan is ticked: that is all of them', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('input[name=from]').setValue('2026-12-20')
    await w.get('input[name=to]').setValue('2026-12-22')
    await w.get('select[name=closed_to_arrival]').setValue('no')
    await w.get('input[name=note]').setValue('Peak')
    await w.get('form[data-testid=fill-form]').trigger('submit')
    await flushPromises()
    expect(PUT.mock.calls[0]?.[1].body).toMatchObject({ room_type_ids: undefined, rate_plan_ids: undefined, set: { closed_to_arrival: false, note: 'Peak' }, clear: [] })
  })

  it('shows what the server refused beside the fields', async () => {
    PUT = vi.fn().mockRejectedValue(new ApiError({
      type: 't', title: 'x', status: 422, code: 'VALIDATION_FAILED', detail: 'invalid', errors: [{ field: 'set', code: 'NOTHING_TO_CHANGE', message: 'set or clear at least one attribute' }],
    }))
    const w = mountView()
    await flushPromises()
    await w.get('form[data-testid=fill-form]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('VALIDATION_FAILED')
    expect(w.get('form[data-testid=fill-form]').text()).toContain('set or clear at least one attribute')
  })

  it('is read only without rate.manage: the grid shows, there is no fill and a night cannot be picked', async () => {
    const w = mountView(['reservation.read'])
    await flushPromises()
    expect(w.find('form[data-testid=fill-form]').exists()).toBe(false)
    expect(w.get('[data-testid=read-only]').text()).toContain('rate.manage')
    expect(w.get('[data-testid=cell-DLX-2026-12-03]').attributes('disabled')).toBeDefined()
    expect(w.find('[data-testid=cell-DLX-2026-12-03] [data-testid=mark-stop_sell]').exists()).toBe(true)
  })

  it('says so when there is no rate plan', async () => {
    GET = vi.fn(async (path: string) => ({ data: { data: path.endsWith('/room-types') ? [] : [] } }))
    const w = mountView()
    await flushPromises()
    expect(w.find('[data-testid=no-plans]').exists()).toBe(true)
  })
})
