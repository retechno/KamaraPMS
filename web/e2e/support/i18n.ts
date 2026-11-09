import { id } from '../../src/i18n/locales/id'

/**
 * The Indonesian text of an i18n key, as the app shows it (the tests run with the locale `id`). A test that reads a label through its key keeps
 * working when the wording changes. Only `{name}` placeholders are filled; for a plural ("a | b") the last form is taken unless `form` says which.
 */
export function tx(key: string, params: Record<string, string | number> = {}, form?: number): string {
  let node: unknown = id
  for (const part of key.split('.')) {
    node = (node as Record<string, unknown> | undefined)?.[part]
    if (node === undefined) throw new Error(`i18n key not found: ${key}`)
  }
  if (typeof node !== 'string') throw new Error(`i18n key is not a message: ${key}`)
  const forms = node.split(' | ')
  let s = forms[form ?? forms.length - 1] ?? node
  s = s.replace(/\{'([^']*)'\}/g, '$1') // literal interpolation: {'@'}
  return s.replace(/\{(\w+)\}/g, (m, k: string) => (k in params ? String(params[k]) : m))
}

/** A regular expression for a message whose placeholders may hold anything (for a toast that names a number). */
export function txPattern(key: string): RegExp {
  const parts = tx(key).split(/\{\w+\}/).map((p) => p.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
  return new RegExp(parts.join('.+'))
}
