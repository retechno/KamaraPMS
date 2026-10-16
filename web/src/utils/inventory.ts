import { formatMoney } from '@/utils/format'

/**
 * A quantity from the API ("28.200000") without the zeros the server keeps at the end, with the separators of the language ("28,2" in Indonesian).
 * Quantities and amounts are strings and stay strings (docs/architecture/35-inventory.md 4.1): nothing here goes through a float.
 */
export function formatQty(value: string | null | undefined): string {
  if (value === null || value === undefined || value === '') return ''
  const m = /^(-?)(\d+)(?:\.(\d+))?$/.exec(String(value).trim())
  if (!m) return String(value)
  const fraction = (m[3] ?? '').replace(/0+$/, '')
  return formatMoney(`${m[1]}${m[2]}${fraction ? '.' + fraction : ''}`)
}

/** Whether a text is a plain decimal with at most `places` decimals (a quantity or a cost typed by a person). */
export function isDecimal(value: string, places: number): boolean {
  const decimals = places > 0 ? `(\\.\\d{1,${places}})?` : ''
  return new RegExp(`^\\d+${decimals}$`).test(value.trim())
}

/** The product of a quantity and a cost, both plain decimals as typed, rounded half away from zero to `places` decimals; as text, without a float. */
export function multiply(qty: string, cost: string, places: number): string {
  const [qi = '0', qf = ''] = qty.trim().split('.')
  const [ci = '0', cf = ''] = cost.trim().split('.')
  const scale = qf.length + cf.length
  let n = BigInt(qi + qf) * BigInt(ci + cf) // an integer scaled by 10^scale
  if (scale > places) {
    const drop = BigInt(10) ** BigInt(scale - places)
    const rest = n % drop
    n = n / drop
    if (rest * BigInt(2) >= drop) n += BigInt(1)
  } else {
    n = n * BigInt(10) ** BigInt(places - scale)
  }
  const digits = n.toString().padStart(places + 1, '0')
  const whole = digits.slice(0, digits.length - places)
  const fraction = digits.slice(digits.length - places)
  return places > 0 ? `${whole}.${fraction}` : whole
}

/** Adds two plain non-negative decimals (money) with the same number of decimals. */
export function addMoney(a: string, b: string, places: number): string {
  const scale = (s: string): bigint => {
    const [i = '0', f = ''] = s.trim().split('.')
    return BigInt(i + f.padEnd(places, '0').slice(0, places))
  }
  const digits = (scale(a) + scale(b)).toString().padStart(places + 1, '0')
  const whole = digits.slice(0, digits.length - places)
  return places > 0 ? `${whole}.${digits.slice(digits.length - places)}` : whole
}
