import { flushPromises, mount } from '@vue/test-utils'
import { BedDouble } from 'lucide-vue-next'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { h } from 'vue'
import { setLocale } from '@/i18n'
import DataTable, { type Column } from './DataTable.vue'
import EmptyState from './EmptyState.vue'
import FormField from './FormField.vue'
import KpiCard from './KpiCard.vue'
import PageHeader from './PageHeader.vue'
import StatusBadge from './StatusBadge.vue'
import StepCard from './StepCard.vue'
import { knownStatus, statusVariant } from './statusMap'

beforeEach(() => setLocale('en'))

describe('PageHeader', () => {
  it('has the title as the page h1, a line under it, marks and actions', () => {
    const w = mount(PageHeader, {
      props: { title: 'Arrivals', description: 'Due today' },
      slots: { marks: '<span data-testid="mark">LIVE</span>', actions: '<button data-testid="act">New</button>' },
    })
    expect(w.get('h1').text()).toBe('Arrivals')
    expect(w.text()).toContain('Due today')
    expect(w.find('[data-testid=mark]').exists()).toBe(true)
    expect(w.find('[data-testid=act]').exists()).toBe(true)
  })

  it('leaves out the actions area when there are none', () => {
    expect(mount(PageHeader, { props: { title: 'x' } }).find('[data-slot=page-actions]').exists()).toBe(false)
  })
})

describe('StatusBadge', () => {
  it('shows a translated label with the colour of its meaning', () => {
    const w = mount(StatusBadge, { props: { domain: 'housekeeping', status: 'DIRTY' } })
    expect(w.text()).toBe('Dirty')
    expect(w.attributes('data-status')).toBe('DIRTY')
    expect(w.classes().join(' ')).toContain('bg-status-dirty-bg')
  })

  it('follows the language', () => {
    setLocale('id')
    expect(mount(StatusBadge, { props: { domain: 'housekeeping', status: 'CLEAN' } }).text()).toBe('Bersih')
  })

  it('colours the same word by its domain', () => {
    expect(statusVariant('work', 'OPEN')).toBe('warning')
    expect(statusVariant('record', 'OPEN')).toBe('success')
    expect(statusVariant('payment', 'VOIDED')).toBe('destructive')
    expect(statusVariant('reservation', 'NO_SHOW')).toBe('noshow')
  })

  it('shows a status it does not know as plain text', () => {
    expect(knownStatus('stay', 'WEIRD')).toBe(false)
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const w = mount(StatusBadge, { props: { domain: 'stay', status: 'WEIRD' } })
    expect(w.text()).toBe('Weird') // readable, never the raw code
    expect(warn).toHaveBeenCalledTimes(1) // and a code with no label is reported, once
    mount(StatusBadge, { props: { domain: 'stay', status: 'WEIRD' } })
    expect(warn).toHaveBeenCalledTimes(1)
    warn.mockRestore()
    expect(w.classes().join(' ')).toContain('text-foreground')
  })

  it('lets a page give its own label', () => {
    expect(mount(StatusBadge, { props: { domain: 'stay', status: 'OPEN', label: 'In house' } }).text()).toBe('In house')
  })
})

interface Row {
  id: number
  name: string
  amount: string
  date: string | null
}

const columns: Column<Row>[] = [
  { key: 'name', label: 'Guest', sortable: true },
  { key: 'amount', label: 'Amount', align: 'right', sortable: true },
  { key: 'date', label: 'Arrival', sortable: true },
  { key: 'actions', label: '' },
]
const rows: Row[] = [
  { id: 1, name: 'Wayan', amount: '900', date: '2026-10-02' },
  { id: 2, name: 'agus', amount: '10000', date: null },
  { id: 3, name: 'Dewi', amount: '250000', date: '2026-10-01' },
]

const names = (w: ReturnType<typeof mount>) => w.findAll('tbody tr').map((r) => r.findAll('td')[0]!.text())

