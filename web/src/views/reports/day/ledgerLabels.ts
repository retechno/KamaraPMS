import { t } from '@/i18n'

const FOLIO_TYPES = new Set(['GUEST', 'COMPANY', 'MASTER', 'OUTLET', 'EVENT'])

/** The kind of a folio in the language of the page; a kind this page does not know is shown as the server names it. */
export function folioType(code: string): string {
  return FOLIO_TYPES.has(code) ? t(`dayReports.ledger.folioType.${code}` as 'dayReports.ledger.folioType.GUEST') : code
}
