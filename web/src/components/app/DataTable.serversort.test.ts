import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import type { Component } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import DataTable, { type Column } from './DataTable.vue'

interface Row {
  id: number
  number: string
  name: string
}
const rows: Row[] = [
  { id: 1, number: 'R1', name: 'Citra' },
  { id: 2, number: 'R2', name: 'Andi' },
]
// "name" is "guest_name" for the server; "plain" is not sortable at all
const columns: Column<Row>[] = [
  { key: 'number', label: 'Number', sortable: true, card: 'primary' },
  { key: 'name', label: 'Guest', sortable: true, sortKey: 'guest_name', card: 'secondary' },
  { key: 'plain', label: 'Plain' },
]

let wrapper: VueWrapper | null = null
const wide = window.innerWidth
const setWidth = (w: number) => Object.defineProperty(window, 'innerWidth', { configurable: true, value: w })

async function mountTable(props: Record<string, unknown> = {}) {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  await router.push('/')
  wrapper = mount(DataTable as unknown as Component, { props: { columns, rows, rowKey: 'id', manual: true, hasMore: true, ...props }, attachTo: document.body, global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}
const emitted = (w: VueWrapper) => w.emitted('sortChange') ?? []
const header = (w: VueWrapper, key: string) => w.get(`th:has([data-testid=sort-${key}])`)

describe('DataTable: a list that the server sorts', () => {
  beforeEach(() => setLocale('en'))
  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
    document.body.innerHTML = ''
    setWidth(wide)
  })

  it('sorts its columns although there are more rows on the server (the server does the sorting), and only the columns that can', async () => {
    const w = await mountTable()
    expect(w.find('[data-testid=sort-number]').exists()).toBe(true)
    expect(w.find('[data-testid=sort-name]').exists()).toBe(true)
    expect(w.find('[data-testid=sort-plain]').exists()).toBe(false)
    // the rows stay as the server sent them
    expect(w.findAll('tbody tr').map((r) => r.text())).toEqual(['R1Citra—', 'R2Andi—'])
  })

  it('says what was asked, by the name the server knows: ascending, then descending, then back to the list\'s own order', async () => {
    const w = await mountTable()
    await w.get('[data-testid=sort-name]').trigger('click')
    await w.get('[data-testid=sort-name]').trigger('click')
    await w.get('[data-testid=sort-name]').trigger('click')
    expect(emitted(w).map((e) => e[0])).toEqual([{ key: 'guest_name', dir: 'asc' }, { key: 'guest_name', dir: 'desc' }, null])
    await w.get('[data-testid=sort-number]').trigger('click')
    expect(emitted(w).at(-1)?.[0]).toEqual({ key: 'number', dir: 'asc' })
    // the rows were not sorted here
    expect(w.findAll('tbody tr').map((r) => r.text())).toEqual(['R1Citra—', 'R2Andi—'])
  })

  it('shows the order that the page keeps (`sort`) in its header, and changes it only when the page does', async () => {
    const w = await mountTable({ sort: { key: 'guest_name', dir: 'desc' } })
    expect(header(w, 'name').attributes('aria-sort')).toBe('descending')
    expect(header(w, 'number').attributes('aria-sort')).toBe('none')
    await w.get('[data-testid=sort-number]').trigger('click') // asks; the page has not answered yet
    expect(header(w, 'name').attributes('aria-sort')).toBe('descending')
    await w.setProps({ sort: { key: 'number', dir: 'asc' } })
    expect(header(w, 'number').attributes('aria-sort')).toBe('ascending')
    expect(header(w, 'name').attributes('aria-sort')).toBe('none')
    await w.setProps({ sort: null }) // the list's own order: no column shows an arrow
    expect(header(w, 'number').attributes('aria-sort')).toBe('none')
  })

  it('goes from descending to the list\'s own order on the page that keeps its order', async () => {
    const w = await mountTable({ sort: { key: 'number', dir: 'desc' } })
    await w.get('[data-testid=sort-number]').trigger('click')
    expect(emitted(w).at(-1)?.[0]).toBeNull()
  })

  it('on a phone, a choice above the cards does what the header does', async () => {
    setWidth(400)
    const w = await mountTable({ cards: true, sort: { key: 'guest_name', dir: 'asc' } })
    expect(w.find('table').exists()).toBe(false)
    const select = w.get('[data-testid=card-sort] select')
    expect(select.findAll('option').map((o) => o.text())).toEqual(['Default order', 'Number ↑', 'Number ↓', 'Guest ↑', 'Guest ↓'])
    expect((select.element as HTMLSelectElement).value).toBe('name:asc')
    await select.setValue('number:desc')
    expect(emitted(w).at(-1)?.[0]).toEqual({ key: 'number', dir: 'desc' })
    await select.setValue('')
    expect(emitted(w).at(-1)?.[0]).toBeNull()
  })

  it('a table that sorts its own rows has no such choice on a phone, and a wide screen has the header', async () => {
    setWidth(400)
    const client = await mountTable({ cards: true, manual: false, hasMore: undefined })
    expect(client.find('[data-testid=card-sort]').exists()).toBe(false)
    client.unmount()
    setWidth(wide)
    const w = await mountTable({ cards: true })
    expect(w.find('[data-testid=card-sort]').exists()).toBe(false)
  })

  it('says it in Indonesian', async () => {
    setLocale('id')
    setWidth(400)
    const w = await mountTable({ cards: true })
    expect(w.get('[data-testid=card-sort]').text()).toContain('Urutkan')
    expect(w.get('[data-testid=card-sort] option').text()).toBe('Urutan bawaan')
  })
})