describe('DataTable', () => {
  const make = (props: object = {}, slots: object = {}) =>
    mount(DataTable as never, { props: { columns, rows, rowKey: 'id', ...props }, slots } as never) as unknown as ReturnType<typeof mount>

  it('renders a row per item with a column per heading', () => {
    const w = make()
    expect(w.findAll('thead th').map((t) => t.text())).toEqual(['Guest', 'Amount', 'Arrival', ''])
    expect(names(w)).toEqual(['Wayan', 'agus', 'Dewi'])
    expect(w.findAll('tbody tr')[1]!.findAll('td')[2]!.text()).toBe('—') // a missing value
  })

  it('sorts a column up, down and back to the given order', async () => {
    const w = make()
    await w.get('[data-testid=sort-name]').trigger('click')
    expect(names(w)).toEqual(['agus', 'Dewi', 'Wayan']) // case does not matter
    expect(w.findAll('thead th')[0]!.attributes('aria-sort')).toBe('ascending')
    await w.get('[data-testid=sort-name]').trigger('click')
    expect(names(w)).toEqual(['Wayan', 'Dewi', 'agus'])
    expect(w.findAll('thead th')[0]!.attributes('aria-sort')).toBe('descending')
    await w.get('[data-testid=sort-name]').trigger('click')
    expect(names(w)).toEqual(['Wayan', 'agus', 'Dewi'])
  })

  it('sorts amounts that arrive as text by their value', async () => {
    const w = make()
    await w.get('[data-testid=sort-amount]').trigger('click')
    expect(names(w)).toEqual(['Wayan', 'agus', 'Dewi']) // 900, 10000, 250000, not "10000" < "250000" < "900"
    await w.get('[data-testid=sort-amount]').trigger('click')
    expect(names(w)).toEqual(['Dewi', 'agus', 'Wayan'])
  })

  it('puts an empty value last in both directions', async () => {
    const w = make()
    await w.get('[data-testid=sort-date]').trigger('click')
    expect(names(w)).toEqual(['Dewi', 'Wayan', 'agus'])
    await w.get('[data-testid=sort-date]').trigger('click')
    expect(names(w)).toEqual(['Wayan', 'Dewi', 'agus'])
  })

  it('does not reorder a column that is not sortable', () => {
    expect(make().find('[data-testid=sort-actions]').exists()).toBe(false)
  })

  it('draws a cell with its slot', () => {
    const w = make({}, { 'cell-name': ({ row, value }: { row: Row; value: string }) => h('b', { 'data-testid': `n-${row.id}` }, value.toUpperCase()) })
    expect(w.get('[data-testid=n-1]').text()).toBe('WAYAN')
  })

  it('takes a function as the row key', () => {
    expect(names(make({ rowKey: (r: Row) => `row-${r.id}` }))).toHaveLength(3)
  })

  it('shows skeleton rows while loading, then the rows', async () => {
    const w = make({ rows: [], loading: true })
    expect(w.findAll('[data-testid=table-loading]')).toHaveLength(5)
    expect(w.find('[data-testid=table-empty]').exists()).toBe(false)
    await w.setProps({ rows, loading: false })
    expect(w.find('[data-testid=table-loading]').exists()).toBe(false)
    expect(names(w)).toHaveLength(3)
  })

  it('shows the empty state, with its own words when given', async () => {
    const w = make({ rows: [], emptyTitle: 'No one is due', emptyDescription: 'Try tomorrow' })
    expect(w.get('[data-testid=table-empty]').text()).toContain('No one is due')
    expect(w.text()).toContain('Try tomorrow')
    expect(make({ rows: [] }).get('[data-testid=table-empty]').text()).toContain('Nothing to show.')
  })

  it('reacts to a click or Enter on a row when clickable', async () => {
    const w = make({ clickable: true })
    await w.findAll('tbody tr')[2]!.trigger('click')
    await w.findAll('tbody tr')[0]!.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('rowClick')).toEqual([[rows[2]], [rows[0]]])
    const plain = make()
    await plain.findAll('tbody tr')[0]!.trigger('click')
    expect(plain.emitted('rowClick')).toBeUndefined()
  })

  it('lets a page draw a header cell, for a select-all box', () => {
    const w = make({}, { 'header-actions': () => h('input', { type: 'checkbox', 'data-testid': 'all' }) })
    expect(w.find('thead [data-testid=all]').exists()).toBe(true)
    expect(w.findAll('thead th')[0]!.text()).toBe('Guest') // the other headers keep their label
  })

  it('adds a class to the rows a page picks out', () => {
    const w = make({ rowClass: (r: Row) => (r.id === 2 ? 'struck' : undefined) })
    expect(w.findAll('tbody tr')[1]!.classes()).toContain('struck')
    expect(w.findAll('tbody tr')[0]!.classes()).not.toContain('struck')
  })

  it('gives each row a test id when asked', () => {
    const w = make({ rowTestId: (r: Row) => `guest-${r.id}` })
    expect(w.find('[data-testid=guest-2]').exists()).toBe(true)
  })

  it('passes attributes such as data-testid to its root', () => {
    expect(mount(DataTable as never, { props: { columns, rows, rowKey: 'id' }, attrs: { 'data-testid': 'guests' } } as never).attributes('data-testid')).toBe('guests')
  })
})

