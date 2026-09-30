import { describe, expect, it } from 'vitest'
import { addDays, formatBusinessDate, todayIn, wallClock } from './dates'

describe('dates', () => {
  it('formats business dates without time zones', () => {
    expect(formatBusinessDate('2026-09-30')).toBe('30 Sep 2026')
    expect(formatBusinessDate('2026-01-05')).toBe('5 Jan 2026')
    expect(formatBusinessDate('garbage')).toBe('garbage')
  })

  it("reads the property's wall clock, not the browser's", () => {
    // 02:30 on 1 Oct in Jakarta must stay 02:30 on 1 Oct whatever zone the browser is in.
    expect(wallClock('2026-10-01T02:30:00+07:00')).toEqual({ date: '2026-10-01', time: '02:30', offset: '+07:00' })
    expect(wallClock('2026-09-30T19:30:00Z')).toEqual({ date: '2026-09-30', time: '19:30', offset: '+00:00' })
    expect(wallClock('not a time')).toBeNull()
  })

  it("computes today's date in a property time zone", () => {
    const instant = new Date('2026-09-30T19:30:00Z')
    expect(todayIn('Asia/Jakarta', instant)).toBe('2026-10-01')
    expect(todayIn('UTC', instant)).toBe('2026-09-30')
    expect(todayIn('Mars/Olympus', instant)).toBeNull()
  })

  it('adds days across month and year boundaries', () => {
    expect(addDays('2026-09-30', 1)).toBe('2026-10-01')
    expect(addDays('2026-01-01', -1)).toBe('2025-12-31')
    expect(addDays('2028-02-28', 1)).toBe('2028-02-29')
  })
})
