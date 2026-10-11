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

/** Runs `fn` with the window of a phone (390 px), then puts the width back. */
async function onAPhone<T>(fn: () => Promise<T>): Promise<T> {
  const wide = window.innerWidth
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 })
  try {
    return await fn()
  } finally {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: wide })
  }
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
    expect(row.text()).toContain('500,000 credit') // a negative balance is a credit, in words
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

  it('does not sort a page of a longer list, counts what is loaded, and sorts again when the last page is in', async () => {
    const f = (id: number, n: string, balance: string) => ({ id, folio_number: n, status: 'OPEN', reservation_id: id, stay_id: null, version: 1, balance })
    const w = mountView(['folio.read'], { data: [f(1, 'FOL000001', '300'), f(2, 'FOL000002', '100')], next_cursor: 'c2' })
    await flushPromises()
    expect(w.findAll('[data-testid^=sort-]')).toHaveLength(0)
    expect(w.get('[data-testid=table-count]').text()).toBe('2 loaded')
    GET.mockResolvedValue({ data: { data: [f(3, 'FOL000003', '200')] } })
    await w.get('[data-testid=more]').trigger('click')
    await flushPromises()
    expect(GET.mock.calls.at(-1)?.[1]).toMatchObject({ params: { query: { cursor: 'c2', limit: 50 } } })
    expect(w.get('[data-testid=table-count]').text()).toBe('3 loaded')
    await w.get('[data-testid=sort-balance]').trigger('click')
    expect(w.findAll('tbody tr').map((r) => r.attributes('data-testid'))).toEqual(['folio-FOL000002', 'folio-FOL000003', 'folio-FOL000001'])
  })

  it('opens the folio when its row is clicked, and is a card on a phone with the balance at the right', async () => {
    const w = mountView()
    await flushPromises()
    const router = (w.vm as unknown as { $router: { currentRoute: { value: { path: string } } } }).$router
    await w.get('[data-testid=folio-FOL000001] td:nth-child(4)').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/folios/3')
    w.unmount()
    await onAPhone(async () => {
      const card = mountView()
      await flushPromises()
      const c = card.get('[data-testid=folio-FOL000001]')
      expect(c.attributes('data-slot')).toBe('data-card')
      expect(c.get('p.font-semibold').text()).toBe('500,000 credit')
      expect(c.findAll('a').map((a) => a.attributes('href'))).toEqual(['/folios/3', '/reservations/9'])
      card.unmount()
    })
  })

  it('has a short bar on a phone: a "Filter" button (nothing is on while the page shows open folios), and the status in a sheet', async () => {
    await onAPhone(async () => {
      const w = mountView()
      await flushPromises()
      const button = w.get('[data-testid=open-filters]')
      expect(button.text()).toBe('Filter')
      expect(w.find('select[name=status]').exists()).toBe(false)
      await button.trigger('click')
      await flushPromises()
      const sheet = document.body.querySelector('[data-testid=filter-sheet]')!
      const status = sheet.querySelector('select[name=status]') as HTMLSelectElement
      status.value = 'CLOSED'
      status.dispatchEvent(new Event('change'))
      await flushPromises()
      ;(sheet.querySelector('button[type=submit]') as HTMLButtonElement).click() // "Search"
      await flushPromises()
      expect(GET.mock.calls.at(-1)?.[1]).toMatchObject({ params: { query: { status: 'CLOSED' } } })
      expect(w.get('[data-testid=filter-count]').text()).toBe('(1)') // another choice than open
      w.unmount()
      document.body.innerHTML = ''
    })
  })

  it('offers "Clear filters" when another status than open shows nothing, and clearing goes back to the open folios', async () => {
    const w = mountView(['folio.read'], { data: [] })
    await flushPromises()
    expect(w.find('[data-testid=empty] [data-testid=empty-action]').exists()).toBe(false) // open folios, no filter: nothing to clear
    await w.get('select[name=status]').setValue('CLOSED')
    await w.get('form[role=search]').trigger('submit')
    await flushPromises()
    const action = w.get('[data-testid=empty] [data-testid=empty-action]')
    expect(action.text()).toBe('Clear filters')
    await action.trigger('click')
    await flushPromises()
    expect((w.get('select[name=status]').element as HTMLSelectElement).value).toBe('OPEN')
    expect(GET.mock.calls.at(-1)?.[1]).toMatchObject({ params: { query: { status: 'OPEN' } } })
  })

  it('names the reservation of a folio by its confirmation number, with a link to it, and never as "#id"', async () => {
    const w = mountView(['folio.read'], { data: [
      { id: 3, folio_number: 'FOL000003', status: 'OPEN', reservation_id: 12, confirmation_number: 'RES000012', version: 1, balance: '900' },
      { id: 4, folio_number: 'FOL000004', status: 'OPEN', reservation_id: 14, version: 1, balance: '0' }, // an answer without the number
    ] })
    await flushPromises()
    const link = w.get('[data-testid=folio-FOL000003] a[href="/reservations/12"]')
    expect(link.text()).toBe('RES000012')
    expect(w.get('[data-testid=folio-FOL000003]').text()).not.toContain('#12')
    expect(w.get('[data-testid=folio-FOL000004] a[href="/reservations/14"]').text()).toBe('#14')
  })
})