describe('FormField', () => {
  it('ties the label, hint and error to the control', async () => {
    const w = mount(FormField, {
      props: { label: 'Code', hint: 'Letters only', error: 'Taken', required: true },
      slots: {
        default: (p: { id: string; describedBy?: string; invalid: boolean }) => h('input', { id: p.id, 'aria-describedby': p.describedBy, 'aria-invalid': p.invalid, 'data-testid': 'ctl' }),
      },
    })
    await flushPromises()
    const input = w.get('[data-testid=ctl]')
    expect(w.get('label').attributes('for')).toBe(input.attributes('id'))
    expect(input.attributes('aria-invalid')).toBe('true')
    const ids = input.attributes('aria-describedby')!.split(' ')
    expect(ids).toHaveLength(2)
    expect(w.text()).toContain('Letters only')
    expect(w.get('[role=alert]').text()).toBe('Taken')
    expect(w.get('label').text()).toContain('*')
  })

  it('is quiet when all is well', () => {
    const w = mount(FormField, { props: { label: 'Name' }, slots: { default: '<input />' } })
    expect(w.find('[role=alert]').exists()).toBe(false)
    expect(w.text()).toBe('Name')
  })
})

describe('EmptyState and KpiCard', () => {
  it('says what is missing and offers the next step', () => {
    const w = mount(EmptyState, { props: { title: 'No rooms yet', description: 'Add one', icon: BedDouble }, slots: { action: '<button data-testid="add">Add</button>' } })
    expect(w.text()).toContain('No rooms yet')
    expect(w.find('svg').exists()).toBe(true)
    expect(w.find('[data-testid=add]').exists()).toBe(true)
  })

  it('shows a figure with its label, hint and tone', () => {
    const w = mount(KpiCard, { props: { label: 'Occupancy', value: '66.67%', hint: '6 of 9 rooms', tone: 'warning' } })
    expect(w.text()).toContain('Occupancy')
    expect(w.get('[data-slot=kpi-value]').text()).toBe('66.67%')
    expect(w.get('[data-slot=kpi-value]').classes()).toContain('text-warning-text')
    expect(w.text()).toContain('6 of 9 rooms')
  })
})

describe('StepCard', () => {
  it('shows the number, the title and the state of a step', () => {
    const w = mount(StepCard, { props: { step: 2, title: 'Unresolved arrivals', state: 'blocked', summary: '(3)' }, slots: { default: '<p data-testid="body">Check them in</p>' } })
    expect(w.text()).toContain('2. Unresolved arrivals')
    expect(w.text()).toContain('(3)')
    expect(w.attributes('data-state')).toBe('blocked')
    expect(w.find('[data-testid=body]').exists()).toBe(true)
  })

  it('has no body when there is nothing in the slot', () => {
    const w = mount(StepCard, { props: { step: 1, title: 'Time', state: 'ok' } })
    expect(w.attributes('data-state')).toBe('ok')
    expect(w.find('[data-slot=card-content]').exists()).toBe(false)
  })
})

