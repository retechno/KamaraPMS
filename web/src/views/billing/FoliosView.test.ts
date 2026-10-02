import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
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
    expect(row.text()).toContain('-500,000')
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

  it('shows the status as a badge and sorts balances by value', async () => {
    const w = mountView(['folio.read'], { data: [
      { id: 3, folio_number: 'FOL000003', status: 'OPEN', reservation_id: 9, version: 1, balance: '900' },
      { id: 4, folio_number: 'FOL000004', status: 'CLOSED', reservation_id: 10, version: 1, balance: '250000' },
      { id: 5, folio_number: 'FOL000005', status: 'OPEN', reservation_id: 11, version: 1, balance: '-10000' },
    ] })
    await flushPromises()
    expect(w.get('[data-testid=folio-FOL000004]').text()).toContain('Closed')
    const numbers = () => w.findAll('tbody tr').map((r) => r.findAll('td')[0]!.text())
    await w.get('[data-testid=sort-balance]').trigger('click')
    expect(numbers()).toEqual(['FOL000005', 'FOL000003', 'FOL000004'])
    await w.get('[data-testid=sort-balance]').trigger('click')
    expect(numbers()).toEqual(['FOL000004', 'FOL000003', 'FOL000005'])
  })

  it('links to the cashier and speaks Indonesian', async () => {
    setLocale('id')
    const w = mountView()
    await flushPromises()
    expect(w.get('h1').text()).toBe('Folio')
    expect(w.get('a[href="/cashier"]').text()).toBe('Kasir')
    expect(w.get('select[name=status]').findAll('option').map((o) => o.text())).toEqual(['Semua', 'Terbuka', 'Tertutup'])
    setLocale('en')
  })

  it('narrows the loaded folios by column, in the words of the language', async () => {
    const w = mountView(['folio.read'], { data: [
      { id: 3, folio_number: 'FOL000003', status: 'OPEN', reservation_id: 9, version: 1, balance: '900' },
      { id: 4, folio_number: 'FOL000004', status: 'CLOSED', reservation_id: 10, version: 1, balance: '0' },
    ] })
    await flushPromises()
    expect(w.findAll('select[name=filter_status] option').map((o) => o.text())).toEqual(['All', 'Closed', 'Open'])
    await w.get('select[name=filter_status]').setValue('Closed')
    expect(w.findAll('tbody tr').map((r) => r.attributes('data-testid'))).toEqual(['folio-FOL000004'])
    await w.get('select[name=filter_status]').setValue('')
    await w.get('input[name=filter_folio_number]').setValue('3')
    expect(w.findAll('tbody tr').map((r) => r.attributes('data-testid'))).toEqual(['folio-FOL000003'])
  })
})
