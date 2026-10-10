<script setup lang="ts" generic="T extends object">
import {
  type ColumnDef, type ColumnFiltersState, type SortingState, getCoreRowModel, getFilteredRowModel, getSortedRowModel, useVueTable,
} from '@tanstack/vue-table'
import { ArrowDown, ArrowUp, ChevronsUpDown } from 'lucide-vue-next'
import { Comment, Fragment, Text, type VNode, computed, inject, ref, useAttrs, useSlots, watch } from 'vue'
import { routerKey } from 'vue-router'
import EmptyState from '@/components/app/EmptyState.vue'
import type { RowAction } from '@/components/app/rowActions'
import TableRowActions from '@/components/app/TableRowActions.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { useLayoutMode } from '@/composables/useLayoutMode'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { formatBalance, formatDate, formatDateTime, formatMoney } from '@/utils/format'

/**
 * The one table of the application: a header that can sort and filter, rows with a cell slot per column (`#cell-<key>="{ row,
 * value }"`), a header slot per column (`#header-<key>`, for a select-all box), a loading state with skeleton rows, and an empty state.
 *
 * The rows are laid out by TanStack Table (headless: sorting, filtering and the row model; the markup is ours). A column
 * opts in with `sortable` and `filter`. Sorting and filtering are done here on the rows given; a page that pages
 * through the server sets `manual`, keeps the rows as the server sent them and listens to `sortChange` and `filterChange`.
 *
 * A table that is read a page at a time says so with `hasMore` (and `loadingMore`, `total`, `loadMore`): it shows how many rows are loaded and a "Load more" button. Sorting
 * and the filters of the columns work on the rows that are here, and while there are more rows on the server those are only part of the list: a sort or a filter of them would
 * look like one of everything. So while `hasMore` the columns do not sort and the column filters are not shown (the page's own filter bar is what the server is asked);
 * when the last page has been loaded they come back.
 *
 * With `cards`, a narrow screen (a phone) gets a card for each row instead of the table: the columns tell their part in the card with `card` (`primary` is the title,
 * `secondary` the line under it, `badge` the mark in the corner, `money` the amount at the right) and the actions of the row (`rowActions`) are one main button and a "..." menu.
 * `rowTo` makes a row, and its card, open a page when it is clicked; the link in the main cell is what the keyboard uses. A cell slot gets `card` (true in a card, false in the table), for a cell that
 * says less in a card.
 */
export interface Column<R> {
  key: string
  label: string
  align?: 'left' | 'right' | 'center'
  sortable?: boolean
  /** What to sort by, when it is not the value of `key` (a number inside text, a date). */
  sortValue?: (row: R) => string | number | null | undefined
  class?: string
  /** How the default cell shows the value: an amount, a balance (a negative one is a credit), a business date or an instant, in the language of the page. */
  format?: 'money' | 'balance' | 'date' | 'datetime'
  /** A filter box under the header: a text that the shown value must contain, or a choice among the values. */
  filter?: 'text' | 'select'
  /** What the filter reads, when it is not the text the cell shows (a column with a custom cell). */
  filterValue?: (row: R) => string | null | undefined
  /** The choices of a `select` filter; by default the distinct values of the rows. */
  filterOptions?: { value: string; label: string }[]
  /** In the card of a phone: the title (`primary`), the line under it (`secondary`), a mark in the corner (`badge`), an amount at the right in bold (`money`). A column with none is a "label: value" line. */
  card?: 'primary' | 'secondary' | 'badge' | 'money'
  /** Left out of the card of a phone. */
  hideOnMobile?: boolean
}

