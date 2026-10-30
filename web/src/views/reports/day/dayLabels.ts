import { t } from '@/i18n'

/** A payment method as the page names it; a code the page does not know is shown as it is. */
export function methodName(code: string): string {
  return code === '' ? '' : tryName(`dayReports.method.${code}`, code)
}

/** A reservation source as the page names it. */
export function sourceName(code: string): string {
  return code === '' ? '' : tryName(`dayReports.source.${code}`, code)
}

function tryName(path: string, fallback: string): string {
  const name = t(path as 'dayReports.method.CASH')
  return name === path ? fallback : name
}
