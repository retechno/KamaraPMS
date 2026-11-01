import { describe, expect, it } from 'vitest'
import { addDays, formatBusinessDate, formatDisplayDate, looksLikeWholeDate, maskDateInput, parseDisplayDate, todayIn, wallClock } from './dates'

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

describe('formatBusinessDate in Indonesian', () => {
  it('names the month in the language of the page', async () => {
    const { setLocale } = await import('@/i18n')
    setLocale('id')
    try {
      expect(formatBusinessDate('2026-08-17')).toBe('17 Agu 2026')
      expect(formatBusinessDate('2026-05-01')).toBe('1 Mei 2026')
    } finally {
      setLocale('en')
    }
    expect(formatBusinessDate('2026-08-17')).toBe('17 Aug 2026')
  })
})

describe('the date a person types (dd/mm/yyyy)', () => {
  it('reads d/m/yyyy with a slash, a dash or a dot', () => {
    expect(parseDisplayDate('02/11/2026')).toBe('2026-11-02')
    expect(parseDisplayDate('2/11/2026')).toBe('2026-11-02')
    expect(parseDisplayDate('2-1-2026')).toBe('2026-01-02')
    expect(parseDisplayDate('02.11.2026')).toBe('2026-11-02')
    expect(parseDisplayDate('  02/11/2026 ')).toBe('2026-11-02')
  })

  it('reads eight digits without separators and a pasted ISO date', () => {
    expect(parseDisplayDate('02112026')).toBe('2026-11-02')
    expect(parseDisplayDate('2026-11-02')).toBe('2026-11-02')
  })

  it('refuses a year of two digits, half a date, and text', () => {
    expect(parseDisplayDate('02/11/26')).toBeNull()
    expect(parseDisplayDate('02/11/202')).toBeNull()
    expect(parseDisplayDate('02/11')).toBeNull()
    expect(parseDisplayDate('')).toBeNull()
    expect(parseDisplayDate('besok')).toBeNull()
    expect(parseDisplayDate('2026-1-5')).toBeNull()
    expect(parseDisplayDate('0211260')).toBeNull()
  })

  it('checks the calendar with numbers: leap years, 31 April, month 13', () => {
    expect(parseDisplayDate('29/02/2028')).toBe('2028-02-29')
    expect(parseDisplayDate('29/02/2027')).toBeNull()
    expect(parseDisplayDate('29/02/2100')).toBeNull() // 2100 is not a leap year
    expect(parseDisplayDate('29/02/2000')).toBe('2000-02-29')
    expect(parseDisplayDate('31/04/2026')).toBeNull()
    expect(parseDisplayDate('31/02/2026')).toBeNull()
    expect(parseDisplayDate('31/12/2026')).toBe('2026-12-31')
    expect(parseDisplayDate('00/11/2026')).toBeNull()
    expect(parseDisplayDate('15/13/2026')).toBeNull()
    expect(parseDisplayDate('2026-02-30')).toBeNull()
  })

  it('writes an ISO date as dd/mm/yyyy, and nothing for what is not a date', () => {
    expect(formatDisplayDate('2026-11-02')).toBe('02/11/2026')
    expect(formatDisplayDate('2028-02-29')).toBe('29/02/2028')
    expect(formatDisplayDate('')).toBe('')
    expect(formatDisplayDate(null)).toBe('')
    expect(formatDisplayDate(undefined)).toBe('')
    expect(formatDisplayDate('2026-13-45')).toBe('')
    expect(formatDisplayDate('soon')).toBe('')
  })

  it('writes the mask while typing: digits alone get their slashes, a text with separators stays', () => {
    expect(maskDateInput('0')).toBe('0')
    expect(maskDateInput('02')).toBe('02')
    expect(maskDateInput('021')).toBe('02/1')
    expect(maskDateInput('0211')).toBe('02/11')
    expect(maskDateInput('02112')).toBe('02/11/2')
    expect(maskDateInput('02112026')).toBe('02/11/2026')
    expect(maskDateInput('021120261234')).toBe('02/11/2026')
    expect(maskDateInput('02/11/2026')).toBe('02/11/2026')
    expect(maskDateInput('2/11/2026')).toBe('2/11/2026')
    expect(maskDateInput('2026-11-02')).toBe('2026-11-02')
    expect(maskDateInput('02a1b')).toBe('02/1')
    expect(maskDateInput('')).toBe('')
  })
})

describe('the shape of a typed date', () => {
  it('tells a whole date that does not exist from a text that is not a date yet', () => {
    for (const whole of ['31/02/2026', '2/11/2026', '02-11-2026', '02.11.2026', '02112026', '2026-02-30', ' 02/11/2026 ']) expect(looksLikeWholeDate(whole)).toBe(true)
    for (const part of ['', '02', '02/11', '02/11/20', '02/11/26', '0211202', 'besok', '2026-1-5']) expect(looksLikeWholeDate(part)).toBe(false)
    expect(parseDisplayDate('31/02/2026')).toBeNull() // whole, but not on the calendar
  })
})
