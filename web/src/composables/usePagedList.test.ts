import { describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/problem'
import { type Page, usePagedList } from './usePagedList'

const page = (from: number, count: number, next?: string, total?: number): Page<number> => ({ data: Array.from({ length: count }, (_, i) => from + i), next_cursor: next, total })

/** A promise that is settled by hand, to answer requests in the wrong order. */
function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe('usePagedList', () => {
  it('reads the first page with no cursor, then asks for the next one with the cursor it was given', async () => {
    const fetch = vi.fn(async (cursor: string | undefined) => (cursor === undefined ? page(1, 50, 'c2') : cursor === 'c2' ? page(51, 50, 'c3') : page(101, 7)))
    const list = usePagedList<number>(fetch)
    expect(list.loaded.value).toBe(false)
    await list.reload()
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(fetch).toHaveBeenLastCalledWith(undefined)
    expect(list.rows.value).toHaveLength(50)
    expect(list.hasMore.value).toBe(true)
    expect(list.loaded.value).toBe(true)
    await list.loadMore()
    expect(fetch).toHaveBeenLastCalledWith('c2')
    expect(list.rows.value).toHaveLength(100)
    expect(list.hasMore.value).toBe(true)
    await list.loadMore()
    expect(fetch).toHaveBeenLastCalledWith('c3')
    expect(list.rows.value).toHaveLength(107)
    expect(list.hasMore.value).toBe(false) // the last page has no cursor
    expect(list.rows.value.slice(48, 52)).toEqual([49, 50, 51, 52]) // in order, none twice
  })

  it('reads one page for each call and never all of them at once', async () => {
    const fetch = vi.fn(async () => page(1, 50, 'more'))
    const list = usePagedList<number>(fetch)
    await list.reload()
    expect(fetch).toHaveBeenCalledTimes(1)
    await flush()
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('does nothing when there is no next page, or while a page is already coming', async () => {
    const gate = deferred<Page<number>>()
    const fetch = vi.fn((cursor: string | undefined) => (cursor === undefined ? Promise.resolve(page(1, 3, 'c2')) : gate.promise))
    const list = usePagedList<number>(fetch)
    await list.loadMore() // nothing read yet: no cursor
    expect(fetch).not.toHaveBeenCalled()
    await list.reload()
    const first = list.loadMore()
    expect(list.loadingMore.value).toBe(true)
    await list.loadMore() // the same page is not asked twice
    expect(fetch).toHaveBeenCalledTimes(2)
    gate.resolve(page(4, 2))
    await first
    expect(list.loadingMore.value).toBe(false)
    expect(list.rows.value).toHaveLength(5)
    await list.loadMore()
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it('starts again from the first page when it is reloaded (a filter or a search changed)', async () => {
    let query = 'a'
    const fetch = vi.fn(async (cursor: string | undefined) => (cursor === undefined ? { data: [query + '1', query + '2'], next_cursor: 'c2' } : { data: [query + '3'] }))
    const list = usePagedList<string>(fetch)
    await list.reload()
    await list.loadMore()
    expect(list.rows.value).toEqual(['a1', 'a2', 'a3'])
    query = 'b'
    await list.reload()
    expect(fetch).toHaveBeenLastCalledWith(undefined)
    expect(list.rows.value).toEqual(['b1', 'b2'])
    expect(list.hasMore.value).toBe(true)
  })

  it('keeps the total when the API gives one', async () => {
    const list = usePagedList<number>(async (cursor) => (cursor === undefined ? page(1, 50, 'c2', 130) : page(51, 50, 'c3')))
    expect(list.total.value).toBeNull()
    await list.reload()
    expect(list.total.value).toBe(130)
    await list.loadMore()
    expect(list.total.value).toBe(130)
    const none = usePagedList<number>(async () => page(1, 1))
    await none.reload()
    expect(none.total.value).toBeNull()
  })

  describe('only the latest request counts', () => {
    it('drops the answer of an older search that comes after a newer one', async () => {
      const slow = deferred<Page<string>>()
      const fast = deferred<Page<string>>()
      const queue = [slow, fast]
      const list = usePagedList<string>(() => queue.shift()!.promise)
      const older = list.reload()
      const newer = list.reload()
      fast.resolve({ data: ['new'] })
      await newer
      slow.resolve({ data: ['old'] })
      await older
      expect(list.rows.value).toEqual(['new'])
      expect(list.loading.value).toBe(false)
    })

    it('drops a next page that was asked for before a new search', async () => {
      const gate = deferred<Page<string>>()
      let phase = 0
      const list = usePagedList<string>((cursor) => {
        if (cursor === 'c2') return gate.promise
        return Promise.resolve(phase++ === 0 ? { data: ['a1'], next_cursor: 'c2' } : { data: ['b1'] })
      })
      await list.reload()
      const more = list.loadMore()
      await list.reload() // a new search
      gate.resolve({ data: ['a2'] })
      await more
      expect(list.rows.value).toEqual(['b1'])
      expect(list.loadingMore.value).toBe(false)
    })

    it('drops the answer that comes after the list was reset (another property was chosen)', async () => {
      const gate = deferred<Page<number>>()
      const list = usePagedList<number>(() => gate.promise)
      const pending = list.reload()
      list.reset()
      gate.resolve(page(1, 5))
      await pending
      expect(list.rows.value).toEqual([])
      expect(list.loaded.value).toBe(false)
      expect(list.loading.value).toBe(false)
    })
  })

  describe('a failure', () => {
    const refusal = new ApiError({ type: 't', title: 'Unavailable', status: 503, code: 'SERVICE_UNAVAILABLE', detail: 'later' })

    it('is kept as it came, is not "loaded", and is cleared by the next read', async () => {
      let fail = true
      const list = usePagedList<number>(async () => {
        if (fail) throw refusal
        return page(1, 2)
      })
      await list.reload()
      expect(list.error.value).toBe(refusal)
      expect(list.loaded.value).toBe(false) // a page shows its error, not an empty list
      expect(list.loading.value).toBe(false)
      fail = false
      await list.reload()
      expect(list.error.value).toBeNull()
      expect(list.loaded.value).toBe(true)
    })

    it('is a network error when it is not an answer of the API', async () => {
      const list = usePagedList<number>(async () => {
        throw new TypeError('Failed to fetch')
      })
      await list.reload()
      expect(list.error.value?.code).toBe('NETWORK_ERROR')
      expect(list.error.value?.status).toBe(0)
    })

    it('leaves the rows that are shown when the next page cannot be read, and keeps the cursor to try again', async () => {
      let fail = true
      const list = usePagedList<number>(async (cursor) => {
        if (cursor === undefined) return page(1, 3, 'c2')
        if (fail) throw refusal
        return page(4, 1)
      })
      await list.reload()
      await list.loadMore()
      expect(list.error.value).toBe(refusal)
      expect(list.rows.value).toHaveLength(3)
      expect(list.hasMore.value).toBe(true)
      fail = false
      await list.loadMore()
      expect(list.rows.value).toHaveLength(4)
      expect(list.error.value).toBeNull()
    })
  })

  it('forgets everything on reset', async () => {
    const list = usePagedList<number>(async () => page(1, 5, 'c2', 90))
    await list.reload()
    list.reset()
    expect(list.rows.value).toEqual([])
    expect(list.hasMore.value).toBe(false)
    expect(list.total.value).toBeNull()
    expect(list.loaded.value).toBe(false)
    expect(list.error.value).toBeNull()
  })
})

const flush = (): Promise<void> => new Promise((r) => setTimeout(r, 0))
