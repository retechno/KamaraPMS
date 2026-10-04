import { formatBusinessDate } from '@/utils/dates'

/**
 * The figures of a budget are decimal strings. The totals of the grid are added up as whole thousandths (BigInt), never as
 * floats, so a row of twelve amounts adds up exactly as the server will add it.
 */
const AMOUNT = /^-?\d{1,13}(\.\d{1,3})?$/

/** An empty cell is zero; anything else must be a plain amount of up to 13 digits and three decimals. */
export function isAmount(s: string): boolean {
  const v = s.trim()
  return v === '' || AMOUNT.test(v)
}

/** An amount as thousandths; empty is zero; `null` when it is not an amount. */
export function toMilli(s: string): bigint | null {
  const v = s.trim()
  if (v === '') return 0n
  if (!AMOUNT.test(v)) return null
  const negative = v.startsWith('-')
  const [whole = '0', frac = ''] = v.replace('-', '').split('.')
  const milli = BigInt(whole) * 1000n + BigInt(frac.padEnd(3, '0'))
  return negative ? -milli : milli
}

/** Thousandths as an amount, without trailing zeros ("1234.5", "-7", "0"). */
export function fromMilli(n: bigint): string {
  const abs = n < 0n ? -n : n
  const whole = (abs / 1000n).toString()
  const frac = (abs % 1000n).toString().padStart(3, '0').replace(/0+$/, '')
  return `${n < 0n ? '-' : ''}${whole}${frac ? `.${frac}` : ''}`
}

/** The sum of amounts; `null` when one of them is not an amount. */
export function sumAmounts(list: string[]): string | null {
  let sum = 0n
  for (const s of list) {
    const m = toMilli(s)
    if (m === null) return null
    sum += m
  }
  return fromMilli(sum)
}

/** The short name of the month of a date ("2026-09-01" -> "Sep"; "Mei" in Indonesian), with the year when asked ("Sep 2026"). */
export function monthLabel(date: string, withYear = false): string {
  const formatted = formatBusinessDate(date) // "1 Sep 2026"
  const m = /^\d+ (\S+) (\d{4})$/.exec(formatted)
  if (!m) return date
  return withYear ? `${m[1]} ${m[2]}` : (m[1] ?? date)
}

/** The first day of the fiscal year `n` years after the one that starts on `start` ("2026-01-01", 1 -> "2027-01-01"). */
export function addYears(start: string, n: number): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(start)
  return m ? `${Number(m[1]) + n}-${m[2]}-${m[3]}` : start
}

/** The name of a fiscal year: after the year it ends in ("FY2026"). */
export function yearLabel(start: string): string {
  const m = /^(\d{4})-(\d{2})-/.exec(start)
  if (!m) return start
  return `FY${Number(m[1]) + (m[2] === '01' ? 0 : 1)}`
}

/** A `<input type="month">` value ("2026-09") as the first day of that month; empty stays empty. */
export function monthStart(value: string): string {
  return /^\d{4}-\d{2}$/.test(value) ? `${value}-01` : ''
}

/** The month input value of a date ("2026-09-30" -> "2026-09"). */
export function monthValue(date: string): string {
  return /^\d{4}-\d{2}-\d{2}$/.test(date) ? date.slice(0, 7) : ''
}