describe('DataTable formats amounts and dates in the language of the page', () => {
  it('groups digits and names the month by column format', async () => {
    const { setLocale } = await import('@/i18n')
    const { mount } = await import('@vue/test-utils')
    const { default: DataTable } = await import('./DataTable.vue')
    const columns = [
      { key: 'amount', label: 'Amount', format: 'money' as const },
      { key: 'day', label: 'Day', format: 'date' as const },
      { key: 'raw', label: 'Raw' },
    ]
    const rows = [{ id: 1, amount: '2442000', day: '2026-08-17', raw: '2442000' }]
    try {
      const en = mount(DataTable, { props: { columns, rows, rowKey: 'id' } })
      expect(en.text()).toContain('2,442,000')
      expect(en.text()).toContain('17 Aug 2026')
      setLocale('id')
      const id = mount(DataTable, { props: { columns, rows, rowKey: 'id' } })
      expect(id.text()).toContain('2.442.000')
      expect(id.text()).toContain('17 Agu 2026')
      expect(id.text()).toContain('2442000') // an unformatted column stays as it came
    } finally {
      setLocale('en')
    }
  })
})

describe('DataTable filters and sorting (TanStack Table)', () => {
  const rows = [
    { id: 1, code: '201', type: 'DLX', amount: '1500000', note: 'a' },
    { id: 2, code: '1001', type: 'STD', amount: '250000', note: '' },
    { id: 3, code: '202', type: 'DLX', amount: '900000', note: 'c' },
  ]
  const columns: Column<(typeof rows)[number]>[] = [
    { key: 'code', label: 'Room', sortable: true, filter: 'text' },
    { key: 'type', label: 'Type', filter: 'select' },
    { key: 'amount', label: 'Amount', sortable: true, format: 'money' },
    { key: 'note', label: 'Note', sortable: true },
  ]
  const order = (w: ReturnType<typeof mount>) => w.findAll('tbody tr').map((r) => r.findAll('td')[0]?.text())

  it('filters a column by text and by choice, and clears the filters', async () => {
    const w = mount(DataTable, { props: { columns: columns as never, rows, rowKey: 'id' } })
    expect(w.find('[data-testid=table-filters]').exists()).toBe(true)
    await w.get('input[name=filter_code]').setValue('20')
    expect(order(w)).toEqual(['201', '202'])
    await w.get('select[name=filter_type]').setValue('STD')
    expect(w.find('[data-testid=table-no-match]').exists()).toBe(true) // 20 and STD match nothing together
    await w.get('[data-testid=table-clear-filters]').trigger('click')
    expect(order(w)).toEqual(['201', '1001', '202'])
    expect((w.get('select[name=filter_type]').element as HTMLSelectElement).value).toBe('')
  })

  it('offers the distinct values as choices and filters on what the cell shows', async () => {
    const w = mount(DataTable, { props: { columns: [{ key: 'amount', label: 'Amount', format: 'money', filter: 'text' }] as never, rows, rowKey: 'id' } })
    await w.get('input[name=filter_amount]').setValue('1,500')
    expect(w.findAll('tbody tr')).toHaveLength(1)
    const w2 = mount(DataTable, { props: { columns: columns as never, rows, rowKey: 'id' } })
    expect(w2.findAll('select[name=filter_type] option').map((o) => o.text())).toEqual(['All', 'DLX', 'STD'])
  })

  it('sorts numbers inside text as numbers, empty values last in both directions', async () => {
    const w = mount(DataTable, { props: { columns: columns as never, rows, rowKey: 'id' } })
    await w.get('[data-testid=sort-amount]').trigger('click')
    expect(order(w)).toEqual(['1001', '202', '201'])
    await w.get('[data-testid=sort-amount]').trigger('click')
    expect(order(w)).toEqual(['201', '202', '1001'])
    await w.get('[data-testid=sort-note]').trigger('click')
    expect(order(w)).toEqual(['201', '202', '1001']) // the empty note is last
    await w.get('[data-testid=sort-note]').trigger('click')
    expect(order(w)).toEqual(['202', '201', '1001']) // and still last when descending
  })

  it('in manual mode leaves the rows alone and tells what was asked', async () => {
    const w = mount(DataTable, { props: { columns: columns as never, rows, rowKey: 'id', manual: true } })
    await w.get('input[name=filter_code]').setValue('zzz')
    expect(order(w)).toEqual(['201', '1001', '202'])
    expect(w.emitted('filterChange')?.at(-1)).toEqual([{ code: 'zzz' }])
    await w.get('[data-testid=sort-code]').trigger('click')
    expect(w.emitted('sortChange')?.at(-1)).toEqual([{ key: 'code', dir: 'asc' }])
    expect(order(w)).toEqual(['201', '1001', '202'])
  })
})
