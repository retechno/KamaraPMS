import { t, te } from '@/i18n'

/**
 * The words for a code the API sends (a status, a payment method, an audit action…) in the language of the page. The label of a code is
 * `<group>.<code>` in the language files (dots of a code become underscores: `payment.posted` is `auditAction.payment_posted`).
 *
 * A code without a label is never shown raw: it becomes readable text ("ALREADY_POSTED" → "Already posted", "payment.posted" → "Payment posted")
 * and a warning is logged once per code, so a code the backend added since is noticed and gets its label.
 */
export type LabelGroup = 'status' | 'payMethod' | 'roomChargeStatus' | 'auditAction' | 'auditEntity' | 'blockType'

const warned = new Set<string>()

/** "ALREADY_POSTED" → "Already posted"; "payment.posted" → "Payment posted"; "room_type" → "Room type". */
export function humanize(code: string): string {
  const words = code.replace(/[._]+/g, ' ').trim().toLowerCase()
  return words.charAt(0).toUpperCase() + words.slice(1)
}

export function labelOf(group: LabelGroup, code: string | null | undefined): string {
  if (code === null || code === undefined || code === '') return ''
  const key = `${group}.${code.replace(/\./g, '_')}`
  if (te(key)) return t(key as never)
  if (!warned.has(key)) {
    warned.add(key)
    console.warn(`[i18n] no label for ${key}`)
  }
  return humanize(code)
}

/** A cashier drawer's name: the usual "MAIN" is "Main drawer"; any other name the property typed is shown as typed (it is free text, not a code). */
export function drawerName(name: string): string {
  const key = `drawer.${name}`
  return te(key) ? t(key as never) : name
}
