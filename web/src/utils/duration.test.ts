import { beforeEach, describe, expect, it } from 'vitest'
import { setLocale } from '@/i18n'
import { formatDuration } from './duration'

describe('formatDuration', () => {
  beforeEach(() => setLocale('id'))

  it('writes minutes under an hour, hours and minutes under a day, days and hours after', () => {
    expect(formatDuration(0)).toBe('0 mnt')
    expect(formatDuration(45)).toBe('45 mnt')
    expect(formatDuration(59)).toBe('59 mnt')
    expect(formatDuration(60)).toBe('1 j 0 m')
    expect(formatDuration(557)).toBe('9 j 17 m')
    expect(formatDuration(1439)).toBe('23 j 59 m')
    expect(formatDuration(1440)).toBe('1 h 0 j')
    expect(formatDuration(1440 + 3 * 60 + 40)).toBe('1 h 3 j')
    expect(formatDuration(870)).toBe('14 j 30 m')
  })

  it('does not break on a value that is not a number or is below zero', () => {
    expect(formatDuration(-5)).toBe('0 mnt')
    expect(formatDuration(Number.NaN)).toBe('0 mnt')
    expect(formatDuration(12.9)).toBe('12 mnt')
  })

  it('has English units', async () => {
    await setLocale('en')
    expect(formatDuration(557)).toBe('9 h 17 m')
    expect(formatDuration(45)).toBe('45 min')
    expect(formatDuration(1500)).toBe('1 d 1 h')
  })
})
