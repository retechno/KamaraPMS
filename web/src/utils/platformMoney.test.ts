import { beforeEach, describe, expect, it } from 'vitest'
import { setLocale } from '@/i18n'
import { platformMoney } from './platformMoney'

describe('platformMoney', () => {
  beforeEach(async () => {
    await setLocale('id')
  })

  it('writes the code of the currency and the grouped amount, without the zeros of numeric(18,3)', () => {
    expect(platformMoney('IDR', '1500000.000')).toBe('IDR 1.500.000')
    expect(platformMoney('IDR', '0.000')).toBe('IDR 0')
    expect(platformMoney('IDR', '1500000')).toBe('IDR 1.500.000')
  })

  it('keeps real decimals and the amount of another currency', () => {
    expect(platformMoney('USD', '99.500')).toBe('USD 99,5')
    expect(platformMoney('USD', '1200.250')).toBe('USD 1.200,25')
  })

  it('writes nothing for no amount', () => {
    expect(platformMoney('IDR', null)).toBe('')
  })
})
