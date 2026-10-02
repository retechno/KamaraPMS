import { afterEach, describe, expect, it } from 'vitest'
import { setLocale } from '@/i18n'
import { formatDateTime, formatMoney } from './format'

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
  it('shows the wall clock of the offset it carries', () => {
    expect(formatDateTime('2026-09-30T13:05:00+07:00')).toBe('30 Sep 2026 13:05')
    expect(formatDateTime('2026-09-30T13:00:00Z')).toBe('30 Sep 2026 13:00')
    expect(formatDateTime('soon')).toBe('soon')
  })
})
