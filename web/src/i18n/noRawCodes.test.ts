import fs from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'

/**
 * A code of the API (BANK_TRANSFER, ALREADY_POSTED, payment.posted) must not reach the screen as text. These tests read the sources:
 * the words a template writes itself and the messages of the language files may not hold such a code. A code that reaches the screen
 * from data goes through `labelOf` / `statusText` (see src/i18n/labels.ts); that part is covered by the tests of each page.
 */
const SRC = path.resolve(__dirname, '..')
const CODE = /\b[A-Z]{2,}(?:_[A-Z0-9]{2,})+\b/

function vueFiles(dir: string): string[] {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = path.join(dir, e.name)
    if (e.isDirectory()) return vueFiles(p)
    return e.name.endsWith('.vue') ? [p] : []
  })
}

/** What a template says in its own words: the text between tags, without the interpolations and the attributes. */
function literalText(template: string): string[] {
  return template
    .replace(/\{\{[\s\S]*?\}\}/g, ' ')
    .replace(/<!--[\s\S]*?-->/g, ' ')
    .split(/<(?:[^>"']|"[^"]*"|'[^']*')*>/) // a tag, whose quoted attributes may hold a >, as in an arrow function
    .map((x) => x.trim())
    .filter(Boolean)
}

// Text that is a code on purpose: it is what the person types or compares to, not a label.
const ALLOWED_TEMPLATE_TEXT = new Set<string>([])

describe('no raw codes in what is written for people', () => {
  it('has no API code as the literal text of a template', () => {
    const hits: string[] = []
    for (const file of vueFiles(SRC)) {
      const m = /<template>([\s\S]*)<\/template>\s*(?:<style|$)/.exec(fs.readFileSync(file, 'utf8'))
      if (!m?.[1]) continue
      for (const text of literalText(m[1])) {
        if (CODE.test(text) && !ALLOWED_TEMPLATE_TEXT.has(text)) hits.push(`${path.relative(SRC, file)}: ${text}`)
      }
    }
    expect(hits).toEqual([])
  })

  it('has no API code in a message of the language files, except the ones that name a configuration setting', () => {
    const hits: string[] = []
    for (const lang of ['en', 'id']) {
      const source = fs.readFileSync(path.join(SRC, 'i18n', 'locales', `${lang}.ts`), 'utf8')
      for (const [, key, value] of source.matchAll(/^\s*(\w+): '((?:[^'\\]|\\.)*)',?$/gm)) {
        // a message that names an environment variable (PMS_SMTP_HOST) is for the administrator; the error code tables are keyed by code
        if (CODE.test(key ?? '')) continue
        if (/\bPMS_[A-Z_]+\b/.test(value ?? '')) continue
        if (CODE.test(value ?? '')) hits.push(`${lang}.${key}: ${value}`)
      }
    }
    expect(hits).toEqual([])
  })
})
