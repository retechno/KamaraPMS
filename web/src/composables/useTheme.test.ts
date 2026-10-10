import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

type Listener = (e: { matches: boolean }) => void

/** A matchMedia whose answer the test controls, and whose change listener it can fire. */
function mockSystem(dark: boolean) {
  const state = { dark, listeners: [] as Listener[] }
  window.matchMedia = vi.fn().mockImplementation(() => ({
    get matches() {
      return state.dark
    },
    addEventListener: (_: string, l: Listener) => state.listeners.push(l),
    removeEventListener: vi.fn(),
  })) as unknown as typeof window.matchMedia
  return {
    flip(next: boolean) {
      state.dark = next
      state.listeners.forEach((l) => l({ matches: next }))
    },
  }
}

const load = async () => {
  vi.resetModules()
  return import('./useTheme')
}
const attr = () => document.documentElement.getAttribute('data-theme')

describe('useTheme', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.removeAttribute('data-theme')
  })
  afterEach(() => vi.restoreAllMocks())

  it('always sets data-theme: "system" resolves from the OS setting', async () => {
    mockSystem(true)
    const { initTheme, useTheme } = await load()
    initTheme()
    expect(attr()).toBe('dark')
    expect(useTheme().preference.value).toBe('system')
  })

  it('follows the OS while the preference is "system", and stops once the person chooses', async () => {
    const os = mockSystem(false)
    const { initTheme, useTheme } = await load()
    initTheme()
    expect(attr()).toBe('light')
    os.flip(true)
    expect(attr()).toBe('dark')
    useTheme().setPreference('light')
    os.flip(true)
    expect(attr()).toBe('light')
  })

  it('remembers a choice, and "system" clears it', async () => {
    mockSystem(false)
    const { initTheme, useTheme } = await load()
    initTheme()
    useTheme().setPreference('dark')
    expect(attr()).toBe('dark')
    expect(localStorage.getItem('pms.theme')).toBe('dark')
    useTheme().setPreference('system')
    expect(localStorage.getItem('pms.theme')).toBeNull()
    expect(attr()).toBe('light')
  })

  it('reads the stored choice at start', async () => {
    mockSystem(true)
    localStorage.setItem('pms.theme', 'light')
    const { initTheme, useTheme } = await load()
    initTheme()
    expect(attr()).toBe('light')
    expect(useTheme().preference.value).toBe('light')
  })

  it('still works when storage throws', async () => {
    mockSystem(false)
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    const { initTheme, useTheme } = await load()
    initTheme()
    expect(attr()).toBe('light')
    useTheme().setPreference('dark')
    expect(attr()).toBe('dark')
  })
})
