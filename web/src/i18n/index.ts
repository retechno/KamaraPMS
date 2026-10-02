import { createI18n } from 'vue-i18n'
import { en, type Messages } from './locales/en'
import { id } from './locales/id'

export const LOCALES = ['en', 'id'] as const
export type Locale = (typeof LOCALES)[number]

const STORAGE_KEY = 'pms.locale'

const isLocale = (v: unknown): v is Locale => LOCALES.includes(v as Locale)

/** The language the person chose last time on this browser; English when none (or when storage is unavailable). */
function storedLocale(): Locale {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    return isLocale(v) ? v : 'en'
  } catch {
    return 'en'
  }
}

export const i18n = createI18n<[Messages], Locale, false>({
  legacy: false,
  locale: storedLocale(),
  fallbackLocale: 'en',
  messages: { en, id },
})

/**
 * Translates a key outside a component (scripts, stores, tests). Components can also use `$t` in a template or
 * `useI18n()`: they read the same instance.
 */
export const t = i18n.global.t

export function currentLocale(): Locale {
  return i18n.global.locale.value
}

/** Switches the language, remembers it on this browser and tells the document (screen readers, hyphenation). */
export function setLocale(locale: Locale): void {
  i18n.global.locale.value = locale
  try {
    localStorage.setItem(STORAGE_KEY, locale)
  } catch {
    // private window or blocked storage: the choice lasts until the page is closed
  }
  if (typeof document !== 'undefined') document.documentElement.lang = locale
}
