import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import AccountingReadinessView from './AccountingReadinessView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

async function mountView(readiness: unknown, permissions = ['accounting.view']) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'a@b.c', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: readiness })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  const w = mount(AccountingReadinessView, { global: { plugins: [pinia, router] } })
  await flushPromises()
  return w
}

// Item 10 of PR C2: GET .../accounting/readiness had no screen, so a blocked night audit could only be read as raw codes.
describe('AccountingReadinessView', () => {
  it('reads the readiness of the property and lists its blockers and warnings', async () => {
    const w = await mountView({ ready: false, status: 'NOT_READY', blockers: [{ code: 'ACCOUNT_MAP_MISSING', ref: 'CASH', message: '' }], warnings: [{ code: 'CODE_UNMAPPED', ref: 'SPA', message: '' }] })
    expect(GET.mock.calls[0]![0]).toBe('/api/v1/properties/{propertyId}/accounting/readiness')
    expect(GET.mock.calls[0]![1].params.path.propertyId).toBe(7)
    expect(w.get('[data-testid=readiness-status]').text()).toContain('Not ready')
    expect(w.get('[data-testid=readiness-blockers] [data-testid=readiness-ACCOUNT_MAP_MISSING]').text()).toContain('CASH')
    expect(w.get('[data-testid=readiness-warnings] [data-testid=readiness-CODE_UNMAPPED]').text()).toContain('SPA')
  })

  it('says that nothing stops the night audit when the property is ready', async () => {
    const w = await mountView({ ready: true, status: 'READY', blockers: [], warnings: [] })
    expect(w.get('[data-testid=readiness-status]').text()).toContain('Ready')
    expect(w.get('[data-testid=readiness-ok]').text()).toContain('Nothing in the accounting stops the night audit.')
    expect(w.find('[data-testid=readiness-warnings]').exists()).toBe(false)
  })

  it('reads it again on demand', async () => {
    const w = await mountView({ ready: true, status: 'READY', blockers: [], warnings: [] })
    await w.get('[data-testid=readiness-reload]').trigger('click')
    await flushPromises()
    expect(GET).toHaveBeenCalledTimes(2)
  })
})
