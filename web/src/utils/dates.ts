/**
 * Date helpers that never let the browser's time zone leak into hotel dates.
 *
 * - Business dates are "YYYY-MM-DD" strings from the API and stay strings.
 * - The property's local time arrives as RFC 3339 with the property's offset
 *   (e.g. "2026-10-01T02:30:00+07:00"). Parsing it into a JS Date would
 *   re-render it in the *browser's* zone, so we read the wall-clock parts directly.
 */

import { currentLocale, type Locale } from '@/i18n'

// Month names are kept in a table, not taken from Intl: the short forms of Intl differ between browsers ("Sep" or "Sept").
const MONTHS: Record<Locale, string[]> = {
  en: ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'],
  id: ['Jan', 'Feb', 'Mar', 'Apr', 'Mei', 'Jun', 'Jul', 'Agu', 'Sep', 'Okt', 'Nov', 'Des'],
}

/** "2026-09-30" -> "30 Sep 2026" (in the language of the page: "30 Agu 2026"). Invalid input is returned unchanged. */
export function formatBusinessDate(date: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date)
  if (!m) return date
  const month = MONTHS[currentLocale()][Number(m[2]) - 1]
  return month ? `${Number(m[3])} ${month} ${m[1]}` : date
}

/** "2026-10" -> "Okt 2026" (a month of a report; the key of the row stays ISO). Invalid input is returned unchanged. */
export function formatBusinessMonth(month: string): string {
  const m = /^(\d{4})-(\d{2})$/.exec(month)
  if (!m) return month
  const name = MONTHS[currentLocale()][Number(m[2]) - 1]
  return name ? `${name} ${m[1]}` : month
}

/** "2026-09-30" -> "30 Sep" (a dense list of the open year). Invalid input is returned unchanged. */
export function formatBusinessDateShort(date: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date)
  if (!m) return date
  const month = MONTHS[currentLocale()][Number(m[2]) - 1]
  return month ? `${Number(m[3])} ${month}` : date
}

/** Wall-clock parts of an RFC 3339 timestamp, in the offset it carries. */
export function wallClock(rfc3339: string): { date: string; time: string; offset: string } | null {
  const m = /^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2})(?::\d{2}(?:\.\d+)?)?(Z|[+-]\d{2}:\d{2})$/.exec(rfc3339)
  if (!m || !m[1] || !m[2] || !m[3]) return null
  return { date: m[1], time: m[2], offset: m[3] === 'Z' ? '+00:00' : m[3] }
}

/** Today's date ("YYYY-MM-DD") in an IANA time zone, e.g. for a property's opening date. */
export function todayIn(timeZone: string, now: Date = new Date()): string | null {
  try {
    // en-CA formats as YYYY-MM-DD.
    return new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit' }).format(now)
  } catch {
    return null // unknown zone
  }
}

/** Adds days to a "YYYY-MM-DD" date without involving any time zone. */
export function addDays(date: string, days: number): string {
  const [y, m, d] = date.split('-').map(Number)
  const t = new Date(Date.UTC(y ?? 0, (m ?? 1) - 1, (d ?? 1) + days))
  return t.toISOString().slice(0, 10)
}

function daysInMonth(year: number, month: number): number {
  if (month === 2) return year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0) ? 29 : 28
  return [4, 6, 9, 11].includes(month) ? 30 : 31
}

function isoOf(year: number, month: number, day: number): string | null {
  if (year < 1 || month < 1 || month > 12 || day < 1 || day > daysInMonth(year, month)) return null
  return `${String(year).padStart(4, '0')}-${String(month).padStart(2, '0')}-${String(day).padStart(2, '0')}`
}

/**
 * What a person types in a date field -> "YYYY-MM-DD", or null when it is not a real date. It accepts d/m/yyyy with "/", "-" or "." between the parts,
 * eight digits without separators (ddmmyyyy) and a pasted ISO date. A year of two digits is refused (it could mean two centuries). The calendar is checked
 * with arithmetic on the numbers (leap years, 31 April), never with `new Date`, which would move a date by the time zone.
 */
export function parseDisplayDate(text: string): string | null {
  const s = text.trim()
  let m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s)
  if (m) return isoOf(Number(m[1]), Number(m[2]), Number(m[3]))
  m = /^(\d{1,2})[/.-](\d{1,2})[/.-](\d{4})$/.exec(s) ?? /^(\d{2})(\d{2})(\d{4})$/.exec(s)
  if (m) return isoOf(Number(m[3]), Number(m[2]), Number(m[1]))
  return null
}

/** Whether a text has the shape of a whole date (d/m/yyyy, eight digits, an ISO date) whether or not the date exists: "31/02/2026" does, "02/11" does not. */
export function looksLikeWholeDate(text: string): boolean {
  const s = text.trim()
  return /^\d{4}-\d{2}-\d{2}$/.test(s) || /^\d{1,2}[/.-]\d{1,2}[/.-]\d{4}$/.test(s) || /^\d{8}$/.test(s)
}

/** "2026-11-02" -> "02/11/2026" in every language (a hotel in Indonesia reads one order, and so does its English screen); anything that is not an ISO date gives "". */
export function formatDisplayDate(iso: string | null | undefined): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso ?? '')
  if (!m || !isoOf(Number(m[1]), Number(m[2]), Number(m[3]))) return ''
  return `${m[3]}/${m[2]}/${m[1]}`
}

/**
 * The mask of the date field while a person types: digits alone become dd/mm/yyyy ("02112026" -> "02/11/2026", at most eight digits); a text that already has
 * separators (typed by hand or a pasted ISO date) is left as it is, cut at ten characters. Characters that cannot be part of a date are dropped.
 */
export function maskDateInput(raw: string): string {
  const clean = raw.replace(/[^\d/.-]/g, '')
  // Slashes that sit where the mask itself puts them ("02/1" while typing digit by digit) do not make the text hand-written.
  const maskSlashes = !/[.-]/.test(clean) && [...clean].every((c, i) => c !== '/' || i === 2 || i === 5)
  if (/^\d*$/.test(clean) || maskSlashes) {
    const d = clean.replace(/\//g, '').slice(0, 8)
    if (d.length <= 2) return d
    if (d.length <= 4) return `${d.slice(0, 2)}/${d.slice(2)}`
    return `${d.slice(0, 2)}/${d.slice(2, 4)}/${d.slice(4)}`
  }
  return clean.slice(0, 10)
}

/** IANA zones known to this browser (falls back to a short list on old engines). */
export function timeZones(): string[] {
  const intl = Intl as unknown as { supportedValuesOf?: (key: string) => string[] }
  return intl.supportedValuesOf?.('timeZone') ?? ['Asia/Jakarta', 'Asia/Makassar', 'Asia/Jayapura', 'Asia/Singapore', 'UTC']
}
