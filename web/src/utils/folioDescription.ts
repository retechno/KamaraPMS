import { currentLocale, i18n, t } from '@/i18n'
import { formatBusinessDate } from './dates'

const MONTHS_EN = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

/**
 * The description of a folio line in the language of the page. The server stores it in English ("Room 209 - 12 Oct 2026", "Payment PAY000001 (BANK_TRANSFER)", "Transfer from FOL000001") and the stored
 * text never changes: only what is shown does (the same patterns as `Lang.FolioDescription` of the printed documents). A description people typed is shown as it was posted.
 */
export function folioDescription(desc: string): string {
  if (currentLocale() === 'en' || !desc) return desc
  let m = /^Transfer (from|to) (.+)$/.exec(desc)
  if (m) return t(m[1] === 'from' ? 'folioDesc.transferFrom' : 'folioDesc.transferTo', { folio: m[2] ?? '' })
  m = /^Reversal: (.+)$/.exec(desc)
  if (m) return t('folioDesc.reversal', { what: folioDescription(m[1] ?? '') })
  m = /^Adjustment: (.+)$/.exec(desc)
  if (m) return t('folioDesc.adjustment', { reason: m[1] ?? '' })
  m = /^Room (\S+) - (\d{1,2}) ([A-Z][a-z]{2}) (\d{4})$/.exec(desc)
  if (m) {
    const month = MONTHS_EN.indexOf(m[3] ?? '') + 1
    if (month > 0) return t('folioDesc.room', { room: m[1] ?? '', date: formatBusinessDate(`${m[4]}-${String(month).padStart(2, '0')}-${String(m[2]).padStart(2, '0')}`) })
  }
  m = /^(Payment|Refund) (\S+) \(([A-Z_]+)\)$/.exec(desc)
  if (m) {
    const key = `cashier.${m[3]}`
    const method = i18n.global.te(key) ? t(key as never) : (m[3] ?? '')
    return t(m[1] === 'Refund' ? 'folioDesc.refund' : 'folioDesc.payment', { number: m[2] ?? '', method })
  }
  return desc
}
