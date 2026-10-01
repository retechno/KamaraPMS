import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import CityLedgerView from './CityLedgerView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

const acme = { company_id: 1, code: 'ACME', name: 'Acme Corp', is_active: true, credit_limit: '1000000', payment_terms_days: 30, transferred: '300000', received: '100000', balance: '200000', available: '800000' }
const free = { company_id: 2, code: 'FREE', name: 'Free Ltd', is_active: false, credit_limit: null, payment_terms_days: 30, transferred: '5', received: '0', balance: '5', available: null }

function mountView(permissions = ['cityledger.read']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: { data: [acme, free] } })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(CityLedgerView, { global: { plugins: [pinia, router] } })
}

describe('CityLedgerView', () => {
  beforeEach(() => {
    GET = vi.fn()
  })

  it('lists what companies owe, only those that owe by default', async () => {
    const w = mountView()
    await flushPromises()
    expect(GET.mock.calls[0]?.[1].params.query.owing).toBe(true)
    expect(w.get('[data-testid=account-ACME]').text()).toContain('200000')
    expect(w.get('[data-testid=account-ACME] a').attributes('href')).toBe('/city-ledger/1')
    expect(w.get('[data-testid=account-FREE]').text()).toContain('No limit')
    expect(w.get('[data-testid=account-FREE]').text()).toContain('(inactive)')
    await w.get('input[name=owing]').setValue(false)
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1].params.query.owing).toBeUndefined()
  })

  it('searches by code or name', async () => {
    const w = mountView()
    await flushPromises()
    await w.get('input[name=q]').setValue('acm')
    await w.get('[data-testid=filters]').trigger('submit')
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1].params.query.q).toBe('acm')
  })

  it('says so when nobody owes, and needs cityledger.read', async () => {
    const w = mountView()
    GET.mockResolvedValue({ data: { data: [] } })
    await w.get('input[name=owing]').setValue(false)
    await w.get('input[name=owing]').setValue(true)
    await flushPromises()
    expect(w.get('[data-testid=empty]').text()).toContain('No company owes anything')
    const denied = mountView([])
    await flushPromises()
    expect(denied.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })
})
