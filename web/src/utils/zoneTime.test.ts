import { afterEach, describe, expect, it } from 'vitest'
import { setLocale } from '@/i18n'
import { zoneTime } from './zoneTime'

describe('zoneTime', () => {
  afterEach(() => setLocale('en'))
  const at = new Date('2026-10-10T03:42:00Z')

  it('is the wall clock of the zone, 24 hours, with the separator of the language', () => {
    setLocale('en')
    expect(zoneTime(at, 'Asia/Makassar')).toBe('11:42')
    setLocale('id')
    expect(zoneTime(at, 'Asia/Jakarta')).toBe('10.42')
    expect(zoneTime(new Date('2026-10-10T17:05:00Z'), 'Asia/Jakarta')).toBe('00.05') // midnight is 00, not 24
  })

  it('falls back to the browser zone for no zone or one it does not know', () => {
    expect(zoneTime(at)).toMatch(/^\d{2}:\d{2}$/)
    expect(zoneTime(at, 'Not/AZone')).toMatch(/^\d{2}:\d{2}$/)
  })
})
