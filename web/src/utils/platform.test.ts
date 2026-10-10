import { describe, expect, it } from 'vitest'
import { finderShortcut, isApplePlatform } from './platform'

describe('platform', () => {
  it('knows an Apple keyboard from the platform, or from the user agent when the platform is empty', () => {
    expect(isApplePlatform({ platform: 'MacIntel', userAgent: '' })).toBe(true)
    expect(isApplePlatform({ platform: 'iPhone', userAgent: '' })).toBe(true)
    expect(isApplePlatform({ platform: '', userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)' })).toBe(true)
    expect(isApplePlatform({ platform: 'Win32', userAgent: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64)' })).toBe(false)
    expect(isApplePlatform({ platform: 'Linux x86_64', userAgent: 'X11' })).toBe(false)
  })

  it('hints ⌘K on a Mac and Ctrl K on the others', () => {
    expect(finderShortcut({ platform: 'MacIntel', userAgent: '' })).toBe('⌘K')
    expect(finderShortcut({ platform: 'Win32', userAgent: '' })).toBe('Ctrl K')
  })

  it('uses the browser it runs in when none is given', () => {
    expect(['⌘K', 'Ctrl K']).toContain(finderShortcut())
  })
})
