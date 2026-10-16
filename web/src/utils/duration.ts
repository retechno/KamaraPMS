import { t } from '@/i18n'

/**
 * How long something has lasted, written for a small place: under an hour in minutes ("45 mnt"), under a day in hours and minutes ("9 j 17 m"), and from a day
 * on in days and hours ("1 h 3 j"). The units come from the language (id: j jam, m menit, h hari; en: h, m, d).
 */
export function formatDuration(totalMinutes: number): string {
  const m = Math.max(0, Math.floor(Number.isFinite(totalMinutes) ? totalMinutes : 0))
  if (m < 60) return t('duration.minutes', { n: m })
  if (m < 1440) return t('duration.hoursMinutes', { h: Math.floor(m / 60), m: m % 60 })
  return t('duration.daysHours', { d: Math.floor(m / 1440), h: Math.floor((m % 1440) / 60) })
}
