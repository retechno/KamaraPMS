import { DOMWrapper, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { resetDepartments } from '@/composables/useDepartments'
import { setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import RequisitionView from './RequisitionView.vue'

let GET = vi.fn()
let POST = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a), POST: (...a: unknown[]) => POST(...a), PUT: vi.fn(), PATCH: vi.fn() } }))

const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
const mounted: VueWrapper[] = []
const item = {
  id: 1, code: 'BERAS', name: 'Beras', stock_unit_id: 10, stock_unit_code: 'kg', is_active: true, default_supplier_id: null,
  units: [{ unit_id: 10, unit_code: 'kg', stock_num: 1, stock_den: 1, is_purchase: true, is_default_purchase: true }], stock: [],
}
const warehouses = [{ id: 1, code: 'MAIN', name: 'Main store', kind: 'MAIN', is_active: true }, { id: 2, code: 'BAR', name: 'Bar store', kind: 'DEPARTMENT', is_active: true }]
const body = (sel: string) => new DOMWrapper(document.body.querySelector(sel) as Element)

async function mountNew(): Promise<void> {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, email: 'm@hotel.test', is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions: ['inventory.view', 'inventory.request', 'inventory.move'] }] } as never
  usePropertyStore().currentId = 7
  usePropertyStore().current = { id: 7, currency_decimals: 0 } as never
  resetDepartments()
  GET = vi.fn(async (path: string) => {
    if (path.endsWith('/inventory/warehouses')) return { data: { data: warehouses } }
    if (path.endsWith('/inventory/items/{id}')) return { data: item }
    if (path.endsWith('/inventory/items')) return { data: { items: [item] } }
    if (path.endsWith('/inventory/suppliers')) return { data: { data: [{ id: 2, name: 'Sumber' }] } }
    if (path.endsWith('/inventory/settings')) return { data: { po_requires_pr: false } }
    if (path.endsWith('/departments')) return { data: { data: [{ id: 3, code: 'FB', name: 'Food and beverage', level: 1, is_active: true }] } }
    return { data: { data: [] } }
  })
  POST = vi.fn().mockResolvedValue({ data: { id: 9 } })
  await router.push('/inventory/requisitions/new')
  await router.isReady()
  mounted.push(mount(RequisitionView as never, { attachTo: document.body, global: { plugins: [pinia, router] } }))
  await flushPromises()
  await flushPromises()
}

beforeEach(() => setLocale('en'))
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount()
  document.body.innerHTML = ''
})

describe('RequisitionView, what is seen is what is sent', () => {
  it('does not save or submit the requisition while the needed date is half typed, and says why', async () => {
    await mountNew()
    ;(document.body.querySelector('[data-testid=line-add]') as HTMLElement).click()
    await flushPromises()
    await body('input[name=qty_0]').setValue('5')
    expect((body('[data-testid=sr-submit]').element as HTMLButtonElement).disabled).toBe(false)
    await body('[data-testid=sr-form] input[data-slot=date-input-text]').setValue('3/2')
    await body('[data-testid=sr-form]').trigger('submit')
    await flushPromises()
    expect(POST.mock.calls.length).toBe(0)
    expect(document.body.textContent).toContain('Write as dd/mm/yyyy')
    ;(body('[data-testid=sr-submit]').element as HTMLElement).click()
    await flushPromises()
    expect(POST.mock.calls.length).toBe(0)
  })
})