const props = withDefaults(
  defineProps<{
    columns: Column<T>[]
    rows: T[]
    /** A property name of the row, or a function: what identifies a row. */
    rowKey: string | ((row: T) => string | number)
    loading?: boolean
    emptyTitle?: string
    emptyDescription?: string
    /** Rows react to a click (and the Enter key) with `rowClick`. */
    clickable?: boolean
    /** A caption for screen readers. */
    caption?: string
    /** A `data-testid` for each row. */
    rowTestId?: (row: T) => string
    /** Extra classes for a row (a voided payment struck through). */
    rowClass?: (row: T) => string | undefined
    /** Rows for which the `detail` slot is shown in a row of its own under them. */
    isExpanded?: (row: T) => boolean
    detailTestId?: (row: T) => string
    /** Sorting and filtering belong to the server: the table shows the rows as they are and only tells what was asked. */
    manual?: boolean
    /** The table is read a page at a time: there are more rows on the server. Undefined for a table that has all its rows. */
    hasMore?: boolean
    loadingMore?: boolean
    /** How many rows there are in all, when the API says (shown as "50 of about N"); otherwise "50 loaded". */
    total?: number | null
    loadMoreLabel?: string
    /** A card for each row on a phone. */
    cards?: boolean
    /** The page a row opens when it is clicked. */
    rowTo?: (row: T) => string | undefined
    /** The actions of a row: the main ones as buttons, the rest in the "..." menu (in the column `actions`, and in the card). */
    rowActions?: (row: T) => RowAction[]
  }>(),
  {
    loading: false, emptyTitle: '', emptyDescription: '', clickable: false, caption: '', rowTestId: undefined, rowClass: undefined, isExpanded: undefined, detailTestId: undefined, manual: false,
    hasMore: undefined, loadingMore: false, total: null, loadMoreLabel: '', cards: false, rowTo: undefined, rowActions: undefined,
  },
)
const emit = defineEmits<{
  loadMore: []
  rowClick: [row: T]
  sortChange: [sort: { key: string; dir: 'asc' | 'desc' } | null]
  filterChange: [filters: Record<string, string>]
}>()
defineOptions({ inheritAttrs: false })
const attrs = useAttrs()

const sorting = ref<SortingState>([])
const filters = ref<ColumnFiltersState>([])

const router = inject(routerKey, null)
const { mode } = useLayoutMode()
const paged = computed(() => props.hasMore !== undefined)
/** The rows here are part of a longer list: no sorting and no column filters until the last page is here (a server-side table sorts and filters on the server). */
const partial = computed(() => props.hasMore === true && !props.manual)
const sortableNow = (col: Column<T>): boolean => !!col.sortable && !partial.value
const cardsOn = computed(() => props.cards && mode.value === 'drawer')

const rowOf = (row: T, col: Column<T>): unknown => (row as Record<string, unknown>)[col.key]

/** The text a cell shows for a value, in the language of the page. */
function shownText(row: T, col: Column<T>): string {
  const v = rowOf(row, col)
  if (v === null || v === undefined) return ''
  const text = String(v)
  return col.format === 'money' ? formatMoney(text) : col.format === 'balance' ? formatBalance(text) : col.format === 'date' ? formatDate(text) : col.format === 'datetime' ? formatDateTime(text) : text
}
const shown = (row: T, col: Column<T>): string => {
  const v = rowOf(row, col)
  return v === null || v === undefined ? '—' : shownText(row, col)
}

const empty = (v: unknown) => v === null || v === undefined || v === ''

function compare(a: unknown, b: unknown): number {
  if (typeof a === 'number' && typeof b === 'number') return a - b
  const na = Number(a)
  const nb = Number(b)
  if (a !== '' && b !== '' && Number.isFinite(na) && Number.isFinite(nb)) return na - nb // amounts arrive as strings
  return String(a).localeCompare(String(b), undefined, { numeric: true, sensitivity: 'base' })
}

const filterText = (row: T, col: Column<T>): string => (col.filterValue ? (col.filterValue(row) ?? '') : shownText(row, col))

const defs = computed<ColumnDef<T>[]>(() =>
  props.columns.map((col) => ({
    id: col.key,
    // An empty value is undefined: with sortUndefined it goes last in both directions.
    accessorFn: (row: T) => {
      const v = col.sortValue ? col.sortValue(row) : rowOf(row, col)
      return empty(v) ? undefined : v
    },
    enableSorting: sortableNow(col),
    sortUndefined: 'last' as const,
    sortingFn: (a, b, id) => compare(a.getValue(id), b.getValue(id)),
    enableColumnFilter: !!col.filter && !partial.value,
    filterFn: (row, _id, value: string) => {
      const text = filterText(row.original, col)
      return col.filter === 'select' ? text === value : text.toLowerCase().includes(value.toLowerCase())
    },
  })),
)

const table = useVueTable<T>({
  get data() {
    return props.rows
  },
  get columns() {
    return defs.value
  },
  state: {
    get sorting() {
      return sorting.value
    },
    get columnFilters() {
      return filters.value
    },
  },
  get manualSorting() {
    return props.manual
  },
  get manualFiltering() {
    return props.manual
  },
  enableMultiSort: false,
  enableSortingRemoval: true,
  onSortingChange: (updater) => {
    sorting.value = typeof updater === 'function' ? updater(sorting.value) : updater
    const s = sorting.value[0]
    emit('sortChange', s ? { key: s.id, dir: s.desc ? 'desc' : 'asc' } : null)
  },
  onColumnFiltersChange: (updater) => {
    filters.value = typeof updater === 'function' ? updater(filters.value) : updater
    emit('filterChange', Object.fromEntries(filters.value.map((f) => [f.id, String(f.value)])))
  },
  getCoreRowModel: getCoreRowModel(),
  getSortedRowModel: getSortedRowModel(),
  getFilteredRowModel: getFilteredRowModel(),
})

