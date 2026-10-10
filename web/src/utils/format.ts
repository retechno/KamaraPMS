import type { App } from 'vue'
import { currentLocale, t, type Locale } from '@/i18n'
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

/** The business dates inside a sentence written by the server ("Day close 2026-09-02") in the language of the page ("Day close 2 Sep 2026"). */
export const localizeDates = (text: string | null | undefined): string => (text ?? '').replace(/\b\d{4}-\d{2}-\d{2}\b/g, (d) => formatBusinessDate(d))

let displayZone: string | undefined

/** The time zone instants are shown in: the open property's (set when its clock is loaded). Without one the browser's zone is used. */
export function setDisplayTimeZone(zone: string | null | undefined): void {
  displayZone = zone || undefined
}

/**
 * An RFC 3339 instant ("2026-09-30T06:05:00Z") as the wall clock of the property, without seconds ("30 Sep 2026 13:05"): never UTC unless the
 * property is. Before the property's clock is loaded the browser's zone stands in. Anything that is not an instant is returned unchanged.
 */
export function formatDateTime(value: string | null | undefined): string {
  if (!value) return ''
  if (!wallClock(value)) return value
  const at = new Date(value)
  if (Number.isNaN(at.getTime())) return value
  let parts: Intl.DateTimeFormatPart[]
  try {
    parts = new Intl.DateTimeFormat('en-GB', { timeZone: displayZone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).formatToParts(at)
  } catch {
    return value // an unknown zone name
  }
  const p = (type: string) => parts.find((x) => x.type === type)?.value ?? ''
  return `${formatBusinessDate(`${p('year')}-${p('month')}-${p('day')}`)} ${p('hour')}:${p('minute')}`
}

/** A date with its weekday ("Sat, 10 Oct 2026" / "Sab, 10 Okt 2026"), as the helper line under a date input. Anything else gives ''. */
export function formatDateWeekday(value: string | null | undefined): string {
  if (!value || !/^\d{4}-\d{2}-\d{2}$/.test(value)) return ''
  const day = new Date(`${value}T00:00:00Z`)
  if (Number.isNaN(day.getTime())) return ''
  const weekday = new Intl.DateTimeFormat(currentLocale() === 'id' ? 'id-ID' : 'en-GB', { weekday: 'short', timeZone: 'UTC' }).format(day).replace('.', '')
  return `${weekday}, ${formatBusinessDate(value)}`
}

/** A percentage from the API ("67.35") with the decimal separator of the language and a % sign ("67,35%"); anything that is not a number is returned unchanged. */
export function formatPercent(value: string | number | null | undefined): string {
  if (value === null || value === undefined || value === '') return ''
  const text = String(value).trim()
  const m = /^(-?)(\d+)(?:\.(\d+))?$/.exec(text)
  if (!m) return text
  return `${m[1]}${m[2]}${m[3] ? SEPARATORS[currentLocale()].decimal + m[3] : ''}%`
}

/**
 * A balance where a negative one is a credit: the amount without its sign and the word ("1.200.000 kredit"), so a guest's credit is not
 * mistaken for a small debt. A positive or zero amount is formatted as `formatMoney` does.
 */
export function formatBalance(value: string | number | null | undefined): string {
  const text = value === null || value === undefined ? '' : String(value).trim()
  return text.startsWith('-') && /^-\d/.test(text) ? `${formatMoney(text.slice(1))} ${t('common.credit')}` : formatMoney(text)
}

// The units of the short form: the language of the page decides the words (Indonesian counts in thousand, million, billion = miliar).
const COMPACT_UNITS: Record<Locale, { exp: number; unit: string }[]> = {
  id: [{ exp: 12, unit: 'T' }, { exp: 9, unit: 'M' }, { exp: 6, unit: 'jt' }, { exp: 3, unit: 'rb' }],
  en: [{ exp: 12, unit: 'T' }, { exp: 9, unit: 'B' }, { exp: 6, unit: 'M' }, { exp: 3, unit: 'K' }],
}

/**
 * A short amount for a KPI card: "Rp 975 rb", "Rp 1,25 jt", "Rp 1,2 M" (miliar); in English "IDR 975K", "IDR 1.25M", "IDR 1.2B". The decimals shrink as the
 * number grows (two below 10, one below 100, none above) and a trailing zero is dropped. Below a thousand the amount is shown whole. The rupiah gets
 * "Rp" in Indonesian; every other currency its code and the browser's own compact notation. Tables keep the full amount (`formatMoney`).
 */
export function formatMoneyCompact(value: string | number | null | undefined, currency: string): string {
  if (value === null || value === undefined || value === '') return ''
  const text = String(value).trim()
  const m = /^(-?)(\d+)(?:\.\d+)?$/.exec(text)
  if (!m) return text
  const locale = currentLocale()
  const sign = m[1] ?? ''
  const whole = BigInt(m[2] ?? '0')
  const isIdr = currency === 'IDR'
  const prefix = isIdr && locale === 'id' ? 'Rp ' : `${currency} `
  if (!isIdr) {
    const compact = new Intl.NumberFormat(locale === 'id' ? 'id-ID' : 'en-US', { notation: 'compact', maximumFractionDigits: 2 }).format(Number(text))
    return `${prefix}${compact}`
  }
  const sep = SEPARATORS[locale].decimal
  for (const { exp, unit } of COMPACT_UNITS[locale]) {
    const div = 10n ** BigInt(exp)
    if (whole < div) continue
    const hundredths = (whole * 100n + div / 2n) / div // the amount in the unit, times 100, rounded half up
    const digits = hundredths < 1000n ? 2 : hundredths < 10000n ? 1 : 0
    const step = 10n ** BigInt(2 - digits)
    const rounded = ((hundredths + step / 2n) / step) * step
    const frac = (rounded % 100n).toString().padStart(2, '0').slice(0, digits).replace(/0+$/, '')
    return `${sign}${prefix}${rounded / 100n}${frac ? sep + frac : ''}${locale === 'en' ? '' : ' '}${unit}`
  }
  return `${sign}${prefix}${formatMoney(m[2])}`
}

/** `$money`, `$date` and `$dateTime` in every template. */
export const formatPlugin = {
  install(app: App): void {
    app.config.globalProperties.$money = formatMoney
    app.config.globalProperties.$date = formatDate
    app.config.globalProperties.$dateTime = formatDateTime
    app.config.globalProperties.$percent = formatPercent
    app.config.globalProperties.$balance = formatBalance
    app.config.globalProperties.$moneyShort = formatMoneyCompact
    app.config.globalProperties.$weekday = formatDateWeekday
  },
}

declare module 'vue' {
  interface ComponentCustomProperties {
    $money: typeof formatMoney
    $date: typeof formatDate
    $dateTime: typeof formatDateTime
    $percent: typeof formatPercent
    $balance: typeof formatBalance
    $moneyShort: typeof formatMoneyCompact
    $weekday: typeof formatDateWeekday
  }
}
