import type { FlashLine } from '@/api/types'
import { t } from '@/i18n'
import { formatMoney, formatPercent } from '@/utils/format'

/** The rows of the daily flash whose name is not data: the server names them in English, the page by their key. */
const FIXED = new Set([
  'rooms_total', 'rooms_ooo', 'rooms_oos', 'rooms_house_use', 'rooms_available', 'rooms_sold', 'rooms_complimentary', 'rooms_vacant', 'walk_ins', 'guests_in_house',
  'occupancy_pct', 'occupancy_paid_pct', 'double_occupancy_pct', 'guests_per_room', 'arr', 'arr_incl_comp', 'revpar', 'alos',
  'reservations_made', 'reservations_cancelled', 'no_shows', 'early_checkouts', 'late_checkouts', 'arrivals', 'departures', 'departures_tomorrow',
  'total_net', 'service', 'tax', 'gtr', 'journal', 'source_hotel', 'source_pos', 'source_banquet', 'source_other', 'gtp', 'refunds_total', 'to_city_ledger', 'banquet_per_pax',
])

const key = (k: string) => `dayReports.flash.rows.${k}` as 'dayReports.flash.rows.gtr'

/** The name of a row in the language of the page. A charge code, an outlet, a room type or a cashier is data and stays as it is. */
export function rowLabel(line: FlashLine): string {
  if (FIXED.has(line.key)) return t(key(line.key))
  const [kind] = line.key.split(':')
  if (kind === 'covers' || kind === 'covers_missing' || kind === 'per_cover') {
    return t(`dayReports.flash.outlet.${kind}` as 'dayReports.flash.outlet.covers', { outlet: line.label.split(': ')[0] ?? '' })
  }
  return line.label
}

/** A value of a row as its kind says: money and counts grouped, a percent with its sign, a ratio with its decimals. */
export function flashValue(kind: FlashLine['kind'], v: string | null): string {
  if (v === null || v === '') return '–'
  return kind === 'PERCENT' ? formatPercent(v) : formatMoney(v)
}

/** A variance with its sign ("+141,7"); a percent row is a difference in points. */
export function flashVariance(line: FlashLine): string {
  if (line.variance === null) return '–'
  const sign = Number(line.variance) > 0 ? '+' : ''
  return `${sign}${formatMoney(line.variance)}${line.kind === 'PERCENT' ? ` ${t('dayReports.flash.points')}` : ''}`
}