const visibleRows = computed(() => table.getRowModel().rows.map((r) => r.original))

// A sort or a filter set while every row was here does not stay when there are more again (a new search): it would be of part of the list.
watch(partial, (now) => {
  if (!now) return
  if (sorting.value.length) table.setSorting([])
  if (filters.value.length) table.resetColumnFilters()
})

function toggleSort(col: Column<T>): void {
  if (!sortableNow(col)) return
  const s = sorting.value[0]
  const next: SortingState = !s || s.id !== col.key ? [{ id: col.key, desc: false }] : !s.desc ? [{ id: col.key, desc: true }] : []
  table.setSorting(next)
}
const sortDir = (col: Column<T>): 'asc' | 'desc' | null => {
  const s = sorting.value[0]
  return s && s.id === col.key ? (s.desc ? 'desc' : 'asc') : null
}

const hasFilters = computed(() => !partial.value && props.columns.some((c) => c.filter))
const filterValueOf = (col: Column<T>): string => String(filters.value.find((f) => f.id === col.key)?.value ?? '')
function setFilter(col: Column<T>, value: string): void {
  table.getColumn(col.key)?.setFilterValue(value === '' ? undefined : value)
}
const activeFilters = computed(() => filters.value.length > 0)
function clearFilters(): void {
  table.resetColumnFilters()
}
function choices(col: Column<T>): { value: string; label: string }[] {
  if (col.filterOptions) return col.filterOptions
  const seen = new Set<string>()
  for (const row of props.rows) {
    const text = filterText(row, col)
    if (text) seen.add(text)
  }
  return [...seen].sort((a, b) => a.localeCompare(b, undefined, { numeric: true })).map((v) => ({ value: v, label: v }))
}

const keyOf = (row: T): string | number =>
  typeof props.rowKey === 'function' ? props.rowKey(row) : ((row as Record<string, unknown>)[props.rowKey] as string | number)

const alignClass = (a?: 'left' | 'right' | 'center') => (a === 'right' ? 'text-right' : a === 'center' ? 'text-center' : 'text-left')
const ariaSort = (col: Column<T>) => (sortDir(col) ? (sortDir(col) === 'asc' ? 'ascending' : 'descending') : sortableNow(col) ? 'none' : undefined)

// What a click on a row does: a table that says `clickable` reports it; a row that has `rowTo` opens its page, unless the click was on a link, a button or a field.
const INTERACTIVE = 'a, button, input, select, textarea, label, summary, [role=menuitem], [role=button], [data-no-row-click]'
function onRowClick(row: T, event: MouseEvent): void {
  if (props.clickable) {
    emit('rowClick', row)
    return
  }
  if (!props.rowTo || (event.target as Element | null)?.closest(INTERACTIVE)) return
  const to = props.rowTo(row)
  if (to) void router?.push(to)
}
const rowCursor = computed(() => props.clickable || !!props.rowTo)

// A card does not show a part that has nothing to say: a "label: value" line with no value, a "—" and the like are left out, so a card is as short as its row. What a cell says
// is what its slot draws (the cell may draw from several fields), so the slot is drawn once to see whether it has any text or any component in it.
const slots = useSlots()
const BLANK = new Set(['', '—', '-'])
function vnodesSay(nodes: VNode[] | undefined): boolean {
  for (const n of nodes ?? []) {
    if (n.type === Comment) continue
    if (n.type === Text) {
      if (!BLANK.has(String(n.children ?? '').trim())) return true
      continue
    }
    if (n.type === Fragment || typeof n.type === 'string') {
      const c = n.children
      if (typeof c === 'string' ? !BLANK.has(c.trim()) : Array.isArray(c) && vnodesSay(c as VNode[])) return true
      continue
    }
    return true // a component draws something
  }
  return false
}
function says(col: Column<T>, row: T): boolean {
  const slot = slots[`cell-${col.key}`]
  if (slot) return vnodesSay(slot({ row, value: rowOf(row, col), card: true }))
  const v = rowOf(row, col)
  return !(v === null || v === undefined || BLANK.has(shownText(row, col).trim()))
}
const live = (cols: Column<T>[], row: T): Column<T>[] => cols.filter((c) => says(c, row))

