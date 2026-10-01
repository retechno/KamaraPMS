import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import ReportsView from './ReportsView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const revenue = {
  from: '2026-09-24', to: '2026-09-30',
  by_charge_code: [
    { charge_code: 'ROOM', name: 'Room', charge_type: 'ROOM', revenue_account_code: '4-1100', items: 3, net_amount: '3000000', service_charge: '0', tax: '0', total: '3000000' },
    { charge_code: 'MINIBAR', name: 'Minibar', charge_type: 'OTHER', revenue_account_code: null, items: 1, net_amount: '100000', service_charge: '10000', tax: '12100', total: '122100' },
  ],
  by_charge_type: [], totals: { items: 4, net_amount: '3100000', service_charge: '10000', tax: '12100', total: '3122100' },
}

function mountView(permissions = ['report.view']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  const property = usePropertyStore()
  property.currentId = 7
  property.clock = { business_date: '2026-09-30' } as never
  GET = vi.fn().mockResolvedValue({ data: revenue })
  return mount(ReportsView, { global: { plugins: [pinia] } })
}

describe('ReportsView', () => {
  beforeEach(() => {
    GET = vi.fn()
  })

  it('runs a range report for the last week up to the business date and shows rows and totals', async () => {
    const w = mountView()
    await flushPromises()
    expect((w.get('input[name=from]').element as HTMLInputElement).value).toBe('2026-09-24')
    expect((w.get('input[name=to]').element as HTMLInputElement).value).toBe('2026-09-30')
    await w.get('[data-testid=filters]').trigger('submit')
    await flushPromises()
    expect(GET.mock.calls[0]).toEqual(['/api/v1/properties/{propertyId}/reports/revenue', { params: { path: { propertyId: 7 }, query: { from: '2026-09-24', to: '2026-09-30' } } }])
    expect(w.get('[data-testid=result]').text()).toContain('MINIBAR · Minibar')
    expect(w.get('[data-testid=result]').text()).toContain('4-1100')
    expect(w.get('[data-testid=footer]').text()).toContain('3122100')
  })

  it('uses a date for dated reports and no input for in-house, and says when there is nothing', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('select[name=report]').setValue('departures')
    expect(w.find('input[name=from]').exists()).toBe(false)
    GET.mockResolvedValue({ data: { date: '2026-09-30', rows: [] } })
    await w.get('input[name=date]').setValue('2026-10-01')
    await w.get('[data-testid=filters]').trigger('submit')
    await flushPromises()
    expect(GET.mock.calls[0]?.[0]).toBe('/api/v1/properties/{propertyId}/reports/departures')
    expect(GET.mock.calls[0]?.[1]).toMatchObject({ params: { query: { date: '2026-10-01' } } })
    expect(w.find('[data-testid=empty]').exists()).toBe(true)
    await w.get('select[name=report]').setValue('in-house')
    expect(w.find('input[name=date]').exists()).toBe(false)
    expect(w.find('input[name=from]').exists()).toBe(false)
  })

  it('downloads the CSV through the same query with format=csv', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('[data-testid=filters]').trigger('submit')
    await flushPromises()
    URL.createObjectURL = vi.fn().mockReturnValue('blob:x')
    URL.revokeObjectURL = vi.fn()
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
    GET.mockResolvedValue({ data: 'charge_type,charge_code\nROOM,ROOM\n' })
    await w.get('[data-testid=csv]').trigger('click')
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1]).toMatchObject({ parseAs: 'text', params: { query: { from: '2026-09-24', to: '2026-09-30', format: 'csv' } } })
    expect(click).toHaveBeenCalledTimes(1)
  })

  it('shows the server refusal and needs the permission', async () => {
    const w = mountView()
    await flushPromises()
    GET.mockRejectedValue(new ApiError({ type: 't', title: 'Invalid', status: 422, code: 'VALIDATION_FAILED', detail: 'range' }))
    await w.get('[data-testid=filters]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-testid=form-error]').text()).toContain('VALIDATION_FAILED')
    expect(w.find('[data-testid=result]').exists()).toBe(false)
    const denied = mountView(['guest.read'])
    await flushPromises()
    expect(denied.find('[data-testid=no-access]').exists()).toBe(true)
  })
})
