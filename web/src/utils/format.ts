import type { App } from 'vue'
import { currentLocale, type Locale } from '@/i18n'
import { formatBusinessDate, wallClock } from './dates'

/** Group and decimal separators of a language. */
const SEPARATORS: Record<Locale, { group: string; decimal: string }> = {
  en: { group: ',', decimal: '.' },
  id: { group: '.', decimal: ',' },
}

/**
 * An amount from the API ("2442000", "-15000.5") with the separators of the language of the page ("2,442,000" or
 * "2.442.000"). Amounts are strings and stay strings: the digits are grouped as text, never through a float, and the
 * decimals are kept as the server sent them. Anything that is not a plain decimal is returned unchanged.
 */
export function formatMoney(value: string | number | null | undefined): string {
  if (value === null || value === undefined || value === '') return ''
  const text = String(value)
  const m = /^(-?)(\d+)(?:\.(\d+))?$/.exec(text.trim())
  if (!m) return text
  const sep = SEPARATORS[currentLocale()]
  const grouped = (m[2] ?? '').replace(/\B(?=(\d{3})+(?!\d))/g, sep.group)
  return `${m[1]}${grouped}${m[3] ? sep.decimal + m[3] : ''}`
}

/** A business date ("2026-09-30") in the language of the page; anything else is returned unchanged. */
export const formatDate = (value: string | null | undefined): string => (value ? formatBusinessDate(value) : '')

/** An RFC 3339 instant as its wall clock in the offset it carries ("30 Sep 2026 13:00"); anything else is returned unchanged. */
export function formatDateTime(value: string | null | undefined): string {
  if (!value) return ''
  const w = wallClock(value)
  return w ? `${formatBusinessDate(w.date)} ${w.time}` : value
}

/** `$money`, `$date` and `$dateTime` in every template. */
export const formatPlugin = {
  install(app: App): void {
    app.config.globalProperties.$money = formatMoney
    app.config.globalProperties.$date = formatDate
    app.config.globalProperties.$dateTime = formatDateTime
  },
}

declare module 'vue' {
  interface ComponentCustomProperties {
    $money: typeof formatMoney
    $date: typeof formatDate
    $dateTime: typeof formatDateTime
  }
}
