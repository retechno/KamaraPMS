import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import FreeNightQuotasView from './FreeNightQuotasView.vue'

let GET = vi.fn()
let PUT = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), PUT: (...a: unknown[]) => PUT(...a) } }))

function mountView(permissions = ['rate.manage']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: { data: [{ occupancy_kind: 'COMPLIMENTARY', monthly_nights: 30 }] } })
  PUT = vi.fn().mockResolvedValue({})
  return mount(FreeNightQuotasView, { global: { plugins: [pinia] } })
}

describe('FreeNightQuotasView', () => {
  beforeEach(() => {
    GET = vi.fn()
    PUT = vi.fn()
  })

  it('shows the limit of each kind, empty when there is none', async () => {
    const w = mountView()
    await flushPromises()
    expect((w.get('input[name=monthly_COMPLIMENTARY]').element as HTMLInputElement).value).toBe('30')
    expect((w.get('input[name=monthly_HOUSE_USE]').element as HTMLInputElement).value).toBe('')
    expect(w.get('[data-testid=save-COMPLIMENTARY]').attributes('disabled')).toBeDefined() // nothing changed yet
  })

  it('sets a limit and removes one', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('input[name=monthly_HOUSE_USE]').setValue('10')
    await w.get('[data-testid=quota-HOUSE_USE] form').trigger('submit')
    await flushPromises()
    expect(PUT).toHaveBeenCalledWith('/api/v1/properties/{propertyId}/free-night-quotas/{kind}', { params: { path: { propertyId: 7, kind: 'HOUSE_USE' } }, body: { monthly_nights: 10 } })
    expect(w.get('[data-testid=notice]').text()).toContain('10')
    await w.get('input[name=monthly_COMPLIMENTARY]').setValue('')
    await w.get('[data-testid=quota-COMPLIMENTARY] form').trigger('submit')
    await flushPromises()
    expect(PUT.mock.calls[1]).toEqual(['/api/v1/properties/{propertyId}/free-night-quotas/{kind}', { params: { path: { propertyId: 7, kind: 'COMPLIMENTARY' } }, body: { monthly_nights: null } }])
  })

  it('is read-only without rate.manage', async () => {
    const w = mountView(['reservation.read'])
    await flushPromises()
    expect(w.find('[data-testid=read-only]').exists()).toBe(true)
    expect(w.find('[data-testid=save-COMPLIMENTARY]').exists()).toBe(false)
  })
})
