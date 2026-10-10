import { computed, ref } from 'vue'

/** What the person picked; "system" follows the operating system setting. */
export type ThemePreference = 'light' | 'dark' | 'system'
export type ResolvedTheme = 'light' | 'dark'

export const THEME_KEY = 'pms.theme'
export const THEME_PREFERENCES: readonly ThemePreference[] = ['light', 'dark', 'system']
const DARK_QUERY = '(prefers-color-scheme: dark)'

function readPreference(): ThemePreference {
  try {
    const v = localStorage.getItem(THEME_KEY)
    return v === 'light' || v === 'dark' ? v : 'system'
  } catch {
    return 'system'
  }
}

function systemTheme(): ResolvedTheme {
  return typeof window !== 'undefined' && typeof window.matchMedia === 'function' && window.matchMedia(DARK_QUERY).matches ? 'dark' : 'light'
}

const preference = ref<ThemePreference>(readPreference())
const system = ref<ResolvedTheme>(systemTheme())
const resolved = computed<ResolvedTheme>(() => (preference.value === 'system' ? system.value : preference.value))

/** `data-theme` is always "light" or "dark" (never absent): the CSS and the `dark:` variant only look at it. */
function apply(): void {
  document.documentElement.setAttribute('data-theme', resolved.value)
}

let listening = false

/**
 * Called once before the app mounts (index.html already set `data-theme` from the same storage key, so the first paint is right). It keeps
 * the attribute in step with the operating system while the preference is "system".
 */
export function initTheme(): void {
  preference.value = readPreference()
  system.value = systemTheme()
  apply()
  if (listening || typeof window.matchMedia !== 'function') return
  listening = true
  window.matchMedia(DARK_QUERY).addEventListener('change', (e) => {
    system.value = e.matches ? 'dark' : 'light'
    apply()
  })
}

export function useTheme() {
  function setPreference(value: ThemePreference): void {
    preference.value = value
    try {
      if (value === 'system') localStorage.removeItem(THEME_KEY)
      else localStorage.setItem(THEME_KEY, value)
    } catch {
      // Private window or blocked storage: the choice still applies until the page is closed.
    }
    apply()
  }
  return { preference, resolved, setPreference }
}
