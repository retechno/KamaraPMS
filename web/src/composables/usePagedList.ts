import { computed, ref, type Ref } from 'vue'
import { ApiError } from '@/api/problem'

const failure = (e: unknown): ApiError => (e instanceof ApiError ? e : new ApiError({ type: 'about:blank', title: 'Error', status: 0, code: 'NETWORK_ERROR', detail: String(e) }))

/** A page of a list: the rows, and the cursor of the next page when there is one. `total` only when the API says how many there are in all. */
export interface Page<T> {
  data: T[]
  next_cursor?: string
  total?: number
}

/**
 * A list that is read one page at a time (the API's `limit` and `cursor`, 50 rows a page). The first page is read by `reload()`, and again whenever a filter or a search
 * changes; the next one by `loadMore()`, which adds it to the rows. The pages are never all read at once. Only the latest request counts: an answer that comes after a
 * newer search was made is dropped, so a slow answer cannot replace a newer one.
 *
 * What the list asks for (the filters, the search, the sort) is read once per `reload()` by `snapshot()` and handed to `fetchPage` as `params`. `loadMore()` never reads
 * the screen again: it asks for the next page with the params of the page it continues, so a filter that is edited and not yet applied cannot be mixed into the rows of
 * the old one (the cursor belongs to the old query). The params of a reload are kept when its first page is taken in, so a reload that fails, or an older one that
 * answers late, leaves the params that go with the rows and the cursor on the screen.
 */
export function usePagedList<T, P>(snapshot: () => P, fetchPage: (cursor: string | undefined, params: P) => Promise<Page<T>>) {
  const rows = ref<T[]>([]) as Ref<T[]>
  const nextCursor = ref<string | undefined>()
  const total = ref<number | null>(null)
  const loading = ref(false)
  const loadingMore = ref(false)
  /** The first page has come (a failed read is not "loaded": a page shows its error, not an empty list). */
  const loaded = ref(false)
  const error = ref<ApiError | null>(null)
  let generation = 0
  /** The params of the first page that is on the screen: what `loadMore()` continues with. */
  let frozen: { params: P } | null = null

  const hasMore = computed(() => nextCursor.value !== undefined)

  /** Starts again from the first page (rows already shown stay until the new page comes). */
  async function reload(): Promise<void> {
    const mine = ++generation
    loading.value = true
    loadingMore.value = false
    error.value = null
    const params = snapshot()
    try {
      const page = await fetchPage(undefined, params)
      if (mine !== generation) return
      frozen = { params }
      rows.value = page.data ?? []
      nextCursor.value = page.next_cursor
      total.value = page.total ?? null
      loaded.value = true
    } catch (e) {
      if (mine !== generation) return
      error.value = failure(e)
    } finally {
      if (mine === generation) loading.value = false
    }
  }

  /** Adds the next page to the rows. */
  async function loadMore(): Promise<void> {
    const cursor = nextCursor.value
    if (cursor === undefined || frozen === null || loading.value || loadingMore.value) return
    const { params } = frozen
    const mine = generation
    loadingMore.value = true
    error.value = null
    try {
      const page = await fetchPage(cursor, params)
      if (mine !== generation) return
      rows.value = [...rows.value, ...(page.data ?? [])]
      nextCursor.value = page.next_cursor
      if (page.total !== undefined) total.value = page.total
    } catch (e) {
      if (mine !== generation) return
      error.value = failure(e)
    } finally {
      if (mine === generation) loadingMore.value = false
    }
  }

  /** Forgets everything (another property was chosen). */
  function reset(): void {
    generation++
    frozen = null
    rows.value = []
    nextCursor.value = undefined
    total.value = null
    loading.value = false
    loadingMore.value = false
    loaded.value = false
    error.value = null
  }

  return { rows, hasMore, total, loading, loadingMore, loaded, error, reload, loadMore, reset }
}
