import { createI18n } from 'vue-i18n'
import { en, type Messages } from './locales/en'

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

/**
 * English is part of the application (it is the source of the keys and the fallback); the other languages are separate
 * files that are downloaded only when someone chooses them (`loadLocale`).
 */
export const i18n = createI18n<[Messages], Locale, false>({
  legacy: false,
  locale: 'en',
  fallbackLocale: 'en',
  messages: { en } as Record<Locale, Messages>,
})

const loaders: Record<Locale, () => Promise<Messages>> = {
  en: async () => en,
  id: async () => (await import('./locales/id')).id,
}

/** Downloads the messages of a language, once. */
export async function loadLocale(locale: Locale): Promise<void> {
  if (i18n.global.availableLocales.includes(locale)) return
  i18n.global.setLocaleMessage(locale, await loaders[locale]())
}

/** Starts the application in the language chosen last time: its messages are there before the first page is drawn. */
export async function initLocale(): Promise<void> {
  const locale = storedLocale()
  await loadLocale(locale)
  applyLocale(locale)
}

/**
 * Translates a key outside a component (scripts, stores, tests). Components can also use `$t` in a template or
 * `useI18n()`: they read the same instance.
 */
export const t = i18n.global.t

/** Whether a key has a message in the current language or the fallback (to show a raw value when it has none). */
export const te = (key: string): boolean => i18n.global.te(key) || i18n.global.te(key, 'en')

export function currentLocale(): Locale {
  return i18n.global.locale.value
}

function applyLocale(locale: Locale): void {
  i18n.global.locale.value = locale
  if (typeof document !== 'undefined') document.documentElement.lang = locale
}

/**
 * Switches the language, remembers it on this browser and tells the document (screen readers, hyphenation). When the
 * messages are not downloaded yet it waits for them; the page keeps its language until they are there.
 */
export function setLocale(locale: Locale): Promise<void> {
  try {
    localStorage.setItem(STORAGE_KEY, locale)
  } catch {
    // private window or blocked storage: the choice lasts until the page is closed
  }
  if (i18n.global.availableLocales.includes(locale)) {
    applyLocale(locale)
    return Promise.resolve()
  }
  return loadLocale(locale).then(() => applyLocale(locale))
}
