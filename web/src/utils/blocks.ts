import { addDays } from './dates'

export interface DateRange {
  start_date: string
  end_date: string // exclusive
}

/**
 * Grid columns (1-based, end exclusive) a half-open date range occupies in a calendar
 * window of `days` days starting at `windowStart`, or null when it is outside the window.
 * ISO dates compare correctly as strings, so no Date objects (and no time zones) are involved.
 */
export function blockColumns(range: DateRange, windowStart: string, days: number): { start: number; end: number } | null {
  const windowEnd = addDays(windowStart, days)
  if (range.end_date <= windowStart || range.start_date >= windowEnd) return null
  const from = range.start_date < windowStart ? windowStart : range.start_date
  const to = range.end_date > windowEnd ? windowEnd : range.end_date
  return { start: dayIndex(windowStart, from) + 1, end: dayIndex(windowStart, to) + 1 }
}

/** The dates shown by a calendar window. */
export function windowDates(windowStart: string, days: number): string[] {
  return Array.from({ length: days }, (_, i) => addDays(windowStart, i))
}

function dayIndex(from: string, to: string): number {
  return Math.round((Date.parse(`${to}T00:00:00Z`) - Date.parse(`${from}T00:00:00Z`)) / 86_400_000)
}
