import { describe, expect, it } from 'vitest'
import { createAttemptKey } from './attemptKey'

describe('createAttemptKey', () => {
  it('gives the same key for the same content until the server has answered', () => {
    const a = createAttemptKey()
    const first = a.keyFor(['100', 'CASH', ''])
    expect(a.keyFor(['100', 'CASH', ''])).toBe(first)
    a.answered()
    expect(a.keyFor(['100', 'CASH', ''])).not.toBe(first)
  })

  it('gives another key when the amount, the method or the reference changed', () => {
    const a = createAttemptKey()
    const first = a.keyFor(['100', 'CASH', ''])
    expect(a.keyFor(['120', 'CASH', ''])).not.toBe(first)
    const second = a.keyFor(['120', 'CASH', ''])
    expect(a.keyFor(['120', 'CARD', ''])).not.toBe(second)
    expect(a.keyFor(['120', 'CARD', 'ref'])).not.toBe(second)
  })
})
