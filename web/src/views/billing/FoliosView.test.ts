import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import FoliosView from './FoliosView.vue'

let GET = vi.fn()
vi.mock('@/api/client', () => ({ api: { GET: (...a: unknown[]) => GET(...a) } }))

function mountView(permissions = ['folio.read'], page: object = { data: [{ id: 3, folio_number: 'FOL000001', status: 'OPEN', reservation_id: 9, stay_id: null, version: 2, balance: '-500000' }] }) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().me = { user: { id: 5, is_tenant_admin: false }, properties: [{ id: 7, code: 'BALI', name: 'Bali', permissions }] } as never
  usePropertyStore().currentId = 7
  GET = vi.fn().mockResolvedValue({ data: page })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(FoliosView, { global: { plugins: [pinia, router] } })
}

describe('FoliosView', () => {
  beforeEach(() => {
    GET = vi.fn()
  })

  it('lists open folios with their balances and links', async () => {
    const w = mountView()
    await flushPromises()
    expect(GET.mock.calls[0]?.[1]).toMatchObject({ params: { path: { propertyId: 7 }, query: { limit: 50, status: 'OPEN' } } })
    const row = w.get('[data-testid=folio-FOL000001]')
    expect(row.text()).toContain('-500000')
    expect(row.findAll('a').map((a) => a.attributes('href'))).toEqual(['/folios/3', '/reservations/9'])
  })

  it('filters by status and loads more', async () => {
    const w = mountView(['folio.read'], { data: [{ id: 3, folio_number: 'F', status: 'OPEN', reservation_id: 9, version: 1, balance: '0' }], next_cursor: 'c1' })
    await flushPromises()
    await w.get('select[name=status]').setValue('')
    GET.mockResolvedValue({ data: { data: [] } })
    await w.get('form[role=search]').trigger('submit')
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1]).toMatchObject({ params: { query: { status: undefined } } })
    expect(w.get('[data-testid=empty]').text()).toContain('No folios')
  })

  it('needs folio.read', async () => {
    const w = mountView(['guest.read'])
    await flushPromises()
    expect(w.find('[data-testid=no-access]').exists()).toBe(true)
    expect(GET).not.toHaveBeenCalled()
  })
})
