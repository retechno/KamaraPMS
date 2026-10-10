import { t, te } from '@/i18n'

/** The title of a line of a financial statement ("Net income") in the language of the page; a title without words in the language files is shown as the server wrote it. */
export function statementTitle(title: string): string {
  const key = `statementLine.${title.toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_|_$/g, '')}`
  return te(key) ? t(key as never) : title
}
