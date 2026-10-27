/**
 * The cost of a journal line divided into units (docs/architecture/37-fixed-assets.md 4.2.3): every unit but the last takes the share rounded down, the last takes what is left. Done in whole units of
 * the smallest money step with BigInt, never with a float; the server does the same sum and has the last word.
 */
export function toMinor(value: string, decimals: number): bigint {
  const m = /^(-?)(\d+)(?:\.(\d+))?$/.exec(value.trim())
  if (!m) return 0n
  const frac = (m[3] ?? '').padEnd(decimals, '0').slice(0, decimals)
  const n = BigInt(`${m[2]}${frac}`)
  return m[1] === '-' ? -n : n
}

export function fromMinor(minor: bigint, decimals: number): string {
  const neg = minor < 0n
  const digits = (neg ? -minor : minor).toString().padStart(decimals + 1, '0')
  const whole = digits.slice(0, digits.length - decimals)
  const frac = decimals > 0 ? `.${digits.slice(digits.length - decimals)}` : ''
  return `${neg ? '-' : ''}${whole}${frac}`
}

/** The cost of each unit: the share rounded down for all but the last, the rest for the last (the server does the same with RoundFloor). */
export function splitCost(cost: string, units: number, decimals: number): string[] {
  const n = Math.max(1, Math.floor(units))
  const total = toMinor(cost, decimals)
  const big = BigInt(n)
  const share = total / big
  const out: bigint[] = Array.from({ length: n - 1 }, () => share)
  out.push(total - share * BigInt(n - 1))
  return out.map((v) => fromMinor(v, decimals))
}

/** The straight-line amount of a month for a unit: (cost - residual) / life, rounded; the residual is a percentage of the cost with up to three decimals. */
export function monthlyStraightLine(cost: string, residualPct: string, lifeMonths: number, decimals: number): string {
  if (lifeMonths <= 0) return fromMinor(0n, decimals)
  const c = toMinor(cost, decimals)
  const pct = toMinor(residualPct || '0', 3)
  const residual = (c * pct + 50000n) / 100000n
  const life = BigInt(lifeMonths)
  return fromMinor(((c - residual) * 2n + life) / (2n * life), decimals)
}
