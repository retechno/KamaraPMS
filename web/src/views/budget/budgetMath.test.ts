import { afterEach, describe, expect, it } from 'vitest'
import { setLocale } from '@/i18n'
import { addYears, fromMilli, isAmount, monthLabel, monthStart, monthValue, sumAmounts, toMilli, yearLabel } from './budgetMath'

describe('budget figures', () => {
  afterEach(() => setLocale('en'))

  it('reads plain amounts and nothing else', () => {
    for (const ok of ['', '  ', '0', '100000', '-500', '12.5', '0.125', '-0.001', '9999999999999']) expect(isAmount(ok), ok).toBe(true)
    for (const bad of ['abc', '1e3', '1,000', '1.0000', '+5', '10000000000000000', '--1', '1 000', '.5']) expect(isAmount(bad), bad).toBe(false)
  })

  it('counts in thousandths, with no float in between', () => {
    expect(toMilli('')).toBe(0n)
    expect(toMilli('12.5')).toBe(12500n)
    expect(toMilli('-0.001')).toBe(-1n)
    expect(toMilli('x')).toBeNull()
    expect(fromMilli(12500n)).toBe('12.5')
    expect(fromMilli(-1n)).toBe('-0.001')
    expect(fromMilli(0n)).toBe('0')
    expect(fromMilli(-7000n)).toBe('-7')
  })

  it('adds a row exactly, as the server does', () => {
    expect(sumAmounts(['0.1', '0.2', '', '-0.3'])).toBe('0')
    expect(sumAmounts(Array(12).fill('83333.333'))).toBe('999999.996')
    expect(sumAmounts(['1', 'x'])).toBeNull()
  })

  it('names fiscal years and months', () => {
    expect(yearLabel('2026-01-01')).toBe('FY2026')
    expect(yearLabel('2025-10-01')).toBe('FY2026')
    expect(addYears('2026-07-01', 2)).toBe('2028-07-01')
    expect(monthStart('2026-09')).toBe('2026-09-01')
    expect(monthStart('')).toBe('')
    expect(monthValue('2026-09-30')).toBe('2026-09')
    expect(monthLabel('2026-09-01')).toBe('Sep')
    expect(monthLabel('2026-09-01', true)).toBe('Sep 2026')
    setLocale('id')
    expect(monthLabel('2026-05-01')).toBe('Mei')
  })
})
