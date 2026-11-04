import { afterEach, describe, expect, it } from 'vitest'
import { setLocale } from '@/i18n'
import { partyText } from './party'

describe('partyText', () => {
  afterEach(() => setLocale('en'))

  it('says the adults, and the children only when there are some', () => {
    setLocale('en')
    expect(partyText(1, 0)).toBe('1 adult')
    expect(partyText(2, 0)).toBe('2 adults')
    expect(partyText(2, 1)).toBe('2 adults · 1 child')
    expect(partyText(2, 3)).toBe('2 adults · 3 children')
  })

  it('says it in Indonesian', () => {
    setLocale('id')
    expect(partyText(2, 0)).toBe('2 dewasa')
    expect(partyText(2, 1)).toBe('2 dewasa · 1 anak')
  })
})
