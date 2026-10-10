import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AccountView from './AccountView.vue'
import { initTheme } from '@/composables/useTheme'

describe('AccountView theme', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    window.matchMedia = vi.fn().mockReturnValue({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() }) as unknown as typeof window.matchMedia
    initTheme()
  })

  it('lets the person pick light, dark or system and applies it to <html>', async () => {
    const w = mount(AccountView)
    await flushPromises()
    expect(w.get('[data-testid=theme-system]').attributes('aria-checked')).toBe('true')
    await w.get('[data-testid=theme-dark]').trigger('click')
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark')
    expect(w.get('[data-testid=theme-dark]').attributes('aria-checked')).toBe('true')
    expect(localStorage.getItem('pms.theme')).toBe('dark')
    await w.get('[data-testid=theme-light]').trigger('click')
    expect(document.documentElement.getAttribute('data-theme')).toBe('light')
  })
})
