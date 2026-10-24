import { beforeEach, describe, expect, it } from 'vitest'
import { setLocale } from '@/i18n'
import { addMoney, formatQty, formatReportQty, isDecimal, multiply } from './inventory'

beforeEach(() => setLocale('en'))

describe('formatQty', () => {
  it('drops the zeros the server keeps and groups the digits', () => {
    expect(formatQty('28.200000')).toBe('28.2')
    expect(formatQty('1200.000000')).toBe('1,200')
    expect(formatQty('-4.000000')).toBe('-4')
    expect(formatQty('0.153846')).toBe('0.153846')
    expect(formatQty('')).toBe('')
  })

  it('follows the language', () => {
    setLocale('id')
    expect(formatQty('1200.500000')).toBe('1.200,5')
  })
})

describe('isDecimal', () => {
  it('accepts a plain decimal with at most the places given', () => {
    expect(isDecimal('10', 0)).toBe(true)
    expect(isDecimal('10.5', 0)).toBe(false)
    expect(isDecimal('10.50', 2)).toBe(true)
    expect(isDecimal('10.505', 2)).toBe(false)
    expect(isDecimal('1e3', 2)).toBe(false)
    expect(isDecimal('-1', 2)).toBe(false)
    expect(isDecimal('', 2)).toBe(false)
  })
})

describe('multiply', () => {
  it('works in text, without a float, rounding half away from zero', () => {
    expect(multiply('36', '49000', 0)).toBe('1764000')
    expect(multiply('0.5', '3', 0)).toBe('2') // 1.5 rounds up
    expect(multiply('2.5', '1', 0)).toBe('3')
    expect(multiply('3', '3.33', 2)).toBe('9.99')
    expect(multiply('1.333333', '3', 2)).toBe('4.00')
    expect(multiply('0.1', '0.2', 3)).toBe('0.020')
    expect(multiply('0', '5', 2)).toBe('0.00')
  })
})

describe('addMoney', () => {
  it('adds in the decimals of the currency', () => {
    expect(addMoney('0', '1764000', 0)).toBe('1764000')
    expect(addMoney('9.99', '0.01', 2)).toBe('10.00')
    expect(addMoney('0.000', '0.020', 3)).toBe('0.020')
  })
})

describe('formatReportQty', () => {
  beforeEach(() => setLocale('id'))
  it('shows a whole quantity without decimals', () => {
    expect(formatReportQty('12')).toBe('12')
    expect(formatReportQty('12.000000')).toBe('12')
    expect(formatReportQty('1200.000000')).toBe('1.200')
  })
  it('keeps up to three decimals and drops the zeros at the end', () => {
    expect(formatReportQty('12.216')).toBe('12,216')
    expect(formatReportQty('12.500000')).toBe('12,5')
    expect(formatReportQty('0.250')).toBe('0,25')
  })
  it('rounds six decimals to three, half away from zero', () => {
    expect(formatReportQty('1.153845')).toBe('1,154')
    expect(formatReportQty('12.216499')).toBe('12,216')
    expect(formatReportQty('0.9996')).toBe('1')
    expect(formatReportQty('99.9999')).toBe('100')
  })
  it('handles negatives', () => {
    expect(formatReportQty('-4.000000')).toBe('-4')
    expect(formatReportQty('-1.153845')).toBe('-1,154')
    expect(formatReportQty('-0.0004')).toBe('0')
  })
  it('never shows more than three decimals and leaves blanks alone', () => {
    expect(formatReportQty('0.153846', 6)).toBe('0,154')
    expect(formatReportQty('1.153845', 1)).toBe('1,2')
    expect(formatReportQty('')).toBe('')
    expect(formatReportQty(null)).toBe('')
  })
})
