/** Follows `next_cursor` until the list is exhausted (used by master-data lists that are small by nature). */
export async function fetchAll<T>(page: (cursor?: string) => Promise<{ data?: { data: T[]; next_cursor?: string } }>): Promise<T[]> {
  const out: T[] = []
  let cursor: string | undefined
  do {
    const res = await page(cursor)
    out.push(...(res.data?.data ?? []))
    cursor = res.data?.next_cursor
  } while (cursor)
  return out
}
