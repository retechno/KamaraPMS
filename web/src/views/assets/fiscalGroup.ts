import { t } from '@/i18n'
import { formatPercent } from '@/utils/format'

const KNOWN = new Set(['G1', 'G2', 'G3', 'G4', 'BLD_PERMANENT', 'BLD_NON_PERMANENT'])

/** A yearly rate from the API ("12.500") without its trailing zeros ("12.5"), so it reads "12,5%" in Indonesian and "12.5%" in English. */
export function ratePercent(value: string | number | null | undefined): string {
  if (value === null || value === undefined || value === '') return ''
  const text = String(value).trim()
  return formatPercent(/^-?\d+\.\d+$/.test(text) ? text.replace(/0+$/, '').replace(/\.$/, '') : text)
}

/** The name of a fiscal group ("Permanent building"); the code itself only when the group is not one of the standard ones. */
export function fiscalGroupName(code: string | null | undefined): string {
  if (!code || code === 'NONE') return '–'
  return KNOWN.has(code) ? t(`assets.fiscalGroup.${code}`) : code
}

/** The group with its yearly rate for the method of the asset ("Permanent building · 5%"), never the code. */
export function fiscalGroupLabel(code: string | null | undefined, rate?: string | number | null): string {
  const name = fiscalGroupName(code)
  const pct = ratePercent(rate)
  return pct && name !== '–' ? `${name} · ${pct}` : name
}
