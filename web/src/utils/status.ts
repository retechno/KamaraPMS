import { t, te } from '@/i18n'

/** The words for a status value (`CONFIRMED`) in the language of the page; a value without a translation is shown as it is. */
export function statusText(status: string | null | undefined): string {
  if (!status) return ''
  const key = `status.${status}`
  return te(key) ? t(key as never) : status
}
