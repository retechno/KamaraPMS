import { describe, expect, it } from 'vitest'
import { fiscalYearToDate, lastMonth, thisMonth } from './periods'

describe('periods', () => {
  it('counts this month from its first day to the business date', () => {
    expect(thisMonth('2026-10-10')).toEqual({ from: '2026-10-01', to: '2026-10-10' })
    expect(thisMonth('2026-02-01')).toEqual({ from: '2026-02-01', to: '2026-02-01' })
  })

  it('counts last month as the whole month before, over the turn of the year and in a leap year', () => {
    expect(lastMonth('2026-10-10')).toEqual({ from: '2026-09-01', to: '2026-09-30' })
    expect(lastMonth('2026-01-15')).toEqual({ from: '2025-12-01', to: '2025-12-31' })
    expect(lastMonth('2026-03-05')).toEqual({ from: '2026-02-01', to: '2026-02-28' })
    expect(lastMonth('2028-03-05')).toEqual({ from: '2028-02-01', to: '2028-02-29' })
  })

  it('counts the fiscal year from its start to the business date, and says nothing when no fiscal year has the date', () => {
    const years = [{ year_start: '2025-07-01', year_end: '2026-06-30' }, { year_start: '2026-07-01', year_end: '2027-06-30' }]
    expect(fiscalYearToDate(years, '2026-10-10')).toEqual({ from: '2026-07-01', to: '2026-10-10' })
    expect(fiscalYearToDate(years, '2026-06-30')).toEqual({ from: '2025-07-01', to: '2026-06-30' })
    expect(fiscalYearToDate(years, '2028-01-01')).toBeNull()
    expect(fiscalYearToDate([], '2026-10-10')).toBeNull()
  })
})
