import { currentLocale } from '@/i18n'

/**
 * The time of day of an instant in a time zone (the property's), as "10.42" in Indonesian and "10:42" in English: two digits, 24 hours, no seconds. Without a zone, or with one
 * the browser does not know, it is the browser's own.
 */
export function zoneTime(at: Date, timeZone?: string | null): string {
  const options: Intl.DateTimeFormatOptions = { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }
  let parts: Intl.DateTimeFormatPart[]
  try {
    parts = new Intl.DateTimeFormat('en-GB', { ...options, timeZone: timeZone || undefined }).formatToParts(at)
  } catch {
    parts = new Intl.DateTimeFormat('en-GB', options).formatToParts(at)
  }
  const hour = parts.find((p) => p.type === 'hour')?.value ?? '00'
  const minute = parts.find((p) => p.type === 'minute')?.value ?? '00'
  return `${hour}${currentLocale() === 'id' ? '.' : ':'}${minute}`
}
