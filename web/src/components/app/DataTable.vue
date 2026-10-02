<script setup lang="ts" generic="T extends object">
import {
  type ColumnDef, type ColumnFiltersState, type SortingState, getCoreRowModel, getFilteredRowModel, getSortedRowModel, useVueTable,
} from '@tanstack/vue-table'
import { ArrowDown, ArrowUp, ChevronsUpDown } from 'lucide-vue-next'
import { computed, ref, useAttrs } from 'vue'
import EmptyState from '@/components/app/EmptyState.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { formatDate, formatDateTime, formatMoney } from '@/utils/format'

/**
 * The one table of the application: a header that can sort and filter, rows with a cell slot per column (`#cell-<key>="{ row,
 * value }"`), a header slot per column (`#header-<key>`, for a select-all box), a loading state with skeleton rows, and an empty state.
 *
 * The rows are laid out by TanStack Table (headless: sorting, filtering and the row model; the markup is ours). A column
 * opts in with `sortable` and `filter`. Sorting and filtering are done here on the rows given; a page that pages
 * through the server sets `manual`, keeps the rows as the server sent them and listens to `sortChange` and `filterChange`.
 */
export interface Column<R> {
  key: string
  label: string
  align?: 'left' | 'right' | 'center'
  sortable?: boolean
  /** What to sort by, when it is not the value of `key` (a number inside text, a date). */
  sortValue?: (row: R) => string | number | null | undefined
  class?: string
  /** How the default cell shows the value: an amount, a business date or an instant, in the language of the page. */
  format?: 'money' | 'date' | 'datetime'
  /** A filter box under the header: a text that the shown value must contain, or a choice among the values. */
  filter?: 'text' | 'select'
  /** What the filter reads, when it is not the text the cell shows (a column with a custom cell). */
  filterValue?: (row: R) => string | null | undefined
  /** The choices of a `select` filter; by default the distinct values of the rows. */
  filterOptions?: { value: string; label: string }[]
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
  }>(),
  { loading: false, emptyTitle: '', emptyDescription: '', clickable: false, caption: '', rowTestId: undefined, rowClass: undefined, isExpanded: undefined, detailTestId: undefined, manual: false },
)
const emit = defineEmits<{
  rowClick: [row: T]
  sortChange: [sort: { key: string; dir: 'asc' | 'desc' } | null]
  filterChange: [filters: Record<string, string>]
}>()
defineOptions({ inheritAttrs: false })
const attrs = useAttrs()

const sorting = ref<SortingState>([])
const filters = ref<ColumnFiltersState>([])

const rowOf = (row: T, col: Column<T>): unknown => (row as Record<string, unknown>)[col.key]

/** The text a cell shows for a value, in the language of the page. */
function shownText(row: T, col: Column<T>): string {
  const v = rowOf(row, col)
  if (v === null || v === undefined) return ''
  const text = String(v)
  return col.format === 'money' ? formatMoney(text) : col.format === 'date' ? formatDate(text) : col.format === 'datetime' ? formatDateTime(text) : text
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
    enableSorting: !!col.sortable,
    sortUndefined: 'last' as const,
    sortingFn: (a, b, id) => compare(a.getValue(id), b.getValue(id)),
    enableColumnFilter: !!col.filter,
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

function toggleSort(col: Column<T>): void {
  if (!col.sortable) return
  const s = sorting.value[0]
  const next: SortingState = !s || s.id !== col.key ? [{ id: col.key, desc: false }] : !s.desc ? [{ id: col.key, desc: true }] : []
  table.setSorting(next)
}
const sortDir = (col: Column<T>): 'asc' | 'desc' | null => {
  const s = sorting.value[0]
  return s && s.id === col.key ? (s.desc ? 'desc' : 'asc') : null
}

const hasFilters = computed(() => props.columns.some((c) => c.filter))
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
const ariaSort = (col: Column<T>) => (sortDir(col) ? (sortDir(col) === 'asc' ? 'ascending' : 'descending') : col.sortable ? 'none' : undefined)
</script>

<template>
  <div class="overflow-x-auto" v-bind="attrs" data-slot="data-table">
    <table class="w-full border-collapse text-sm">
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
              v-if="col.sortable"
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
            :class="cn('border-b border-border hover:bg-accent/50', clickable && 'cursor-pointer', rowClass?.(row))"
            @click="clickable && emit('rowClick', row)"
            @keydown.enter="clickable && emit('rowClick', row)"
          >
            <td v-for="col in columns" :key="col.key" :class="cn('px-3 py-2.5 align-middle', alignClass(col.align), col.class)">
              <slot :name="`cell-${col.key}`" :row="row" :value="(row as Record<string, unknown>)[col.key]">
                {{ shown(row, col) }}
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
    <div v-if="!loading && rows.length && !visibleRows.length && activeFilters" class="flex flex-col items-center gap-2 py-8 text-sm text-muted-foreground" data-testid="table-no-match">
      <span>{{ t('dataTable.noMatch') }}</span>
      <Button type="button" variant="outline" size="sm" data-testid="table-clear-filters" @click="clearFilters">{{ t('dataTable.clearFilters') }}</Button>
    </div>
    <slot v-if="!loading && !rows.length" name="empty">
      <EmptyState :title="emptyTitle || t('common.noResults')" :description="emptyDescription" data-testid="table-empty" />
    </slot>
    <slot name="footer" />
  </div>
</template>
