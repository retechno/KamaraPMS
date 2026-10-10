import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import type { Component } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setLocale } from '@/i18n'
import DataTable, { type Column } from './DataTable.vue'
import type { RowAction } from './rowActions'

interface Row {
  id: number
  number: string
  guest: string
  rate: string
  status: string
}

const rows: Row[] = [
  { id: 1, number: 'R1', guest: 'Citra', rate: '300000', status: 'OPEN' },
  { id: 2, number: 'R2', guest: 'Andi', rate: '100000', status: 'DONE' },
  { id: 3, number: 'R3', guest: 'Budi', rate: '200000', status: 'OPEN' },
]

const columns: Column<Row>[] = [
  { key: 'number', label: 'Number', sortable: true, filter: 'text', card: 'primary' },
  { key: 'guest', label: 'Guest', sortable: true, card: 'secondary' },
  { key: 'rate', label: 'Rate', align: 'right', sortable: true, format: 'money', card: 'money' },
  { key: 'status', label: 'Status', sortable: true, filter: 'select', card: 'badge' },
  { key: 'extra', label: 'Extra' },
  { key: 'actions', label: '' },
]

let wrapper: VueWrapper | null = null
const wide = window.innerWidth
const setWidth = (w: number) => Object.defineProperty(window, 'innerWidth', { configurable: true, value: w })

async function mountTable(props: Record<string, unknown> = {}, slots: Record<string, string> = {}, path = '/') {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  await router.push(path)
  wrapper = mount(DataTable as unknown as Component, {
    props: { columns, rows, rowKey: 'id', rowTestId: (r: Row) => `row-${r.number}`, ...props },
    slots,
    attachTo: document.body,
    global: { plugins: [router] },
  })
  await flushPromises()
  return { w: wrapper, router }
}

const sortButtons = (w: VueWrapper) => w.findAll('[data-testid^=sort-]')

