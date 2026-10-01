import { api } from '@/api/client'

/** Downloads a report as CSV: the same request with `format=csv`, saved as a file. */
export async function downloadCsv(path: string, params: { path: object; query?: Record<string, unknown> }, filename: string): Promise<void> {
  const get = api.GET as unknown as (path: string, init: object) => Promise<{ data?: unknown }>
  const { data } = await get(path, { params: { path: params.path, query: { ...params.query, format: 'csv' } }, parseAs: 'text' })
  const url = URL.createObjectURL(new Blob([String(data ?? '')], { type: 'text/csv;charset=utf-8' }))
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}

/** Zero amounts are shown as a dash so the columns of a statement stay readable. */
export function money(v: string | undefined): string {
  if (v === undefined || v === '' || Number(v) === 0) return '–'
  return v
}
