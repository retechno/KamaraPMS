import type { FiscalYear } from '@/api/types'

/** A period of dates (both ends included), the filter of a report. */
export interface Period {
  from: string
  to: string
}

const pad = (n: number): string => String(n).padStart(2, '0')

/** From the first of the month of the business date to the business date. */
export function thisMonth(businessDate: string): Period {
  return { from: `${businessDate.slice(0, 8)}01`, to: businessDate }
}

/** The whole month before the month of the business date. */
export function lastMonth(businessDate: string): Period {
  const [y = 0, m = 1] = businessDate.split('-').map(Number)
  const year = m === 1 ? y - 1 : y
  const month = m === 1 ? 12 : m - 1
  const lastDay = new Date(Date.UTC(y, m - 1, 0)).getUTCDate()
  return { from: `${year}-${pad(month)}-01`, to: `${year}-${pad(month)}-${pad(lastDay)}` }
}

/** From the start of the fiscal year the business date is in to the business date; nothing when no fiscal year has been set up for it. */
export function fiscalYearToDate(years: readonly Pick<FiscalYear, 'year_start' | 'year_end'>[], businessDate: string): Period | null {
  const year = years.find((y) => y.year_start <= businessDate && businessDate <= y.year_end)
  return year ? { from: year.year_start, to: businessDate } : null
}
