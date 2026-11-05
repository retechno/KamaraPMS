import { describe, expect, it } from 'vitest'
import { creditAfterPayment } from './folioCredit'

describe('creditAfterPayment', () => {
  it('is the part of the payment above what is owed', () => {
    expect(creditAfterPayment('100000', '150000')).toBe(50000)
    expect(creditAfterPayment('100000', '100000')).toBe(0)
    expect(creditAfterPayment('100000', '60000')).toBe(0)
  })
  it('counts a folio that is already in credit, and ignores what is not an amount', () => {
    expect(creditAfterPayment('-20000', '30000')).toBe(50000)
    expect(creditAfterPayment('100000', '')).toBe(0)
    expect(creditAfterPayment('100000', 'abc')).toBe(0)
    expect(creditAfterPayment('100000', '-5')).toBe(0)
  })
})
