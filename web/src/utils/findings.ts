import { t, te } from '@/i18n'
import { formatMoney } from '@/utils/format'
import { formatQty } from '@/utils/inventory'

/**
 * A finding of the night audit or of a setup check as the server sends it: a code, the values its sentence needs (params) and, for logs and for a client that does not know the code, the English sentence.
 * The screen writes the sentence in the language of the person: the key `findings.<CODE>`, or `findings.<CODE>__<part>` when the params name a `part` of the finding, or `findings.<CODE>__warning` when a
 * warning reads differently from a blocker. A test keeps every code that the backend can send translated in English and Indonesian.
 */
export interface Finding {
  code: string
  message: string
  ref?: string
  params?: Record<string, string>
}

/** The values that are money and the ones that are quantities are written with the separators of the language. */
const MONEY = new Set(['ledger', 'book', 'value', 'ledger_value', 'balance_value', 'amount'])
const QTY = new Set(['qty', 'ledger_qty', 'balance_qty', 'short_qty', 'need', 'on_hand'])

export function findingText(f: Finding, severity: 'blocker' | 'warning' = 'blocker'): string {
  const part = f.params?.part
  const candidates = [severity === 'warning' ? `findings.${f.code}__warning` : '', part ? `findings.${f.code}__${part}` : '', `findings.${f.code}`]
  const key = candidates.find((k) => k !== '' && te(k))
  if (key === undefined) return f.message // a code this release does not know: the sentence of the server
  const params: Record<string, string> = {}
  for (const [k, v] of Object.entries(f.params ?? {})) params[k] = MONEY.has(k) ? formatMoney(v) : QTY.has(k) ? formatQty(v) : v
  if (f.ref && params.ref === undefined) params.ref = f.ref
  if (params.role !== undefined && te(`findings.role.${params.role}`)) params.role = t(`findings.role.${params.role}`)
  const cause = params.cause
  if (cause !== undefined && te(`findings.cause.${cause}`)) params.cause = t(`findings.cause.${cause}`, params)
  return t(key, params)
}