describe('DataTable: a table read a page at a time', () => {
  beforeEach(() => setLocale('en'))
  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
    document.body.innerHTML = ''
    setWidth(wide)
  })

  describe('sorting and the filters of the columns', () => {
    it('are not offered while there are more rows on the server: they would sort and filter part of the list', async () => {
      const { w } = await mountTable({ hasMore: true })
      expect(sortButtons(w)).toHaveLength(0)
      expect(w.find('[data-testid=table-filters]').exists()).toBe(false)
      expect(w.findAll('th').every((th) => th.attributes('aria-sort') === undefined)).toBe(true)
      expect(w.find('svg.lucide-chevrons-up-down').exists()).toBe(false) // no sort icon either
      expect(w.get('th').text()).toBe('Number') // the label is plain text
    })

    it('are there when every row has been loaded (no more pages), and come after the last page is loaded', async () => {
      const { w } = await mountTable({ hasMore: true })
      expect(sortButtons(w)).toHaveLength(0)
      await w.setProps({ hasMore: false }) // the last page arrived
      expect(sortButtons(w).map((b) => b.attributes('data-testid'))).toEqual(['sort-number', 'sort-guest', 'sort-rate', 'sort-status'])
      expect(w.find('[data-testid=table-filters]').exists()).toBe(true)
      await w.get('[data-testid=sort-rate]').trigger('click')
      expect(w.findAll('tbody tr').map((r) => r.attributes('data-testid'))).toEqual(['row-R2', 'row-R3', 'row-R1'])
    })

    it('are there for a table that has all its rows from the start (a table that is not paged)', async () => {
      const { w } = await mountTable()
      expect(sortButtons(w)).toHaveLength(4)
      expect(w.find('[data-testid=table-filters]').exists()).toBe(true)
      expect(w.find('[data-testid=table-paging]').exists()).toBe(false)
    })

    it('are dropped when a new search brings more pages again: a sort of the old rows must not look like one of the new', async () => {
      const { w } = await mountTable({ hasMore: false })
      await w.get('[data-testid=sort-rate]').trigger('click')
      await w.get('input[name=filter_number]').setValue('R1')
      expect(w.findAll('tbody tr')).toHaveLength(1)
      await w.setProps({ hasMore: true })
      expect(sortButtons(w)).toHaveLength(0)
      expect(w.findAll('tbody tr').map((r) => r.attributes('data-testid'))).toEqual(['row-R1', 'row-R2', 'row-R3']) // as the server sent them
      await w.setProps({ hasMore: false })
      expect(w.findAll('tbody tr')).toHaveLength(3)
      expect(w.get('th[scope=col]').attributes('aria-sort')).toBe('none') // nothing is sorted any more
    })

    it('stay for a table that sorts and filters on the server (manual), more pages or not', async () => {
      const { w } = await mountTable({ hasMore: true, manual: true })
      expect(sortButtons(w)).toHaveLength(4)
      expect(w.find('[data-testid=table-filters]').exists()).toBe(true)
      await w.get('[data-testid=sort-rate]').trigger('click')
      expect(w.emitted('sortChange')?.at(-1)).toEqual([{ key: 'rate', dir: 'asc' }])
    })
  })

  describe('the count and the button', () => {
    it('says how many rows are loaded and offers "Load more" while there are more', async () => {
      const { w } = await mountTable({ hasMore: true })
      expect(w.get('[data-testid=table-count]').text()).toBe('3 loaded')
      const more = w.get('[data-testid=more]')
      expect(more.text()).toBe('Load more')
      await more.trigger('click')
      expect(w.emitted('loadMore')).toHaveLength(1)
    })

    it('says "of about N" when the total is known, and keeps the button off while a page is coming', async () => {
      const { w } = await mountTable({ hasMore: true, total: 120, loadingMore: true })
      expect(w.get('[data-testid=table-count]').text()).toBe('3 of about 120')
      expect(w.get('[data-testid=more]').attributes('disabled')).toBeDefined()
    })

    it('shows the count without the button when the last page is in, and nothing for an empty or an unpaged table', async () => {
      const { w } = await mountTable({ hasMore: false })
      expect(w.get('[data-testid=table-count]').text()).toBe('3 loaded')
      expect(w.find('[data-testid=more]').exists()).toBe(false)
      await w.setProps({ rows: [] })
      expect(w.find('[data-testid=table-paging]').exists()).toBe(false)
    })

    it('takes another label for the button, and speaks Indonesian', async () => {
      const { w } = await mountTable({ hasMore: true, loadMoreLabel: 'More guests' })
      expect(w.get('[data-testid=more]').text()).toBe('More guests')
      setLocale('id')
      const id = await mountTable({ hasMore: true, total: 200 })
      expect(id.w.get('[data-testid=more]').text()).toBe('Muat lebih banyak')
      expect(id.w.get('[data-testid=table-count]').text()).toBe('3 dari ±200')
    })
  })

  describe('the cards of a phone', () => {
    const cardActions = (r: Row): RowAction[] => [
      { key: 'open', label: 'Open', primary: true, testId: `open-${r.number}`, onSelect: () => (opened = r.number) },
      { key: 'edit', label: 'Edit', to: `/rows/${r.id}/edit`, testId: `edit-${r.number}` },
      { key: 'drop', label: 'Drop', destructive: true, testId: `drop-${r.number}`, onSelect: () => (dropped = r.number) },
    ]
    let opened = ''
    let dropped = ''

    it('are a card for each row on a narrow screen when the table asks for them, and a table otherwise', async () => {
      setWidth(400)
      const cards = await mountTable({ cards: true })
      expect(cards.w.find('table').exists()).toBe(false)
      expect(cards.w.findAll('[data-slot=data-card]')).toHaveLength(3)
      cards.w.unmount()
      const plain = await mountTable({ cards: false }) // a table that did not ask keeps being a table
      expect(plain.w.find('table').exists()).toBe(true)
      plain.w.unmount()
      setWidth(1280)
      const desk = await mountTable({ cards: true })
      expect(desk.w.find('table').exists()).toBe(true)
      expect(desk.w.find('[data-slot=data-card]').exists()).toBe(false)
    })

    it('puts the title, the line under it, the mark in the corner and the amount where their columns say, and the rest as label and value', async () => {
      setWidth(400)
      const { w } = await mountTable({ cards: true }, { 'cell-extra': '<span data-testid="extra">more</span>' })
      const card = w.get('[data-testid=row-R1]')
      expect(card.text()).toContain('R1')
      expect(card.text()).toContain('Citra')
      expect(card.text()).toContain('OPEN')
      expect(card.text()).toContain('300,000') // the amount, formatted
      expect(card.find('p.font-semibold').text()).toBe('300,000') // at the right, bold
      expect(card.find('dl').text()).toContain('Extra') // a column with no part: a line with its label
      expect(card.find('dl').text()).not.toContain('Number') // the title is not repeated
      expect(card.find('[data-testid=extra]').exists()).toBe(true) // from the same slot as the table
    })

    it('leaves out the columns that are hidden on a phone, and the column of actions', async () => {
      setWidth(400)
      const { w } = await mountTable({ cards: true, columns: columns.map((c) => (c.key === 'extra' ? { ...c, hideOnMobile: true } : c)) })
      expect(w.get('[data-testid=row-R1]').text()).not.toContain('Extra')
    })

    it('has one main action as a button and the others in the "..." menu', async () => {
      setWidth(400)
      opened = ''
      dropped = ''
      const { w } = await mountTable({ cards: true, rowActions: cardActions })
      const card = w.get('[data-testid=row-R1]')
      expect(card.findAll('button').map((b) => b.text())).toEqual(['Open', '']) // the main one, and the menu's button
      expect(card.get('[data-testid=open-R1]').text()).toBe('Open')
      await card.get('[data-testid=open-R1]').trigger('click')
      expect(opened).toBe('R1')
      await card.get('[data-slot=row-menu-trigger]').trigger('click')
      await flushPromises()
      const menu = document.body.querySelector('[data-slot=row-menu]')!
      expect(Array.from(menu.querySelectorAll('[data-testid]')).map((e) => e.getAttribute('data-testid'))).toEqual(['edit-R1', 'drop-R1'])
      expect(menu.querySelector('[data-testid=edit-R1]')?.getAttribute('href')).toBe('/rows/1/edit') // a link
      ;(menu.querySelector('[data-testid=drop-R1]') as HTMLElement).click()
      await flushPromises()
      expect(dropped).toBe('R1')
    })

    it('shows the detail of an expanded row under its card, and a skeleton while loading', async () => {
      setWidth(400)
      const { w } = await mountTable({ cards: true, isExpanded: (r: Row) => r.id === 2, detailTestId: (r: Row) => `detail-${r.id}` }, { detail: '<p>The detail</p>' })
      expect(w.get('[data-testid=detail-2]').text()).toBe('The detail')
      await w.setProps({ rows: [], loading: true })
      expect(w.findAll('[data-testid=table-loading]').length).toBeGreaterThan(0)
    })

    it('are told the page the same way: the card carries the row class and the empty state is the same', async () => {
      setWidth(400)
      const { w } = await mountTable({ cards: true, rowClass: (r: Row) => (r.status === 'DONE' ? 'struck' : undefined) })
      expect(w.get('[data-testid=row-R2]').classes()).toContain('struck')
      await w.setProps({ rows: [] })
      expect(w.find('[data-testid=table-empty]').exists()).toBe(true)
    })
  })

  describe('a row that opens a page', () => {
    it('opens it when the row is clicked, and not when a link, a button or a field in it is clicked', async () => {
      const { w, router } = await mountTable({ rowTo: (r: Row) => `/rows/${r.id}` }, { 'cell-guest': '<a href="/elsewhere" data-testid="link">link</a><button type="button" data-testid="btn">b</button><input data-testid="field" />' })
      await w.get('[data-testid=link]').trigger('click')
      await w.get('[data-testid=btn]').trigger('click')
      await w.get('[data-testid=field]').trigger('click')
      await flushPromises()
      expect(router.currentRoute.value.path).toBe('/')
      await w.get('[data-testid=row-R1] td:nth-child(3)').trigger('click') // plain text
      await flushPromises()
      expect(router.currentRoute.value.path).toBe('/rows/1')
    })

    it('makes the row look clickable, but gives it no tab stop: the link in it is what the keyboard reaches', async () => {
      const { w } = await mountTable({ rowTo: (r: Row) => `/rows/${r.id}` }, { 'cell-number': '<a href="/rows/1" data-testid="number-link">R1</a>' })
      const tr = w.get('[data-testid=row-R1]')
      expect(tr.classes()).toContain('cursor-pointer')
      expect(tr.attributes('tabindex')).toBeUndefined()
      expect(w.get('[data-testid=number-link]').attributes('href')).toBe('/rows/1') // a real link, focusable
    })

    it('opens the page from a card too', async () => {
      setWidth(400)
      const { w, router } = await mountTable({ cards: true, rowTo: (r: Row) => `/rows/${r.id}` })
      await w.get('[data-testid=row-R3] dl').trigger('click')
      await flushPromises()
      expect(router.currentRoute.value.path).toBe('/rows/3')
    })

    it('does nothing for a row that has no page, and leaves "clickable" tables to their own event', async () => {
      const { w, router } = await mountTable({ rowTo: () => undefined })
      await w.get('[data-testid=row-R1] td:nth-child(3)').trigger('click')
      expect(router.currentRoute.value.path).toBe('/')
      w.unmount()
      const c = await mountTable({ clickable: true })
      await c.w.get('[data-testid=row-R2] td').trigger('click')
      expect(c.w.emitted('rowClick')?.[0]).toEqual([rows[1]])
    })
  })

  describe('the actions of a row in the table', () => {
    const actions = (r: Row): RowAction[] => [{ key: 'view', label: 'View', primary: true, testId: `view-${r.number}`, onSelect: () => {} }, { key: 'more', label: 'More', to: '/more' }]

    it('are in the column "actions": the main ones as buttons and the rest in the "..." menu', async () => {
      const { w } = await mountTable({ rowActions: actions })
      const cell = w.get('[data-testid=row-R1] [data-slot=row-actions]')
      expect(cell.find('[data-testid=view-R1]').exists()).toBe(true)
      expect(cell.find('[data-slot=row-menu-trigger]').exists()).toBe(true)
    })

    it('are the page\'s own when it fills the cell of that column itself', async () => {
      const { w } = await mountTable({ rowActions: actions }, { 'cell-actions': '<span data-testid="mine">mine</span>' })
      expect(w.find('[data-slot=row-actions]').exists()).toBe(false)
      expect(w.findAll('[data-testid=mine]')).toHaveLength(3)
    })
  })
})
