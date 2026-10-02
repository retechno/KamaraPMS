<script setup lang="ts" generic="T extends object">
import { ArrowDown, ArrowUp, ChevronsUpDown } from 'lucide-vue-next'
import { computed, ref, useAttrs } from 'vue'
import EmptyState from '@/components/app/EmptyState.vue'
import { Skeleton } from '@/components/ui/skeleton'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

/**
 * The one table of the application: a header that can sort, rows with a cell slot per column (`#cell-<key>="{ row,
 * value }"`), a header slot per column (`#header-<key>`, for a select-all box), a loading state with skeleton rows, and an empty state. Sorting is done here on the rows it was given;
 * a page that pages through the server keeps its own order and leaves `sortable` off.
 */
export interface Column<R> {
  key: string
  label: string
  align?: 'left' | 'right' | 'center'
  sortable?: boolean
  /** What to sort by, when it is not the value of `key` (a number inside text, a date). */
  sortValue?: (row: R) => string | number | null | undefined
  class?: string
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
  }>(),
  { loading: false, emptyTitle: '', emptyDescription: '', clickable: false, caption: '', rowTestId: undefined, rowClass: undefined },
)
const emit = defineEmits<{ rowClick: [row: T] }>()
defineOptions({ inheritAttrs: false })
const attrs = useAttrs()

const sort = ref<{ key: string; dir: 'asc' | 'desc' } | null>(null)

function toggleSort(col: Column<T>): void {
  if (!col.sortable) return
  const s = sort.value
  if (!s || s.key !== col.key) sort.value = { key: col.key, dir: 'asc' }
  else if (s.dir === 'asc') sort.value = { key: col.key, dir: 'desc' }
  else sort.value = null
}

const valueOf = (row: T, col: Column<T>): unknown => (col.sortValue ? col.sortValue(row) : (row as Record<string, unknown>)[col.key])

function compare(a: unknown, b: unknown): number {
  const missing = (v: unknown) => v === null || v === undefined || v === ''
  if (missing(a) && missing(b)) return 0
  if (missing(a)) return 1 // an empty value goes last in both directions
  if (missing(b)) return -1
  if (typeof a === 'number' && typeof b === 'number') return a - b
  const na = Number(a)
  const nb = Number(b)
  if (a !== '' && b !== '' && Number.isFinite(na) && Number.isFinite(nb)) return na - nb // amounts arrive as strings
  return String(a).localeCompare(String(b), undefined, { numeric: true, sensitivity: 'base' })
}

const sorted = computed(() => {
  const s = sort.value
  if (!s) return props.rows
  const col = props.columns.find((c) => c.key === s.key)
  if (!col) return props.rows
  const sign = s.dir === 'asc' ? 1 : -1
  return [...props.rows].sort((a, b) => {
    const x = valueOf(a, col)
    const y = valueOf(b, col)
    const empty = (v: unknown) => v === null || v === undefined || v === ''
    if (empty(x) || empty(y)) return compare(x, y)
    return sign * compare(x, y)
  })
})

const keyOf = (row: T): string | number =>
  typeof props.rowKey === 'function' ? props.rowKey(row) : ((row as Record<string, unknown>)[props.rowKey] as string | number)

const alignClass = (a?: 'left' | 'right' | 'center') => (a === 'right' ? 'text-right' : a === 'center' ? 'text-center' : 'text-left')
const ariaSort = (col: Column<T>) => (sort.value?.key === col.key ? (sort.value.dir === 'asc' ? 'ascending' : 'descending') : col.sortable ? 'none' : undefined)
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
              <ArrowUp v-if="sort?.key === col.key && sort.dir === 'asc'" class="size-3.5" aria-hidden="true" />
              <ArrowDown v-else-if="sort?.key === col.key" class="size-3.5" aria-hidden="true" />
              <ChevronsUpDown v-else class="size-3.5 opacity-50" aria-hidden="true" />
            </button>
            <slot v-else :name="`header-${col.key}`" :column="col">{{ col.label }}</slot>
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
          <tr
            v-for="row in sorted"
            :key="keyOf(row)"
            :data-testid="rowTestId?.(row)"
            :tabindex="clickable ? 0 : undefined"
            :class="cn('border-b border-border hover:bg-accent/50', clickable && 'cursor-pointer', rowClass?.(row))"
            @click="clickable && emit('rowClick', row)"
            @keydown.enter="clickable && emit('rowClick', row)"
          >
            <td v-for="col in columns" :key="col.key" :class="cn('px-3 py-2.5 align-middle', alignClass(col.align), col.class)">
              <slot :name="`cell-${col.key}`" :row="row" :value="(row as Record<string, unknown>)[col.key]">
                {{ (row as Record<string, unknown>)[col.key] ?? '—' }}
              </slot>
            </td>
          </tr>
        </template>
      </tbody>
    </table>
    <slot v-if="!loading && !rows.length" name="empty">
      <EmptyState :title="emptyTitle || t('common.noResults')" :description="emptyDescription" data-testid="table-empty" />
    </slot>
    <slot name="footer" />
  </div>
</template>
