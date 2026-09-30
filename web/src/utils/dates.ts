/**
 * Date helpers that never let the browser's time zone leak into hotel dates.
 *
 * - Business dates are "YYYY-MM-DD" strings from the API and stay strings.
 * - The property's local time arrives as RFC 3339 with the property's offset
 *   (e.g. "2026-10-01T02:30:00+07:00"). Parsing it into a JS Date would
 *   re-render it in the *browser's* zone, so we read the wall-clock parts directly.
 */

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

/** "2026-09-30" -> "30 Sep 2026". Invalid input is returned unchanged. */
export function formatBusinessDate(date: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date)
  if (!m) return date
  const month = MONTHS[Number(m[2]) - 1]
  return month ? `${Number(m[3])} ${month} ${m[1]}` : date
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

/** IANA zones known to this browser (falls back to a short list on old engines). */
export function timeZones(): string[] {
  const intl = Intl as unknown as { supportedValuesOf?: (key: string) => string[] }
  return intl.supportedValuesOf?.('timeZone') ?? ['Asia/Jakarta', 'Asia/Makassar', 'Asia/Jayapura', 'Asia/Singapore', 'UTC']
}
