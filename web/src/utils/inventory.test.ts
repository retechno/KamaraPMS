import { beforeEach, describe, expect, it } from 'vitest'
import { setLocale } from '@/i18n'
import { addMoney, formatQty, isDecimal, multiply } from './inventory'

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
