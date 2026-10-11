import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'

export interface ServerSort<K extends string = string> {
  key: K
  dir: 'asc' | 'desc'
}

/**
 * The order of a list that the server sorts (the list is read a page at a time), kept in the address of the page: `?sort=<column>&order=asc|desc`. The address is the one place the order
 * lives, so a link carries it, a reload keeps it and the Back button returns to the order before (a change of order is a new entry of the history; the filters of the page replace theirs).
 *
 * `allowed` are the columns the server knows (a column that is not among them in the address is ignored, as no order). `sort` is null for the list's own order. `params` are what the request
 * adds. `setSort` writes the order to the address and leaves the other parameters of it alone; the page watches `sort` and reads the list again from its first page (a cursor belongs to an order).
 */
export function useServerSort<K extends string>(allowed: readonly K[]) {
  const route = useRoute()
  const router = useRouter()

  const sort = computed<ServerSort<K> | null>(() => {
    const key = route.query.sort
    const known = allowed.find((k) => k === key)
    if (known === undefined) return null
    return { key: known, dir: route.query.order === 'desc' ? 'desc' : 'asc' }
  })
  const params = computed<{ sort?: K; order?: 'asc' | 'desc' }>(() => (sort.value ? { sort: sort.value.key, order: sort.value.dir } : {}))
  /** The parameters of the address that belong to the order, to put back when a page rewrites its address. */
  const query = computed(() => {
    const out: Record<string, string> = {}
    if (sort.value) {
      out.sort = sort.value.key
      out.order = sort.value.dir
    }
    return out
  })

  function setSort(next: { key: string; dir: 'asc' | 'desc' } | null): void {
    const { sort: _s, order: _o, ...rest } = route.query
    void router.push({ query: { ...rest, ...(next && allowed.some((k) => k === next.key) ? { sort: next.key, order: next.dir } : {}) } })
  }

  return { sort, params, query, setSort }
}