// The card of a phone: which column plays which part.
const cardParts = computed(() => {
  const shown = props.columns.filter((c) => !c.hideOnMobile && c.key !== 'actions')
  return {
    primary: shown.filter((c) => c.card === 'primary'),
    secondary: shown.filter((c) => c.card === 'secondary'),
    badge: shown.filter((c) => c.card === 'badge'),
    money: shown.filter((c) => c.card === 'money'),
    lines: shown.filter((c) => !c.card),
  }
})
const countText = computed(() => (props.total ? t('dataTable.loadedOf', { n: props.rows.length, total: props.total }) : t('dataTable.loaded', { n: props.rows.length })))
</script>

<template>
  <div class="overflow-x-auto" v-bind="attrs" data-slot="data-table">
    <table v-if="!cardsOn" class="w-full border-collapse text-sm">
      <caption v-if="caption" class="sr-only">{{ caption }}</caption>
      <thead>
        <tr>
          <th
            v-for="col in columns"
            :key="col.key"
            scope="col"
            :aria-sort="ariaSort(col)"
            :class="cn('whitespace-nowrap border-b border-border px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground', alignClass(col.align), col.class)"
          >
            <button
              v-if="sortableNow(col)"
              type="button"
              :data-testid="`sort-${col.key}`"
              :class="cn('inline-flex cursor-pointer items-center gap-1 border-0 bg-transparent p-0 text-xs font-semibold uppercase tracking-wide text-muted-foreground hover:text-foreground', col.align === 'right' && 'flex-row-reverse')"
              @click="toggleSort(col)"
            >
              {{ col.label }}
              <ArrowUp v-if="sortDir(col) === 'asc'" class="size-3.5" aria-hidden="true" />
              <ArrowDown v-else-if="sortDir(col) === 'desc'" class="size-3.5" aria-hidden="true" />
              <ChevronsUpDown v-else class="size-3.5 opacity-50" aria-hidden="true" />
            </button>
            <slot v-else :name="`header-${col.key}`" :column="col">{{ col.label }}</slot>
          </th>
        </tr>
        <tr v-if="hasFilters" data-testid="table-filters">
          <th v-for="col in columns" :key="`filter-${col.key}`" class="border-b border-border px-3 pb-2 pt-0 font-normal">
            <Input
              v-if="col.filter === 'text'"
              :model-value="filterValueOf(col)"
              :name="`filter_${col.key}`"
              :placeholder="t('dataTable.filter')"
              :aria-label="t('dataTable.filterBy', { column: col.label })"
              class="h-8 text-xs"
              @update:model-value="(v) => setFilter(col, String(v ?? ''))"
            />
            <NativeSelect
              v-else-if="col.filter === 'select'"
              :model-value="filterValueOf(col)"
              :name="`filter_${col.key}`"
              :aria-label="t('dataTable.filterBy', { column: col.label })"
              class="h-8 text-xs"
              @update:model-value="(v) => setFilter(col, String(v ?? ''))"
            >
              <option value="">{{ t('dataTable.all') }}</option>
              <option v-for="o in choices(col)" :key="o.value" :value="o.value">{{ o.label }}</option>
            </NativeSelect>
          </th>
        </tr>
      </thead>
      <tbody>
        <template v-if="loading && !rows.length">
          <tr v-for="n in 5" :key="`skeleton-${n}`" data-testid="table-loading">
            <td v-for="col in columns" :key="col.key" class="border-b border-border px-3 py-3"><Skeleton class="h-4 w-full max-w-40" /></td>
          </tr>
        </template>
        <template v-else>
          <template v-for="row in visibleRows" :key="keyOf(row)">
          <tr
            :data-testid="rowTestId?.(row)"
            :tabindex="clickable ? 0 : undefined"
            :class="cn('border-b border-border hover:bg-accent/50', rowCursor && 'cursor-pointer', rowClass?.(row))"
            @click="onRowClick(row, $event)"
            @keydown.enter="clickable && emit('rowClick', row)"
          >
            <td v-for="col in columns" :key="col.key" :class="cn('px-3 py-2.5 align-middle', alignClass(col.align), col.class)">
              <slot :name="`cell-${col.key}`" :row="row" :value="(row as Record<string, unknown>)[col.key]" :card="false">
                <TableRowActions v-if="col.key === 'actions' && rowActions" :actions="rowActions(row)" />
                <template v-else>{{ shown(row, col) }}</template>
              </slot>
            </td>
          </tr>
          <tr v-if="isExpanded?.(row)" class="border-b border-border bg-muted/40" :data-testid="detailTestId?.(row)">
            <td :colspan="columns.length" class="px-3 py-2.5"><slot name="detail" :row="row" /></td>
          </tr>
          </template>
        </template>
      </tbody>
    </table>
    <ul v-else class="m-0 grid list-none gap-3 p-0" data-slot="data-cards">
      <template v-if="loading && !rows.length">
        <li v-for="n in 3" :key="`skeleton-${n}`" class="rounded-lg border border-border p-3" data-testid="table-loading"><Skeleton class="h-4 w-2/3" /><Skeleton class="mt-2 h-4 w-full" /></li>
      </template>
      <template v-else>
        <li
          v-for="row in visibleRows"
          :key="keyOf(row)"
          :data-testid="rowTestId?.(row)"
          data-slot="data-card"
          :class="cn('rounded-lg border border-border bg-card p-3', rowCursor && 'cursor-pointer', rowClass?.(row))"
          @click="onRowClick(row, $event)"
        >
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0">
              <div v-for="col in live(cardParts.primary, row)" :key="col.key" class="font-medium">
                <slot :name="`cell-${col.key}`" :row="row" :value="rowOf(row, col)" :card="true">{{ shown(row, col) }}</slot>
              </div>
              <div v-for="col in live(cardParts.secondary, row)" :key="col.key" class="text-sm text-muted-foreground">
                <slot :name="`cell-${col.key}`" :row="row" :value="rowOf(row, col)" :card="true">{{ shown(row, col) }}</slot>
              </div>
            </div>
            <div v-if="live(cardParts.badge, row).length" class="flex shrink-0 flex-col items-end gap-1">
              <div v-for="col in live(cardParts.badge, row)" :key="col.key">
                <slot :name="`cell-${col.key}`" :row="row" :value="rowOf(row, col)" :card="true">{{ shown(row, col) }}</slot>
              </div>
            </div>
          </div>
          <dl v-if="live(cardParts.lines, row).length" class="m-0 mt-2 grid gap-1 text-sm">
            <div v-for="col in live(cardParts.lines, row)" :key="col.key" class="flex items-baseline justify-between gap-3">
              <dt class="shrink-0 text-muted-foreground">{{ col.label }}</dt>
              <dd class="m-0 min-w-0 text-right">
                <slot :name="`cell-${col.key}`" :row="row" :value="rowOf(row, col)" :card="true">{{ shown(row, col) }}</slot>
              </dd>
            </div>
          </dl>
          <p v-for="col in live(cardParts.money, row)" :key="col.key" class="m-0 mt-2 text-right text-base font-semibold tabular-nums">
            <slot :name="`cell-${col.key}`" :row="row" :value="rowOf(row, col)" :card="true">{{ shown(row, col) }}</slot>
          </p>
          <TableRowActions v-if="rowActions && rowActions(row).length" class="mt-2" :actions="rowActions(row)" :max-primary="1" />
          <div v-if="isExpanded?.(row)" class="mt-2 overflow-x-auto border-t border-border pt-2" :data-testid="detailTestId?.(row)"><slot name="detail" :row="row" /></div>
        </li>
      </template>
    </ul>
    <div v-if="!loading && rows.length && !visibleRows.length && activeFilters" class="flex flex-col items-center gap-2 py-8 text-sm text-muted-foreground" data-testid="table-no-match">
      <span>{{ t('dataTable.noMatch') }}</span>
      <Button type="button" variant="outline" size="sm" data-testid="table-clear-filters" @click="clearFilters">{{ t('dataTable.clearFilters') }}</Button>
    </div>
    <slot v-if="!loading && !rows.length" name="empty">
      <EmptyState :title="emptyTitle || t('common.noResults')" :description="emptyDescription" data-testid="table-empty" />
    </slot>
    <slot name="footer" />
    <div v-if="paged && rows.length" class="flex flex-col items-center gap-1.5 p-3" data-testid="table-paging">
      <p class="m-0 text-xs text-muted-foreground" data-testid="table-count">{{ countText }}</p>
      <Button v-if="hasMore" type="button" variant="outline" size="sm" :disabled="loadingMore" data-testid="more" @click="emit('loadMore')">{{ loadMoreLabel || t('dataTable.loadMore') }}</Button>
    </div>
  </div>
</template>
