import { afterEach, describe, expect, it } from 'vitest'
import { setLocale } from '@/i18n'
import { formatBalance, formatDateTime, formatDateWeekday, formatMoney, formatMoneyCompact, formatPercent, localizeDates, setDisplayTimeZone } from './format'

describe('formatMoney', () => {
  afterEach(() => setLocale('en'))

  it('groups the digits with the separators of the language, as text', () => {
    expect(formatMoney('2442000')).toBe('2,442,000')
    expect(formatMoney('-15000.5')).toBe('-15,000.5')
    expect(formatMoney('999')).toBe('999')
    expect(formatMoney('12345678901234567890.123')).toBe('12,345,678,901,234,567,890.123')
    setLocale('id')
    expect(formatMoney('2442000')).toBe('2.442.000')
    expect(formatMoney('-15000.5')).toBe('-15.000,5')
  })

  it('leaves what is not an amount alone', () => {
    expect(formatMoney('')).toBe('')
    expect(formatMoney(null)).toBe('')
    expect(formatMoney('n/a')).toBe('n/a')
  })
})

describe('formatDateTime', () => {
  afterEach(() => setDisplayTimeZone(null))

  it('shows an instant on the wall clock of the property, without seconds, never in UTC unless the property is', () => {
    setDisplayTimeZone('Asia/Jakarta')
    expect(formatDateTime('2026-09-30T06:05:00Z')).toBe('30 Sep 2026 13:05')
    expect(formatDateTime('2026-09-30T13:05:00+07:00')).toBe('30 Sep 2026 13:05')
    expect(formatDateTime('2026-09-30T23:30:00.123Z')).toBe('1 Oct 2026 06:30') // past midnight there: the next day
    setDisplayTimeZone('Asia/Jayapura')
    expect(formatDateTime('2026-09-30T06:05:00Z')).toBe('30 Sep 2026 15:05')
    setDisplayTimeZone('UTC')
    expect(formatDateTime('2026-09-30T06:05:00Z')).toBe('30 Sep 2026 06:05')
  })

  it('uses the zone of the browser until the clock of a property is loaded, and leaves what is not an instant alone', () => {
    const browser = new Intl.DateTimeFormat('en-GB', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).formatToParts(new Date('2026-09-30T06:05:00Z'))
    const hh = browser.find((p) => p.type === 'hour')?.value
    expect(formatDateTime('2026-09-30T06:05:00Z')).toContain(` ${hh}:05`)
    expect(formatDateTime('soon')).toBe('soon')
    expect(formatDateTime('')).toBe('')
    setDisplayTimeZone('Not/AZone')
    expect(formatDateTime('2026-09-30T06:05:00Z')).toBe('2026-09-30T06:05:00Z')
  })
})

describe('formatPercent', () => {
  afterEach(() => setLocale('en'))

  it('uses the decimal separator of the language and keeps the digits the server sent', () => {
    expect(formatPercent('67.35')).toBe('67.35%')
    expect(formatPercent('0.00')).toBe('0.00%')
    setLocale('id')
    expect(formatPercent('67.35')).toBe('67,35%')
    expect(formatPercent('100')).toBe('100%')
    expect(formatPercent('n/a')).toBe('n/a')
    expect(formatPercent(null)).toBe('')
  })
})

describe('formatMoneyCompact', () => {
  afterEach(() => setLocale('en'))

  it('abbreviates a rupiah amount in Indonesian: rb, jt, M (miliar), T', () => {
    setLocale('id')
    expect(formatMoneyCompact('975000', 'IDR')).toBe('Rp 975 rb')
    expect(formatMoneyCompact('1250000', 'IDR')).toBe('Rp 1,25 jt')
    expect(formatMoneyCompact('12500000', 'IDR')).toBe('Rp 12,5 jt')
    expect(formatMoneyCompact('125000000', 'IDR')).toBe('Rp 125 jt')
    expect(formatMoneyCompact('1200000000', 'IDR')).toBe('Rp 1,2 M')
    expect(formatMoneyCompact('2500000000000', 'IDR')).toBe('Rp 2,5 T')
    expect(formatMoneyCompact('-1250000', 'IDR')).toBe('-Rp 1,25 jt')
  })

  it('abbreviates it in English: K, M, B', () => {
    expect(formatMoneyCompact('975000', 'IDR')).toBe('IDR 975K')
    expect(formatMoneyCompact('1250000', 'IDR')).toBe('IDR 1.25M')
    expect(formatMoneyCompact('1200000000', 'IDR')).toBe('IDR 1.2B')
  })

  it('shows a small amount whole, and handles zero and what is not an amount', () => {
    setLocale('id')
    expect(formatMoneyCompact('0', 'IDR')).toBe('Rp 0')
    expect(formatMoneyCompact('999', 'IDR')).toBe('Rp 999')
    expect(formatMoneyCompact('', 'IDR')).toBe('')
    expect(formatMoneyCompact('n/a', 'IDR')).toBe('n/a')
  })

  it('gives another currency its code and the compact notation of the browser', () => {
    expect(formatMoneyCompact('1250000', 'USD')).toBe('USD 1.25M')
    expect(formatMoneyCompact('975000', 'USD')).toBe('USD 975K')
  })
})

describe('formatBalance and formatDateWeekday', () => {
  afterEach(() => setLocale('en'))

  it('says "credit" for a negative balance instead of a bare minus sign', () => {
    expect(formatBalance('1200000')).toBe('1,200,000')
    expect(formatBalance('-1200000')).toBe('1,200,000 credit')
    setLocale('id')
    expect(formatBalance('-1200000')).toBe('1.200.000 kredit')
    expect(formatBalance('0')).toBe('0')
  })

  it('writes a date with its weekday for the line under a date input', () => {
    expect(formatDateWeekday('2026-10-10')).toBe('Sat, 10 Oct 2026')
    setLocale('id')
    expect(formatDateWeekday('2026-10-10')).toBe('Sab, 10 Okt 2026')
    expect(formatDateWeekday('')).toBe('')
    expect(formatDateWeekday('2026-13-45')).toBe('')
    expect(formatDateWeekday('soon')).toBe('')
  })
})

describe('localizeDates', () => {
  it('writes the business dates inside a sentence of the server in the language of the page', () => {
    expect(localizeDates('Day close 2026-09-02')).toBe('Day close 2 Sep 2026')
    expect(localizeDates('from 2026-09-01 to 2026-09-03')).toBe('from 1 Sep 2026 to 3 Sep 2026')
    expect(localizeDates('no date here 12345')).toBe('no date here 12345')
    expect(localizeDates(null)).toBe('')
  })
})
